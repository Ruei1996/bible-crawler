// cmd/spec-builder/main.go discovers the actual per-chapter verse counts for
// both Chinese 和合本 (CUV) and English BBE from the source website, then writes
// two language-specific JSON spec files:
//
//	bible_books_zh.json — Chinese names only (name_zh, testament_zh)
//	bible_books_en.json — English names only (name_en, testament)
//
// Run this command ONCE before the main crawler (and again whenever the source
// website changes):
//
//	go run cmd/spec-builder/main.go
//
// After it completes, run the crawler:
//
//	go run cmd/crawler/main.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"bible-crawler/internal/utils"

	"github.com/PuerkitoBio/goquery"
)

// ── Static book metadata ──────────────────────────────────────────────────────

// bookMeta holds the compile-time book names and testament classification.
// These values never change between runs; only the verse counts are discovered
// dynamically by fetching the actual chapter pages.
type bookMeta struct {
	Number      int
	Testament   string // "OT" or "NT" (for English file)
	TestamentZH string // "舊約" or "新約" (for Chinese file)
	NameZH      string
	NameEN      string
}

// defaultBookMeta is the authoritative static list of all 66 canonical books.
// It is always used as the source of names and testament labels.
// The spec-builder never reads the existing JSON files for these values.
var defaultBookMeta = [66]bookMeta{
	{1, "OT", "舊約", "創世記", "Genesis"},
	{2, "OT", "舊約", "出埃及記", "Exodus"},
	{3, "OT", "舊約", "利未記", "Leviticus"},
	{4, "OT", "舊約", "民數記", "Numbers"},
	{5, "OT", "舊約", "申命記", "Deuteronomy"},
	{6, "OT", "舊約", "約書亞記", "Joshua"},
	{7, "OT", "舊約", "士師記", "Judges"},
	{8, "OT", "舊約", "路得記", "Ruth"},
	{9, "OT", "舊約", "撒母耳記上", "1 Samuel"},
	{10, "OT", "舊約", "撒母耳記下", "2 Samuel"},
	{11, "OT", "舊約", "列王紀上", "1 Kings"},
	{12, "OT", "舊約", "列王紀下", "2 Kings"},
	{13, "OT", "舊約", "歷代志上", "1 Chronicles"},
	{14, "OT", "舊約", "歷代志下", "2 Chronicles"},
	{15, "OT", "舊約", "以斯拉記", "Ezra"},
	{16, "OT", "舊約", "尼希米記", "Nehemiah"},
	{17, "OT", "舊約", "以斯帖記", "Esther"},
	{18, "OT", "舊約", "約伯記", "Job"},
	{19, "OT", "舊約", "詩篇", "Psalms"},
	{20, "OT", "舊約", "箴言", "Proverbs"},
	{21, "OT", "舊約", "傳道書", "Ecclesiastes"},
	{22, "OT", "舊約", "雅歌", "Song of Solomon"},
	{23, "OT", "舊約", "以賽亞書", "Isaiah"},
	{24, "OT", "舊約", "耶利米書", "Jeremiah"},
	{25, "OT", "舊約", "耶利米哀歌", "Lamentations"},
	{26, "OT", "舊約", "以西結書", "Ezekiel"},
	{27, "OT", "舊約", "但以理書", "Daniel"},
	{28, "OT", "舊約", "何西阿書", "Hosea"},
	{29, "OT", "舊約", "約珥書", "Joel"},
	{30, "OT", "舊約", "阿摩司書", "Amos"},
	{31, "OT", "舊約", "俄巴底亞書", "Obadiah"},
	{32, "OT", "舊約", "約拿書", "Jonah"},
	{33, "OT", "舊約", "彌迦書", "Micah"},
	{34, "OT", "舊約", "那鴻書", "Nahum"},
	{35, "OT", "舊約", "哈巴谷書", "Habakkuk"},
	{36, "OT", "舊約", "西番雅書", "Zephaniah"},
	{37, "OT", "舊約", "哈該書", "Haggai"},
	{38, "OT", "舊約", "撒迦利亞書", "Zechariah"},
	{39, "OT", "舊約", "瑪拉基書", "Malachi"},
	{40, "NT", "新約", "馬太福音", "Matthew"},
	{41, "NT", "新約", "馬可福音", "Mark"},
	{42, "NT", "新約", "路加福音", "Luke"},
	{43, "NT", "新約", "約翰福音", "John"},
	{44, "NT", "新約", "使徒行傳", "Acts"},
	{45, "NT", "新約", "羅馬書", "Romans"},
	{46, "NT", "新約", "哥林多前書", "1 Corinthians"},
	{47, "NT", "新約", "哥林多後書", "2 Corinthians"},
	{48, "NT", "新約", "加拉太書", "Galatians"},
	{49, "NT", "新約", "以弗所書", "Ephesians"},
	{50, "NT", "新約", "腓立比書", "Philippians"},
	{51, "NT", "新約", "歌羅西書", "Colossians"},
	{52, "NT", "新約", "帖撒羅尼迦前書", "1 Thessalonians"},
	{53, "NT", "新約", "帖撒羅尼迦後書", "2 Thessalonians"},
	{54, "NT", "新約", "提摩太前書", "1 Timothy"},
	{55, "NT", "新約", "提摩太後書", "2 Timothy"},
	{56, "NT", "新約", "提多書", "Titus"},
	{57, "NT", "新約", "腓利門書", "Philemon"},
	{58, "NT", "新約", "希伯來書", "Hebrews"},
	{59, "NT", "新約", "雅各書", "James"},
	{60, "NT", "新約", "彼得前書", "1 Peter"},
	{61, "NT", "新約", "彼得後書", "2 Peter"},
	{62, "NT", "新約", "約翰一書", "1 John"},
	{63, "NT", "新約", "約翰二書", "2 John"},
	{64, "NT", "新約", "約翰三書", "3 John"},
	{65, "NT", "新約", "猶大書", "Jude"},
	{66, "NT", "新約", "啟示錄", "Revelation"},
}

