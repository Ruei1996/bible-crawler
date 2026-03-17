// Package migration handles backup and restore of cross-schema bible references
// before and after truncating the bibles schema tables.
//
// Three tables in other microservice schemas store bibles.bible_sections(id)
// as a plain UUID column without a declared FK constraint:
//
//   - activities.general_bibles.bible_id
//   - activities.general_template_bibles.bible_id
//   - devotions.devotion_bibles.bible_section_id
//
// Because no FK exists, TRUNCATE ... CASCADE does not cascade to these tables.
// After a TRUNCATE + re-crawl every bibles UUID changes (gen_random_uuid), so
// these three columns become stale / orphaned.
//
// The fix: before truncating, record the stable coordinates
// (book_sort, chapter_sort, section_sort) for every referenced section.
// After re-crawl the same sort triple always resolves to the same logical verse,
// so the three tables can be updated to point at the new UUIDs.
package migration

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// BackupResult holds the count of rows captured per source table during Backup.
type BackupResult struct {
	GeneralBibles         int
	GeneralTemplateBibles int
	DevotionBibles        int
	Total                 int
}

// RestoreResult holds the count of rows updated per target table during Restore.
type RestoreResult struct {
	GeneralBibles         int
	GeneralTemplateBibles int
	DevotionBibles        int
	Total                 int
}

// OrphanResult holds orphan counts per table after a Restore.
// All values should be 0 after a successful Restore.
type OrphanResult struct {
	GeneralBibles         int
	GeneralTemplateBibles int
	DevotionBibles        int
	Total                 int
}

// ── SQL constants ─────────────────────────────────────────────────────────────

const sqlCreateBackupTable = `
CREATE TABLE IF NOT EXISTS bibles._orphan_refs_backup (
	source_table         varchar NOT NULL,
	source_id            uuid    NOT NULL,
	old_bible_section_id uuid    NOT NULL,
	book_sort            int     NOT NULL,
	chapter_sort         int     NOT NULL,
	section_sort         int     NOT NULL,
	PRIMARY KEY (source_table, source_id)
)`

const sqlInsertGeneralBibles = `
INSERT INTO bibles._orphan_refs_backup
SELECT
	'general_bibles',
	gb.id,
	gb.bible_id,
	bb.sort, bc.sort, bs.sort
FROM activities.general_bibles gb
JOIN bibles.bible_sections  bs ON bs.id = gb.bible_id
JOIN bibles.bible_books     bb ON bb.id = bs.bible_book_id
JOIN bibles.bible_chapters  bc ON bc.id = bs.bible_chapter_id
ON CONFLICT (source_table, source_id) DO NOTHING`

const sqlInsertGeneralTemplateBibles = `
INSERT INTO bibles._orphan_refs_backup
SELECT
	'general_template_bibles',
	gtb.id,
	gtb.bible_id,
	bb.sort, bc.sort, bs.sort
FROM activities.general_template_bibles gtb
JOIN bibles.bible_sections  bs ON bs.id = gtb.bible_id
JOIN bibles.bible_books     bb ON bb.id = bs.bible_book_id
JOIN bibles.bible_chapters  bc ON bc.id = bs.bible_chapter_id
ON CONFLICT (source_table, source_id) DO NOTHING`

const sqlInsertDevotionBibles = `
INSERT INTO bibles._orphan_refs_backup
SELECT
	'devotion_bibles',
	db.id,
	db.bible_section_id,
	bb.sort, bc.sort, bs.sort
FROM devotions.devotion_bibles db
JOIN bibles.bible_sections  bs ON bs.id = db.bible_section_id
JOIN bibles.bible_books     bb ON bb.id = bs.bible_book_id
JOIN bibles.bible_chapters  bc ON bc.id = bs.bible_chapter_id
ON CONFLICT (source_table, source_id) DO NOTHING`

const sqlUpdateGeneralBibles = `
UPDATE activities.general_bibles gb
SET    bible_id = new_bs.id
FROM   bibles._orphan_refs_backup bkp
JOIN   bibles.bible_books    new_bb ON new_bb.sort = bkp.book_sort
JOIN   bibles.bible_chapters new_bc ON new_bc.bible_book_id = new_bb.id AND new_bc.sort = bkp.chapter_sort
JOIN   bibles.bible_sections new_bs ON new_bs.bible_book_id = new_bb.id
                                    AND new_bs.bible_chapter_id = new_bc.id
                                    AND new_bs.sort = bkp.section_sort
WHERE  bkp.source_table = 'general_bibles'
  AND  gb.id = bkp.source_id`

const sqlUpdateGeneralTemplateBibles = `
UPDATE activities.general_template_bibles gtb
SET    bible_id = new_bs.id
FROM   bibles._orphan_refs_backup bkp
JOIN   bibles.bible_books    new_bb ON new_bb.sort = bkp.book_sort
JOIN   bibles.bible_chapters new_bc ON new_bc.bible_book_id = new_bb.id AND new_bc.sort = bkp.chapter_sort
JOIN   bibles.bible_sections new_bs ON new_bs.bible_book_id = new_bb.id
                                    AND new_bs.bible_chapter_id = new_bc.id
                                    AND new_bs.sort = bkp.section_sort
WHERE  bkp.source_table = 'general_template_bibles'
  AND  gtb.id = bkp.source_id`

