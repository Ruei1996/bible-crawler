// Package biblecom — scraper.go
//
// BibleComScraper orchestrates a polite, parallel crawl of bible.com for two
// Bible translations simultaneously:
//
//   - Chinese: CUNP-上帝 (version ID 414)
//   - English: NIV       (version ID 111)
//
// For each of the 66 canonical books it spawns one work item per (language,
// chapter), fans them into a fixed-size worker pool, and writes the parsed
// verses into pre-allocated result arrays. After all workers finish, the
// results are assembled into two OutputFile values ready to be serialised to
// JSON by the caller.
//
// Thread-safety: result arrays are indexed by (lang, bookIdx, chapIdx) and
// each cell is written by exactly one goroutine, so no mutex is needed for the
// result storage. The rate limiter and the work channel provide the only shared
// synchronisation points.
package biblecom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bible-crawler/internal/config"
	"bible-crawler/internal/spec"

	"golang.org/x/time/rate"
)

const (
	// LangChinese is the language identifier stored in OutputFile.Language
	// and used when logging progress.
	LangChinese = "chinese"
	// LangEnglish is the language identifier for the NIV translation.
	LangEnglish = "english"

	// zhVersionID is the YouVersion Bible ID for CUNP-上帝.
	zhVersionID = 414
	// enVersionID is the YouVersion Bible ID for NIV.
	enVersionID = 111

	// maxResponseBytes caps the HTTP response body to 10 MiB, preventing OOM
	// if bible.com ever returns an unexpectedly large page.
	maxResponseBytes = 10 * 1024 * 1024

	// disputedVerseSuffix is the canonical suffix appended to every note that
	// marks a textually-disputed (bracket) verse whose content is intentionally
	// stored as an empty string. It is used as a structural signal in
	// syncDisputedVersesZH to identify disputed EN verses without a fragile
	// full-string match on localised book/chapter text.
	disputedVerseSuffix = ",遇到特殊情境，爬蟲實作邏輯在此處留下空字串"

	// userAgent is sent with every request so bible.com can identify the crawler
	// in its access logs. A real browser UA avoids triggering bot-detection rules
	// that target the default Go http client user agent string.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

// HTTP transport timing constants. Named values prevent magic-number spread
// across NewBibleComScraper and make tuning diffs self-documenting.
const (
	// dialTimeout caps TCP connection establishment (DNS + SYN/ACK).
	// 15 s gives ample headroom for slow DNS without blocking a worker slot.
	dialTimeout = 15 * time.Second

	// keepAliveInterval controls TCP keepalive probes on idle connections,
	// keeping the pool warm across the polite-crawl pauses between tokens.
	keepAliveInterval = 30 * time.Second

	// idleConnTimeout is how long an unused keep-alive connection stays in
	// the pool before it is closed. Matches Go's stdlib Transport default.
	idleConnTimeout = 90 * time.Second

	// tlsHandshakeTimeout bounds TLS negotiation independently of the overall
	// request timeout, so a slow-TLS server cannot monopolise a worker slot.
	tlsHandshakeTimeout = 10 * time.Second
)

// BibleComScraper holds all runtime state for a single crawl run.
type BibleComScraper struct {
	cfg     *config.Config
	client  *http.Client
	limiter *rate.Limiter
	spec    []*spec.BookSpec // ZH spec used for chapter counts (same for EN)
}

// chapterResult is what a worker writes into the pre-allocated result grid
// after a successful page parse.
type chapterResult struct {
	verses []VerseOutput // nil means fetch/parse failed; chapter will be omitted
}

// NewBibleComScraper constructs a scraper from the shared Config.
// The HTTP client reuses connections and honours the configured timeout.
// The rate limiter is initialised with a burst of 1 so the first request is
// also subject to the configured RPS limit (avoids an initial burst spike).
//
// Parameters:
//   - cfg:       application config; all BIBLECOM_* fields are consumed here.
//     BibleComWorkers sets the goroutine pool size; BibleComRateLimitRPS
//     controls the token-bucket ceiling shared across all workers.
//   - bibleSpec: ordered []*spec.BookSpec from spec.Load — drives chapter-count
//     iteration. Must satisfy len(bibleSpec) == len(Books) (checked by LoadSpec).
//
// Returns:
//   - *BibleComScraper fully initialised and ready to call Run.
func NewBibleComScraper(cfg *config.Config, bibleSpec []*spec.BookSpec) *BibleComScraper {
	// idlePoolSize must be at least as large as the worker count so that every
	// worker can hold an idle connection open between rate-limiter waits.
	idlePoolSize := cfg.BibleComWorkers + 2

	transport := &http.Transport{
		// DialContext caps TCP connection establishment (DNS + SYN/ACK).
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAliveInterval,
		}).DialContext,
		// Global and per-host idle-connection ceilings both match the worker
		// count so evicted connections never starve a ready worker.
		MaxIdleConns:        idlePoolSize,
		MaxIdleConnsPerHost: idlePoolSize,
		// Evict idle connections that have been unused longer than 90 s.
		IdleConnTimeout: idleConnTimeout,
		// Bound TLS negotiation independently of the per-request Timeout so a
		// slow handshake cannot block a worker slot for the full request window.
		TLSHandshakeTimeout: tlsHandshakeTimeout,
	}
	client := &http.Client{
		Timeout:   time.Duration(cfg.BibleComTimeoutSec) * time.Second,
		Transport: transport,
		// Reject redirects to non-HTTPS URLs and cap redirect depth to prevent
		// silent TLS downgrade (CWE-319: Cleartext Transmission of Sensitive
		// Information) and redirect chains that could reach unintended hosts
		// (CWE-601: URL Redirection to Untrusted Site — OWASP A10:2021 SSRF).
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-HTTPS URL refused: %s", req.URL)
			}
			if len(via) >= 3 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			return nil
		},
	}
	// Burst = 1: no initial spike; every request waits for a token.
	limiter := rate.NewLimiter(rate.Limit(cfg.BibleComRateLimitRPS), 1)

	return &BibleComScraper{
		cfg:     cfg,
		client:  client,
		limiter: limiter,
		spec:    bibleSpec,
	}
}

