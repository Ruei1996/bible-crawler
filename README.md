# Bible Crawler

A high-performance, concurrent web crawler written in Go. It scrapes Bible content (Chinese Union Version 和合本 & Basic English Version BBE) from `springbible.fhl.net` and populates a PostgreSQL database with a strict, normalized schema.

## 🌟 Features

- **Spec-Driven Crawling**: Per-language verse counts are sourced from `bible_books_zh.json` / `bible_books_en.json`. The crawler never hard-codes verse numbers — all limits come from the JSON spec files.
- **Three-Stage Workflow**:
  - **Stage 0 — Spec Builder**: Crawls every chapter in both languages to discover actual verse counts, then writes the two JSON spec files. Run once (or whenever you need to refresh the spec).
  - **Stage 1 — Book Setup**: Writes all 66 book names from the JSON spec directly to the DB (no HTTP needed).
  - **Stage 2 — Chapter & Verse Crawl**: Asynchronously fetches 1,189 chapters × 2 languages and persists verses, bounded by each language's spec verse count.
- **Versification-Aware**: Chinese 和合本 and English BBE differ in chapter boundary placement for several books (e.g. Leviticus, Zechariah). The spec files capture the correct verse count per language, and the repair tool writes readable placeholders where a verse position exists in one translation but not the other.
- **Idempotent Writes**: Every DB write uses a `SELECT → INSERT → SELECT` pattern (race-condition safe for concurrent goroutines). Re-running the crawler never creates duplicates.
- **Robust Encoding**: Automatically decodes **Big5** (Chinese pages) to UTF-8 before parsing.
- **Fully Configurable**: Source URLs, concurrency, delays, and HTTP timeout are all set via `.env` — no recompile needed when the website changes.
- **Repair Tool**: After the main crawl, run `cmd/repair` to patch any chapters missed due to transient HTTP errors.

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

### Step 6: Repair Any Missed Data (optional)

If transient network errors caused any chapters or verses to be skipped, run the repair tool. It queries the DB for missing entries and re-fetches only those pages. It also writes versification-difference placeholders for verse positions that genuinely do not exist in one translation.

```bash
go run cmd/repair/main.go
```

Expected output when everything is already complete:
```text
No missing chapters found — nothing to repair.
```

### Step 7: Validate (optional)

Run `validation.sql` (at the project root) against your PostgreSQL database. The queries check all three levels (books, chapters, verses). Results should return **0 rows** for Sections 1–3. Section 5 will list versification-difference chapters — that is expected and normal.

## 📂 Project Structure

```text
bible-crawler/
├── cmd/
│   ├── crawler/
│   │   └── main.go           # Main crawl entry point (Stages 1 + 2)
│   ├── spec-builder/
│   │   └── main.go           # Stage 0: discovers verse counts, writes JSON spec files
│   └── repair/
│       └── main.go           # Patches missed chapters/verses after a crawl
├── internal/
│   ├── config/               # Environment variable loader (all .env fields)
│   ├── database/             # PostgreSQL connection setup
│   ├── model/                # Go structs for DB tables
│   ├── repository/           # Idempotent data-access layer (all SQL)
│   ├── scraper/              # Colly-based crawl orchestration
│   ├── spec/                 # JSON spec loader (BibleSpec, BookSpec)
│   └── utils/                # Big5→UTF-8 decoder, text cleaner
├── bible_books_zh.json       # Per-chapter verse counts for 和合本 (auto-generated by spec-builder)
├── bible_books_en.json       # Per-chapter verse counts for BBE    (auto-generated by spec-builder)
├── validation.sql            # PostgreSQL validation + bilingual chapter viewer queries
├── .env                      # Local DB credentials and settings (not committed)
├── .env.example              # Template for .env (all fields documented)
├── go.mod
└── README.md
```

> **Note**: compiled binaries (`spec-builder`, `crawler`, `repair`) are listed in `.gitignore` and must not be committed. Always run via `go run cmd/<name>/main.go`.

## ⚠️ Troubleshooting

**Q: "pq: password authentication failed for user..."**  
A: Check `DATABASE_URL` in your `.env` file.

**Q: "dial tcp [::1]:5432: connect: connection refused"**  
A: Ensure PostgreSQL is running and listening on port 5432.

**Q: Validation SQL still returns missing rows after repair.**  
A: The repair tool writes placeholders for versification-difference verses automatically. If rows remain, re-run `cmd/repair` — it is fully idempotent.

**Q: Data looks garbled (mojibake).**  
A: The crawler decodes Big5 pages before parsing. Do not modify `internal/utils/encoding.go`.

**Q: `bible_books_zh.json` and `bible_books_en.json` have identical verse counts.**  
A: Re-run `cmd/spec-builder/main.go`. The spec files must be generated from the live website to capture per-language versification differences.

## 📜 License

This project is for educational and personal use. Please respect the copyright of the source website content.
