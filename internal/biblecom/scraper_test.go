// Package biblecom — scraper_test.go
//
// Unit tests for scraper-level helpers: disputedNote, applyDisputedPostProcessing,
// and syncDisputedVersesZH.
package biblecom

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDisputedNote verifies the human-readable note format for both languages.
func TestDisputedNote(t *testing.T) {
	zh := disputedNote(LangChinese, "馬太福音", 18, 11)
	assert.Equal(t, "馬太福音,第18章,第11小節,遇到特殊情境，爬蟲實作邏輯在此處留下空字串", zh)

	en := disputedNote(LangEnglish, "Matthew", 17, 21)
	assert.Equal(t, "Matthew,chapter 17,verse 21,遇到特殊情境，爬蟲實作邏輯在此處留下空字串", en)
}

// TestSyncDisputedVersesZH verifies that syncDisputedVersesZH inserts a
// placeholder verse into the ZH output for every EN disputed verse that is
// absent from the ZH chapter.
func TestSyncDisputedVersesZH(t *testing.T) {
	// Build a minimal EN output with one disputed verse (Matt 18:11) and one
	// normal verse (Matt 18:10) so we can verify only the disputed one is synced.
	enNote := disputedNote(LangEnglish, "Matthew", 18, 11)
	enOut := &OutputFile{
		Language: LangEnglish,
		Books: []BookOutput{
			{
				BookSort: 40,
				BookName: "Matthew",
				BookUSFM: "MAT",
				Chapters: []ChapterOutput{
					{
						ChapterSort: 18,
						Verses: []VerseOutput{
							{VerseSort: 10, Content: "normal verse"},
							{VerseSort: 11, Content: "", Note: enNote},
							{VerseSort: 12, Content: "normal verse"},
						},
					},
				},
			},
		},
	}

	// Build a ZH output that has v10 and v12 but NOT v11 (CUNP omits it).
	zhOut := &OutputFile{
		Language: LangChinese,
		Books: []BookOutput{
			{
				BookSort: 40,
				BookName: "馬太福音",
				BookUSFM: "MAT",
				Chapters: []ChapterOutput{
					{
						ChapterSort: 18,
						Verses: []VerseOutput{
							{VerseSort: 10, Content: "你們要小心"},
							{VerseSort: 12, Content: "一個人若有一百隻羊"},
						},
					},
				},
			},
		},
	}

	syncDisputedVersesZH(zhOut, enOut)

	verses := zhOut.Books[0].Chapters[0].Verses
	require.Len(t, verses, 3, "v11 should have been inserted")

	// Verify sort order is preserved.
	assert.Equal(t, 10, verses[0].VerseSort)
	assert.Equal(t, 11, verses[1].VerseSort)
	assert.Equal(t, 12, verses[2].VerseSort)

	// Verify the inserted verse has empty content and a Chinese-format note.
	v11 := verses[1]
	assert.Equal(t, "", v11.Content, "disputed verse content must be empty string")
	assert.True(t, strings.Contains(v11.Note, "馬太福音"), "note should contain Chinese book name")
	assert.True(t, strings.Contains(v11.Note, "第18章"), "note should contain chapter")
	assert.True(t, strings.Contains(v11.Note, "第11小節"), "note should contain verse")
	assert.True(t, strings.HasSuffix(v11.Note, disputedVerseSuffix), "note should end with the canonical disputed-verse suffix")

	// Normal verses must not be duplicated.
	assert.Equal(t, "你們要小心", verses[0].Content)
	assert.Equal(t, "一個人若有一百隻羊", verses[2].Content)
}

// TestSyncDisputedVersesZH_AlreadyPresent verifies that if the ZH output
// already contains the disputed verse (e.g. from a previous partial run),
// syncDisputedVersesZH does not add a duplicate.
func TestSyncDisputedVersesZH_AlreadyPresent(t *testing.T) {
	enNote := disputedNote(LangEnglish, "Matthew", 18, 11)
	enOut := &OutputFile{
		Language: LangEnglish,
		Books: []BookOutput{
			{BookSort: 40, BookName: "Matthew", BookUSFM: "MAT", Chapters: []ChapterOutput{
				{ChapterSort: 18, Verses: []VerseOutput{
					{VerseSort: 11, Content: "", Note: enNote},
				}},
			}},
		},
	}

	zhNote := disputedNote(LangChinese, "馬太福音", 18, 11)
	zhOut := &OutputFile{
		Language: LangChinese,
		Books: []BookOutput{
			{BookSort: 40, BookName: "馬太福音", BookUSFM: "MAT", Chapters: []ChapterOutput{
				{ChapterSort: 18, Verses: []VerseOutput{
					{VerseSort: 11, Content: "", Note: zhNote},
				}},
			}},
		},
	}

	syncDisputedVersesZH(zhOut, enOut)

	assert.Len(t, zhOut.Books[0].Chapters[0].Verses, 1, "no duplicate should be added")
}

