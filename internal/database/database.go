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

	// Connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Println("Connected to database successfully")
	return db
}
