// Package biblecom — parser.go
//
// parser.go implements the HTML-to-VerseOutput transformation for a single
// bible.com chapter page. It uses goquery for CSS-selector-based navigation
// and golang.org/x/net/html for direct node-tree walking when extracting
// verse prose (avoiding intermediate DOM clones).
package biblecom

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// headingMatcher, verseMatcher, and labelMatcher are pre-compiled CSS
// selectors used by ParseChapter and isBracketVerse respectively.
//
// goquery.Selection.Find(string) calls cascadia.MustCompile on every
// invocation (cascadia v1.3.3 has no internal selector cache). Pre-compiling
// once at package init eliminates approximately 300,000 redundant
// parse+allocate cycles across a full-canon crawl (~2,378 chapter pages ×
// child div count per page).
//
//   - headingMatcher: section-heading spans (class suffix "__heading").
//     Their class always ends with the stable semantic suffix "__heading"
//     (e.g. "__s__heading", "__s1__heading") regardless of the build-hash
//     prefix. The class*= substring match is used because CSS2 provides no
//     suffix-anchor operator; there is no CSS selector equivalent of HasSuffix.
//     Used in ParseChapter's direct-child walk to detect pericope titles.
//
//   - verseMatcher: canonical verse-content spans (class suffix "__verse",
//     data-usfm attribute required). The [class*="__verse"] filter excludes
//     footnote ref spans (class="ref") that carry data-usfm attributes inside
//     __note containers — e.g. the NIV Daniel 4 footnote annotating Aramaic
//     versification. Without this class filter those spans would be treated as
//     merged-verse content and corrupt the output. (CWE-74: no external class
//     value can accidentally match unless it contains the literal "__verse".)
//
//   - labelMatcher: verse-number label spans (class suffix "__label").
//     Used by isBracketVerse to locate the outer "[N]" verse-number label
//     without descending into __note's own "#" footnote-anchor label child.
//
// NOTE: All three selectors rely on "__heading", "__verse", and "__label"
// appearing only as module-suffix tokens in bible.com's generated class names.
// If bible.com ever introduces these strings as non-suffix infixes (e.g.
// "__verse_link"), the selectors must be tightened.
var (
	headingMatcher = cascadia.MustCompile(`span[class*="__heading"]`)
	verseMatcher   = cascadia.MustCompile(`span[data-usfm][class*="__verse"]`)
	labelMatcher   = cascadia.MustCompile(`span[class*="__label"]`)
	// noteMatcher targets footnote containers inside verse spans. Used by
	// extractCrossRef and extractNoteBodyText to navigate into the __note.
	noteMatcher = cascadia.MustCompile(`span[class*="__note"]`)
	// bodyMatcher targets the __body span inside a __note, which holds the
	// footnote text (optionally preceded by a __fr "CHAP:VERSE " prefix span).
	bodyMatcher = cascadia.MustCompile(`span[class*="__body"]`)
	// refSpanMatcher targets inline scripture-reference spans inside footnotes.
	// bible.com emits these as <span data-usfm="MRK.9.29" class="ref">…</span>.
	// The class="ref" is an exact-match selector (not class*=), so it never
	// accidentally picks up verse spans whose class contains "__verse".
	refSpanMatcher = cascadia.MustCompile(`span[data-usfm][class="ref"]`)
)

// bracketVersePattern matches verse-number labels that use the "[N]" format
// (e.g. "[21]", "[44]") that bible.com uses for textually-disputed verses
// absent from the translation. The pattern requires one or more digits between
// the brackets, which prevents false matches against empty brackets "[]",
// alphabetic labels "[abc]", or compound notations "[21] [22]".
var bracketVersePattern = regexp.MustCompile(`^\[\d+\]$`)

// usfmCodePattern validates that a USFM book code is a well-defined 2–5
// character uppercase-alphanumeric format used by bible.com
// (e.g. "GEN", "1CO", "SNG", "PHIL"). The value is validated before it is
// interpolated into goquery CSS selector strings inside ParseChapter; without
// this check a caller that supplies an attacker-controlled bookUSFM value
// could inject arbitrary CSS selector syntax into doc.Find().
// (CWE-74: Improper Neutralisation of Special Elements in Output Used by a
// Downstream Component — OWASP A03:2021 Injection)
var usfmCodePattern = regexp.MustCompile(`^[A-Z0-9]{2,5}$`)

