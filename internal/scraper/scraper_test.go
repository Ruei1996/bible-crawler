package scraper

// Tests for unexported functions (parseChapterContext, saveVerse) and
// crawlChapters must live in the same package.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gocolly/colly/v2"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bible-crawler/internal/config"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/spec"
)

// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────

func qre(q string) string { return regexp.QuoteMeta(q) }

func newMockRepo(t *testing.T) (*repository.BibleRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return repository.NewBibleRepository(sqlx.NewDb(db, "sqlmock")), mock
}

// makeMinimalSpec builds a BibleSpec with 66 books where only book at index
// bookIdx has chapters. All other books have 0 chapters so no HTTP requests
// are queued for them.
func makeMinimalSpec(bookIdx, chapters, verses int) *spec.BibleSpec {
	zhBooks := make([]*spec.BookSpec, 66)
	enBooks := make([]*spec.BookSpec, 66)
	for i := 0; i < 66; i++ {
		zhBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
		enBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
	}
	vpc := make(map[string]int)
	if chapters >= 1 && verses >= 1 {
		vpc[fmt.Sprintf("01-[%d]", verses)] = verses
	}
	zhBooks[bookIdx] = &spec.BookSpec{
		Number:           bookIdx + 1,
		NameZH:           "Genesis",
		TotalChapters:    chapters,
		VersesPerChapter: vpc,
	}
	enBooks[bookIdx] = &spec.BookSpec{
		Number:           bookIdx + 1,
		NameEN:           "Genesis",
		TotalChapters:    chapters,
		VersesPerChapter: vpc,
	}
	return &spec.BibleSpec{ZH: zhBooks, EN: enBooks}
}

// ──────────────────────────────────────────────────────────────
// NewBibleScraper
// ──────────────────────────────────────────────────────────────

func TestNewBibleScraper(t *testing.T) {
	repo, _ := newMockRepo(t)
	bspec := makeMinimalSpec(0, 0, 0)
	cfg := &config.Config{
		SourceDomain:         "example.com",
		SourceZHURL:          "http://example.com/%d",
		SourceENURL:          "http://example.com/%d",
		CrawlerParallelism:   1,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	assert.NotNil(t, s)
	assert.NotNil(t, s.C)
	assert.Equal(t, bspec, s.Spec)
	assert.Equal(t, cfg, s.Cfg)
}

// ──────────────────────────────────────────────────────────────
// parseChapterContext
// ──────────────────────────────────────────────────────────────

func makeCtx(t *testing.T, bookID, bookIndex, chapSort, lang, maxVerses string) *colly.Context {
	t.Helper()
	ctx := colly.NewContext()
	ctx.Put("bookID", bookID)
	ctx.Put("bookIndex", bookIndex)
	ctx.Put("chapSort", chapSort)
	ctx.Put("lang", lang)
	ctx.Put("maxVerses", maxVerses)
	return ctx
}

func TestParseChapterContext_Valid_Chinese(t *testing.T) {
	id := uuid.New()
	ctx := makeCtx(t, id.String(), "0", "1", "chinese", "31")
	cc, err := parseChapterContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, id, cc.bookID)
	assert.Equal(t, 0, cc.bookIndex)
	assert.Equal(t, 1, cc.chapSort)
	assert.Equal(t, "chinese", cc.lang)
	assert.Equal(t, 31, cc.maxVerses)
}

func TestParseChapterContext_Valid_English(t *testing.T) {
	id := uuid.New()
	ctx := makeCtx(t, id.String(), "65", "22", "english", "21")
	cc, err := parseChapterContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, "english", cc.lang)
	assert.Equal(t, 65, cc.bookIndex)
	assert.Equal(t, 22, cc.chapSort)
	assert.Equal(t, 21, cc.maxVerses)
}

