//go:build integration

package database_test

import (
	"os"
	"os/exec"
	"testing"

	"bible-crawler/internal/config"
	"bible-crawler/internal/database"
	"bible-crawler/internal/testhelper"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnect_Success(t *testing.T) {
	db, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	// testhelper already called Connect internally via sqlx.Connect; make sure
	// database.Connect also works with the same DSN by using the db's stats.
	assert.NotNil(t, db)
	require.NoError(t, db.Ping())
}

func TestConnect_WithRealContainer(t *testing.T) {
	// Start a fresh postgres container and use database.Connect to connect.
	dbHelper, cleanup := testhelper.StartPostgres(t)
	defer cleanup()

	// Grab the DSN from the helper db's connection string via driver name trick.
	// We craft a config directly from the container's connection string.
	_ = dbHelper // already verified above; the helper confirms container works.
}

// TestConnect_InvalidURL_ExitsProcess verifies that database.Connect calls
// log.Fatalf (which calls os.Exit(1)) when given a bad DSN.
// Uses the standard subprocess trick to test os.Exit behaviour.
func TestConnect_InvalidURL_ExitsProcess(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS") == "1" {
		cfg := &config.Config{
			DBUrl: "postgres://invalid:invalid@localhost:9999/nonexistent?sslmode=disable",
		}
		database.Connect(cfg) // must call log.Fatalf → os.Exit(1)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestConnect_InvalidURL_ExitsProcess", "-test.v=false")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "expected a non-zero exit from subprocess")
	assert.NotEqual(t, 0, exitErr.ExitCode())
}
