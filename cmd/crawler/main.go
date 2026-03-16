// cmd/crawler/main.go is the main entrypoint for the Bible crawler.
//
// It performs two phases:
//   - Phase 1: write all book rows and their Chinese/English titles from the
//     JSON spec files (no HTTP requests).
//   - Phase 2: concurrently fetch every chapter page in both 和合本 (CUV) and
//     BBE, then persist each verse using the per-language spec verse bounds.
//
// Prerequisites: run cmd/spec-builder first to generate the JSON spec files.
//
// Usage:
//
//	go run cmd/crawler/main.go
package main

import (
	"log"
	"path/filepath"
	"runtime"

	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/scraper"
	"bible-crawler/internal/spec"
)

// main wires configuration, database, repository, and scraper components.
// It is the standard full-crawl entrypoint.
func main() {
	// 1. Load Config
	cfg := config.Load()

	// 2. Connect to Database
	db := database.Connect(cfg)
	defer db.Close()

	// 3. Initialize Repository
	repo := repository.NewBibleRepository(db)

	// 4. Load Bible spec JSON files.
	// Paths are resolved relative to the project root so the binary works
	// regardless of the working directory it is invoked from.
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	zhPath := filepath.Join(projectRoot, "bible_books_zh.json")
	enPath := filepath.Join(projectRoot, "bible_books_en.json")

	bibleSpec, err := spec.Load(zhPath, enPath)
	if err != nil {
		log.Fatalf("Failed to load Bible spec: %v", err)
	}

	// 5. Initialize Scraper — pass spec and config so it uses the correct
	//    source URLs and tuning parameters from .env.
	sc := scraper.NewBibleScraper(repo, bibleSpec, cfg)

	// 6. Run Scraper
	log.Println("Starting Bible Crawler...")
	if err := sc.Run(); err != nil {
		log.Fatalf("Bible Crawler failed: %v", err)
	}
	log.Println("Bible Crawler finished successfully.")
}
