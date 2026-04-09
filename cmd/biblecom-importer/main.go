// cmd/biblecom-importer reads the two JSON output files produced by
// cmd/biblecom-crawler (one for Chinese CUNP-上帝, one for English NIV) and
// batch-imports every book, chapter, and verse into PostgreSQL using the same
// idempotent repository pattern as the other crawlers.
//
// This is Step 2 of the bible.com two-step pipeline:
//
//  1. Run cmd/biblecom-crawler → writes youversion-bible_books_zh.json and
//     youversion-bible_books_en.json (the JSON output files).
//
//  2. Run cmd/biblecom-importer (this program) → reads both JSON files and
//     upserts all structural and content rows into PostgreSQL.
//
// Because every repository call follows SELECT→INSERT→SELECT (idempotent),
// re-running the importer is safe and will not create duplicate rows.
//
// # Performance design
//
// The importer uses bulk (UNNEST-based) repository methods that collapse
// O(N_verses) individual round-trips into O(N_books) round-trips. Over a
// high-latency VPN (≥10 ms per round-trip) this reduces a multi-hour run to
// under 60 seconds:
//
//   - BulkGetOrCreateChapters  — 2 round-trips per book   (~66 books = 132)
//   - BulkGetOrCreateSections  — 2 round-trips per book   (~66 books = 132)
//   - BulkUpsertChapterContents — 2-3 round-trips per book (~66 books = ~200)
//   - BulkUpsertSectionContents — 2-3 round-trips per book (~66 books = ~200)
//
// Total: ~700 round-trips for the full two-language import, vs. ~93,000 with
// the previous per-verse approach.
//
// Required environment variables:
//
//	DATABASE_URL         — PostgreSQL connection string
//	BIBLECOM_OUTPUT_ZH   — path to the Chinese JSON output file
//	BIBLECOM_OUTPUT_EN   — path to the English JSON output file
//
// Optional (with defaults matching cmd/biblecom-crawler defaults):
//
//	BIBLECOM_OUTPUT_ZH defaults to "youversion-bible_books_zh.json"
//	BIBLECOM_OUTPUT_EN defaults to "youversion-bible_books_en.json"
//
// Usage:
//
//	go run cmd/biblecom-importer/main.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"bible-crawler/internal/biblecom"
	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/youversion"
)

func main() {
	// 1. Load configuration from .env / environment variables.
	cfg := config.Load()

	// 2. Resolve the JSON file paths — fall back to the crawler's default names
	//    when the env vars are not explicitly set.
	zhPath := cfg.BibleComOutputZH
	enPath := cfg.BibleComOutputEN
	if zhPath == "" {
		zhPath = "youversion-bible_books_zh.json"
	}
	if enPath == "" {
		enPath = "youversion-bible_books_en.json"
	}

	// 3. Connect to PostgreSQL; defer ensures the pool is closed on exit.
	db := database.Connect(cfg)
	defer db.Close()

	repo := repository.NewBibleRepository(db)

	// 4. Import both language files.  The Chinese file is imported first so that
	//    the structural rows (bible_books, bible_chapters, bible_sections) are
	//    always created in a deterministic order.
	//
	//    The three UUID caches are allocated once here and shared across both
	//    imports. During the ZH import they are populated with every structural
	//    UUID (66 books, ~1,190 chapters, ~31,000 sections). During the EN import
	//    every BulkGetOrCreate* call finds its key already cached and skips the
	//    DB round-trip entirely, reducing EN structural overhead to zero.
	sharedBookCache := make(map[int]uuid.UUID, maxBibleBooks)
	sharedChapCache := make(map[chapterCacheKey]uuid.UUID, maxBibleChapters)
	sharedSecCache := make(map[sectionCacheKey]uuid.UUID, maxBibleVerses)

	log.Printf("[biblecom-importer] importing Chinese file: %s", zhPath)
	zhStats, err := importOutputFile(zhPath, biblecom.LangChinese, repo, sharedBookCache, sharedChapCache, sharedSecCache)
	if err != nil {
		log.Fatalf("[biblecom-importer] Chinese import failed: %v", err)
	}
	log.Printf("[biblecom-importer] Chinese import complete: %s", zhStats)

	log.Printf("[biblecom-importer] importing English file: %s", enPath)
	enStats, err := importOutputFile(enPath, biblecom.LangEnglish, repo, sharedBookCache, sharedChapCache, sharedSecCache)
	if err != nil {
		log.Fatalf("[biblecom-importer] English import failed: %v", err)
	}
	log.Printf("[biblecom-importer] English import complete: %s", enStats)
}