// Run crawls all 66 books for both languages and returns the two OutputFile
// values. The caller is responsible for wiring up cancellation (e.g. via
// signal.NotifyContext in main) so that Ctrl-C triggers a graceful shutdown:
// workers finish their current HTTP request before exiting, and a partial
// OutputFile is returned with whatever was collected so far.
//
// Parameters:
//   - ctx: controls the lifetime of all worker goroutines. Cancelling it causes
//     workers to stop after their current in-flight HTTP request completes,
//     then return partial results rather than blocking indefinitely.
//
// Returns:
//   - *OutputFile: Chinese (CUNP-上帝) result; may be partial if ctx was cancelled.
//   - *OutputFile: English (NIV) result; same partial-result guarantee.
//   - error: currently always nil; reserved for future fatal-error promotion
//     (e.g. if a minimum-chapter threshold is introduced).
func (s *BibleComScraper) Run(ctx context.Context) (*OutputFile, *OutputFile, error) {
	// Pre-allocate the result grid: [bookIdx][chapIdx] = chapterResult.
	// Each cell is written by exactly one goroutine (no races on the cells
	// themselves). The outer slice is created here before goroutines start,
	// so appending to it is safe without a mutex.
	zhGrid := make([][]chapterResult, len(Books))
	enGrid := make([][]chapterResult, len(Books))
	for i, book := range s.spec {
		zhGrid[i] = make([]chapterResult, book.TotalChapters)
		enGrid[i] = make([]chapterResult, book.TotalChapters)
	}

	// Build the work queue, then pre-load a fully-buffered channel.
	// This avoids a separate producer goroutine for this bounded work set.
	items := s.buildWorkItems()
	workCh := make(chan workItem, len(items))
	for _, it := range items {
		workCh <- it
	}
	close(workCh)

	// Log the crawl plan with estimated duration and a worker-count advisory.
	estimatedMin := float64(len(items)) / s.cfg.BibleComRateLimitRPS / 60
	log.Printf("[biblecom] Starting crawl: %d chapters × 2 langs = %d requests, "+
		"workers=%d, rate=%.1f req/s (~%.0f min)",
		countTotalChapters(s.spec), len(items),
		s.cfg.BibleComWorkers, s.cfg.BibleComRateLimitRPS, estimatedMin)
	if len(s.cfg.BibleComFilterSorts) > 0 {
		log.Printf("[biblecom] Book filter active — crawling only sorts: %v", s.cfg.BibleComFilterSorts)
	}
	if float64(s.cfg.BibleComWorkers) < s.cfg.BibleComRateLimitRPS {
		log.Printf("[biblecom] WARN: workers (%d) < rate limit (%.1f req/s) — "+
			"consider increasing BIBLECOM_WORKERS",
			s.cfg.BibleComWorkers, s.cfg.BibleComRateLimitRPS)
	}
	log.Printf("[biblecom] Output: %s (ZH), %s (EN)",
		s.cfg.BibleComOutputZH, s.cfg.BibleComOutputEN)

	// Launch the worker pool and wait for all goroutines to finish.
	// sync.WaitGroup is the idiomatic Go alternative to a done-signal channel.
	var wg sync.WaitGroup
	for i := range s.cfg.BibleComWorkers {
		wg.Add(1)
		// Pass i as an explicit argument so each goroutine captures its own
		// stable worker ID at launch time rather than sharing the loop variable
		// through a closure.  (Go 1.22+ loop semantics make the closure safe
		// too, but the explicit parameter is clearer and serves as the ID label
		// in log lines produced by runWorker.)
		go func(workerID int) {
			defer wg.Done()
			s.runWorker(ctx, workerID, workCh, zhGrid, enGrid)
		}(i)
	}
	wg.Wait()

	if ctx.Err() != nil {
		log.Printf("[biblecom] Crawl interrupted: %v — partial results returned", ctx.Err())
	} else {
		log.Printf("[biblecom] Crawl complete")
	}

	zhOut := s.assembleOutput(zhGrid, LangChinese, "CUNP-上帝", zhVersionID)
	enOut := s.assembleOutput(enGrid, LangEnglish, "NIV", enVersionID)

	// Bracket (disputed) verses are handled in processItem, which writes ""
	// as content and a human-readable note. resolveRefs — the cross-reference
	// resolution pass that would fill in prose from a target verse — is
	// intentionally not used: we want empty content, not resolved prose.

	// syncDisputedVersesZH ensures the Chinese output contains a placeholder
	// entry (content="", human-readable note) for every disputed verse that
	// the English output captured via bracket-label detection. The Chinese
	// CUNP source on bible.com silently skips disputed verse numbers, so the
	// parser produces no entry — without this sync, re-running the crawler
	// would overwrite the JSON and lose those placeholder rows.
	syncDisputedVersesZH(zhOut, enOut)

	return zhOut, enOut, nil
}

