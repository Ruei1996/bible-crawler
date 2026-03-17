package migration_test

import (
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bible-crawler/internal/migration"
)

// newMockDB creates a sqlmock *sqlx.DB with regexp query matching (the default).
func newMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

// qre escapes a literal string for use as an exact regexp pattern.
func qre(q string) string { return regexp.QuoteMeta(q) }

var errDB = errors.New("db error")

// Unique substrings used to match each SQL statement in mock expectations.
// Each is chosen from a single line that appears only in that one SQL constant.
const (
	matchCreateBackupTable         = "CREATE TABLE IF NOT EXISTS bibles._orphan_refs_backup"
	matchInsertGeneralBibles       = "'general_bibles',"
	matchInsertGeneralTplBibles    = "'general_template_bibles',"
	matchInsertDevotionBibles      = "'devotion_bibles',"
	matchTruncateBibles            = "TRUNCATE TABLE bibles.bible_books CASCADE"
	matchUpdateGeneralBibles       = "UPDATE activities.general_bibles gb"
	matchUpdateGeneralTplBibles    = "UPDATE activities.general_template_bibles gtb"
	matchUpdateDevotionBibles      = "UPDATE devotions.devotion_bibles db"
	matchVerifyGeneralBibles       = "FROM activities.general_bibles gb WHERE NOT EXISTS"
	matchVerifyGeneralTplBibles    = "FROM activities.general_template_bibles gtb WHERE NOT EXISTS"
	matchVerifyDevotionBibles      = "FROM devotions.devotion_bibles db WHERE NOT EXISTS"
	matchDropBackupTable           = "DROP TABLE IF EXISTS bibles._orphan_refs_backup"
)

// ── Backup ────────────────────────────────────────────────────────────────────

