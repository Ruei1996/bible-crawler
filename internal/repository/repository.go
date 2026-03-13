// Package repository provides idempotent read/write operations for the bibles
// PostgreSQL schema. All write methods follow the same equality-aware pattern:
//
//  1. SELECT — return immediately if the row already exists with matching data.
//  2. INSERT (or INSERT … ON CONFLICT DO NOTHING) — write the new row.
//  3. SELECT fallback — picks up a row committed by a concurrent goroutine
//     when step 2 returns ErrNoRows (CTE snapshot race-condition safety).
//
// Structural tables (bible_books, bible_chapters, bible_sections) use the
// three-step SELECT→INSERT→SELECT pattern because they have unique constraints.
// Content tables (bible_book_contents, bible_chapter_contents,
// bible_section_contents) use a Go-level SELECT→INSERT/UPDATE because they
// have no unique constraint (only a btree index), so ON CONFLICT is invalid.
package repository

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	// Supported content languages in this crawler.
	languageChinese = "chinese"
	languageEnglish = "english"
)

// BibleRepository centralizes all write/read operations for Bible tables.
// Keeping write rules here ensures crawler and repair commands share the same
// idempotent behavior.
type BibleRepository struct {
	DB *sqlx.DB
}

// NewBibleRepository returns a repository instance backed by a sqlx DB handle.
func NewBibleRepository(db *sqlx.DB) *BibleRepository {
	return &BibleRepository{DB: db}
}

// normalizeRequired trims a field and validates it is non-empty.
func normalizeRequired(fieldName, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s must not be empty", fieldName)
	}
	return trimmed, nil
}

// normalizeLanguage validates and normalizes language values used by content tables.
func normalizeLanguage(lang string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(lang))
	switch normalized {
	case languageChinese, languageEnglish:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported language %q", lang)
	}
}

// validateUUID prevents accidental writes with a nil UUID.
func validateUUID(fieldName string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%s must not be nil uuid", fieldName)
	}
	return nil
}

// validateSort ensures chapter/verse/book sort fields remain positive.
func validateSort(fieldName string, sort int) error {
	if sort <= 0 {
		return fmt.Errorf("%s must be greater than 0", fieldName)
	}
	return nil
}

// GetOrCreateBook returns the canonical book ID for a given sort index.
// Uses a SELECT → INSERT → SELECT-fallback sequence so that concurrent
// callers never race on CTE snapshot visibility (a single-statement CTE
// evaluates the fallback SELECT with the same snapshot as the INSERT,
// making the concurrent row invisible when ON CONFLICT DO NOTHING fires).
func (r *BibleRepository) GetOrCreateBook(sort int) (uuid.UUID, error) {
	if err := validateSort("book sort", sort); err != nil {
		return uuid.Nil, err
	}

	// Step 1: fast path for re-runs — row usually already exists.
	var id uuid.UUID
	err := r.DB.QueryRow(
		`SELECT id FROM bibles.bible_books WHERE sort = $1`, sort,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("failed to query bible_book sort=%d: %w", sort, err)
	}

	// Step 2: row absent — attempt insert; DO NOTHING handles concurrent callers.
	err = r.DB.QueryRow(
		`INSERT INTO bibles.bible_books (sort) VALUES ($1)
		 ON CONFLICT (sort) DO NOTHING RETURNING id`, sort,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("failed to insert bible_book sort=%d: %w", sort, err)
	}

	// Step 3: another caller inserted concurrently; its commit is now visible
	// because this is a fresh statement with a new snapshot.
	err = r.DB.QueryRow(
		`SELECT id FROM bibles.bible_books WHERE sort = $1`, sort,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to resolve bible_book sort=%d after conflict: %w", sort, err)
	}
	return id, nil
}

