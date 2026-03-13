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
type BibleSectionContent struct {
	ID             uuid.UUID `db:"id"`
	BibleSectionID uuid.UUID `db:"bible_section_id"`
	Language       string    `db:"language"`
	Title          string    `db:"title"`
	Content        string    `db:"content"`
	SubTitle       *string   `db:"sub_title"` // Nullable
}
