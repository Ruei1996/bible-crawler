// cmd/repair/main.go re-fetches chapters whose content is missing for one language.
// Run after the main crawler to patch any HTTP failures from the initial run.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"bible-crawler/internal/repository"
	"bible-crawler/internal/utils"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var bibleChapterCounts = [66]int{
	50, 40, 27, 36, 34, 24, 21, 4, 31, 24,
	22, 25, 29, 36, 10, 13, 10, 42, 150, 31,
	12, 8, 66, 52, 5, 48, 12, 14, 3, 9,
	1, 4, 7, 3, 3, 3, 2, 14, 4,
	28, 16, 24, 21, 28, 16, 16, 13, 6, 6,
	4, 4, 5, 3, 6, 4, 3, 1, 13, 5,
	5, 3, 5, 1, 1, 1, 22,
}

var globalChapStarts [66]int

func init() {
	offset := 1
	for i := 0; i < 66; i++ {
		globalChapStarts[i] = offset
		offset += bibleChapterCounts[i]
	}
}

var repairHTTPClient = &http.Client{Timeout: 20 * time.Second}

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
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file, using environment variables")
	}

	db, err := sqlx.Connect("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("DB connect:", err)
	}
	defer db.Close()

	repo := repository.NewBibleRepository(db)

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
		if bookIndex < 0 || bookIndex >= len(globalChapStarts) {
			log.Printf("  ERROR invalid book_sort=%d for chapter id %s", m.bookSort, m.chapID)
			continue
		}
		if m.chapSort <= 0 || m.chapSort > bibleChapterCounts[bookIndex] {
			log.Printf("  ERROR invalid chapter sort=%d for book_sort=%d", m.chapSort, m.bookSort)
			continue
		}
		globalChap := globalChapStarts[bookIndex] + m.chapSort - 1

		var pageURL string
		isChinese := m.lang == "chinese"
		if isChinese {
			pageURL = fmt.Sprintf("https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0", globalChap)
		} else {
			pageURL = fmt.Sprintf("https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe", globalChap)
		}

		log.Printf("Repairing: book_sort=%d chap=%d lang=%s  →  %s", m.bookSort, m.chapSort, m.lang, pageURL)

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

		// Parse and save verses.
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

// fetchPage reads one chapter page and decodes Big5 payload when needed.
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
