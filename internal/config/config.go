package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Config contains runtime settings required by the crawler process.
type Config struct {
	AppEnv string
	DBUrl  string
}

// Load reads configuration from `.env` (if present) and environment variables.
// Environment variables always take precedence over fallback defaults.
func Load() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	return &Config{
		AppEnv: getEnv("APP_ENV", "development"),
		DBUrl:  getEnv("DATABASE_URL", "postgres://ooxx:ooxx@localhost:5432/topchurch_dev?sslmode=disable"),
	}
}

// getEnv returns an env var value or fallback when the variable is missing.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
