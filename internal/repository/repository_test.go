package repository_test

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bible-crawler/internal/repository"
)

// newMockDB creates a sqlmock *sqlx.DB with regexp query matching (the default).
func newMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

func qre(q string) string { return regexp.QuoteMeta(q) }

var errDB = errors.New("db error")

// ──────────────────────────────────────────────────────────────
// NewBibleRepository
// ──────────────────────────────────────────────────────────────

func TestNewBibleRepository(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	assert.NotNil(t, repo)
}

// ──────────────────────────────────────────────────────────────
// GetOrCreateBook
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectBook = `SELECT id FROM bibles.bible_books WHERE sort = $1`
	sqlInsertBook = `INSERT INTO bibles.bible_books (sort) VALUES ($1) ON CONFLICT (sort) DO NOTHING RETURNING id`
)

func TestGetOrCreateBook_InvalidSort_Zero(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateBook(0)
	require.Error(t, err)
}

func TestGetOrCreateBook_InvalidSort_Negative(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateBook(-1)
	require.Error(t, err)
}

func TestGetOrCreateBook_FoundInStep1(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	bookID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(bookID))

	got, err := repo.GetOrCreateBook(1)
	require.NoError(t, err)
	assert.Equal(t, bookID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateBook_Step1DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(errDB)

	_, err := repo.GetOrCreateBook(1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateBook_InsertedInStep2(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	bookID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // ErrNoRows
	mock.ExpectQuery(qre(sqlInsertBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(bookID))

	got, err := repo.GetOrCreateBook(1)
	require.NoError(t, err)
	assert.Equal(t, bookID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateBook_Step2DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertBook)).WillReturnError(errDB)

	_, err := repo.GetOrCreateBook(1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateBook_ConflictFallbackStep3(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	bookID := uuid.New()
	// step1: not found
	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// step2: conflict, nothing returned
	mock.ExpectQuery(qre(sqlInsertBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// step3: found after conflict
	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(bookID))

	got, err := repo.GetOrCreateBook(1)
	require.NoError(t, err)
	assert.Equal(t, bookID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateBook_Step3DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlSelectBook)).WillReturnError(errDB)

	_, err := repo.GetOrCreateBook(1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// UpsertBookContent
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectBookContent = `SELECT title FROM bibles.bible_book_contents WHERE bible_book_id = $1 AND language = $2`
	sqlUpdateBookContent = `UPDATE bibles.bible_book_contents SET title = $3 WHERE bible_book_id = $1 AND language = $2`
	sqlInsertBookContent = `INSERT INTO bibles.bible_book_contents (bible_book_id, language, title) VALUES ($1, $2, $3)`
)

func TestUpsertBookContent_NilUUID(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertBookContent(uuid.Nil, "chinese", "Genesis")
	require.Error(t, err)
}

func TestUpsertBookContent_BadLang(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertBookContent(uuid.New(), "french", "Genèse")
	require.Error(t, err)
}

func TestUpsertBookContent_EmptyTitle(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertBookContent(uuid.New(), "chinese", "   ")
	require.Error(t, err)
}

func TestUpsertBookContent_QueryError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).WillReturnError(errDB)

	err := repo.UpsertBookContent(uuid.New(), "chinese", "Genesis")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBookContent_SameTitle(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}).AddRow("Genesis"))

	err := repo.UpsertBookContent(uuid.New(), "chinese", "Genesis")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBookContent_DiffTitle_Update(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}).AddRow("Old Title"))
	mock.ExpectExec(qre(sqlUpdateBookContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertBookContent(uuid.New(), "chinese", "New Title")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBookContent_DiffTitle_UpdateError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}).AddRow("Old Title"))
	mock.ExpectExec(qre(sqlUpdateBookContent)).WillReturnError(errDB)

	err := repo.UpsertBookContent(uuid.New(), "chinese", "New Title")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBookContent_Insert(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"})) // ErrNoRows
	mock.ExpectExec(qre(sqlInsertBookContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertBookContent(uuid.New(), "english", "Genesis")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertBookContent_InsertError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertBookContent)).WillReturnError(errDB)

	err := repo.UpsertBookContent(uuid.New(), "english", "Genesis")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// GetOrCreateChapter
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectChapter = `SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`
	sqlInsertChapter = `INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2) ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`
)

func TestGetOrCreateChapter_NilBookID(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateChapter(uuid.Nil, 1)
	require.Error(t, err)
}