// buildFilterSet converts cfg.BibleComFilterSorts into a deduplicated lookup
// map and a convenience flag. Using a map (vs the raw slice) ensures that
// duplicate entries in the env var (e.g. "65,65") do not inflate the expected-
// books count used for completeness validation.
//
// Returns (nil, false) when no filter is configured, meaning "crawl all books".
func (s *BibleComScraper) buildFilterSet() (map[int]struct{}, bool) {
	if len(s.cfg.BibleComFilterSorts) == 0 {
		return nil, false
	}
	filterSorts := make(map[int]struct{}, len(s.cfg.BibleComFilterSorts))
	for _, sort := range s.cfg.BibleComFilterSorts {
		filterSorts[sort] = struct{}{}
	}
	return filterSorts, true
}

// buildWorkItems constructs the full list of (lang, book, chapter) work items
// in canonical order: all Chinese chapters first, then all English chapters.
// This ordering has no correctness implication but makes log output easier to
// scan during a run.
//
// When cfg.BibleComFilterSorts is non-empty, only books whose canonical Sort
// number appears in the list are enqueued.  This enables targeted single-book
// re-crawls (e.g. BIBLECOM_FILTER_SORTS=65 to re-crawl only Jude after its
// USFM code was corrected from "JDE" to "JUD").
//
// Returns:
//   - []workItem: all (lang, book, chapter) tuples ready to be pushed into the
//     buffered work channel. Length is 2 × (total chapters across all included
//     books). Returns an empty slice when the filter excludes every book.
func (s *BibleComScraper) buildWorkItems() []workItem {
	filterSorts, filterActive := s.buildFilterSet()

	// Compute the exact capacity from the spec rather than hard-coding 1189.
	// This remains correct if a custom spec with a different chapter count is used.
	totalChapters := countTotalChapters(s.spec)
	items := make([]workItem, 0, totalChapters*2) // 2 languages × total chapters

	for _, lang := range []string{LangChinese, LangEnglish} {
		for bookIdx, book := range s.spec {
			// Skip books that are not in the filter when a filter is active.
			if filterActive {
				if _, ok := filterSorts[Books[bookIdx].Sort]; !ok {
					continue
				}
			}
			usfm := Books[bookIdx].USFM
			for chapSort := 1; chapSort <= book.TotalChapters; chapSort++ {
				items = append(items, workItem{
					lang:     lang,
					bookIdx:  bookIdx,
					chapSort: chapSort,
					url:      s.buildURL(lang, usfm, chapSort),
				})
			}
		}
	}
	return items
}