func TestParseChapterContext_MissingBookID(t *testing.T) {
	ctx := makeCtx(t, "", "0", "1", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing bookID")
}

func TestParseChapterContext_InvalidBookIDFormat(t *testing.T) {
	ctx := makeCtx(t, "not-a-uuid", "0", "1", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid bookID")
}

func TestParseChapterContext_BookIndexNonNumeric(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "abc", "1", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid bookIndex")
}

func TestParseChapterContext_BookIndexNegative(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "-1", "1", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
}

func TestParseChapterContext_BookIndex66(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "66", "1", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
}

func TestParseChapterContext_ChapSortZero(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "0", "0", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid chapSort")
}

func TestParseChapterContext_ChapSortNonNumeric(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "0", "abc", "chinese", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
}

func TestParseChapterContext_MaxVersesZero(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "0", "1", "chinese", "0")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid maxVerses")
}

func TestParseChapterContext_MaxVersesNonNumeric(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "0", "1", "chinese", "abc")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
}

func TestParseChapterContext_UnsupportedLang(t *testing.T) {
	ctx := makeCtx(t, uuid.New().String(), "0", "1", "french", "31")
	_, err := parseChapterContext(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported language")
}

// ──────────────────────────────────────────────────────────────
// saveVerse
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectSection = `SELECT id FROM bibles.bible_sections WHERE bible_book_id = $1 AND bible_chapter_id = $2 AND sort = $3`
	sqlInsertSection = `INSERT INTO bibles.bible_sections (bible_book_id, bible_chapter_id, sort) VALUES ($1, $2, $3) ON CONFLICT (bible_book_id, bible_chapter_id, sort) DO NOTHING RETURNING id`

	sqlSelectSectionContent = `SELECT title, content FROM bibles.bible_section_contents WHERE bible_section_id = $1 AND language = $2`
	sqlInsertSectionContent = `INSERT INTO bibles.bible_section_contents (bible_section_id, language, title, content) VALUES ($1, $2, $3, $4)`
)

func newScraper(repo *repository.BibleRepository, bspec *spec.BibleSpec, cfg *config.Config) *BibleScraper {
	return &BibleScraper{
		Repo:             repo,
		C:                colly.NewCollector(),
		Spec:             bspec,
		Cfg:              cfg,
		globalChapStarts: bspec.GlobalChapStarts(),
	}
}

func TestSaveVerse_ZeroVerseNum(t *testing.T) {
	repo, _ := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})
	err := s.saveVerse(uuid.New(), uuid.New(), 0, LangEnglish, "some content")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid verse number")
}

func TestSaveVerse_NegativeVerseNum(t *testing.T) {
	repo, _ := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})
	err := s.saveVerse(uuid.New(), uuid.New(), -1, LangEnglish, "some content")
	require.Error(t, err)
}

func TestSaveVerse_EmptyContent(t *testing.T) {
	repo, _ := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})
	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty verse content")
}

func TestSaveVerse_ValidEnglish(t *testing.T) {
	repo, mock := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})

	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "In the beginning")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveVerse_ValidChinese(t *testing.T) {
	repo, mock := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})

	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangChinese, "太初，神創造天地")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveVerse_GetOrCreateSectionError(t *testing.T) {
	repo, mock := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})

	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnError(fmt.Errorf("db error"))

	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "verse text")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveVerse_UpsertSectionContentError(t *testing.T) {
	repo, mock := newMockRepo(t)
	s := newScraper(repo, makeMinimalSpec(0, 0, 0), &config.Config{})

	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnError(fmt.Errorf("db error"))

	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "verse text")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// crawlChapters – nil book-ID path
// ──────────────────────────────────────────────────────────────