// ── Chapter count tables ──────────────────────────────────────────────────────

// bibleChapterCounts is the total number of chapters per book.
// Both translations share the same chapter count; only verse counts differ.
// Index 0 = Genesis, index 65 = Revelation.
var bibleChapterCounts = [66]int{
	50, 40, 27, 36, 34, 24, 21, 4, 31, 24,
	22, 25, 29, 36, 10, 13, 10, 42, 150, 31,
	12, 8, 66, 52, 5, 48, 12, 14, 3, 9,
	1, 4, 7, 3, 3, 3, 2, 14, 4,
	28, 16, 24, 21, 28, 16, 16, 13, 6, 6,
	4, 4, 5, 3, 6, 4, 3, 1, 13, 5,
	5, 3, 5, 1, 1, 1, 22,
}

// globalChapStarts[i] = 1-based global chapter index for book i's first chapter.
// Used to construct the source website's "chap=" URL parameter.
var globalChapStarts [66]int

func init() {
	offset := 1
	for i := 0; i < 66; i++ {
		globalChapStarts[i] = offset
		offset += bibleChapterCounts[i]
	}
}

// ── JSON output shapes ────────────────────────────────────────────────────────
// Each file has its own dedicated struct so that the serialized JSON contains
// only fields relevant to that language — no cross-language fields appear.

// zhBookJSON is the per-book record written to bible_books_zh.json.
// Contains only Chinese names and the Chinese testament label.
type zhBookJSON struct {
	Number           int            `json:"number"`
	TestamentZH      string         `json:"testament_zh"`
	NameZH           string         `json:"name_zh"`
	TotalChapters    int            `json:"total_chapters"`
	TotalVerses      int            `json:"total_verses"`
	VersesPerChapter map[string]int `json:"verses_per_chapter"`
}

// enBookJSON is the per-book record written to bible_books_en.json.
// Contains only the English name and the canonical testament abbreviation.
type enBookJSON struct {
	Number           int            `json:"number"`
	Testament        string         `json:"testament"`
	NameEN           string         `json:"name_en"`
	TotalChapters    int            `json:"total_chapters"`
	TotalVerses      int            `json:"total_verses"`
	VersesPerChapter map[string]int `json:"verses_per_chapter"`
}

// summaryBlock carries aggregate verse and chapter counts for the whole Bible
// and each testament. Used by both output files.
type summaryBlock struct {
	TotalBooks    int `json:"total_books"`
	TotalChapters int `json:"total_chapters"`
	TotalVerses   int `json:"total_verses"`
	OldTestament  struct {
		Books    int `json:"books"`
		Chapters int `json:"chapters"`
		Verses   int `json:"verses"`
	} `json:"old_testament"`
	NewTestament struct {
		Books    int `json:"books"`
		Chapters int `json:"chapters"`
		Verses   int `json:"verses"`
	} `json:"new_testament"`
}

// zhOutputJSON is the top-level document written to bible_books_zh.json.
type zhOutputJSON struct {
	Title   string       `json:"title"`
	Version string       `json:"version"`
	Summary summaryBlock `json:"summary"`
	Books   []zhBookJSON `json:"books"`
}

