// cmd/youversion-fetcher/main.go calls every available YouVersion Platform API
// (https://api.youversion.com/v1) endpoint and writes the combined responses to
// youversion-bible-api-result.json in the project root.
//
// The JSON output serves two purposes:
//  1. Documents the exact response shape of every endpoint (for future parsing
//     and integration work) without needing to re-hit the API.
//  2. Acts as a local cache so development and test workflows are not
//     rate-limited or blocked by the upstream service.
//
// Selected Bible versions
//
//	Chinese (licensed): Bible ID 46 — 新標點和合本, 神版 (CUNP-Shen, zh-Hant-TW)
//	Chinese (open):     Bible ID 36 — 当代圣经 (CCB, simplified, no extra license needed)
//	English:            Bible ID 111 — New International Version 2011 (NIV11)
//
// Passage text endpoint: GET /bibles/{id}/passages/{passageID}
// Returns actual verse text. Access depends on Bible version licensing:
//   - NIV11 (111) and CCB (36): freely accessible
//   - CUNP-Shen (46): requires YouVersion publisher license (returns 403 without it)
//
// Usage:
//
//	YOUVERSION_API_KEY=<your-key> go run cmd/youversion-fetcher/main.go
package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"bible-crawler/internal/config"
	"bible-crawler/internal/youversion"
)

const (
	// chineseBibleID is the YouVersion ID for 新標點和合本, 神版 (zh-Hant-TW).
	// Passage text for this version requires a YouVersion publisher license.
	chineseBibleID = 46
	// ccbBibleID is the YouVersion ID for 当代圣经 (CCB, simplified Chinese).
	// Passage text is accessible without extra licensing.
	ccbBibleID = 36
	// englishBibleID is the YouVersion ID for New International Version 2011.
	// Passage text is accessible without extra licensing.
	englishBibleID = 111

	// sampleBooks lists the USFM book IDs used for the detailed chapter/verse
	// sample section of the output. Three books give a representative sample
	// from the beginning of the Old Testament without making thousands of requests.
	// GEN = Genesis, EXO = Exodus, PSA = Psalms (longest OT book)
)

var sampleBooks = []string{"GEN", "EXO", "PSA"}

// outputResult is the top-level JSON structure written to the output file.
// Each field corresponds to a category of YouVersion API endpoints.
type outputResult struct {
	Meta           resultMeta                      `json:"meta"`
	Bibles         biblesSection                   `json:"bibles"`
	Books          booksSection                    `json:"books"`
	SampleChapters map[string]chapterSamplePair    `json:"sample_chapters"`
	SamplePassages passagesSection                 `json:"sample_passages"`
	VerseOfTheDays []youversion.VOTDEntry          `json:"verse_of_the_days"`
}

type resultMeta struct {
	FetchedAt     string            `json:"fetched_at"`
	APIBaseURL    string            `json:"api_base_url"`
	Note          string            `json:"note"`
	SelectedBibles selectedBiblesMeta `json:"selected_bibles"`
}

type selectedBiblesMeta struct {
	ChineseID       int    `json:"chinese_id"`
	ChineseName     string `json:"chinese_name"`
	ChineseOpenID   int    `json:"chinese_open_id"`
	ChineseOpenName string `json:"chinese_open_name"`
	EnglishID       int    `json:"english_id"`
	EnglishName     string `json:"english_name"`
}

type biblesSection struct {
	ChineseVersions []youversion.BibleVersion `json:"chinese_versions"`
	EnglishVersions []youversion.BibleVersion `json:"english_versions"`
	SelectedChinese *youversion.BibleVersion  `json:"selected_chinese"`
	SelectedEnglish *youversion.BibleVersion  `json:"selected_english"`
}

type booksSection struct {
	Chinese []youversion.BookData `json:"chinese"`
	English []youversion.BookData `json:"english"`
}

// chapterSamplePair holds one chapter's data in both languages, keyed by the
// book+chapter label (e.g. "GEN_1").
type chapterSamplePair struct {
	Chinese *youversion.ChapterData `json:"chinese"`
	English *youversion.ChapterData `json:"english"`
}

// passagesSection documents the GET /bibles/{id}/passages/{passageID} endpoint
// and includes concrete sample responses for accessible Bible versions.
type passagesSection struct {
	Note                 string                        `json:"note"`
	AccessibilityByBible []passageAccessEntry          `json:"accessibility_by_bible"`
	Samples              map[string]passageSampleEntry `json:"samples"`
}

