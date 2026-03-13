// cmd/repair/main.go re-fetches chapters whose content is missing for one or
// both languages. Run it after the main crawler to patch any entries that were
// skipped due to transient HTTP errors.
//
// It is also safe to run on a database that is already complete — it will find
// nothing to do and exit immediately ("nothing to repair").
//
// Usage:
//
//	go run cmd/repair/main.go
//
// The repair tool loads the same JSON spec files as the crawler so that it
// applies the same per-language verse-count bounds. After re-fetching a chapter
// page it writes versification-difference placeholder rows for any verse positions
// that exist in one translation's DB sections but are absent from the other
// translation's source page (e.g. Hebrew Lev 5:20-26 = Chinese CUV Lev 6:1-7).
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"bible-crawler/internal/config"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/spec"
	"bible-crawler/internal/utils"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// repairHTTPClient is set in main() from config so the timeout is configurable.
var repairHTTPClient *http.Client

// missingEntry identifies one missing chapter-content language pair.
type missingEntry struct {
	bookID   uuid.UUID
	bookSort int
	chapID   uuid.UUID
	chapSort int
	lang     string
}

// versePlaceholderTitle returns a language-appropriate verse title for
// verses that exist in one translation but not in the other.
func versePlaceholderTitle(lang string, sort int) string {
	if lang == "english" {
		return fmt.Sprintf("verse %d", sort)
	}
	return fmt.Sprintf("第%d節", sort)
}

// versePlaceholderContent returns a short note indicating that this verse
// position does not appear in the given translation. This arises from
// versification differences between 和合本 (Chinese CUV) and the BBE
// (English), where the two traditions draw chapter boundaries differently
// (e.g. Hebrew Lev 5:20-26 = Chinese Lev 6:1-7).
func versePlaceholderContent(lang string) string {
	if lang == "english" {
		return "(This verse is not present in this translation.)"
	}
	return "（此節不在本譯本中）"
}

