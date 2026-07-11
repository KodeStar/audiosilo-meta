// Package canonical produces the one true on-disk JSON form for data files:
// object keys sorted alphabetically (recursively), 2-space indent, LF line
// endings, a single trailing newline, UTF-8 with no HTML escaping. Numbers are
// preserved exactly (no float rounding), array order is preserved.
package canonical

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Format returns the canonical form of raw. It fails if raw is not valid JSON
// or carries trailing content after the top-level value.
func Format(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// Reject trailing tokens (e.g. two concatenated JSON values).
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing content after JSON value")
		}
		return nil, err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// enc.Encode already appends exactly one trailing newline.
	return buf.Bytes(), nil
}

// IsCanonical reports whether raw already equals its canonical form.
func IsCanonical(raw []byte) (bool, error) {
	got, err := Format(raw)
	if err != nil {
		return false, err
	}
	return bytes.Equal(raw, got), nil
}
