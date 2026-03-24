// Package youversion provides the YouVersion Platform API client, response
// types, and the YouVersionScraper that replaces the HTML-based Bible crawler.
//
// YouVersionScraper fetches Bible content through the YouVersion REST API
// (https://api.youversion.com/v1) and persists it to the same PostgreSQL
// schema used by the original springbible.fhl.net HTML scraper.
//
// Two Bible versions are used by default:
//
//	English: NIV 2011 (ID 111) — freely accessible
//	Chinese: CSB 中文標準譯本 (ID 312) — Chinese Standard Bible, traditional Chinese, freely accessible
//
// To use a different Chinese translation, set
// YOUVERSION_CHINESE_BIBLE_ID=<id> in your .env file.
//
// # Parallel Mode (required)
//
// YouVersionScraper always operates in parallel mode:
//   - Phase 1 (setupBooks): fetches book/title metadata and writes it to the DB.
//   - Phase 2 (crawlVersesParallel): fetches verse text using N worker goroutines
//     with rate limiting and exponential-backoff retry, then appends each verse
//     as a JSON line to a JSONL checkpoint file. No verse DB writes happen here.
//
// After the crawler finishes, run cmd/youversion-importer to batch-write the
// JSONL checkpoint file into PostgreSQL.
//
// # sub_title Field
//
// The YouVersion Platform API v1 does not expose pericope headings or section
// sub-titles (e.g. "The Creation", "第一節"). The bibles.bible_section_contents
// sub_title column is left empty for all YouVersion-sourced records.
// If sub-titles are needed, they must be sourced from a different API or data set.
package youversion

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	backoff "github.com/cenkalti/backoff/v4"
	"golang.org/x/time/rate"

	"bible-crawler/internal/repository"

	"github.com/google/uuid"
)

const (
	// LangChinese is the language key stored in DB content rows for Chinese text.
	LangChinese = "chinese"
	// LangEnglish is the language key stored in DB content rows for English text.
	LangEnglish = "english"

	// workChannelMultiplier scales the work channel buffer relative to the worker
	// count. A buffer of workers×2 gives each worker one item of look-ahead while
	// keeping peak memory O(workers) instead of O(total verses). This replaces the
	// prior O(n) channel buffer that duplicated the entire work slice in memory.
	workChannelMultiplier = 2
)

// BibleAPIClient is the subset of the YouVersion Platform API required by
// YouVersionScraper. Using an interface (rather than the concrete *Client)
// allows unit tests to inject a mock without starting an HTTP server.
type BibleAPIClient interface {
	// GetBooks returns all books (with chapter and verse structure) for the
	// given Bible version ID.
	GetBooks(ctx context.Context, bibleID int) (*BooksResponse, error)
	// GetPassage returns the text content for the given USFM passage ID
	// (e.g. "GEN.1.1", "GEN.1.1-3", "GEN.1") from the given Bible version.
	GetPassage(ctx context.Context, bibleID int, passageID string) (*PassageData, error)
}

// errVerseNotFound is returned by fetchWithRetry when the API responds with
// HTTP 404 for a passage. 404s are expected for certain verses that modern
// translations (e.g. NIV) omit. Workers treat this as a permanent skip.
var errVerseNotFound = errors.New("verse not found (404)")

// ErrPhase2WriteFailures is a sentinel returned by RunWithContext when one or
// more checkpoint write errors occur during Phase 2. The crawler still writes
// all successful verses; the error gives the operator a non-zero exit code
// signal to investigate the count logged in Phase 2's summary line.
var ErrPhase2WriteFailures = errors.New("one or more checkpoint write errors in phase 2")

const (
	// maxWorkers is the hard upper cap on the parallel worker count.
	// Beyond this value, the token-bucket rate limiter dominates throughput
	// and additional goroutines only add scheduling overhead.
	maxWorkers = 200
)

