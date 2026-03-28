// Package biblecom — parser_test.go
//
// Unit tests for ParseChapter and its internal helpers.
// Each test builds a minimal but realistic HTML fragment that mirrors a
// specific bible.com rendering pattern (prose paragraphs, poetry lines,
// inline footnotes, section headings, proper-noun spans) and asserts on the
// shape and content of the returned VerseOutput slice.  Running these tests
// does not require a network connection.
package biblecom

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseChapter_BasicVerses verifies that consecutive verse spans within a
// single container div are extracted with the correct sort order and content.
func TestParseChapter_BasicVerses(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__label">1</div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">In the beginning God created the heavens and the earth.</span>
	      </span>
	      <span data-usfm="GEN.1.2" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">2</span>
	        <span class="ChapterContent-module__cat7xG__content">Now the earth was formless and empty.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 2)

	assert.Equal(t, 1, verses[0].VerseSort)
	assert.Equal(t, "In the beginning God created the heavens and the earth.", verses[0].Content)
	assert.Empty(t, verses[0].SubTitle)

	assert.Equal(t, 2, verses[1].VerseSort)
	assert.Equal(t, "Now the earth was formless and empty.", verses[1].Content)
}

// TestParseChapter_SubTitleAttachedToFirstFollowingVerse verifies that a
// section heading (sub_title) is attached only to the first verse that appears
// after it, not to subsequent verses in the same chapter.
func TestParseChapter_SubTitleAttachedToFirstFollowingVerse(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__s1">
	      <span class="ChapterContent-module__cat7xG__heading">The Beginning</span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">In the beginning God created the heavens and the earth.</span>
	      </span>
	      <span data-usfm="GEN.1.2" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">2</span>
	        <span class="ChapterContent-module__cat7xG__content">Now the earth was formless and empty.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 2)

	// Sub-title must be on verse 1 only.
	assert.Equal(t, "The Beginning", verses[0].SubTitle)
	// Verse 2 must not inherit the same sub-title.
	assert.Empty(t, verses[1].SubTitle)
}

// TestParseChapter_MultipleSubTitles verifies that multiple section headings
// within a chapter each attach to the correct immediately-following verse.
func TestParseChapter_MultipleSubTitles(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="1CH.4">
	    <div class="ChapterContent-module__cat7xG__s">
	      <span class="ChapterContent-module__cat7xG__heading">復記猶大的後裔</span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="1CH.4.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">猶大的兒子是法勒斯。</span>
	      </span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__s">
	      <span class="ChapterContent-module__cat7xG__heading">示拉的後裔</span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="1CH.4.21" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">21</span>
	        <span class="ChapterContent-module__cat7xG__content">猶大之子示拉的後裔。</span>
	      </span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__s">
	      <span class="ChapterContent-module__cat7xG__heading">西緬的後裔</span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="1CH.4.24" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">24</span>
	        <span class="ChapterContent-module__cat7xG__content">西緬的兒子是尼母利。</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "1CH", 4)
	require.NoError(t, err)
	require.Len(t, verses, 3)

	assert.Equal(t, 1, verses[0].VerseSort)
	assert.Equal(t, "復記猶大的後裔", verses[0].SubTitle)

	assert.Equal(t, 21, verses[1].VerseSort)
	assert.Equal(t, "示拉的後裔", verses[1].SubTitle)

	assert.Equal(t, 24, verses[2].VerseSort)
	assert.Equal(t, "西緬的後裔", verses[2].SubTitle)
}

// TestParseChapter_FootnoteSkipped verifies that inline footnote spans
// (class*="__note") are stripped from verse content.
func TestParseChapter_FootnoteSkipped(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.26" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">26</span>
	        <span class="ChapterContent-module__cat7xG__content">Then God said, "Let us make mankind in our image,</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">1:26 footnote text</span>
	        </span>
	        <span class="ChapterContent-module__cat7xG__content"> and over all the creatures."</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 1)

	// Footnote text must be absent; verse prose should be joined cleanly.
	assert.Equal(t, `Then God said, "Let us make mankind in our image, and over all the creatures."`, verses[0].Content)
	assert.NotContains(t, verses[0].Content, "footnote")
}

