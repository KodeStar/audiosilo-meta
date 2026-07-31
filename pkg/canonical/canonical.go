// Package canonical produces the one true on-disk JSON form for data files:
// object keys sorted alphabetically (recursively), 2-space indent, LF line
// endings, a single trailing newline, UTF-8 with no HTML escaping. Numbers are
// preserved exactly (no float rounding), array order is preserved.
//
// This package is PUBLIC API: it is consumed by the sibling audiosilo-sidecars
// tool as an ordinary module dependency, so its exported surface is a contract.
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
	out, err := FormatIndent(raw, "")
	if err != nil {
		return nil, err
	}
	// Format's result is a whole file, so it ends in exactly one newline.
	return append(out, '\n'), nil
}

// FormatIndent returns the canonical form of raw with every line after the
// first carrying prefix, and WITHOUT a trailing newline - the shape a value
// takes when it is nested inside a larger canonical document at that
// indentation. Format is FormatIndent at the top level plus the file's trailing
// newline.
//
// It exists so pkg/pack can render a pack entry at its in-file indentation
// through this package rather than reimplementing the decode/encode settings
// that define canonical form. Those settings live in exactly one place.
func FormatIndent(raw []byte, prefix string) ([]byte, error) {
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
	return Encode(v, prefix)
}

// Encode renders an already-decoded value in canonical form at the given line
// prefix, without a trailing newline. Decode with json.Decoder.UseNumber so
// numbers survive exactly.
func Encode(v any, prefix string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// enc.Encode appends exactly one newline; callers that want a file add it
	// back themselves.
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// IsCanonical reports whether raw already equals its canonical form.
func IsCanonical(raw []byte) (bool, error) {
	got, err := Format(raw)
	if err != nil {
		return false, err
	}
	return bytes.Equal(raw, got), nil
}