const sqlUpdateDevotionBibles = `
UPDATE devotions.devotion_bibles db
SET    bible_section_id = new_bs.id
FROM   bibles._orphan_refs_backup bkp
JOIN   bibles.bible_books    new_bb ON new_bb.sort = bkp.book_sort
JOIN   bibles.bible_chapters new_bc ON new_bc.bible_book_id = new_bb.id AND new_bc.sort = bkp.chapter_sort
JOIN   bibles.bible_sections new_bs ON new_bs.bible_book_id = new_bb.id
                                    AND new_bs.bible_chapter_id = new_bc.id
                                    AND new_bs.sort = bkp.section_sort
WHERE  bkp.source_table = 'devotion_bibles'
  AND  db.id = bkp.source_id`

// ── Public API ────────────────────────────────────────────────────────────────

// Backup creates and populates bibles._orphan_refs_backup with the stable
// (book_sort, chapter_sort, section_sort) coordinates for every cross-schema
// row that logically references bibles.bible_sections without a declared FK.
// Safe to run multiple times (CREATE TABLE IF NOT EXISTS + ON CONFLICT DO NOTHING).
func Backup(db *sqlx.DB) (BackupResult, error) {
	if _, err := db.Exec(sqlCreateBackupTable); err != nil {
		return BackupResult{}, fmt.Errorf("create backup table: %w", err)
	}

	res1, err := db.Exec(sqlInsertGeneralBibles)
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup general_bibles: %w", err)
	}
	n1, _ := res1.RowsAffected()

	res2, err := db.Exec(sqlInsertGeneralTemplateBibles)
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup general_template_bibles: %w", err)
	}
	n2, _ := res2.RowsAffected()

	res3, err := db.Exec(sqlInsertDevotionBibles)
	if err != nil {
		return BackupResult{}, fmt.Errorf("backup devotion_bibles: %w", err)
	}
	n3, _ := res3.RowsAffected()

	return BackupResult{
		GeneralBibles:         int(n1),
		GeneralTemplateBibles: int(n2),
		DevotionBibles:        int(n3),
		Total:                 int(n1 + n2 + n3),
	}, nil
}

// TruncateBibles clears all six bibles schema tables via CASCADE.
// Call this after a successful Backup and before running the crawler.
func TruncateBibles(db *sqlx.DB) error {
	if _, err := db.Exec(`TRUNCATE TABLE bibles.bible_books CASCADE`); err != nil {
		return fmt.Errorf("truncate bibles: %w", err)
	}
	return nil
}

// Restore updates the three cross-schema tables with newly assigned
// bibles.bible_sections UUIDs by matching on the stable
// (book_sort, chapter_sort, section_sort) coordinates stored in the backup table.
// Must be called after the crawler has fully re-populated the bibles schema.
func Restore(db *sqlx.DB) (RestoreResult, error) {
	res1, err := db.Exec(sqlUpdateGeneralBibles)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore general_bibles: %w", err)
	}
	n1, _ := res1.RowsAffected()

	res2, err := db.Exec(sqlUpdateGeneralTemplateBibles)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore general_template_bibles: %w", err)
	}
	n2, _ := res2.RowsAffected()

	res3, err := db.Exec(sqlUpdateDevotionBibles)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore devotion_bibles: %w", err)
	}
	n3, _ := res3.RowsAffected()

	return RestoreResult{
		GeneralBibles:         int(n1),
		GeneralTemplateBibles: int(n2),
		DevotionBibles:        int(n3),
		Total:                 int(n1 + n2 + n3),
	}, nil
}

// Verify counts orphaned cross-schema bible references that no longer exist in
// bibles.bible_sections. All counts should be 0 after a successful Restore.
func Verify(db *sqlx.DB) (OrphanResult, error) {
	var n1, n2, n3 int

	if err := db.QueryRow(
		`SELECT count(*) FROM activities.general_bibles gb WHERE NOT EXISTS (SELECT 1 FROM bibles.bible_sections bs WHERE bs.id = gb.bible_id)`,
	).Scan(&n1); err != nil {
		return OrphanResult{}, fmt.Errorf("verify general_bibles: %w", err)
	}

	if err := db.QueryRow(
		`SELECT count(*) FROM activities.general_template_bibles gtb WHERE NOT EXISTS (SELECT 1 FROM bibles.bible_sections bs WHERE bs.id = gtb.bible_id)`,
	).Scan(&n2); err != nil {
		return OrphanResult{}, fmt.Errorf("verify general_template_bibles: %w", err)
	}

	if err := db.QueryRow(
		`SELECT count(*) FROM devotions.devotion_bibles db WHERE NOT EXISTS (SELECT 1 FROM bibles.bible_sections bs WHERE bs.id = db.bible_section_id)`,
	).Scan(&n3); err != nil {
		return OrphanResult{}, fmt.Errorf("verify devotion_bibles: %w", err)
	}

	return OrphanResult{
		GeneralBibles:         n1,
		GeneralTemplateBibles: n2,
		DevotionBibles:        n3,
		Total:                 n1 + n2 + n3,
	}, nil
}

// CleanupBackup drops the temporary backup table after a successful Restore.
func CleanupBackup(db *sqlx.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS bibles._orphan_refs_backup`); err != nil {
		return fmt.Errorf("cleanup backup table: %w", err)
	}
	return nil
}
