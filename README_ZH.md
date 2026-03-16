# Bible Crawler（聖經爬蟲系統）

這是一個高效能、支援併發的 Go 語言網頁爬蟲。專門從 `springbible.fhl.net` 抓取聖經資料（和合本中文版與 Basic English Version BBE），並儲存到結構嚴謹的 PostgreSQL 資料庫中。

## 🌟 功能特色

- **Spec 驅動爬取**：各章節的實際節數從 `bible_books_zh.json`（和合本）與 `bible_books_en.json`（BBE）讀取，程式碼零硬寫數字，完全由 JSON 規格控制。
- **三階段工作流程**：
  - **Stage 0 — Spec Builder**：爬取每個章節（兩語言），發現實際節數，寫入兩份 JSON 規格檔。首次建置或需更新規格時執行。
  - **Stage 1 — 書本設定**：直接從 JSON 規格寫入所有書卷的中英文書名（不需 HTTP 請求）。書卷總數完全由 Stage 0 產生的規格檔決定。
  - **Stage 2 — 章節與經文爬取**：非同步併發爬取 1,189 個章節 × 2 語言，依各語言的規格節數上限存入資料庫。
- **版本節數差異處理（Versification-Aware）**：和合本與 BBE 在部分書卷（例如利未記、撒迦利亞書）的章節邊界不同。Spec 檔案記錄各語言的正確節數，爬蟲自動依上限存入，不會寫入超出範圍的節。
- **冪等寫入（Idempotent）**：所有 DB 寫入使用 `SELECT → INSERT → SELECT` 三步驟模式（對併發 goroutine 安全無 race condition）。重複執行爬蟲不會產生重複資料。
- **編碼處理**：自動將中文頁面的 **Big5** 編碼轉換為 UTF-8 後再解析。
- **完全可配置**：來源 URL、並行數、延遲與 HTTP 逾時皆透過 `.env` 設定，網站更新時無需重新編譯。

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

- **Go** 1.21 或更高版本
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

# ── 來源網站 ──────────────────────────────────────────────────────────────────
# 若網站搬遷或更換聖經翻譯版本，更新以下三個值即可，無需重新編譯程式碼。
# SOURCE_ZH_URL 與 SOURCE_EN_URL 各需包含一個 %d 佔位符，代表全域章節索引。
SOURCE_DOMAIN=springbible.fhl.net
SOURCE_ZH_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0
SOURCE_EN_URL=https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe

# ── 爬蟲調校 ──────────────────────────────────────────────────────────────────
# 如遭伺服器限速，可降低 CRAWLER_PARALLELISM 或提高延遲值。
CRAWLER_PARALLELISM=5
CRAWLER_DELAY_MS=200
CRAWLER_RANDOM_DELAY_MS=100
HTTP_TIMEOUT_SEC=30
```

> **提示 — 更換來源網站**：若 `springbible.fhl.net` 搬遷，或需改用其他中文/英文聖經翻譯，只需在 `.env` 更新 `SOURCE_DOMAIN`、`SOURCE_ZH_URL`、`SOURCE_EN_URL`，然後重新執行 `cmd/spec-builder` 重新產生規格 JSON，再執行主爬蟲即可。

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

### 步驟 5：執行主爬蟲

```bash
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

### 步驟 6：驗證資料（選擇性）

對資料庫執行專案根目錄的 `validation.sql`。查詢涵蓋三個層級（書、章、節）。第 1–3 節（缺漏偵測）應返回 **0 筆**結果；第 5 節（版本差異稽核）會列出預期的版本差異章節，這是正常現象。

## 📂 專案結構說明

```text
bible-crawler/
├── cmd/
│   ├── crawler/
│   │   └── main.go           # 主爬蟲進入點（Stage 1 + 2）
│   └── spec-builder/
│       └── main.go           # Stage 0：發現節數，寫入 JSON 規格檔
├── internal/
│   ├── config/               # 環境變數載入（所有 .env 欄位）
│   │   └── config_test.go    # 單元測試 — 100 % 覆蓋率
│   ├── database/             # PostgreSQL 連線設定
│   │   └── database_test.go  # 整合測試（build tag: integration）
│   ├── model/                # 對應資料庫的 Go Struct
│   ├── repository/           # 冪等資料存取層（所有 SQL）
│   │   ├── repository_test.go             # 單元測試（go-sqlmock）— 100 % 覆蓋率
│   │   └── repository_integration_test.go # 整合測試（build tag: integration）
│   ├── scraper/              # Colly 爬蟲核心邏輯
│   │   ├── scraper_test.go             # 單元測試 — 96 % 覆蓋率
│   │   └── scraper_integration_test.go  # 整合測試（build tag: integration）
│   ├── spec/                 # JSON 規格檔載入（BibleSpec, BookSpec）
│   │   └── spec_test.go      # 單元測試 — 100 % 覆蓋率
│   ├── testhelper/           # 共用 Testcontainers 輔助（僅 integration build tag）
│   │   └── postgres.go
│   └── utils/                # Big5→UTF-8 解碼、文字清理
│       └── encoding_test.go  # 單元測試 — 83 % 覆蓋率
├── bible_books_zh.json       # 和合本各章節數（由 spec-builder 自動產生）
├── bible_books_en.json       # BBE 各章節數（由 spec-builder 自動產生）
├── validation.sql            # PostgreSQL 驗證查詢 + 雙語章節內容查詢
├── .env                      # 本機 DB 連線設定與調校（不納入版控）
├── .env.example              # .env 範本（所有欄位均有說明）
├── go.mod
└── README_ZH.md
```

> **注意**：已編譯的執行檔（`spec-builder`、`crawler`）已列入 `.gitignore`，請勿提交至版本控制。請一律使用 `go run cmd/<name>/main.go` 執行。

## ⚠️ 常見問題排除

**Q：出現 "pq: password authentication failed for user..." 錯誤**  
A：請檢查 `.env` 中的 `DATABASE_URL`，確認帳號密碼正確。

**Q：出現 "dial tcp [::1]:5432: connect: connection refused" 錯誤**  
A：請確認 PostgreSQL 服務已啟動並監聽 5432 連接埠。

**Q：驗證 SQL 有缺漏資料**  
A：`validation.sql` 第 5 節列出的版本差異章節為正常現象。第 1–3 節若有缺漏，請重新執行 `cmd/crawler`（完全冪等，不會產生重複資料）。

**Q：資料出現亂碼（Mojibake）**  
A：爬蟲已內建 Big5 轉 UTF-8 處理。請勿修改 `internal/utils/encoding.go` 的編碼邏輯。

**Q：`bible_books_zh.json` 與 `bible_books_en.json` 節數完全相同**  
A：請重新執行 `cmd/spec-builder/main.go`。規格檔必須從網站即時抓取才能正確反映兩語言的版本差異。

## 📜 授權

本專案僅供教育與個人研究使用。請尊重來源網站的內容版權與使用規範。
