//go:build integration

package scraper_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"bible-crawler/internal/config"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/scraper"
	"bible-crawler/internal/spec"
	"bible-crawler/internal/testhelper"
)

// makeIntegrationSpec builds a minimal BibleSpec where book 0 has 1 chapter
// with 2 verses and all other books have 0 chapters.
func makeIntegrationSpec() *spec.BibleSpec {
	zhBooks := make([]*spec.BookSpec, 66)
	enBooks := make([]*spec.BookSpec, 66)
	for i := 0; i < 66; i++ {
		zhBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
		enBooks[i] = &spec.BookSpec{Number: i + 1, TotalChapters: 0}
	}
	vpc := map[string]int{"01-[2]": 2}
	zhBooks[0] = &spec.BookSpec{
		Number:           1,
		NameZH:           "創世記",
		TotalChapters:    1,
		VersesPerChapter: vpc,
	}
	enBooks[0] = &spec.BookSpec{
		Number:           1,
		NameEN:           "Genesis",
		TotalChapters:    1,
		VersesPerChapter: vpc,
	}
	return &spec.BibleSpec{ZH: zhBooks, EN: enBooks}
}

func TestScraper_Run_Integration(t *testing.T) {
	// ── fake HTTP server ──────────────────────────────────────
	verseHTML := `<html><body><ol>
<li value="1">In the beginning God created the heavens and the earth.</li>
<li value="2">The earth was without form and void.</li>
</ol></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, verseHTML)
	}))
	t.Cleanup(srv.Close)

	// ── real PostgreSQL via testcontainers ────────────────────
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	repo := repository.NewBibleRepository(db)
	bspec := makeIntegrationSpec()

	cfg := &config.Config{
		SourceDomain:         "127.0.0.1",
		SourceZHURL:          srv.URL + "/%d",
		SourceENURL:          srv.URL + "/%d",
		CrawlerParallelism:   2,
		CrawlerDelayMS:       0,
		CrawlerRandomDelayMS: 0,
		HTTPTimeoutSec:       10,
	}

	s := scraper.NewBibleScraper(repo, bspec, cfg)
	err := s.Run()
	require.NoError(t, err)

	// Verify: at least one bible_book row exists.
	var bookCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM bibles.bible_books`).Scan(&bookCount))
	require.Equal(t, 66, bookCount, "expected 66 book rows")

	// Verify: book 1 content exists for both languages.
	var zhTitle, enTitle string
	require.NoError(t, db.QueryRow(
		`SELECT title FROM bibles.bible_book_contents WHERE language = 'chinese'
		 ORDER BY title LIMIT 1`).Scan(&zhTitle))
	require.NotEmpty(t, zhTitle)

	require.NoError(t, db.QueryRow(
		`SELECT title FROM bibles.bible_book_contents WHERE language = 'english'
		 ORDER BY title LIMIT 1`).Scan(&enTitle))
	require.NotEmpty(t, enTitle)

	// Verify: 1 chapter created for book 0.
	var chapCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM bibles.bible_chapters`).Scan(&chapCount))
	require.Equal(t, 1, chapCount)

	// Verify: 2 sections created (1 per verse).
	var secCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM bibles.bible_sections`).Scan(&secCount))
	require.Equal(t, 2, secCount)

	// Verify: section contents (2 sections × 2 languages = 4 rows).
	var secContentCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM bibles.bible_section_contents`).Scan(&secContentCount))
	require.Equal(t, 4, secContentCount)
}
