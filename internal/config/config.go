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
"strings"

"github.com/joho/godotenv"
)

// Config holds every runtime-configurable setting for the crawler system.
// All two commands (spec-builder, crawler) share this struct so
// that a single .env change propagates everywhere.
type Config struct {
// ── Application ──────────────────────────────────────────────────────────
AppEnv string // "development" | "production"

// ── Database ─────────────────────────────────────────────────────────────
// Full PostgreSQL DSN. Only used by cmd/crawler.
DBUrl string

// ── Source website ───────────────────────────────────────────────────────
// These three values encode all website-specific knowledge.
// Update them here (via .env) if the website migrates or if you switch to
// a different Bible translation for either language.
//
// SourceDomain — bare hostname used by Colly AllowedDomains and LimitRule.
// SourceZHURL  — URL template for Chinese (和合本 CUV) chapter pages.
//               Must contain exactly one %d placeholder for the global
//               chapter index (1-based, sequential across all books).
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

// ── YouVersion API ───────────────────────────────────────────────────────
// Settings for cmd/youversion-crawler, which calls the YouVersion Platform API.
//
// YouVersionAPIKey      — app key sent as the x-yvp-app-key request header.
//                         Required; obtain from https://platform.youversion.com
// YouVersionBaseURL     — base URL for the YouVersion API (without trailing slash).
// YouVersionChineseBibleID — YouVersion Bible ID for the Chinese translation.
//                         Default 312 = CSB 中文標準譯本 (Chinese Standard Bible, traditional Chinese). Also available: 36=CCB (Simplified), 43=CSBS (Simplified).
//                         Switch to 46 (新標點和合本) after obtaining a publisher license.
// YouVersionEnglishBibleID — YouVersion Bible ID for the English translation.
//                         Default 111 = NIV 2011 (freely accessible).
//
// ── YouVersion parallel-crawl tuning ─────────────────────────────────────
// These settings control the fault-tolerant parallel crawler and are all
// required — cmd/youversion-crawler will fatal if Checkpoint is not set.
//
// YouVersionWorkers      — number of parallel worker goroutines (default 3).
//                          Should be ≥ YouVersionRateLimitRPS to keep all
//                          workers busy; higher values risk rate-limiting.
// YouVersionRateLimitRPS — token-bucket rate limit: maximum requests per second
//                          across all workers combined (default 2.0).
// YouVersionMaxRetries   — maximum retry attempts per verse on transient errors
//                          (default 5). 404 responses are never retried.
// YouVersionRetryBaseMS  — initial backoff interval in milliseconds; doubles on
//                          each retry with ±25 % jitter (default 1000).
// YouVersionCheckpointFile — path to the JSONL checkpoint file (required).
//                          Each line stores one fetched verse; serves as the
//                          progress log (for resume) and the input for
//                          cmd/youversion-importer. Must not be empty.
YouVersionAPIKey          string
YouVersionBaseURL         string
YouVersionChineseBibleID  int
YouVersionEnglishBibleID  int
YouVersionWorkers         int
YouVersionRateLimitRPS    float64
YouVersionMaxRetries      int
YouVersionRetryBaseMS     int
YouVersionCheckpointFile  string

// ── Bible.com HTML crawler ────────────────────────────────────────────────
// Settings for cmd/biblecom-crawler, which scrapes bible.com HTML pages for
// the CUNP-上帝 (Chinese) and NIV (English) translations and writes the
// results to two JSON files (BibleComOutputZH / BibleComOutputEN).
//
// BibleComZHBaseURL        — base URL for Chinese CUNP-上帝 pages.
//                            Default: https://www.bible.com/bible/414
// BibleComENBaseURL        — base URL for English NIV pages.
//                            Default: https://www.bible.com/bible/111
// BibleComZHVersionSuffix  — URL suffix appended after the chapter number.
//                            The Chinese suffix contains percent-encoded
//                            Chinese characters (上帝 = %E4%B8%8A%E5%B8%9D).
//                            Default: CUNP-%E4%B8%8A%E5%B8%9D
// BibleComENVersionSuffix  — URL suffix for the English version.
//                            Default: NIV
// BibleComWorkers          — number of parallel worker goroutines (default 5).
//                            Increase with caution — bible.com may rate-limit
//                            or block excessive parallel connections.
// BibleComRateLimitRPS     — token-bucket rate limit in requests per second
//                            across all workers (default 2.0). Should be
//                            ≤ BibleComWorkers to avoid worker starvation.
// BibleComTimeoutSec       — per-request HTTP timeout in seconds (default 30).
// BibleComOutputZH         — output JSON file path for Chinese content.
//                            Default: youversion-bible_books_zh.json
// BibleComOutputEN         — output JSON file path for English content.
//                            Default: youversion-bible_books_en.json
// BibleComFilterSorts     — optional comma-separated list of book sort numbers
//                            (1-66) to limit the crawl to specific books.
//                            Example: BIBLECOM_FILTER_SORTS=65 re-crawls only
//                            Jude without touching the other 65 books.
//                            When unset, all 66 books are crawled.
BibleComFilterSorts     []int
BibleComZHBaseURL        string
BibleComENBaseURL        string
BibleComZHVersionSuffix  string
BibleComENVersionSuffix  string
BibleComWorkers          int
BibleComRateLimitRPS     float64
BibleComTimeoutSec       int
BibleComOutputZH         string
BibleComOutputEN         string
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

YouVersionAPIKey:         getEnv("YOUVERSION_API_KEY", ""),
YouVersionBaseURL:        getEnv("YOUVERSION_BASE_URL", "https://api.youversion.com/v1"),
YouVersionChineseBibleID: getEnvInt("YOUVERSION_CHINESE_BIBLE_ID", 312),
YouVersionEnglishBibleID: getEnvInt("YOUVERSION_ENGLISH_BIBLE_ID", 111),
YouVersionWorkers:        getEnvInt("YOUVERSION_WORKERS", 3),
YouVersionRateLimitRPS:   getEnvFloat64("YOUVERSION_RATE_LIMIT_RPS", 2.0),
YouVersionMaxRetries:     getEnvInt("YOUVERSION_MAX_RETRIES", 5),
YouVersionRetryBaseMS:    getEnvInt("YOUVERSION_RETRY_BASE_MS", 1000),
YouVersionCheckpointFile: getEnv("YOUVERSION_CHECKPOINT_FILE", ""),

// Bible.com HTML crawler defaults. The ZH version suffix contains
// percent-encoded Chinese characters for 上帝 (%E4%B8%8A%E5%B8%9D).
// These defaults work without any .env override for a standard crawl.
BibleComFilterSorts:     getEnvIntSlice("BIBLECOM_FILTER_SORTS"),
BibleComZHBaseURL:       getEnv("BIBLECOM_ZH_BASE_URL", "https://www.bible.com/bible/414"),
BibleComENBaseURL:       getEnv("BIBLECOM_EN_BASE_URL", "https://www.bible.com/bible/111"),
BibleComZHVersionSuffix: getEnv("BIBLECOM_ZH_VERSION_SUFFIX", "CUNP-%E4%B8%8A%E5%B8%9D"),
BibleComENVersionSuffix: getEnv("BIBLECOM_EN_VERSION_SUFFIX", "NIV"),
BibleComWorkers:         getEnvInt("BIBLECOM_WORKERS", 5),
BibleComRateLimitRPS:    getEnvFloat64("BIBLECOM_RATE_LIMIT_RPS", 2.0),
BibleComTimeoutSec:      getEnvInt("BIBLECOM_TIMEOUT_SEC", 30),
BibleComOutputZH:        getEnv("BIBLECOM_OUTPUT_ZH", "youversion-bible_books_zh.json"),
BibleComOutputEN:        getEnv("BIBLECOM_OUTPUT_EN", "youversion-bible_books_en.json"),
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

// getEnvIntSlice returns a slice of integers parsed from a comma-separated env
// var. Values must be valid Bible book sort numbers in the range [1, 66].
// Elements that are not valid integers or outside the [1, 66] range are skipped
// with a warning. Returns nil when the env var is unset or empty (meaning "no
// filter" to callers).
func getEnvIntSlice(key string) []int {
value, exists := os.LookupEnv(key)
if !exists || strings.TrimSpace(value) == "" {
return nil
}
parts := strings.Split(value, ",")
result := make([]int, 0, len(parts))
for _, p := range parts {
p = strings.TrimSpace(p)
n, err := strconv.Atoi(p)
if err != nil {
log.Printf("Config warning: %s element %q is not a valid integer, skipping", key, p)
continue
}
// Bible book sort numbers are 1–66; values outside this range will
// silently match no book, producing a zero-item crawl that is hard to debug.
if n < 1 || n > 66 {
log.Printf("Config warning: %s element %d is outside valid range [1, 66], skipping", key, n)
continue
}
result = append(result, n)
}
return result
}

// getEnvFloat64 returns the float64 env var value for key, or fallback when it
// is unset or not a valid float (a warning is logged in the latter case).
func getEnvFloat64(key string, fallback float64) float64 {
if value, exists := os.LookupEnv(key); exists {
if f, err := strconv.ParseFloat(value, 64); err == nil {
return f
}
log.Printf("Config warning: %s=%q is not a valid float, using default %g", key, value, fallback)
}
return fallback
}