// TestParseChapter_PoetryVerseAcrossMultipleSpans verifies the poetry
// continuation logic: when the same data-usfm value appears in multiple child
// divs (e.g. __q1, __q2), the content fragments are joined into one verse.
func TestParseChapter_PoetryVerseAcrossMultipleSpans(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__q1">
	      <span data-usfm="GEN.1.27" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">27</span>
	        <span class="ChapterContent-module__cat7xG__content">So God created mankind in his own image,</span>
	      </span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__q2">
	      <span data-usfm="GEN.1.27" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">in the image of God he created them;</span>
	      </span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__q2">
	      <span data-usfm="GEN.1.27" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">male and female he created them.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	// Three spans, but only ONE verse because they share the same data-usfm.
	require.Len(t, verses, 1)

	assert.Equal(t, 27, verses[0].VerseSort)
	assert.Contains(t, verses[0].Content, "So God created mankind in his own image,")
	assert.Contains(t, verses[0].Content, "in the image of God he created them;")
	assert.Contains(t, verses[0].Content, "male and female he created them.")
}

// TestParseChapter_EmptyContentSpansSkipped verifies that whitespace-only
// continuation markers (paragraph indent spacers) do not create spurious verse
// entries. These appear as span[data-usfm] with only "  " as content.
func TestParseChapter_EmptyContentSpansSkipped(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">First verse text.</span>
	      </span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__li1">
	      <span data-usfm="GEN.1.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">  </span>
	      </span>
	      <span data-usfm="GEN.1.2" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">2</span>
	        <span class="ChapterContent-module__cat7xG__content">Second verse text.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 2, "whitespace-only continuation span must not add a third verse")

	assert.Equal(t, 1, verses[0].VerseSort)
	assert.Equal(t, "First verse text.", verses[0].Content)
	assert.Equal(t, 2, verses[1].VerseSort)
	assert.Equal(t, "Second verse text.", verses[1].Content)
}

// TestParseChapter_ProperNounSpanIncluded verifies that Chinese proper-noun
// spans (class*="__pn") contribute their text to the verse content. These
// wrapping spans style the text differently but must not be stripped.
func TestParseChapter_ProperNounSpanIncluded(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="1CH.4">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="1CH.4.13" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">13</span>
	        <span class="ChapterContent-module__cat7xG__pn">
	          <span class="ChapterContent-module__cat7xG__content">基納斯</span>
	        </span>
	        <span class="ChapterContent-module__cat7xG__content">的兒子是</span>
	        <span class="ChapterContent-module__cat7xG__pn">
	          <span class="ChapterContent-module__cat7xG__content">俄陀聶</span>
	        </span>
	        <span class="ChapterContent-module__cat7xG__content">。</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "1CH", 4)
	require.NoError(t, err)
	require.Len(t, verses, 1)

	// Proper noun text must appear in the content string.
	assert.Contains(t, verses[0].Content, "基納斯")
	assert.Contains(t, verses[0].Content, "俄陀聶")
	assert.Contains(t, verses[0].Content, "的兒子是")
}

// TestParseChapter_MissingChapterDiv verifies that a useful error is returned
// when the expected data-usfm chapter div is not present in the HTML.
func TestParseChapter_MissingChapterDiv(t *testing.T) {
	html := `<!DOCTYPE html><html><body><p>No bible content here.</p></body></html>`

	_, err := ParseChapter(html, "GEN", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chapter div not found")
}

// TestParseChapter_ContentNormalisedWhitespace verifies that leading/trailing
// whitespace and internal runs of multiple spaces are collapsed to single spaces.
func TestParseChapter_ContentNormalisedWhitespace(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">  In the  beginning  </span>
	        <span class="ChapterContent-module__cat7xG__content">  God created.  </span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 1)

	assert.Equal(t, "In the beginning God created.", verses[0].Content)
}

// TestParseChapter_MergedVersesPlusSeparator verifies that the real bible.com
// HTML encoding of merged verses — using "+" as the ref separator instead of a
// space (e.g. data-usfm="2SA.3.9+2SA.3.10") — is parsed identically to the
// space-separated form. This is the encoding observed in the live CUNP-上帝
// Chinese translation; without normalisation the whole span is silently dropped.
func TestParseChapter_MergedVersesPlusSeparator(t *testing.T) {
	sharedContent := "我若不照着耶和華起誓應許大衛的話行，願上帝重重地降罰與我！」"
	v8Content := "押尼珥因伊施波設的話就甚發怒。"
	v11Content := "伊施波設懼怕押尼珥，不敢回答一句。"
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="2SA.3">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="2SA.3.8" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">8</span>
	        <span class="ChapterContent-module__cat7xG__content">` + v8Content + `</span>
	      </span>
	      <span data-usfm="2SA.3.9+2SA.3.10" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">9-10</span>
	        <span class="ChapterContent-module__cat7xG__content">` + sharedContent + `</span>
	      </span>
	      <span data-usfm="2SA.3.11" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">11</span>
	        <span class="ChapterContent-module__cat7xG__content">` + v11Content + `</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "2SA", 3)
	require.NoError(t, err)
	// 4 verses: 8, 9 (primary), 10 (merged sentinel), 11.
	require.Len(t, verses, 4, "verses 9+10 merged via '+' separator must both appear")

	// Verse 8 — framing verse; content must not be contaminated by the merge.
	assert.Equal(t, 8, verses[0].VerseSort)
	assert.Equal(t, v8Content, verses[0].Content)

	assert.Equal(t, 9, verses[1].VerseSort)
	assert.Equal(t, sharedContent, verses[1].Content)
	assert.Empty(t, verses[1].Note)

	assert.Equal(t, 10, verses[2].VerseSort)
	assert.Equal(t, mergedVerseContent, verses[2].Content)
	assert.Equal(t, "merged", verses[2].Note)

	// Verse 11 — framing verse; content must not be contaminated by the merge.
	assert.Equal(t, 11, verses[3].VerseSort)
	assert.Equal(t, v11Content, verses[3].Content)
}

