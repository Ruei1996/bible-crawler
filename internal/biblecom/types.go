// Package biblecom implements an HTML crawler for bible.com, targeting the
// CUNP-上帝 (Chinese, version 414) and NIV (English, version 111) translations.
//
// The output is written to two JSON files — one per language — whose structure
// mirrors the existing springbible.fhl.net scraper's DB content, but stored
// as flat files (similar to the YouVersion JSONL checkpoint) so the crawl can
// be resumed and the data can be imported to PostgreSQL later.
package biblecom

import "time"

// OutputFile is the top-level JSON structure written to
// youversion-bible_books_zh.json and youversion-bible_books_en.json.
// It records the version metadata together with all scraped content.
type OutputFile struct {
	Version   string       `json:"version"`    // e.g. "CUNP-上帝" or "NIV"
	VersionID int          `json:"version_id"` // YouVersion Bible ID (414 or 111)
	Language  string       `json:"language"`   // "chinese" | "english"
	CrawledAt time.Time    `json:"crawled_at"` // UTC timestamp of crawl completion
	Books     []BookOutput `json:"books"`
}

// BookOutput represents one Bible book's scraped content.
type BookOutput struct {
	BookSort  int             `json:"book_sort"`  // canonical 1–66
	BookName  string          `json:"book_name"`  // localised title
	BookUSFM  string          `json:"book_usfm"`  // e.g. "GEN"
	Chapters  []ChapterOutput `json:"chapters"`
}

// ChapterOutput represents one chapter's scraped content.
type ChapterOutput struct {
	ChapterSort int           `json:"chapter_sort"` // 1-based within the book
	Verses      []VerseOutput `json:"verses"`
}

// VerseOutput represents one verse.
// SubTitle is set only for the first verse that follows a section heading;
// it maps directly to bibles.bible_section_contents.sub_title in PostgreSQL.
// Note carries optional annotation metadata:
//   - "merged"           — secondary verse in a merged-verse group (shares content with the primary)
//   - "ref:BOOK.CHAP.V" — bracket-labeled verse whose content was sourced from the referenced verse
//   - "omitted"          — bracket-labeled verse with no resolvable cross-reference (content is the extracted footnote text)
//
// Note is stored in the JSON output for auditing but is intentionally ignored
// during DB import.
// CrossRef holds the raw USFM key used to resolve the content of bracket-labeled
// verses (e.g. "MRK.9.29" for NIV Matthew 17:21). It is retained in the JSON
// after resolution so consumers can trace where the content originated.
// CrossRef is always empty for non-bracket verses.
type VerseOutput struct {
	VerseSort int    `json:"verse_sort"`          // 1-based within the chapter
	SubTitle  string `json:"sub_title,omitempty"` // section heading; absent when empty
	Content   string `json:"content"`             // verse text
	Note      string `json:"note,omitempty"`      // audit annotation; not written to DB
	CrossRef  string `json:"cross_ref,omitempty"` // source USFM for cross-referenced bracket verses; not written to DB
}

// workItem is a single unit of crawl work: one (language, book, chapter) fetch.
// Using a struct rather than a goroutine-per-URL lets the worker pool enforce
// the configured rate limit across both languages simultaneously.
type workItem struct {
	lang      string // LangChinese | LangEnglish
	bookIdx   int    // 0-based index into the Books slice
	chapSort  int    // 1-based chapter number within the book
	url       string // fully-constructed page URL
}
