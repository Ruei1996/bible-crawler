package spec_test

import (
	"encoding/json"
	"os"
	"testing"

	"bible-crawler/internal/spec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Relative to internal/spec/ (the test working directory).
const (
	realZHPath = "../../bible_books_zh.json"
	realENPath = "../../bible_books_en.json"
)

func TestLoad_RealFiles(t *testing.T) {
	s, err := spec.Load(realZHPath, realENPath)
	require.NoError(t, err)
	assert.Len(t, s.ZH, 66)
	assert.Len(t, s.EN, 66)
}

func TestLoad_ZHNotFound(t *testing.T) {
	_, err := spec.Load("/nonexistent/path.json", realENPath)
	require.Error(t, err)
}

func TestLoad_ENNotFound(t *testing.T) {
	_, err := spec.Load(realZHPath, "/nonexistent/path.json")
	require.Error(t, err)
}

func writeTempJSON(t *testing.T, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp("", "spec-test-*.json")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, err = f.Write(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestLoad_ZHInvalidJSON(t *testing.T) {
	zhPath := writeTempJSON(t, []byte("invalid json"))
	_, err := spec.Load(zhPath, realENPath)
	require.Error(t, err)
}

func TestLoad_ENInvalidJSON(t *testing.T) {
	enPath := writeTempJSON(t, []byte("{not valid json}"))
	_, err := spec.Load(realZHPath, enPath)
	require.Error(t, err)
}

func makeZHDoc(numBooks int) []byte {
	type book struct {
		Number           int            `json:"number"`
		TestamentZH      string         `json:"testament_zh"`
		NameZH           string         `json:"name_zh"`
		TotalChapters    int            `json:"total_chapters"`
		TotalVerses      int            `json:"total_verses"`
		VersesPerChapter map[string]int `json:"verses_per_chapter"`
	}
	books := make([]book, numBooks)
	for i := 0; i < numBooks; i++ {
		books[i] = book{
			Number: i + 1, TestamentZH: "舊約", NameZH: "Genesis",
			TotalChapters: 1, TotalVerses: 31,
			VersesPerChapter: map[string]int{"01-[31]": 31},
		}
	}
	raw, _ := json.Marshal(map[string]interface{}{"books": books})
	return raw
}

func makeENDoc(numBooks int) []byte {
	type book struct {
		Number           int            `json:"number"`
		Testament        string         `json:"testament"`
		NameEN           string         `json:"name_en"`
		TotalChapters    int            `json:"total_chapters"`
		TotalVerses      int            `json:"total_verses"`
		VersesPerChapter map[string]int `json:"verses_per_chapter"`
	}
	books := make([]book, numBooks)
	for i := 0; i < numBooks; i++ {
		books[i] = book{
			Number: i + 1, Testament: "OT", NameEN: "Genesis",
			TotalChapters: 1, TotalVerses: 31,
			VersesPerChapter: map[string]int{"01-[31]": 31},
		}
	}
	raw, _ := json.Marshal(map[string]interface{}{"books": books})
	return raw
}

func TestLoad_ZHWrongBookCount(t *testing.T) {
	zhPath := writeTempJSON(t, makeZHDoc(1))
	_, err := spec.Load(zhPath, realENPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "66")
}

func TestLoad_ENWrongBookCount(t *testing.T) {
	enPath := writeTempJSON(t, makeENDoc(1))
	_, err := spec.Load(realZHPath, enPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "66")
}

func TestBookSpec_VerseCount(t *testing.T) {
	b := &spec.BookSpec{
		Number:        1,
		TotalChapters: 2,
		VersesPerChapter: map[string]int{
			"01-[31]": 31,
			"02-[25]": 25,
		},
	}

	count, err := b.VerseCount(1)
	require.NoError(t, err)
	assert.Equal(t, 31, count)

	count, err = b.VerseCount(2)
	require.NoError(t, err)
	assert.Equal(t, 25, count)

	_, err = b.VerseCount(0)
	require.Error(t, err)

	_, err = b.VerseCount(3)
	require.Error(t, err)

	// Second call hits the cached path.
	count, err = b.VerseCount(1)
	require.NoError(t, err)
	assert.Equal(t, 31, count)
}

func TestBookSpec_BuildVerseCounts_BadKey_NoDash(t *testing.T) {
	b := &spec.BookSpec{
		Number:           1,
		TotalChapters:    1,
		VersesPerChapter: map[string]int{"nodash": 31},
	}
	_, err := b.VerseCount(1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nodash")
}

func TestBookSpec_BuildVerseCounts_BadChapterNum(t *testing.T) {
	b := &spec.BookSpec{
		Number:           1,
		TotalChapters:    1,
		VersesPerChapter: map[string]int{"XX-[31]": 31},
	}
	_, err := b.VerseCount(1)
	require.Error(t, err)
}

func TestBibleSpec_GlobalChapStarts(t *testing.T) {
	s, err := spec.Load(realZHPath, realENPath)
	require.NoError(t, err)
	starts := s.GlobalChapStarts()
	// Genesis starts at global chapter 1.
	assert.Equal(t, 1, starts[0])
	// Exodus starts right after Genesis's 50 chapters.
	assert.Equal(t, 51, starts[1])
}