// passageAccessEntry documents whether a specific Bible version allows
// unauthenticated passage text retrieval.
type passageAccessEntry struct {
	BibleID   int    `json:"bible_id"`
	BibleName string `json:"bible_name"`
	CanAccess bool   `json:"can_access"`
	Error     string `json:"error,omitempty"`
}

// passageSampleEntry holds representative passage text responses for one
// Bible version: a single verse, a verse range, a full chapter, and a VOTD.
type passageSampleEntry struct {
	BibleID     int                     `json:"bible_id"`
	BibleName   string                  `json:"bible_name"`
	SingleVerse *youversion.PassageData `json:"single_verse_GEN_1_1"`
	VerseRange  *youversion.PassageData `json:"verse_range_GEN_1_1_3"`
	FullChapter *youversion.PassageData `json:"full_chapter_GEN_1"`
	VOTDSample  *youversion.PassageData `json:"votd_sample_ISA_43_18_19"`
}

func main() {
	cfg := config.Load()

	if cfg.YouVersionAPIKey == "" {
		log.Fatal("YOUVERSION_API_KEY is not set — set it in .env or as an environment variable")
	}

	client := youversion.NewClient(cfg.YouVersionBaseURL, cfg.YouVersionAPIKey, cfg.HTTPTimeoutSec)

	result := outputResult{
		Meta: resultMeta{
			FetchedAt:  time.Now().UTC().Format(time.RFC3339),
			APIBaseURL: cfg.YouVersionBaseURL,
			Note: "The YouVersion Platform API (v1) provides Bible structure AND verse text content. " +
				"Verse text is available via GET /bibles/{id}/passages/{passageID}. " +
				"Access depends on translation licensing: NIV11 (ID 111) and CCB (ID 36) are freely accessible. " +
				"新標點和合本 (ID 46) requires a YouVersion publisher license agreement and returns HTTP 403 without one.",
			SelectedBibles: selectedBiblesMeta{
				ChineseID:       chineseBibleID,
				ChineseName:     "新標點和合本, 神版 (CUNP-Shen, zh-Hant-TW) — requires publisher license for passage text",
				ChineseOpenID:   ccbBibleID,
				ChineseOpenName: "当代圣经 (CCB, simplified Chinese) — passage text freely accessible",
				EnglishID:       englishBibleID,
				EnglishName:     "New International Version 2011 (NIV11) — passage text freely accessible",
			},
		},
		SampleChapters: make(map[string]chapterSamplePair),
		SamplePassages: passagesSection{
			Samples: make(map[string]passageSampleEntry),
		},
	}

	// ── 1. Bibles ──────────────────────────────────────────────────────────
	log.Println("Fetching Chinese Bible versions (zh-Hans)...")
	zhBibles, err := client.GetBibles("zh-Hans")
	if err != nil {
		log.Printf("Warning: could not fetch zh-Hans bibles: %v", err)
	} else {
		result.Bibles.ChineseVersions = zhBibles.Data
	}

	log.Println("Fetching English Bible versions...")
	enBibles, err := client.GetBibles("en")
	if err != nil {
		log.Printf("Warning: could not fetch en bibles: %v", err)
	} else {
		result.Bibles.EnglishVersions = enBibles.Data
	}

	log.Printf("Fetching selected Chinese Bible (ID %d)...", chineseBibleID)
	zhBible, err := client.GetBible(chineseBibleID)
	if err != nil {
		log.Printf("Warning: could not fetch Chinese Bible %d: %v", chineseBibleID, err)
	} else {
		result.Bibles.SelectedChinese = zhBible
	}

	log.Printf("Fetching selected English Bible (ID %d)...", englishBibleID)
	enBible, err := client.GetBible(englishBibleID)
	if err != nil {
		log.Printf("Warning: could not fetch English Bible %d: %v", englishBibleID, err)
	} else {
		result.Bibles.SelectedEnglish = enBible
	}

	// ── 2. Books ───────────────────────────────────────────────────────────
	log.Printf("Fetching all books for Chinese Bible (ID %d)...", chineseBibleID)
	zhBooks, err := client.GetBooks(chineseBibleID)
	if err != nil {
		log.Printf("Warning: could not fetch Chinese books: %v", err)
	} else {
		result.Books.Chinese = zhBooks.Data
	}

	log.Printf("Fetching all books for English Bible (ID %d)...", englishBibleID)
	enBooks, err := client.GetBooks(englishBibleID)
	if err != nil {
		log.Printf("Warning: could not fetch English books: %v", err)
	} else {
		result.Books.English = enBooks.Data
	}

	// ── 3. Sample chapters (first chapter of each sample book) ─────────────
	for _, bookID := range sampleBooks {
		key := bookID + "_1"
		log.Printf("Fetching sample chapter 1 for book %s...", bookID)

		zhChap, err := client.GetChapter(chineseBibleID, bookID, 1)
		if err != nil {
			log.Printf("Warning: could not fetch ZH chapter %s.1: %v", bookID, err)
		}

		enChap, err := client.GetChapter(englishBibleID, bookID, 1)
		if err != nil {
			log.Printf("Warning: could not fetch EN chapter %s.1: %v", bookID, err)
		}

		result.SampleChapters[key] = chapterSamplePair{
			Chinese: zhChap,
			English: enChap,
		}
	}

	// ── 4. Verse of the Day ────────────────────────────────────────────────
	log.Println("Fetching verse of the day list (all 366 entries)...")
	votd, err := client.GetVOTD()
	if err != nil {
		log.Printf("Warning: could not fetch VOTD: %v", err)
	} else {
		result.VerseOfTheDays = votd.Data
	}

	// ── 5. Passage text samples ────────────────────────────────────────────
	// Document the GET /bibles/{id}/passages/{passageID} endpoint.
	// Check accessibility for each Bible version and collect text samples.
	log.Println("Fetching passage text samples...")

	result.SamplePassages.Note = "GET /bibles/{id}/passages/{passageID} returns actual verse text. " +
		"Supported passageID formats: single verse (GEN.1.1), range (GEN.1.1-3), or chapter (GEN.1). " +
		"Access is governed by per-translation licensing."

	// Test passage access for each selected Bible version.
	type passageTestCase struct {
		id   int
		name string
	}
	bibleTests := []passageTestCase{
		{englishBibleID, "NIV11 (New International Version 2011)"},
		{ccbBibleID, "CCB (当代圣经, simplified Chinese)"},
		{chineseBibleID, "CUNP-Shen (新標點和合本, 神版, zh-Hant-TW)"},
	}

	for _, bt := range bibleTests {
		_, err := client.GetPassage(bt.id, "GEN.1.1")
		entry := passageAccessEntry{
			BibleID:   bt.id,
			BibleName: bt.name,
			CanAccess: err == nil,
		}
		if err != nil {
			entry.Error = err.Error()
		}
		result.SamplePassages.AccessibilityByBible = append(
			result.SamplePassages.AccessibilityByBible, entry)
	}

	// Collect text samples for accessible Bible versions.
	accessibleBibles := []passageTestCase{
		{englishBibleID, "NIV11"},
		{ccbBibleID, "CCB"},
	}
	samplePassageIDs := []struct {
		id    string
		field string
	}{
		{"GEN.1.1", "single_verse"},
		{"GEN.1.1-3", "range"},
		{"GEN.1", "chapter"},
		{"ISA.43.18-19", "votd"},
	}

	for _, ab := range accessibleBibles {
		log.Printf("Fetching passage samples for Bible %d (%s)...", ab.id, ab.name)
		entry := passageSampleEntry{BibleID: ab.id, BibleName: ab.name}

		for _, sp := range samplePassageIDs {
			p, err := client.GetPassage(ab.id, sp.id)
			if err != nil {
				log.Printf("Warning: passage %s for bible %d: %v", sp.id, ab.id, err)
				continue
			}
			switch sp.field {
			case "single_verse":
				entry.SingleVerse = p
			case "range":
				entry.VerseRange = p
			case "chapter":
				entry.FullChapter = p
			case "votd":
				entry.VOTDSample = p
			}
		}

		result.SamplePassages.Samples[ab.name] = entry
	}

	// ── Write output file ──────────────────────────────────────────────────
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	outputPath := filepath.Join(projectRoot, "youversion-bible-api-result.json")

	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal result to JSON: %v", err)
	}

	if err := os.WriteFile(outputPath, encoded, 0644); err != nil {
		log.Fatalf("Failed to write output file %s: %v", outputPath, err)
	}

	log.Printf("Done! Output written to %s (%d bytes)", outputPath, len(encoded))
}
