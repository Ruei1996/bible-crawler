// Package model defines the Go struct types that map to the bibles schema tables.
// Tags use the sqlx `db:` convention so sqlx.StructScan and NamedExec work directly.
//
// Schema relationship (all under the "bibles" PostgreSQL schema):
//
//	bible_books          (1)──(N) bible_chapters
//	bible_chapters       (1)──(N) bible_sections
//	bible_books          (1)──(N) bible_book_contents     [one per language]
//	bible_chapters       (1)──(N) bible_chapter_contents  [one per language]
//	bible_sections       (1)──(N) bible_section_contents  [one per language]
//
// The "sort" column on bible_books, bible_chapters, and bible_sections is the
// stable 1-based canonical index used across all crawlers (1-66 for books,
// 1-N for chapters, 1-M for verses). UUIDs are assigned by gen_random_uuid()
// on insert and change on every TRUNCATE + re-crawl; sort values never change.
package model

import "github.com/google/uuid"

// BibleBook maps to bibles.bible_books.
type BibleBook struct {
	ID   uuid.UUID `db:"id"`
	Sort int       `db:"sort"`
}

// BibleBookContent maps to bibles.bible_book_contents.
type BibleBookContent struct {
	ID          uuid.UUID `db:"id"`
	BibleBookID uuid.UUID `db:"bible_book_id"`
	Language    string    `db:"language"`
	Title       string    `db:"title"`
}

// BibleChapter maps to bibles.bible_chapters.
type BibleChapter struct {
	ID          uuid.UUID `db:"id"`
	BibleBookID uuid.UUID `db:"bible_book_id"`
	Sort        int       `db:"sort"`
}

// BibleChapterContent maps to bibles.bible_chapter_contents.
type BibleChapterContent struct {
	ID             uuid.UUID `db:"id"`
	BibleChapterID uuid.UUID `db:"bible_chapter_id"`
	Language       string    `db:"language"`
	Title          string    `db:"title"`
}

// BibleSection maps to bibles.bible_sections (one verse row per chapter + sort).
type BibleSection struct {
	ID             uuid.UUID `db:"id"`
	BibleBookID    uuid.UUID `db:"bible_book_id"`
	BibleChapterID uuid.UUID `db:"bible_chapter_id"`
	Sort           int       `db:"sort"`
}

// BibleSectionContent maps to bibles.bible_section_contents.
// One row exists per (section, language) pair.
// Title holds the verse number heading (e.g. "第1節" / "verse 1").
// Content holds the verse text.
// SubTitle is nullable: springbible and YouVersion rows leave it NULL; biblecom
// rows store the pericope heading (e.g. "The Creation") for the verse that
// immediately follows a section heading in the source HTML, or an empty string
// for all other verses. NULL vs "" is normalised on import by UpsertSectionContentFull.
type BibleSectionContent struct {
	ID             uuid.UUID `db:"id"`
	BibleSectionID uuid.UUID `db:"bible_section_id"`
	Language       string    `db:"language"`
	Title          string    `db:"title"`
	Content        string    `db:"content"`
	SubTitle       *string   `db:"sub_title"` // Nullable; "" after biblecom import normalisation
}
