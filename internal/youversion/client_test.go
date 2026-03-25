package youversion

// White-box tests for the HTTP client. Living in package youversion (not
// youversion_test) allows tests to replace the unexported httpClient field
// with a custom http.Client (to inject a failing transport) and to exercise
// every branch of the internal get() helper, including the ReadAll error path.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// Custom transports for error injection
// ──────────────────────────────────────────────────────────────────────────────

// errBodyTransport returns a 200 OK response whose body fails on the first Read.
// This exercises the io.ReadAll error branch in Client.get.
type errBodyTransport struct{}

func (errBodyTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(&errReader{}),
		Header:     make(http.Header),
	}, nil
}

// errReader simulates a broken body reader.
type errReader struct{}

func (*errReader) Read(_ []byte) (int, error) { return 0, fmt.Errorf("simulated read error") }

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// newTestClient creates a Client pointing at baseURL with a short timeout.
// httptest servers use http://, so we bypass the https enforcement by
// constructing the Client directly rather than through the public constructors.
func newTestClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  "test-api-key",
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// jsonServer returns an httptest.Server that responds to every request with
// the JSON encoding of body and status statusCode.
func jsonServer(t *testing.T, statusCode int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if body != nil {
			b, _ := json.Marshal(body)
			w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rawServer returns an httptest.Server that responds with a raw string body.
func rawServer(t *testing.T, statusCode int, raw string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		fmt.Fprint(w, raw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ──────────────────────────────────────────────────────────────────────────────
// NewClient
// ──────────────────────────────────────────────────────────────────────────────

func TestNewClient(t *testing.T) {
	c, err := NewClient("https://api.example.com/v1", "mykey", 30)
	require.NoError(t, err)
	assert.NotNil(t, c)
	assert.Equal(t, "https://api.example.com/v1", c.baseURL)
	assert.Equal(t, "mykey", c.apiKey)
	assert.Equal(t, 30*time.Second, c.httpClient.Timeout)
}

func TestNewClient_HTTPSchemeRejected(t *testing.T) {
	_, err := NewClient("http://api.example.com/v1", "mykey", 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https://")
}

// ──────────────────────────────────────────────────────────────────────────────
// get — error branches
// ──────────────────────────────────────────────────────────────────────────────

func TestGet_NewRequestError(t *testing.T) {
	// An invalid URL scheme causes http.NewRequest to fail.
	// Construct directly (bypassing the https guard) since we want to test get().
	c := &Client{baseURL: "://invalid-url", apiKey: "key", httpClient: &http.Client{Timeout: 5 * time.Second}}
	var result BiblesResponse
	err := c.get(context.Background(), "/test", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build request")
}

func TestGet_DoError(t *testing.T) {
	// Create and immediately close a server so the subsequent request fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	c := newTestClient(srv.URL)
	var result BiblesResponse
	err := c.get(context.Background(), "/test", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GET")
}

func TestGet_ReadBodyError(t *testing.T) {
	// Inject a transport whose response body always fails on Read.
	// Since we switched to json.NewDecoder (streaming), the read error surfaces
	// as a decode error: "decode JSON from ...: <read error>".
	c := newTestClient("http://unused.example.com")
	c.httpClient = &http.Client{Transport: errBodyTransport{}}
	var result BiblesResponse
	err := c.get(context.Background(), "/test", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode JSON from")
}

func TestGet_NonOKStatus(t *testing.T) {
	srv := rawServer(t, http.StatusForbidden, `{"message":"Access denied"}`)
	c := newTestClient(srv.URL)
	var result BiblesResponse
	err := c.get(context.Background(), "/bibles/46", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestGet_InvalidJSON(t *testing.T) {
	srv := rawServer(t, http.StatusOK, `not-valid-json`)
	c := newTestClient(srv.URL)
	var result BiblesResponse
	err := c.get(context.Background(), "/test", &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode JSON")
}

func TestGet_Success(t *testing.T) {
	resp := BiblesResponse{Data: []BibleVersion{{ID: 1, Title: "Test Bible"}}}
	srv := jsonServer(t, http.StatusOK, resp)
	c := newTestClient(srv.URL)
	var result BiblesResponse
	err := c.get(context.Background(), "/bibles?language_ranges%5B%5D=en", &result)
	require.NoError(t, err)
	assert.Len(t, result.Data, 1)
	assert.Equal(t, "Test Bible", result.Data[0].Title)
}

// ──────────────────────────────────────────────────────────────────────────────
// GetBibles
// ──────────────────────────────────────────────────────────────────────────────

func TestGetBibles_Success(t *testing.T) {
	payload := BiblesResponse{Data: []BibleVersion{{ID: 111, Abbreviation: "NIV11"}}}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetBibles(context.Background(), "en")
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	assert.Equal(t, "NIV11", result.Data[0].Abbreviation)
}

func TestGetBibles_Error(t *testing.T) {
	srv := rawServer(t, http.StatusInternalServerError, `{"message":"error"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetBibles(context.Background(), "en")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBibles")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetBible
// ──────────────────────────────────────────────────────────────────────────────

func TestGetBible_Success(t *testing.T) {
	payload := BibleVersion{ID: 111, Title: "New International Version 2011"}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetBible(context.Background(), 111)
	require.NoError(t, err)
	assert.Equal(t, "New International Version 2011", result.Title)
}

func TestGetBible_Error(t *testing.T) {
	srv := rawServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetBible(context.Background(), 9999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBible")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetBooks
// ──────────────────────────────────────────────────────────────────────────────

func TestGetBooks_Success(t *testing.T) {
	payload := BooksResponse{Data: []BookData{{ID: "GEN", Title: "Genesis"}}}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetBooks(context.Background(), 111)
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	assert.Equal(t, "GEN", result.Data[0].ID)
}

func TestGetBooks_Error(t *testing.T) {
	srv := rawServer(t, http.StatusUnauthorized, `{"message":"Unauthorized"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetBooks(context.Background(), 111)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBooks")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetBook
// ──────────────────────────────────────────────────────────────────────────────

func TestGetBook_Success(t *testing.T) {
	payload := BookData{ID: "GEN", Title: "Genesis", FullTitle: "Genesis"}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetBook(context.Background(), 111, "GEN")
	require.NoError(t, err)
	assert.Equal(t, "GEN", result.ID)
}

func TestGetBook_Error(t *testing.T) {
	srv := rawServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetBook(context.Background(), 111, "INVALID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetBook")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetChapter
// ──────────────────────────────────────────────────────────────────────────────

func TestGetChapter_Success(t *testing.T) {
	payload := ChapterData{ID: "1", PassageID: "GEN.1", Title: "1"}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetChapter(context.Background(), 111, "GEN", 1)
	require.NoError(t, err)
	assert.Equal(t, "GEN.1", result.PassageID)
}

func TestGetChapter_Error(t *testing.T) {
	srv := rawServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetChapter(context.Background(), 111, "GEN", 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetChapter")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetVerse
// ──────────────────────────────────────────────────────────────────────────────

func TestGetVerse_Success(t *testing.T) {
	payload := VerseData{ID: "1", PassageID: "GEN.1.1", Title: "1"}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetVerse(context.Background(), 111, "GEN", 1, 1)
	require.NoError(t, err)
	assert.Equal(t, "GEN.1.1", result.PassageID)
}

func TestGetVerse_Error(t *testing.T) {
	srv := rawServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetVerse(context.Background(), 111, "GEN", 1, 999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetVerse")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetVOTD
// ──────────────────────────────────────────────────────────────────────────────

func TestGetVOTD_Success(t *testing.T) {
	payload := VOTDResponse{Data: []VOTDEntry{{Day: 1, PassageID: "ISA.43.18-19"}}}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetVOTD(context.Background())
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	assert.Equal(t, "ISA.43.18-19", result.Data[0].PassageID)
}

func TestGetVOTD_Error(t *testing.T) {
	srv := rawServer(t, http.StatusInternalServerError, `{"message":"error"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetVOTD(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetVOTD")
}

// ──────────────────────────────────────────────────────────────────────────────
// GetPassage
// ──────────────────────────────────────────────────────────────────────────────

func TestGetPassage_Success(t *testing.T) {
	payload := PassageData{
		ID:        "GEN.1.1",
		Content:   "In the beginning God created the heavens and the earth.",
		Reference: "Genesis 1:1",
	}
	srv := jsonServer(t, http.StatusOK, payload)
	c := newTestClient(srv.URL)
	result, err := c.GetPassage(context.Background(), 111, "GEN.1.1")
	require.NoError(t, err)
	assert.Equal(t, "GEN.1.1", result.ID)
	assert.Contains(t, result.Content, "beginning")
}

func TestGetPassage_Error_Forbidden(t *testing.T) {
	// Simulates the 403 returned for licensed Bibles without access.
	srv := rawServer(t, http.StatusForbidden, `{"message":"Access denied for 46"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetPassage(context.Background(), 46, "GEN.1.1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetPassage")
}

func TestGetPassage_Error_NotFound(t *testing.T) {
	srv := rawServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	c := newTestClient(srv.URL)
	_, err := c.GetPassage(context.Background(), 111, "INVALID.99.99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetPassage")
}