// TestParseChapter_MergedVersesMixedSeparator verifies that a data-usfm
// attribute that combines both separator styles (e.g. "2SA.3.9+2SA.3.10 2SA.3.11")
// is handled correctly: ReplaceAll("+" → " ") followed by Fields produces
// three distinct tokens regardless of which separators are present or mixed.
func TestParseChapter_MergedVersesMixedSeparator(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="2SA.3">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="2SA.3.9+2SA.3.10 2SA.3.11" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">9-11</span>
	        <span class="ChapterContent-module__cat7xG__content">Shared content for three verses.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "2SA", 3)
	require.NoError(t, err)
	require.Len(t, verses, 3, "all three refs must be parsed from the mixed-separator attribute")

	assert.Equal(t, 9, verses[0].VerseSort)
	assert.Equal(t, "Shared content for three verses.", verses[0].Content)
	assert.Empty(t, verses[0].Note)

	assert.Equal(t, 10, verses[1].VerseSort)
	assert.Equal(t, mergedVerseContent, verses[1].Content)
	assert.Equal(t, "merged", verses[1].Note)

	assert.Equal(t, 11, verses[2].VerseSort)
	assert.Equal(t, mergedVerseContent, verses[2].Content)
	assert.Equal(t, "merged", verses[2].Note)
}

// TestParseChapter_MergedVerses verifies the merged-verse behaviour: when a
// single span carries multiple USFM refs (e.g. data-usfm="2SA.3.9 2SA.3.10"),
// bible.com is indicating that verses 9 and 10 share the same prose text.
// ParseChapter should emit the actual content for the verse with the smallest
// sort number, and "併於上節。" (mergedVerseContent) for all others.
// The secondary merged verse must be annotated with Note="merged" and must
// NOT carry a SubTitle even if a section heading preceded the span.
func TestParseChapter_MergedVerses(t *testing.T) {
	sharedContent := "我若不照着耶和華起誓應許大衛的話行，廢去掃羅的位，建立大衛的位，使他治理以色列和猶大，從但直到別是巴，願上帝重重地降罰與我！"
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="2SA.3">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="2SA.3.8" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">8</span>
	        <span class="ChapterContent-module__cat7xG__content">押尼珥因伊施波設的話就甚發怒。</span>
	      </span>
	      <span data-usfm="2SA.3.9 2SA.3.10" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">9-10</span>
	        <span class="ChapterContent-module__cat7xG__content">` + sharedContent + `</span>
	      </span>
	      <span data-usfm="2SA.3.11" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">11</span>
	        <span class="ChapterContent-module__cat7xG__content">伊施波設懼怕押尼珥，不敢回答一句。</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "2SA", 3)
	require.NoError(t, err)
	// Expect 4 verses: 8, 9 (primary with real content), 10 (merged), 11.
	require.Len(t, verses, 4)

	// Verse 8 — normal preceding verse.
	assert.Equal(t, 8, verses[0].VerseSort)
	assert.Equal(t, "押尼珥因伊施波設的話就甚發怒。", verses[0].Content)
	assert.Empty(t, verses[0].Note)

	// Verse 9 — primary merged verse: carries the actual shared content.
	assert.Equal(t, 9, verses[1].VerseSort)
	assert.Equal(t, sharedContent, verses[1].Content)
	assert.Empty(t, verses[1].Note, "primary merged verse must not be annotated")

	// Verse 10 — secondary merged verse: gets sentinel text + Note annotation.
	assert.Equal(t, 10, verses[2].VerseSort)
	assert.Equal(t, mergedVerseContent, verses[2].Content)
	assert.Equal(t, "merged", verses[2].Note)
	assert.Empty(t, verses[2].SubTitle, "merged secondary verse must not carry a SubTitle")

	// Verse 11 — normal following verse.
	assert.Equal(t, 11, verses[3].VerseSort)
	assert.Equal(t, "伊施波設懼怕押尼珥，不敢回答一句。", verses[3].Content)
	assert.Empty(t, verses[3].Note)
}

