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

// runBackup first runs a pre-check to detect references that are already broken
// before the migration starts, then captures stable (book_sort, chapter_sort,
// section_sort) coordinates for every valid cross-schema bible reference into
// the bibles._orphan_refs_backup table.
// When withTruncate is true it also cascades a TRUNCATE on bibles.bible_books
// immediately after the backup, clearing all six bibles schema tables in one
// statement so the operator can proceed directly to re-crawling.
func runBackup(db *sqlx.DB, withTruncate bool) {
	log.Println("Phase: backup — pre-checking for stale cross-schema references...")

	precheck, err := migration.PreCheck(db)
	if err != nil {
		log.Fatalf("Pre-check failed: %v", err)
	}
	log.Printf("Pre-check result (already-broken references before this migration):")
	log.Printf("  activities.general_bibles:          %d rows", precheck.GeneralBibles)
	log.Printf("  activities.general_template_bibles: %d rows", precheck.GeneralTemplateBibles)
	log.Printf("  devotions.devotion_bibles:           %d rows", precheck.DevotionBibles)
	log.Printf("  notes.note_items (category=bible):  %d rows", precheck.NoteItems)
	log.Printf("  Total:                               %d rows", precheck.Total)

	if precheck.Total > 0 {
		log.Printf("WARNING: %d reference(s) are ALREADY broken and cannot be recovered by this migration.", precheck.Total)
		log.Printf("  These rows point to bibles.bible_sections UUIDs that no longer exist.")
		log.Printf("  Root cause: a previous re-crawl ran without this migration tool,")
		log.Printf("  or the rows were copied from a different environment with different bibles UUIDs.")
		log.Printf("  After restore, the orphan-check will still report these rows.")
		log.Printf("  Remedies (apply BEFORE or AFTER this migration):")
		log.Printf("    1. DELETE the stale rows if they are no longer needed:")
		log.Printf("       DELETE FROM <table> WHERE NOT EXISTS")
		log.Printf("         (SELECT 1 FROM bibles.bible_sections WHERE id = <bible_section_col>);")
		log.Printf("    2. Look up (book_sort, chapter_sort, section_sort) for each stale UUID")
		log.Printf("       from another environment (e.g. production) and re-point them manually.")
		log.Printf("  Continuing backup for the remaining valid references...")
	}

	log.Println("Capturing valid cross-schema bible references...")

	result, err := migration.Backup(db)
	if err != nil {
		log.Fatalf("Backup failed: %v", err)
	}
	log.Printf("Backup complete:")
	log.Printf("  activities.general_bibles:          %d rows", result.GeneralBibles)
	log.Printf("  activities.general_template_bibles: %d rows", result.GeneralTemplateBibles)
	log.Printf("  devotions.devotion_bibles:           %d rows", result.DevotionBibles)
	log.Printf("  notes.note_items (category=bible):  %d rows", result.NoteItems)
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

// runRestore updates the three cross-schema tables (activities.general_bibles,
// activities.general_template_bibles, devotions.devotion_bibles) so their
// bible_id / bible_section_id columns point at the new UUIDs assigned by the
// re-crawl. It then calls Verify to confirm no orphaned references remain,
// and — when withCleanup is true and orphan count is 0 — drops the temporary
// backup table to clean up.
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
	log.Printf("  notes.note_items updated (category=bible):  %d rows", result.NoteItems)
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
	log.Printf("  notes.note_items (category=bible):  %d", orphans.NoteItems)

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