// enOutputJSON is the top-level document written to bible_books_en.json.
type enOutputJSON struct {
	Title   string       `json:"title"`
	Version string       `json:"version"`
	Summary summaryBlock `json:"summary"`
	Books   []enBookJSON `json:"books"`
}

// ── HTTP fetch ────────────────────────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 30 * time.Second}

// fetchVerseCount fetches one chapter page and returns the highest verse number
// found in the <ol><li> elements, which equals the actual verse count for that
// chapter in that translation.
func fetchVerseCount(globalChap int, isChinese bool) (int, error) {
	var pageURL string
	if isChinese {
		pageURL = fmt.Sprintf(
			"https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0",
			globalChap)
	} else {
		pageURL = fmt.Sprintf(
			"https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe",
			globalChap)
	}

	resp, err := httpClient.Get(pageURL)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", pageURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d for %s", resp.StatusCode, pageURL)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body %s: %w", pageURL, err)
	}

	// Chinese pages are Big5-encoded; English pages are plain ASCII/UTF-8.
	var body string
	if isChinese {
		body, err = utils.Big5ToUTF8(raw)
		if err != nil {
			return 0, fmt.Errorf("Big5 decode: %w", err)
		}
	} else {
		body = string(raw)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("parse HTML: %w", err)
	}
	// Remove non-verse DOM noise before iterating list items.
	doc.Find("script, style, a").Remove()

	maxVerse := 0
	itemCount := 0
	doc.Find("ol li").Each(func(i int, sel *goquery.Selection) {
		itemCount++
		// Prefer the explicit value= attribute when present; fall back to position.
		verseNum := i + 1
		if valStr, exists := sel.Attr("value"); exists {
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(valStr)); parseErr == nil && parsed > 0 {
				verseNum = parsed
			}
		}
		if verseNum > maxVerse {
			maxVerse = verseNum
		}
	})

	// Some pages may not carry explicit value= attributes; use count as fallback.
	if maxVerse == 0 && itemCount > 0 {
		maxVerse = itemCount
	}
	if maxVerse == 0 {
		return 0, fmt.Errorf("no verses found at %s", pageURL)
	}
	return maxVerse, nil
}

// ── Concurrent fetch ──────────────────────────────────────────────────────────

// chapterResult carries the fetched verse count for one book/chapter/language.
type chapterResult struct {
	bookIdx   int
	chapNum   int // 1-based chapter number within the book
	isChinese bool
	count     int
	err       error
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	zhPath := filepath.Join(projectRoot, "bible_books_zh.json")
	enPath := filepath.Join(projectRoot, "bible_books_en.json")

	// Count total HTTP requests upfront for progress reporting.
	totalRequests := 0
	for _, c := range bibleChapterCounts {
		totalRequests += c * 2 // one ZH request + one EN request per chapter
	}
	log.Printf("Spec-builder starting: %d HTTP requests (1189 chapters × 2 languages).", totalRequests)
	log.Println("This will take several minutes. Please wait…")

	// Semaphore limits concurrent outbound connections to stay polite.
	const maxConcurrent = 5
	sem := make(chan struct{}, maxConcurrent)
	results := make(chan chapterResult, totalRequests)

	var wg sync.WaitGroup
	for bookIdx := 0; bookIdx < 66; bookIdx++ {
		for chap := 1; chap <= bibleChapterCounts[bookIdx]; chap++ {
			globalChap := globalChapStarts[bookIdx] + chap - 1
			for _, isChinese := range []bool{true, false} {
				wg.Add(1)
				// Capture loop variables for the goroutine closure.
				isChinese := isChinese
				bookIdx := bookIdx
				chap := chap
				globalChap := globalChap
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					// Small delay per request to avoid overwhelming the server.
					time.Sleep(50 * time.Millisecond)
					count, err := fetchVerseCount(globalChap, isChinese)
					results <- chapterResult{
						bookIdx:   bookIdx,
						chapNum:   chap,
						isChinese: isChinese,
						count:     count,
						err:       err,
					}
				}()
			}
		}
	}

	// Close results once all goroutines have finished sending.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Accumulate verse counts into per-book slices indexed by (chapNum - 1).
	zhCounts := [66][]int{}
	enCounts := [66][]int{}
	for i := 0; i < 66; i++ {
		zhCounts[i] = make([]int, bibleChapterCounts[i])
		enCounts[i] = make([]int, bibleChapterCounts[i])
	}

	done, errCount := 0, 0
	for r := range results {
		done++
		if r.err != nil {
			log.Printf("ERROR book=%d chap=%d zh=%v: %v", r.bookIdx+1, r.chapNum, r.isChinese, r.err)
			errCount++
			continue
		}
		if r.isChinese {
			zhCounts[r.bookIdx][r.chapNum-1] = r.count
		} else {
			enCounts[r.bookIdx][r.chapNum-1] = r.count
		}
		if done%200 == 0 {
			log.Printf("Progress: %d/%d requests done (%d errors)", done, totalRequests, errCount)
		}
	}
	log.Printf("Fetch complete: %d/%d requests done, %d errors.", done, totalRequests, errCount)
	if errCount > 0 {
		log.Printf("WARNING: %d fetch error(s) — affected chapters will have verse count 0 in JSON.", errCount)
	}

	// Write the two language-specific spec files.
	if err := writeZHSpec(zhPath, zhCounts); err != nil {
		log.Fatalf("Write ZH spec: %v", err)
	}
	if err := writeENSpec(enPath, enCounts); err != nil {
		log.Fatalf("Write EN spec: %v", err)
	}
	log.Printf("Done. Written:\n  %s\n  %s", zhPath, enPath)
}

