package youversion

// Tests for YouVersionScraper live in the same package so they can access
// unexported types (bookSetup, bookMeta).

import (
	"context"
	"fmt"
	"os"
	"regexp"
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
	getBooksFunc   func(ctx context.Context, bibleID int) (*BooksResponse, error)
	getPassageFunc func(ctx context.Context, bibleID int, passageID string) (*PassageData, error)
}

func (m *mockClient) GetBooks(ctx context.Context, bibleID int) (*BooksResponse, error) {
	return m.getBooksFunc(ctx, bibleID)
}
func (m *mockClient) GetPassage(ctx context.Context, bibleID int, passageID string) (*PassageData, error) {
	return m.getPassageFunc(ctx, bibleID, passageID)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// qre returns a regexp-anchored, metacharacter-escaped version of q for sqlmock.
func qre(q string) string {
	return "^" + regexp.QuoteMeta(q) + "$"
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
	sqlSelectBook        = `SELECT id FROM bibles.bible_books WHERE sort = $1`
	sqlInsertBook        = `INSERT INTO bibles.bible_books (sort) VALUES ($1) ON CONFLICT (sort) DO NOTHING RETURNING id`
	sqlSelectBookContent = `SELECT title FROM bibles.bible_book_contents WHERE bible_book_id = $1 AND language = $2`
	sqlInsertBookContent = `INSERT INTO bibles.bible_book_contents (bible_book_id, language, title) VALUES ($1, $2, $3)`
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
	// Parallel-mode fields must be set by the caller before Run/RunWithContext.
	assert.Nil(t, s.Checkpoint)
}

// ──────────────────────────────────────────────────────────────────────────────
// setupBooks
// ──────────────────────────────────────────────────────────────────────────────

func TestSetupBooks_GetBooksENError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ context.Context, bibleID int) (*BooksResponse, error) {
			return nil, fmt.Errorf("network error")
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBooks(EN=111)")
}

func TestSetupBooks_GetBooksZHError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ context.Context, bibleID int) (*BooksResponse, error) {
			if bibleID == 111 {
				return oneBook("GEN", "Genesis", 0), nil
			}
			return nil, fmt.Errorf("zh api error")
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBooks(ZH=312)")
}

func TestSetupBooks_BookCountMismatch(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ context.Context, bibleID int) (*BooksResponse, error) {
			if bibleID == 111 {
				return &BooksResponse{Data: []BookData{{ID: "GEN"}, {ID: "EXO"}}}, nil
			}
			return &BooksResponse{Data: []BookData{{ID: "GEN"}}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "book count mismatch")
}

func TestSetupBooks_ZeroBooks(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return &BooksResponse{Data: []BookData{}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0 books")
}

func TestSetupBooks_GetOrCreateBookError_AllFail(t *testing.T) {
	repo, mock := newMockRepo(t)
	// Both GetOrCreateBook calls fail → metas stays empty → error.
	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))
	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(fmt.Errorf("db error"))

	client := &mockClient{
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return &BooksResponse{Data: []BookData{
				{ID: "GEN", Title: "Genesis"},
				{ID: "EXO", Title: "Exodus"},
			}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	_, err := s.setupBooks(context.Background())
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
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return &BooksResponse{Data: []BookData{
				{ID: "GEN", Title: "Genesis or 創世記"},
				{ID: "EXO", Title: "Exodus or 出埃及記"},
			}}, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks(context.Background())
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
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 0), nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks(context.Background())
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
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 0), nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks(context.Background())
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
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return oneBook("GEN", "Genesis", 1), nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	setup, err := s.setupBooks(context.Background())
	require.NoError(t, err)
	require.Len(t, setup.metas, 1)
	assert.Equal(t, bookID, setup.metas[0].id)
	assert.Equal(t, 0, setup.metas[0].index)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────────────────────
// Run / RunWithContext
// ──────────────────────────────────────────────────────────────────────────────

// TestRun_SetupBooksError verifies that a Phase 1 failure is wrapped and
// propagated as "phase 1 failed: ...". The nil-check for Checkpoint now runs
// BEFORE setupBooks, so a valid checkpoint must be provided to reach Phase 1.
func TestRun_SetupBooksError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			return nil, fmt.Errorf("api down")
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)

	// Provide a real (empty) checkpoint so the nil-check passes and the error
	// originates from setupBooks (Phase 1), not the checkpoint guard.
	f, err := os.CreateTemp(t.TempDir(), "ckpt-*.jsonl")
	require.NoError(t, err)
	f.Close()
	ckpt, err := NewCheckpoint(f.Name())
	require.NoError(t, err)
	defer ckpt.Close()
	s.Checkpoint = ckpt

	runErr := s.Run()
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "phase 1 failed")
}

// TestRun_NoCheckpointError verifies that RunWithContext returns an error when
// Checkpoint is nil. The nil-check now runs BEFORE setupBooks, so no DB
// interactions occur and no mock expectations are needed.
func TestRun_NoCheckpointError(t *testing.T) {
	repo, _ := newMockRepo(t)
	client := &mockClient{
		getBooksFunc: func(_ context.Context, _ int) (*BooksResponse, error) {
			t.Error("GetBooks should not be called when Checkpoint is nil")
			return nil, nil
		},
	}
	s := NewYouVersionScraper(repo, client, 312, 111)
	// Checkpoint is nil — RunWithContext must return an error immediately.
	err := s.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Checkpoint is required")
}
