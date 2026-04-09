// Package database provides a helper to open and validate the PostgreSQL
// connection required by all three crawler commands.
package database

import (
	"log"

	"bible-crawler/internal/config"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// Connect opens and validates a PostgreSQL connection using app config.
// It also applies conservative pool settings suitable for crawler workloads.
func Connect(cfg *config.Config) *sqlx.DB {
	db, err := sqlx.Connect("postgres", cfg.DBUrl)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Connection pool settings: 25 open + 25 idle connections is sufficient for
	// the crawler's concurrency (CRAWLER_PARALLELISM default 5, YouVersion workers
	// default 20) while leaving headroom for the PostgreSQL server's connection limit.
	// Open = Idle keeps a warm pool: no connection is ever torn down and re-created
	// between parallel chapter requests, avoiding TCP + TLS overhead on every verse.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Println("Connected to database successfully")
	return db
}
