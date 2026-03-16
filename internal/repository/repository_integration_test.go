//go:build integration

package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bible-crawler/internal/repository"
	"bible-crawler/internal/testhelper"
)

func TestGetOrCreateBook_Integration(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)

	// First call: creates the book.
	id1, err := repo.GetOrCreateBook(1)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id1)

	// Second call: returns the same ID (idempotent).
	id2, err := repo.GetOrCreateBook(1)
	require.NoError(t, err)
	assert.Equal(t, id1, id2)
}

func TestUpsertBookContent_Integration(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)
	bookID, err := repo.GetOrCreateBook(2)
	require.NoError(t, err)

	// Insert.
	require.NoError(t, repo.UpsertBookContent(bookID, "chinese", "出埃及記"))
	// Same title → no-op.
	require.NoError(t, repo.UpsertBookContent(bookID, "chinese", "出埃及記"))
	// Different title → update.
	require.NoError(t, repo.UpsertBookContent(bookID, "chinese", "Exodus ZH"))
	// English insert.
	require.NoError(t, repo.UpsertBookContent(bookID, "english", "Exodus"))
}

func TestGetOrCreateChapter_Integration(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)
	bookID, err := repo.GetOrCreateBook(3)
	require.NoError(t, err)

	// Create chapter.
	chapID1, err := repo.GetOrCreateChapter(bookID, 1)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, chapID1)

	// Idempotent.
	chapID2, err := repo.GetOrCreateChapter(bookID, 1)
	require.NoError(t, err)
	assert.Equal(t, chapID1, chapID2)
}

func TestUpsertChapterContent_Integration(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)
	bookID, err := repo.GetOrCreateBook(4)
	require.NoError(t, err)
	chapID, err := repo.GetOrCreateChapter(bookID, 1)
	require.NoError(t, err)

	require.NoError(t, repo.UpsertChapterContent(chapID, "english", "Chapter 1"))
	require.NoError(t, repo.UpsertChapterContent(chapID, "english", "Chapter 1"))   // same
	require.NoError(t, repo.UpsertChapterContent(chapID, "english", "Chapter One")) // update
	require.NoError(t, repo.UpsertChapterContent(chapID, "chinese", "第 1 章"))
}

func TestGetOrCreateSection_Integration(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)
	bookID, err := repo.GetOrCreateBook(5)
	require.NoError(t, err)
	chapID, err := repo.GetOrCreateChapter(bookID, 1)
	require.NoError(t, err)

	secID1, err := repo.GetOrCreateSection(bookID, chapID, 1)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, secID1)

	secID2, err := repo.GetOrCreateSection(bookID, chapID, 1)
	require.NoError(t, err)
	assert.Equal(t, secID1, secID2)
}

func TestUpsertSectionContent_Integration(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)
	bookID, err := repo.GetOrCreateBook(6)
	require.NoError(t, err)
	chapID, err := repo.GetOrCreateChapter(bookID, 1)
	require.NoError(t, err)
	secID, err := repo.GetOrCreateSection(bookID, chapID, 1)
	require.NoError(t, err)

	// Insert.
	require.NoError(t, repo.UpsertSectionContent(secID, "english", "verse 1", "In the beginning"))
	// Same → no-op.
	require.NoError(t, repo.UpsertSectionContent(secID, "english", "verse 1", "In the beginning"))
	// Update.
	require.NoError(t, repo.UpsertSectionContent(secID, "english", "verse 1", "Updated content"))
	// Chinese.
	require.NoError(t, repo.UpsertSectionContent(secID, "chinese", "第1節", "太初，神創造天地"))
}