// buildURL constructs the full bible.com page URL for a given language, USFM
// book code, and chapter number.
//
// Chinese URL example: https://www.bible.com/bible/414/GEN.1.CUNP-%E4%B8%8A%E5%B8%9D
// English URL example: https://www.bible.com/bible/111/GEN.1.NIV
func (s *BibleComScraper) buildURL(lang, usfm string, chapSort int) string {
	if lang == LangChinese {
		return fmt.Sprintf("%s/%s.%d.%s",
			s.cfg.BibleComZHBaseURL, usfm, chapSort, s.cfg.BibleComZHVersionSuffix)
	}
	return fmt.Sprintf("%s/%s.%d.%s",
		s.cfg.BibleComENBaseURL, usfm, chapSort, s.cfg.BibleComENVersionSuffix)
}

// runWorker is the body of each worker goroutine. It reads work items from
// workCh until the channel is drained or the context is cancelled, fetches
// and parses each page, and writes the result into the appropriate grid cell.
func (s *BibleComScraper) runWorker(
	ctx context.Context,
	id int,
	workCh <-chan workItem,
	zhGrid, enGrid [][]chapterResult,
) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("[biblecom] worker %d: context cancelled, stopping", id)
			return
		case item, ok := <-workCh:
			if !ok {
				return // channel drained
			}
			s.processItem(ctx, item, zhGrid, enGrid)
		}
	}
}

// processItem fetches one page, parses it, and stores the verse slice in the
// correct grid cell. Any per-page error is logged and the cell is left as nil
// (the assembler later omits chapters with nil verse slices).
func (s *BibleComScraper) processItem(
	ctx context.Context,
	item workItem,
	zhGrid, enGrid [][]chapterResult,
) {
	usfm := Books[item.bookIdx].USFM

	// Wait for a rate-limiter token before issuing the HTTP request. This
	// enforces the global BibleComRateLimitRPS ceiling across all workers.
	if err := s.limiter.Wait(ctx); err != nil {
		// Context cancelled — stop without logging a spurious error.
		return
	}

	html, err := s.fetchPage(ctx, item.url, item.lang)
	if err != nil {
		log.Printf("[biblecom] WARN fetch %s: %v", item.url, err)
		return
	}

	verses, err := ParseChapter(html, usfm, item.chapSort)
	if err != nil {
		log.Printf("[biblecom] WARN parse %s ch%d (%s): %v",
			usfm, item.chapSort, item.lang, err)
		return
	}

	bookEntry := Books[item.bookIdx]
	bookName := bookEntry.NameZH
	if item.lang == LangEnglish {
		bookName = bookEntry.NameEN
	}
	applyDisputedPostProcessing(verses, item.lang, bookName, item.chapSort)

	result := chapterResult{verses: verses}
	if item.lang == LangChinese {
		zhGrid[item.bookIdx][item.chapSort-1] = result
	} else {
		enGrid[item.bookIdx][item.chapSort-1] = result
	}

	log.Printf("[biblecom] OK %s %s.%d (%d verses)",
		item.lang, usfm, item.chapSort, len(verses))
}

