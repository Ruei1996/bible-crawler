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
	//    every GetOrCreate* call finds its key already cached and skips the DB
	//    round-trip, eliminating ~32,256 redundant SELECT statements.
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
// content into the database.  lang must match the language code used in the
// repository (biblecom.LangChinese or biblecom.LangEnglish).
//
// The three UUID caches are passed in from the caller (main) and shared across
// both the ZH and EN imports. During the first (ZH) import they accumulate all
// structural UUIDs (66 books, ~1,190 chapters, ~31,000 sections). During the
// second (EN) import every GetOrCreate* call finds its key already in the cache
// and skips the SELECT round-trip, eliminating ~32,256 redundant DB queries.
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
	// always happen regardless of cache hits (UpsertSectionContentFull is never
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

// importBook upserts a single BookOutput and all its chapters and verses.
// The book UUID is fetched from bookCache on the first encounter and reused
// for all subsequent chapters in the same book.
func importBook(
	repo *repository.BibleRepository,
	book biblecom.BookOutput,
	lang string,
	bookCache map[int]uuid.UUID,
	chapCache map[chapterCacheKey]uuid.UUID,
	secCache map[sectionCacheKey]uuid.UUID,
	stats *importStats,
) error {
	// Resolve or create the book structural row.
	bookID, ok := bookCache[book.BookSort]
	if !ok {
		var err error
		bookID, err = repo.GetOrCreateBook(book.BookSort)
		if err != nil {
			return fmt.Errorf("GetOrCreateBook(sort=%d): %w", book.BookSort, err)
		}
		bookCache[book.BookSort] = bookID
	}

	// Upsert the localised book title. stats.books is incremented only after
	// the content write succeeds so that all three counters (books, chapters,
	// verses) uniformly mean "structural row resolved AND content row written".
	// A book whose content write fails is counted in stats.skipped only, never
	// in stats.books — consistent with how importVerse counts stats.verses.
	if err := repo.UpsertBookContent(bookID, lang, book.BookName); err != nil {
		return fmt.Errorf("UpsertBookContent(sort=%d lang=%s): %w", book.BookSort, lang, err)
	}
	// stats.books is incremented unconditionally (cache hit or miss) so that the
	// summary reflects "books processed" — during the EN import all books are
	// already in bookCache from ZH, so the INSERT is skipped but the book was
	// still fully handled.
	stats.books++

	for _, chap := range book.Chapters {
		if err := importChapter(repo, book.BookSort, bookID, chap, lang, chapCache, secCache, stats); err != nil {
			log.Printf("[biblecom-importer] WARN book_sort=%d chap=%d (%s): %v",
				book.BookSort, chap.ChapterSort, lang, err)
			stats.skipped++
		}
	}
	return nil
}

// importChapter upserts one ChapterOutput and all its verses.
func importChapter(
	repo *repository.BibleRepository,
	bookSort int,
	bookID uuid.UUID,
	chap biblecom.ChapterOutput,
	lang string,
	chapCache map[chapterCacheKey]uuid.UUID,
	secCache map[sectionCacheKey]uuid.UUID,
	stats *importStats,
) error {
	ck := chapterCacheKey{bookSort: bookSort, chapSort: chap.ChapterSort}
	chapID, ok := chapCache[ck]
	if !ok {
		var err error
		chapID, err = repo.GetOrCreateChapter(bookID, chap.ChapterSort)
		if err != nil {
			return fmt.Errorf("GetOrCreateChapter(sort=%d): %w", chap.ChapterSort, err)
		}
		chapCache[ck] = chapID
	}

	// Synthesise a localised chapter title using the same template as the
	// YouVersion importer so that both importers produce identical title rows.
	// stats.chapters is incremented only after the content write succeeds,
	// mirroring the stats.books and stats.verses semantics.
	chapterTitle := youversion.FormatChapterTitle(lang, chap.ChapterSort)
	if err := repo.UpsertChapterContent(chapID, lang, chapterTitle); err != nil {
		return fmt.Errorf("UpsertChapterContent(sort=%d lang=%s): %w", chap.ChapterSort, lang, err)
	}
	// Incremented unconditionally (cache hit or miss) — see importBook comment.
	stats.chapters++

	for _, verse := range chap.Verses {
		if err := importVerse(repo, bookSort, bookID, chap.ChapterSort, chapID, verse, lang, secCache, stats); err != nil {
			log.Printf("[biblecom-importer] WARN book=%d chap=%d verse=%d (%s): %v",
				bookSort, chap.ChapterSort, verse.VerseSort, lang, err)
			stats.skipped++
		}
	}
	return nil
}

// importVerse upserts a single VerseOutput into the database.
//
// The Note field is intentionally ignored during import:
//   - "merged"          — secondary verse in a merged group; "併於上節。" content stored as-is.
//   - "ref:BOOK.CHAP.V" — bracket-labeled verse resolved via cross-reference; actual verse
//                         content from the referenced verse is stored (resolved before import).
//   - "omitted"         — bracket-labeled verse with no resolvable cross-reference; note body
//                         text (e.g. "Some manuscripts include verse 44.") is stored as content.
//
// The CrossRef field is never written to the database; it exists only in the JSON
// output for audit and traceability purposes.
//
// SubTitle is written to bible_section_contents.sub_title via
// UpsertSectionContentFull; it is left empty for verses that do not begin a
// new pericope section.
func importVerse(
	repo *repository.BibleRepository,
	bookSort int,
	bookID uuid.UUID,
	chapSort int,
	chapID uuid.UUID,
	verse biblecom.VerseOutput,
	lang string,
	secCache map[sectionCacheKey]uuid.UUID,
	stats *importStats,
) error {
	// secCache gates only the structural GetOrCreateSection round-trip (the
	// bible_section row). It does NOT skip the UpsertSectionContentFull call
	// below, because content rows are language-specific: the same section UUID
	// must be written once for "chinese" and once for "english". The cache
	// eliminates the redundant SELECT when both language files share a section.
	sk := sectionCacheKey{bookSort: bookSort, chapSort: chapSort, verseSort: verse.VerseSort}
	secID, ok := secCache[sk]
	if !ok {
		var err error
		secID, err = repo.GetOrCreateSection(bookID, chapID, verse.VerseSort)
		if err != nil {
			return fmt.Errorf("GetOrCreateSection(verse=%d): %w", verse.VerseSort, err)
		}
		secCache[sk] = secID
	}

	// verseTitle mirrors the format stored by the springbible HTML crawler.
	verseTitle := youversion.FormatVerseTitle(lang, verse.VerseSort)

	// UpsertSectionContentFull persists both the verse text and the optional
	// sub_title (pericope heading). The Note field is deliberately excluded
	// from the DB — it is a JSON-only audit annotation.
	if err := repo.UpsertSectionContentFull(secID, lang, verseTitle, verse.Content, verse.SubTitle); err != nil {
		return fmt.Errorf("UpsertSectionContentFull(verse=%d lang=%s): %w", verse.VerseSort, lang, err)
	}

	// Count every content-write attempt — including idempotent no-ops on
	// re-runs — as a measure of total throughput, not net new rows inserted.
	stats.verses++
	if stats.verses%logProgressEvery == 0 {
		log.Printf("[biblecom-importer] %s: imported %d verses...", lang, stats.verses)
	}
	return nil
}
