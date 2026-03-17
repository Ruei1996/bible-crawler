// cmd/migrate/main.go manages cross-schema bible reference backup and restore
// around a TRUNCATE + re-crawl cycle.
//
// # Before TRUNCATE: backup cross-schema references
//
//	go run cmd/migrate/main.go --phase=backup
//
// To also truncate the bibles tables immediately after backup:
//
//	go run cmd/migrate/main.go --phase=backup --truncate
//
// # After re-crawl: restore references with new UUIDs
//
//	go run cmd/migrate/main.go --phase=restore
//
// To also drop the backup table after a clean restore:
//
//	go run cmd/migrate/main.go --phase=restore --cleanup
//
// # Full re-crawl sequence
//
//	go run cmd/migrate/main.go --phase=backup --truncate
//	go run cmd/spec-builder/main.go          # optional: rebuild spec JSON
//	go run cmd/crawler/main.go
//	go run cmd/migrate/main.go --phase=restore --cleanup
package main

import (
	"flag"
	"log"

	"github.com/jmoiron/sqlx"

	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/migration"
)

func main() {
	phase := flag.String("phase", "", "Migration phase: 'backup' or 'restore' (required)")
	truncate := flag.Bool("truncate", false, "Also truncate bibles tables after backup (only with --phase=backup)")
	cleanup := flag.Bool("cleanup", false, "Drop backup table after a clean restore (only with --phase=restore)")
	flag.Parse()

	if *phase == "" {
		log.Fatal("--phase is required: use 'backup' or 'restore'")
	}

	cfg := config.Load()
	db := database.Connect(cfg)
	defer db.Close()

	switch *phase {
	case "backup":
		runBackup(db, *truncate)
	case "restore":
		runRestore(db, *cleanup)
	default:
		log.Fatalf("Unknown phase %q: use 'backup' or 'restore'", *phase)
	}
}

func runBackup(db *sqlx.DB, withTruncate bool) {
	log.Println("Phase: backup — capturing cross-schema bible references...")

	result, err := migration.Backup(db)
	if err != nil {
		log.Fatalf("Backup failed: %v", err)
	}
	log.Printf("Backup complete:")
	log.Printf("  activities.general_bibles:          %d rows", result.GeneralBibles)
	log.Printf("  activities.general_template_bibles: %d rows", result.GeneralTemplateBibles)
	log.Printf("  devotions.devotion_bibles:           %d rows", result.DevotionBibles)
	log.Printf("  Total:                               %d rows", result.Total)

	if withTruncate {
		log.Println("Truncating bibles tables (CASCADE)...")
		if err := migration.TruncateBibles(db); err != nil {
			log.Fatalf("Truncate failed: %v", err)
		}
		log.Println("Truncate complete. All 6 bibles tables cleared.")
	} else {
		log.Println("Tip: run 'TRUNCATE TABLE bibles.bible_books CASCADE;' then re-crawl.")
	}
}

func runRestore(db *sqlx.DB, withCleanup bool) {
	log.Println("Phase: restore — updating cross-schema bible references with new UUIDs...")

	result, err := migration.Restore(db)
	if err != nil {
		log.Fatalf("Restore failed: %v", err)
	}
	log.Printf("Restore complete:")
	log.Printf("  activities.general_bibles updated:          %d rows", result.GeneralBibles)
	log.Printf("  activities.general_template_bibles updated: %d rows", result.GeneralTemplateBibles)
	log.Printf("  devotions.devotion_bibles updated:           %d rows", result.DevotionBibles)
	log.Printf("  Total:                                       %d rows", result.Total)

	log.Println("Verifying orphan counts...")
	orphans, err := migration.Verify(db)
	if err != nil {
		log.Fatalf("Verify failed: %v", err)
	}
	log.Printf("Orphan check:")
	log.Printf("  activities.general_bibles:          %d", orphans.GeneralBibles)
	log.Printf("  activities.general_template_bibles: %d", orphans.GeneralTemplateBibles)
	log.Printf("  devotions.devotion_bibles:           %d", orphans.DevotionBibles)

	if orphans.Total > 0 {
		log.Printf("WARNING: %d orphan reference(s) remain. Review before using --cleanup.", orphans.Total)
	} else {
		log.Println("All cross-schema references are valid.")
	}

	if withCleanup {
		if orphans.Total > 0 {
			log.Fatal("Aborting cleanup: orphan references detected. Fix them first.")
		}
		log.Println("Cleaning up backup table...")
		if err := migration.CleanupBackup(db); err != nil {
			log.Fatalf("Cleanup failed: %v", err)
		}
		log.Println("Backup table dropped.")
	}
}