// TestParseChapter_MergedVersesOutOfOrder verifies that even when the USFM refs
// inside the data-usfm attribute appear in descending order (e.g.
// "2SA.3.10 2SA.3.9"), the verse with the numerically smallest sort number
// is still treated as the primary verse carrying the actual content.
func TestParseChapter_MergedVersesOutOfOrder(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.4 GEN.1.3" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">3-4</span>
	        <span class="ChapterContent-module__cat7xG__content">Shared content for verses 3 and 4.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 2)

	// Verse 3 must be first and carry the real content (smallest number wins).
	assert.Equal(t, 3, verses[0].VerseSort)
	assert.Equal(t, "Shared content for verses 3 and 4.", verses[0].Content)
	assert.Empty(t, verses[0].Note)

	// Verse 4 must be second with sentinel content and merged annotation.
	assert.Equal(t, 4, verses[1].VerseSort)
	assert.Equal(t, mergedVerseContent, verses[1].Content)
	assert.Equal(t, "merged", verses[1].Note)
}

// TestParseChapter_MergedVersesSubTitleOnPrimaryOnly verifies that a section
// heading preceding a merged-verse span is attached to the primary verse only
// and must not propagate to the secondary (merged) verse entries.
func TestParseChapter_MergedVersesSubTitleOnPrimaryOnly(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__s">
	      <span class="ChapterContent-module__cat7xG__heading">A Section Heading</span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.5 GEN.1.6" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">5-6</span>
	        <span class="ChapterContent-module__cat7xG__content">Shared content.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	require.Len(t, verses, 2)

	// Primary verse inherits the heading.
	assert.Equal(t, 5, verses[0].VerseSort)
	assert.Equal(t, "A Section Heading", verses[0].SubTitle)
	assert.Empty(t, verses[0].Note)

	// Secondary merged verse must NOT inherit the heading.
	assert.Equal(t, 6, verses[1].VerseSort)
	assert.Empty(t, verses[1].SubTitle)
	assert.Equal(t, "merged", verses[1].Note)
}

// TestParseChapter_MergedVersesRejectsCrossBookRef verifies that ref validation
// rejects entries from a different book or chapter within the same data-usfm
// attribute. A stray ref like "EXO.2.1" inside a GEN.1 span must not inject a
// spurious verse into the output.
func TestParseChapter_MergedVersesRejectsCrossBookRef(t *testing.T) {
	// The span carries one valid GEN.1.5 ref and one stray EXO.2.1 ref.
	// Only verse 5 (GEN.1.5) must appear in the output.
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.5 EXO.2.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">5</span>
	        <span class="ChapterContent-module__cat7xG__content">Cross-book test content.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	// Only verse 5 must be produced; EXO.2.1 is rejected by the book guard.
	require.Len(t, verses, 1, "stray cross-book ref must be silently discarded")
	assert.Equal(t, 5, verses[0].VerseSort)
	assert.Equal(t, "Cross-book test content.", verses[0].Content)
	assert.Empty(t, verses[0].Note, "sole valid verse must not be treated as merged")
}

// TestParseChapter_MergedVersesRejectsCrossChapterRef verifies that a ref from
// a different chapter number (e.g. "GEN.2.1" inside a GEN.1 span) is silently
// rejected, leaving only the valid same-chapter ref(s) in the output.
func TestParseChapter_MergedVersesRejectsCrossChapterRef(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="GEN.1">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="GEN.1.3 GEN.2.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">3</span>
	        <span class="ChapterContent-module__cat7xG__content">Cross-chapter test content.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "GEN", 1)
	require.NoError(t, err)
	// Only verse 3 must be produced; GEN.2.1 is rejected by the chapter guard.
	require.Len(t, verses, 1, "stray cross-chapter ref must be silently discarded")
	assert.Equal(t, 3, verses[0].VerseSort)
	assert.Equal(t, "Cross-chapter test content.", verses[0].Content)
	assert.Empty(t, verses[0].Note)
}

