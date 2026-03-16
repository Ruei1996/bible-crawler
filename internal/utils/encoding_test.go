package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

func TestBig5ToUTF8_ValidChineseText(t *testing.T) {
	original := "你好世界"
	encoder := traditionalchinese.Big5.NewEncoder()
	big5Bytes, _, err := transform.Bytes(encoder, []byte(original))
	require.NoError(t, err)
	result, err := Big5ToUTF8(big5Bytes)
	require.NoError(t, err)
	assert.Equal(t, original, result)
}

func TestBig5ToUTF8_EmptyBytes(t *testing.T) {
	result, err := Big5ToUTF8([]byte{})
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestBig5ToUTF8_ASCIIText(t *testing.T) {
	// ASCII is a strict subset of Big5; round-trip must be lossless.
	result, err := Big5ToUTF8([]byte("Hello World"))
	require.NoError(t, err)
	assert.Equal(t, "Hello World", result)
}

func TestCleanText_LeadingTrailingWhitespace(t *testing.T) {
	assert.Equal(t, "hello", CleanText("  hello  "))
}

func TestCleanText_OnlyWhitespace(t *testing.T) {
	assert.Equal(t, "", CleanText("   \t\n  "))
}

func TestCleanText_EmptyString(t *testing.T) {
	assert.Equal(t, "", CleanText(""))
}

func TestCleanText_NoWhitespace(t *testing.T) {
	assert.Equal(t, "hello", CleanText("hello"))
}

func TestCleanText_InnerWhitespace(t *testing.T) {
	// TrimSpace only strips leading/trailing; inner spaces are preserved.
	assert.Equal(t, "hello world", CleanText("  hello world  "))
}
