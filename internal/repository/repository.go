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
	"github.com/lib/pq"
)

const (
	// Supported content languages in this crawler. These values are stored
	// verbatim in the `language` column of all _contents tables and must
	// match the language keys used by every crawler (scraper, youversion, biblecom).
	languageChinese = "chinese"
	languageEnglish = "english"
)

// BibleRepository centralizes all write/read operations for Bible tables.
// Keeping write rules here ensures the crawler command shares a consistent
// idempotent data-access layer.
type BibleRepository struct {
	DB *sqlx.DB
}

// NewBibleRepository returns a repository instance backed by a sqlx DB handle.
func NewBibleRepository(db *sqlx.DB) *BibleRepository {
	return &BibleRepository{DB: db}
}

// Input-length limits shared by both the scalar and bulk upsert methods.
// Limits are intentionally generous for any real Bible content (CWE-400).
const (
	maxTitleBytes    = 512        // FormatVerseTitle / FormatChapterTitle never approach this
	maxContentBytes  = 64 * 1024 // 64 KiB — far beyond the longest real verse
	maxSubTitleBytes = 1024      // Pericope headings are short; 1 KiB is ample
)

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
// sub_title is left empty (the springbible and YouVersion crawlers do not extract
// section headings). Use UpsertSectionContentFull when sub_title is available.
func (r *BibleRepository) UpsertSectionContent(sectionID uuid.UUID, lang, title, content string) error {
	return r.UpsertSectionContentFull(sectionID, lang, title, content, "")
}

// UpsertSectionContentFull is the full-parameter variant of UpsertSectionContent
// that also persists the optional sub_title column. It is used by the biblecom
// importer which extracts section headings (pericopes) from the HTML.
//
// sub_title may be empty; in that case the column is stored as an empty string
// (not NULL) for consistency with rows written by UpsertSectionContent.
//
// Strategy (mirrors UpsertSectionContent):
//  1. SELECT current title, content, sub_title for (bible_section_id, language).
//  2. Row missing → INSERT with all four values.
//  3. Row exists, all three columns identical → no-op.
//  4. Row exists, any column differs → UPDATE all three.
func (r *BibleRepository) UpsertSectionContentFull(sectionID uuid.UUID, lang, title, content, subTitle string) error {
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
	// sub_title is optional; normalise whitespace but do not reject empty values.
	normalizedSubTitle := strings.TrimSpace(subTitle)
	// Guard input lengths against tampered JSON files (CWE-400).
	if len(normalizedTitle) > maxTitleBytes {
		return fmt.Errorf("title too long (%d bytes, max %d) for section %s",
			len(normalizedTitle), maxTitleBytes, sectionID)
	}
	if len(normalizedContent) > maxContentBytes {
		return fmt.Errorf("content too long (%d bytes, max %d) for section %s",
			len(normalizedContent), maxContentBytes, sectionID)
	}
	if len(normalizedSubTitle) > maxSubTitleBytes {
		return fmt.Errorf("sub_title too long (%d bytes, max %d) for section %s",
			len(normalizedSubTitle), maxSubTitleBytes, sectionID)
	}

	// sub_title is a nullable column; rows written by earlier crawlers
	// (springbible, YouVersion) have sub_title = NULL. Scanning NULL into a
	// plain string would cause a conversion error, so sql.NullString is used.
	var storedTitle, storedContent string
	var storedSubTitle sql.NullString
	err = r.DB.QueryRow(
		`SELECT title, content, sub_title FROM bibles.bible_section_contents
		 WHERE bible_section_id = $1 AND language = $2`,
		sectionID, normalizedLang,
	).Scan(&storedTitle, &storedContent, &storedSubTitle)

	// When NullString.Valid is false (legacy NULL row from an earlier crawler),
	// .String is "" — matching the normalizedSubTitle value this function always
	// writes as "" rather than NULL. This means no IS NULL logic is ever needed
	// on re-runs: plain string equality handles both new and legacy rows.
	storedSub := storedSubTitle.String

	switch err {
	case nil:
		// Row exists. Skip only when all three values are identical AND the
		// stored sub_title is already a proper empty string (not SQL NULL).
		// Legacy rows written by earlier crawlers (springbible, YouVersion)
		// have sub_title = NULL. Treating NULL == "" in the equality check
		// would leave those rows permanently as NULL while new rows get "".
		// By also checking storedSubTitle.Valid we force a one-time UPDATE that
		// normalises NULL → "" on the first biblecom-importer run, so downstream
		// consumers can rely on plain string equality (WHERE sub_title = '')
		// without needing IS NULL guards.
		if storedTitle == normalizedTitle &&
			storedContent == normalizedContent &&
			storedSub == normalizedSubTitle &&
			storedSubTitle.Valid {
			return nil
		}
		// Update all three columns together even when only one changed.
		// A partial-update path would add branching complexity with no
		// meaningful gain at this write volume.
		_, err = r.DB.Exec(
			`UPDATE bibles.bible_section_contents
			 SET title = $3, content = $4, sub_title = $5
			 WHERE bible_section_id = $1 AND language = $2`,
			sectionID, normalizedLang, normalizedTitle, normalizedContent, normalizedSubTitle,
		)
		if err != nil {
			return fmt.Errorf("failed to update bible_section_contents: %w", err)
		}
	case sql.ErrNoRows:
		// Store "" rather than NULL for sub_title so every future SELECT+compare
		// cycle uses plain string equality without IS NULL / IS NOT NULL handling.
		_, err = r.DB.Exec(
			`INSERT INTO bibles.bible_section_contents (bible_section_id, language, title, content, sub_title)
			 VALUES ($1, $2, $3, $4, $5)`,
			sectionID, normalizedLang, normalizedTitle, normalizedContent, normalizedSubTitle,
		)
		if err != nil {
			return fmt.Errorf("failed to insert bible_section_contents: %w", err)
		}
	default:
		return fmt.Errorf("failed to query bible_section_contents: %w", err)
	}
	return nil
}

