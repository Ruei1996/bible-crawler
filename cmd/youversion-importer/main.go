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
	cfg := config.Load()

	if cfg.YouVersionCheckpointFile == "" {
		log.Fatal("YOUVERSION_CHECKPOINT_FILE must be set to the JSONL file path")
	}

	db := database.Connect(cfg)
	defer db.Close()

	repo := repository.NewBibleRepository(db)

	if err := importJSONL(cfg.YouVersionCheckpointFile, repo); err != nil {
		log.Fatalf("Import failed: %v", err)
	}
}

// chapKey uniquely identifies a (chapter, language) pair for deduplication.
type chapKey struct {
	chapID uuid.UUID
	lang   string
}

// importJSONL streams the JSONL file at path and upserts each verse into the DB.
// Progress is logged every 1000 records.
func importJSONL(path string, repo *repository.BibleRepository) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	// Track which (chapID, lang) pairs have already had chapter content written
	// so we call UpsertChapterContent only once per chapter per language.
	writtenChapters := make(map[chapKey]bool)

	var total, written, skipped int
	for scanner.Scan() {
		total++
		var rec youversion.VerseRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			log.Printf("line %d: malformed JSON, skipping: %v", total, err)
			skipped++
			continue
		}
		if err := importVerse(repo, rec, writtenChapters); err != nil {
			log.Printf("line %d: import %s lang=%s: %v", total, rec.PassageID, rec.Lang, err)
			skipped++
			continue
		}
		written++
		if written%1000 == 0 {
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
// writtenChapters tracks which (chapID, lang) pairs have already had chapter
// content written in this import session, avoiding redundant DB calls.
func importVerse(repo *repository.BibleRepository, rec youversion.VerseRecord, writtenChapters map[chapKey]bool) error {
	bookID, err := repo.GetOrCreateBook(rec.BookSort)
	if err != nil {
		return fmt.Errorf("GetOrCreateBook(sort=%d): %w", rec.BookSort, err)
	}

	chapID, err := repo.GetOrCreateChapter(bookID, rec.ChapterSort)
	if err != nil {
		return fmt.Errorf("GetOrCreateChapter(sort=%d): %w", rec.ChapterSort, err)
	}

	// Write chapter content (title) once per chapter per language.
	ck := chapKey{chapID, rec.Lang}
	if !writtenChapters[ck] {
		chapTitle := fmt.Sprintf("Chapter %d", rec.ChapterSort)
		if rec.Lang == youversion.LangChinese {
			chapTitle = fmt.Sprintf("第 %d 章", rec.ChapterSort)
		}
		if err := repo.UpsertChapterContent(chapID, rec.Lang, chapTitle); err != nil {
			return fmt.Errorf("UpsertChapterContent(sort=%d lang=%s): %w", rec.ChapterSort, rec.Lang, err)
		}
		writtenChapters[ck] = true
	}

	secID, err := repo.GetOrCreateSection(bookID, chapID, rec.VerseSort)
	if err != nil {
		return fmt.Errorf("GetOrCreateSection(verse=%d): %w", rec.VerseSort, err)
	}

	verseTitle := fmt.Sprintf("verse %d", rec.VerseSort)
	if rec.Lang == youversion.LangChinese {
		verseTitle = fmt.Sprintf("第%d節", rec.VerseSort)
	}

	if err := repo.UpsertSectionContent(secID, rec.Lang, verseTitle, rec.Content); err != nil {
		return fmt.Errorf("UpsertSectionContent: %w", err)
	}
	return nil
}
