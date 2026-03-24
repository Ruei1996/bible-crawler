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