// ── Bulk helpers (biblecom-importer performance path) ────────────────────────
//
// Each Bulk* method reduces O(N) individual round-trips to O(1) by batching
// all records for a book into one INSERT (using UNNEST) plus one SELECT.
// Over a high-latency VPN connection (≥10 ms per round-trip) this makes the
// difference between hours and seconds for a full-canon import.

// ChapterContentRecord holds the fields for a single bible_chapter_contents row.
type ChapterContentRecord struct {
	ChapterID uuid.UUID
	Lang      string
	Title     string
}

// SectionContentRecord holds the fields for a single bible_section_contents row.
type SectionContentRecord struct {
	SectionID uuid.UUID
	Lang      string
	Title     string
	Content   string
	SubTitle  string
}

// VerseKey identifies a verse by its (chapter sort, verse sort) pair within a
// single book. Used as a map key returned by BulkGetOrCreateSections.
type VerseKey struct {
	ChapSort  int
	VerseSort int
}

// BulkGetOrCreateChapters creates or resolves multiple chapter rows for a single
// book in two round-trips:
//  1. INSERT … ON CONFLICT DO NOTHING for all missing chapters.
//  2. SELECT all chapters for the book to collect their UUIDs.
//
// Returns a map from chapter sort order to its UUID.
func (r *BibleRepository) BulkGetOrCreateChapters(bookID uuid.UUID, sorts []int) (map[int]uuid.UUID, error) {
	if len(sorts) == 0 {
		return nil, nil
	}
	// bookID is a constant scalar for this call — broadcast via SQL rather than
	// allocating a []string of N identical values (one per chapter).
	_, err := r.DB.Exec(`
		INSERT INTO bibles.bible_chapters (bible_book_id, sort)
		SELECT $1::uuid, unnest($2::int[])
		ON CONFLICT (bible_book_id, sort) DO NOTHING`,
		bookID, pq.Array(sorts),
	)
	if err != nil {
		return nil, fmt.Errorf("BulkGetOrCreateChapters INSERT: %w", err)
	}

	// Scope the SELECT to the requested sorts only so that re-runs over a
	// partially-imported book do not transfer every previously-inserted chapter.
	rows, err := r.DB.Query(`
		SELECT id, sort FROM bibles.bible_chapters
		WHERE bible_book_id = $1 AND sort = ANY($2::int[])`,
		bookID, pq.Array(sorts),
	)
	if err != nil {
		return nil, fmt.Errorf("BulkGetOrCreateChapters SELECT: %w", err)
	}
	defer rows.Close()

	result := make(map[int]uuid.UUID, len(sorts))
	for rows.Next() {
		var id uuid.UUID
		var sort int
		if err := rows.Scan(&id, &sort); err != nil {
			return nil, fmt.Errorf("BulkGetOrCreateChapters scan: %w", err)
		}
		result[sort] = id
	}
	return result, rows.Err()
}

