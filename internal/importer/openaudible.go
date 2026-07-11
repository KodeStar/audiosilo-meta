package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// rawBook is one entry of an OpenAudible books.json export. Every scalar may
// arrive as a string, number, bool, or null, so fields are decoded lazily
// through the coercion helpers rather than into typed struct fields.
type rawBook map[string]any

// rawChapter is one entry of a book's chapters array, same loose typing.
type rawChapter map[string]any

// parseBooks decodes an OpenAudible export (a top-level JSON array of objects)
// into rawBooks. Numbers are preserved as json.Number so integer offsets keep
// their exact value.
func parseBooks(data []byte) ([]rawBook, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var books []rawBook
	if err := dec.Decode(&books); err != nil {
		return nil, fmt.Errorf("parse books.json: %w", err)
	}
	return books, nil
}

// str returns the field as a trimmed string. Numbers render via their literal
// form ("3", "0.5"); bools render as "true"/"false"; nil and absent yield "".
func (b rawBook) str(key string) string { return coerceStr(b[key]) }

// chapters returns the book's chapters array as rawChapters, or nil.
func (b rawBook) chapters() []rawChapter {
	arr, ok := b["chapters"].([]any)
	if !ok {
		return nil
	}
	out := make([]rawChapter, 0, len(arr))
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			out = append(out, rawChapter(m))
		}
	}
	return out
}

func (c rawChapter) str(key string) string { return coerceStr(c[key]) }

// intVal returns an integer field. ok is false when the value is missing, null,
// or not parseable as a whole number (a float like 12.9 truncates toward zero).
func (b rawBook) intVal(key string) (int64, bool) { return coerceInt(b[key]) }

func (c rawChapter) intVal(key string) (int64, bool) { return coerceInt(c[key]) }

// boolPtr returns a tri-state boolean: nil when the field is absent, null, or
// otherwise not an explicit boolean; a pointer to the value when it is an
// explicit true/false (a bool, or the strings "true"/"false").
func (b rawBook) boolPtr(key string) *bool { return coerceBoolPtr(b[key]) }

func coerceStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

func coerceInt(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	case float64:
		return int64(x), true
	default:
		return 0, false
	}
}

func coerceBoolPtr(v any) *bool {
	switch x := v.(type) {
	case bool:
		return &x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true":
			t := true
			return &t
		case "false":
			f := false
			return &f
		}
		return nil
	default:
		return nil
	}
}