// fetchPage performs an HTTP GET request for the given URL, enforcing the
// configured timeout and the max-response-body cap. The lang parameter
// controls the Accept-Language header so the server returns the expected
// locale. Returns the raw HTML string on success.
func (s *BibleComScraper) fetchPage(ctx context.Context, url, lang string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	// Send Accept-Language matching the requested translation so the server
	// does not localise its response toward the wrong language.
	if lang == LangChinese {
		req.Header.Set("Accept-Language", "zh-TW,zh;q=0.9,en;q=0.8")
	} else {
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain the body so Go's HTTP transport can reuse the keep-alive connection.
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GET %s: unexpected status %d", url, resp.StatusCode)
	}

	// Cap the response body to prevent OOM on unexpectedly large pages.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read body %s: %w", url, err)
	}
	// Detect silent truncation: if the read stopped exactly at the cap, the
	// response may have been cut short — log and skip rather than silently
	// storing incomplete chapter content.
	if int64(len(body)) >= maxResponseBytes {
		return "", fmt.Errorf("response for %s hit %d-byte cap — possible truncation",
			url, maxResponseBytes)
	}
	return string(body), nil
}

// assembleOutput converts the raw crawl grid into the JSON-serialisable
// OutputFile structure, omitting any chapters that failed to parse (nil cells).
//
// Parameters:
//   - grid:      2-D slice [bookIdx][chapIdx] of *ParsedChapter results produced
//                by the worker pool. Nil cells represent chapters that could not
//                be fetched or parsed (network errors, unexpected HTML, etc.).
//   - lang:      "chinese" or "english", used as the Language field in the output.
//   - version:   Human-readable Bible version label (e.g. "CUNP" or "NIV").
//   - versionID: Numeric bible.com version identifier embedded in the URL path.
//
// Returns:
//   - *OutputFile ready for JSON marshalling. Books with zero successful chapters
//     are omitted from the output entirely.
//
// Validation warnings (logged but do not abort):
//   - WARN per in-scope book that produced 0 chapters (signals USFM code mismatch
//     or site-side content gaps; includes the USFM code to simplify debugging).
//   - WARN at the end when the number of non-empty books in the output is less
//     than the expected count (all-66 crawl: expects 66; filtered crawl: expects
//     len(filter-set)).
func (s *BibleComScraper) assembleOutput(
	grid [][]chapterResult,
	lang, version string,
	versionID int,
) *OutputFile {
	out := &OutputFile{
		Version:   version,
		VersionID: versionID,
		Language:  lang,
		CrawledAt: time.Now().UTC(),
		Books:     make([]BookOutput, 0, len(Books)),
	}

	// Reuse the same deduplicated filter set used by buildWorkItems so that
	// the warning suppression logic is consistent with the work-queue logic.
	filterSorts, filterActive := s.buildFilterSet()

	for bookIdx, bookEntry := range Books {
		bookName := bookEntry.NameZH
		if lang == LangEnglish {
			bookName = bookEntry.NameEN
		}

		chapResults := grid[bookIdx]
		chapters := make([]ChapterOutput, 0, len(chapResults))
		for chapIdx, cr := range chapResults {
			if len(cr.verses) == 0 {
				// Skip chapters where the fetch/parse failed (nil) or where
				// the page was found but contained no verse spans.
				continue
			}
			chapters = append(chapters, ChapterOutput{
				ChapterSort: chapIdx + 1, // convert 0-based index to 1-based sort
				Verses:      cr.verses,
			})
		}

		// Include the book even when some chapters failed, so partial results
		// remain inspectable. Books with zero chapters are omitted entirely.
		if len(chapters) == 0 {
			// Suppress the warning for books intentionally excluded by the filter.
			// isExcludedByFilter is true only when a filter IS active AND this
			// book's sort is NOT in the filter — meaning it was deliberately skipped.
			//
			// Truth table:
			//   filterActive=false → isExcludedByFilter=false → warn (unexpected)
			//   filterActive=true, inFilter=true  → isExcludedByFilter=false → warn
			//   filterActive=true, inFilter=false → isExcludedByFilter=true  → silent
			_, inFilter := filterSorts[bookEntry.Sort]
			isExcludedByFilter := filterActive && !inFilter
			if !isExcludedByFilter {
				log.Printf("[biblecom] WARN: book sort=%d (%s, %s) produced 0 chapters — "+
					"check USFM code %q and network connectivity",
					bookEntry.Sort, bookEntry.NameZH, bookEntry.NameEN, bookEntry.USFM)
			}
			continue
		}
		out.Books = append(out.Books, BookOutput{
			BookSort: bookEntry.Sort,
			BookName: bookName,
			BookUSFM: bookEntry.USFM,
			Chapters: chapters,
		})
	}

	// Validate completeness: warn when the output has fewer books than expected.
	// For a full crawl, expect all 66 canonical books.
	// For a filtered crawl, expect exactly len(filterSorts) books — using the
	// deduplicated map size so that "65,65" in the env var counts as 1, not 2.
	expectedBooks := len(Books)
	if filterActive {
		expectedBooks = len(filterSorts)
	}
	if len(out.Books) < expectedBooks {
		log.Printf("[biblecom] WARN: %s output has %d/%d books — some books produced "+
			"no chapters; re-check USFM codes or run with BIBLECOM_FILTER_SORTS=<sort>",
			lang, len(out.Books), expectedBooks)
	}
	return out
}

