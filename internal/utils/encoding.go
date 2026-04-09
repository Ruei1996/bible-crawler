// Package utils provides small, stateless helpers shared across all commands.
package utils

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// Big5ToUTF8 converts a Big5-encoded byte slice to a UTF-8 string.
//
// springbible.fhl.net serves its Chinese (和合本 CUV) pages in Big5 encoding —
// the traditional character encoding historically common on Taiwanese websites.
// All English pages from the same site are UTF-8 / ASCII and do not need
// this conversion. Using the golang.org/x/text transform pipeline avoids the
// allocation overhead of an intermediate string while handling the full Big5
// extension character set (UAO, ETen) that standard GB2312 decoders miss.
func Big5ToUTF8(s []byte) (string, error) {
	reader := transform.NewReader(bytes.NewReader(s), traditionalchinese.Big5.NewDecoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(d), nil
}

// CleanText trims leading and trailing whitespace from s before it is persisted
// to the database. Scraped HTML text nodes frequently carry incidental newlines
// and indent spaces that are invisible in a browser but appear in DB queries.
func CleanText(s string) string {
	return strings.TrimSpace(s)
}
