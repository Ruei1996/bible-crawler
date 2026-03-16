# Bible Crawler

A high-performance, concurrent web crawler written in Go. It scrapes Bible content (Chinese Union Version 和合本 & Basic English Version BBE) from `springbible.fhl.net` and populates a PostgreSQL database with a strict, normalized schema.

## 🌟 Features

- **Spec-Driven Crawling**: Per-language verse counts are sourced from `bible_books_zh.json` / `bible_books_en.json`. The crawler never hard-codes verse numbers — all limits come from the JSON spec files.
- **Three-Stage Workflow**:
  - **Stage 0 — Spec Builder**: Crawls every chapter in both languages to discover actual verse counts, then writes the two JSON spec files. Run once (or whenever you need to refresh the spec).
  - **Stage 1 — Book Setup**: Writes all 66 book names from the JSON spec directly to the DB (no HTTP needed).
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

> **Requires Docker** (Testcontainers starts a `postgres:16-alpine` container).  
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
| `bibles.bible_books` | One row per book (sort 1–66) |
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

# ── Source website ────────────────────────────────────────────────────────────
# Update these three values if the website migrates or you switch Bible translations.
# SOURCE_ZH_URL and SOURCE_EN_URL must each contain one %d placeholder for the
# global chapter index (1-based, sequential across all 66 books).
SOURCE_DOMAIN=springbible.fhl.net
SOURCE_ZH_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0
SOURCE_EN_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe

# ── Crawler tuning ────────────────────────────────────────────────────────────
# Lower CRAWLER_PARALLELISM or raise *_DELAY_MS if the server starts throttling.
CRAWLER_PARALLELISM=5
CRAWLER_DELAY_MS=200
CRAWLER_RANDOM_DELAY_MS=100
HTTP_TIMEOUT_SEC=30
```

> **Tip — Changing the source website**: if `springbible.fhl.net` ever moves or you want to target a different Chinese / English Bible translation, update `SOURCE_DOMAIN`, `SOURCE_ZH_URL`, and `SOURCE_EN_URL` in `.env`, then re-run `cmd/spec-builder` to regenerate the JSON spec files before running the crawler.

### Step 4: Build Spec Files (first time, or to refresh)

This step crawls every chapter page in both languages (≈ 2,378 requests) and writes accurate per-language verse counts to the two JSON spec files. **Must be run before the crawler on a fresh setup.**

```bash
go run cmd/spec-builder/main.go
```

Expected duration: **5–10 minutes** (5 concurrent workers).

Expected output:
```text
Spec-builder starting: 2378 HTTP requests (1189 chapters × 2 languages).
Progress: 200/2378 requests done (0 errors)
...
Writing ZH (和合本): total_verses=31102 (OT=23145 NT=7957)
Writing EN (BBE): total_verses=31173 (OT=23214 NT=7959)
Done. Written:
  /path/to/bible_books_zh.json
  /path/to/bible_books_en.json
```

> After this step, `bible_books_zh.json` and `bible_books_en.json` will have **different** verse counts for chapters where the two translations draw boundaries differently.

### Step 5: Run the Main Crawler

```bash
go run cmd/crawler/main.go
```

Expected output:
```text
Connected to database successfully
Starting Bible Crawler...
Phase 1: Setting up Books from spec...
Phase 1 complete: 66 books ready.
Phase 2: Crawling Chapters...
Phase 2 complete.
Bible Crawler finished successfully.
```

### Step 6: Validate (optional)

Run `validation.sql` (at the project root) against your PostgreSQL database. The queries check all three levels (books, chapters, verses). Results should return **0 rows** for Sections 1–3. Section 5 will list versification-difference chapters — that is expected and normal.

## 📂 Project Structure

```text
bible-crawler/
├── cmd/
│   ├── crawler/
│   │   └── main.go           # Main crawl entry point (Stages 1 + 2)
│   └── spec-builder/
│       └── main.go           # Stage 0: discovers verse counts, writes JSON spec files
├── internal/
│   ├── config/               # Environment variable loader (all .env fields)
│   │   └── config_test.go    # Unit tests — 100 % coverage
│   ├── database/             # PostgreSQL connection setup
│   │   └── database_test.go  # Integration tests (build tag: integration)
│   ├── model/                # Go structs for DB tables
│   ├── repository/           # Idempotent data-access layer (all SQL)
│   │   ├── repository_test.go             # Unit tests with go-sqlmock — 100 % coverage
│   │   └── repository_integration_test.go # Integration tests (build tag: integration)
│   ├── scraper/              # Colly-based crawl orchestration
│   │   ├── scraper_test.go             # Unit tests — 96 % coverage
│   │   └── scraper_integration_test.go  # Integration tests (build tag: integration)
│   ├── spec/                 # JSON spec loader (BibleSpec, BookSpec)
│   │   └── spec_test.go      # Unit tests — 100 % coverage
│   ├── testhelper/           # Shared Testcontainers helper (integration build tag only)
│   │   └── postgres.go
│   └── utils/                # Big5→UTF-8 decoder, text cleaner
│       └── encoding_test.go  # Unit tests — 83 % coverage
├── bible_books_zh.json       # Per-chapter verse counts for 和合本 (auto-generated by spec-builder)
├── bible_books_en.json       # Per-chapter verse counts for BBE    (auto-generated by spec-builder)
├── validation.sql            # PostgreSQL validation + bilingual chapter viewer queries
├── .env                      # Local DB credentials and settings (not committed)
├── .env.example              # Template for .env (all fields documented)
├── go.mod
└── README.md
```

> **Note**: compiled binaries (`spec-builder`, `crawler`) are listed in `.gitignore` and must not be committed. Always run via `go run cmd/<name>/main.go`.

## ⚠️ Troubleshooting

**Q: "pq: password authentication failed for user..."**  
A: Check `DATABASE_URL` in your `.env` file.

**Q: "dial tcp [::1]:5432: connect: connection refused"**  
A: Ensure PostgreSQL is running and listening on port 5432.

**Q: Validation SQL returns missing rows.**  
A: Section 5 of `validation.sql` lists versification-difference chapters — these are expected. Sections 1–3 should all return 0 rows after a complete crawl. If they do not, re-run `cmd/crawler` (it is fully idempotent).

**Q: Data looks garbled (mojibake).**  
A: The crawler decodes Big5 pages before parsing. Do not modify `internal/utils/encoding.go`.

**Q: `bible_books_zh.json` and `bible_books_en.json` have identical verse counts.**  
A: Re-run `cmd/spec-builder/main.go`. The spec files must be generated from the live website to capture per-language versification differences.

## 📜 License

This project is for educational and personal use. Please respect the copyright of the source website content.
