# Bible Crawler（聖經爬蟲系統）

這是一個 Go 語言聖經爬蟲，將聖經內容存入結構嚴謹的 PostgreSQL 資料庫。支援**三種資料來源**，寫入完全相同的資料表 Schema，可互換使用：

| 資料來源 | 指令 | 翻譯版本 | 輸出 |
|----------|------|----------|------|
| `springbible.fhl.net` HTML | `cmd/crawler` | 和合本 CUV + BBE | 直接 → PostgreSQL |
| [YouVersion Platform API](https://api.youversion.com/v1) | `cmd/youversion-crawler` + `cmd/youversion-importer` | CSB 中文標準譯本 (ID 312) + NIV 2011 (ID 111) | JSONL 斷點檔 → PostgreSQL |
| `bible.com` HTML | `cmd/biblecom-crawler` + `cmd/biblecom-importer` | CUNP-上帝 (ID 414) + NIV (ID 111) | JSON 檔案 → PostgreSQL |

## 🌟 功能特色

- **三種資料來源支援**：HTML 爬蟲、YouVersion API 爬蟲與 bible.com HTML 爬蟲，輸出至相同 Schema，資料格式完全一致。
- **Spec 驅動爬取**（HTML 路徑）：各章節的實際節數從 `bible_books_zh.json`（和合本）與 `bible_books_en.json`（BBE）讀取，程式碼零硬寫數字，完全由 JSON 規格控制。
- **三階段工作流程**（HTML 路徑）:
  - **Stage 0 — Spec Builder**：爬取每個章節（兩語言），發現實際節數，寫入兩份 JSON 規格檔。首次建置或需更新規格時執行。
  - **Stage 1 — 書本設定**：直接從 JSON 規格寫入所有書卷的中英文書名（不需 HTTP 請求）。書卷總數完全由 Stage 0 產生的規格檔決定。
  - **Stage 2 — 章節與經文爬取**：非同步併發爬取 1,189 個章節 × 2 語言，依各語言的規格節數上限存入資料庫。
- **版本節數差異處理（Versification-Aware）**：和合本與 BBE 在部分書卷（例如利未記、撒迦利亞書）的章節邊界不同。Spec 檔案記錄各語言的正確節數，爬蟲自動依上限存入，不會寫入超出範圍的節。
- **冪等寫入（Idempotent）**：所有 DB 寫入使用 `SELECT → INSERT → SELECT` 三步驟模式（對併發 goroutine 安全無 race condition）。重複執行爬蟲不會產生重複資料。
- **編碼處理**：自動將 springbible.fhl.net 中文頁面的 **Big5** 編碼轉換為 UTF-8 後再解析。
- **完全可配置**：來源 URL、並行數、延遲與 HTTP 逾時皆透過 `.env` 設定，網站更新時無需重新編譯。
- **跨 Schema 遷移**（`cmd/migrate`）：TRUNCATE `bibles` schema 重新爬取前，備份並還原其他微服務 schema（`activities`、`devotions`）中無宣告 FK 的 UUID 引用。

## 🧪 測試

本專案配備完整的兩層測試套件，涵蓋所有內部套件。

### 快速指令參考

| 目標 | 指令 |
|------|------|
| 執行全部單元測試（快速，無需 Docker） | `go test ./...` |
| 單元測試 — 詳細輸出、禁用快取 | `go test -count=1 -v -parallel=4 ./...` |
| 執行全部測試（單元 + 整合，需要 Docker） | `go test -tags integration ./... -timeout 300s` |
| 收集覆蓋率並顯示摘要 | `go test -tags integration ./internal/... -coverprofile=coverage.out -covermode=atomic -timeout 300s && go tool cover -func=coverage.out` |
| 開啟視覺化 HTML 覆蓋率報告 | `go tool cover -html=coverage.out -o coverage.html && open coverage.html` |

### 各套件覆蓋率

| 套件 | 覆蓋率 | 測試層級 |
|------|--------|----------|
| `internal/config` | **100 %** | 單元測試 |
| `internal/repository` | **100 %** | 單元 + 整合測試 |
| `internal/spec` | **100 %** | 單元測試 |
| `internal/youversion` | **100 %** | 單元測試 |
| `internal/scraper` | **96 %** | 單元 + 整合測試 |
| `internal/utils` | **83 %** | 單元測試 |

> **未覆蓋行說明：** `crawlChapters` 中有三個防禦性錯誤分支、`Big5ToUTF8` 中有一個，這些分支在正常運作下確實無法觸及（`bytes.NewReader` 與寬鬆的 Big5 解碼器不會回傳錯誤；`strings.Reader` 上的 `goquery.NewDocumentFromReader` 也不會出錯；請求佇列時 `parseChapterContext` 的 context 永遠有效）。這些分支作為防禦性備援保留，並已文件化說明。

---

### 三個核心指令詳解

本節逐一拆解開發者最常使用的三個指令，讓你完全理解每個 flag 的作用。

#### 1. `go test -count=1 -v -parallel=4 ./... && echo PASSla~~ || echo FAILla~~`

開發者日常使用、可視性最高的**單元測試**指令：

| 部分 | 作用說明 |
|------|----------|
| `-count=1` | **停用快取。** Go 預設會快取通過的測試結果，若程式碼沒有變動，下次執行會顯示 `(cached)` 而非真正重跑。加上 `-count=1` 可強制每次都全新執行，確保結果真實。 |
| `-v` | **詳細輸出（Verbose）。** 在測試執行過程中逐行印出 `--- PASS: TestFoo (0.00s)`。若不加此 flag，只會顯示每個套件最終的 `ok` 或 `FAIL` 一行。 |
| `-parallel=4` | **並行測試執行。** 允許最多 4 個測試函式同時執行，但只有明確呼叫 `t.Parallel()` 的測試才會參與並行。預設值為 `GOMAXPROCS`（CPU 核心數）。 |
| `./...` | **遞迴比對所有套件**（從當前目錄向下）。 |
| `&& echo PASSla~~` | Shell 短路語法：若 `go test` 以代碼 `0` 結束（全部通過），則印出此訊息。 |
| `\|\| echo FAILla~~` | 若 `go test` 以非零代碼結束（有失敗），則改印此訊息。 |

> **不包含**整合測試（沒有 `-tags integration`），**不會**產生覆蓋率檔案。  
> **適用情境：** 日常開發、CI 快速煙霧測試 — 最高可視性、可靠的非快取結果、不需要 Docker。

---

#### 2. `go test -tags integration ./internal/... -coverprofile=coverage.out -covermode=atomic -timeout 300s`

最完整的**覆蓋率收集**指令 — 同時編譯單元測試與整合測試，並記錄所有被執行到的程式碼行：

| Flag | 作用說明 |
|------|----------|
| `-tags integration` | **啟用 `integration` 建置標籤。** 帶有 `//go:build integration` 的檔案（例如 `*_integration_test.go`、`internal/testhelper/postgres.go`）才會被編譯並納入測試。沒有這個 flag，這些檔案完全被 Go 編譯器忽略。 |
| `./internal/...` | 只測試 `internal/` 下的套件 — 排除沒有測試檔的 `cmd/`，避免這些行數影響覆蓋率計算。 |
| `-coverprofile=coverage.out` | **將原始覆蓋率資料寫入檔案。** 記錄測試執行期間哪些程式碼語句被跑到。此檔案供下一步 `go tool cover` 讀取。 |
| `-covermode=atomic` | **執行緒安全的覆蓋率計數器。** 共有三種模式：`set`（每行是否被執行 — 布林值）、`count`（被執行幾次）、`atomic`（與 `count` 相同，但使用 CPU 原子操作，避免計數器本身產生 race condition）。**只要測試有並行執行，請一律使用 `atomic`。** |
| `-timeout 300s` | **全域執行時限。** 若整個測試套件在 5 分鐘內未完成，Go 會終止所有 goroutine 並將本次執行標記為失敗。預設值為 10 分鐘（`10m0s`）。整合測試需要啟動 Docker 容器，耗時較長，建議明確設定此值以避免 CI 靜默逾時。 |

> **需要 Docker**（Testcontainers 會啟動 `postgres:18` 容器）。  
> **輸出：** 產生 `coverage.out` — 這是資料檔，不是人可直接閱讀的報告。需搭配 `go tool cover` 使用。

---

#### 3. `go tool cover -func=coverage.out`

讀取 `coverage.out` 原始資料，輸出每個函式的覆蓋率百分比摘要：

```text
bible-crawler/internal/config/config.go:10:   Load            100.0%
bible-crawler/internal/repository/...  :42:   UpsertBook      100.0%
bible-crawler/internal/scraper/...     :87:   crawlChapters    92.3%
...
total:                                         (statements)     96.1%
```

| 子指令選項 | 作用說明 |
|------------|----------|
| `-func=coverage.out` | 在終端機印出每函式覆蓋率百分比（文字格式，最適合快速瀏覽）。 |
| `-html=coverage.out` | 產生互動式 HTML 報告 — 綠色 = 已覆蓋，紅色 = 未覆蓋。 |
| `-html=coverage.out -o coverage.html` | 將 HTML 輸出到指定檔案，而非自動開啟瀏覽器。 |

> **小技巧：** 兩個指令搭配使用效果最佳：
> ```bash
> go tool cover -func=coverage.out                                     # 終端快速摘要
> go tool cover -html=coverage.out -o coverage.html && open coverage.html  # 視覺化逐行檢視
> ```

---

### 完整 `go test` Flag 參考手冊

#### 套件選擇模式

```bash
go test ./...                   # 遞迴所有套件（最常用）
go test ./internal/...          # 只有 internal/ 下的套件
go test ./internal/repository   # 指定某一個套件
go test .                       # 只有當前目錄的套件
```

#### 按名稱篩選測試

```bash
# -run 接受 regexp，比對測試函式名稱（及子測試名稱）
go test -v -run TestRepository ./internal/repository      # 名稱含有 "TestRepository" 的所有測試
go test -v -run TestUpsertBook/success ./internal/...     # 指定某個子測試
go test -v -run ^TestLoad$ ./internal/spec                # 精確比對
```

#### 執行控制 Flag

| Flag | 預設值 | 說明 |
|------|--------|------|
| `-v` | 關閉 | 詳細輸出 — 印出每個測試名稱與耗時 |
| `-count=N` | 1（有快取） | 每個測試執行 N 次；`-count=1` 停用結果快取 |
| `-parallel=N` | GOMAXPROCS | 每個套件中，`t.Parallel()` 測試的最大並行數 |
| `-timeout <時長>` | `10m0s` | 超過此時限即強制終止（例如 `300s`、`5m`） |
| `-failfast` | 關閉 | 第一個測試失敗後立即停止 |
| `-short` | 關閉 | 告知測試跳過耗時路徑（測試內檢查 `testing.Short()`） |
| `-race` | 關閉 | 啟用 Go 內建資料 race 偵測器（CI 中建議開啟） |

#### 建置標籤

```bash
# 啟用建置標籤 — 編譯帶有 //go:build <tag> 的檔案
go test -tags integration ./...

# 多個標籤（逗號分隔）
go test -tags "integration slow" ./...
```

#### 覆蓋率 Flag

| Flag | 說明 |
|------|------|
| `-cover` | 在每個套件結果旁顯示覆蓋率百分比（不輸出資料檔） |
| `-coverprofile=<檔案>` | 將覆蓋率資料寫入檔案供後續分析 |
| `-covermode=set` | 每個語句是否被執行（布林值） |
| `-covermode=count` | 每個語句被執行的次數（整數） |
| `-covermode=atomic` | 同 `count`，但執行緒安全（**並行測試請務必使用**） |

#### 基準測試（Benchmark）

```bash
go test -bench=. ./...                        # 執行所有基準測試
go test -bench=BenchmarkParse -benchmem ./... # 含記憶體分配統計
go test -bench=. -benchtime=5s ./...          # 每個基準測試執行 5 秒
```

#### 實用配方

```bash
# 日常開發 — 全部單元測試，無快取，詳細輸出
go test -count=1 -v ./...

# 最快速的冒煙測試（跳過 testing.Short() 標記的慢速路徑）
go test -short ./...

# CI 單元測試（含 race 偵測 + 覆蓋率百分比，無需 Docker）
go test -race -cover -count=1 ./...

# 完整覆蓋率報告 — 兩層測試（需要 Docker）
go test -tags integration -race \
  -coverprofile=coverage.out -covermode=atomic \
  ./internal/... -timeout 300s
go tool cover -func=coverage.out

# 只執行某一個測試並查看完整輸出
go test -v -count=1 -run ^TestUpsertBook$ ./internal/repository

# JSON 格式輸出（供 CI 儀表板或解析工具使用）
go test -json ./... | jq '.Output' -r
```

---

### 測試架構說明

| 層級 | 建置標籤 | 依賴項目 | 相關檔案 |
|------|----------|----------|----------|
| 單元測試 | _(無)_ | `go-sqlmock`、`testify`、`httptest` | `*_test.go`（無 integration 後綴） |
| 整合測試 | `integration` | Docker + Testcontainers（PostgreSQL 16） | `*_integration_test.go`、`testhelper/postgres.go` |

整合測試額外覆蓋：
- `internal/database` — `Connect` 成功路徑及無效 URL 觸發 `log.Fatalf`→`os.Exit` 的子程序測試
- `internal/repository` — 對真實 PostgreSQL 16 資料庫的完整冪等性驗證
- `internal/scraper` — 搭配 `httptest.NewServer` 模擬 HTTP 伺服器和真實資料庫的端對端 `Run()` 測試

### 測試相依套件

| 套件 | 用途 |
|------|------|
| `github.com/DATA-DOG/go-sqlmock` | 單元測試用 SQL mock 驅動（無需真實資料庫） |
| `github.com/stretchr/testify` | 斷言輔助（`assert`、`require`） |
| `github.com/testcontainers/testcontainers-go` | 在測試中自動啟動 Docker 容器 |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | 預建的 PostgreSQL 容器模組 |

---

## 🛠 前置需求

- **Go** 1.25 或更高版本
- **PostgreSQL** 13 或更高版本
- **Git**

## 📐 資料庫 Schema

爬蟲寫入 `bibles` schema，共六張資料表，分為結構層與內容層：

| 資料表 | 用途 |
|--------|------|
| `bibles.bible_books` | 每卷書一列（sort 順序由規格檔決定） |
| `bibles.bible_book_contents` | 書名本地化（chinese / english） |
| `bibles.bible_chapters` | 每章一列（隸屬於書） |
| `bibles.bible_chapter_contents` | 章名本地化 |
| `bibles.bible_sections` | 每節一列（隸屬於章） |
| `bibles.bible_section_contents` | 節的本地化標題與內文 |

第一次執行前，請先用專案文件或 `db/schema.sql` 中的 DDL 建立 Schema。

## 🚀 標準作業程序（SOP）

### 步驟 1：取得程式碼並安裝相依套件

```bash
git clone https://github.com/your-username/bible-crawler.git
cd bible-crawler
go mod tidy
```

### 步驟 2：資料庫初始化

1. 登入 PostgreSQL，建立資料庫（例如 `topchurch_dev`）。
2. 執行 DDL 腳本建立 `bibles` schema 及六張資料表。

### 步驟 3：設定環境變數

將 `.env.example` 複製為 `.env`，填入實際連線資訊與設定：

```ini
# ── 資料庫 ────────────────────────────────────────────────────────────────────
APP_ENV=development
DATABASE_URL=postgres://username:password@localhost:5432/topchurch_dev?sslmode=disable

# ── 來源網站（HTML 爬蟲專用）──────────────────────────────────────────────────
# 若網站搬遷或更換聖經翻譯版本，更新以下三個值即可，無需重新編譯程式碼。
# SOURCE_ZH_URL 與 SOURCE_EN_URL 各需包含一個 %d 佔位符，代表全域章節索引。
SOURCE_DOMAIN=springbible.fhl.net
SOURCE_ZH_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0
SOURCE_EN_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe

# ── 爬蟲調校（HTML 爬蟲專用）─────────────────────────────────────────────────
# 如遭伺服器限速，可降低 CRAWLER_PARALLELISM 或提高延遲值。
CRAWLER_PARALLELISM=5
CRAWLER_DELAY_MS=200
CRAWLER_RANDOM_DELAY_MS=100
HTTP_TIMEOUT_SEC=30

# ── YouVersion Platform API（youversion-crawler 專用）────────────────────────
# 在 https://platform.youversion.com 申請 App Key
YOUVERSION_API_KEY=your-app-key-here
YOUVERSION_BASE_URL=https://api.youversion.com/v1   # 選填，此為預設值
# 預設 Bible ID：111 = NIV 2011（英文），312 = CSB 中文標準譯本（繁體中文）
# 若需使用其他中文譯本，將 YOUVERSION_CHINESE_BIBLE_ID 設為對應的 Bible ID 即可
YOUVERSION_ENGLISH_BIBLE_ID=111
YOUVERSION_CHINESE_BIBLE_ID=312

# ── YouVersion 平行模式（必填）────────────────────────────────────────────────
# cmd/youversion-crawler 只支援平行模式，YOUVERSION_CHECKPOINT_FILE 為必填。
# 未設定時爬蟲將拒絕啟動。
YOUVERSION_CHECKPOINT_FILE=ckpoint.jsonl  # JSONL 斷點檔案路徑（必填）
YOUVERSION_WORKERS=20              # 平行 goroutine 數量（需 ≥ RPS）
YOUVERSION_RATE_LIMIT_RPS=15.0    # 每秒最大請求數（≥15 可在 1 小時內完成約 6.2 萬節）
YOUVERSION_MAX_RETRIES=3           # 5xx/網路錯誤最大重試次數（預設：3）
YOUVERSION_RETRY_BASE_MS=500       # 初始退避間隔毫秒，每次加倍（預設：500）

# ── Bible.com HTML 爬蟲（CUNP-上帝 + NIV）───────────────────────────────────
# 用於 cmd/biblecom-crawler（步驟 C-1，輸出 JSON 檔案）
# 及 cmd/biblecom-importer（步驟 C-2，讀取 JSON 匯入 DB）。
BIBLECOM_ZH_BASE_URL=https://www.bible.com/bible/414
BIBLECOM_EN_BASE_URL=https://www.bible.com/bible/111
BIBLECOM_ZH_VERSION_SUFFIX=CUNP-%E4%B8%8A%E5%B8%9D
BIBLECOM_EN_VERSION_SUFFIX=NIV
BIBLECOM_WORKERS=5
BIBLECOM_RATE_LIMIT_RPS=2.0
BIBLECOM_TIMEOUT_SEC=30
BIBLECOM_OUTPUT_ZH=youversion-bible_books_zh.json
BIBLECOM_OUTPUT_EN=youversion-bible_books_en.json
# Optional: comma-separated book sort numbers (1–66); leave unset to crawl all 66 books.
# Example: BIBLECOM_FILTER_SORTS=65   ← re-crawls only Jude without touching the other 65 books
#BIBLECOM_FILTER_SORTS=
```

### 步驟 4：建置 Spec 規格檔（首次或需更新時執行）

此步驟爬取全部章節頁面（約 2,378 個請求），取得每語言的實際節數，並寫入兩份 JSON 規格檔。**全新建置時必須先執行此步驟。**

```bash
go run cmd/spec-builder/main.go
```

預計時間：**5–10 分鐘**（5 個並行 worker）。

預期輸出：
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

> 執行完成後，`bible_books_zh.json` 與 `bible_books_en.json` 在部分書卷的章節節數將**不同**，正確反映兩個翻譯版本的版本差異。

### 步驟 5：執行爬蟲

請選擇**其中一種**（或多種）爬蟲方式。三者彼此**獨立**，可任意順序執行，但不可同時執行（全部寫入相同資料表）。

---

#### 選項 A — HTML 爬蟲（原 springbible.fhl.net）

**前置條件**：步驟 4（規格檔必須存在）。從 HTML 網站抓取聖經內容。

**執行順序**：

```
步驟 4 (spec-builder) ──→ 選項 A (HTML 爬蟲)
```

```bash
# 步驟 4 須先執行（產生 bible_books_zh.json + bible_books_en.json）：
go run cmd/spec-builder/main.go

# 再執行 HTML 爬蟲（直接寫入資料庫）：
go run cmd/crawler/main.go
```

預期輸出：
```text
Connected to database successfully
Starting Bible Crawler...
Phase 1: Setting up Books from spec...
Phase 1 complete: N books ready.
Phase 2: Crawling Chapters...
Phase 2 complete.
Bible Crawler finished successfully.
```

---

#### 選項 B — YouVersion API 爬蟲 ✨

**前置條件**：`.env` 已設定 `YOUVERSION_API_KEY` 與 `YOUVERSION_CHECKPOINT_FILE`。無需規格檔，所有結構直接從 API 取得。

> ⚠️ `YOUVERSION_CHECKPOINT_FILE` 為**必填**，未設定時爬蟲將拒絕啟動。爬蟲**只支援平行模式**。

**執行順序**（兩個必要步驟，須依序執行）：

```
步驟 B-1 (youversion-crawler) ──→ 步驟 B-2 (youversion-importer)
```

> ⚠️ 步驟 B-1 與 B-2 之間請勿刪除 `YOUVERSION_CHECKPOINT_FILE`（預設 `ckpoint.jsonl`）。
> 匯入程式需要讀取爬蟲寫出的 JSONL 檔案。

---

##### B-1. 爬取 → JSONL 斷點檔案

Phase 1 直接將書卷／書名 metadata 寫入資料庫。
Phase 2 平行抓取所有經文，寫入 JSONL 斷點檔案 — **此階段不直接寫入 DB**。

```bash
go run cmd/youversion-crawler/main.go
```

預期輸出：
```text
Connected to database successfully
Parallel mode: workers=20 rps=15.0 maxRetries=3 checkpoint="ckpoint.jsonl"
Starting YouVersion Bible Crawler...
YouVersion Scraper: starting...
Phase 1: fetching book list for English Bible (ID 111)...
Phase 1: fetching book list for Chinese Bible (ID 312)...
Phase 1: 66/66 books ready.
Phase 2: crawling verses (parallel)...
Phase 2: 0 verses already in checkpoint
Phase 2: 62213/62213 verses remaining
Phase 2: done. written=62197 already-done=0 write-errors=0
YouVersion Scraper: done.
YouVersion Crawler finished successfully.
Next step: run 'go run cmd/youversion-importer/main.go' to import "ckpoint.jsonl" into the database.
```

> **優雅停止**：執行中按 `Ctrl+C`，Worker 完成當前節後安全停止並 flush 斷點檔案。重新執行同樣指令即可從中斷處繼續，已完成的節自動跳過。

> **404 回應**（例如 `MAT.17.21`、`MAT.18.11`）為**正常現象**，自動靜默跳過。NIV 等現代譯本刻意省略部分節號，並非錯誤。

> **sub_title 欄位**：YouVersion API v1 不提供段落標題（pericope headings）。`bibles.bible_section_contents.sub_title` 欄位在 YouVersion 資料來源下將永遠為空。

---

##### B-2. 將 JSONL 匯入資料庫

在步驟 B-1 完成後（或繼續爬取後完成後）執行：

```bash
go run cmd/youversion-importer/main.go
```

預期輸出：
```text
Imported 1000 verses...
...
Import complete: total=62197 written=62197 skipped=0
```

> 匯入程式會自動從 `.env` 讀取 `YOUVERSION_CHECKPOINT_FILE`，無需額外指定。完全冪等，可安全重複執行。

---

> **聖經版本說明**：選項 B 預設使用 CSB 中文標準譯本（ID 312，繁體中文）與 NIV 2011（ID 111），兩者皆可免費透過 YouVersion Platform App Key 存取。若需使用其他中文譯本（例如取得授權後的新標點和合本 ID 46），在 `.env` 設定 `YOUVERSION_CHINESE_BIBLE_ID=<id>` 即可。

---

#### 選項 C — Bible.com HTML 爬蟲 🆕

**前置條件**：`.env` 已設定 `BIBLECOM_*` 環境變數（預設值已提供）。規格 JSON 檔案（`bible_books_zh.json` / `bible_books_en.json`）必須存在 — 若尚未建立，請先執行步驟 4。

**翻譯版本**：CUNP-上帝（中文，版本 ID 414）+ NIV（英文，版本 ID 111）。

**執行順序**（必須依序完成兩個步驟）：

```
步驟 C-1 (biblecom-crawler) ──→ 步驟 C-2 (biblecom-importer)
```

> ⚠️ 在步驟 C-1 與 C-2 之間，**不要刪除** JSON 輸出檔案。匯入程式需讀取爬蟲產生的 JSON 檔案。

---

##### C-1. 爬取 → JSON 檔案

爬取 bible.com 網站全部 1,189 章 × 2 種語言，並寫出兩份 JSON 檔案（各一語言）。**本步驟不會寫入資料庫。**

```bash
go run cmd/biblecom-crawler/main.go
```

預期輸出：
```text
[biblecom-crawler] Loading Bible spec...
[biblecom-crawler] Starting crawl (workers=5 rps=2.0)...
[biblecom-crawler] Writing output files…
[biblecom-crawler] Done — ZH: 31102 verses in 66 books | EN: 31102 verses in 66 books
```

輸出檔案路徑由 `.env` 中的 `BIBLECOM_OUTPUT_ZH` 與 `BIBLECOM_OUTPUT_EN` 控制（預設：`youversion-bible_books_zh.json` 與 `youversion-bible_books_en.json`）。

> **合併節偵測**：當 bible.com 以同一段經文顯示多個節號（例如撒母耳記下 3:9–10），解析器會將完整內容指定給最小節號，其餘節號則以哨兵文字 `"併於上節。"` 填充，並在 JSON 中以 `note: "merged"` 標記。

> **交叉參照的方括號節（Cross-referenced bracket verses）**：某些譯本（如 NIV）會以方括號包住節號（例如 `[21]`），表示該節僅見於部分抄本，非譯者採用的最早手稿。bible.com 對這類節僅渲染 `__note` 腳注元素，腳注通常說明「某些抄本於此處包含類似馬可福音 9:29 的文字」——意即該節內容應填入另一節的原文。
>
> 解析器以兩個階段處理此狀況：
> 1. **偵測**：`[N]` 標記的節若僅包含 `__note` 元素（無正文），即被標記為方括號節。若腳注中含有 `<span class="ref" data-usfm="MRK.9.29">` 元素，則提取 USFM 鍵值並存入 JSON 的 `cross_ref` 欄位。
> 2. **解析** (`resolveRefs`)：66 本書全部爬取完成後，爬蟲對所有方括號節進行一次記憶體內掃描。有 `cross_ref` 的節會從被參照的節取得實際內容（例如馬太福音 17:21 會取得馬可福音 9:29 的原文）。`note` 欄位設為 `"ref:MRK.9.29"`，`cross_ref` 保留供稽核追蹤。
>
> 若腳注中找不到 `span.ref[data-usfm]`（退回機制），則直接以腳注本文作為節的內容，`note` 設為 `"omitted"`。
>
> NIV 已知的 16 個案例：馬太福音 17:21、18:11、23:14；馬可福音 7:16、9:44、9:46、11:26、15:28；路加福音 17:36、23:17；約翰福音 5:4；使徒行傳 8:37、15:34、24:7、28:29；羅馬書 16:24。

> **sub_title 支援**：與 YouVersion API 不同，bible.com HTML 頁面包含段落標題（pericope headings）。這些標題會捕捉至每個 `VerseOutput` 的 `sub_title` 欄位，並在匯入時寫入 `bibles.bible_section_contents.sub_title`。

> **優雅停止**：執行中按 `Ctrl+C`，Worker 完成當前 HTTP 請求後安全停止，部分結果會寫入輸出 JSON 檔案。

> **Targeted single-book re-crawl** (`BIBLECOM_FILTER_SORTS`): Set this env var to a
> comma-separated list of book sort numbers (1–66) to crawl only those books — useful
> after a USFM-code correction or a network interruption affecting a single book, without
> re-fetching all 1,189 chapters:
>
> ```bash
> # Re-crawl only Jude (sort=65):
> BIBLECOM_FILTER_SORTS=65 go run cmd/biblecom-crawler/main.go
> ```
>
> The scraper logs `[biblecom] Book filter active — crawling only sorts: [65]` when the
> filter is active. The output JSON contains only the filtered books. Run
> `cmd/biblecom-importer` against it as usual — the importer is fully idempotent and only
> updates rows for books present in the JSON file.

---

##### C-2. 匯入 JSON → 資料庫

在 C-1 完成後執行。讀取兩份 JSON 檔案，以與其他爬蟲相同的冪等 `SELECT → INSERT → SELECT` 模式，將所有書卷、章節和節段 upsert 至 PostgreSQL。

```bash
go run cmd/biblecom-importer/main.go
```

預期輸出：
```text
[biblecom-importer] importing Chinese file: youversion-bible_books_zh.json
[biblecom-importer] Chinese import complete: books=66 chapters=1189 verses=31102 skipped=0
[biblecom-importer] importing English file: youversion-bible_books_en.json
[biblecom-importer] English import complete: books=66 chapters=1189 verses=31102 skipped=0
```

匯入程式讀取 `.env` 中的 `BIBLECOM_OUTPUT_ZH` 與 `BIBLECOM_OUTPUT_EN`。完全冪等，可安全重複執行多次，不會產生重複資料。

> **UUID 快取**：中文匯入期間解析出的結構性 UUID（書卷、章節、小節）會在英文匯入時共用，減少約 32,000 次冗餘的 SELECT 資料庫查詢。

---

### 步驟 6：驗證資料（選擇性）

對資料庫執行專案根目錄的 `validation.sql`。查詢涵蓋三個層級（書、章、節）。第 1–3 節（缺漏偵測）應返回 **0 筆**結果；第 5 節（版本差異稽核）會列出預期的版本差異章節，這是正常現象。

---

## 🔄 重新爬取流程（保護跨 Schema 的聖經引用資料）

當需要在**已有其他微服務資料**引用 `bibles.bible_sections` 的環境下執行 TRUNCATE + 重跑爬蟲時，使用此流程取代步驟 5。

> **背景說明**：以下三張表以純 UUID 欄位儲存 `bibles.bible_sections(id)`，且**未宣告 FK constraint**，因此 `TRUNCATE … CASCADE` 不會自動清理它們。重跑爬蟲後所有 UUID 都會改變，這些欄位將變成孤兒資料：
>
> | 資料表 | 欄位 |
> |---|---|
> | `activities.general_bibles` | `bible_id` |
> | `activities.general_template_bibles` | `bible_id` |
> | `devotions.devotion_bibles` | `bible_section_id` |

### 步驟 A — 備份（TRUNCATE 前）

```bash
# 記錄每個被引用節的穩定座標 (book_sort, chapter_sort, section_sort)，
# 備份完成後立即 TRUNCATE 全部 6 張 bibles 資料表。
go run cmd/migrate/main.go --phase=backup --truncate
```

> 若偏好手動執行 TRUNCATE，可省略 `--truncate`，改為手動執行 `TRUNCATE TABLE bibles.bible_books CASCADE;`。

### 步驟 B — 重建 Spec（選擇性）

只有需要從來源網站重新取得最新節數時才執行：

```bash
go run cmd/spec-builder/main.go
```

### 步驟 C — 重新爬取

**重新爬取執行順序** — 請選擇與原本相同的爬蟲方式：

**選項 A（HTML 爬蟲）**：
```
步驟 B（spec-builder，選擇性）──→ 步驟 C（HTML 爬蟲）
```
```bash
go run cmd/crawler/main.go
```

**選項 B（YouVersion API 爬蟲）** — 兩個必要步驟，須依序執行：
```
步驟 C-1 (youversion-crawler) ──→ 步驟 C-2 (youversion-importer)
```
```bash
# 步驟 C-1：爬取經文 → JSONL 斷點檔案
go run cmd/youversion-crawler/main.go

# 步驟 C-2：匯入 JSONL → 資料庫（步驟 C-1 完成後才執行）
go run cmd/youversion-importer/main.go
```

**選項 C（bible.com HTML 爬蟲）** — 兩個必要步驟，須依序執行：
```
步驟 C-1 (biblecom-crawler) ──→ 步驟 C-2 (biblecom-importer)
```
```bash
# 步驟 C-1：爬取 HTML → JSON 檔案
go run cmd/biblecom-crawler/main.go

# 步驟 C-2：匯入 JSON → 資料庫（步驟 C-1 完成後才執行）
go run cmd/biblecom-importer/main.go
```

### 步驟 D — 還原（爬蟲完成後）

```bash
# 將 3 張跨 schema 資料表更新為新的 UUID，
# 驗證孤兒數量（應全為 0），然後刪除備份表。
go run cmd/migrate/main.go --phase=restore --cleanup
```

預期輸出：
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

> 若出現 `WARNING: N orphan reference(s) remain`，請省略 `--cleanup`，先調查原因後再刪除備份表。

---

對資料庫執行專案根目錄的 `validation.sql`。查詢涵蓋三個層級（書、章、節）。第 1–3 節（缺漏偵測）應返回 **0 筆**結果；第 5 節（版本差異稽核）會列出預期的版本差異章節，這是正常現象。

## 📂 專案結構說明

```text
bible-crawler/
├── cmd/
│   ├── biblecom-crawler/
│   │   └── main.go               # bible.com HTML 爬蟲（CUNP-上帝 + NIV）→ JSON 檔案（步驟 C-1）
│   ├── biblecom-importer/
│   │   └── main.go               # 讀取 JSON 檔案 → 批次寫入 PostgreSQL（步驟 C-2）
│   ├── crawler/
│   │   └── main.go               # 主爬蟲進入點（Stage 1 + 2）→ PostgreSQL
│   ├── migrate/
│   │   └── main.go               # 跨 schema UUID 備份/還原（--phase=backup|restore）
│   ├── spec-builder/
│   │   └── main.go               # Stage 0：發現節數，寫入 JSON 規格檔
│   ├── youversion-crawler/
│   │   └── main.go               # YouVersion API 爬蟲：Phase 1（DB 建置）+ Phase 2（JSONL）
│   └── youversion-importer/
│       └── main.go               # 讀取 JSONL 斷點檔案 → 批次寫入 PostgreSQL
├── internal/
│   ├── biblecom/                 # bible.com HTML 爬蟲：爬蟲、解析器、書目清單、型別
│   │   ├── books.go              # 66 卷書正典目錄（USFM 代碼 + 本地化名稱）
│   │   ├── parser.go             # HTML 解析器（從 bible.com 擷取經文與段落標題）
│   │   ├── parser_test.go        # 單元測試
│   │   ├── scraper.go            # BibleComScraper：平行 HTTP 爬取 + 流量限制
│   │   └── types.go              # OutputFile、BookOutput、ChapterOutput、VerseOutput、workItem
│   ├── config/                   # 環境變數載入（所有 .env 欄位）
│   │   └── config_test.go        # 單元測試 — 100 % 覆蓋率
│   ├── database/                 # PostgreSQL 連線設定
│   │   └── database_test.go      # 整合測試（build tag: integration）
│   ├── migration/                # 跨 schema 聖經引用備份/還原邏輯
│   │   └── migration_test.go     # 單元測試（go-sqlmock）— 100 % 覆蓋率
│   ├── model/                    # 對應資料庫的 Go Struct（6 個）
│   ├── repository/               # 冪等資料存取層（所有 SQL）
│   │   ├── repository_test.go             # 單元測試（go-sqlmock）— 100 % 覆蓋率
│   │   └── repository_integration_test.go # 整合測試（build tag: integration）
│   ├── scraper/                  # Colly HTML 爬蟲核心邏輯（springbible.fhl.net）
│   │   ├── scraper_test.go             # 單元測試 — 96 % 覆蓋率
│   │   └── scraper_integration_test.go  # 整合測試（build tag: integration）
│   ├── spec/                     # JSON 規格檔載入（BibleSpec, BookSpec）
│   │   └── spec_test.go          # 單元測試 — 100 % 覆蓋率
│   ├── testhelper/               # 共用 Testcontainers 輔助（僅 integration build tag）
│   │   └── postgres.go
│   ├── utils/                    # Big5→UTF-8 解碼、文字清理
│   │   └── encoding_test.go      # 單元測試 — 83 % 覆蓋率
│   └── youversion/               # YouVersion Platform API 客戶端 + 爬蟲
│       ├── checkpoint.go         # JSONL 斷點：VerseRecord、Append、LoadCompleted
│       ├── client.go             # HTTP 客戶端（GetBooks、GetPassage、GetBibles、GetVOTD…）
│       ├── client_test.go        # 單元測試 — 100 % 覆蓋率
│       ├── scraper.go            # 爬蟲協調器：setupBooks、crawlVersesParallel
│       ├── scraper_test.go       # 單元測試 — 100 % 覆蓋率
│       ├── titles.go             # FormatChapterTitle / FormatVerseTitle（本地化模板）
│       └── types.go              # API 回應型別（BooksResponse、PassageData、VOTDEntry…）
├── bible_books_zh.json           # 和合本各章節數（由 spec-builder 自動產生）
├── bible_books_en.json           # BBE 各章節數（由 spec-builder 自動產生）
├── validation.sql                # PostgreSQL 驗證查詢 + 雙語章節內容查詢
├── .env                          # 本機 DB 連線設定與調校（不納入版控）
├── .env.example                  # .env 範本（所有欄位均有說明）
├── go.mod
└── README_ZH.md
```

> **注意**：已編譯的執行檔已列入 `.gitignore`，請勿提交至版本控制。請一律使用 `go run cmd/<name>/main.go` 執行。

## ⚠️ 常見問題排除

**Q：出現 "pq: password authentication failed for user..." 錯誤**  
A：請檢查 `.env` 中的 `DATABASE_URL`，確認帳號密碼正確。

**Q：出現 "dial tcp [::1]:5432: connect: connection refused" 錯誤**  
A：請確認 PostgreSQL 服務已啟動並監聽 5432 連接埠。

**Q：驗證 SQL 有缺漏資料**  
A：`validation.sql` 第 5 節列出的版本差異章節為正常現象。第 1–3 節若有缺漏，請重新執行 `cmd/crawler`（完全冪等，不會產生重複資料）。

**Q：資料出現亂碼（Mojibake）**  
A：爬蟲已內建 Big5 轉 UTF-8 處理。請勿修改 `internal/utils/encoding.go` 的編碼邏輯。

**Q：YouVersion 爬蟲記錄大量「status 404」錯誤**  
A：404 回應為**正常現象**。NIV 等現代譯本刻意省略部分節號（例如 MAT.17.21、MAT.18.11、MRK.7.16 等），平行模式自動靜默跳過這些節。只有 429 或 5xx 回應才會觸發重試。

**Q：YouVersion 爬蟲出現「YOUVERSION_CHECKPOINT_FILE must be set」錯誤**  
A：YouVersion 爬蟲**只支援平行模式**，`YOUVERSION_CHECKPOINT_FILE` 為必填。請在 `.env` 中設定任意檔案路徑（例如 `YOUVERSION_CHECKPOINT_FILE=ckpoint.jsonl`）後再啟動。

**Q：Bible.com 爬蟲啟動時出現設定錯誤**  
A：爬蟲啟動時會驗證 URL（必須為 `https://www.bible.com/…`）和數值範圍（workers 1–50、RPS 0.1–20、timeout 5–120 秒）。`.env.example` 已提供所有欄位的合理預設值。

**Q: bible.com crawler output is missing a book — e.g. Jude never appears in the JSON.**  
A: A silently-missing book almost always means its USFM code in `internal/biblecom/books.go` does not match what bible.com uses in its page URLs. Historically, Jude (sort=65) was catalogued as `"JDE"` instead of the correct `"JUD"` — every request returned HTTP 404, and the book was silently dropped. **This has been fixed.** The scraper now logs a `WARN` line for every in-scope book that yields no chapters, including the USFM code, making future mismatches immediately visible. If you have JSON files produced before this fix, simply re-run `cmd/biblecom-crawler` to regenerate them.

**Q: How do I re-crawl only one book without re-fetching all 66?**  
A: Use the `BIBLECOM_FILTER_SORTS` env var set to the book's canonical sort number (1–66):

```bash
# Re-crawl only Jude (sort=65) — leaves the other 65 books untouched in the DB:
BIBLECOM_FILTER_SORTS=65 go run cmd/biblecom-crawler/main.go
go run cmd/biblecom-importer/main.go
```

The scraper logs `[biblecom] Book filter active — crawling only sorts: [65]` to confirm. The output JSON contains only the filtered books; the importer is fully idempotent and only updates rows present in the JSON.

**Q：`bible_books_zh.json` 與 `bible_books_en.json` 節數完全相同**  
A：請重新執行 `cmd/spec-builder/main.go`。規格檔必須從網站即時抓取才能正確反映兩語言的版本差異。

## 📜 授權

本專案僅供教育與個人研究使用。請尊重來源網站的內容版權與使用規範。
