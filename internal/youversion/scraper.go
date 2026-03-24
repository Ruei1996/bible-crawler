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
)

// BibleAPIClient is the subset of the YouVersion Platform API required by
// YouVersionScraper. Using an interface (rather than the concrete *Client)
// allows unit tests to inject a mock without starting an HTTP server.
type BibleAPIClient interface {
	// GetBooks returns all books (with chapter and verse structure) for the
	// given Bible version ID.
	GetBooks(bibleID int) (*BooksResponse, error)
	// GetPassage returns the text content for the given USFM passage ID
	// (e.g. "GEN.1.1", "GEN.1.1-3", "GEN.1") from the given Bible version.
	GetPassage(bibleID int, passageID string) (*PassageData, error)
}

// errVerseNotFound is returned by fetchWithRetry when the API responds with
// HTTP 404 for a passage. 404s are expected for certain verses that modern
// translations (e.g. NIV) omit. Workers treat this as a permanent skip.
var errVerseNotFound = errors.New("verse not found (404)")

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
// Phase 2 – crawlVerses: for every book/chapter/verse returned by the API,
// fetches passage text and persists verse records.
//
// Optional parallel mode: when Checkpoint is non-nil, Phase 2 uses a worker
// pool that writes verse records to a JSONL file. A separate importer program
// then batch-writes the JSONL to the database. This mode supports graceful
// shutdown and resume from the last checkpoint.
type YouVersionScraper struct {
	Repo           *repository.BibleRepository
	Client         BibleAPIClient
	ChineseBibleID int
	EnglishBibleID int

	// Optional parallel-mode fields. Leave all zero/nil for sequential mode.
	Checkpoint   *Checkpoint // non-nil enables parallel+JSONL mode
	Workers      int         // parallel worker count (0 → 1)
	RateLimitRPS float64     // token-bucket rate limit in req/s (0 → 1.0)
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

// Run executes both crawler phases in sequence using a background context.
// It is kept for backward compatibility; prefer RunWithContext for graceful
// shutdown support.
func (s *YouVersionScraper) Run() error {
	return s.RunWithContext(context.Background())
}

// RunWithContext executes both crawler phases. The provided context is used to
// support graceful shutdown: cancelling ctx causes parallel workers to stop
// after their current verse. Sequential mode ignores ctx beyond startup.
func (s *YouVersionScraper) RunWithContext(ctx context.Context) error {
	log.Println("YouVersion Scraper: starting...")
	setup, err := s.setupBooks()
	if err != nil {
		return fmt.Errorf("phase 1 failed: %w", err)
	}
	if s.Checkpoint != nil {
		s.crawlVersesParallel(ctx, setup)
	} else {
		s.crawlVerses(setup)
	}
	log.Println("YouVersion Scraper: done.")
	return nil
}

// setupBooks fetches English and Chinese book lists from the YouVersion API,
// creates a DB record for each book in canonical order, and writes both
// localized titles. Processing is sequential; the returned bookSetup contains
// all data needed by Phase 2.
func (s *YouVersionScraper) setupBooks() (*bookSetup, error) {
	log.Printf("Phase 1: fetching book list for English Bible (ID %d)...", s.EnglishBibleID)
	enResp, err := s.Client.GetBooks(s.EnglishBibleID)
	if err != nil {
		return nil, fmt.Errorf("GetBooks(EN=%d): %w", s.EnglishBibleID, err)
	}

	log.Printf("Phase 1: fetching book list for Chinese Bible (ID %d)...", s.ChineseBibleID)
	zhResp, err := s.Client.GetBooks(s.ChineseBibleID)
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

// crawlVerses iterates over all books/chapters/verses for English then Chinese,
// calling the YouVersion passages endpoint per verse and persisting the text.
// Chapter-level errors are logged; verse-level errors within a chapter are
// also logged but do not abort processing of subsequent verses.
func (s *YouVersionScraper) crawlVerses(setup *bookSetup) {
	log.Println("Phase 2: crawling verses...")

	type langConfig struct {
		lang    string
		bibleID int
		books   []BookData
	}
	langs := []langConfig{
		{LangEnglish, s.EnglishBibleID, setup.enBooks},
		{LangChinese, s.ChineseBibleID, setup.zhBooks},
	}

	for _, lc := range langs {
		log.Printf("Phase 2: language=%s bibleID=%d", lc.lang, lc.bibleID)
		for _, meta := range setup.metas {
			book := lc.books[meta.index]
			for chapIdx, chap := range book.Chapters {
				chapSort := chapIdx + 1
				if err := s.processChapter(meta.id, chapSort, chap, lc.lang, lc.bibleID); err != nil {
					log.Printf("processChapter (book=%d chap=%d lang=%s): %v",
						meta.index+1, chapSort, lc.lang, err)
				}
			}
		}
	}

	log.Println("Phase 2: done.")
}

// processChapter creates the chapter DB record and title, then fetches and
// persists every verse listed in the chapter's verse slice.
// Returns an error only if the chapter DB record cannot be created; verse-level
// errors are logged and skipped so one bad verse does not abort the chapter.
func (s *YouVersionScraper) processChapter(
	bookID uuid.UUID, chapSort int, chap ChapterData, lang string, bibleID int,
) error {
	chapID, err := s.Repo.GetOrCreateChapter(bookID, chapSort)
	if err != nil {
		return fmt.Errorf("GetOrCreateChapter: %w", err)
	}

	chapTitle := fmt.Sprintf("Chapter %d", chapSort)
	if lang == LangChinese {
		chapTitle = fmt.Sprintf("第 %d 章", chapSort)
	}
	if err := s.Repo.UpsertChapterContent(chapID, lang, chapTitle); err != nil {
		log.Printf("UpsertChapterContent (chap=%d lang=%s): %v", chapSort, lang, err)
	}

	for _, verse := range chap.Verses {
		verseNum, err := strconv.Atoi(verse.ID)
		if err != nil || verseNum <= 0 {
			log.Printf("Invalid verse ID %q (chap=%d lang=%s): skipping", verse.ID, chapSort, lang)
			continue
		}
		passage, err := s.Client.GetPassage(bibleID, verse.PassageID)
		if err != nil {
			log.Printf("GetPassage(%s lang=%s): %v", verse.PassageID, lang, err)
			continue
		}
		if err := s.saveVerse(bookID, chapID, verseNum, lang, passage.Content); err != nil {
			log.Printf("saveVerse (chap=%d verse=%d lang=%s): %v", chapSort, verseNum, lang, err)
		}
	}
	return nil
}

// saveVerse creates the verse structural record (bible_sections) and writes its
// localized content (bible_section_contents). The verse title follows the same
// convention as the HTML scraper: "verse N" for English, "第N節" for Chinese.
func (s *YouVersionScraper) saveVerse(
	bookID, chapID uuid.UUID, verseNum int, lang, content string,
) error {
	if content == "" {
		return fmt.Errorf("empty passage content for verse %d", verseNum)
	}

	verseTitle := fmt.Sprintf("verse %d", verseNum)
	if lang == LangChinese {
		verseTitle = fmt.Sprintf("第%d節", verseNum)
	}

	secID, err := s.Repo.GetOrCreateSection(bookID, chapID, verseNum)
	if err != nil {
		return fmt.Errorf("GetOrCreateSection: %w", err)
	}
	if err := s.Repo.UpsertSectionContent(secID, lang, verseTitle, content); err != nil {
		return fmt.Errorf("UpsertSectionContent: %w", err)
	}
	return nil
}

// crawlVersesParallel is the parallel implementation of Phase 2. It loads the
// checkpoint to determine which verses are already done, builds a work queue
// of remaining verses, runs N worker goroutines that each fetch with retry and
// rate limiting, and uses a single writer goroutine to append results to the
// JSONL checkpoint file — eliminating concurrent write races.
func (s *YouVersionScraper) crawlVersesParallel(ctx context.Context, setup *bookSetup) {
	log.Println("Phase 2: crawling verses (parallel)...")

	completed, err := s.Checkpoint.LoadCompleted()
	if err != nil {
		log.Printf("Phase 2: warning: could not load checkpoint: %v — starting fresh", err)
		completed = make(map[string]bool)
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
					verseNum, parseErr := strconv.Atoi(verse.ID)
					if parseErr != nil || verseNum <= 0 {
						log.Printf("Phase 2: invalid verse ID %q (book=%d chap=%d lang=%s): skipping",
							verse.ID, bookSort, chapSort, lc.lang)
						continue
					}
					if completed[checkpointKey(lc.lang, verse.PassageID)] {
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
		return
	}

	workers := s.Workers
	if workers <= 0 {
		workers = 1
	}
	rps := s.RateLimitRPS
	if rps <= 0 {
		rps = 1.0
	}

	// Shared rate limiter: burst = workers so a fresh start doesn't block.
	limiter := rate.NewLimiter(rate.Limit(rps), workers)

	workCh := make(chan verseWork, len(works))
	for _, w := range works {
		workCh <- w
	}
	close(workCh)

	resultCh := make(chan VerseRecord, workers*4)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range workCh {
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
					log.Printf("Phase 2: GetPassage(%s lang=%s): %v", w.passageID, w.lang, fetchErr)
					continue
				}
				select {
				case resultCh <- rec:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Close resultCh once all workers finish.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Single writer: reads results and appends to checkpoint file.
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
	bo.Multiplier = 2.0
	bo.RandomizationFactor = 0.25
	bo.MaxInterval = 60 * time.Second
	bo.MaxElapsedTime = 0 // controlled by MaxRetries instead

	var rec VerseRecord
	operation := func() error {
		passage, apiErr := s.Client.GetPassage(w.bibleID, w.passageID)
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
	boWithMax := backoff.WithMaxRetries(boWithCtx, uint64(maxRetries)) //nolint:gosec
	if retryErr := backoff.Retry(operation, boWithMax); retryErr != nil {
		return VerseRecord{}, retryErr
	}
	return rec, nil
}
