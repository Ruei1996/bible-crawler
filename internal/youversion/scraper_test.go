package youversion

// Tests for YouVersionScraper live in the same package so they can access
// unexported types (bookSetup, bookMeta, processChapter, saveVerse).

import (
	"fmt"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bible-crawler/internal/repository"
)

// ──────────────────────────────────────────────────────────────────────────────
// Mock BibleAPIClient
// ──────────────────────────────────────────────────────────────────────────────

// mockClient is a test double for BibleAPIClient. Each method delegates to
// the corresponding func field, which tests set to control the behaviour.
type mockClient struct {
	getBooksFunc   func(bibleID int) (*BooksResponse, error)
	getPassageFunc func(bibleID int, passageID string) (*PassageData, error)
}

func (m *mockClient) GetBooks(bibleID int) (*BooksResponse, error) {
	return m.getBooksFunc(bibleID)
}
func (m *mockClient) GetPassage(bibleID int, passageID string) (*PassageData, error) {
	return m.getPassageFunc(bibleID, passageID)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// qre returns a regexp-quoted version of q, suitable for sqlmock matchers.
func qre(q string) string {
	// sqlmock uses regexp matching; QuoteMeta escapes all metacharacters.
	// Import regexp is not needed because sqlmock.QueryMatcherRegexp does this.
	return "^" + sqlmockEscape(q) + "$"
}

// sqlmockEscape escapes special regexp characters for sqlmock.
func sqlmockEscape(s string) string {
	// regexp.QuoteMeta is the right tool but would require importing "regexp".
	// Inline the characters that appear in our SQL strings:
	// ( ) $ [ ] . * + ? { } ^ | \
	result := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '$', '[', ']', '.', '*', '+', '?', '{', '}', '^', '|', '\\':
			result = append(result, '\\')
		}
		result = append(result, c)
	}
	return string(result)
}

func newMockRepo(t *testing.T) (*repository.BibleRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return repository.NewBibleRepository(sqlx.NewDb(db, "sqlmock")), mock
}

// SQL constants matching exactly the queries issued by the repository layer.
const (
	sqlSelectBook   = `SELECT id FROM bibles.bible_books WHERE sort = $1`
	sqlInsertBook   = `INSERT INTO bibles.bible_books (sort) VALUES ($1) ON CONFLICT (sort) DO NOTHING RETURNING id`
	sqlSelectBookContent = `SELECT title FROM bibles.bible_book_contents WHERE bible_book_id = $1 AND language = $2`
	sqlInsertBookContent = `INSERT INTO bibles.bible_book_contents (bible_book_id, language, title) VALUES ($1, $2, $3)`

	sqlSelectChapter = `SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`
	sqlInsertChapter = `INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2) ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`
	sqlSelectChapterContent = `SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`
	sqlInsertChapterContent = `INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`

	sqlSelectSection = `SELECT id FROM bibles.bible_sections WHERE bible_book_id = $1 AND bible_chapter_id = $2 AND sort = $3`
	sqlInsertSection = `INSERT INTO bibles.bible_sections (bible_book_id, bible_chapter_id, sort) VALUES ($1, $2, $3) ON CONFLICT (bible_book_id, bible_chapter_id, sort) DO NOTHING RETURNING id`
	sqlSelectSectionContent = `SELECT title, content FROM bibles.bible_section_contents WHERE bible_section_id = $1 AND language = $2`
	sqlInsertSectionContent = `INSERT INTO bibles.bible_section_contents (bible_section_id, language, title, content) VALUES ($1, $2, $3, $4)`
)

// oneBook returns a minimal BooksResponse with one book and one chapter
// containing count verses.
func oneBook(bookID, title string, verseCount int) *BooksResponse {
	verses := make([]VerseData, verseCount)
	for i := range verses {
		verses[i] = VerseData{
			ID:        fmt.Sprintf("%d", i+1),
			PassageID: fmt.Sprintf("%s.1.%d", bookID, i+1),
			Title:     fmt.Sprintf("%d", i+1),
		}
	}
	return &BooksResponse{Data: []BookData{
		{
			ID:    bookID,
			Title: title,
			Chapters: []ChapterData{
				{ID: "1", PassageID: bookID + ".1", Title: "1", Verses: verses},
			},
		},
	}}
}

// expectBookInsert sets up sqlmock expectations for GetOrCreateBook(sort):
// SELECT → no rows, INSERT → returns newID.
func expectBookInsert(mock sqlmock.Sqlmock, newID uuid.UUID) {
	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
}