// verseWork is one unit of parallel crawl work — a single verse to fetch.
type verseWork struct {
	lang      string
	bibleID   int
	bookSort  int // 1-based
	chapSort  int // 1-based
	verseSort int // actual verse number from API (e.g. 14, not index)
	passageID string // USFM passage ID, e.g. "GEN.1.1"
}

// YouVersionScraper crawls Bible content via the YouVersion Platform API and
// persists it to the same PostgreSQL schema used by the HTML scraper.
//
// Phase 1 – setupBooks: fetches book lists for both languages and writes
// book rows plus localized titles to the database.
//
// Phase 2 – crawlVersesParallel: for every book/chapter/verse returned by the
// API, fetches passage text using N worker goroutines with rate limiting and
// exponential-backoff retry, and appends each result to the JSONL Checkpoint
// file. The separate cmd/youversion-importer then batch-writes the file to DB.
//
// All fields are required. Use NewYouVersionScraper to build the base struct,
// then set Checkpoint, Workers, RateLimitRPS, MaxRetries, and RetryBaseMS
// before calling Run/RunWithContext.
type YouVersionScraper struct {
	Repo           *repository.BibleRepository
	Client         BibleAPIClient
	ChineseBibleID int
	EnglishBibleID int

	// Parallel-mode fields — all must be set before calling Run/RunWithContext.
	Checkpoint   *Checkpoint // JSONL progress file; non-nil required
	Workers      int         // parallel worker count (>0 required)
	RateLimitRPS float64     // token-bucket rate limit in req/s (>0 required)
	MaxRetries   int         // max exponential-backoff retries (0 → 5)
	RetryBaseMS  int         // backoff initial interval in ms (0 → 1000)
}

// NewYouVersionScraper returns a YouVersionScraper configured with the given
// repository, API client, and per-language Bible version IDs.
func NewYouVersionScraper(
	repo *repository.BibleRepository,
	client BibleAPIClient,
	chineseBibleID, englishBibleID int,
) *YouVersionScraper {
	return &YouVersionScraper{
		Repo:           repo,
		Client:         client,
		ChineseBibleID: chineseBibleID,
		EnglishBibleID: englishBibleID,
	}
}

// bookSetup bundles the DB-assigned book UUIDs with the API-sourced
// chapter/verse structure so Phase 2 does not need to re-call GetBooks.
type bookSetup struct {
	metas   []bookMeta
	enBooks []BookData
	zhBooks []BookData
}

// bookMeta holds the DB UUID and 0-based canonical position for one Bible book.
type bookMeta struct {
	id    uuid.UUID
	index int
}

// Run executes both crawler phases using a background context.
// Equivalent to RunWithContext(context.Background()).
func (s *YouVersionScraper) Run() error {
	return s.RunWithContext(context.Background())
}

// RunWithContext executes both crawler phases. ctx is used for graceful
// shutdown: cancelling it causes parallel workers to stop after their current
// verse. Phase 1 (setupBooks) always runs regardless of ctx state.
//
// All parallel-mode fields (Checkpoint, Workers, RateLimitRPS) are validated
// before any API call is made — misconfigured runs fail fast without wasting
// the Phase 1 network round-trips.
func (s *YouVersionScraper) RunWithContext(ctx context.Context) error {
	// Validate required fields upfront, before any API call (fail-fast).
	if s.Checkpoint == nil {
		return fmt.Errorf("Checkpoint is required: set YouVersionScraper.Checkpoint before calling Run")
	}

	log.Println("YouVersion Scraper: starting...")

	// Phase 1: write book/title metadata to the database.
	setup, err := s.setupBooks(ctx)
	if err != nil {
		return fmt.Errorf("phase 1 failed: %w", err)
	}

	// Phase 2: fetch verse text in parallel, write to JSONL checkpoint.
	if err := s.crawlVersesParallel(ctx, setup); err != nil {
		return fmt.Errorf("phase 2 failed: %w", err)
	}

	log.Println("YouVersion Scraper: done.")
	return nil
}