func TestGetOrCreateChapter_InvalidSort(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateChapter(uuid.New(), 0)
	require.Error(t, err)
}

func TestGetOrCreateChapter_FoundInStep1(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	chapID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))

	got, err := repo.GetOrCreateChapter(uuid.New(), 1)
	require.NoError(t, err)
	assert.Equal(t, chapID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateChapter_Step1DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapter)).WillReturnError(errDB)

	_, err := repo.GetOrCreateChapter(uuid.New(), 1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateChapter_InsertedInStep2(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	chapID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))

	got, err := repo.GetOrCreateChapter(uuid.New(), 1)
	require.NoError(t, err)
	assert.Equal(t, chapID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateChapter_Step2DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertChapter)).WillReturnError(errDB)

	_, err := repo.GetOrCreateChapter(uuid.New(), 1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateChapter_ConflictFallbackStep3(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	chapID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(chapID))

	got, err := repo.GetOrCreateChapter(uuid.New(), 1)
	require.NoError(t, err)
	assert.Equal(t, chapID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateChapter_Step3DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertChapter)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlSelectChapter)).WillReturnError(errDB)

	_, err := repo.GetOrCreateChapter(uuid.New(), 1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// UpsertChapterContent
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectChapterContent = `SELECT title FROM bibles.bible_chapter_contents WHERE bible_chapter_id = $1 AND language = $2`
	sqlUpdateChapterContent = `UPDATE bibles.bible_chapter_contents SET title = $3 WHERE bible_chapter_id = $1 AND language = $2`
	sqlInsertChapterContent = `INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title) VALUES ($1, $2, $3)`
)

func TestUpsertChapterContent_NilUUID(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertChapterContent(uuid.Nil, "chinese", "Chapter 1")
	require.Error(t, err)
}

func TestUpsertChapterContent_BadLang(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertChapterContent(uuid.New(), "klingon", "Chapter 1")
	require.Error(t, err)
}

func TestUpsertChapterContent_EmptyTitle(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertChapterContent(uuid.New(), "english", "")
	require.Error(t, err)
}

func TestUpsertChapterContent_QueryError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapterContent)).WillReturnError(errDB)

	err := repo.UpsertChapterContent(uuid.New(), "english", "Chapter 1")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertChapterContent_SameTitle(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}).AddRow("Chapter 1"))

	err := repo.UpsertChapterContent(uuid.New(), "english", "Chapter 1")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertChapterContent_DiffTitle_Update(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}).AddRow("Old Title"))
	mock.ExpectExec(qre(sqlUpdateChapterContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertChapterContent(uuid.New(), "english", "New Title")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertChapterContent_DiffTitle_UpdateError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}).AddRow("Old Title"))
	mock.ExpectExec(qre(sqlUpdateChapterContent)).WillReturnError(errDB)

	err := repo.UpsertChapterContent(uuid.New(), "english", "New Title")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertChapterContent_Insert(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertChapterContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertChapterContent(uuid.New(), "chinese", "第 1 章")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertChapterContent_InsertError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectChapterContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertChapterContent)).WillReturnError(errDB)

	err := repo.UpsertChapterContent(uuid.New(), "chinese", "第 1 章")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// GetOrCreateSection
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectSection = `SELECT id FROM bibles.bible_sections WHERE bible_book_id = $1 AND bible_chapter_id = $2 AND sort = $3`
	sqlInsertSection = `INSERT INTO bibles.bible_sections (bible_book_id, bible_chapter_id, sort) VALUES ($1, $2, $3) ON CONFLICT (bible_book_id, bible_chapter_id, sort) DO NOTHING RETURNING id`
)

func TestGetOrCreateSection_NilBookID(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateSection(uuid.Nil, uuid.New(), 1)
	require.Error(t, err)
}

func TestGetOrCreateSection_NilChapterID(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateSection(uuid.New(), uuid.Nil, 1)
	require.Error(t, err)
}

func TestGetOrCreateSection_InvalidSort(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	_, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 0)
	require.Error(t, err)
}

func TestGetOrCreateSection_FoundInStep1(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))

	got, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 1)
	require.NoError(t, err)
	assert.Equal(t, secID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateSection_Step1DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnError(errDB)

	_, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateSection_InsertedInStep2(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))

	got, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 1)
	require.NoError(t, err)
	assert.Equal(t, secID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateSection_Step2DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).WillReturnError(errDB)

	_, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateSection_ConflictFallbackStep3(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	secID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(secID))

	got, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 1)
	require.NoError(t, err)
	assert.Equal(t, secID, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetOrCreateSection_Step3DBError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlInsertSection)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(qre(sqlSelectSection)).WillReturnError(errDB)

	_, err := repo.GetOrCreateSection(uuid.New(), uuid.New(), 1)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ──────────────────────────────────────────────────────────────