// typicalVersesPerChapter is the pre-allocation hint for verseOrder in
// ParseChapter. 30 covers the median chapter without over-allocating; outliers
// like Psalm 119 (176 verses) still grow correctly via append.
const typicalVersesPerChapter = 30

// maxChapterPerBook is the inclusive upper bound accepted for the chapNum
// argument to ParseChapter. Psalms is the longest canonical book with 150
// chapters; 200 gives safe headroom for any extended canon without being
// absurdly permissive.
const maxChapterPerBook = 200

// mergedVerseContent is the sentinel stored for secondary (non-primary) verses
// that share their content with the previous verse on bible.com (e.g. when
// verse numbers 9 and 10 are displayed merged into one block). The convention
// "併於上節。" (Merged with the verse above) is used by the same CUNP-上帝
// printed bible to indicate that a verse's text is carried by the prior verse.
const mergedVerseContent = "併於上節。"


// ParseChapter parses the raw HTML of a bible.com chapter page and returns
// an ordered slice of VerseOutput values for the given book/chapter.
//
// The selector strategy intentionally avoids brittle full CSS class names such
// as "ChapterContent-module__cat7xG__s1", which contain a build-time hash that
// changes across deployments. Instead, stable data-* attributes and partial
// class substring selectors (class*="__heading") are used throughout.
//
// Key parsing rules:
//   - Chapter container is identified by data-usfm="BOOK.CHAP".
//   - Its direct children are walked in document order; this preserves the
//     relative position of section headings (sub_titles) and verse containers.
//   - A child div that contains a span[class*="__heading"] is a section heading.
//     Its text is stored as pendingSubTitle and attached to the next new verse.
//   - All other child divs are verse containers; each span[data-usfm] inside
//     them is a verse reference.
//   - When the same data-usfm appears more than once (e.g. poetry split across
//     multiple <div class="...q1/q2"> containers), the content is concatenated
//     with a single space. This is expected and normal for poetic books.
//   - When a single span carries multiple verse refs — separated by either a
//     space (e.g. data-usfm="2SA.3.9 2SA.3.10") or a "+" with no surrounding
//     whitespace (e.g. data-usfm="2SA.3.9+2SA.3.10") — bible.com is rendering
//     merged verses.  ParseChapter normalises both separator styles before
//     parsing, so callers do not need to pre-process the attribute.
//     The verse with the smallest sort number receives the actual content; all
//     other verse numbers in the group receive mergedVerseContent ("併於上節。")
//     and are annotated with Note="merged" in the returned VerseOutput slice.
//   - Footnotes (span[class*="__note"]) and verse-number labels
//     (span[class*="__label"]) are stripped before extracting text, so the
//     returned Content strings contain only the verse prose.
//   - Bracket-labeled verses ([N]) whose span contains only a __note element
//     and no prose — e.g. NIV Matthew 17:21 — are resolved as follows:
//     (a) If the __note contains a <span class="ref" data-usfm="BOOK.CHAP.V">
//         cross-reference, Content is left empty and CrossRef is set; the
//         caller's resolveRefs pass fills in the actual verse text after all
//         chapters are crawled.
//     (b) If no cross-reference span is found, the footnote body text is
//         extracted from the __note and stored directly as the verse content.
//     In both cases Note is set ("ref:BOOK.CHAP.V" or "omitted") and the DB
//     repository always receives non-empty content.
//   - Whitespace-only content spans (e.g. the indent-spacer span that bible.com
//     emits after a bracket verse) are detected via strings.TrimSpace and skipped
//     unless they are the sole occurrence of a bracket verse.
func ParseChapter(rawHTML string, bookUSFM string, chapNum int) ([]VerseOutput, error) {
	if !usfmCodePattern.MatchString(bookUSFM) {
		return nil, fmt.Errorf("invalid USFM code %q: must match ^[A-Z0-9]{2,5}$", bookUSFM)
	}
	// Psalms is the longest book with 150 chapters; maxChapterPerBook=200 gives
	// safe headroom for any future extended canons without being absurdly permissive.
	if chapNum <= 0 || chapNum > maxChapterPerBook {
		return nil, fmt.Errorf("invalid chapter number %d for %s", chapNum, bookUSFM)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawHTML))
	if err != nil {
		return nil, fmt.Errorf("parse HTML for %s ch%d: %w", bookUSFM, chapNum, err)
	}

	// The chapter <div> is identified by its data-usfm attribute, e.g.
	// data-usfm="GEN.1". Using a data attribute avoids coupling to class names.
	chapterSel := doc.Find(fmt.Sprintf(`div[data-usfm="%s.%d"]`, bookUSFM, chapNum))
	if chapterSel.Length() == 0 {
		// Some books (e.g. Obadiah) have only one chapter; bible.com renders
		// single-chapter books with just the USFM code, no chapter number.
		// Attempt alternate selector as a fallback: data-usfm="BOOK".
		chapterSel = doc.Find(fmt.Sprintf(`div[data-usfm="%s"]`, bookUSFM))
	}
	if chapterSel.Length() == 0 {
		return nil, fmt.Errorf("chapter div not found for %s ch%d", bookUSFM, chapNum)
	}
	// Use only the first match in case of duplicate data-usfm attributes.
	chapterSel = chapterSel.First()

	// verseEntry accumulates text fragments and metadata for one verse number.
	// Using a []string fragment slice instead of a plain string eliminates
	// intermediate string allocations when a poetry verse spans multiple child
	// divs; fragments are joined with a single space during the assembly step.
	// merged=true marks a secondary verse in a merged-verse group (content is
	// always mergedVerseContent for these entries; note is set in the output).
	// omitted=true marks a bracket-labeled verse: content may be empty pending
	// resolveRefs (when refUSFM is set) or equal to the footnote body text
	// (when no specific cross-reference is found in the __note).
	// refUSFM holds the USFM key of the cross-referenced verse (e.g. "MRK.9.29")
	// so that resolveRefs can look it up and fill in the actual verse content.
	type verseEntry struct {
		sort      int
		subTitle  string
		fragments []string // verse text pieces; joined with " " when finalised
		merged    bool     // true → secondary verse in a merged-verse group
		omitted   bool     // true → bracket-labeled verse absent from this translation
		refUSFM   string   // USFM of the cross-referenced source verse (e.g. "MRK.9.29")
	}

	var (
		// versesMap stores verse data keyed by verse sort number.
		// Separate verseOrder slice preserves the first-seen document order.
		// Pre-sized to the typical chapter length (30 verses) to avoid the
		// 4–5 doubling reallocations that occur growing from a nil slice,
		// while still handling outliers like Psalm 119 (176 verses) correctly.
		versesMap  = make(map[int]*verseEntry)
		verseOrder = make([]int, 0, typicalVersesPerChapter)
		pendingSub string // section heading waiting to be attached to next verse
	)

	// Walk the direct children of the chapter div in document order so that
	// section headings (which are sibling divs, not ancestors of verse spans)
	// are observed before the verses that follow them.
	chapterSel.Children().Each(func(_ int, child *goquery.Selection) {
		// Detect section-heading containers. The heading text lives inside a
		// span with class containing "__heading" (stable semantic suffix used
		// by both Chinese __s and English __s1 containers).
		// FindMatcher reuses the pre-compiled headingMatcher selector instead
		// of calling cascadia.MustCompile on every child div iteration.
		if heading := child.FindMatcher(headingMatcher); heading.Length() > 0 {
			pendingSub = strings.TrimSpace(heading.Text())
			return
		}

		// For every other direct child, find all verse spans within it.
		// span[data-usfm][class*="__verse"] targets only canonical verse-content
		// spans, which always carry the "__verse" class suffix regardless of the
		// build-hash prefix. This selector excludes "ref" spans that carry
		// data-usfm attributes but appear inside __note footnotes (e.g. the NIV
		// Daniel 4 footnote that annotates Aramaic versification differences:
		// data-usfm="DAN.4.1+...+DAN.4.37" class="ref"). Without the class
		// filter, those footnote ref spans would be mistakenly treated as merged
		// verse content and corrupt the output for the following individual spans.
		//
		// FindMatcher reuses the pre-compiled verseMatcher selector; calling
		// Find(string) here would re-parse the selector string on every child div.
		child.FindMatcher(verseMatcher).Each(func(_ int, verseSel *goquery.Selection) {
			usfmVal, exists := verseSel.Attr("data-usfm")
			if !exists {
				return
			}

			// bible.com uses two distinct separators between merged-verse refs,
			// depending on translation:
			//   • space-separated (most translations): data-usfm="2SA.3.9 2SA.3.10"
			//   • plus-separated  (e.g. CUNP-上帝):   data-usfm="2SA.3.9+2SA.3.10"
			// In the "+" format the refs are adjacent with no surrounding whitespace,
			// so a plain strings.Fields on the raw value would return a single token
			// "2SA.3.9+2SA.3.10" instead of two distinct refs — silently losing the
			// merge relationship.  Replacing "+" with " " first lets strings.Fields
			// handle both formats in one unified pass; its whitespace-run collapsing
			// also defends against stray double-spaces or leading/trailing spaces in
			// scraped HTML attribute values, removing the need for a separate trim step.
			refs := strings.Fields(strings.ReplaceAll(usfmVal, "+", " "))
			if len(refs) == 0 {
				return
			}

			// Collect all valid verse numbers from this ref group. Validation
			// ensures every ref follows BOOK.CHAP.VERSE format with a positive
			// integer verse number. Refs from a different book or chapter are
			// silently rejected to guard against malformed data-usfm attributes
			// (e.g. a cross-chapter bridge span on a chapter-boundary page).
			verseNums := make([]int, 0, len(refs))
			for _, ref := range refs {
				parts := strings.SplitN(ref, ".", 3)
				if len(parts) != 3 {
					continue
				}
				// Guard: reject refs that belong to a different book or chapter.
				// This prevents a stray ref like "EXO.2.1" inside a GEN.1 page
				// from injecting a verse number into the wrong chapter output.
				if parts[0] != bookUSFM {
					continue
				}
				cn, cnErr := strconv.Atoi(parts[1])
				if cnErr != nil || cn != chapNum {
					continue
				}
				vn, err := strconv.Atoi(parts[2])
				// maxVersePerChapter is a generous ceiling; no canonical chapter in
				// any known translation exceeds 176 verses (Psalm 119). 300 gives
				// safe headroom while still rejecting clearly bogus scraped values
				// (e.g. data-usfm="GEN.1.99999") that would corrupt the JSON output.
				const maxVersePerChapter = 300
				if err != nil || vn <= 0 || vn > maxVersePerChapter {
					continue
				}
				verseNums = append(verseNums, vn)
			}
			if len(verseNums) == 0 {
				return // no valid verse number found in this span; skip
			}

			// We scan all numbers for the minimum rather than trusting
			// verseNums[0] because bible.com does not guarantee ascending ref
			// order within the data-usfm attribute (see the out-of-order test).
			primaryNum := verseNums[0]
			for _, vn := range verseNums[1:] {
				if vn < primaryNum {
					primaryNum = vn
				}
			}

			content := extractContent(verseSel)
			// Use TrimSpace so that whitespace-only spans (e.g. bible.com's
			// paragraph indent spacers that follow a bracket verse) are caught
			// by the same branch as truly empty strings.

			// isBracket and crossRef carry results out of the empty-content
			// branch into the verseEntry constructor. Using explicit bools/strings
			// avoids fragile post-hoc string comparisons at the constructor site.
			isBracket := false
			crossRef := ""
			if strings.TrimSpace(content) == "" {
				if isBracketVerse(verseSel) {
					// Security guard: if this verse was already recorded as
					// omitted (e.g. an additional bracket-labeled duplicate span),
					// skip the new span entirely to prevent double-stamping the
					// cross-ref as a poetry-continuation fragment. (CWE-20)
					if existing, seen := versesMap[primaryNum]; seen && existing.omitted {
						return
					}
					crossRef = extractCrossRef(verseSel)
					if crossRef != "" {
						// Cross-reference found: leave content empty so
						// resolveRefs (called in scraper.RunWithContext after
						// all chapters are crawled) can look up the target
						// verse and fill in the actual prose.
						content = ""
					} else {
						// No specific verse cross-reference in the __note:
						// extract the footnote body text directly and use it
						// as the verse content.  This is the fallback for the
						// rare case where a bracket verse's footnote describes
						// the omission in general terms rather than pointing to
						// a specific verse.
						content = extractNoteBodyText(verseSel)
						if strings.TrimSpace(content) == "" {
							// Final safety net: content must never be empty in
							// the DB.  Use a brief neutral placeholder.
							content = "[verse not in this translation]"
						}
					}
					isBracket = true
				} else {
					// Ordinary whitespace-only span: paragraph-continuation
					// indent marker — nothing to record.
					return
				}
			}

			// Record the primary verse with the actual scraped content.
			if _, seen := versesMap[primaryNum]; !seen {
				// First occurrence: create a new verse entry and consume the
				// pending section heading (if any). The heading is cleared so
				// it is only attached to the immediately-following verse.
				versesMap[primaryNum] = &verseEntry{
					sort:      primaryNum,
					subTitle:  pendingSub,
					fragments: []string{content},
					omitted:   isBracket, // set from detection result, not string comparison
					refUSFM:   crossRef,  // non-empty only for cross-referenced bracket verses
				}
				verseOrder = append(verseOrder, primaryNum)
				pendingSub = "" // consumed; next verse gets a fresh slate
			} else {
				// Subsequent occurrence of the same primary verse (poetry line
				// continuation): append as a new fragment.
				versesMap[primaryNum].fragments = append(versesMap[primaryNum].fragments, content)
			}

			// Record every non-primary verse in the merged group.
			// This block is guarded on len > 1 because merged verses are rare
			// (tens of instances per testament). Allocating secondaries and
			// calling sort.Ints on every single-verse span — the vast majority
			// of ~31,000 verse spans — wastes allocations and sort dispatch on
			// empty or zero-capacity slices. The guard skips the block entirely
			// for the common case at zero cost.
			if len(verseNums) > 1 {
				// Collect and sort the secondaries ascending so that 3-way merges
				// (e.g. data-usfm="X.1.5 X.1.3 X.1.4") emit sentinel rows in the
				// correct canonical order [3]primary → [4]merged → [5]merged
				// rather than the attribute's textual order.
				secondaries := make([]int, 0, len(verseNums)-1)
				for _, vn := range verseNums {
					if vn != primaryNum {
						secondaries = append(secondaries, vn)
					}
				}
				sort.Ints(secondaries)

				for _, vn := range secondaries {
					// This guard can trigger in poetic passages where the same verse
					// number appears independently in one structural container and
					// again as a secondary ref in a later merged-verse span. Without
					// the guard we would overwrite the legitimate primary entry.
					if _, seen := versesMap[vn]; seen {
						continue
					}
					versesMap[vn] = &verseEntry{
						sort:      vn,
						fragments: []string{mergedVerseContent},
						merged:    true,
					}
					verseOrder = append(verseOrder, vn)
				}
			}
		})
	})

	// Assemble the ordered result slice. Whitespace normalisation is applied
	// here — not inside extractContent — so it runs exactly once per verse
	// regardless of fragment count. For the common single-fragment verse,
	// strings.Join is skipped entirely to avoid its slice allocation.
	// Merged secondary verses receive Note="merged" to document in the JSON
	// output that their content was not independently scraped.
	result := make([]VerseOutput, 0, len(verseOrder))
	for _, vnum := range verseOrder {
		e := versesMap[vnum]
		// Build rawContent without an intermediate normalised string in the
		// fragments — extractContent now returns raw text so this is the sole
		// normalisation site.
		var rawContent string
		if len(e.fragments) == 1 {
			rawContent = e.fragments[0] // skip strings.Join allocation for common case
		} else {
			rawContent = strings.Join(e.fragments, " ")
		}
		v := VerseOutput{
			VerseSort: e.sort,
			Content:   normaliseSpace(rawContent),
		}
		if e.subTitle != "" {
			v.SubTitle = e.subTitle
		}
		// switch enforces mutual exclusivity: a verse cannot be both merged
		// and omitted.
		switch {
		case e.omitted && e.refUSFM != "":
			// Bracket verse resolved via cross-reference: content is filled in
			// by resolveRefs after this function returns.  CrossRef and Note
			// both retain the source USFM for JSON auditability.
			v.Note = "ref:" + e.refUSFM
			v.CrossRef = e.refUSFM
		case e.omitted:
			// Bracket verse with no specific cross-reference — content is the
			// footnote body text extracted directly from the __note element.
			v.Note = "omitted"
		case e.merged:
			v.Note = "merged" // secondary verse in a merged-verse group
		}
		result = append(result, v)
	}
	return result, nil
}

