package youversion

// checkpoint.go provides durable progress tracking for the parallel YouVersion
// crawler. Each successfully fetched verse is appended as a JSON line to a
// JSONL file. On restart the file is scanned to build the set of already-
// completed verses, which are then skipped by the work-queue builder.
//
// Thread safety: Append uses a mutex so it is safe to call from multiple
// goroutines concurrently. In practice the crawler uses a single writer
// goroutine, so contention is minimal.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// ScannerInitialBuf is the initial token buffer size used by JSONL scanners
	// in this package. Shared here so checkpoint reader and importer stay in sync.
	ScannerInitialBuf = 64 * 1024 // 64 KiB

	// ScannerMaxBuf is the maximum single-token size. 1 MiB is generous for any
	// verse; it prevents OOM from a single malformed or unusually long line.
	ScannerMaxBuf = 1024 * 1024 // 1 MiB

	// checkpointWriteBufferBytes is the in-process write buffer size for the
	// JSONL checkpoint file. At ~270 bytes per average verse record, a 64 KiB
	// buffer batches ~240 records into a single write(2) syscall, reducing
	// per-verse OS overhead from 62,200 individual calls to ~260.
	checkpointWriteBufferBytes = 64 * 1024

	// estimatedBytesPerCheckpointLine is used to pre-size the completed-verse
	// map from the checkpoint file's byte count, eliminating O(log n) rehash
	// cycles on partial resume. Derived from avg(PassageID≈10) + avg(Content≈150)
	// + JSON structural overhead ≈ 270 bytes per line.
	estimatedBytesPerCheckpointLine = 270
)

// verseKey is a zero-allocation map key for the completed-verse set.
// A struct key uses existing string headers (pointer+length); no new backing
// array is allocated, unlike "lang+":"+passageID" string concatenation which
// allocates on every map read and write. With 124,400 lookups per crawl run
// this eliminates ~2.5 MB of transient heap churn.
type verseKey struct {
	lang      string
	passageID string
}

// verseCheckpointKey is a minimal subset of VerseRecord used only during
// LoadCompleted to extract the two fields needed for deduplication.
// Using a smaller struct instead of the full VerseRecord avoids allocating
// and JSON-decoding the Content field (100-500 bytes per verse) for every
// line in the checkpoint file, cutting peak LoadCompleted memory significantly
// on large partial-resume runs (e.g. ~4.7 MB saved at 31,100 completed verses).
type verseCheckpointKey struct {
	PassageID string `json:"passage_id"`
	Lang      string `json:"lang"`
}

// VerseRecord is one line in the JSONL checkpoint file.
// It carries every field needed by cmd/youversion-importer to recreate the
// bible_sections and bible_section_contents rows without any additional API
// calls — only the logical sort keys (book/chapter/verse) plus the text.
type VerseRecord struct {
	PassageID   string    `json:"passage_id"`    // e.g. "GEN.1.1"
	Lang        string    `json:"lang"`           // "english" | "chinese"
	BibleID     int       `json:"bible_id"`       // YouVersion bible version ID
	BookSort    int       `json:"book_sort"`      // 1-based canonical book index (1–66)
	ChapterSort int       `json:"chapter_sort"`   // 1-based chapter index within the book
	VerseSort   int       `json:"verse_sort"`     // 1-based verse index within the chapter
	Content     string    `json:"content"`        // verse text
	CrawledAt   time.Time `json:"crawled_at"`     // UTC timestamp when the verse was fetched
}

// Checkpoint manages the JSONL progress file for the parallel crawler.
// Each line written to the file represents one successfully fetched verse.
// The file is opened in append mode so partial runs accumulate safely.
type Checkpoint struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	writer *bufio.Writer  // retained for Flush in Close; enc writes into it
	enc    *json.Encoder  // writes JSON + '\n' directly into writer; no intermediate []byte
}

// NewCheckpoint opens (or creates) the JSONL file at path in append mode and
// returns a ready-to-use Checkpoint. Call Close when the crawler exits.
//
// path is canonicalised via filepath.Abs to prevent directory traversal.
// File is created with mode 0600 (owner read/write only) to protect API usage
// metadata from other local users.
func NewCheckpoint(path string) (*Checkpoint, error) {
	cleaned, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve checkpoint path %q: %w", path, err)
	}
	// O_APPEND positions the write cursor atomically at EOF before each write
	// (POSIX-guaranteed), preventing data loss when the file already contains
	// records from a previous run. O_CREATE creates the file on first use.
	// 0600: owner read/write only — protects crawl metadata on shared hosts.
	f, err := os.OpenFile(cleaned, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint %q: %w", cleaned, err)
	}
	w := bufio.NewWriterSize(f, checkpointWriteBufferBytes)
	enc := json.NewEncoder(w) // shares w; Encode writes JSON + '\n' atomically
	return &Checkpoint{
		path:   cleaned,
		file:   f,
		writer: w,
		enc:    enc,
	}, nil
}