// UpsertBookContent writes localized book metadata with equality-aware updates.
// Strategy (no unique constraint required):
//  1. SELECT current title for (bible_book_id, language).
//  2. Row missing → INSERT VALUES directly.
//  3. Row exists, title identical → no-op.
//  4. Row exists, title differs → UPDATE.
func (r *BibleRepository) UpsertBookContent(bookID uuid.UUID, lang, title string) error {
	if err := validateUUID("bookID", bookID); err != nil {
		return err
	}
	normalizedLang, err := normalizeLanguage(lang)
	if err != nil {
		return err
	}
	normalizedTitle, err := normalizeRequired("book title", title)
	if err != nil {
		return err
	}

	// Fetch the stored title in one round-trip.
	var storedTitle string
	err = r.DB.QueryRow(
		`SELECT title FROM bibles.bible_book_contents
		 WHERE bible_book_id = $1 AND language = $2`,
		bookID, normalizedLang,
	).Scan(&storedTitle)

	switch err {
	case nil:
		// Row exists – skip if identical, otherwise update.
		if storedTitle == normalizedTitle {
			return nil
		}
		_, err = r.DB.Exec(
			`UPDATE bibles.bible_book_contents
			 SET title = $3
			 WHERE bible_book_id = $1 AND language = $2`,
			bookID, normalizedLang, normalizedTitle,
		)
		if err != nil {
			return fmt.Errorf("failed to update bible_book_contents: %w", err)
		}
	case sql.ErrNoRows:
		// Row does not exist – insert with VALUES to avoid type-inference issues.
		_, err = r.DB.Exec(
			`INSERT INTO bibles.bible_book_contents (bible_book_id, language, title)
			 VALUES ($1, $2, $3)`,
			bookID, normalizedLang, normalizedTitle,
		)
		if err != nil {
			return fmt.Errorf("failed to insert bible_book_contents: %w", err)
		}
	default:
		return fmt.Errorf("failed to query bible_book_contents: %w", err)
	}
	return nil
}

// GetOrCreateChapter returns the canonical chapter ID under a book.
// Chinese and English requests for the same chapter run concurrently, so a
// CTE-based approach risks a snapshot race (the UNION ALL fallback SELECT uses
// the same snapshot as the INSERT and cannot see a row committed by a concurrent
// transaction after the statement began). The three-step pattern is safe because
// each SELECT is a distinct statement with its own up-to-date snapshot.
func (r *BibleRepository) GetOrCreateChapter(bookID uuid.UUID, sort int) (uuid.UUID, error) {
	if err := validateUUID("bookID", bookID); err != nil {
		return uuid.Nil, err
	}
	if err := validateSort("chapter sort", sort); err != nil {
		return uuid.Nil, err
	}

	// Step 1: fast path.
	var id uuid.UUID
	err := r.DB.QueryRow(
		`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`,
		bookID, sort,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("failed to query bible_chapter book=%s sort=%d: %w", bookID, sort, err)
	}

	// Step 2: attempt insert.
	err = r.DB.QueryRow(
		`INSERT INTO bibles.bible_chapters (bible_book_id, sort) VALUES ($1, $2)
		 ON CONFLICT (bible_book_id, sort) DO NOTHING RETURNING id`,
		bookID, sort,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("failed to insert bible_chapter book=%s sort=%d: %w", bookID, sort, err)
	}

	// Step 3: concurrent insert won — row is now committed and visible.
	err = r.DB.QueryRow(
		`SELECT id FROM bibles.bible_chapters WHERE bible_book_id = $1 AND sort = $2`,
		bookID, sort,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to resolve bible_chapter book=%s sort=%d after conflict: %w", bookID, sort, err)
	}
	return id, nil
}

// UpsertChapterContent writes localized chapter metadata with equality-aware updates.
// Strategy (no unique constraint required):
//  1. SELECT current title for (bible_chapter_id, language).
//  2. Row missing → INSERT VALUES directly.
//  3. Row exists, title identical → no-op.
//  4. Row exists, title differs → UPDATE.
func (r *BibleRepository) UpsertChapterContent(chapterID uuid.UUID, lang, title string) error {
	if err := validateUUID("chapterID", chapterID); err != nil {
		return err
	}
	normalizedLang, err := normalizeLanguage(lang)
	if err != nil {
		return err
	}
	normalizedTitle, err := normalizeRequired("chapter title", title)
	if err != nil {
		return err
	}

	var storedTitle string
	err = r.DB.QueryRow(
		`SELECT title FROM bibles.bible_chapter_contents
		 WHERE bible_chapter_id = $1 AND language = $2`,
		chapterID, normalizedLang,
	).Scan(&storedTitle)

	switch err {
	case nil:
		if storedTitle == normalizedTitle {
			return nil
		}
		_, err = r.DB.Exec(
			`UPDATE bibles.bible_chapter_contents
			 SET title = $3
			 WHERE bible_chapter_id = $1 AND language = $2`,
			chapterID, normalizedLang, normalizedTitle,
		)
		if err != nil {
			return fmt.Errorf("failed to update bible_chapter_contents: %w", err)
		}
	case sql.ErrNoRows:
		_, err = r.DB.Exec(
			`INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title)
			 VALUES ($1, $2, $3)`,
			chapterID, normalizedLang, normalizedTitle,
		)
		if err != nil {
			return fmt.Errorf("failed to insert bible_chapter_contents: %w", err)
		}
	default:
		return fmt.Errorf("failed to query bible_chapter_contents: %w", err)
	}
	return nil
}