// extractContent collects verse prose text from verseSel by walking the
// underlying html.Node tree directly — without cloning the goquery Selection.
//
// Subtrees rooted at a span whose class attribute contains "__note" (inline
// footnote markers, e.g. "# 1:1 …") or "__label" (verse-number labels, e.g.
// "1" for normal verses, "[21]" for bracket-labeled omitted verses) are
// skipped; all other text nodes are written to a strings.Builder.
// As a consequence, bracket-labeled verses (see isBracketVerse) always return
// an empty string from extractContent, which the caller detects and replaces
// with omittedVerseContent.
//
// The returned string is raw (not whitespace-normalised). Callers are
// responsible for a single normaliseSpace call at the assembly point so that
// normalisation runs exactly once per verse regardless of fragment count.
//
// This replaces the previous Clone + Remove approach, which allocated a full
// O(subtree-size) copy of each verse's DOM on every call (~73 K times across
// a full canon crawl). Walking the live tree is O(subtree-size) in time but
// O(1) in extra allocations (one strings.Builder per verse).
func extractContent(verseSel *goquery.Selection) string {
	if len(verseSel.Nodes) == 0 {
		return ""
	}
	var sb strings.Builder
	collectText(verseSel.Nodes[0], &sb)
	return sb.String() // raw; normalised once at assembly in ParseChapter
}

