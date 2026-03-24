# Bible Crawler

A Go-based Bible crawler that populates a PostgreSQL database with a strict, normalized schema. It supports **two data sources** — the original HTML scraper (`springbible.fhl.net`) and the [YouVersion Platform REST API](https://api.youversion.com/v1) — writing to the exact same schema so both approaches are fully interchangeable.

## 🌟 Features

- **Dual Data-Source Support**:
  - **HTML Scraper** (`cmd/crawler`): crawls Chinese Union Version (和合本) and BBE from `springbible.fhl.net`. Spec-driven — verse counts come from JSON spec files.
  - **YouVersion API Crawler** (`cmd/youversion-crawler` + `cmd/youversion-importer`): fetches Bible content via the [YouVersion Platform API](https://developers.youversion.com/api/bibles). No spec files needed — all structure is derived from the API itself. Supports two sub-modes: **sequential** (direct DB writes, original behaviour) and **parallel** (N workers + rate-limit + exponential-backoff retry + JSONL checkpoint file for resume). Uses NIV 2011 (ID 111) for English and CSB 中文標準譯本 (ID 312) for Chinese by default.
- **Spec-Driven Crawling** (HTML path): Per-language verse counts are sourced from `bible_books_zh.json` / `bible_books_en.json`. The crawler never hard-codes verse numbers — all limits come from the JSON spec files.
- **Three-Stage Workflow** (HTML path):
  - **Stage 0 — Spec Builder**: Crawls every chapter in both languages to discover actual verse counts, then writes the two JSON spec files. Run once (or whenever you need to refresh the spec).
  - **Stage 1 — Book Setup**: Writes all book names from the JSON spec directly to the DB (no HTTP needed). The number of books is determined entirely by the spec files generated in Stage 0.
  - **Stage 2 — Chapter & Verse Crawl**: Asynchronously fetches 1,189 chapters × 2 languages and persists verses, bounded by each language's spec verse count.
- **Versification-Aware**: Chinese 和合本 and English BBE differ in chapter boundary placement for several books (e.g. Leviticus, Zechariah). The spec files capture the correct verse count per language so the crawler never writes out-of-range verse rows.
- **Idempotent Writes**: Every DB write uses a `SELECT → INSERT → SELECT` pattern (race-condition safe for concurrent goroutines). Re-running the crawler never creates duplicates.
- **Robust Encoding**: Automatically decodes **Big5** (Chinese pages) to UTF-8 before parsing.
- **Fully Configurable**: Source URLs, concurrency, delays, and HTTP timeout are all set via `.env` — no recompile needed when the website changes.

## 🧪 Testing

The project ships with a full two-tier test suite covering every internal package.

### Quick Reference

| Goal | Command |
|------|---------|
| Run all unit tests (fast, no Docker) | `go test ./...` |
| Run unit tests — verbose, no cache | `go test -count=1 -v -parallel=4 ./...` |
| Run all tests (unit + integration, needs Docker) | `go test -tags integration ./... -timeout 300s` |
| Collect coverage + view summary | `go test -tags integration ./internal/... -coverprofile=coverage.out -covermode=atomic -timeout 300s && go tool cover -func=coverage.out` |
| Open visual HTML coverage report | `go tool cover -html=coverage.out -o coverage.html && open coverage.html` |

### Coverage by Package

| Package | Coverage | Test tier |
|---------|----------|-----------|
| `internal/config` | **100 %** | unit |
| `internal/repository` | **100 %** | unit + integration |
| `internal/spec` | **100 %** | unit |
| `internal/youversion` | **100 %** | unit |
| `internal/scraper` | **96 %** | unit + integration |
| `internal/utils` | **83 %** | unit |

> **Note on remaining gaps:** three defensive error branches in `crawlChapters` and one in `Big5ToUTF8` are genuinely unreachable — `bytes.NewReader` + the lenient Big5 decoder never error; `goquery.NewDocumentFromReader` on a `strings.Reader` never errors; and `parseChapterContext` context is always valid when a request is queued. They exist as safety nets and are intentionally left in.

---

### Understanding the Three Key Commands

This section breaks down the three commands developers most commonly use so you understand exactly what each flag does.

#### 1. `go test -count=1 -v -parallel=4 ./... && echo PASSla~~ || echo FAILla~~`

A developer-friendly command for running all **unit** tests with full visibility.

| Part | What it does |
|------|--------------|
| `-count=1` | **Disable caching.** Go caches passing test results — if code hasn't changed, it shows `(cached)` instead of re-running. `-count=1` forces a fresh run every time, ensuring you see real results. |
| `-v` | **Verbose output.** Prints every `--- PASS: TestFoo (0.00s)` line as tests run. Without this, only the final per-package `ok` / `FAIL` line appears. |
| `-parallel=4` | **Concurrent test execution.** Allows up to 4 test functions to run simultaneously — but only tests that explicitly call `t.Parallel()` opt in to this. The default is `GOMAXPROCS` (number of CPU cores). |
| `./...` | **All packages recursively** from the current directory. |
| `&& echo PASSla~~` | Shell short-circuit: if `go test` exits with code `0` (all pass), print this message. |
| `\|\| echo FAILla~~` | If `go test` exits with any non-zero code (failure), print this instead. |

> **Does NOT include** integration tests (no `-tags integration`), does **not** produce a coverage file.  
> **When to use:** everyday development and CI smoke checks — maximum visibility, reliable uncached results, no Docker required.

---

#### 2. `go test -tags integration ./internal/... -coverprofile=coverage.out -covermode=atomic -timeout 300s`

The definitive **coverage run** — compiles integration test files alongside unit tests and records every line executed.

| Flag | What it does |
|------|--------------|
| `-tags integration` | **Enable the `integration` build tag.** Files guarded with `//go:build integration` (e.g. `*_integration_test.go`, `internal/testhelper/postgres.go`) are compiled and included. Without this flag, those files are completely ignored by the Go compiler. |
| `./internal/...` | Test only packages under `internal/` — excludes `cmd/` (no test files) so those lines don't skew the coverage total. |
| `-coverprofile=coverage.out` | **Write raw coverage data to a file.** Records which source-code statements were executed during the run. This file is consumed by `go tool cover` in the next step. |
| `-covermode=atomic` | **Thread-safe coverage counters.** Three modes exist: `set` (was a line hit — boolean), `count` (how many times), `atomic` (same as `count` but uses CPU atomic ops, preventing data races inside the coverage counters themselves). **Always use `atomic` when tests run in parallel.** |
| `-timeout 300s` | **Global deadline.** Kills the run and marks it failed if it hasn't finished in 5 minutes. The default is 10 minutes (`10m0s`). Integration tests that start Docker containers need more time, so an explicit value prevents silent CI timeouts. |

> **Requires Docker** (Testcontainers starts a `postgres:18` container).  
> **Output:** produces `coverage.out` — a data file, not a human report. Use `go tool cover` to read it.

---

#### 3. `go tool cover -func=coverage.out`

Reads the raw `coverage.out` file and prints a human-readable per-function breakdown.

```text
bible-crawler/internal/config/config.go:10:   Load            100.0%
bible-crawler/internal/repository/...  :42:   UpsertBook      100.0%
bible-crawler/internal/scraper/...     :87:   crawlChapters    92.3%
...
total:                                         (statements)     96.1%
```

| Subcommand option | What it does |
|-------------------|--------------|
| `-func=coverage.out` | Print per-function coverage percentages to stdout (text, best for terminal). |
| `-html=coverage.out` | Generate an interactive HTML report — green = covered, red = not covered. |
| `-html=coverage.out -o coverage.html` | Write the HTML to a named file instead of auto-opening a browser. |

> **Tip:** combine both for maximum insight:
> ```bash
> go tool cover -func=coverage.out            # quick terminal summary
> go tool cover -html=coverage.out -o coverage.html && open coverage.html   # visual drill-down
> ```

---

### Complete `go test` Flag Reference

#### Package patterns

```bash
go test ./...                   # all packages recursively (most common)
go test ./internal/...          # only packages under internal/
go test ./internal/repository   # one specific package
go test .                       # current directory only
```

#### Filtering tests by name

```bash
# -run accepts a regexp matched against test function (and sub-test) names
go test -v -run TestRepository ./internal/repository      # all tests whose name contains "TestRepository"
go test -v -run TestUpsertBook/success ./internal/...     # a specific sub-test
go test -v -run ^TestLoad$ ./internal/spec                # exact match only
```

#### Execution control flags

| Flag | Default | Description |
|------|---------|-------------|
| `-v` | off | Verbose — print each test name and its elapsed time |
| `-count=N` | 1 (cached) | Run each test N times; `-count=1` disables result caching |
| `-parallel=N` | GOMAXPROCS | Max concurrent `t.Parallel()` tests per package |
| `-timeout <dur>` | `10m0s` | Kill the run if it exceeds this duration (e.g. `300s`, `5m`) |
| `-failfast` | off | Stop immediately after the first failure |
| `-short` | off | Signal tests to skip slow paths (`testing.Short()`) |
| `-race` | off | Enable Go's built-in data race detector (always use in CI) |

#### Build tags

```bash
# Enable a build tag — compiles files with //go:build <tag>
go test -tags integration ./...

# Multiple tags (comma-separated)
go test -tags "integration slow" ./...
```

#### Coverage flags

| Flag | Description |
|------|-------------|
| `-cover` | Print a coverage % per package (no output file produced) |
| `-coverprofile=<file>` | Write coverage data to file for later analysis |
| `-covermode=set` | Boolean per statement — was it hit? |
| `-covermode=count` | Integer per statement — how many times was it hit? |
| `-covermode=atomic` | Like `count`, but thread-safe (**required for parallel tests**) |

#### Benchmarks

```bash
go test -bench=. ./...                       # run all benchmarks
go test -bench=BenchmarkParse -benchmem ./... # with memory allocation stats
go test -bench=. -benchtime=5s ./...         # each benchmark runs for 5 s
```

#### Practical recipes

```bash
# Everyday check — all unit tests, no cache, verbose
go test -count=1 -v ./...

# Fastest possible check (skip anything marked testing.Short)
go test -short ./...

# CI-grade unit test run (race detector + coverage %, no Docker)
go test -race -cover -count=1 ./...

# Full coverage report — both tiers (needs Docker)
go test -tags integration -race \
  -coverprofile=coverage.out -covermode=atomic \
  ./internal/... -timeout 300s
go tool cover -func=coverage.out

# Run exactly one test and see all its output
go test -v -count=1 -run ^TestUpsertBook$ ./internal/repository

# JSON output for CI dashboards / parsers
go test -json ./... | jq '.Output' -r
```

---

### Test Architecture

| Layer | Build tag | Dependencies | Files |
|-------|-----------|--------------|-------|
| Unit | _(none)_ | `go-sqlmock`, `testify`, `httptest` | `*_test.go` (no integration suffix) |
| Integration | `integration` | Docker + Testcontainers (PostgreSQL 16) | `*_integration_test.go`, `testhelper/postgres.go` |

Integration tests additionally cover:
- `internal/database` — `Connect` success path and `log.Fatalf` exit-on-bad-URL via subprocess test
- `internal/repository` — full round-trip idempotency with a real PostgreSQL 16 database
- `internal/scraper` — end-to-end `Run()` against a `httptest.NewServer` + real database

### Test Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/DATA-DOG/go-sqlmock` | SQL mock driver for unit tests (no real DB needed) |
| `github.com/stretchr/testify` | Assertion helpers (`assert`, `require`) |
| `github.com/testcontainers/testcontainers-go` | Spin up Docker containers inside tests |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | Pre-built PostgreSQL container module |

---

## 🛠 Prerequisites

- **Go** 1.21 or higher
- **PostgreSQL** 13 or higher
- **Git**

## 📐 Database Schema

The crawler writes to the `bibles` schema. Six tables, three structural and three content:

| Table | Purpose |
|-------|---------|
| `bibles.bible_books` | One row per book (sort order driven by spec) |
| `bibles.bible_book_contents` | Localized book title (chinese / english) |
| `bibles.bible_chapters` | One row per chapter within a book |
| `bibles.bible_chapter_contents` | Localized chapter title |
| `bibles.bible_sections` | One row per verse within a chapter |
| `bibles.bible_section_contents` | Localized verse title + content |

Run the DDL from your project documentation (or `db/schema.sql`) before the first crawl.

## 🚀 Standard Operating Procedure (SOP)

### Step 1: Clone & Install Dependencies

```bash
git clone https://github.com/your-username/bible-crawler.git
cd bible-crawler
go mod tidy
```

### Step 2: Database Initialization

1. Log in to PostgreSQL and create a database (e.g. `topchurch_dev`).
2. Execute the DDL script to create the `bibles` schema and all six tables.

### Step 3: Configure Environment

Copy `.env.example` to `.env` and fill in your credentials and settings:

```ini
# ── Database ──────────────────────────────────────────────────────────────────
APP_ENV=development
DATABASE_URL=postgres://username:password@localhost:5432/topchurch_dev?sslmode=disable

# ── Source website (HTML scraper only) ────────────────────────────────────────
# Update these three values if the website migrates or you switch Bible translations.
# SOURCE_ZH_URL and SOURCE_EN_URL must each contain one %d placeholder for the
# global chapter index (1-based, sequential across all books).
SOURCE_DOMAIN=springbible.fhl.net
SOURCE_ZH_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0
SOURCE_EN_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe

# ── Crawler tuning (HTML scraper only) ────────────────────────────────────────
# Lower CRAWLER_PARALLELISM or raise *_DELAY_MS if the server starts throttling.
CRAWLER_PARALLELISM=5
CRAWLER_DELAY_MS=200
CRAWLER_RANDOM_DELAY_MS=100
HTTP_TIMEOUT_SEC=10

# ── YouVersion Platform API (youversion-crawler only) ─────────────────────────
# Obtain an app key at https://platform.youversion.com
YOUVERSION_API_KEY=your-app-key-here
YOUVERSION_BASE_URL=https://api.youversion.com/v1   # optional, this is the default
# Default Bible IDs: 111 = NIV 2011 (English), 312 = CSB 中文標準譯本 (Chinese Standard Bible, traditional Chinese).
# To use a different Chinese translation, set YOUVERSION_CHINESE_BIBLE_ID to the desired Bible ID.
YOUVERSION_ENGLISH_BIBLE_ID=111
YOUVERSION_CHINESE_BIBLE_ID=312

# ── YouVersion Parallel Mode (optional) ────────────────────────────────────────
# Set YOUVERSION_CHECKPOINT_FILE to enable parallel crawl mode.
# Leave empty (or unset) to use sequential mode (original direct-DB behaviour).
YOUVERSION_CHECKPOINT_FILE=verses.jsonl  # set a file path to enable parallel mode
YOUVERSION_WORKERS=20              # parallel goroutines — match or exceed RPS
YOUVERSION_RATE_LIMIT_RPS=15.0    # requests/sec, token-bucket (≥15 to finish ~62k verses in 1h)
YOUVERSION_MAX_RETRIES=3           # max retries on 5xx/network errors (default: 3)
YOUVERSION_RETRY_BASE_MS=500       # initial backoff in ms, doubles each retry (default: 500)
```

### Step 4: Build Spec Files (first time, or to refresh)

This step crawls every chapter page in both languages (≈ 2,378 requests) and writes accurate per-language verse counts to the two JSON spec files. **Must be run before the crawler on a fresh setup.**

```bash
go run cmd/spec-builder/main.go
```

Expected duration: **5–10 minutes** (5 concurrent workers).

Expected output:
```text
Spec-builder starting: N HTTP requests (M chapters × 2 languages).
Progress: 200/N requests done (0 errors)
...
Writing ZH (和合本): total_verses=XXXXX (OT=XXXXX NT=XXXXX)
Writing EN (BBE): total_verses=XXXXX (OT=XXXXX NT=XXXXX)
Done. Written:
  /path/to/bible_books_zh.json
  /path/to/bible_books_en.json
```

> After this step, `bible_books_zh.json` and `bible_books_en.json` will have **different** verse counts for chapters where the two translations draw boundaries differently.

### Step 5: Run the Crawler

Choose **one** of the two crawler approaches:

#### Option A — HTML Scraper (原 springbible.fhl.net)

Requires Step 4 (spec files must already exist). Fetches content from the HTML website.

```bash
go run cmd/crawler/main.go
```

Expected output:
```text
Connected to database successfully
Starting Bible Crawler...
Phase 1: Setting up Books from spec...
Phase 1 complete: N books ready.
Phase 2: Crawling Chapters...
Phase 2 complete.
Bible Crawler finished successfully.
```

#### Option B — YouVersion API Crawler ✨

No spec files needed. Fetches content directly from the YouVersion REST API.
Requires `YOUVERSION_API_KEY` to be set in `.env`.

The YouVersion crawler offers **two sub-modes** for Phase 2 (verse fetching):

---

##### B-1. Sequential Mode (default — no extra config needed)

Writes verses directly to the database one-by-one. Safe and simple; requires only the core env vars.

```bash
go run cmd/youversion-crawler/main.go
```

Expected output:
```text
Connected to database successfully
Starting YouVersion Bible Crawler...
YouVersion Scraper: starting...
Phase 1: fetching book list for English Bible (ID 111)...
Phase 1: fetching book list for Chinese Bible (ID 312)...
Phase 1: 66/66 books ready.
Phase 2: crawling verses...
Phase 2: language=english bibleID=111
Phase 2: language=chinese bibleID=312
Phase 2: done.
YouVersion Scraper: done.
YouVersion Crawler finished successfully.
```

---

##### B-2. Parallel Mode (recommended — set `YOUVERSION_CHECKPOINT_FILE` in `.env`)

Uses a worker pool with token-bucket rate limiting and exponential-backoff retry. Results are written to a JSONL checkpoint file instead of the database, enabling **graceful resume** after interruption.

**Phase 2A — Crawl to JSONL:**

Make sure `.env` has the parallel-mode variables set (see Step 3), then:

```bash
go run cmd/youversion-crawler/main.go
```

Expected output:
```text
Connected to database successfully
Parallel mode: workers=20 rps=15.0 maxRetries=3 checkpoint="verses.jsonl"
Starting YouVersion Bible Crawler...
YouVersion Scraper: starting...
Phase 1: 66/66 books ready.
Phase 2: crawling verses (parallel)...
Phase 2: 0 verses already in checkpoint
Phase 2: 62213/62213 verses remaining
Phase 2: done. written=62197 already-done=0 write-errors=0
YouVersion Scraper: done.
YouVersion Crawler finished successfully.
```

> **Graceful shutdown**: Press `Ctrl+C` at any time. Workers finish their current verse, flush the checkpoint file, and exit cleanly. Re-run the same command to resume — already-fetched verses are automatically skipped.

> **404 responses** (e.g. `MAT.17.21`, `MAT.18.11`) are **expected** and silently skipped. Modern translations like NIV deliberately omit certain verses. These are not errors.

**Phase 2B — Import JSONL to database:**

```bash
go run cmd/youversion-importer/main.go
```

Expected output:
```text
Import complete: total=62197 written=62197 skipped=0
```

> The importer reads `YOUVERSION_CHECKPOINT_FILE` from `.env` automatically — no inline env vars needed. It is fully idempotent (safe to run multiple times) and uses the same `SELECT→INSERT→SELECT` pattern as all other repository writes.

---

> **Bible version note**: Option B defaults to NIV 2011 (ID 111) for English and CSB 中文標準譯本 (ID 312, Chinese Standard Bible, traditional Chinese) for Chinese — both freely accessible with a YouVersion Platform app key. To use a different Chinese translation, set `YOUVERSION_CHINESE_BIBLE_ID=<id>` in `.env`.

### Step 6: Validate (optional)

---

## 🔄 Re-crawl Procedure (Protecting Existing Cross-Schema References)

Use this procedure **instead of Step 5** whenever you need to TRUNCATE and re-populate the bibles schema in an environment that already contains data referencing `bibles.bible_sections` from other microservice schemas.

> **Background**: Three tables store `bibles.bible_sections(id)` as a plain UUID without a declared FK constraint, so `TRUNCATE … CASCADE` does not reach them. After re-crawl every UUID changes, leaving these columns with stale references:
>
> | Table | Column |
> |---|---|
> | `activities.general_bibles` | `bible_id` |
> | `activities.general_template_bibles` | `bible_id` |
> | `devotions.devotion_bibles` | `bible_section_id` |

### Step A — Backup (before TRUNCATE)

```bash
# Captures (book_sort, chapter_sort, section_sort) for every referenced verse,
# then truncates all 6 bibles tables immediately after.
go run cmd/migrate/main.go --phase=backup --truncate
```

> Omit `--truncate` if you prefer to run `TRUNCATE TABLE bibles.bible_books CASCADE;` manually.

### Step B — Rebuild Spec (optional)

Only needed if you want to refresh verse counts from the source website:

```bash
go run cmd/spec-builder/main.go
```

### Step C — Re-crawl

```bash
go run cmd/crawler/main.go
```

### Step D — Restore (after re-crawl)

```bash
# Updates the 3 cross-schema tables to point at the new UUIDs,
# verifies orphan counts (should all be 0), then drops the backup table.
go run cmd/migrate/main.go --phase=restore --cleanup
```

Expected output:
```text
Phase: restore — updating cross-schema bible references with new UUIDs...
Restore complete:
  activities.general_bibles updated:          N rows
  activities.general_template_bibles updated: N rows
  devotions.devotion_bibles updated:           N rows
  Total:                                       N rows
Verifying orphan counts...
Orphan check:
  activities.general_bibles:          0
  activities.general_template_bibles: 0
  devotions.devotion_bibles:           0
All cross-schema references are valid.
Cleaning up backup table...
Backup table dropped.
```

> If `WARNING: N orphan reference(s) remain` appears, omit `--cleanup` and investigate before dropping the backup table.

---

Run `validation.sql` (at the project root) against your PostgreSQL database. The queries check all three levels (books, chapters, verses). Results should return **0 rows** for Sections 1–3. Section 5 will list versification-difference chapters — that is expected and normal.

## 📂 Project Structure

```text
bible-crawler/
├── cmd/
│   ├── crawler/
│   │   └── main.go               # HTML crawl entry point (Stages 1 + 2)
│   ├── migrate/
│   │   └── main.go               # Cross-schema UUID backup/restore (--phase=backup|restore)
│   ├── spec-builder/
│   │   └── main.go               # Stage 0: discovers verse counts, writes JSON spec files
│   ├── youversion-crawler/
│   │   └── main.go               # YouVersion API crawl: Phase 1 (DB setup) + Phase 2 (verse fetch)
│   └── youversion-importer/
│       └── main.go               # Reads JSONL checkpoint → batch-writes verses to PostgreSQL
├── internal/
│   ├── config/                   # Environment variable loader (all .env fields)
│   │   └── config_test.go        # Unit tests — 100 % coverage
│   ├── database/                 # PostgreSQL connection setup
│   │   └── database_test.go      # Integration tests (build tag: integration)
│   ├── migration/                # Backup/restore logic for cross-schema bible refs
│   │   └── migration_test.go     # Unit tests with go-sqlmock — 100 % coverage
│   ├── model/                    # Go structs for DB tables
│   ├── repository/               # Idempotent data-access layer (all SQL)
│   │   ├── repository_test.go             # Unit tests with go-sqlmock — 100 % coverage
│   │   └── repository_integration_test.go # Integration tests (build tag: integration)
│   ├── scraper/                  # Colly-based HTML crawl orchestration
│   │   ├── scraper_test.go             # Unit tests — 96 % coverage
│   │   └── scraper_integration_test.go  # Integration tests (build tag: integration)
│   ├── spec/                     # JSON spec loader (BibleSpec, BookSpec)
│   │   └── spec_test.go          # Unit tests — 100 % coverage
│   ├── testhelper/               # Shared Testcontainers helper (integration build tag only)
│   │   └── postgres.go
│   ├── utils/                    # Big5→UTF-8 decoder, text cleaner
│   │   └── encoding_test.go      # Unit tests — 83 % coverage
│   └── youversion/               # YouVersion Platform API client + scraper
│       ├── checkpoint.go         # JSONL checkpoint: VerseRecord, Append, LoadCompleted
│       ├── client.go             # HTTP client (GetBooks, GetPassage, …)
│       ├── client_test.go        # Unit tests — 100 % coverage
│       ├── scraper.go            # Orchestrator: setupBooks, crawlVerses, crawlVersesParallel
│       ├── scraper_test.go       # Unit tests — 100 % coverage
│       └── types.go              # API response types (BooksResponse, PassageData, …)
├── bible_books_zh.json           # Per-chapter verse counts for 和合本 (auto-generated)
├── bible_books_en.json           # Per-chapter verse counts for BBE    (auto-generated)
├── validation.sql                # PostgreSQL validation + bilingual chapter viewer queries
├── .env                          # Local DB credentials and settings (not committed)
├── .env.example                  # Template for .env (all fields documented)
├── go.mod
└── README.md
```

> **Note**: compiled binaries are listed in `.gitignore` and must not be committed. Always run via `go run cmd/<name>/main.go`.

## ⚠️ Troubleshooting

**Q: "pq: password authentication failed for user..."**  
A: Check `DATABASE_URL` in your `.env` file.

**Q: "dial tcp [::1]:5432: connect: connection refused"**  
A: Ensure PostgreSQL is running and listening on port 5432.

**Q: Validation SQL returns missing rows.**  
A: Section 5 of `validation.sql` lists versification-difference chapters — these are expected. Sections 1–3 should all return 0 rows after a complete crawl. If they do not, re-run `cmd/crawler` (it is fully idempotent).

**Q: Data looks garbled (mojibake).**  
A: The crawler decodes Big5 pages before parsing. Do not modify `internal/utils/encoding.go`.

**Q: YouVersion crawler logs many "status 404" errors.**  
A: 404 responses are **expected and normal** for certain verses that modern translations deliberately omit. For example, NIV omits MAT.17.21, MAT.18.11, MRK.7.16, etc. These are silently skipped in both sequential and parallel modes. Only 429 or 5xx responses trigger retries.

**Q: `bible_books_zh.json` and `bible_books_en.json` have identical verse counts.**  
A: Re-run `cmd/spec-builder/main.go`. The spec files must be generated from the live website to capture per-language versification differences.

## 📜 License

This project is for educational and personal use. Please respect the copyright of the source website content.
