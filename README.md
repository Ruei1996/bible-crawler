# Bible Crawler

A high-performance, concurrent web crawler written in Go. It scrapes Bible data (Chinese Union Version & Basic English Version) from `springbible.fhl.net` and populates a PostgreSQL database with a strict, normalized schema.

## 🌟 Features

- **Two-Phase Crawling**: 
  - **Phase 1**: Synchronously discovers books and metadata to ensure structural integrity.
  - **Phase 2**: Asynchronously crawls chapters and verses with concurrency for high performance.
- **Robust Encoding Support**: Automatically handles **Big5** encoding from the source website and converts it to UTF-8.
- **Idempotency**: Designed to be re-runnable. Writes are equality-aware (`insert if missing`, `no-op if identical`, `update if changed`) to prevent duplicate data and unnecessary churn.
- **Rate Limiting**: Built-in protection (2 requests/second) to respect the target server and avoid IP bans.
- **Clean Architecture**: Separation of concerns into Scraper, Repository, and Model layers.

## 🛠 Prerequisites

Before you begin, ensure you have the following installed:

- **Go** (version 1.21 or higher)
- **PostgreSQL** (version 13 or higher)
- **Git**

## 🚀 Standard Operating Procedure (SOP)

Follow these steps to set up and run the crawler.

### Step 1: Clone the Repository

```bash
git clone https://github.com/your-username/bible-crawler.git
cd bible-crawler
```

### Step 2: Database Initialization

You need to create the database schema before running the crawler.

1.  Log in to your PostgreSQL instance.
2.  Create a database (e.g., `topchurch_dev`).
3.  Execute the DDL SQL script to create the tables. 
    *(Note: Ensure you have the DDL script provided in the project documentation or `db/schema.sql` if available. The schema includes tables like `bible_books`, `bible_chapters`, `bible_sections` within the `bibles` schema.)*

### Step 3: Configuration

Create a `.env` file in the project root to configure the database connection.

1.  Copy the example configuration (if available) or create a new file named `.env`.
2.  Add the following environment variables:

```ini
# .env file
APP_ENV=development
DATABASE_URL=postgres://username:password@localhost:5432/topchurch_dev?sslmode=disable
```

*Replace `username`, `password`, `localhost`, and `topchurch_dev` with your actual PostgreSQL credentials.*

### Step 4: Install Dependencies

Download the required Go modules:

```bash
go mod tidy
```

### Step 5: Run the Crawler

Execute the main entry point:

```bash
go run cmd/crawler/main.go
```

**Expected Output:**

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

## 📂 Project Structure

```text
bible-crawler/
├── cmd/
│   └── crawler/
│       └── main.go           # Application entry point
├── internal/
│   ├── config/               # Environment configuration loader
│   ├── database/             # Database connection logic
│   ├── model/                # Go structs for database tables
│   ├── repository/           # Data access layer (SQL queries)
│   ├── scraper/              # Colly crawler & logic
│   └── utils/                # Utilities (e.g., Big5 decoding)
├── .env                      # Database configuration (User created)
├── go.mod                    # Go module definition
└── README.md                 # Project documentation
```

## ⚠️ Troubleshooting

**Q: "pq: password authentication failed for user..."**
A: Check your `DATABASE_URL` in the `.env` file. Ensure the username and password are correct.

**Q: "dial tcp [::1]:5432: connect: connection refused"**
A: Ensure your PostgreSQL service is running and accepting connections on port 5432.

**Q: The crawler stops or hangs.**
A: The target site might be rate-limiting you. The scraper is configured to wait 0.5s between requests. If issues persist, check your network connection.

**Q: Data looks garbled (mojibake).**
A: The crawler includes a `Big5ToUTF8` utility. Ensure you haven't modified the encoding logic in `internal/utils/encoding.go`.

## 📜 License

This project is for educational and personal use. Please respect the copyright of the source website content.