// TestSyncDisputedVersesZH_MissingZHChapter verifies that syncDisputedVersesZH
// silently skips disputed verses whose ZH chapter does not exist in the output
// (e.g. the ZH chapter fetch failed).
func TestSyncDisputedVersesZH_MissingZHChapter(t *testing.T) {
	enNote := disputedNote(LangEnglish, "Matthew", 18, 11)
	enOut := &OutputFile{
		Language: LangEnglish,
		Books: []BookOutput{
			{BookSort: 40, BookName: "Matthew", BookUSFM: "MAT", Chapters: []ChapterOutput{
				{ChapterSort: 18, Verses: []VerseOutput{
					{VerseSort: 11, Content: "", Note: enNote},
				}},
			}},
		},
	}

	// ZH output has no chapters at all for MAT.
	zhOut := &OutputFile{
		Language: LangChinese,
		Books:    []BookOutput{},
	}

	// Should not panic.
	require.NotPanics(t, func() { syncDisputedVersesZH(zhOut, enOut) })
	assert.Empty(t, zhOut.Books)
}

// TestApplyDisputedPostProcessing verifies the invariants of the post-processing
// loop that is applied to every crawled chapter before it is stored in the grid.
//
// The critical invariant is that notes "ref:*" and "omitted" are normalised to
// content="" + human-readable note, while "merged" and "" are left untouched.
// A typo in the filter condition would silently corrupt merged-verse content
// across tens of thousands of DB rows.
func TestApplyDisputedPostProcessing(t *testing.T) {
	cases := []struct {
		name        string
		input       VerseOutput
		wantContent string
		wantCross   string
		wantNoteHas string // non-empty → assert HasSuffix(note, disputedVerseSuffix)
		wantNoteIs  string // non-empty → assert exact note equality
	}{
		{
			name:        "ref: note is blanked",
			input:       VerseOutput{VerseSort: 21, Content: "some text", CrossRef: "MRK.9.29", Note: "ref:MRK.9.29"},
			wantContent: "",
			wantCross:   "",
			wantNoteHas: disputedVerseSuffix,
		},
		{
			name:        "omitted note is blanked",
			input:       VerseOutput{VerseSort: 44, Content: "Some manuscripts add verse 44.", CrossRef: "", Note: "omitted"},
			wantContent: "",
			wantCross:   "",
			wantNoteHas: disputedVerseSuffix,
		},
		{
			name:        "merged note is untouched",
			input:       VerseOutput{VerseSort: 2, Content: "併於上節。", CrossRef: "", Note: "merged"},
			wantContent: "併於上節。",
			wantCross:   "",
			wantNoteIs:  "merged",
		},
		{
			name:        "normal verse (empty note) is untouched",
			input:       VerseOutput{VerseSort: 5, Content: "For God so loved the world.", CrossRef: "", Note: ""},
			wantContent: "For God so loved the world.",
			wantCross:   "",
			wantNoteIs:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verses := []VerseOutput{tc.input}
			applyDisputedPostProcessing(verses, LangEnglish, "Matthew", 17)
			assert.Equal(t, tc.wantContent, verses[0].Content, "content mismatch")
			assert.Equal(t, tc.wantCross, verses[0].CrossRef, "CrossRef mismatch")
			if tc.wantNoteHas != "" {
				assert.True(t, strings.HasSuffix(verses[0].Note, tc.wantNoteHas),
					"expected note to end with disputed suffix, got %q", verses[0].Note)
			}
			if tc.wantNoteIs != "" {
				assert.Equal(t, tc.wantNoteIs, verses[0].Note, "note mismatch")
			}
		})
	}
}
