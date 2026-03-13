// Package config loads all runtime settings from environment variables (and
// optionally a .env file). Every value that might change when the source
// website migrates, or when the operator wants to throttle/tune the crawler,
// is expressed as an env var with a documented default.
//
// Settings fall into three groups:
//   1. Database    — DATABASE_URL
//   2. Source site — SOURCE_DOMAIN, SOURCE_ZH_URL, SOURCE_EN_URL
//   3. Tuning      — CRAWLER_PARALLELISM, CRAWLER_DELAY_MS,
//                    CRAWLER_RANDOM_DELAY_MS, HTTP_TIMEOUT_SEC
package config

import (
"log"
"os"
"strconv"

"github.com/joho/godotenv"
)

// Config holds every runtime-configurable setting for the crawler system.
// All three commands (spec-builder, crawler, repair) share this struct so
// that a single .env change propagates everywhere.
type Config struct {
// ── Application ──────────────────────────────────────────────────────────
AppEnv string // "development" | "production"

// ── Database ─────────────────────────────────────────────────────────────
// Full PostgreSQL DSN. Only used by cmd/crawler and cmd/repair.
DBUrl string

// ── Source website ───────────────────────────────────────────────────────
// These three values encode all website-specific knowledge.
// Update them here (via .env) if the website migrates or if you switch to
// a different Bible translation for either language.
//
// SourceDomain — bare hostname used by Colly AllowedDomains and LimitRule.
// SourceZHURL  — URL template for Chinese (和合本 CUV) chapter pages.
//               Must contain exactly one %d placeholder for the global
//               chapter index (1-based, sequential across all 66 books).
// SourceENURL  — URL template for English (BBE) chapter pages.
//               Same %d convention as SourceZHURL.
SourceDomain string
SourceZHURL  string
SourceENURL  string

// ── Crawler tuning ───────────────────────────────────────────────────────
// Reduce parallelism / increase delays if the server starts throttling.
CrawlerParallelism   int // max concurrent outbound HTTP requests
CrawlerDelayMS       int // base delay between requests (milliseconds)
CrawlerRandomDelayMS int // additional random jitter per request (ms)
HTTPTimeoutSec       int // per-request HTTP timeout (seconds)
}

// Load reads configuration from a .env file (if present) then from the process
// environment. Environment variables always override .env values.
// Sensible defaults are applied for every field so the crawler works out of the
// box without a .env file for non-sensitive settings.
func Load() *Config {
if err := godotenv.Load(); err != nil {
log.Println("No .env file found, reading from system environment")
}

return &Config{
AppEnv: getEnv("APP_ENV", "development"),
DBUrl:  getEnv("DATABASE_URL", ""),

// Source website defaults point at the current springbible.fhl.net URLs.
// Override via .env when the website changes its URL structure or when
// switching to a different Chinese / English Bible translation.
SourceDomain: getEnv("SOURCE_DOMAIN", "springbible.fhl.net"),
SourceZHURL:  getEnv("SOURCE_ZH_URL", "https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0"),
SourceENURL:  getEnv("SOURCE_EN_URL", "https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe"),

CrawlerParallelism:   getEnvInt("CRAWLER_PARALLELISM", 5),
CrawlerDelayMS:       getEnvInt("CRAWLER_DELAY_MS", 200),
CrawlerRandomDelayMS: getEnvInt("CRAWLER_RANDOM_DELAY_MS", 100),
HTTPTimeoutSec:       getEnvInt("HTTP_TIMEOUT_SEC", 30),
}
}

// getEnv returns the env var value for key, or fallback when it is unset.
func getEnv(key, fallback string) string {
if value, exists := os.LookupEnv(key); exists {
return value
}
return fallback
}

// getEnvInt returns the integer env var value for key, or fallback when it is
// unset or not a valid integer (a warning is logged in the latter case).
func getEnvInt(key string, fallback int) int {
if value, exists := os.LookupEnv(key); exists {
if n, err := strconv.Atoi(value); err == nil {
return n
}
log.Printf("Config warning: %s=%q is not a valid integer, using default %d", key, value, fallback)
}
return fallback
}