// setupBooks fetches English and Chinese book lists from the YouVersion API,
// creates a DB record for each book in canonical order, and writes both
// localized titles. Processing is sequential; the returned bookSetup contains
// all data needed by Phase 2.
func (s *YouVersionScraper) setupBooks(ctx context.Context) (*bookSetup, error) {
	log.Printf("Phase 1: fetching book list for English Bible (ID %d)...", s.EnglishBibleID)
	enResp, err := s.Client.GetBooks(ctx, s.EnglishBibleID)
	if err != nil {
		return nil, fmt.Errorf("GetBooks(EN=%d): %w", s.EnglishBibleID, err)
	}

	log.Printf("Phase 1: fetching book list for Chinese Bible (ID %d)...", s.ChineseBibleID)
	zhResp, err := s.Client.GetBooks(ctx, s.ChineseBibleID)
	if err != nil {
		return nil, fmt.Errorf("GetBooks(ZH=%d): %w", s.ChineseBibleID, err)
	}

	if len(enResp.Data) != len(zhResp.Data) {
		return nil, fmt.Errorf("book count mismatch: EN=%d ZH=%d",
			len(enResp.Data), len(zhResp.Data))
	}
	if len(enResp.Data) == 0 {
		return nil, fmt.Errorf("API returned 0 books for Bible ID %d", s.EnglishBibleID)
	}

	metas := make([]bookMeta, 0, len(enResp.Data))
	for i, enBook := range enResp.Data {
		sortIdx := i + 1 // 1-based canonical book sort
		bookID, err := s.Repo.GetOrCreateBook(sortIdx)
		if err != nil {
			log.Printf("Phase 1: GetOrCreateBook(sort=%d): %v", sortIdx, err)
			continue
		}
		if err := s.Repo.UpsertBookContent(bookID, LangEnglish, enBook.Title); err != nil {
			log.Printf("Phase 1: UpsertBookContent EN (sort=%d): %v", sortIdx, err)
		}
		if err := s.Repo.UpsertBookContent(bookID, LangChinese, zhResp.Data[i].Title); err != nil {
			log.Printf("Phase 1: UpsertBookContent ZH (sort=%d): %v", sortIdx, err)
		}
		metas = append(metas, bookMeta{id: bookID, index: i})
	}

	if len(metas) == 0 {
		return nil, fmt.Errorf("no books were successfully written to DB in phase 1")
	}

	log.Printf("Phase 1: %d/%d books ready.", len(metas), len(enResp.Data))
	return &bookSetup{metas: metas, enBooks: enResp.Data, zhBooks: zhResp.Data}, nil
}