// UpsertSectionContent
// ──────────────────────────────────────────────────────────────

const (
	sqlSelectSectionContent = `SELECT title, content, sub_title FROM bibles.bible_section_contents WHERE bible_section_id = $1 AND language = $2`
	sqlUpdateSectionContent = `UPDATE bibles.bible_section_contents SET title = $3, content = $4, sub_title = $5 WHERE bible_section_id = $1 AND language = $2`
	sqlInsertSectionContent = `INSERT INTO bibles.bible_section_contents (bible_section_id, language, title, content, sub_title) VALUES ($1, $2, $3, $4, $5)`
)

func TestUpsertSectionContent_NilUUID(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertSectionContent(uuid.Nil, "chinese", "Title", "Content")
	require.Error(t, err)
}

func TestUpsertSectionContent_BadLang(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertSectionContent(uuid.New(), "latin", "Title", "Content")
	require.Error(t, err)
}

func TestUpsertSectionContent_EmptyTitle(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertSectionContent(uuid.New(), "english", "", "Content")
	require.Error(t, err)
}

func TestUpsertSectionContent_EmptyContent(t *testing.T) {
	sqlxDB, _ := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)
	err := repo.UpsertSectionContent(uuid.New(), "english", "Title", "   ")
	require.Error(t, err)
}

func TestUpsertSectionContent_QueryError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).WillReturnError(errDB)

	err := repo.UpsertSectionContent(uuid.New(), "english", "verse 1", "In the beginning")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSectionContent_SameTitleAndContent(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "sub_title"}).
			AddRow("verse 1", "In the beginning", ""))

	err := repo.UpsertSectionContent(uuid.New(), "english", "verse 1", "In the beginning")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSectionContent_ContentChanged_Update(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "sub_title"}).
			AddRow("verse 1", "old content", ""))
	mock.ExpectExec(qre(sqlUpdateSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertSectionContent(uuid.New(), "english", "verse 1", "new content")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSectionContent_UpdateError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "sub_title"}).
			AddRow("verse 1", "old content", ""))
	mock.ExpectExec(qre(sqlUpdateSectionContent)).WillReturnError(errDB)

	err := repo.UpsertSectionContent(uuid.New(), "english", "verse 1", "new content")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSectionContent_Insert(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "sub_title"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertSectionContent(uuid.New(), "chinese", "第1節", "太初，神創造天地")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertSectionContent_InsertError(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "sub_title"}))
	mock.ExpectExec(qre(sqlInsertSectionContent)).WillReturnError(errDB)

	err := repo.UpsertSectionContent(uuid.New(), "chinese", "第1節", "太初，神創造天地")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// normalizeLanguage edge: verify "CHINESE" and "ENGLISH" (upper-case) are
// accepted (normaliseLanguage lowercases before comparing).
func TestUpsertBookContent_UpperCaseLang(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectBookContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title"}))
	mock.ExpectExec(qre(sqlInsertBookContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertBookContent(uuid.New(), "CHINESE", "Genesis")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Verify that a sql.ErrNoRows from the section-content SELECT triggers an INSERT
// (covers the default branch path in UpsertSectionContent as well as the title-only
// change via Update).
func TestUpsertSectionContent_TitleChanged_Update(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	mock.ExpectQuery(qre(sqlSelectSectionContent)).
		WillReturnRows(sqlmock.NewRows([]string{"title", "content", "sub_title"}).
			AddRow("old title", "same content", ""))
	mock.ExpectExec(qre(sqlUpdateSectionContent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.UpsertSectionContent(uuid.New(), "english", "new title", "same content")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Sanity-check that sql.ErrNoRows on the book SELECT triggers the INSERT path.
func TestGetOrCreateBook_ReturnsNilError_OnSuccess(t *testing.T) {
	sqlxDB, mock := newMockDB(t)
	repo := repository.NewBibleRepository(sqlxDB)

	bookID := uuid.New()
	mock.ExpectQuery(qre(sqlSelectBook)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(bookID))

	id, err := repo.GetOrCreateBook(5)
	assert.NoError(t, err)
	assert.Equal(t, bookID, id)

	_ = sql.ErrNoRows // imported but unused otherwise
}
