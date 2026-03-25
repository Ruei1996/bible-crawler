package youversion

// client.go implements the HTTP transport layer for the YouVersion Platform
// API v1 (https://api.youversion.com/v1).
//
// All API calls funnel through the private get() helper, which injects the
// required x-yvp-app-key authentication header, reads the full response body,
// and returns a typed HTTPStatusError for non-200 responses — enabling callers
// such as fetchWithRetry to distinguish retryable (429, 5xx) from permanent
// (403, 404) failures without parsing error strings.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// headerAPIKey is the HTTP request header name for YouVersion Platform API
	// authentication. The API requires this header on every request.
	headerAPIKey = "x-yvp-app-key"

	// maxResponseBytes caps the success response body read at 10 MiB to prevent
	// OOM from a rogue or misconfigured server returning an unbounded payload.
	// The largest expected response (GetBooks, 66 books) is well under 1 MiB.
	maxResponseBytes = 10 << 20 // 10 MiB

	// defaultMaxIdleConnsPerHost is a conservative idle-connection pool size
	// that covers crawlers using up to ~50 concurrent workers without incurring
	// repeated TLS re-handshakes from connection churn. Callers that know their
	// exact worker count should use NewClientWithConcurrency to tune this value.
	defaultMaxIdleConnsPerHost = 64

	// defaultIdleConnTimeoutSec matches Go's http.DefaultTransport behaviour,
	// ensuring idle connections are reclaimed after a predictable quiet period.
	defaultIdleConnTimeoutSec = 90

	// maxErrorBodyBytes caps how much of a non-2xx response body is buffered
	// into HTTPStatusError.Body, preventing OOM on unexpectedly large error
	// pages (e.g. a WAF returning a full HTML document on a 403 or 429).
	maxErrorBodyBytes = 4 * 1024
)

// HTTPStatusError is returned by get() when the server responds with a
// non-200 HTTP status. Callers can inspect the StatusCode field via
// errors.As to decide whether to retry (5xx, 429) or skip (404).
// Error() preserves the original "GET <url>: status <code> — <body>" format
// so that all existing tests that check err.Error() continue to pass.
type HTTPStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s %s: status %d — %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// Client is an HTTP client for the YouVersion Platform API.
// All requests include the required x-yvp-app-key header automatically.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient returns a Client configured with the given base URL and API key.
// timeoutSec controls the per-request HTTP timeout. The underlying transport
// uses defaultMaxIdleConnsPerHost idle connections per host; for crawlers with
// a known worker count, prefer NewClientWithConcurrency to size the pool exactly.
//
// Returns an error if baseURL does not use the https:// scheme — an http://
// base URL would transmit x-yvp-app-key in plaintext and allow SSRF to
// internal network services on cloud hosts.
func NewClient(baseURL, apiKey string, timeoutSec int) (*Client, error) {
	return NewClientWithConcurrency(baseURL, apiKey, timeoutSec, defaultMaxIdleConnsPerHost)
}

// NewClientWithConcurrency returns a Client whose HTTP transport idle-connection
// pool is sized to maxConnsPerHost. Setting this to the crawler's worker count
// ensures that each worker can reuse a kept-alive connection across requests,
// eliminating repeated TCP+TLS handshakes (~150 ms each) from connection churn.
//
// Rule of thumb: set maxConnsPerHost = WORKERS (e.g. 20 for the default config).
//
// Returns an error if baseURL does not begin with https://. An http:// base URL
// would send the x-yvp-app-key authentication header in cleartext on every
// request and could redirect traffic to internal network services (SSRF/CWE-918).
func NewClientWithConcurrency(baseURL, apiKey string, timeoutSec, maxConnsPerHost int) (*Client, error) {
	// Enforce HTTPS: the API key is injected into every request header.
	// Transmitting it over plain HTTP exposes credentials to passive eavesdroppers
	// and, on cloud hosts, allows an attacker to redirect traffic to the instance
	// metadata service (169.254.169.254) via a crafted YOUVERSION_BASE_URL.
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(baseURL)), "https://") {
		return nil, fmt.Errorf(
			"YOUVERSION_BASE_URL must use https://, got %q: "+
				"http would expose x-yvp-app-key in plaintext and enable SSRF", baseURL)
	}
	// Normalize baseURL once at construction: trim trailing slash so get() can
	// concatenate path directly without calling TrimRight on every request.
	// c.baseURL is immutable after construction; this is idempotent and cheap here.
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if maxConnsPerHost <= 0 {
		maxConnsPerHost = defaultMaxIdleConnsPerHost
	}
	transport := &http.Transport{
		// Size idle pool to the caller's concurrency level so every worker
		// can hold a persistent connection without evicting a sibling's.
		MaxIdleConns:        maxConnsPerHost * 2,
		MaxIdleConnsPerHost: maxConnsPerHost,
		IdleConnTimeout:     defaultIdleConnTimeoutSec * time.Second,
	}
	return &Client{
		baseURL: trimmedBaseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   time.Duration(timeoutSec) * time.Second,
			Transport: transport,
		},
	}, nil
}

