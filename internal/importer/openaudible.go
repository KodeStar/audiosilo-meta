package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// rawBook is one loosely-typed export entry (an OpenAudible books.json object,
// or a Libation object before normalization). Every scalar may arrive as a
// string, number, bool, or null, so fields are decoded lazily through the
// coercion helpers rather than into typed struct fields.
type rawBook map[string]any

// rawChapter is one entry of a book's chapters array, same loose typing.
type rawChapter map[string]any

// wrapperKeys are the object keys an export list may ride under when a tool
// wraps its array in an envelope (mirrors the site parser's extractEntries, so
// a file the /import page accepts also imports here).
var wrapperKeys = []string{"Books", "books", "Items", "items", "Library", "library"}

// errNotAnEntryList is the refusal for a file that is neither an array of
// entries nor an envelope holding one. It is a function rather than an inline
// Errorf because the libex STREAMING reader (libexselect.go) must refuse the
// same file with the same words: an operator who is told to select a file and
// then to import it should never see two descriptions of one problem.
func errNotAnEntryList(label string) error {
	return fmt.Errorf("parse %s: expected a JSON array of objects (or a wrapper object holding one)", label)
}

// decodeEntries decodes an export's entries into rawBooks: a top-level JSON
// array of objects, or a wrapper object carrying the array under one of
// wrapperKeys. Non-object entries are skipped (same as the site parser).
// Numbers are preserved as json.Number so integer offsets keep their exact
// value. label names the source in the error ("books.json", "libation export").
//
// Content AFTER the first value is an error, not a silent truncation: two
// concatenated arrays ("[...][...]") are a mis-assembled export, and importing
// only the first half of someone's library while reporting success is worse
// than refusing the file.
func decodeEntries(data []byte, label string) ([]rawBook, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("parse %s: trailing content after the first JSON value (concatenated exports?)", label)
	}
	arr, ok := root.([]any)
	if !ok {
		if obj, isObj := root.(map[string]any); isObj {
			for _, k := range wrapperKeys {
				if v, isArr := obj[k].([]any); isArr {
					arr, ok = v, true
					break
				}
			}
		}
	}
	if !ok {
		return nil, errNotAnEntryList(label)
	}
	books := make([]rawBook, 0, len(arr))
	for _, el := range arr {
		if m, isMap := el.(map[string]any); isMap {
			books = append(books, rawBook(m))
		}
	}
	return books, nil
}

// parseOpenAudible decodes an OpenAudible export and lifts each entry into a
// sourceBook (symmetric with parseLibation, so a caller can never forget the
// wrap step).
func parseOpenAudible(data []byte) ([]sourceBook, error) {
	entries, err := decodeEntries(data, "books.json")
	if err != nil {
		return nil, err
	}
	books := make([]sourceBook, 0, len(entries))
	for _, e := range entries {
		books = append(books, openAudibleToBook(e))
	}
	return books, nil
}

// openAudibleToBook derives one OpenAudible entry's parse-time facts: the
// single series claim from series_name/series_sequence (a seriesRef is emitted
// only for a non-empty name - the sourceBook invariant), the runtime from the
// seconds field rounded to whole minutes, and the tri-state abridged flag.
func openAudibleToBook(b rawBook) sourceBook {
	sb := sourceBook{raw: b}
	if name := b.str("series_name"); name != "" {
		sb.series = []seriesRef{makeSeriesRef(name, b.str("series_sequence"))}
	}
	if secs, ok := b.intVal("seconds"); ok && secs > 0 {
		sb.runtimeMin = int((secs + 30) / 60)
	}
	sb.abridged = b.boolPtr("abridged")
	return sb
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

// startMS / lengthMS read a chapter's offsets under either spelling a source
// documents for them: OpenAudible's snake_case (start_offset_ms / length_ms) or
// libex's camelCase (startOffsetMs / lengthMs). Accepting both here is what lets
// each parser hand buildChapters its own chapter rows instead of re-boxing them
// into the other source's key names.
func (c rawChapter) startMS() (int64, bool)  { return c.firstInt("start_offset_ms", "startOffsetMs") }
func (c rawChapter) lengthMS() (int64, bool) { return c.firstInt("length_ms", "lengthMs") }

// firstInt reads the first of keys that is present and non-null.
func (c rawChapter) firstInt(keys ...string) (int64, bool) {
	for _, k := range keys {
		if v, ok := c[k]; ok && v != nil {
			return coerceInt(v)
		}
	}
	return 0, false
}

// intVal returns an integer field. ok is false when the value is missing, null,
// or not parseable as a whole number (a float like 12.9 truncates toward zero).
func (b rawBook) intVal(key string) (int64, bool) { return coerceInt(b[key]) }

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