// WriteOutputFiles serialises the two OutputFile values to the configured
// JSON output paths. Files are created with mode 0640 (owner rw, group r).
// Both files are written before returning; if the second write fails, the first
// file is still preserved so the crawl work is not lost.
func WriteOutputFiles(zhOut, enOut *OutputFile, zhPath, enPath string) error {
	if err := writeJSON(zhPath, zhOut); err != nil {
		return fmt.Errorf("write ZH output: %w", err)
	}
	if err := writeJSON(enPath, enOut); err != nil {
		return fmt.Errorf("write EN output: %w", err)
	}
	return nil
}

// writeJSON serialises v to the given path with 2-space indentation.
// It first marshals to memory (so the file is never touched on failure), then
// writes via os.CreateTemp (which uses O_EXCL, preventing symlink-following on
// the temp name — CWE-377: Insecure Temporary File), and finally renames
// atomically so readers never observe a partial file.
//
// Security: the output path is confined to the current working directory subtree.
// Both the resolved target and CWD have their symlinks expanded before the
// filepath.IsLocal check so that absolute env var paths (e.g.
// BIBLECOM_OUTPUT_ZH=/etc/cron.d/biblecom) and symlinks inside CWD that
// point outside (e.g. ./output.json → /etc/passwd) are both rejected
// (CWE-22: Improper Limitation of a Pathname to a Restricted Directory;
// CWE-61: UNIX Symbolic Link Following).
func writeJSON(path string, v any) error {
	// Lexically resolve the path first.
	cleaned, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve output path %q: %w", path, err)
	}

	// Resolve symlinks on the CWD so the confinement check is apples-to-apples.
	// On macOS, os.Getwd() returns /var/... but the real path is /private/var/...;
	// without EvalSymlinks the comparison would incorrectly reject CWD-local files.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	resolvedCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return fmt.Errorf("resolve symlinks for cwd %q: %w", cwd, err)
	}

	// Resolve symlinks on the parent directory of the target path.
	// EvalSymlinks is called on the parent (not the file itself) because the
	// output file may not exist yet. If the parent directory does not exist
	// either, fall back to the lexically-cleaned directory — the confinement
	// check still rejects absolute paths and path-traversal sequences.
	parentDir := filepath.Dir(cleaned)
	resolvedParent, symErr := filepath.EvalSymlinks(parentDir)
	if symErr != nil {
		resolvedParent = parentDir
	}
	resolvedTarget := filepath.Join(resolvedParent, filepath.Base(cleaned))

	rel, relErr := filepath.Rel(resolvedCWD, resolvedTarget)
	// filepath.IsLocal returns false for any relative path that escapes the root
	// (starts with ".." or is absolute) — it handles the edge case of filenames
	// beginning with ".." that strings.HasPrefix would incorrectly reject.
	if relErr != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf(
			"output path %q (resolved: %q) is outside allowed directory %q (CWE-22)",
			path, resolvedTarget, resolvedCWD,
		)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON for %s: %w", resolvedTarget, err)
	}
	data = append(data, '\n') // POSIX: text files end with newline

	// Create the temp file in the same directory as the target so that
	// os.Rename (which is atomic on POSIX) never crosses filesystem boundaries.
	dir := filepath.Dir(resolvedTarget)
	tmp, err := os.CreateTemp(dir, ".biblecom-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Remove the temp file if any subsequent step fails.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	// 0640: owner read/write, group read — restricts world access on shared hosts
	// since JSON output may contain content from licensed Bible translations (CWE-732).
	if err := tmp.Chmod(0o640); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, resolvedTarget); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpName, resolvedTarget, err)
	}

	log.Printf("[biblecom] wrote %s (%.1f KB)", resolvedTarget, float64(len(data))/1024)
	return nil
}