// TestParseChapter_FootnoteRefSpanIgnored verifies that span[data-usfm] elements
// with class="ref" that appear inside a __note footnote (e.g. the NIV Daniel 4
// footnote annotating Aramaic versification differences) are silently excluded
// from verse parsing. Without the [class*="__verse"] guard, the parser would
// treat data-usfm="DAN.4.1+DAN.4.2+DAN.4.3" (class="ref") as a merged-verse
// group, assign "併於上節。" to secondary verses 2 & 3, and then concatenate
// their real content when the individual verse spans are later encountered.
func TestParseChapter_FootnoteRefSpanIgnored(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="DAN.4">
	    <div class="ChapterContent-module__cat7xG__pmo">
	      <span class="ChapterContent-module__cat7xG__note">
	        <span class="ChapterContent-module__cat7xG__label">#</span>
	        <span class="ChapterContent-module__cat7xG__body">
	          In Aramaic texts <span data-usfm="DAN.4.1+DAN.4.2+DAN.4.3" class="ref">4:1-3</span>
	          is numbered 3:31-33.
	        </span>
	      </span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="DAN.4.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">King Nebuchadnezzar, to the nations.</span>
	      </span>
	      <span data-usfm="DAN.4.2" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">2</span>
	        <span class="ChapterContent-module__cat7xG__content">It is my pleasure to tell you.</span>
	      </span>
	      <span data-usfm="DAN.4.3" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">3</span>
	        <span class="ChapterContent-module__cat7xG__content">How great are his signs.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "DAN", 4)
	require.NoError(t, err)
	// All 3 verses must be independent with their own content; the footnote ref
	// span must have been completely ignored.
	require.Len(t, verses, 3, "footnote ref span must not create extra verse entries")

	assert.Equal(t, 1, verses[0].VerseSort)
	assert.Equal(t, "King Nebuchadnezzar, to the nations.", verses[0].Content)
	assert.Empty(t, verses[0].Note)

	assert.Equal(t, 2, verses[1].VerseSort)
	assert.Equal(t, "It is my pleasure to tell you.", verses[1].Content)
	assert.Empty(t, verses[1].Note, "verse 2 must NOT be marked merged — it has its own content")

	assert.Equal(t, 3, verses[2].VerseSort)
	assert.Equal(t, "How great are his signs.", verses[2].Content)
	assert.Empty(t, verses[2].Note)
}

// TestParseChapter_FootnoteRefSpanIgnoredInVerseContainer covers the variant
// where a ref span (class="ref") appears as a direct sibling of __verse spans
// inside the same __p div, rather than inside a separate __note container.
// The [class*="__verse"] selector must still exclude it because "ref" does not
// contain "__verse" — no __note ancestry is required for the filter to work.
func TestParseChapter_FootnoteRefSpanIgnoredInVerseContainer(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="DAN.4">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="DAN.4.1+DAN.4.2+DAN.4.3" class="ref">4:1-3</span>
	      <span data-usfm="DAN.4.1" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">1</span>
	        <span class="ChapterContent-module__cat7xG__content">King Nebuchadnezzar.</span>
	      </span>
	      <span data-usfm="DAN.4.2" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">2</span>
	        <span class="ChapterContent-module__cat7xG__content">It is my pleasure.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "DAN", 4)
	require.NoError(t, err)
	require.Len(t, verses, 2, "ref span inside __p must be excluded by class filter even without __note ancestor")
	assert.Equal(t, 1, verses[0].VerseSort)
	assert.Equal(t, "King Nebuchadnezzar.", verses[0].Content)
	assert.Empty(t, verses[0].Note)
	assert.Equal(t, 2, verses[1].VerseSort)
	assert.Equal(t, "It is my pleasure.", verses[1].Content)
	assert.Empty(t, verses[1].Note)
}

// TestNormaliseSpace tests the internal normaliseSpace helper with various
// whitespace patterns to ensure consistent output.
func TestNormaliseSpace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  hello  world  ", "hello world"},
		{"no extra spaces", "no extra spaces"},
		{"\t\nhello\n\tworld\n", "hello world"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		got := normaliseSpace(tc.input)
		assert.Equal(t, tc.want, got, "input: %q", tc.input)
	}
}

