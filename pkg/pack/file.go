package pack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// File is a parsed pack: the wrapper's entries held as raw JSON, so fields the
// tooling does not know about survive a round-trip untouched. Parsing a
// canonical pack and re-serializing it is byte-identical.
type File struct {
	entries map[string]json.RawMessage
}

// NewFile returns an empty pack.
func NewFile() *File { return &File{entries: map[string]json.RawMessage{}} }

// Parse reads a pack file. The wrapper holds only "entries"; anything else is
// an error, matching the pack schemas' additionalProperties:false.
func Parse(raw []byte) (*File, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var wrapper map[string]json.RawMessage
	if err := dec.Decode(&wrapper); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing content after JSON value")
		}
		return nil, err
	}
	for k := range wrapper {
		if k != "entries" {
			return nil, fmt.Errorf("unexpected pack member %q (a pack holds only \"entries\")", k)
		}
	}
	rawEntries, ok := wrapper["entries"]
	if !ok {
		return nil, fmt.Errorf("pack is missing \"entries\"")
	}
	f := NewFile()
	if err := json.Unmarshal(rawEntries, &f.entries); err != nil {
		return nil, fmt.Errorf("entries: %w", err)
	}
	if f.entries == nil {
		return nil, fmt.Errorf("entries must be an object")
	}
	return f, nil
}

// Len returns the number of entries.
func (f *File) Len() int { return len(f.entries) }

// Slugs returns the entry keys in sorted order, which is also the order they
// are written in.
func (f *File) Slugs() []string {
	out := make([]string, 0, len(f.entries))
	for k := range f.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns the raw entry stored under slug.
func (f *File) Get(slug string) (json.RawMessage, bool) {
	e, ok := f.entries[slug]
	return e, ok
}

// Set stores entry under slug, replacing any existing entry.
func (f *File) Set(slug string, entry json.RawMessage) {
	cp := make(json.RawMessage, len(entry))
	copy(cp, entry)
	f.entries[slug] = cp
}

// Remove deletes the entry under slug, if any.
func (f *File) Remove(slug string) { delete(f.entries, slug) }

// Clone returns a deep copy.
func (f *File) Clone() *File {
	out := NewFile()
	for k, v := range f.entries {
		out.Set(k, v)
	}
	return out
}

// Bytes returns the pack's canonical on-disk form.
func (f *File) Bytes() ([]byte, error) {
	data, _, err := f.render()
	return data, err
}

// Size returns the canonical byte length of the pack file.
func (f *File) Size() (int, error) {
	data, _, err := f.render()
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// Sizes returns the pack's canonical byte length and the number of those bytes
// each entry contributes (its key, value, separator and newline). The split
// point is derived from these, so they are exact rather than estimated.
func (f *File) Sizes() (total int, per map[string]int, err error) {
	data, per, err := f.render()
	if err != nil {
		return 0, nil, err
	}
	return len(data), per, nil
}

const (
	packPrefix    = "{\n  \"entries\": {"
	packSuffix    = "  }\n}\n"
	entryIndent   = "    "
	nestingIndent = "  "
)

// render writes the canonical pack bytes and each entry's contribution to them.
// It reproduces pkg/canonical's form exactly (sorted keys, 2-space indent, LF,
// one trailing newline, no HTML escaping); canonical_test asserts that.
func (f *File) render() ([]byte, map[string]int, error) {
	slugs := f.Slugs()
	per := make(map[string]int, len(slugs))

	var buf bytes.Buffer
	buf.WriteString(packPrefix)
	if len(slugs) == 0 {
		buf.WriteString("}\n}\n")
		return buf.Bytes(), per, nil
	}
	buf.WriteString("\n")
	for i, s := range slugs {
		start := buf.Len()
		key, err := encodeJSON(s, "")
		if err != nil {
			return nil, nil, fmt.Errorf("entry %q: %w", s, err)
		}
		val, err := indentRaw(f.entries[s], entryIndent)
		if err != nil {
			return nil, nil, fmt.Errorf("entry %q: %w", s, err)
		}
		buf.WriteString(entryIndent)
		buf.Write(key)
		buf.WriteString(": ")
		buf.Write(val)
		if i < len(slugs)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
		per[s] = buf.Len() - start
	}
	buf.WriteString(packSuffix)
	return buf.Bytes(), per, nil
}

// indentRaw re-renders raw at the indentation depth an entry sits at inside a
// pack: keys sorted, 2-space indent, every line after the first carrying prefix.
func indentRaw(raw json.RawMessage, prefix string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing content after JSON value")
		}
		return nil, err
	}
	return encodeJSON(v, prefix)
}

// encodeJSON renders v the canonical way, with the given per-line prefix, and
// without the trailing newline json.Encoder appends.
func encodeJSON(v any, prefix string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, nestingIndent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