// BulkGetOrCreateSections creates or resolves multiple section rows for a
// single book in two round-trips:
//  1. INSERT … ON CONFLICT DO NOTHING for all missing sections.
//  2. SELECT all sections for the affected chapters to collect their UUIDs.
//
// chapSortToID must map every chapter sort that appears in verses to its UUID.
// Returns a map from VerseKey{ChapSort, VerseSort} to section UUID.
func (r *BibleRepository) BulkGetOrCreateSections(
	bookID uuid.UUID,
	chapSortToID map[int]uuid.UUID,
	verses []VerseKey,
) (map[VerseKey]uuid.UUID, error) {
	if len(verses) == 0 {
		return nil, nil
	}
	// Validate bookID upfront — a nil UUID would create corrupt foreign-key rows
	// silently because postgres accepts uuid_nil as a valid UUID value (CWE-20).
	if err := validateUUID("bookID", bookID); err != nil {
		return nil, fmt.Errorf("BulkGetOrCreateSections: %w", err)
	}
	// bookID is a constant scalar — use unnest for the two varying columns only.
	chapIDs := make([]string, len(verses))
	verseSorts := make([]int, len(verses))
	for i, v := range verses {
		// Guard against a missing chapter entry. A missing key returns uuid.Nil,
		// which would produce a corrupt foreign-key row without any DB error (CWE-476).
		chapID, ok := chapSortToID[v.ChapSort]
		if !ok {
			return nil, fmt.Errorf("BulkGetOrCreateSections: no chapter UUID for sort=%d (book=%s)", v.ChapSort, bookID)
		}
		chapIDs[i] = chapID.String()
		verseSorts[i] = v.VerseSort
	}
	_, err := r.DB.Exec(`
		INSERT INTO bibles.bible_sections (bible_book_id, bible_chapter_id, sort)
		SELECT $1::uuid, unnest($2::uuid[]), unnest($3::int[])
		ON CONFLICT (bible_book_id, bible_chapter_id, sort) DO NOTHING`,
		bookID, pq.Array(chapIDs), pq.Array(verseSorts),
	)
	if err != nil {
		return nil, fmt.Errorf("BulkGetOrCreateSections INSERT: %w", err)
	}

	// Build the unique chapter ID list for the SELECT filter.
	// Use map[uuid.UUID]int for the reverse-lookup — uuid.UUID is [16]byte and
	// directly hashable, avoiding .String() allocation in the hot scan loop.
	chapIDStrs := make([]string, 0, len(chapSortToID))
	chapUUIDToSort := make(map[uuid.UUID]int, len(chapSortToID))
	for sort, id := range chapSortToID {
		chapIDStrs = append(chapIDStrs, id.String())
		chapUUIDToSort[id] = sort
	}
	rows, err := r.DB.Query(`
		SELECT id, bible_chapter_id, sort
		FROM bibles.bible_sections
		WHERE bible_book_id = $1 AND bible_chapter_id = ANY($2::uuid[])`,
		bookID, pq.Array(chapIDStrs),
	)
	if err != nil {
		return nil, fmt.Errorf("BulkGetOrCreateSections SELECT: %w", err)
	}
	defer rows.Close()

	result := make(map[VerseKey]uuid.UUID, len(verses))
	for rows.Next() {
		var id, chapID uuid.UUID
		var sort int
		if err := rows.Scan(&id, &chapID, &sort); err != nil {
			return nil, fmt.Errorf("BulkGetOrCreateSections scan: %w", err)
		}
		chapSort := chapUUIDToSort[chapID] // no .String() allocation
		result[VerseKey{ChapSort: chapSort, VerseSort: sort}] = id
	}
	return result, rows.Err()
}

