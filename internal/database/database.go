// Package database provides a helper to open and validate the PostgreSQL
// connection required by all three crawler commands.
package database

import (
	"log"
	"strings"

	"bible-crawler/internal/config"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// Connect opens and validates a PostgreSQL connection using app config.
// It enforces TLS by injecting sslmode=require when the DSN does not already
// specify an explicit sslmode parameter (CWE-319, A02:2021).
// It also applies conservative pool settings suitable for crawler workloads.
//
// Parameters:
//   - cfg: application config; only cfg.DBUrl is consumed by this function.
//
// Returns:
//   - *sqlx.DB: an open, pinged, pool-configured handle. The caller owns it
//     and must call db.Close() when finished (typically via defer).
//
// Side effects:
//   - Calls log.Fatalf (process exit) on connection or ping failure so the
//     crawler never proceeds with a silently broken database handle.
func Connect(cfg *config.Config) *sqlx.DB {
	db, err := sqlx.Connect("postgres", enforceTLS(cfg.DBUrl))
	if err != nil {
		// Do not log err directly — lib/pq error strings may include the full DSN
		// (host, user, database name, and sometimes the password), which would
		// leak credentials to any log aggregator (CWE-532, A09:2021).
		log.Fatalf("Failed to connect to database (check DATABASE_URL and network): %T", err)
	}

	// Connection pool settings: 25 open + 25 idle connections is sufficient for
	// the crawler's concurrency (CRAWLER_PARALLELISM default 5, YouVersion workers
	// default 20) while leaving headroom for the PostgreSQL server's connection limit.
	// Open = Idle keeps a warm pool: no connection is ever torn down and re-created
	// between parallel chapter requests, avoiding TCP + TLS overhead on every verse.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database (check DATABASE_URL and network): %T", err)
	}

	log.Println("Connected to database successfully")
	return db
}

// enforceTLS appends sslmode=require to a PostgreSQL DSN that does not already
// contain an explicit sslmode= parameter (CWE-319, A02:2021).
//
// lib/pq defaults to sslmode=prefer when no sslmode is specified, which silently
// falls back to plaintext if the server does not offer TLS — leaking the password
// and all Bible content on any unencrypted network path.
//
// If the operator has already supplied an explicit sslmode= (e.g. sslmode=disable
// for local testing), that choice is preserved unchanged.
//
// Both DSN formats are handled:
//
//	Key=value:  "host=db user=u password=p dbname=n"       → "… sslmode=require"
//	URI:        "postgres://u:p@host/dbname"               → "…?sslmode=require"
//	URI+params: "postgres://u:p@host/dbname?connect_timeout=10" → "…&sslmode=require"
func enforceTLS(dsn string) string {
	if strings.Contains(dsn, "sslmode=") {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "sslmode=require"
	}
	return dsn + " sslmode=require"
}
