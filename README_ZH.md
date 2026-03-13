# Bible Crawler（聖經爬蟲系統）

這是一個高效能、支援併發的 Go 語言網頁爬蟲。專門從 `springbible.fhl.net` 抓取聖經資料（和合本中文版與 Basic English Version BBE），並儲存到結構嚴謹的 PostgreSQL 資料庫中。

## 🌟 功能特色

- **Spec 驅動爬取**：各章節的實際節數從 `bible_books_zh.json`（和合本）與 `bible_books_en.json`（BBE）讀取，程式碼零硬寫數字，完全由 JSON 規格控制。
- **三階段工作流程**：
  - **Stage 0 — Spec Builder**：爬取每個章節（兩語言），發現實際節數，寫入兩份 JSON 規格檔。首次建置或需更新規格時執行。
  - **Stage 1 — 書本設定**：直接從 JSON 規格寫入 66 卷書的中英文書名（不需 HTTP 請求）。
  - **Stage 2 — 章節與經文爬取**：非同步併發爬取 1,189 個章節 × 2 語言，依各語言的規格節數上限存入資料庫。
- **版本節數差異處理（Versification-Aware）**：和合本與 BBE 在部分書卷（例如利未記、撒迦利亞書）的章節邊界不同。Spec 檔案記錄各語言的正確節數；修復工具（repair）對「一個語言有、另一個沒有」的節位自動寫入可讀的佔位說明文字。
- **冪等寫入（Idempotent）**：所有 DB 寫入使用 `SELECT → INSERT → SELECT` 三步驟模式（對併發 goroutine 安全無 race condition）。重複執行爬蟲不會產生重複資料。
- **編碼處理**：自動將中文頁面的 **Big5** 編碼轉換為 UTF-8 後再解析。
- **完全可配置**：來源 URL、並行數、延遲與 HTTP 逾時皆透過 `.env` 設定，網站更新時無需重新編譯。
- **修復工具（Repair Tool）**：主爬蟲完成後，執行 `cmd/repair` 補齊任何因暫時性 HTTP 錯誤而遺漏的章節與經文。

## 🛠 前置需求

- **Go** 1.21 或更高版本
- **PostgreSQL** 13 或更高版本
- **Git**

## 📐 資料庫 Schema

爬蟲寫入 `bibles` schema，共六張資料表，分為結構層與內容層：

| 資料表 | 用途 |
|--------|------|
| `bibles.bible_books` | 每卷書一列（sort 1–66） |
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
Spec-builder starting: 2378 HTTP requests (1189 chapters × 2 languages).
Progress: 200/2378 requests done (0 errors)
...
Writing ZH (和合本): total_verses=31102 (OT=23145 NT=7957)
Writing EN (BBE): total_verses=31173 (OT=23214 NT=7959)
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
Phase 1 complete: 66 books ready.
Phase 2: Crawling Chapters...
Phase 2 complete.
Bible Crawler finished successfully.
```

### 步驟 6：執行修復工具（選擇性）

如果因暫時性網路錯誤導致部分章節或經文遺漏，執行修復工具。它會查詢 DB 找出缺漏項目，只補抓那些頁面，並對版本差異節位寫入佔位說明文字。

```bash
go run cmd/repair/main.go
```

資料完整時的預期輸出：
```text
No missing chapters found — nothing to repair.
```

### 步驟 7：驗證資料（選擇性）

對資料庫執行專案根目錄的 `validation.sql`。查詢涵蓋三個層級（書、章、節）。第 1–3 節（缺漏偵測）應返回 **0 筆**結果；第 5 節（版本差異稽核）會列出預期的版本差異章節，這是正常現象。

## 📂 專案結構說明

```text
bible-crawler/
├── cmd/
│   ├── crawler/
│   │   └── main.go           # 主爬蟲進入點（Stage 1 + 2）
│   ├── spec-builder/
│   │   └── main.go           # Stage 0：發現節數，寫入 JSON 規格檔
│   └── repair/
│       └── main.go           # 補齊遺漏章節/經文
├── internal/
│   ├── config/               # 環境變數載入（所有 .env 欄位）
│   ├── database/             # PostgreSQL 連線設定
│   ├── model/                # 對應資料庫的 Go Struct
│   ├── repository/           # 冪等資料存取層（所有 SQL）
│   ├── scraper/              # Colly 爬蟲核心邏輯
│   ├── spec/                 # JSON 規格檔載入（BibleSpec, BookSpec）
│   └── utils/                # Big5→UTF-8 解碼、文字清理
├── bible_books_zh.json       # 和合本各章節數（由 spec-builder 自動產生）
├── bible_books_en.json       # BBE 各章節數（由 spec-builder 自動產生）
├── validation.sql            # PostgreSQL 驗證查詢 + 雙語章節內容查詢
├── .env                      # 本機 DB 連線設定與調校（不納入版控）
├── .env.example              # .env 範本（所有欄位均有說明）
├── go.mod
└── README_ZH.md
```

> **注意**：已編譯的執行檔（`spec-builder`、`crawler`、`repair`）已列入 `.gitignore`，請勿提交至版本控制。請一律使用 `go run cmd/<name>/main.go` 執行。

## ⚠️ 常見問題排除

**Q：出現 "pq: password authentication failed for user..." 錯誤**  
A：請檢查 `.env` 中的 `DATABASE_URL`，確認帳號密碼正確。

**Q：出現 "dial tcp [::1]:5432: connect: connection refused" 錯誤**  
A：請確認 PostgreSQL 服務已啟動並監聽 5432 連接埠。

**Q：驗證 SQL 仍有缺漏資料**  
A：修復工具已自動寫入版本差異佔位文字。若仍有缺漏，重新執行 `cmd/repair` 即可（完全冪等）。

**Q：資料出現亂碼（Mojibake）**  
A：爬蟲已內建 Big5 轉 UTF-8 處理。請勿修改 `internal/utils/encoding.go` 的編碼邏輯。

**Q：`bible_books_zh.json` 與 `bible_books_en.json` 節數完全相同**  
A：請重新執行 `cmd/spec-builder/main.go`。規格檔必須從網站即時抓取才能正確反映兩語言的版本差異。

## 📜 授權

本專案僅供教育與個人研究使用。請尊重來源網站的內容版權與使用規範。