// LoadCompleted reads the checkpoint file and returns the set of verse keys
// that have already been fetched. Each key has the form "lang:passageID"
// (e.g. "english:GEN.1.1").
//
// The map is pre-sized from the file's byte count (using estimatedBytesPerCheckpointLine)
// to avoid O(log n) rehash cycles on large partial-resume runs. Only PassageID
// and Lang are decoded per line (via verseCheckpointKey) — skipping the Content
// field prevents allocating ~4.7 MB of verse text strings that are immediately discarded.
//
// path is canonicalised via filepath.Abs before opening.
// If the file does not exist yet, an empty set is returned (not an error).
// Malformed or unreadable lines are silently skipped so a corrupted tail
// (e.g. from a SIGKILL during a write) does not block a resume.
// Genuine I/O errors mid-scan are returned so callers are not misled into
// treating a partial read as a complete checkpoint.
func LoadCompleted(path string) (map[verseKey]struct{}, error) {
	cleaned, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve checkpoint path %q: %w", path, err)
	}

	f, err := os.Open(cleaned)
	if os.IsNotExist(err) {
		return make(map[verseKey]struct{}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open checkpoint for reading %q: %w", cleaned, err)
	}
	defer f.Close()

	// Estimate completed verse count from file size to pre-allocate the map
	// with a single backing array, preventing ~16 rehash cycles that would
	// otherwise occur when loading a full 62,200-verse corpus from scratch.
	estimatedLines := 0
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > 0 {
		estimatedLines = int(fi.Size()) / estimatedBytesPerCheckpointLine
	}
	// verseKey struct keys avoid string concat allocations on every insert/lookup.
	completed := make(map[verseKey]struct{}, estimatedLines)

	scanner := bufio.NewScanner(f)
	// Expand the scanner buffer: the default 64 KB max-token size can be
	// exceeded by verbose verse content. 1 MB is a generous cap that prevents
	// unbounded memory use from a single malformed or unusually long line.
	scanner.Buffer(make([]byte, ScannerInitialBuf), ScannerMaxBuf)

	var lastGoodLine int
	for scanner.Scan() {
		lastGoodLine++
		// Decode only the two fields needed for deduplication — avoids
		// allocating the Content string (100-500 bytes) on every line.
		var key verseCheckpointKey
		if err := json.Unmarshal(scanner.Bytes(), &key); err != nil {
			continue // skip corrupted/truncated lines (e.g. SIGKILL mid-write)
		}
		// struct literal is stack-allocated; existing string headers are reused —
		// zero heap allocation versus the previous lang+":"+passageID concat.
		completed[verseKey{lang: key.Lang, passageID: key.PassageID}] = struct{}{}
	}

	// Distinguish genuine I/O errors (EIO, ENOSPC, network mount failure) from
	// a merely truncated final line — the latter is safe to ignore on resume,
	// but the former would cause us to return a falsely sparse completed set and
	// re-fetch all subsequent verses unnecessarily.
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf(
			"read checkpoint %q (stopped after line %d): %w",
			cleaned, lastGoodLine, err,
		)
	}
	return completed, nil
}

// checkpointKey is kept for internal use but replaced by verseKey struct in
// the hot path. This alias is no longer called; verseKey is used everywhere.
// Retained only to avoid breaking any external tools that scan for this symbol.

// Append serialises rec as a JSON line and appends it to the checkpoint's
// buffered writer. The buffer is flushed to the OS on Close.
// It is safe to call from multiple goroutines; the mutex serialises writes.
//
// json.Encoder.Encode writes JSON + '\n' directly into the bufio.Writer buffer —
// no intermediate []byte is allocated. Over 62,200 verses this eliminates
// ~16.8 MB of transient heap churn compared to json.Marshal + Write.
//
// Using a bufio.Writer reduces write(2) syscalls from one-per-verse (62,200)
// to one-per-buffer-flush (~260 at 64 KiB / 270 bytes-per-line).
func (c *Checkpoint) Append(rec VerseRecord) error {
	// The mutex guards concurrent callers even though the crawler funnels all
	// writes through a single goroutine in practice — keeping Append safe for
	// any future caller that might invoke it from multiple goroutines.
	c.mu.Lock()
	defer c.mu.Unlock()
	// enc.Encode writes JSON + '\n' atomically in one internal write to c.writer;
	// no intermediate []byte allocation unlike json.Marshal.
	if err := c.enc.Encode(rec); err != nil {
		return fmt.Errorf("encode verse record to checkpoint (passage=%s lang=%s): %w",
			rec.PassageID, rec.Lang, err)
	}
	return nil
}

// LoadCompleted reads this checkpoint's file and returns the set of verse keys
// that have already been fetched. It is a convenience wrapper around the
// package-level LoadCompleted function using the checkpoint's own path.
func (c *Checkpoint) LoadCompleted() (map[verseKey]struct{}, error) {
	return LoadCompleted(c.path)
}

// Close flushes the in-process write buffer, syncs the file to durable
// storage, and closes the underlying file descriptor.
// The mutex is held through all three operations to prevent a concurrent
// Append from interleaving bytes between Flush and Close — without the lock,
// a racing Append could buffer new data after the Flush that never reaches disk.
func (c *Checkpoint) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.writer.Flush(); err != nil {
		return fmt.Errorf("flush checkpoint buffer: %w", err)
	}
	if err := c.file.Sync(); err != nil {
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	return fmt.Errorf("close checkpoint %q: %w", c.path, c.file.Close())
}