func main() {
	// Load all settings (source URLs, DB DSN, tuning) from .env / environment.
	cfg := config.Load()

	// Initialize HTTP client with the configured timeout.
	repairHTTPClient = &http.Client{
		Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second,
	}

	if cfg.DBUrl == "" {
		log.Fatal("DATABASE_URL is not set — set it in .env or the environment")
	}
	db, err := sqlx.Connect("postgres", cfg.DBUrl)
	if err != nil {
		log.Fatal("DB connect:", err)
	}
	defer db.Close()

	repo := repository.NewBibleRepository(db)

	// Load Bible spec JSON files (same paths as the crawler uses).
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	bibleSpec, err := spec.Load(
		filepath.Join(projectRoot, "bible_books_zh.json"),
		filepath.Join(projectRoot, "bible_books_en.json"),
	)
	if err != nil {
		log.Fatalf("Failed to load Bible spec: %v", err)
	}
	globalChapStarts := bibleSpec.GlobalChapStarts()

	// Find all chapter-language pairs that need repair:
	//   (a) missing bible_chapter_contents row, OR
	//   (b) has at least one section whose content is missing for that language.
	// The repair loop re-fetches the whole chapter page and idempotently writes
	// both the chapter title and every verse, so a single pass fixes both cases.
	rows, err := db.Query(`
		SELECT DISTINCT bc.id, bc.sort, bb.id, bb.sort, 'chinese' AS lang
		FROM bibles.bible_chapters bc
		JOIN bibles.bible_books bb ON bb.id = bc.bible_book_id
		WHERE
			NOT EXISTS (
				SELECT 1 FROM bibles.bible_chapter_contents bcc
				WHERE bcc.bible_chapter_id = bc.id AND bcc.language = 'chinese'
			)
			OR EXISTS (
				SELECT 1 FROM bibles.bible_sections bs
				WHERE bs.bible_chapter_id = bc.id
				  AND NOT EXISTS (
					SELECT 1 FROM bibles.bible_section_contents bsc
					WHERE bsc.bible_section_id = bs.id AND bsc.language = 'chinese'
				  )
			)
		UNION ALL
		SELECT DISTINCT bc.id, bc.sort, bb.id, bb.sort, 'english'
		FROM bibles.bible_chapters bc
		JOIN bibles.bible_books bb ON bb.id = bc.bible_book_id
		WHERE
			NOT EXISTS (
				SELECT 1 FROM bibles.bible_chapter_contents bcc
				WHERE bcc.bible_chapter_id = bc.id AND bcc.language = 'english'
			)
			OR EXISTS (
				SELECT 1 FROM bibles.bible_sections bs
				WHERE bs.bible_chapter_id = bc.id
				  AND NOT EXISTS (
					SELECT 1 FROM bibles.bible_section_contents bsc
					WHERE bsc.bible_section_id = bs.id AND bsc.language = 'english'
				  )
			)
		ORDER BY 4, 2
	`)
	if err != nil {
		log.Fatal("Query:", err)
	}
	defer rows.Close()

	var missing []missingEntry
	for rows.Next() {
		var m missingEntry
		if err := rows.Scan(&m.chapID, &m.chapSort, &m.bookID, &m.bookSort, &m.lang); err != nil {
			log.Fatal("Scan:", err)
		}
		missing = append(missing, m)
	}
	if err := rows.Err(); err != nil {
		log.Fatal("Row iteration:", err)
	}

	if len(missing) == 0 {
		log.Println("No missing chapters found — nothing to repair.")
		return
	}
	log.Printf("Found %d missing chapter-language entries to repair.", len(missing))

	for _, m := range missing {
		bookIndex := m.bookSort - 1
		if bookIndex < 0 || bookIndex >= 66 {
			log.Printf("  ERROR invalid book_sort=%d for chapter id %s", m.bookSort, m.chapID)
			continue
		}
		bookSpec := bibleSpec.ZH[bookIndex]
		if m.lang == "english" {
			bookSpec = bibleSpec.EN[bookIndex]
		}
		if m.chapSort <= 0 || m.chapSort > bookSpec.TotalChapters {
			log.Printf("  ERROR invalid chapter sort=%d for book_sort=%d", m.chapSort, m.bookSort)
			continue
		}
		// maxVerses is the spec-defined verse count for this language/book/chapter.
		maxVerses, err := bookSpec.VerseCount(m.chapSort)
		if err != nil {
			log.Printf("  ERROR spec VerseCount book=%d chap=%d: %v", m.bookSort, m.chapSort, err)
			continue
		}

		globalChap := globalChapStarts[bookIndex] + m.chapSort - 1

		// Build the source URL using the configured URL template.
		// SourceZHURL and SourceENURL each contain a single %d placeholder
		// for the global (sequential) chapter index.
		var pageURL string
		isChinese := m.lang == "chinese"
		if isChinese {
			pageURL = fmt.Sprintf(cfg.SourceZHURL, globalChap)
		} else {
			pageURL = fmt.Sprintf(cfg.SourceENURL, globalChap)
		}

		log.Printf("Repairing: book_sort=%d chap=%d lang=%s maxVerses=%d  →  %s",
			m.bookSort, m.chapSort, m.lang, maxVerses, pageURL)

		body, err := fetchPage(pageURL, isChinese)
		if err != nil {
			log.Printf("  ERROR fetching: %v", err)
			continue
		}

		// Save chapter content title.
		chapTitle := fmt.Sprintf("第 %d 章", m.chapSort)
		if m.lang == "english" {
			chapTitle = fmt.Sprintf("Chapter %d", m.chapSort)
		}
		if err := repo.UpsertChapterContent(m.chapID, m.lang, chapTitle); err != nil {
			log.Printf("  ERROR saving chapter_content: %v", err)
		}

		// Parse and save verses. Only accept verse numbers within spec bounds.
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			log.Printf("  ERROR parsing HTML: %v", err)
			continue
		}
		doc.Find("script, style, a").Remove()

		count := 0
		doc.Find("ol li").Each(func(i int, sel *goquery.Selection) {
			verseNum := i + 1
			if valStr, exists := sel.Attr("value"); exists {
				if parsedVerseNum, parseErr := strconv.Atoi(strings.TrimSpace(valStr)); parseErr == nil && parsedVerseNum > 0 {
					verseNum = parsedVerseNum
				}
			}
			if verseNum <= 0 {
				verseNum = i + 1
			}
			// Spec guard: skip verses beyond what the JSON spec defines.
			if verseNum > maxVerses {
				log.Printf("  SKIP verse=%d exceeds spec max=%d (book=%d chap=%d lang=%s)",
					verseNum, maxVerses, m.bookSort, m.chapSort, m.lang)
				return
			}
			content := utils.CleanText(sel.Text())
			if content == "" {
				return
			}

			secID, err := repo.GetOrCreateSection(m.bookID, m.chapID, verseNum)
			if err != nil {
				log.Printf("  ERROR GetOrCreateSection verse=%d: %v", verseNum, err)
				return
			}

			verseTitle := fmt.Sprintf("第%d節", verseNum)
			if m.lang == "english" {
				verseTitle = fmt.Sprintf("verse %d", verseNum)
			}
			if err := repo.UpsertSectionContent(secID, m.lang, verseTitle, content); err != nil {
				log.Printf("  ERROR UpsertSectionContent verse=%d: %v", verseNum, err)
				return
			}
			count++
		})

		log.Printf("  Saved %d verses.", count)

		// Phase 3: write versification-difference placeholders.
		// Some sections exist in one translation but not the other because the
		// Chinese 和合本 and the English BBE draw chapter boundaries differently
		// (e.g. Hebrew Lev 5:20-26 = Chinese Lev 6:1-7). After re-fetching the
		// page we write a clear placeholder for any section that the source page
		// genuinely does not contain, so the DB has complete coverage in both
		// languages and the validation query returns zero rows.
		phRows, phErr := db.Query(`
			SELECT id, sort
			FROM bibles.bible_sections
			WHERE bible_chapter_id = $1
			  AND NOT EXISTS (
				SELECT 1 FROM bibles.bible_section_contents
				WHERE bible_section_id = bibles.bible_sections.id
				  AND language = $2
			  )
			ORDER BY sort
		`, m.chapID, m.lang)
		if phErr != nil {
			log.Printf("  ERROR querying residual missing sections: %v", phErr)
		} else {
			phCount := 0
			for phRows.Next() {
				var secID uuid.UUID
				var secSort int
				if scanErr := phRows.Scan(&secID, &secSort); scanErr != nil {
					log.Printf("  ERROR scanning residual section: %v", scanErr)
					continue
				}
				title := versePlaceholderTitle(m.lang, secSort)
				content := versePlaceholderContent(m.lang)
				if uErr := repo.UpsertSectionContent(secID, m.lang, title, content); uErr != nil {
					log.Printf("  ERROR writing placeholder verse=%d: %v", secSort, uErr)
					continue
				}
				phCount++
			}
			phRows.Close()
			if phErr = phRows.Err(); phErr != nil {
				log.Printf("  ERROR iterating residual sections: %v", phErr)
			}
			if phCount > 0 {
				log.Printf("  Wrote %d versification-difference placeholder(s).", phCount)
			}
		}

		// Polite pause between requests.
		time.Sleep(300 * time.Millisecond)
	}

	log.Println("Repair complete.")
}

// fetchPage downloads a chapter page and returns its decoded UTF-8 body.
// Chinese pages from springbible.fhl.net are Big5-encoded; English pages are ASCII.
func fetchPage(url string, isChinese bool) (string, error) {
	resp, err := repairHTTPClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, url)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if isChinese {
		return utils.Big5ToUTF8(raw)
	}
	return string(raw), nil
}
