# Bible Crawler (聖經爬蟲系統)

這是一個高效能、支援併發的 Go 語言網頁爬蟲。專門用於從 `springbible.fhl.net` 抓取聖經資料（和合本中文版與 Basic English Version），並將其儲存到結構嚴謹的 PostgreSQL 資料庫中。

## 🌟 功能特色

- **兩階段爬取 (Two-Phase Crawling)**：
  - **第一階段**：同步探索並建立 66 卷書的元數據，確保資料結構完整性。
  - **第二階段**：非同步（Async）併發爬取所有章節與經文，極大化效能。
- **強大的編碼支援**：自動處理來源網站的 **Big5** 編碼，並轉換為 UTF-8 儲存。
- **冪等性設計 (Idempotency)**：程式可重複執行。寫入規則為「不存在就插入、完全一致不動作、資料不同才更新」，避免重複資料與不必要寫入。
- **速率限制 (Rate Limiting)**：內建保護機制（每秒 2 次請求），尊重目標伺服器負載並避免 IP 被封鎖。
- **清晰架構**：符合 SOLID 原則，分為 Scraper (爬蟲)、Repository (資料存取)、Model (模型) 層。

## 🛠 前置需求

在開始之前，請確保您已安裝以下工具：

- **Go** (版本 1.21 或更高)
- **PostgreSQL** (版本 13 或更高)
- **Git**

## 🚀 標準作業程序 (SOP)

請依照以下步驟設定並執行爬蟲。

### 步驟 1：取得專案程式碼

```bash
git clone https://github.com/your-username/bible-crawler.git
cd bible-crawler
```

### 步驟 2：資料庫初始化

在執行爬蟲之前，必須先建立資料庫 Schema。

1.  登入您的 PostgreSQL 資料庫。
2.  建立一個新的資料庫（例如：`topchurch_dev`）。
3.  執行 DDL SQL 腳本以建立資料表。
    *(注意：請使用專案文件中提供的 Schema SQL，需包含 `bibles` schema 以及 `bible_books`, `bible_chapters` 等資料表。)*

### 步驟 3：設定環境變數

在專案根目錄下建立一個 `.env` 檔案，用於設定資料庫連線資訊。

1.  複製範例設定或直接建立新檔案 `.env`。
2.  加入以下內容：

```ini
# .env file
APP_ENV=development
DATABASE_URL=postgres://username:password@localhost:5432/topchurch_dev?sslmode=disable
```

*請將 `username`, `password`, `localhost`, `topchurch_dev` 替換為您實際的 PostgreSQL 帳號密碼與資料庫名稱。*

### 步驟 4：安裝相依套件

下載並整理所需的 Go 模組：

```bash
go mod tidy
```

### 步驟 5：執行爬蟲

執行主程式：

```bash
go run cmd/crawler/main.go
```

**預期輸出結果：**

```text
2024/03/11 10:00:00 Starting Bible Crawler...
2024/03/11 10:00:00 Connected to database successfully
2024/03/11 10:00:00 Phase 1: Discovering Books...
...
2024/03/11 10:00:05 Discovered 66 books.
2024/03/11 10:00:05 Phase 2: Crawling Chapters...
...
2024/03/11 10:05:00 Bible Crawler finished successfully.
```

## 📂 專案結構說明

```text
bible-crawler/
├── cmd/
│   └── crawler/
│       └── main.go           # 程式進入點 (Entry Point)
├── internal/
│   ├── config/               # 環境變數載入與設定
│   ├── database/             # 資料庫連線邏輯
│   ├── model/                # 對應資料庫的 Go Struct 定義
│   ├── repository/           # 資料存取層 (SQL 查詢與寫入)
│   ├── scraper/              # 爬蟲核心邏輯 (Colly + GoQuery)
│   └── utils/                # 工具函式 (如：Big5 編碼轉換)
├── .env                      # 資料庫設定檔 (需自行建立)
├── go.mod                    # Go 模組定義檔
└── README_ZH.md              # 專案說明文件 (中文)
```

## ⚠️ 常見問題排除 (Troubleshooting)

**Q: 出現 "pq: password authentication failed for user..." 錯誤**
A: 請檢查 `.env` 檔案中的 `DATABASE_URL`，確認使用者名稱與密碼是否正確。

**Q: 出現 "dial tcp [::1]:5432: connect: connection refused" 錯誤**
A: 請確認您的 PostgreSQL 服務已啟動，且正在監聽 5432 連接埠。

**Q: 爬蟲卡住或停止回應**
A: 目標網站可能對您進行了暫時的速率限制。爬蟲預設設定為每請求間隔 0.5 秒。如果問題持續，請檢查網路連線。

**Q: 資料出現亂碼 (Mojibake)**
A: 本爬蟲內建 `Big5ToUTF8` 轉換工具。請確保您沒有修改 `internal/utils/encoding.go` 中的編碼處理邏輯。

## 📜 授權

本專案僅供教育與個人研究使用。請尊重來源網站的內容版權與使用規範。
