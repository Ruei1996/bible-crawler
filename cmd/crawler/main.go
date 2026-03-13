package main

import (
	"log"

	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/scraper"
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

	// 4. Initialize Scraper
	sc := scraper.NewBibleScraper(repo)

	// 5. Run Scraper
	log.Println("Starting Bible Crawler...")
	if err := sc.Run(); err != nil {
		log.Fatalf("Bible Crawler failed: %v", err)
	}
	log.Println("Bible Crawler finished successfully.")
}
