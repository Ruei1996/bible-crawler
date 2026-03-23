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
//	Chinese: CCB 当代圣经 (ID 36) — freely accessible
//
// To use 新標點和合本 (ID 46) for Chinese, apply for a YouVersion publisher
// license at https://platform.youversion.com/bibles and then set
// YOUVERSION_CHINESE_BIBLE_ID=46 in your .env file.
package youversion

import (
	"fmt"
	"log"
	"strconv"

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

// YouVersionScraper crawls Bible content via the YouVersion Platform API and
// persists it to the same PostgreSQL schema used by the HTML scraper.
//
// Phase 1 – setupBooks: fetches book lists for both languages and writes
// book rows plus localized titles to the database.
//
// Phase 2 – crawlVerses: for every book/chapter/verse returned by the API,
// fetches passage text and persists verse records.
type YouVersionScraper struct {
	Repo           *repository.BibleRepository
	Client         BibleAPIClient
	ChineseBibleID int
	EnglishBibleID int
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

// Run executes both crawler phases in sequence.
// A Phase 1 error aborts the run; Phase 2 errors are logged per-item.
func (s *YouVersionScraper) Run() error {
	log.Println("YouVersion Scraper: starting...")
	setup, err := s.setupBooks()
	if err != nil {
		return fmt.Errorf("phase 1 failed: %w", err)
	}
	s.crawlVerses(setup)
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