// expectBookContentInsert sets up sqlmock expectations for UpsertBookContent
// with a new row (SELECT → no rows, INSERT → success).
func expectBookContentInsert(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertBookContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

// expectChapterInsert sets up sqlmock for GetOrCreateChapter.
func expectChapterInsert(mock sqlmock.Sqlmock, chapID uuid.UUID) {
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))
}

// expectChapterContentInsert sets up sqlmock for UpsertChapterContent.
func expectChapterContentInsert(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertChapterContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

// expectSectionInsert sets up sqlmock for GetOrCreateSection.
func expectSectionInsert(mock sqlmock.Sqlmock, secID uuid.UUID) {
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))
}

// expectSectionContentInsert sets up sqlmock for UpsertSectionContent.
func expectSectionContentInsert(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

// ──────────────────────────────────────────────────────────────────────────────
// NewYouVersionScraper
// ──────────────────────────────────────────────────────────────────────────────

func TestNewYouVersionScraper(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{}
	s := NewYouVersionScraper(repo, client, 312, 111)
	assert.NotNil(t, s)
	assert.Equal(t, repo, s.Repo)
	assert.Equal(t, client, s.Client)
	assert.Equal(t, 312, s.ChineseBibleID)
	assert.Equal(t, 111, s.EnglishBibleID)
}

// ──────────────────────────────────────────────────────────────────────────────
// saveVerse
// ──────────────────────────────────────────────────────────────────────────────

func TestSaveVerse_EmptyContent(t *testing.T) {
	repo, _ := newMockRepo(t)
	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty passage content")
}

func TestSaveVerse_GetOrCreateSectionError(t *testing.T) {
	repo, mock := newMockRepo(t)
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnError(fmt.Errorf("db error"))

	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "verse text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetOrCreateSection")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveVerse_UpsertSectionContentError(t *testing.T) {
	repo, mock := newMockRepo(t)
	secID := uuid.New()
	expectSectionInsert(mock, secID)
	mock.ExpectQuery(qre(sqlSelectSectionContent)).WillReturnError(fmt.Errorf("db error"))

	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "verse text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UpsertSectionContent")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveVerse_SuccessEnglish(t *testing.T) {
	repo, mock := newMockRepo(t)
	secID := uuid.New()
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangEnglish, "In the beginning")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveVerse_SuccessChinese(t *testing.T) {
	repo, mock := newMockRepo(t)
	secID := uuid.New()
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	err := s.saveVerse(uuid.New(), uuid.New(), 1, LangChinese, "太初，上帝創造了天地。")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────────────────────
// processChapter
// ──────────────────────────────────────────────────────────────────────────────

func TestProcessChapter_GetOrCreateChapterError(t *testing.T) {
	repo, mock := newMockRepo(t)
	mock.ExpectQuery(qre(sqlSelectChapter)).WillReturnError(fmt.Errorf("db error"))

	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	chap := ChapterData{ID: "1", Verses: []VerseData{{ID: "1", PassageID: "GEN.1.1"}}}
	err := s.processChapter(uuid.New(), 1, chap, LangEnglish, 111)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetOrCreateChapter")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessChapter_UpsertChapterContentError_Continues(t *testing.T) {
	// UpsertChapterContent failing should not abort chapter processing.
	repo, mock := newMockRepo(t)
	chapID := uuid.New()
	secID := uuid.New()
	expectChapterInsert(mock, chapID)
	// UpsertChapterContent fails (SELECT returns DB error):
	mock.ExpectQuery(qre(sqlSelectChapterContent)).WillReturnError(fmt.Errorf("db error"))
	// Verse processing still happens:
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	client := &mockClient{
		getPassageFunc: func(bibleID int, passageID string) (*PassageData, error) {
			return &PassageData{Content: "In the beginning"}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	chap := ChapterData{ID: "1", Verses: []VerseData{{ID: "1", PassageID: "GEN.1.1"}}}
	err := s.processChapter(uuid.New(), 1, chap, LangEnglish, 111)
	require.NoError(t, err) // processChapter returns nil; content error is logged
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessChapter_InvalidVerseIDSkipped(t *testing.T) {
	repo, mock := newMockRepo(t)
	chapID := uuid.New()
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	// No section expectations — invalid verse IDs are skipped.

	s := NewYouVersionScraper(repo, &mockClient{getPassageFunc: func(int, string) (*PassageData, error) {
		return nil, fmt.Errorf("should not be called")
	}}, 312, 111)
	chap := ChapterData{
		ID: "1",
		Verses: []VerseData{
			{ID: "0", PassageID: "GEN.1.0"},   // verseNum <= 0
			{ID: "abc", PassageID: "GEN.1.abc"}, // non-numeric
		},
	}
	err := s.processChapter(uuid.New(), 1, chap, LangEnglish, 111)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessChapter_GetPassageError_Continues(t *testing.T) {
	repo, mock := newMockRepo(t)
	chapID := uuid.New()
	secID := uuid.New()
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	// Verse 1 fails GetPassage; verse 2 succeeds.
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	callCount := 0
	client := &mockClient{
		getPassageFunc: func(bibleID int, passageID string) (*PassageData, error) {
			callCount++
			if callCount == 1 {
				return nil, fmt.Errorf("api error")
			}
			return &PassageData{Content: "second verse"}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	chap := ChapterData{
		ID: "1",
		Verses: []VerseData{
			{ID: "1", PassageID: "GEN.1.1"},
			{ID: "2", PassageID: "GEN.1.2"},
		},
	}
	err := s.processChapter(uuid.New(), 1, chap, LangEnglish, 111)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessChapter_SaveVerseError_Logged(t *testing.T) {
	// saveVerse returning an error (empty content) is logged, not propagated.
	repo, mock := newMockRepo(t)
	chapID := uuid.New()
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	// No section expectations — saveVerse fails before touching DB.

	client := &mockClient{
		getPassageFunc: func(bibleID int, passageID string) (*PassageData, error) {
			return &PassageData{Content: ""}, nil // empty content triggers error
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	chap := ChapterData{
		ID:     "1",
		Verses: []VerseData{{ID: "1", PassageID: "GEN.1.1"}},
	}
	err := s.processChapter(uuid.New(), 1, chap, LangEnglish, 111)
	require.NoError(t, err) // saveVerse error is logged, not returned
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessChapter_SuccessEnglish(t *testing.T) {
	repo, mock := newMockRepo(t)
	chapID, secID := uuid.New(), uuid.New()
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	client := &mockClient{
		getPassageFunc: func(_ int, _ string) (*PassageData, error) {
			return &PassageData{Content: "In the beginning"}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	chap := ChapterData{ID: "1", Verses: []VerseData{{ID: "1", PassageID: "GEN.1.1"}}}
	err := s.processChapter(uuid.New(), 1, chap, LangEnglish, 111)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessChapter_SuccessChinese(t *testing.T) {
	repo, mock := newMockRepo(t)
	chapID, secID := uuid.New(), uuid.New()
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	client := &mockClient{
		getPassageFunc: func(_ int, _ string) (*PassageData, error) {
			return &PassageData{Content: "太初，上帝創造了天地。"}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	chap := ChapterData{ID: "1", Verses: []VerseData{{ID: "1", PassageID: "GEN.1.1"}}}
	err := s.processChapter(uuid.New(), 1, chap, LangChinese, 312)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────────────────────
// crawlVerses
// ──────────────────────────────────────────────────────────────────────────────

func TestCrawlVerses_ProcessChapterError_Logged(t *testing.T) {
	// GetOrCreateChapter failing is logged; crawlVerses still returns normally.
	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)
	// Both EN and ZH will try to create a chapter but fail.
	mock.ExpectQuery(qre(sqlSelectChapter)).WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(sqlSelectChapter)).WillReturnError(fmt.Errorf("db error"))

	bookID := uuid.New()
	setup := &bookSetup{
		metas:   []bookMeta{{id: bookID, index: 0}},
		enBooks: oneBook("GEN", "Genesis", 0).Data,
		zhBooks: oneBook("GEN", "創世記", 0).Data,
	}
	// Remove all chapters to avoid chapter DB calls, then re-add one to trigger error:
	setup.enBooks[0].Chapters = []ChapterData{{ID: "1", Verses: nil}}
	setup.zhBooks[0].Chapters = []ChapterData{{ID: "1", Verses: nil}}

	s := NewYouVersionScraper(repo, &mockClient{}, 312, 111)
	s.crawlVerses(setup) // must not panic
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCrawlVerses_Success_BothLanguages(t *testing.T) {
	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)

	chapID, secID := uuid.New(), uuid.New()
	// Chapter expectations for English:
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)
	// Chapter expectations for Chinese:
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	client := &mockClient{
		getPassageFunc: func(_ int, _ string) (*PassageData, error) {
			return &PassageData{Content: "verse text"}, nil
		},
	}

	bookID := uuid.New()
	enBooks := oneBook("GEN", "Genesis", 1)
	zhBooks := oneBook("GEN", "創世記", 1)

	setup := &bookSetup{
		metas:   []bookMeta{{id: bookID, index: 0}},
		enBooks: enBooks.Data,
		zhBooks: zhBooks.Data,
	}

	s := NewYouVersionScraper(repo, client, 312, 111)
	s.crawlVerses(setup)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────────────────────
// setupBooks
// ──────────────────────────────────────────────────────────────────────────────

func TestSetupBooks_GetBooksENError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(bibleID int) (*BooksResponse, error) {
			return nil, fmt.Errorf("network error")
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBooks(EN=111)")
}

func TestSetupBooks_GetBooksZHError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(bibleID int) (*BooksResponse, error) {
			if bibleID == 111 {
				return oneBook("GEN", "Genesis", 0), nil
			}
			return nil, fmt.Errorf("zh api error")
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBooks(ZH=312)")
}

func TestSetupBooks_BookCountMismatch(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(bibleID int) (*BooksResponse, error) {
			if bibleID == 111 {
				return &BooksResponse{Data: []BookData{{ID: "GEN"}, {ID: "EXO"}}}, nil
			}
			return &BooksResponse{Data: []BookData{{ID: "GEN"}}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "book count mismatch")
}

func TestSetupBooks_ZeroBooks(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return &BooksResponse{Data: []BookData{}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0 books")
}

func TestSetupBooks_GetOrCreateBookError_AllFail(t *testing.T) {
	repo, mock := newMockRepo(t)
	// Both GetOrCreateBook calls fail → metas stays empty → error.
	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))

	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return &BooksResponse{Data: []BookData{
				{ID: "GEN", Title: "Genesis"},
				{ID: "EXO", Title: "Exodus"},
			}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no books were successfully written")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetupBooks_GetOrCreateBookError_SomeFail(t *testing.T) {
	// Book 1 fails, book 2 succeeds → setupBooks succeeds with 1 book.
	repo, mock := newMockRepo(t)
	bookID := uuid.New()
	// Book 1 (GEN) fails:
	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))
	// Book 2 (EXO) succeeds:
	expectBookInsert(mock, bookID)
	expectBookContentInsert(mock) // EN
	expectBookContentInsert(mock) // ZH

	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return &BooksResponse{Data: []BookData{
				{ID: "GEN", Title: "Genesis or 創世記"},
				{ID: "EXO", Title: "Exodus or 出埃及記"},
			}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks()
	require.NoError(t, err)
	assert.Len(t, setup.metas, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetupBooks_UpsertBookContentENError_Continues(t *testing.T) {
	repo, mock := newMockRepo(t)
	bookID := uuid.New()
	expectBookInsert(mock, bookID)
	// EN UpsertBookContent fails:
	mock.ExpectQuery(qre(sqlSelectBookContent)).WillReturnError(fmt.Errorf("db error"))
	// ZH UpsertBookContent still runs and succeeds:
	expectBookContentInsert(mock)

	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 0), nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks()
	require.NoError(t, err) // errors are logged, not returned
	assert.Len(t, setup.metas, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetupBooks_UpsertBookContentZHError_Continues(t *testing.T) {
	repo, mock := newMockRepo(t)
	bookID := uuid.New()
	expectBookInsert(mock, bookID)
	expectBookContentInsert(mock) // EN succeeds
	// ZH UpsertBookContent fails:
	mock.ExpectQuery(qre(sqlSelectBookContent)).WillReturnError(fmt.Errorf("db error"))

	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 0), nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks()
	require.NoError(t, err)
	assert.Len(t, setup.metas, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetupBooks_Success(t *testing.T) {
	repo, mock := newMockRepo(t)
	bookID := uuid.New()
	expectBookInsert(mock, bookID)
	expectBookContentInsert(mock) // EN
	expectBookContentInsert(mock) // ZH

	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 1), nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks()
	require.NoError(t, err)
	require.Len(t, setup.metas, 1)
	assert.Equal(t, bookID, setup.metas[0].id)
	assert.Equal(t, 0, setup.metas[0].index)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────────────────────
// Run
// ──────────────────────────────────────────────────────────────────────────────

func TestRun_SetupBooksError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return nil, fmt.Errorf("api down")
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	err := s.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phase 1 failed")
}

func TestRun_Success(t *testing.T) {
	repo, mock := newMockRepo(t)
	mock.MatchExpectationsInOrder(false)

	bookID := uuid.New()
	chapID := uuid.New()
	secID := uuid.New()

	// Phase 1 — one book, EN + ZH titles:
	expectBookInsert(mock, bookID)
	expectBookContentInsert(mock) // EN
	expectBookContentInsert(mock) // ZH

	// Phase 2 — one chapter, one verse × 2 languages (EN then ZH):
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)
	expectChapterInsert(mock, chapID)
	expectChapterContentInsert(mock)
	expectSectionInsert(mock, secID)
	expectSectionContentInsert(mock)

	client := &mockClient{
		getBooksFunc: func(_ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 1), nil
		},
		getPassageFunc: func(_ int, _ string) (*PassageData, error) {
			return &PassageData{Content: "verse content"}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	err := s.Run()
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
