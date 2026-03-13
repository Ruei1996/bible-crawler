// Package utils provides small, stateless helpers shared across all commands.
package utils

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// Big5ToUTF8 converts Big5 encoded byte slice to UTF-8 string
func Big5ToUTF8(s []byte) (string, error) {
	reader := transform.NewReader(bytes.NewReader(s), traditionalchinese.Big5.NewDecoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(d), nil
}

// CleanText trims leading/trailing whitespace for normalized persistence.
func CleanText(s string) string {
	return strings.TrimSpace(s)
}
