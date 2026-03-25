// cmd/youversion-importer/main.go reads a JSONL checkpoint file produced by
// cmd/youversion-crawler (parallel mode) and batch-writes all verse records to
// the PostgreSQL database using the same idempotent repository pattern used by
// the original HTML crawler.
//
// This is the second step of the two-step pipeline:
//
//  1. Run cmd/youversion-crawler with YOUVERSION_CHECKPOINT_FILE set.
//     This writes verse records to a JSONL file without touching the DB.
//
//  2. Run cmd/youversion-importer (this program).
//     It streams the JSONL and upserts each verse into the DB.
//
// Because all repository writes follow the SELECT→INSERT→SELECT pattern, it is
// safe to run the importer multiple times — duplicate records are harmless.
//
// Required environment variables:
//
//	DATABASE_URL              — PostgreSQL connection string
//	YOUVERSION_CHECKPOINT_FILE — path to the JSONL file to import
//
// Usage:
//
//	go run cmd/youversion-importer/main.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"

	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/youversion"
)

func main() {
	// 1. Load configuration from .env / environment variables.
	cfg := config.Load()

	// 2. YOUVERSION_CHECKPOINT_FILE must point to the JSONL file produced by
	//    cmd/youversion-crawler; without it there is nothing to import.
	if cfg.YouVersionCheckpointFile == "" {
		log.Fatal("YOUVERSION_CHECKPOINT_FILE must be set to the JSONL file path")
	}

	// 3. Connect to PostgreSQL using the same schema as the crawler.
	db := database.Connect(cfg)
	defer db.Close()

	// 4. Initialise the repository — idempotent upsert operations are safe to
	//    repeat, so the importer can be re-run without producing duplicate rows.
	repo := repository.NewBibleRepository(db)

	// 5. Stream the JSONL checkpoint file and upsert every verse into the DB.
	if err := importJSONL(cfg.YouVersionCheckpointFile, repo); err != nil {
		log.Fatalf("Import failed: %v", err)
	}
}

// chapKey uniquely identifies a (chapter, language) pair for deduplication.
type chapKey struct {
	chapID uuid.UUID
	lang   string
}

// chapterCacheKey uniquely identifies a (book, chapter) structural pair by
// sort indices for in-memory UUID caching. Using plain ints as the key is
// cheaper to hash and compare than uuid.UUID ([16]byte) + bookSort int.
type chapterCacheKey struct {
	bookSort int
	chapSort int
}

const (
	// maxBibleBooks is the canonical count of books in the Protestant Bible.
	// Used to pre-size the bookUUIDCache so it never rehashes.
	maxBibleBooks = 66

	// maxBibleChapters is the approximate number of unique (book, chapter) pairs
	// across all 66 canonical books (~1,189). Used to pre-size caches.
	maxBibleChapters = 1_190

	// languageCount is the number of languages imported per verse (EN + ZH).
	// Used to pre-size the writtenChapters dedup map.
	languageCount = 2

	// logProgressEvery controls how often import progress is logged (in verses).
	logProgressEvery = 1_000
)

// importJSONL streams the JSONL file at path and upserts each verse into the DB.
// Progress is logged every logProgressEvery records.
//
// Three in-memory caches are maintained for the duration of the import:
//   - bookUUIDCache: maps bookSort → uuid.UUID (66 unique entries max).
//     Eliminates 62,134 redundant GetOrCreateBook DB round-trips.
//   - chapUUIDCache: maps (bookSort, chapSort) → uuid.UUID (~1,189 entries).
//     Eliminates 60,880 redundant GetOrCreateChapter DB round-trips.
//   - writtenChapters: set of (chapID, lang) pairs already upserted this run.
//     Pre-sized to maxBibleChapters×languageCount to avoid all rehash cycles.
//
// Combined, the caches save ~123,000 SELECT round-trips ≈ 2 minutes of import
// time at a typical 1 ms/round-trip LAN latency.
func importJSONL(path string, repo *repository.BibleRepository) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Expand the scanner buffer to 1 MB: verse content lines can exceed the
	// default 64 KB token limit for verbose or multi-sentence translations.
	scanner.Buffer(make([]byte, youversion.ScannerInitialBuf), youversion.ScannerMaxBuf)

	// bookUUIDCache: 66 books, pre-sized to avoid any rehash.
	bookUUIDCache := make(map[int]uuid.UUID, maxBibleBooks)
	// chapUUIDCache: ~1,189 (book, chapter) pairs, pre-sized to avoid rehash.
	chapUUIDCache := make(map[chapterCacheKey]uuid.UUID, maxBibleChapters)
	// writtenChapters deduplicates UpsertChapterContent calls within this run.
	// The key is (chapID, lang) rather than chapID alone because each chapter
	// needs one localised title row per language. Pre-sized to cover all
	// (chapter, language) pairs without a single rehash.
	writtenChapters := make(map[chapKey]bool, maxBibleChapters*languageCount)

	var total, written, skipped int
	for scanner.Scan() {
		total++
		var rec youversion.VerseRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			log.Printf("line %d: malformed JSON, skipping: %v", total, err)
			skipped++
			continue
		}
		if err := importVerse(repo, rec, bookUUIDCache, chapUUIDCache, writtenChapters); err != nil {
			log.Printf("line %d: import %s lang=%s: %v", total, rec.PassageID, rec.Lang, err)
			skipped++
			continue
		}
		written++
		if written%logProgressEvery == 0 {
			log.Printf("Imported %d verses...", written)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}

	log.Printf("Import complete: total=%d written=%d skipped=%d", total, written, skipped)
	return nil
}

