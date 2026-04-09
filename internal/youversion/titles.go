// Package youversion — titles.go
//
// titles.go centralises the localised title templates for chapter and verse
// rows written to bibles.bible_chapter_contents and bibles.bible_section_contents.
// Keeping the templates here (rather than inlining them in cmd/youversion-importer
// or cmd/biblecom-importer) achieves two goals:
//
//  1. Both importers produce identical title strings, so a cross-importer query
//     (e.g. joining YouVersion and biblecom rows by chapter sort) always succeeds.
//  2. If the DB schema ever changes the expected title format, updating this file
//     automatically fixes all callers at once.
//
// Note: these functions are also called by cmd/biblecom-importer, which imports
// bible.com content — the shared templates guarantee consistent title formatting
// across all three crawler pipelines.
package youversion

import "fmt"

// FormatChapterTitle returns the localised chapter heading for DB storage.
// The format matches what the HTML crawler stores in bibles.bible_chapter_contents.
// Keeping the logic here (rather than inlining in cmd/youversion-importer) makes it
// independently testable and prevents template-string drift between callers.
func FormatChapterTitle(lang string, chapterSort int) string {
	if lang == LangChinese {
		return fmt.Sprintf("第 %d 章", chapterSort)
	}
	return fmt.Sprintf("Chapter %d", chapterSort)
}

// FormatVerseTitle returns the localised verse heading for DB storage.
// The format matches what the HTML crawler stores in bibles.bible_section_contents.
func FormatVerseTitle(lang string, verseSort int) string {
	if lang == LangChinese {
		return fmt.Sprintf("第%d節", verseSort)
	}
	return fmt.Sprintf("verse %d", verseSort)
}