// BulkUpsertChapterContents efficiently upserts a batch of chapter content
// rows in two or three round-trips regardless of batch size:
//  1. SELECT existing rows to detect inserts vs updates.
//  2. Batch INSERT new rows using UNNEST.
//  3. Batch UPDATE changed rows using UNNEST (typically empty on re-runs).
//
// All records must share the same Lang value.
func (r *BibleRepository) BulkUpsertChapterContents(records []ChapterContentRecord) error {
	if len(records) == 0 {
		return nil
	}
	// Validate and normalise the shared language value (CWE-20).
	// All records must carry the same Lang; use the first to derive it.
	lang, err := normalizeLanguage(records[0].Lang)
	if err != nil {
		return fmt.Errorf("BulkUpsertChapterContents: %w", err)
	}

	// Collect chapter IDs for the bulk SELECT; validate each record up front.
	chapIDStrs := make([]string, len(records))
	for i, rec := range records {
		if err := validateUUID("ChapterID", rec.ChapterID); err != nil {
			return fmt.Errorf("BulkUpsertChapterContents record[%d]: %w", i, err)
		}
		if _, err := normalizeRequired("chapter title", rec.Title); err != nil {
			return fmt.Errorf("BulkUpsertChapterContents record[%d]: %w", i, err)
		}
		if len(rec.Title) > maxTitleBytes {
			return fmt.Errorf("BulkUpsertChapterContents record[%d]: title too long (%d bytes)", i, len(rec.Title))
		}
		chapIDStrs[i] = rec.ChapterID.String()
	}
	rows, err := r.DB.Query(`
		SELECT bible_chapter_id, title
		FROM bibles.bible_chapter_contents
		WHERE language = $1 AND bible_chapter_id = ANY($2::uuid[])`,
		lang, pq.Array(chapIDStrs),
	)
	if err != nil {
		return fmt.Errorf("BulkUpsertChapterContents SELECT: %w", err)
	}
	defer rows.Close()
	// uuid.UUID is [16]byte — directly hashable; avoids .String() per lookup.
	existing := make(map[uuid.UUID]string, len(records))
	for rows.Next() {
		var chapID uuid.UUID
		var title string
		if err := rows.Scan(&chapID, &title); err != nil {
			return fmt.Errorf("BulkUpsertChapterContents scan: %w", err)
		}
		existing[chapID] = title
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("BulkUpsertChapterContents SELECT rows.Err: %w", err)
	}

	// Pre-size: first run inserts everything; re-runs update almost nothing.
	toInsert := make([]ChapterContentRecord, 0, len(records))
	toUpdate := make([]ChapterContentRecord, 0, 8)
	for _, rec := range records {
		storedTitle, exists := existing[rec.ChapterID] // no .String() allocation
		if !exists {
			toInsert = append(toInsert, rec)
		} else if storedTitle != rec.Title {
			toUpdate = append(toUpdate, rec)
		}
	}
	if len(toInsert) > 0 {
		ids := make([]string, len(toInsert))
		titles := make([]string, len(toInsert))
		for i, rec := range toInsert {
			ids[i] = rec.ChapterID.String()
			titles[i] = rec.Title
		}
		_, err = r.DB.Exec(`
			INSERT INTO bibles.bible_chapter_contents (bible_chapter_id, language, title)
			SELECT unnest($1::uuid[]), $2, unnest($3::text[])`,
			pq.Array(ids), lang, pq.Array(titles),
		)
		if err != nil {
			return fmt.Errorf("BulkUpsertChapterContents INSERT: %w", err)
		}
	}
	if len(toUpdate) > 0 {
		ids := make([]string, len(toUpdate))
		titles := make([]string, len(toUpdate))
		for i, rec := range toUpdate {
			ids[i] = rec.ChapterID.String()
			titles[i] = rec.Title
		}
		_, err = r.DB.Exec(`
			UPDATE bibles.bible_chapter_contents AS bcc
			SET title = v.title
			FROM (SELECT unnest($1::uuid[]) AS chap_id, unnest($2::text[]) AS title) AS v
			WHERE bcc.bible_chapter_id = v.chap_id AND bcc.language = $3`,
			pq.Array(ids), pq.Array(titles), lang,
		)
		if err != nil {
			return fmt.Errorf("BulkUpsertChapterContents UPDATE: %w", err)
		}
	}
	return nil
}