func TestBackup_Success(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchCreateBackupTable)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(qre(matchInsertGeneralTplBibles)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(qre(matchInsertDevotionBibles)).WillReturnResult(sqlmock.NewResult(0, 5))

	result, err := migration.Backup(db)

	require.NoError(t, err)
	assert.Equal(t, 3, result.GeneralBibles)
	assert.Equal(t, 2, result.GeneralTemplateBibles)
	assert.Equal(t, 5, result.DevotionBibles)
	assert.Equal(t, 10, result.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackup_EmptyTables(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchCreateBackupTable)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertGeneralTplBibles)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertDevotionBibles)).WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := migration.Backup(db)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackup_CreateTableError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchCreateBackupTable)).WillReturnError(errDB)

	_, err := migration.Backup(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create backup table")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackup_InsertGeneralBiblesError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchCreateBackupTable)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertGeneralBibles)).WillReturnError(errDB)

	_, err := migration.Backup(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup general_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackup_InsertGeneralTemplateBiblesError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchCreateBackupTable)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(qre(matchInsertGeneralTplBibles)).WillReturnError(errDB)

	_, err := migration.Backup(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup general_template_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackup_InsertDevotionBiblesError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchCreateBackupTable)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(qre(matchInsertGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(qre(matchInsertGeneralTplBibles)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(qre(matchInsertDevotionBibles)).WillReturnError(errDB)

	_, err := migration.Backup(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup devotion_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ── TruncateBibles ────────────────────────────────────────────────────────────

func TestTruncateBibles_Success(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchTruncateBibles)).WillReturnResult(sqlmock.NewResult(0, 0))

	err := migration.TruncateBibles(db)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTruncateBibles_Error(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchTruncateBibles)).WillReturnError(errDB)

	err := migration.TruncateBibles(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "truncate bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ── Restore ───────────────────────────────────────────────────────────────────

func TestRestore_Success(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchUpdateGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(qre(matchUpdateGeneralTplBibles)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(qre(matchUpdateDevotionBibles)).WillReturnResult(sqlmock.NewResult(0, 5))

	result, err := migration.Restore(db)

	require.NoError(t, err)
	assert.Equal(t, 3, result.GeneralBibles)
	assert.Equal(t, 2, result.GeneralTemplateBibles)
	assert.Equal(t, 5, result.DevotionBibles)
	assert.Equal(t, 10, result.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRestore_UpdateGeneralBiblesError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchUpdateGeneralBibles)).WillReturnError(errDB)

	_, err := migration.Restore(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore general_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRestore_UpdateGeneralTemplateBiblesError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchUpdateGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(qre(matchUpdateGeneralTplBibles)).WillReturnError(errDB)

	_, err := migration.Restore(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore general_template_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRestore_UpdateDevotionBiblesError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchUpdateGeneralBibles)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(qre(matchUpdateGeneralTplBibles)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(qre(matchUpdateDevotionBibles)).WillReturnError(errDB)

	_, err := migration.Restore(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "restore devotion_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ── Verify ────────────────────────────────────────────────────────────────────

func TestVerify_AllZero(t *testing.T) {
	db, mock := newMockDB(t)

	countRow := func(n int) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"count"}).AddRow(n)
	}
	mock.ExpectQuery(qre(matchVerifyGeneralBibles)).WillReturnRows(countRow(0))
	mock.ExpectQuery(qre(matchVerifyGeneralTplBibles)).WillReturnRows(countRow(0))
	mock.ExpectQuery(qre(matchVerifyDevotionBibles)).WillReturnRows(countRow(0))

	result, err := migration.Verify(db)

	require.NoError(t, err)
	assert.Equal(t, 0, result.GeneralBibles)
	assert.Equal(t, 0, result.GeneralTemplateBibles)
	assert.Equal(t, 0, result.DevotionBibles)
	assert.Equal(t, 0, result.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerify_WithOrphans(t *testing.T) {
	db, mock := newMockDB(t)

	countRow := func(n int) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"count"}).AddRow(n)
	}
	mock.ExpectQuery(qre(matchVerifyGeneralBibles)).WillReturnRows(countRow(2))
	mock.ExpectQuery(qre(matchVerifyGeneralTplBibles)).WillReturnRows(countRow(0))
	mock.ExpectQuery(qre(matchVerifyDevotionBibles)).WillReturnRows(countRow(3))

	result, err := migration.Verify(db)

	require.NoError(t, err)
	assert.Equal(t, 2, result.GeneralBibles)
	assert.Equal(t, 0, result.GeneralTemplateBibles)
	assert.Equal(t, 3, result.DevotionBibles)
	assert.Equal(t, 5, result.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerify_GeneralBiblesQueryError(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectQuery(qre(matchVerifyGeneralBibles)).WillReturnError(errDB)

	_, err := migration.Verify(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify general_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerify_GeneralTemplateBiblesQueryError(t *testing.T) {
	db, mock := newMockDB(t)

	countRow := func(n int) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"count"}).AddRow(n)
	}
	mock.ExpectQuery(qre(matchVerifyGeneralBibles)).WillReturnRows(countRow(0))
	mock.ExpectQuery(qre(matchVerifyGeneralTplBibles)).WillReturnError(errDB)

	_, err := migration.Verify(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify general_template_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerify_DevotionBiblesQueryError(t *testing.T) {
	db, mock := newMockDB(t)

	countRow := func(n int) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"count"}).AddRow(n)
	}
	mock.ExpectQuery(qre(matchVerifyGeneralBibles)).WillReturnRows(countRow(0))
	mock.ExpectQuery(qre(matchVerifyGeneralTplBibles)).WillReturnRows(countRow(0))
	mock.ExpectQuery(qre(matchVerifyDevotionBibles)).WillReturnError(errDB)

	_, err := migration.Verify(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify devotion_bibles")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ── CleanupBackup ─────────────────────────────────────────────────────────────

func TestCleanupBackup_Success(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchDropBackupTable)).WillReturnResult(sqlmock.NewResult(0, 0))

	err := migration.CleanupBackup(db)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupBackup_Error(t *testing.T) {
	db, mock := newMockDB(t)

	mock.ExpectExec(qre(matchDropBackupTable)).WillReturnError(errDB)

	err := migration.CleanupBackup(db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cleanup backup table")
	require.NoError(t, mock.ExpectationsWereMet())
}