// crawlVersesParallel is the parallel implementation of Phase 2. It loads the
// checkpoint to determine which verses are already done, builds a work queue
// of remaining verses, runs N worker goroutines that each fetch with retry and
// rate limiting, and uses a single writer goroutine to append results to the
// JSONL checkpoint file — eliminating concurrent write races.
func (s *YouVersionScraper) crawlVersesParallel(ctx context.Context, setup *bookSetup) error {
	log.Println("Phase 2: crawling verses (parallel)...")

	completed, err := s.Checkpoint.LoadCompleted()
	if err != nil {
		log.Printf("Phase 2: warning: could not load checkpoint: %v — starting fresh", err)
		completed = make(map[string]struct{})
	}
	log.Printf("Phase 2: %d verses already in checkpoint", len(completed))

	type langConfig struct {
		lang    string
		bibleID int
		books   []BookData
	}
	langs := []langConfig{
		{LangEnglish, s.EnglishBibleID, setup.enBooks},
		{LangChinese, s.ChineseBibleID, setup.zhBooks},
	}

	// Build the work queue, skipping completed verses.
	var works []verseWork
	for _, lc := range langs {
		for _, meta := range setup.metas {
			book := lc.books[meta.index]
			bookSort := meta.index + 1
			for chapIdx, chap := range book.Chapters {
				chapSort := chapIdx + 1
				for _, verse := range chap.Verses {
					// verse.ID from the API is a numeric string (e.g. "14"), not an
					// integer field; parse it so VerseSort stores a numeric sort key.
					// Reject non-positive values to prevent DB sort-key violations.
					verseNum, parseErr := strconv.Atoi(verse.ID)
					if parseErr != nil || verseNum <= 0 {
						log.Printf("Phase 2: invalid verse ID %q (book=%d chap=%d lang=%s): skipping",
							verse.ID, bookSort, chapSort, lc.lang)
						continue
					}
						if _, ok := completed[checkpointKey(lc.lang, verse.PassageID)]; ok {
						continue
					}
					works = append(works, verseWork{
						lang:      lc.lang,
						bibleID:   lc.bibleID,
						bookSort:  bookSort,
						chapSort:  chapSort,
						verseSort: verseNum,
						passageID: verse.PassageID,
					})
				}
			}
		}
	}

	total := len(works) + len(completed)
	log.Printf("Phase 2: %d/%d verses remaining", len(works), total)

	if len(works) == 0 {
		log.Println("Phase 2: nothing to do — all verses already in checkpoint.")
		log.Println("Phase 2: done.")
		return nil
	}

	// Sanitize: zero or negative config values fall back to safe defaults
	// so the scraper is runnable even with a minimal or missing configuration.
	workers := s.Workers
	switch {
	case workers <= 0:
		workers = 1
	case workers > maxWorkers:
		log.Printf("Phase 2: clamping workers from %d to %d (hard cap)", workers, maxWorkers)
		workers = maxWorkers
	}
	rps := s.RateLimitRPS
	if rps <= 0 {
		rps = 1.0
	}

	// Shared rate limiter: burst = workers so a fresh start doesn't block.
	limiter := rate.NewLimiter(rate.Limit(rps), workers)

	// workCh is bounded to workers×workChannelMultiplier (40 slots at WORKERS=20)
	// instead of len(works) (~62,200 slots ≈ 4 MB). A producer goroutine feeds
	// it with back-pressure: workers pull at their own rate, and the producer
	// blocks when the buffer is full. This keeps peak work-queue RAM O(workers)
	// rather than O(n total verses), cutting the allocation from ~4 MB to ~2.6 KB.
	//
	// The ctx.Done() arm in the producer's select ensures that a SIGINT/SIGTERM
	// drains cleanly: the producer stops enqueuing, closes workCh, and workers
	// exit via their own ctx.Done() check before the next verse.
	workCh := make(chan verseWork, workers*workChannelMultiplier)
	go func() {
		defer close(workCh)
		for _, w := range works {
			select {
			case workCh <- w:
			case <-ctx.Done():
				return
			}
		}
	}()

	// resultCh buffer = workers×4 gives the single checkpoint writer headroom
	// to fall behind momentarily without blocking worker goroutines.
	resultCh := make(chan VerseRecord, workers*4)

	var wg sync.WaitGroup
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for w := range workCh {
				// Check for cancellation before each verse so workers exit promptly after
				// SIGINT/SIGTERM without waiting for the rate limiter or a retry sleep.
				select {
				case <-ctx.Done():
					return
				default:
				}
				// Rate-limit once per verse, outside the retry loop, so that
				// backoff sleeps do not consume additional tokens and starve
				// sibling workers.
				if limErr := limiter.Wait(ctx); limErr != nil {
					return
				}
				rec, fetchErr := s.fetchWithRetry(ctx, w)
				if fetchErr != nil {
					if errors.Is(fetchErr, errVerseNotFound) {
						continue // expected for some translations
					}
					if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
						return
					}
					log.Printf("Phase 2: worker=%d GetPassage(%s lang=%s): %v", id, w.passageID, w.lang, fetchErr)
					continue
				}
				select {
				case resultCh <- rec:
				case <-ctx.Done():
					return
				}
			}
		}(workerID)
	}

	// A dedicated goroutine closes resultCh after all workers finish.
	// This cannot be done inline because the main goroutine is the single
	// writer blocked on "for rec := range resultCh" below; wg.Wait() must
	// run concurrently. Closing resultCh unblocks the range and exits the writer.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Single writer: serialise all checkpoint writes through the main goroutine
	// so file I/O is never concurrent. Ranging over a closed channel drains it
	// fully before returning — the canonical Go fan-in pattern.
	// Checkpoint.Append still holds its own mutex as a safety net for any
	// future callers that might write from multiple goroutines.
	var written, writeErr int
	for rec := range resultCh {
		if appendErr := s.Checkpoint.Append(rec); appendErr != nil {
			log.Printf("Phase 2: checkpoint write error for %s: %v", rec.PassageID, appendErr)
			writeErr++
		} else {
			written++
		}
	}

	log.Printf("Phase 2: done. written=%d already-done=%d write-errors=%d",
		written, len(completed), writeErr)

	if writeErr > 0 {
		return fmt.Errorf("%w: count=%d", ErrPhase2WriteFailures, writeErr)
	}
	return nil
}

