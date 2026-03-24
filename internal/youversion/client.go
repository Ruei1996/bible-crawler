package youversion

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	headerAPIKey = "x-yvp-app-key"
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
// timeoutSec controls the per-request HTTP timeout.
func NewClient(baseURL, apiKey string, timeoutSec int) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
	}
}

// get performs a GET request to path (relative to baseURL), decodes the JSON
// response body into dest, and returns any HTTP or decode error.
func (c *Client) get(path string, dest any) error {
	u := c.baseURL + path
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", u, err)
	}
	req.Header.Set(headerAPIKey, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body %s: %w", u, err)
	}

	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{
			Method:     http.MethodGet,
			URL:        u,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode JSON from %s: %w", u, err)
	}
	return nil
}

// GetBibles returns all Bible versions whose language matches languageRange.
// languageRange uses BCP-47 subtag syntax, e.g. "en", "zh-Hans".
func (c *Client) GetBibles(languageRange string) (*BiblesResponse, error) {
	path := "/bibles?" + url.QueryEscape("language_ranges[]") + "=" + url.QueryEscape(languageRange)
	var result BiblesResponse
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetBibles(%q): %w", languageRange, err)
	}
	return &result, nil
}

// GetBible returns metadata for the Bible version with the given numeric ID.
func (c *Client) GetBible(bibleID int) (*BibleVersion, error) {
	path := "/bibles/" + strconv.Itoa(bibleID)
	var result BibleVersion
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetBible(%d): %w", bibleID, err)
	}
	return &result, nil
}

// GetBooks returns all books (with their full chapter and verse structure) for
// the given Bible version. This is the largest single API call — it returns
// all 66 books with nested chapters and verse identifiers in one response.
func (c *Client) GetBooks(bibleID int) (*BooksResponse, error) {
	path := "/bibles/" + strconv.Itoa(bibleID) + "/books"
	var result BooksResponse
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetBooks(bible=%d): %w", bibleID, err)
	}
	return &result, nil
}

// GetBook returns the detail for one book (including full chapter+verse tree).
// bookID is the USFM book abbreviation, e.g. "GEN", "MAT".
func (c *Client) GetBook(bibleID int, bookID string) (*BookData, error) {
	path := "/bibles/" + strconv.Itoa(bibleID) + "/books/" + url.PathEscape(bookID)
	var result BookData
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetBook(bible=%d book=%s): %w", bibleID, bookID, err)
	}
	return &result, nil
}

// GetChapter returns one chapter's verse list for the given book and 1-based
// chapter number. chapterNum is an integer (e.g. 1 for chapter 1).
func (c *Client) GetChapter(bibleID int, bookID string, chapterNum int) (*ChapterData, error) {
	path := fmt.Sprintf("/bibles/%d/books/%s/chapters/%d",
		bibleID, url.PathEscape(bookID), chapterNum)
	var result ChapterData
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetChapter(bible=%d book=%s chap=%d): %w", bibleID, bookID, chapterNum, err)
	}
	return &result, nil
}

// GetVerse returns the structural record for one verse.
// This returns only identifiers (id, passage_id, title), not verse text.
// Use GetPassage to retrieve the actual text content.
func (c *Client) GetVerse(bibleID int, bookID string, chapterNum, verseNum int) (*VerseData, error) {
	path := fmt.Sprintf("/bibles/%d/books/%s/chapters/%d/verses/%d",
		bibleID, url.PathEscape(bookID), chapterNum, verseNum)
	var result VerseData
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetVerse(bible=%d book=%s chap=%d verse=%d): %w",
			bibleID, bookID, chapterNum, verseNum, err)
	}
	return &result, nil
}

// GetVOTD returns all 366 verse-of-the-day entries (day → passage reference).
// The returned passage IDs (e.g. "ISA.43.18-19") are USFM passage references;
// use GetPassage to retrieve the verse text for each entry.
func (c *Client) GetVOTD() (*VOTDResponse, error) {
	var result VOTDResponse
	if err := c.get("/verse_of_the_days", &result); err != nil {
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
func (c *Client) GetPassage(bibleID int, passageID string) (*PassageData, error) {
	path := fmt.Sprintf("/bibles/%d/passages/%s", bibleID, url.PathEscape(passageID))
	var result PassageData
	if err := c.get(path, &result); err != nil {
		return nil, fmt.Errorf("GetPassage(bible=%d passage=%s): %w", bibleID, passageID, err)
	}
	return &result, nil
}