// collectText recursively writes all text-node data within n to sb,
// skipping any subtree rooted at a <span> whose class contains "__note"
// or "__label". This mirrors the semantics of the previous Clone+Remove
// approach without any intermediate DOM allocation.
func collectText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
		return
	}
	if n.Type == html.ElementNode && n.Data == "span" {
		cls := nodeAttr(n, "class")
		if strings.Contains(cls, "__note") || strings.Contains(cls, "__label") {
			return // skip this span and its entire subtree
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, sb)
	}
}

// nodeAttr returns the value of the named attribute from n, or "" when absent.
// Reading html.Node.Attr directly avoids the overhead of building a transient
// goquery.Selection just to call .Attr() on a single known node.
func nodeAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// isBracketVerse reports whether verseSel is a bracket-labeled verse —
// i.e. its direct-child __label span contains "[N]" (e.g. "[21]", "[44]")
// rather than plain "N".
//
// In NIV and similar modern translations, textually-disputed verses are
// rendered on bible.com with a bracketed number like "[21]" instead of "21",
// and the span contains only a __note element (no prose text). This pattern
// means the verse is intentionally absent from this translation because it
// does not appear in the earliest manuscripts relied on by the translators.
//
// The search is restricted to direct children of verseSel (not all
// descendants) so that the "#" label nested inside a __note grandchild cannot
// be returned before the verse's own "[N]" label regardless of DOM ordering.
// bracketVersePattern (`^\[\d+\]$`) further tightens the check so that
// labels like "[]", "[abc]", or "[21a]" are not misclassified as bracket
// verses (CWE-20: Improper Input Validation).
//
// When isBracketVerse returns true, the caller invokes extractCrossRef to
// locate the specific verse being referenced, then either leaves Content
// empty (to be resolved by resolveRefs) or calls extractNoteBodyText for
// the fallback case.
//
// Known NIV examples: Matthew 17:21, 18:11, 23:14; Mark 7:16, 9:44, 9:46,
// 11:26, 15:28; Luke 17:36, 23:17; John 5:4; Acts 8:37, 15:34, 24:7, 28:29;
// Romans 16:24.
func isBracketVerse(verseSel *goquery.Selection) bool {
	// Children().FilterMatcher restricts to direct children, eliminating the
	// ordering dependency on the "#" footnote label inside __note.
	label := verseSel.Children().FilterMatcher(labelMatcher).First()
	if label.Length() == 0 {
		return false
	}
	return bracketVersePattern.MatchString(strings.TrimSpace(label.Text()))
}