// importStats summarises the outcome of one file import.
type importStats struct {
	books    int
	chapters int
	verses   int
	skipped  int
}

func (s importStats) String() string {
	return fmt.Sprintf("books=%d chapters=%d verses=%d skipped=%d",
		s.books, s.chapters, s.verses, s.skipped)
}

// chapterCacheKey uniquely identifies a (book, chapter) pair for in-memory
// UUID caching. Using integer keys is cheaper to hash than uuid.UUID values.
type chapterCacheKey struct {
	bookSort int
	chapSort int
}

// sectionCacheKey uniquely identifies a (book, chapter, verse) triple.
type sectionCacheKey struct {
	bookSort  int
	chapSort  int
	verseSort int
}

const (
	// maxBibleBooks is the canonical count of books in the Protestant Bible.
	maxBibleBooks = 66

	// maxBibleChapters is the approximate number of (book, chapter) pairs in
	// the Protestant canon. Used to pre-size the chapter UUID cache.
	maxBibleChapters = 1_190

	// maxBibleVerses is the approximate number of (book, chapter, verse)
	// triples. Used to pre-size the section UUID cache.
	maxBibleVerses = 31_000

	// logProgressEvery controls the log-line frequency during verse import.
	logProgressEvery = 500
)

// importOutputFile reads a biblecom.OutputFile JSON from path and upserts all
// content into the database using bulk operations.
//
// The three UUID caches are passed in from the caller (main) and shared across
// both the ZH and EN imports. During the first (ZH) import they accumulate all
// structural UUIDs (66 books, ~1,190 chapters, ~31,000 sections). During the
// second (EN) import every BulkGetOrCreate* call finds its key already in the
// cache and skips the structural DB round-trips entirely.
func importOutputFile(
	path, lang string,
	repo *repository.BibleRepository,
	bookCache map[int]uuid.UUID,
	chapCache map[chapterCacheKey]uuid.UUID,
	secCache map[sectionCacheKey]uuid.UUID,
) (importStats, error) {
	// Sanitise and validate the input path before opening (CWE-22: path traversal).
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return importStats{}, fmt.Errorf("resolve path %q: %w", path, err)
	}

	// Resolve any symlinks in the cleaned path so the confinement check below
	// operates on the real filesystem target, not on the lexical symlink name.
	// Without this step, a symlink inside the CWD that points outside
	// (e.g. ./input.json → /etc/passwd) would pass the filepath.Rel check
	// but open the sensitive target — a classic symlink-following vulnerability
	// (CWE-61). EvalSymlinks fails if the file does not exist yet, which is
	// acceptable here since we are about to open it for reading.
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return importStats{}, fmt.Errorf("resolve symlinks for %q: %w", cleanPath, err)
	}

	// Confine reads to the current working directory subtree so that env vars
	// like BIBLECOM_OUTPUT_ZH=/etc/passwd or ../../secrets cannot be exploited.
	allowedRoot, err := os.Getwd()
	if err != nil {
		return importStats{}, fmt.Errorf("getwd: %w", err)
	}
	// Also resolve the CWD's symlinks so the comparison is apples-to-apples.
	// On macOS, /var is a symlink to /private/var, so without this step a file
	// at /private/var/... would be incorrectly rejected as outside /var/....
	resolvedRoot, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		return importStats{}, fmt.Errorf("resolve symlinks for cwd %q: %w", allowedRoot, err)
	}
	rel, relErr := filepath.Rel(resolvedRoot, resolvedPath)
	// filepath.IsLocal returns false for any relative path that escapes the root
	// (starts with ".." or is absolute). It is the correct API for this check —
	// strings.HasPrefix(rel, "..") would incorrectly reject files whose names
	// begin with ".." (e.g. "..data.json") that are legitimately inside the CWD.
	if relErr != nil || !filepath.IsLocal(rel) {
		return importStats{}, fmt.Errorf(
			"path %q (resolved: %q) is outside allowed directory %q (possible path traversal)",
			path, resolvedPath, resolvedRoot,
		)
	}

	// Open the fully-resolved path — can never escape resolvedRoot.
	f, err := os.Open(resolvedPath)
	if err != nil {
		return importStats{}, fmt.Errorf("open %q: %w", resolvedPath, err)
	}
	defer f.Close()

	// Limit JSON reads to 64 MiB — 10× the actual upper bound of any real
	// output file (~5–6 MiB), providing generous headroom without allowing
	// a corrupted or maliciously replaced file to allocate 256 MiB on the heap
	// before the decoder terminates (CWE-400).
	const maxJSONBytes = 64 << 20 // 64 MiB
	var out biblecom.OutputFile
	if err := json.NewDecoder(io.LimitReader(f, maxJSONBytes)).Decode(&out); err != nil {
		return importStats{}, fmt.Errorf("decode JSON %q: %w", resolvedPath, err)
	}

	var stats importStats

	for _, book := range out.Books {
		if err := importBook(repo, book, lang, bookCache, chapCache, secCache, &stats); err != nil {
			// Log the error and continue so one bad book does not abort the run.
			log.Printf("[biblecom-importer] WARN book_sort=%d (%s): %v",
				book.BookSort, book.BookName, err)
			stats.skipped++
		}
	}

	// Guard against silent total failure: if no verse content was written for a
	// non-empty file, something is systematically wrong (wrong file, DB error on
	// every verse). Use stats.verses as the indicator because verse content writes
	// always happen regardless of cache hits (BulkUpsertSectionContents is never
	// skipped). stats.books/chapters may be 0 during the EN import when all
	// structural rows hit the shared cache — that is expected and correct.
	if stats.verses == 0 && len(out.Books) > 0 {
		return stats, fmt.Errorf(
			"import aborted: 0 verses written for %d books (%d book/chapter/verse errors); check WARN lines above",
			len(out.Books), stats.skipped,
		)
	}
	return stats, nil
}