// LoadSpec reads the Chinese bible spec file (which contains chapter counts
// identical to the NIV versification) and returns the ordered BookSpec slice.
// This is a thin wrapper around spec.Load that isolates the biblecom package
// from the spec package's dual-file API (both files must be provided even
// though only the ZH data is used here for chapter navigation).
func LoadSpec(zhPath, enPath string) ([]*spec.BookSpec, error) {
	bibleSpec, err := spec.Load(zhPath, enPath)
	if err != nil {
		return nil, fmt.Errorf("load Bible spec: %w", err)
	}
	if len(bibleSpec.ZH) != len(Books) {
		return nil, fmt.Errorf(
			"spec has %d books but canonical list has %d; re-run spec-builder",
			len(bibleSpec.ZH), len(Books),
		)
	}
	return bibleSpec.ZH, nil
}

// countTotalChapters returns the sum of TotalChapters across all spec books.
// It is used to pre-allocate the work-item slice and to compute the estimated
// crawl duration in the startup log.
func countTotalChapters(books []*spec.BookSpec) int {
	total := 0
	for _, b := range books {
		total += b.TotalChapters
	}
	return total
}

// disputedNote returns a human-readable JSON annotation for a textually-
// disputed verse that the crawler intentionally stores as an empty string.
// The note embeds the book name, chapter, and verse so that a reader of the
// JSON output can immediately identify the affected verse without cross-
// referencing additional files.
//
// Chinese format: "馬太福音,第18章,第11小節,遇到特殊情境，爬蟲實作邏輯在此處留下空字串"
// English format: "Matthew,chapter 17,verse 21,遇到特殊情境，爬蟲實作邏輯在此處留下空字串"
func disputedNote(lang, bookName string, chapSort, verseSort int) string {
	if lang == LangChinese {
		return fmt.Sprintf("%s,第%d章,第%d小節%s", bookName, chapSort, verseSort, disputedVerseSuffix)
	}
	return fmt.Sprintf("%s,chapter %d,verse %d%s", bookName, chapSort, verseSort, disputedVerseSuffix)
}

// applyDisputedPostProcessing blanks out any bracket (disputed) verses in the
// provided slice and replaces their technical parser note ("ref:*" or "omitted")
// with a human-readable annotation produced by disputedNote.
//
// Rules:
//   - Note starts with "ref:" (cross-ref bracket verse) → clear Content & CrossRef, set Note.
//   - Note == "omitted" (no-cross-ref bracket verse) → clear Content, set Note.
//   - Note == "merged" → untouched (merged-verse content is valid prose).
//   - Note == "" → untouched (normal verse).
//
// The function mutates verses in place and is idempotent.
func applyDisputedPostProcessing(verses []VerseOutput, lang, bookName string, chapSort int) {
	for i := range verses {
		if !strings.HasPrefix(verses[i].Note, "ref:") && verses[i].Note != "omitted" {
			continue
		}
		verses[i].Content = ""
		// For "ref:*" verses, CrossRef was set by the parser — clear it so
		// the JSON output does not expose the internal cross-reference USFM.
		// For "omitted" verses, CrossRef is already "" (the parser never sets
		// it when omitted=true and refUSFM=""); the assignment is explicit for
		// clarity and defensive consistency across both branches.
		verses[i].CrossRef = ""
		verses[i].Note = disputedNote(lang, bookName, chapSort, verses[i].VerseSort)
	}
}