// get performs a GET request to path (relative to baseURL), decodes the JSON
// response body into dest, and returns any HTTP or decode error.
// ctx is propagated into the HTTP request so context cancellation (e.g. Ctrl+C)
// aborts in-flight TCP reads immediately rather than waiting for the OS timeout.
func (c *Client) get(ctx context.Context, path string, dest any) error {
	// baseURL is pre-trimmed at construction — direct concat avoids calling
	// strings.TrimRight on every one of the ~62,200 verse requests.
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", u, err)
	}
	// Inject the app key on every outgoing request.
	req.Header.Set(headerAPIKey, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	// Cap success body reads to prevent OOM from an unexpected large response.
	// Error bodies use a smaller cap (maxErrorBodyBytes) defined below.
	if resp.StatusCode == http.StatusOK {
		limited := io.LimitReader(resp.Body, maxResponseBytes)
		// Streaming decode: json.NewDecoder reads into a small internal buffer
		// and decodes directly into dest — no intermediate []byte is allocated.
		// Over 62,200 verse fetches this eliminates ~18.6 MB of transient buffers
		// and reduces GC pressure inside the parallel worker hot path.
		if err := json.NewDecoder(limited).Decode(dest); err != nil {
			return fmt.Errorf("decode JSON from %s: %w", u, err)
		}
		return nil
	}

	// Non-200: read error body (capped) for diagnostics, then return typed error.
	// Draining the body lets the transport reuse the keep-alive connection.
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	errBodyStr := string(errBody)
	if len(errBodyStr) == maxErrorBodyBytes {
		errBodyStr += "…[truncated]"
	}
	return &HTTPStatusError{
		Method:     http.MethodGet,
		URL:        u,
		StatusCode: resp.StatusCode,
		Body:       errBodyStr,
	}
}

// GetBibles returns all Bible versions whose language matches languageRange.
// languageRange uses BCP-47 subtag syntax, e.g. "en", "zh-Hans".
func (c *Client) GetBibles(ctx context.Context, languageRange string) (*BiblesResponse, error) {
	path := "/bibles?" + url.QueryEscape("language_ranges[]") + "=" + url.QueryEscape(languageRange)
	var result BiblesResponse
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetBibles(%q): %w", languageRange, err)
	}
	return &result, nil
}

// GetBible returns metadata for the Bible version with the given numeric ID.
func (c *Client) GetBible(ctx context.Context, bibleID int) (*BibleVersion, error) {
	path := "/bibles/" + strconv.Itoa(bibleID)
	var result BibleVersion
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetBible(%d): %w", bibleID, err)
	}
	return &result, nil
}

// GetBooks returns all books (with their full chapter and verse structure) for
// the given Bible version. This is the largest single API call — it returns
// all 66 books with nested chapters and verse identifiers in one response.
func (c *Client) GetBooks(ctx context.Context, bibleID int) (*BooksResponse, error) {
	path := "/bibles/" + strconv.Itoa(bibleID) + "/books"
	var result BooksResponse
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetBooks(bible=%d): %w", bibleID, err)
	}
	return &result, nil
}

// GetBook returns the detail for one book (including full chapter+verse tree).
// bookID is the USFM book abbreviation, e.g. "GEN", "MAT".
func (c *Client) GetBook(ctx context.Context, bibleID int, bookID string) (*BookData, error) {
	path := "/bibles/" + strconv.Itoa(bibleID) + "/books/" + url.PathEscape(bookID)
	var result BookData
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetBook(bible=%d book=%s): %w", bibleID, bookID, err)
	}
	return &result, nil
}

// GetChapter returns one chapter's verse list for the given book and 1-based
// chapter number. chapterNum is an integer (e.g. 1 for chapter 1).
func (c *Client) GetChapter(ctx context.Context, bibleID int, bookID string, chapterNum int) (*ChapterData, error) {
	path := fmt.Sprintf("/bibles/%d/books/%s/chapters/%d",
		bibleID, url.PathEscape(bookID), chapterNum)
	var result ChapterData
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetChapter(bible=%d book=%s chap=%d): %w", bibleID, bookID, chapterNum, err)
	}
	return &result, nil
}

// GetVerse returns the structural record for one verse.
// This returns only identifiers (id, passage_id, title), not verse text.
// Use GetPassage to retrieve the actual text content.
func (c *Client) GetVerse(ctx context.Context, bibleID int, bookID string, chapterNum, verseNum int) (*VerseData, error) {
	path := fmt.Sprintf("/bibles/%d/books/%s/chapters/%d/verses/%d",
		bibleID, url.PathEscape(bookID), chapterNum, verseNum)
	var result VerseData
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetVerse(bible=%d book=%s chap=%d verse=%d): %w",
			bibleID, bookID, chapterNum, verseNum, err)
	}
	return &result, nil
}

// GetVOTD returns all 366 verse-of-the-day entries (day → passage reference).
// The returned passage IDs (e.g. "ISA.43.18-19") are USFM passage references;
// use GetPassage to retrieve the verse text for each entry.
func (c *Client) GetVOTD(ctx context.Context) (*VOTDResponse, error) {
	var result VOTDResponse
	if err := c.get(ctx, "/verse_of_the_days", &result); err != nil {
		return nil, fmt.Errorf("GetVOTD: %w", err)
	}
	return &result, nil
}

// GetPassage returns the text content for a Bible passage by USFM passage ID.
// passageID supports single verses ("GEN.1.1"), ranges ("GEN.1.1-3"), or
// whole chapters ("GEN.1").
//
// Access depends on the Bible version's licensing agreement. Translations
// without a publisher restriction (e.g. NIV11 ID 111, CSB 中文標準譯本 ID 312) return
// text freely. Restricted translations (e.g. 新標點和合本 ID 46) return a 403 error.
func (c *Client) GetPassage(ctx context.Context, bibleID int, passageID string) (*PassageData, error) {
	path := fmt.Sprintf("/bibles/%d/passages/%s", bibleID, url.PathEscape(passageID))
	var result PassageData
	if err := c.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("GetPassage(bible=%d passage=%s): %w", bibleID, passageID, err)
	}
	return &result, nil
}
