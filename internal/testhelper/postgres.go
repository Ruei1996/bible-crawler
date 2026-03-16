//go:build integration

package testhelper

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const schemaSQL = `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE SCHEMA IF NOT EXISTS bibles;

CREATE TABLE IF NOT EXISTS bibles.bible_books (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sort INTEGER NOT NULL,
    UNIQUE (sort)
);
CREATE TABLE IF NOT EXISTS bibles.bible_book_contents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bible_book_id UUID NOT NULL,
    language VARCHAR NOT NULL,
    title VARCHAR NOT NULL
);
CREATE TABLE IF NOT EXISTS bibles.bible_chapters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bible_book_id UUID NOT NULL,
    sort INTEGER NOT NULL,
    UNIQUE (bible_book_id, sort)
);
CREATE TABLE IF NOT EXISTS bibles.bible_chapter_contents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bible_chapter_id UUID NOT NULL,
    language VARCHAR NOT NULL,
    title VARCHAR NOT NULL
);
CREATE TABLE IF NOT EXISTS bibles.bible_sections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bible_book_id UUID NOT NULL,
    bible_chapter_id UUID NOT NULL,
    sort INTEGER NOT NULL,
    UNIQUE (bible_book_id, bible_chapter_id, sort)
);
CREATE TABLE IF NOT EXISTS bibles.bible_section_contents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bible_section_id UUID NOT NULL,
    language VARCHAR NOT NULL,
    title VARCHAR NOT NULL,
    content TEXT NOT NULL,
    sub_title VARCHAR
);
`

// StartPostgres spins up a PostgreSQL container, applies the Bible schema, and
// returns a ready *sqlx.DB plus a cleanup function to call in t.Cleanup.
func StartPostgres(t *testing.T) (db *sqlx.DB, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:18",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		ctr.Terminate(ctx) //nolint:errcheck
		t.Fatalf("failed to get connection string: %v", err)
	}

	db, err = sqlx.Connect("postgres", connStr)
	if err != nil {
		ctr.Terminate(ctx) //nolint:errcheck
		t.Fatalf("failed to connect to postgres: %v", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		ctr.Terminate(ctx) //nolint:errcheck
		t.Fatalf("failed to apply schema: %v", err)
	}

	cleanup = func() {
		db.Close()
		ctr.Terminate(ctx) //nolint:errcheck
	}
	return db, cleanup
}