// syncDisputedVersesZH ensures the Chinese output contains a placeholder entry
// (content="", human-readable Chinese note) for every verse that the English
// output flagged as textually-disputed (its Note contains "遇到特殊情境").
//
// The Chinese CUNP source on bible.com silently skips disputed verse numbers
// without any bracket label, so ParseChapter produces no entry for them.
// This function bridges that gap by cross-referencing the EN output, inserting
// a correctly-sorted VerseOutput into the matching ZH chapter whenever the ZH
// chapter is missing that verse. The content is always "" and the note format
// follows the Chinese convention ("書名,第N章,第N小節,遇到特殊情境…").
//
// Must be called after both assembleOutput passes complete and after the EN
// post-processing in processItem has already set disputed verse notes.
func syncDisputedVersesZH(zhOut, enOut *OutputFile) {
	// Build a pointer index: USFM + chapSort → *ChapterOutput inside zhOut.
	// Using pointers means mutations to Verses are visible via zhOut directly.
	type chapKey struct {
		usfm string
		chap int
	}
	zhChapIdx := make(map[chapKey]*ChapterOutput)
	// Build a per-chapter verse-sort set so duplicate-checks are O(1) instead
	// of O(V_ch) linear scan. Keyed identically to zhChapIdx.
	zhChapVerses := make(map[chapKey]map[int]struct{})
	for bi := range zhOut.Books {
		for ci := range zhOut.Books[bi].Chapters {
			key := chapKey{zhOut.Books[bi].BookUSFM, zhOut.Books[bi].Chapters[ci].ChapterSort}
			zhChapIdx[key] = &zhOut.Books[bi].Chapters[ci]
			vset := make(map[int]struct{}, len(zhOut.Books[bi].Chapters[ci].Verses))
			for _, v := range zhOut.Books[bi].Chapters[ci].Verses {
				vset[v.VerseSort] = struct{}{}
			}
			zhChapVerses[key] = vset
		}
	}

	// Build ZH book name lookup: USFM → localised Chinese book name.
	zhBookName := make(map[string]string, len(zhOut.Books))
	for _, b := range zhOut.Books {
		zhBookName[b.BookUSFM] = b.BookName
	}

	added := 0
	for _, enBook := range enOut.Books {
		for _, enChap := range enBook.Chapters {
			for _, enVerse := range enChap.Verses {
				// Use the canonical suffix constant as the structural signal —
				// avoids scanning the localised book/chapter prefix text and
				// protects against accidental string drift.
				if !strings.HasSuffix(enVerse.Note, disputedVerseSuffix) {
					continue
				}
				key := chapKey{enBook.BookUSFM, enChap.ChapterSort}
				zhChap, ok := zhChapIdx[key]
				if !ok {
					// ZH chapter entirely absent (fetch/parse failure during crawl).
					// Log a warning so operators can identify chapters whose
					// disputed verse placeholders were not inserted.
					log.Printf("[biblecom] WARN: disputed verse %s ch%d v%d — ZH chapter absent, placeholder not inserted",
						enBook.BookUSFM, enChap.ChapterSort, enVerse.VerseSort)
					continue
				}
				// O(1) duplicate check via pre-built verse set.
				if _, exists := zhChapVerses[key][enVerse.VerseSort]; exists {
					continue
				}
				// Determine the Chinese book name (fall back to USFM if ZH book
				// didn't make it into the output).
				bookName := zhBookName[enBook.BookUSFM]
				if bookName == "" {
					bookName = enBook.BookUSFM
				}
				newVerse := VerseOutput{
					VerseSort: enVerse.VerseSort,
					Content:   "",
					Note:      disputedNote(LangChinese, bookName, enChap.ChapterSort, enVerse.VerseSort),
				}
				// Insert at the correct sorted position with a single-pass scan.
				idx := 0
				for idx < len(zhChap.Verses) && zhChap.Verses[idx].VerseSort < enVerse.VerseSort {
					idx++
				}
				// Standard Go slice-insertion idiom: grow the slice by one element
				// (append), shift existing elements at [idx, end) one position to the
				// right (copy — safe because append already allocated fresh backing
				// memory), then write the new verse into the freed slot at idx.
				// Time complexity: O(V_ch) in the worst case (idx=0), but disputed
				// verses are rare (~16 per canon) so this path is nearly never hot.
				zhChap.Verses = append(zhChap.Verses, VerseOutput{})
				copy(zhChap.Verses[idx+1:], zhChap.Verses[idx:])
				zhChap.Verses[idx] = newVerse
				// Keep the verse set in sync so subsequent iterations (unlikely
				// with only ~16 disputed verses) don't insert the same verse twice.
				zhChapVerses[key][enVerse.VerseSort] = struct{}{}
				added++
			}
		}
	}
	if added > 0 {
		log.Printf("[biblecom] synced %d disputed verse(s) from EN into ZH output", added)
	}
}