// importVerse persists a single VerseRecord to the database using the standard
// idempotent repository pattern: GetOrCreate* for structural rows, Upsert* for
// content rows. Phase 1 (book/chapter setup) should have been run first, but
// GetOrCreate* is safe to call even when the record already exists.
//
// bookUUIDCache and chapUUIDCache short-circuit GetOrCreateBook /
// GetOrCreateChapter DB calls after the first lookup per unique key. Since
// there are only 66 books and ~1,189 (book, chapter) pairs, these caches reach
// 100% hit-rate after the first ~1,189 verses, eliminating ~123,000 redundant
// SELECT round-trips across the full 62,200-verse corpus (≈ 2 minutes saved).
//
// writtenChapters tracks which (chapID, lang) pairs have already had chapter
// content written in this import session, avoiding redundant DB upserts.
func importVerse(
	repo *repository.BibleRepository,
	rec youversion.VerseRecord,
	bookUUIDCache map[int]uuid.UUID,
	chapUUIDCache map[chapterCacheKey]uuid.UUID,
	writtenChapters map[chapKey]bool,
) error {
	// Book UUID — cache hit after first encounter for this book sort index.
	bookID, ok := bookUUIDCache[rec.BookSort]
	if !ok {
		var err error
		bookID, err = repo.GetOrCreateBook(rec.BookSort)
		if err != nil {
			return fmt.Errorf("GetOrCreateBook(sort=%d): %w", rec.BookSort, err)
		}
		bookUUIDCache[rec.BookSort] = bookID
	}

	// Chapter UUID — cache hit after first encounter for this (book, chapter) pair.
	chapCK := chapterCacheKey{bookSort: rec.BookSort, chapSort: rec.ChapterSort}
	chapID, ok := chapUUIDCache[chapCK]
	if !ok {
		var err error
		chapID, err = repo.GetOrCreateChapter(bookID, rec.ChapterSort)
		if err != nil {
			return fmt.Errorf("GetOrCreateChapter(sort=%d): %w", rec.ChapterSort, err)
		}
		chapUUIDCache[chapCK] = chapID
	}

	// Write chapter content (title) once per chapter per language.
	ck := chapKey{chapID, rec.Lang}
	if !writtenChapters[ck] {
		// The YouVersion API does not return chapter titles; synthesise them via
		// FormatChapterTitle which owns the per-language template strings.
		if err := repo.UpsertChapterContent(chapID, rec.Lang, youversion.FormatChapterTitle(rec.Lang, rec.ChapterSort)); err != nil {
			return fmt.Errorf("UpsertChapterContent(sort=%d lang=%s): %w", rec.ChapterSort, rec.Lang, err)
		}
		writtenChapters[ck] = true
	}

	secID, err := repo.GetOrCreateSection(bookID, chapID, rec.VerseSort)
	if err != nil {
		return fmt.Errorf("GetOrCreateSection(verse=%d): %w", rec.VerseSort, err)
	}

	// sub_title (pericope heading) is intentionally left empty: the YouVersion
	// Platform API v1 does not expose section headings. rec.Content carries the
	// full verse text as the section body.
	if err := repo.UpsertSectionContent(secID, rec.Lang, youversion.FormatVerseTitle(rec.Lang, rec.VerseSort), rec.Content); err != nil {
		return fmt.Errorf("UpsertSectionContent: %w", err)
	}
	return nil
}