// GetOrCreateSection returns the canonical verse row ID within a chapter.
// Chinese and English response handlers for the same chapter run concurrently
// and race on every verse sort number. The three-step SELECT→INSERT→SELECT
// pattern is used for the same snapshot-safety reasons as GetOrCreateChapter.
func (r *BibleRepository) GetOrCreateSection(bookID, chapterID uuid.UUID, sort int) (uuid.UUID, error) {
	if err := validateUUID("bookID", bookID); err != nil {
		return uuid.Nil, err
	}
	if err := validateUUID("chapterID", chapterID); err != nil {
		return uuid.Nil, err
	}
	if err := validateSort("section sort", sort); err != nil {
		return uuid.Nil, err
	}

	// Step 1: fast path.
	var id uuid.UUID
	err := r.DB.QueryRow(
		`SELECT id FROM bibles.bible_sections
		 WHERE bible_book_id = $1 AND bible_chapter_id = $2 AND sort = $3`,
		bookID, chapterID, sort,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("failed to query bible_section book=%s chap=%s sort=%d: %w", bookID, chapterID, sort, err)
	}

	// Step 2: attempt insert.
	err = r.DB.QueryRow(
		`INSERT INTO bibles.bible_sections (bible_book_id, bible_chapter_id, sort) VALUES ($1, $2, $3)
		 ON CONFLICT (bible_book_id, bible_chapter_id, sort) DO NOTHING RETURNING id`,
		bookID, chapterID, sort,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("failed to insert bible_section book=%s chap=%s sort=%d: %w", bookID, chapterID, sort, err)
	}

	// Step 3: concurrent insert won — row is now committed and visible.
	err = r.DB.QueryRow(
		`SELECT id FROM bibles.bible_sections
		 WHERE bible_book_id = $1 AND bible_chapter_id = $2 AND sort = $3`,
		bookID, chapterID, sort,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to resolve bible_section book=%s chap=%s sort=%d after conflict: %w", bookID, chapterID, sort, err)
	}
	return id, nil
}

// UpsertSectionContent writes localized verse content with equality-aware updates.
// Strategy (no unique constraint required):
//  1. SELECT current title+content for (bible_section_id, language).
//  2. Row missing → INSERT VALUES directly.
//  3. Row exists, both columns identical → no-op.
//  4. Row exists, any column differs → UPDATE.
//
// Note: sub_title is not extracted by the crawler so it is left untouched.
func (r *BibleRepository) UpsertSectionContent(sectionID uuid.UUID, lang, title, content string) error {
	if err := validateUUID("sectionID", sectionID); err != nil {
		return err
	}
	normalizedLang, err := normalizeLanguage(lang)
	if err != nil {
		return err
	}
	normalizedTitle, err := normalizeRequired("section title", title)
	if err != nil {
		return err
	}
	normalizedContent, err := normalizeRequired("section content", content)
	if err != nil {
		return err
	}

	var storedTitle, storedContent string
	err = r.DB.QueryRow(
		`SELECT title, content FROM bibles.bible_section_contents
		 WHERE bible_section_id = $1 AND language = $2`,
		sectionID, normalizedLang,
	).Scan(&storedTitle, &storedContent)

	switch err {
	case nil:
		// Row exists – skip if both values are identical.
		if storedTitle == normalizedTitle && storedContent == normalizedContent {
			return nil
		}
		_, err = r.DB.Exec(
			`UPDATE bibles.bible_section_contents
			 SET title = $3, content = $4
			 WHERE bible_section_id = $1 AND language = $2`,
			sectionID, normalizedLang, normalizedTitle, normalizedContent,
		)
		if err != nil {
			return fmt.Errorf("failed to update bible_section_contents: %w", err)
		}
	case sql.ErrNoRows:
		_, err = r.DB.Exec(
			`INSERT INTO bibles.bible_section_contents (bible_section_id, language, title, content)
			 VALUES ($1, $2, $3, $4)`,
			sectionID, normalizedLang, normalizedTitle, normalizedContent,
		)
		if err != nil {
			return fmt.Errorf("failed to insert bible_section_contents: %w", err)
		}
	default:
		return fmt.Errorf("failed to query bible_section_contents: %w", err)
	}
	return nil
}
