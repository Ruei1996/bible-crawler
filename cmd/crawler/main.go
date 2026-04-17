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
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"runtime"

	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/repository"
	"bible-crawler/internal/scraper"
	"bible-crawler/internal/spec"
)

// validateSourceURLs verifies that SOURCE_ZH_URL and SOURCE_EN_URL only target
// the configured SourceDomain over HTTPS (CWE-918, A10:2021 SSRF prevention).
//
// Without this check a misconfigured or compromised .env could set SOURCE_ZH_URL
// to an internal metadata endpoint (e.g. http://169.254.169.254/...), causing all
// Colly workers to probe cloud-provider instance metadata or internal services.
//
// Parameters:
//   - cfg: application config; SourceDomain, SourceZHURL, and SourceENURL
//     are the only fields read. SourceDomain is the sole entry in the allow-list.
//
// Returns:
//   - nil when both URL templates pass every check.
//   - a descriptive non-nil error naming the offending env-var and value on the
//     first validation failure so operators can identify the misconfiguration
//     without inspecting the raw DSN or rerunning with verbose logging.
func validateSourceURLs(cfg *config.Config) error {
	allowedHosts := map[string]bool{
		cfg.SourceDomain: true,
	}
	templates := []struct{ name, tmpl string }{
		{"SOURCE_ZH_URL", cfg.SourceZHURL},
		{"SOURCE_EN_URL", cfg.SourceENURL},
	}
	for _, t := range templates {
		// Substitute a safe numeric placeholder so the template becomes a valid URL.
		candidate := fmt.Sprintf(t.tmpl, 1)
		u, err := url.Parse(candidate)
		if err != nil {
			return fmt.Errorf("%s is not a valid URL template: %w", t.name, err)
		}
		if u.Scheme != "https" {
			return fmt.Errorf("%s must use https://, got scheme %q", t.name, u.Scheme)
		}
		if !allowedHosts[u.Hostname()] {
			return fmt.Errorf(
				"%s host %q is not in the allowed-domain list %v (CWE-918)",
				t.name, u.Hostname(), cfg.SourceDomain)
		}
	}
	return nil
}

// main wires configuration, database, repository, and scraper components.
// It is the standard full-crawl entrypoint.
func main() {
	// 1. Load Config
	cfg := config.Load()

	// 2. Validate source URL templates against the SSRF allow-list before any
	//    HTTP traffic is initiated (CWE-918, A10:2021).
	if err := validateSourceURLs(cfg); err != nil {
		log.Fatalf("Invalid source URL configuration: %v", err)
	}

	// 3. Connect to Database
	db := database.Connect(cfg)
	defer db.Close()

	// 4. Initialize Repository
	repo := repository.NewBibleRepository(db)

	// 5. Load Bible spec JSON files.
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

	// 6. Initialize Scraper — pass spec and config so it uses the correct
	//    source URLs and tuning parameters from .env.
	sc := scraper.NewBibleScraper(repo, bibleSpec, cfg)

	// 7. Run Scraper
	log.Println("Starting Bible Crawler...")
	if err := sc.Run(); err != nil {
		log.Fatalf("Bible Crawler failed: %v", err)
	}
	log.Println("Bible Crawler finished successfully.")
}