// TestExtractContent_RemovesNoteAndLabel verifies that extractContent strips
// footnote and label spans but preserves all prose text.
func TestExtractContent_RemovesNoteAndLabel(t *testing.T) {
	// Build a minimal goquery selection that mimics a verse span.
	// Class names must include the "__" module suffix that the selectors target.
	html := `<html><body>
	<span data-usfm="GEN.1.1" class="module__abc__verse">
	  <span class="module__abc__label">1</span>
	  <span class="module__abc__content">Verse text.</span>
	  <span class="module__abc__note">
	    <span class="module__abc__label">#</span>
	    <span class="module__abc__body">footnote body</span>
	  </span>
	</span>
	</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	verseSel := doc.Find(`span[data-usfm]`).First()
	got := extractContent(verseSel)

	// extractContent returns raw (un-normalised) text; whitespace normalisation
	// is deferred to the single call site in ParseChapter so it runs exactly once
	// per verse. Apply normaliseSpace here to match what ParseChapter would produce.
	normalised := normaliseSpace(got)
	assert.Equal(t, "Verse text.", normalised)
	assert.NotContains(t, got, "footnote")
	assert.NotContains(t, got, "1") // verse number label must be stripped
}

// TestParseChapter_BracketOmittedVerse verifies that a bracket-labeled verse
// (e.g. NIV Matthew 17:21) is detected and its cross-reference USFM extracted
// from the __note's <span class="ref" data-usfm="…"> element. Content is left
// empty by ParseChapter (resolveRefs fills it in after the full crawl), and
// CrossRef/Note are set for JSON auditability.
//
// Also verifies the two-span pattern: the bracket span plus a whitespace-only
// spacer span for the same verse number — the spacer must be silently dropped.
func TestParseChapter_BracketOmittedVerse(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="MAT.17">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="MAT.17.20" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">20</span>
	        <span class="ChapterContent-module__cat7xG__content">He replied, "Because you have so little faith."</span>
	      </span>
	      <span data-usfm="MAT.17.21" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[21]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">
	            <span class="ChapterContent-module__cat7xG__fr">17:21 </span>
	            <span class="ft">Some manuscripts include here words similar to <span data-usfm="MRK.9.29" class="ref">Mark 9:29</span>.</span>
	          </span>
	        </span>
	      </span>
	      <span data-usfm="MAT.17.21" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">  </span>
	      </span>
	      <span data-usfm="MAT.17.22" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">22</span>
	        <span class="ChapterContent-module__cat7xG__content">When they came together in Galilee.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "MAT", 17)
	require.NoError(t, err)
	// Expect exactly 3 verses: 20, 21, 22 — the bracket verse must appear.
	require.Len(t, verses, 3, "bracket verse must be included with CrossRef set")

	assert.Equal(t, 20, verses[0].VerseSort)
	assert.Equal(t, `He replied, "Because you have so little faith."`, verses[0].Content)
	assert.Empty(t, verses[0].Note)
	assert.Empty(t, verses[0].CrossRef)

	assert.Equal(t, 21, verses[1].VerseSort)
	assert.Empty(t, verses[1].Content,
		"bracket verse content must be empty pre-resolveRefs when CrossRef is set")
	assert.Equal(t, "ref:MRK.9.29", verses[1].Note,
		"bracket verse must be annotated Note=ref:USFM")
	assert.Equal(t, "MRK.9.29", verses[1].CrossRef,
		"CrossRef must hold the raw USFM for resolveRefs lookup")
	assert.Empty(t, verses[1].SubTitle, "bracket verse must not carry an accidental sub_title")

	assert.Equal(t, 22, verses[2].VerseSort)
	assert.Equal(t, "When they came together in Galilee.", verses[2].Content)
	assert.Empty(t, verses[2].Note)
}

// TestParseChapter_BracketOmittedVerseWithSubTitle verifies that when a
// section heading immediately precedes a bracket-labeled verse, the heading
// is attached to that verse's SubTitle. This test also covers the fallback
// path where the __note contains no <span class="ref" data-usfm="…">: the
// footnote body text is extracted directly and stored as the verse Content.
func TestParseChapter_BracketOmittedVerseWithSubTitle(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="MRK.9">
	    <div class="ChapterContent-module__cat7xG__s1">
	      <span class="ChapterContent-module__cat7xG__heading">Prayer and Fasting</span>
	    </div>
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="MRK.9.44" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[44]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">Some manuscripts include verse 44.</span>
	        </span>
	      </span>
	      <span data-usfm="MRK.9.44" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">  </span>
	      </span>
	      <span data-usfm="MRK.9.45" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">45</span>
	        <span class="ChapterContent-module__cat7xG__content">And if your foot causes you to stumble, cut it off.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "MRK", 9)
	require.NoError(t, err)
	require.Len(t, verses, 2, "bracket verse 44 + normal verse 45")

	assert.Equal(t, 44, verses[0].VerseSort)
	// No span.ref in the note → fallback: body text used as content directly.
	assert.Equal(t, "Some manuscripts include verse 44.", verses[0].Content,
		"bracket verse with no cross-ref must use footnote body text as content")
	assert.Equal(t, "omitted", verses[0].Note,
		"bracket verse without cross-ref must use Note=omitted")
	assert.Empty(t, verses[0].CrossRef, "no span.ref means CrossRef must be empty")
	assert.Equal(t, "Prayer and Fasting", verses[0].SubTitle,
		"sub_title from preceding heading must be attached to the bracket verse")

	assert.Equal(t, 45, verses[1].VerseSort)
	assert.Equal(t, "And if your foot causes you to stumble, cut it off.", verses[1].Content)
	assert.Empty(t, verses[1].SubTitle, "sub_title must not propagate past the bracket verse")
}

// TestParseChapter_BracketOmittedVerse_SingleSpan verifies that a bracket
// verse consisting of only ONE span (the "[N]" + __note span, without the
// trailing whitespace-only spacer span) is still correctly detected and
// stored with the sentinel. This variant can appear when paragraph boundaries
// fall between the bracket span and the next verse.
func TestParseChapter_BracketOmittedVerse_SingleSpan(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="ACT.8">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="ACT.8.37" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[37]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">Some manuscripts include here Philips words.</span>
	        </span>
	      </span>
	      <span data-usfm="ACT.8.38" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">38</span>
	        <span class="ChapterContent-module__cat7xG__content">And he gave orders to stop the chariot.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "ACT", 8)
	require.NoError(t, err)
	require.Len(t, verses, 2, "single-span bracket verse 37 + normal verse 38")

	assert.Equal(t, 37, verses[0].VerseSort)
	// No span.ref in note → extractNoteBodyText fallback; body text is used as content.
	assert.Equal(t, "Some manuscripts include here Philips words.", verses[0].Content,
		"bracket verse with no cross-ref must use footnote body text as content")
	assert.Equal(t, "omitted", verses[0].Note)
	assert.Empty(t, verses[0].CrossRef)

	assert.Equal(t, 38, verses[1].VerseSort)
	assert.Equal(t, "And he gave orders to stop the chariot.", verses[1].Content)
	assert.Empty(t, verses[1].Note)
}

// TestParseChapter_BracketOmittedVerse_DoubleStampGuard verifies that if a
// bracket verse span appears twice for the same verse number (adversarial HTML
// or parser edge case), only ONE entry is recorded. The double-stamp guard in
// ParseChapter checks existing.omitted and skips the duplicate span.
func TestParseChapter_BracketOmittedVerse_DoubleStampGuard(t *testing.T) {
	// Two bracket spans for the same verse — only the first must survive.
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="ROM.16">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="ROM.16.24" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[24]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">Some manuscripts include a benediction here.</span>
	        </span>
	      </span>
	      <span data-usfm="ROM.16.24" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[24]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">Duplicate bracket span (defensive test).</span>
	        </span>
	      </span>
	      <span data-usfm="ROM.16.25" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">25</span>
	        <span class="ChapterContent-module__cat7xG__content">Now to him who is able to establish you.</span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "ROM", 16)
	require.NoError(t, err)
	require.Len(t, verses, 2, "exactly one bracket entry for verse 24 + normal verse 25")

	assert.Equal(t, 24, verses[0].VerseSort)
	// No span.ref → note body text from FIRST span is used.
	assert.Equal(t, "Some manuscripts include a benediction here.", verses[0].Content,
		"only first bracket span must survive; duplicate must be silently dropped")
	assert.Equal(t, "omitted", verses[0].Note)
	// Content must NOT contain duplicated text from the second span.
	assert.NotContains(t, verses[0].Content, "Duplicate bracket span",
		"double-stamp guard must prevent second bracket span from overwriting or appending")

	assert.Equal(t, 25, verses[1].VerseSort)
	assert.Equal(t, "Now to him who is able to establish you.", verses[1].Content)
}

// TestParseChapter_MultipleAdjacentBracketVerses verifies that multiple
// consecutive bracket-labeled verses (e.g. NIV Mark 9:44 and 9:46) are all
// detected correctly and each stored with the fallback note body text. This
// exercises the MRK.9 pattern where verses 44 and 46 carry only a __note.
func TestParseChapter_MultipleAdjacentBracketVerses(t *testing.T) {
	html := `<!DOCTYPE html><html><body>
	<div data-testid="chapter-content">
	  <div data-usfm="MRK.9">
	    <div class="ChapterContent-module__cat7xG__p">
	      <span data-usfm="MRK.9.43" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">43</span>
	        <span class="ChapterContent-module__cat7xG__content">If your hand causes you to stumble, cut it off.</span>
	      </span>
	      <span data-usfm="MRK.9.44" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[44]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">Some manuscripts add verse 44 (see v. 48).</span>
	        </span>
	      </span>
	      <span data-usfm="MRK.9.44" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">  </span>
	      </span>
	      <span data-usfm="MRK.9.45" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">45</span>
	        <span class="ChapterContent-module__cat7xG__content">And if your foot causes you to stumble, cut it off.</span>
	      </span>
	      <span data-usfm="MRK.9.46" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__label">[46]</span>
	        <span class="ChapterContent-module__cat7xG__note">
	          <span class="ChapterContent-module__cat7xG__label">#</span>
	          <span class="ChapterContent-module__cat7xG__body">Some manuscripts add verse 46 (see v. 48).</span>
	        </span>
	      </span>
	      <span data-usfm="MRK.9.46" class="ChapterContent-module__cat7xG__verse">
	        <span class="ChapterContent-module__cat7xG__content">  </span>
	      </span>
	    </div>
	  </div>
	</div>
	</body></html>`

	verses, err := ParseChapter(html, "MRK", 9)
	require.NoError(t, err)
	require.Len(t, verses, 4, "verse 43 + bracket 44 + verse 45 + bracket 46")

	assert.Equal(t, 43, verses[0].VerseSort)
	assert.Equal(t, "If your hand causes you to stumble, cut it off.", verses[0].Content)
	assert.Empty(t, verses[0].Note)

	assert.Equal(t, 44, verses[1].VerseSort)
	// No span.ref → fallback: note body text as content.
	assert.Equal(t, "Some manuscripts add verse 44 (see v. 48).", verses[1].Content)
	assert.Equal(t, "omitted", verses[1].Note)
	assert.Empty(t, verses[1].CrossRef)

	assert.Equal(t, 45, verses[2].VerseSort)
	assert.Equal(t, "And if your foot causes you to stumble, cut it off.", verses[2].Content)
	assert.Empty(t, verses[2].Note)

	assert.Equal(t, 46, verses[3].VerseSort)
	// No span.ref → fallback: note body text as content.
	assert.Equal(t, "Some manuscripts add verse 46 (see v. 48).", verses[3].Content)
	assert.Equal(t, "omitted", verses[3].Note)
	assert.Empty(t, verses[3].CrossRef)
}

// TestResolveRefs_UnresolvableRef verifies the fallback path when a CrossRef
// key is absent from the OutputFile (e.g. the target chapter failed to fetch
// during a partial crawl or was cancelled mid-run). The verse must receive the
// bracketed placeholder content "[See BOOK.CHAP.VERSE]" rather than an empty
// string, so the DB import never hits an empty-content error.
func TestResolveRefs_UnresolvableRef(t *testing.T) {
	// MRK book intentionally omitted to simulate a failed chapter fetch.
	out := &OutputFile{
		Books: []BookOutput{
			{
				BookUSFM: "MAT",
				Chapters: []ChapterOutput{
					{
						ChapterSort: 17,
						Verses: []VerseOutput{
							{VerseSort: 21, Content: "", Note: "ref:MRK.9.29", CrossRef: "MRK.9.29"},
						},
					},
				},
			},
		},
	}

	resolveRefs(out)

	v := out.Books[0].Chapters[0].Verses[0]
	assert.Equal(t, "[See MRK.9.29]", v.Content,
		"unresolvable CrossRef must produce a non-empty bracketed placeholder for DB import")
	assert.Equal(t, "ref:MRK.9.29", v.Note,
		"Note must be preserved unchanged after failed resolution")
	assert.Equal(t, "MRK.9.29", v.CrossRef,
		"CrossRef must be preserved for audit trail even when resolution fails")
}

// all chapters have been scraped. A verse with CrossRef="MRK.9.29" must have
// its Content filled in from the Mark 9:29 verse in the same OutputFile.
func TestResolveRefs(t *testing.T) {
	out := &OutputFile{
		Books: []BookOutput{
			{
				BookUSFM: "MAT",
				Chapters: []ChapterOutput{
					{
						ChapterSort: 17,
						Verses: []VerseOutput{
							{VerseSort: 21, Content: "", Note: "ref:MRK.9.29", CrossRef: "MRK.9.29"},
						},
					},
				},
			},
			{
				BookUSFM: "MRK",
				Chapters: []ChapterOutput{
					{
						ChapterSort: 9,
						Verses: []VerseOutput{
							{VerseSort: 29, Content: "This kind can come out only by prayer.", Note: ""},
						},
					},
				},
			},
		},
	}

	resolveRefs(out)

	mat17 := out.Books[0].Chapters[0].Verses[0]
	assert.Equal(t, "This kind can come out only by prayer.", mat17.Content,
		"resolveRefs must copy the target verse content into the cross-ref verse")
	assert.Equal(t, "ref:MRK.9.29", mat17.Note,
		"Note must remain unchanged after resolution")
	assert.Equal(t, "MRK.9.29", mat17.CrossRef,
		"CrossRef must remain for audit trail after resolution")

	// Source verse must be unchanged.
	mrk9 := out.Books[1].Chapters[0].Verses[0]
	assert.Equal(t, "This kind can come out only by prayer.", mrk9.Content)
}
