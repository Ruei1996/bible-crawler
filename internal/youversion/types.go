// Package youversion provides types and an HTTP client for the YouVersion
// Platform API (https://api.youversion.com/v1).
//
// Passage text is returned by GET /bibles/{id}/passages/{passageID}.
// Access depends on Bible version licensing: some translations (e.g. 新標點和合本 ID 46)
// require a separate publisher agreement and return 403 without one.
// Free/unlicensed translations such as NIV11 (ID 111) and CSB 中文標準譯本 (ID 312) return text freely.
package youversion

import "encoding/json"

// BiblesResponse is the top-level wrapper returned by GET /bibles.
type BiblesResponse struct {
	Data []BibleVersion `json:"data"`
}

// BibleVersion represents one Bible translation returned by the /bibles endpoint.
type BibleVersion struct {
	ID                   int      `json:"id"`
	Abbreviation         string   `json:"abbreviation"`
	PromotionalContent   *string  `json:"promotional_content"`
	Copyright            *string  `json:"copyright"`
	Info                 *string  `json:"info"`
	PublisherURL         *string  `json:"publisher_url"`
	LanguageTag          string   `json:"language_tag"`
	LocalizedAbbreviation string  `json:"localized_abbreviation"`
	LocalizedTitle       string   `json:"localized_title"`
	Title                string   `json:"title"`
	Books                []string `json:"books"` // e.g. ["GEN","EXO",...]
}

// BooksResponse is the top-level wrapper returned by GET /bibles/{id}/books.
type BooksResponse struct {
	Data []BookData `json:"data"`
}

// BookData represents one Bible book, including its full chapter and verse tree.
// This shape is shared by both /bibles/{id}/books (list) and
// /bibles/{id}/books/{book} (single-book detail).
type BookData struct {
	ID           string            `json:"id"`            // e.g. "GEN"
	Title        string            `json:"title"`          // localized book name
	FullTitle    string            `json:"full_title"`
	Abbreviation string            `json:"abbreviation"`
	Canon        string            `json:"canon"`          // "old_testament" | "new_testament"
	Chapters     []ChapterData     `json:"chapters"`
	// Intro may be null, a string, or an object like {"id":"INTRO","passage_id":"GEN.INTRO","title":"Intro"}.
	// json.RawMessage preserves the raw JSON value regardless of its type.
	Intro        json.RawMessage   `json:"intro"`
}

// ChaptersResponse is the top-level wrapper returned by
// GET /bibles/{id}/books/{book}/chapters.
type ChaptersResponse struct {
	Data []ChapterData `json:"data"`
}

// ChapterData represents one chapter within a book.
// Returned both as elements of a book's chapters array and as the single-chapter
// response from GET /bibles/{id}/books/{book}/chapters/{n}.
type ChapterData struct {
	ID        string      `json:"id"`         // e.g. "1"
	PassageID string      `json:"passage_id"` // e.g. "GEN.1"
	Title     string      `json:"title"`      // e.g. "1"
	Verses    []VerseData `json:"verses"`
}

// VersesResponse is the top-level wrapper returned by
// GET /bibles/{id}/books/{book}/chapters/{n}/verses.
type VersesResponse struct {
	Data []VerseData `json:"data"`
}

// VerseData represents one verse within a chapter.
// Note: verse-list endpoints (/books/{book}/chapters/{n}/verses) return only
// structural identifiers. Use GetPassage to retrieve the actual verse text.
type VerseData struct {
	ID        string `json:"id"`         // e.g. "1"
	PassageID string `json:"passage_id"` // e.g. "GEN.1.1"
	Title     string `json:"title"`      // e.g. "1"
}

// PassageData is the response from GET /bibles/{id}/passages/{passageID}.
// This is the primary endpoint for retrieving actual Bible verse text content.
//
// passageID supports several formats:
//   - Single verse:  "GEN.1.1"
//   - Verse range:   "GEN.1.1-3"
//   - Whole chapter: "GEN.1"
//
// Access depends on licensing: translations with publisher restrictions (e.g. 新標點和合本,
// ID 46) return HTTP 403. Open/licensed translations like NIV11 (ID 111) and CSB 中文標準譯本 (ID 312)
// return text without additional agreement.
//
// API v1 limitation — sub_title / section headings:
//
// The YouVersion Platform API v1 returns exactly three fields per passage: id,
// content, and reference. There is NO field for pericope headings, section
// titles, or sub-titles. Verified against live API and the saved
// youversion-bible-api-result.json sample file.
//
// As a consequence, the bibles.bible_section_contents.sub_title column will
// always be NULL when data originates from this API. This is a confirmed API
// constraint, not a bug in the crawler. Populating sub_title would require
// a future API version that exposes headings, or a licensed HTML-scraping
// approach applied on top of the existing data.
type PassageData struct {
	ID        string `json:"id"`        // e.g. "GEN.1.1" or "GEN.1.1-3"
	Content   string `json:"content"`   // the actual verse text
	Reference string `json:"reference"` // e.g. "Genesis 1:1" or "Genesis 1:1-3"
	// The API returns no sub_title/heading field; bibles.bible_section_contents.sub_title will be NULL.
}

// VOTDResponse is the top-level wrapper returned by GET /verse_of_the_days.
type VOTDResponse struct {
	Data []VOTDEntry `json:"data"`
}

// VOTDEntry maps a calendar day (1–366) to a Bible passage reference.
// Verse text is not included; use the PassageID to look up the verse elsewhere.
type VOTDEntry struct {
	Day       int    `json:"day"`
	PassageID string `json:"passage_id"` // e.g. "ISA.43.18-19"
}