// extractCrossRef returns the first data-usfm attribute found on a
// <span class="ref" data-usfm="…"> element inside the verse's __note.
// Returns "" when no such span is present (i.e. the note mentions the
// omission in general terms without pointing to a specific verse).
//
// Example: for NIV Matthew 17:21 the __note contains:
//
//	<span data-usfm="MRK.9.29" class="ref">Mark 9:29</span>
//
// extractCrossRef returns "MRK.9.29".
//
// When multiple ref spans are present (rare), only the first is returned.
func extractCrossRef(verseSel *goquery.Selection) string {
	note := verseSel.Children().FilterMatcher(noteMatcher)
	if note.Length() == 0 {
		return ""
	}
	// refSpanMatcher uses class="ref" (exact match), so it cannot accidentally
	// pick up verse spans or other data-usfm-bearing elements.
	ref := note.FindMatcher(refSpanMatcher).First()
	if ref.Length() == 0 {
		return ""
	}
	// Take only the first token in case data-usfm is a space-separated list.
	usfm, _ := ref.Attr("data-usfm")

	// Guard: strings.Fields("") returns []string{}, and [][0] would panic.
	// This mirrors the identical guard in ParseChapter's verse-ref parsing loop.
	// (CWE-129: Improper Validation of Array Index)
	tokens := strings.Fields(strings.TrimSpace(usfm))
	if len(tokens) == 0 {
		return ""
	}
	candidate := tokens[0] // e.g. "MRK.9.29"

	// Validate the BOOK.CHAP.VERSE structure before returning.
	// This ensures the value stored in CrossRef, used as a map key, logged,
	// and potentially embedded in DB content is structurally sound.
	// (CWE-20: Improper Input Validation)
	parts := strings.SplitN(candidate, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	if !usfmCodePattern.MatchString(parts[0]) {
		return ""
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return ""
	}
	if vn, err := strconv.Atoi(parts[2]); err != nil || vn <= 0 {
		return ""
	}
	return candidate
}

// extractNoteBodyText returns the human-readable footnote text from the
// __body span inside a verse's __note element, stripping the "CHAP:VERSE "
// reference prefix (span[class*="__fr"]) if present.
//
// This is used as the verse content for bracket verses whose __note does not
// contain a specific cross-reference span — e.g. when a note simply states
// "Some manuscripts include additional material here" without naming a verse.
//
// Example: for a __note like
//
//	<span class="__body"><span class="__fr">9:44 </span><span class="ft">See v. 48.</span></span>
//
// extractNoteBodyText returns "See v. 48."
func extractNoteBodyText(verseSel *goquery.Selection) string {
	note := verseSel.Children().FilterMatcher(noteMatcher)
	if note.Length() == 0 {
		return ""
	}
	body := note.FindMatcher(bodyMatcher).First()
	if body.Length() == 0 {
		return ""
	}
	// Collect text from each direct child, skipping the __fr reference prefix.
	var sb strings.Builder
	body.Children().Each(func(_ int, child *goquery.Selection) {
		cls, _ := child.Attr("class")
		if strings.Contains(cls, "__fr") {
			return // skip "17:21 " chapter:verse prefix
		}
		sb.WriteString(child.Text())
	})
	result := normaliseSpace(sb.String())
	if result == "" && body.Children().Length() == 0 {
		// Truly no child elements: body is a single raw text node.
		// body.Text() is safe here because there are no __fr children to re-include.
		result = normaliseSpace(body.Text())
	}
	// If children existed but were all __fr (all skipped), result stays "".
	// The caller's "[verse not in this translation]" safety net handles that case.
	return result
}

// normaliseSpace replaces any run of whitespace characters (space, tab,
// newline, ideographic space U+3000) with a single ASCII space and strips
// leading/trailing whitespace. This converts poetry-continuation joins (which
// may produce double spaces) into clean prose strings without manual trimming
// at each concatenation point.
func normaliseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
