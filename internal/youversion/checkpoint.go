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
	"sync"
	"time"
)

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
	mu   sync.Mutex
	path string
	file *os.File
}

// NewCheckpoint opens (or creates) the JSONL file at path in append mode and
// returns a ready-to-use Checkpoint. Call Close when the crawler exits.
func NewCheckpoint(path string) (*Checkpoint, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint %q: %w", path, err)
	}
	return &Checkpoint{path: path, file: f}, nil
}

// LoadCompleted reads the checkpoint file and returns the set of verse keys
// that have already been fetched. Each key has the form "lang:passageID"
// (e.g. "english:GEN.1.1").
//
// If the file does not exist yet, an empty set is returned (not an error).
// Malformed or unreadable lines are silently skipped so a corrupted tail
// (e.g. from a SIGKILL during a write) does not block a resume.
func LoadCompleted(path string) (map[string]bool, error) {
	completed := make(map[string]bool)

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return completed, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open checkpoint for reading %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow lines up to 1 MB (generous for verse text).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec VerseRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue // skip corrupted lines
		}
		completed[checkpointKey(rec.Lang, rec.PassageID)] = true
	}
	// Ignore scanner.Err() — a partial final line is tolerable on resume.
	return completed, nil
}

// checkpointKey returns the deduplication key for a lang+passageID pair.
func checkpointKey(lang, passageID string) string {
	return lang + ":" + passageID
}

// Append serialises rec as a JSON line and appends it to the checkpoint file.
// It is safe to call from multiple goroutines.
func (c *Checkpoint) Append(rec VerseRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal verse record: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

// LoadCompleted reads this checkpoint's file and returns the set of verse keys
// that have already been fetched. It is a convenience wrapper around the
// package-level LoadCompleted function using the checkpoint's own path.
func (c *Checkpoint) LoadCompleted() (map[string]bool, error) {
	return LoadCompleted(c.path)
}

// Close flushes and closes the underlying file.
func (c *Checkpoint) Close() error {
	if err := c.file.Sync(); err != nil {
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	return c.file.Close()
}