// importBook upserts a single BookOutput and all its chapters and verses using
// bulk operations. The algorithm per book is:
//
//  1. GetOrCreateBook  (single row — cached on EN pass)
//  2. BulkGetOrCreateChapters (one INSERT + one SELECT for the whole book)
//  3. BulkGetOrCreateSections (one INSERT + one SELECT for all verses in the book)
//  4. UpsertBookContent        (single content row — per-language write)
//  5. BulkUpsertChapterContents (one SELECT + one INSERT for all chapter titles)
//  6. BulkUpsertSectionContents (one SELECT + one INSERT for all verse content)
//
// Total DB round-trips per book: ~8 (vs. 2×N_verses with the old per-verse
// approach). For Genesis (~1,500 verses) this is a 375× reduction.
func importBook(
	repo *repository.BibleRepository,
	book biblecom.BookOutput,
	lang string,
	bookCache map[int]uuid.UUID,
	chapCache map[chapterCacheKey]uuid.UUID,
	secCache map[sectionCacheKey]uuid.UUID,
	stats *importStats,
) error {
	// ── Step 1: Resolve book structural row (always cached after ZH pass) ──
	bookID, ok := bookCache[book.BookSort]
	if !ok {
		var err error
		bookID, err = repo.GetOrCreateBook(book.BookSort)
		if err != nil {
			return fmt.Errorf("GetOrCreateBook(sort=%d): %w", book.BookSort, err)
		}
		bookCache[book.BookSort] = bookID
	}

	// ── Step 2: Bulk-resolve chapter structural rows ────────────────────────
	// Single pass: cache hits go directly into chapSortToID; misses are
	// collected for BulkGetOrCreateChapters (eliminates a redundant second
	// iteration over book.Chapters compared to the two-pass approach).
	missingChapSorts := make([]int, 0, len(book.Chapters))
	chapSortToID := make(map[int]uuid.UUID, len(book.Chapters))
	for _, chap := range book.Chapters {
		ck := chapterCacheKey{bookSort: book.BookSort, chapSort: chap.ChapterSort}
		if id, ok := chapCache[ck]; ok {
			chapSortToID[chap.ChapterSort] = id
		} else {
			missingChapSorts = append(missingChapSorts, chap.ChapterSort)
		}
	}
	if len(missingChapSorts) > 0 {
		fetched, err := repo.BulkGetOrCreateChapters(bookID, missingChapSorts)
		if err != nil {
			return fmt.Errorf("BulkGetOrCreateChapters(book=%d): %w", book.BookSort, err)
		}
		for sort, id := range fetched {
			chapCache[chapterCacheKey{bookSort: book.BookSort, chapSort: sort}] = id
			chapSortToID[sort] = id
		}
	}

	// ── Step 3: Bulk-resolve section structural rows ────────────────────────
	missingVerseKeys := make([]repository.VerseKey, 0, len(book.Chapters)*30)
	for _, chap := range book.Chapters {
		for _, verse := range chap.Verses {
			sk := sectionCacheKey{bookSort: book.BookSort, chapSort: chap.ChapterSort, verseSort: verse.VerseSort}
			if _, ok := secCache[sk]; !ok {
				missingVerseKeys = append(missingVerseKeys, repository.VerseKey{
					ChapSort:  chap.ChapterSort,
					VerseSort: verse.VerseSort,
				})
			}
		}
	}
	if len(missingVerseKeys) > 0 {
		verseKeyToID, err := repo.BulkGetOrCreateSections(bookID, chapSortToID, missingVerseKeys)
		if err != nil {
			return fmt.Errorf("BulkGetOrCreateSections(book=%d): %w", book.BookSort, err)
		}
		for vk, id := range verseKeyToID {
			sk := sectionCacheKey{bookSort: book.BookSort, chapSort: vk.ChapSort, verseSort: vk.VerseSort}
			secCache[sk] = id
		}
	}

	// ── Step 4: Upsert localised book title ─────────────────────────────────
	if err := repo.UpsertBookContent(bookID, lang, book.BookName); err != nil {
		return fmt.Errorf("UpsertBookContent(sort=%d lang=%s): %w", book.BookSort, lang, err)
	}
	stats.books++

	// ── Steps 5 & 6: Bulk-upsert chapter and section content ────────────────
	// Collect all content records for this book in a single pass so that both
	// bulk calls can be issued in one batch per book.
	chapContentRecs := make([]repository.ChapterContentRecord, 0, len(book.Chapters))
	secContentRecs := make([]repository.SectionContentRecord, 0, len(book.Chapters)*30)

	for _, chap := range book.Chapters {
		chapID := chapSortToID[chap.ChapterSort]
		chapTitle := youversion.FormatChapterTitle(lang, chap.ChapterSort)
		chapContentRecs = append(chapContentRecs, repository.ChapterContentRecord{
			ChapterID: chapID,
			Lang:      lang,
			Title:     chapTitle,
		})

		for _, verse := range chap.Verses {
			sk := sectionCacheKey{bookSort: book.BookSort, chapSort: chap.ChapterSort, verseSort: verse.VerseSort}
			secID := secCache[sk]
			verseTitle := youversion.FormatVerseTitle(lang, verse.VerseSort)
			secContentRecs = append(secContentRecs, repository.SectionContentRecord{
				SectionID: secID,
				Lang:      lang,
				Title:     verseTitle,
				Content:   verse.Content,
				SubTitle:  verse.SubTitle,
			})
		}
	}

	if err := repo.BulkUpsertChapterContents(chapContentRecs); err != nil {
		return fmt.Errorf("BulkUpsertChapterContents(book=%d lang=%s): %w", book.BookSort, lang, err)
	}
	stats.chapters += len(chapContentRecs)

	if err := repo.BulkUpsertSectionContents(secContentRecs); err != nil {
		return fmt.Errorf("BulkUpsertSectionContents(book=%d lang=%s): %w", book.BookSort, lang, err)
	}
	prevVerses := stats.verses
	stats.verses += len(secContentRecs)
	// Threshold-crossing check: fires once per logProgressEvery boundary even
	// when a single book adds hundreds of verses at once (modulo would miss it).
	if stats.verses/logProgressEvery > prevVerses/logProgressEvery {
		log.Printf("[biblecom-importer] %s: imported %d verses...", lang, stats.verses)
	}

	return nil
}