// fetchWithRetry fetches a single verse with exponential-backoff retry.
// Rate limiting is applied by the caller (worker goroutine) once per verse,
// not inside this function, so backoff sleeps do not consume rate-limit tokens.
// 404 responses are treated as permanent skips (errVerseNotFound).
// 403 and other permanent errors are not retried.
// Context cancellation stops all retries immediately.
func (s *YouVersionScraper) fetchWithRetry(
	ctx context.Context, w verseWork,
) (VerseRecord, error) {
	maxRetries := s.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	baseMS := s.RetryBaseMS
	if baseMS <= 0 {
		baseMS = 1000
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = time.Duration(baseMS) * time.Millisecond
	bo.Multiplier = 2.0           // double the wait interval on each attempt
	bo.RandomizationFactor = 0.25 // ±25 % jitter to prevent thundering-herd retries
	bo.MaxInterval = 60 * time.Second
	bo.MaxElapsedTime = 0 // 0 disables the wall-clock cap; MaxRetries governs stopping

	var rec VerseRecord
	operation := func() error {
		passage, apiErr := s.Client.GetPassage(ctx, w.bibleID, w.passageID)
		if apiErr != nil {
			var httpErr *HTTPStatusError
			if errors.As(apiErr, &httpErr) {
				switch httpErr.StatusCode {
				case http.StatusNotFound:
					return backoff.Permanent(errVerseNotFound)
				case http.StatusForbidden:
					return backoff.Permanent(apiErr)
				}
				// 429 or 5xx: fall through to retry
			}
			return apiErr
		}

		if passage.Content == "" {
			return backoff.Permanent(fmt.Errorf("empty content for %s", w.passageID))
		}

		rec = VerseRecord{
			PassageID:   w.passageID,
			Lang:        w.lang,
			BibleID:     w.bibleID,
			BookSort:    w.bookSort,
			ChapterSort: w.chapSort,
			VerseSort:   w.verseSort,
			Content:     passage.Content,
			CrawledAt:   time.Now().UTC(),
		}
		return nil
	}

	boWithCtx := backoff.WithContext(bo, ctx)
	// G115 (gosec): MaxRetries is validated above to be a small positive int;
	// the uint64 conversion cannot overflow in any realistic scenario.
	boWithMax := backoff.WithMaxRetries(boWithCtx, uint64(maxRetries)) //nolint:gosec
	if retryErr := backoff.Retry(operation, boWithMax); retryErr != nil {
		return VerseRecord{}, retryErr
	}
	return rec, nil
}