// BulkUpsertSectionContents efficiently upserts a batch of section content
// rows in two or three round-trips regardless of batch size:
//  1. SELECT existing rows to detect inserts vs updates.
//  2. Batch INSERT new rows using UNNEST.
//  3. Batch UPDATE changed rows using UNNEST (typically empty on re-runs).
//
// All records must share the same Lang value.
func (r *BibleRepository) BulkUpsertSectionContents(records []SectionContentRecord) error {
	if len(records) == 0 {
		return nil
	}
	// Validate and normalise the shared language value (CWE-20).
	lang, err := normalizeLanguage(records[0].Lang)
	if err != nil {
		return fmt.Errorf("BulkUpsertSectionContents: %w", err)
	}

	// Collect section IDs for the bulk SELECT; validate each record up front.
	secIDStrs := make([]string, len(records))
	for i, rec := range records {
		if err := validateUUID("SectionID", rec.SectionID); err != nil {
			return fmt.Errorf("BulkUpsertSectionContents record[%d]: %w", i, err)
		}
		if _, err := normalizeRequired("section title", rec.Title); err != nil {
			return fmt.Errorf("BulkUpsertSectionContents record[%d]: %w", i, err)
		}
		if _, err := normalizeRequired("section content", rec.Content); err != nil {
			return fmt.Errorf("BulkUpsertSectionContents record[%d]: %w", i, err)
		}
		if len(rec.Title) > maxTitleBytes {
			return fmt.Errorf("BulkUpsertSectionContents record[%d]: title too long (%d bytes)", i, len(rec.Title))
		}
		if len(rec.Content) > maxContentBytes {
			return fmt.Errorf("BulkUpsertSectionContents record[%d]: content too long (%d bytes)", i, len(rec.Content))
		}
		if len(rec.SubTitle) > maxSubTitleBytes {
			return fmt.Errorf("BulkUpsertSectionContents record[%d]: sub_title too long (%d bytes)", i, len(rec.SubTitle))
		}
		secIDStrs[i] = rec.SectionID.String()
	}
	rows, err := r.DB.Query(`
		SELECT bible_section_id, title, content, COALESCE(sub_title, '')
		FROM bibles.bible_section_contents
		WHERE language = $1 AND bible_section_id = ANY($2::uuid[])`,
		lang, pq.Array(secIDStrs),
	)
	if err != nil {
		return fmt.Errorf("BulkUpsertSectionContents SELECT: %w", err)
	}
	defer rows.Close()
	// uuid.UUID is [16]byte — directly hashable; avoids .String() per lookup.
	type existingRow struct{ title, content, subTitle string }
	existing := make(map[uuid.UUID]existingRow, len(records))
	for rows.Next() {
		var secID uuid.UUID
		var row existingRow
		if err := rows.Scan(&secID, &row.title, &row.content, &row.subTitle); err != nil {
			return fmt.Errorf("BulkUpsertSectionContents scan: %w", err)
		}
		existing[secID] = row
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("BulkUpsertSectionContents SELECT rows.Err: %w", err)
	}

	// Pre-size: first run inserts everything; re-runs update almost nothing.
	toInsert := make([]SectionContentRecord, 0, len(records))
	toUpdate := make([]SectionContentRecord, 0, 8)
	for _, rec := range records {
		ex, exists := existing[rec.SectionID] // no .String() allocation
		if !exists {
			toInsert = append(toInsert, rec)
		} else if ex.title != rec.Title || ex.content != rec.Content || ex.subTitle != rec.SubTitle {
			toUpdate = append(toUpdate, rec)
		}
	}
	if len(toInsert) > 0 {
		ids := make([]string, len(toInsert))
		titles := make([]string, len(toInsert))
		contents := make([]string, len(toInsert))
		subTitles := make([]string, len(toInsert))
		for i, rec := range toInsert {
			ids[i] = rec.SectionID.String()
			titles[i] = rec.Title
			contents[i] = rec.Content
			subTitles[i] = rec.SubTitle
		}
		_, err = r.DB.Exec(`
			INSERT INTO bibles.bible_section_contents (bible_section_id, language, title, content, sub_title)
			SELECT unnest($1::uuid[]), $2, unnest($3::text[]), unnest($4::text[]), unnest($5::text[])`,
			pq.Array(ids), lang, pq.Array(titles), pq.Array(contents), pq.Array(subTitles),
		)
		if err != nil {
			return fmt.Errorf("BulkUpsertSectionContents INSERT: %w", err)
		}
	}
	if len(toUpdate) > 0 {
		ids := make([]string, len(toUpdate))
		titles := make([]string, len(toUpdate))
		contents := make([]string, len(toUpdate))
		subTitles := make([]string, len(toUpdate))
		for i, rec := range toUpdate {
			ids[i] = rec.SectionID.String()
			titles[i] = rec.Title
			contents[i] = rec.Content
			subTitles[i] = rec.SubTitle
		}
		// Parameters are ordered sequentially ($1–$5) to match the chapter bulk
		// UPDATE convention and eliminate the non-sequential $1,$3,$4,$5 hole.
		_, err = r.DB.Exec(`
			UPDATE bibles.bible_section_contents AS bsc
			SET title = v.title, content = v.content, sub_title = v.sub_title
			FROM (
				SELECT unnest($1::uuid[]) AS sec_id,
				       unnest($2::text[]) AS title,
				       unnest($3::text[]) AS content,
				       unnest($4::text[]) AS sub_title
			) AS v
			WHERE bsc.bible_section_id = v.sec_id AND bsc.language = $5`,
			pq.Array(ids), pq.Array(titles), pq.Array(contents), pq.Array(subTitles), lang,
		)
		if err != nil {
			return fmt.Errorf("BulkUpsertSectionContents UPDATE: %w", err)
		}
	}
	return nil
}
