package library

import (
	"errors"
	"fmt"
	"io"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

var errImportUnsupportedEncoding = errors.New("library: import XML declares an encoding that is not supported")

// importCharsetReader is the decoding step of UC3.3 (specs/2026-10-28-writer-first-success.md):
// an XML part that declares an encoding other than UTF-8 — an FB2 from a Russian library is often
// windows-1251 — is decoded to UTF-8 as it is read, by the WHATWG label of its declaration.
// encoding/xml asks only for labels other than UTF-8, so a UTF-8 part is read as before. A label
// with no encoding, or one WHATWG maps to the replacement encoding, is refused instead of turning
// the book into replacement characters.
func importCharsetReader(label string, input io.Reader) (io.Reader, error) {
	charset, err := htmlindex.Get(label)
	if err != nil || charset == encoding.Replacement {
		return nil, fmt.Errorf("%w: %q", errImportUnsupportedEncoding, label)
	}
	return charset.NewDecoder().Reader(input), nil
}