// ── Spec writers ──────────────────────────────────────────────────────────────

// buildSummary computes aggregate verse statistics from the per-book counts.
// Testament classification is taken from defaultBookMeta.
func buildSummary(counts [66][]int) (summary summaryBlock, bookTotals [66]int) {
	summary.TotalBooks = 66
	summary.TotalChapters = 1189
	for i, b := range defaultBookMeta {
		bookTotal := 0
		for _, v := range counts[i] {
			bookTotal += v
		}
		bookTotals[i] = bookTotal
		summary.TotalVerses += bookTotal
		if b.Testament == "OT" {
			summary.OldTestament.Verses += bookTotal
		} else {
			summary.NewTestament.Verses += bookTotal
		}
	}
	summary.OldTestament.Books = 39
	summary.OldTestament.Chapters = 929
	summary.NewTestament.Books = 27
	summary.NewTestament.Chapters = 260
	return
}

// versesPerChapterMap converts a chapter-ordered verse count slice into the
// "01-[31]" key format used by the spec JSON (zero-padded chapter index + bracket count).
func versesPerChapterMap(chapCounts []int) map[string]int {
	vpc := make(map[string]int, len(chapCounts))
	for chapIdx, count := range chapCounts {
		key := fmt.Sprintf("%02d-[%d]", chapIdx+1, count)
		vpc[key] = count
	}
	return vpc
}

// writeZHSpec builds and writes bible_books_zh.json.
// Only Chinese name fields (name_zh, testament_zh) are included.
func writeZHSpec(path string, counts [66][]int) error {
	summary, bookTotals := buildSummary(counts)
	books := make([]zhBookJSON, 66)
	for i, b := range defaultBookMeta {
		books[i] = zhBookJSON{
			Number:           b.Number,
			TestamentZH:      b.TestamentZH,
			NameZH:           b.NameZH,
			TotalChapters:    bibleChapterCounts[i],
			TotalVerses:      bookTotals[i],
			VersesPerChapter: versesPerChapterMap(counts[i]),
		}
	}
	log.Printf("Writing ZH (和合本): total_verses=%d (OT=%d NT=%d)",
		summary.TotalVerses, summary.OldTestament.Verses, summary.NewTestament.Verses)
	out := zhOutputJSON{
		Title:   "聖經章節總覽（和合本）",
		Version: "和合本 (Chinese Union Version)",
		Summary: summary,
		Books:   books,
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}

// writeENSpec builds and writes bible_books_en.json.
// Only English name fields (name_en, testament) are included.
func writeENSpec(path string, counts [66][]int) error {
	summary, bookTotals := buildSummary(counts)
	books := make([]enBookJSON, 66)
	for i, b := range defaultBookMeta {
		books[i] = enBookJSON{
			Number:           b.Number,
			Testament:        b.Testament,
			NameEN:           b.NameEN,
			TotalChapters:    bibleChapterCounts[i],
			TotalVerses:      bookTotals[i],
			VersesPerChapter: versesPerChapterMap(counts[i]),
		}
	}
	log.Printf("Writing EN (BBE): total_verses=%d (OT=%d NT=%d)",
		summary.TotalVerses, summary.OldTestament.Verses, summary.NewTestament.Verses)
	out := enOutputJSON{
		Title:   "Bible Books Overview (BBE)",
		Version: "Basic English Version (BBE)",
		Summary: summary,
		Books:   books,
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}