func TestCrawlChapters_NilBookID(t *testing.T) {
	repo, mock := newMockRepo(t)
	bspec := makeMinimalSpec(0, 1, 1)
	cfg := &config.Config{
		SourceDomain:         "example.com",
		SourceZHURL:          "http://example.com/%d",
		SourceENURL:          "http://example.com/%d",
		CrawlerParallelism:   1,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	// No HTTP calls or DB calls should happen.
	s.crawlChapters([]BookMeta{{ID: uuid.Nil, Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// crawlChapters – VerseCount error paths
// ──────────────────────────────────────────────────────────────

func TestCrawlChapters_ZHVerseCountError(t *testing.T) {
	repo, mock := newMockRepo(t)

	zhBooks := make([]*spec.BookSpec, 66)
	enBooks := make([]*spec.BookSpec, 66)
	for i := 0; i < 66; i++ {
		zhBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
		enBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
	}
	// Bad key → VerseCount returns error.
	zhBooks[0] = &spec.BookSpec{
		Number:           1,
		TotalChapters:    1,
		VersesPerChapter: map[string]int{"nodash": 31},
	}
	enBooks[0] = &spec.BookSpec{
		Number:        1,
		TotalChapters: 0,
	}
	bspec := &spec.BibleSpec{ZH: zhBooks, EN: enBooks}

	cfg := &config.Config{
		SourceDomain: "example.com", SourceZHURL: "http://example.com/%d",
		SourceENURL: "http://example.com/%d", CrawlerParallelism: 1,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	bookID := uuid.New()
	s.crawlChapters([]BookMeta{{ID: bookID, Index: 0}})
	// No DB expectations needed; the error path just logs and continues.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCrawlChapters_ENVerseCountError(t *testing.T) {
	repo, mock := newMockRepo(t)

	zhBooks := make([]*spec.BookSpec, 66)
	enBooks := make([]*spec.BookSpec, 66)
	for i := 0; i < 66; i++ {
		zhBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
		enBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
	}
	zhBooks[0] = &spec.BookSpec{
		Number:           1,
		TotalChapters:    1,
		VersesPerChapter: map[string]int{"01-[31]": 31},
	}
	// Bad key → EN VerseCount returns error.
	enBooks[0] = &spec.BookSpec{
		Number:           1,
		TotalChapters:    1,
		VersesPerChapter: map[string]int{"nodash": 31},
	}
	bspec := &spec.BibleSpec{ZH: zhBooks, EN: enBooks}

	cfg := &config.Config{
		SourceDomain: "example.com", SourceZHURL: "http://example.com/%d",
		SourceENURL: "http://example.com/%d", CrawlerParallelism: 1,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	bookID := uuid.New()
	s.crawlChapters([]BookMeta{{ID: bookID, Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// crawlChapters – full HTTP round-trip with mock server
// ──────────────────────────────────────────────────────────────

const verseHTML = `<html><body><ol>
<li value="1">In the beginning God created the heavens and the earth.</li>
</ol></body></html>`

func setupChapterMock(mock sqlmock.Sqlmock, chapID, secID uuid.UUID) {
	const (
		sqlSelectChapter = `SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`
		sqlInsertChapter = `INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2) ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`

		sqlSelectChapterContent = `SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`
		sqlInsertChapterContent = `INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`
	)

	mock.MatchExpectationsInOrder(false)

	// GetOrCreateChapter – ZH: not found → inserted
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))

	// GetOrCreateChapter – EN: found on first SELECT
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))

	// UpsertChapterContent – ZH
	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertChapterContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// UpsertChapterContent – EN
	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertChapterContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// GetOrCreateSection – ZH verse 1: not found → inserted
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))

	// GetOrCreateSection – EN verse 1: found
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))

	// UpsertSectionContent – ZH verse 1
	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// UpsertSectionContent – EN verse 1
	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestCrawlChapters_WithHTTPMockServer(t *testing.T) {
	// Serve static verse HTML regardless of path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, verseHTML)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	chapID := uuid.New()
	secID := uuid.New()
	setupChapterMock(mock, chapID, secID)

	bspec := makeMinimalSpec(0, 1, 1) // book 0, 1 chapter, 1 verse
	cfg := &config.Config{
		SourceDomain:         "127.0.0.1",
		SourceZHURL:          srv.URL + "/%d",
		SourceENURL:          srv.URL + "/%d",
		CrawlerParallelism:   1,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
		HTTPTimeoutSec:       5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	bookID := uuid.New()
	s.crawlChapters([]BookMeta{{ID: bookID, Index: 0}})

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// crawlChapters – OnResponse DB error paths
// ──────────────────────────────────────────────────────────────

func TestCrawlChapters_GetOrCreateChapterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, verseHTML)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)

	// Both ZH and EN chapter SELECTs return a DB error → OnResponse logs and returns.
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnError(fmt.Errorf("db error"))

	bspec := makeMinimalSpec(0, 1, 1)
	cfg := &config.Config{
		SourceDomain: "127.0.0.1", SourceZHURL: srv.URL + "/%d",
		SourceENURL: srv.URL + "/%d", CrawlerParallelism: 1,
		HTTPTimeoutSec: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	s.crawlChapters([]BookMeta{{ID: uuid.New(), Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCrawlChapters_UpsertChapterContentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, verseHTML)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	chapID := uuid.New()

	// Both ZH and EN: chapter found, but UpsertChapterContent fails.
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnError(fmt.Errorf("db error"))

	// Sections are still created for the verses.
	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).WillReturnResult(sqlmock.NewResult(1, 1))

	bspec := makeMinimalSpec(0, 1, 1)
	cfg := &config.Config{
		SourceDomain: "127.0.0.1", SourceZHURL: srv.URL + "/%d",
		SourceENURL: srv.URL + "/%d", CrawlerParallelism: 1,
		HTTPTimeoutSec: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	s.crawlChapters([]BookMeta{{ID: uuid.New(), Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCrawlChapters_InvalidVerseValue(t *testing.T) {
	// Verse has value="notanumber" → fallback to sequential index; also has
	// an empty-text li → early skip.
	htmlInvalidVerseVal := `<html><body><ol>
<li value="notanumber">valid verse text</li>
<li value="1">  </li>
</ol></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, htmlInvalidVerseVal)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	chapID := uuid.New()
	secID := uuid.New()

	// ZH: chapter + 1 verse (fallback verseNum = 1)
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(`INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2) ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	// EN: chapter found
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))

	// Chapter content ZH + EN
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Section for the fallback-indexed verse (both ZH and EN)
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(qre(sqlSelectSectionContent)).WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).WillReturnResult(sqlmock.NewResult(1, 1))

	bspec := makeMinimalSpec(0, 1, 2) // maxVerses = 2
	cfg := &config.Config{
		SourceDomain: "127.0.0.1", SourceZHURL: srv.URL + "/%d",
		SourceENURL: srv.URL + "/%d", CrawlerParallelism: 1,
		HTTPTimeoutSec: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	s.crawlChapters([]BookMeta{{ID: uuid.New(), Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCrawlChapters_SaveVerseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, verseHTML)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	chapID := uuid.New()

	// Chapter found for both ZH and EN.
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(`INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2) ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// GetOrCreateSection returns DB error for both ZH and EN → saveVerse error logged.
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnError(fmt.Errorf("db error"))

	bspec := makeMinimalSpec(0, 1, 1)
	cfg := &config.Config{
		SourceDomain: "127.0.0.1", SourceZHURL: srv.URL + "/%d",
		SourceENURL: srv.URL + "/%d", CrawlerParallelism: 1,
		HTTPTimeoutSec: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	s.crawlChapters([]BookMeta{{ID: uuid.New(), Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// setupBooks
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectBook        = `SELECT id FROM bibles.bible_books WHERE sort = $1`
	sqlInsertBook        = `INSERT INTO bibles.bible_books (sort) VALUES ($1) ON CONFLICT (sort) DO NOTHING RETURNING id`
	sqlSelectBookContent = `SELECT title FROM bibles.bible_book_contents WHERE bible_book_id = $1 AND language = $2`
	sqlInsertBookContent = `INSERT INTO bibles.bible_book_contents (bible_book_id, language, title) VALUES ($1, $2, $3)`
)

func TestSetupBooks_AllGetOrCreateFail(t *testing.T) {
	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 66; i++ {
		mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))
	}
	bspec := makeMinimalSpec(0, 0, 0)
	cfg := &config.Config{
		SourceDomain: "example.com", SourceZHURL: "http://example.com/%d",
		SourceENURL: "http://example.com/%d", CrawlerParallelism: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	_, err := s.setupBooks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "66")
}

func TestSetupBooks_UpsertBookContentError(t *testing.T) {
	// With makeMinimalSpec(0, 0, 0): book[0] has NameZH="Genesis"/NameEN="Genesis";
	// all other books have empty names, so UpsertBookContent fails in validation
	// (no DB call). Only book[0] reaches the DB.
	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 66; i++ {
		bookID := uuid.New()
		mock.ExpectQuery(qre(sqlSelectBook)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(bookID))
	}
	// Book 0 (Genesis) calls UpsertBookContent twice (ZH + EN) → DB error each.
	mock.ExpectQuery(qre(sqlSelectBookContent)).WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(sqlSelectBookContent)).WillReturnError(fmt.Errorf("db error"))

	bspec := makeMinimalSpec(0, 0, 0)
	cfg := &config.Config{
		SourceDomain: "example.com", SourceZHURL: "http://example.com/%d",
		SourceENURL: "http://example.com/%d", CrawlerParallelism: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	books, err := s.setupBooks()
	require.NoError(t, err)
	assert.Len(t, books, 66)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// Run
// ──────────────────────────────────────────────────────────────

func TestRun_SetupBooksError(t *testing.T) {
	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 66; i++ {
		mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))
	}
	bspec := makeMinimalSpec(0, 0, 0)
	cfg := &config.Config{
		SourceDomain: "example.com", SourceZHURL: "http://example.com/%d",
		SourceENURL: "http://example.com/%d", CrawlerParallelism: 5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	err := s.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phase 1 failed")
}

// ──────────────────────────────────────────────────────────────
// crawlChapters – c.Request() error path (domain not allowed)
// ──────────────────────────────────────────────────────────────

func TestCrawlChapters_RequestURLDomainMismatch(t *testing.T) {
	repo, mock := newMockRepo(t)
	// Use a domain mismatch: allowed domain is "example.com" but URL uses "other.com"
	// → c.Request() returns an error, which is logged and skipped.
	bspec := makeMinimalSpec(0, 1, 1)
	cfg := &config.Config{
		SourceDomain: "example.com",
		// URLs point to a different domain → colly refuses to request them.
		SourceZHURL:          "http://notallowed.com/%d",
		SourceENURL:          "http://notallowed.com/%d",
		CrawlerParallelism:   1,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	s.crawlChapters([]BookMeta{{ID: uuid.New(), Index: 0}})
	// No DB calls expected since requests are blocked.
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// Run – full success path (setupBooks + crawlChapters)
// ──────────────────────────────────────────────────────────────

func TestRun_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, verseHTML)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)

	// Phase 1: setupBooks – 66 books, each found on first SELECT.
	for i := 0; i < 66; i++ {
		mock.ExpectQuery(qre(sqlSelectBook)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))
	}
	// UpsertBookContent for book[0] only (Genesis/Genesis – the only non-empty names).
	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertBookContent)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertBookContent)).WillReturnResult(sqlmock.NewResult(1, 1))

	// Phase 2: crawlChapters – book[0] has 1 chapter, 1 verse.
	chapID := uuid.New()
	secID := uuid.New()
	setupChapterMock(mock, chapID, secID)

	bspec := makeMinimalSpec(0, 1, 1)
	cfg := &config.Config{
		SourceDomain:         "127.0.0.1",
		SourceZHURL:          srv.URL + "/%d",
		SourceENURL:          srv.URL + "/%d",
		CrawlerParallelism:   1,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
		HTTPTimeoutSec:       5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	err := s.Run()
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCrawlChapters_ExcessVerseSkipped(t *testing.T) {
	// HTML has verse #5 but maxVerses=1, so verse 5 is skipped.
	excessHTML := `<html><body><ol><li value="5">extra verse</li></ol></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, excessHTML)
	}))
	t.Cleanup(srv.Close)

	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)

	// Chapter will be created, but no sections/verses.
	chapID := uuid.New()
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(`INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2) ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	mock.ExpectQuery(qre(`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
	// Chapter contents for ZH and EN
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(qre(`SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	bspec := makeMinimalSpec(0, 1, 1) // maxVerses = 1
	cfg := &config.Config{
		SourceDomain:         "127.0.0.1",
		SourceZHURL:          srv.URL + "/%d",
		SourceENURL:          srv.URL + "/%d",
		CrawlerParallelism:   1,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
		HTTPTimeoutSec:       5,
	}
	s := NewBibleScraper(repo, bspec, cfg)
	s.crawlChapters([]BookMeta{{ID: uuid.New(), Index: 0}})
	assert.NoError(t, mock.ExpectationsWereMet())
}
