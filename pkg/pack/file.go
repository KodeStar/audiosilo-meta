package pack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
)

// File is a parsed pack: the wrapper's entries held as raw JSON, so fields the
// tooling does not know about survive a round-trip untouched. Parsing a
// canonical pack and re-serializing it is byte-identical.
//
// The rendered bytes and per-entry sizes are memoized, because one metafmt pass
// asks for them several times per pack (cap check, split point, final write).
// Set and Remove are the only mutators, and both drop the memo.
type File struct {
	entries map[string]json.RawMessage

	rendered []byte
	sizes    map[string]int
}

// NewFile returns an empty pack.
func NewFile() *File { return &File{entries: map[string]json.RawMessage{}} }

// Parse reads a pack file. The wrapper holds only "entries"; anything else is
// an error, matching the pack schemas' additionalProperties:false. A duplicate
// key anywhere in the document is an error too: encoding/json would silently
// keep the last one, and re-serializing would then make the loss permanent.
func Parse(raw []byte) (*File, error) {
	if err := checkNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	var wrapper map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
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
	f.invalidate()
}

// Remove deletes the entry under slug, if any.
func (f *File) Remove(slug string) {
	delete(f.entries, slug)
	f.invalidate()
}

// invalidate drops the memoized render. Every mutator calls it.
func (f *File) invalidate() { f.rendered, f.sizes = nil, nil }

// ReleaseMemo drops the memoized render without changing what the pack holds,
// for a caller that has finished with the sizes but is holding the pack itself
// open. The memo is a pure function of the entries, so releasing it only costs
// the next reader a re-render.
//
// It exists because the memo is expensive and, on the paths that keep a pack
// resident, unread afterwards: a validating pass asks every pack for its Sizes
// (the cap check) and then never renders it again, which used to leave a second
// copy of the whole tree - larger than the entries themselves - alive for as
// long as the pack was. See check.readPack, its only caller in this repo.
func (f *File) ReleaseMemo() { f.rendered, f.sizes = nil, nil }

// Clone returns a deep copy.
func (f *File) Clone() *File {
	out := NewFile()
	for k, v := range f.entries {
		out.Set(k, v)
	}
	return out
}

// Bytes returns the pack's canonical on-disk form. The result is a copy: the
// memoized render must not be handed to a caller that could scribble on it.
func (f *File) Bytes() ([]byte, error) {
	data, _, err := f.render()
	if err != nil {
		return nil, err
	}
	return bytes.Clone(data), nil
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
// point is derived from these, so they are exact rather than estimated. The
// returned map is the memo: read it, do not write to it.
func (f *File) Sizes() (total int, per map[string]int, err error) {
	data, per, err := f.render()
	if err != nil {
		return 0, nil, err
	}
	return len(data), per, nil
}

const (
	packPrefix  = "{\n  \"entries\": {"
	packSuffix  = "  }\n}\n"
	entryIndent = "    "
)

// render writes the canonical pack bytes and each entry's contribution to them,
// memoizing both. It reproduces pkg/canonical's form exactly by rendering every
// entry through canonical.FormatIndent at its in-file indentation, so the
// definition of canonical form lives in one package;
// TestRenderMatchesCanonical asserts the assembled whole still equals
// canonical.Format.
func (f *File) render() ([]byte, map[string]int, error) {
	if f.rendered != nil {
		return f.rendered, f.sizes, nil
	}
	slugs := f.Slugs()
	per := make(map[string]int, len(slugs))

	var buf bytes.Buffer
	buf.WriteString(packPrefix)
	if len(slugs) == 0 {
		buf.WriteString("}\n}\n")
		f.rendered, f.sizes = buf.Bytes(), per
		return f.rendered, f.sizes, nil
	}
	buf.WriteString("\n")
	for i, s := range slugs {
		start := buf.Len()
		key, err := canonical.Encode(s, "")
		if err != nil {
			return nil, nil, fmt.Errorf("entry %q: %w", s, err)
		}
		val, err := canonical.FormatIndent(f.entries[s], entryIndent)
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
	f.rendered, f.sizes = buf.Bytes(), per
	return f.rendered, f.sizes, nil
}

// CheckNoDuplicateKeys reports a repeated key in any object in raw.
//
// It is exported because the rule is not really about packs: encoding/json keeps
// the LAST of a repeated key without complaint, and JSON Schema cannot see the
// first one at all, so any file this project decodes and writes back would lose
// data to a duplicate - silently, and permanently once rewritten. Every reader of
// a file in the data tree asks it, packs (Parse, DecodeEntry) and the one non-pack
// file alike (pkg/redirects.Load, pkg/check's loadRedirects), so there is one
// implementation rather than one per file kind.
func CheckNoDuplicateKeys(raw []byte) error { return checkNoDuplicateKeys(raw) }

// checkNoDuplicateKeys reports a repeated key in any object in raw.
// encoding/json keeps the last of a repeated key without complaint, so a pack
// carrying one would lose an entry the moment the tooling rewrote it -
// silently, and permanently.
func checkNoDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return scanDuplicateKeys(dec, nil)
}

// scanDuplicateKeys walks one JSON value, checking every object it contains.
// path names the enclosing keys, so a duplicate deep inside an entry is
// reportable. A malformed document is left to the real decoder to describe.
func scanDuplicateKeys(dec *json.Decoder, path []string) error {
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil
			}
			key, _ := kt.(string)
			if seen[key] {
				return fmt.Errorf("duplicate key %q in %s", key, objectLabel(path))
			}
			seen[key] = true
			if err := scanDuplicateKeys(dec, append(path, key)); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := scanDuplicateKeys(dec, path); err != nil {
				return err
			}
		}
	}
	// Consume the matching closing delimiter.
	if _, err := dec.Token(); err != nil {
		return nil
	}
	return nil
}

// objectLabel names the object a duplicate key was found in.
func objectLabel(path []string) string {
	if len(path) == 0 {
		return "the top-level object"
	}
	out := path[0]
	for _, p := range path[1:] {
		out += "." + p
	}
	return out
}

// DecodeEntry decodes a pack entry into a mutable map, preserving numbers
// exactly (json.Number) and rejecting a duplicate key.
//
// Exactness matters because an entry is read, edited in one field, and written
// back whole: a float round-trip would reformat every number the edit never
// touched, so a second identical run would stop being a byte-level no-op. The
// duplicate-key check matters for the same reason a pack's does - a writer
// re-serializes what it decodes, which would make a silent loss permanent.
func DecodeEntry(raw json.RawMessage) (map[string]any, error) {
	if err := checkNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("entry is not a JSON object")
	}
	return m, nil
}

// SetRecording splices rec into a works-family composite entry under recSlug,
// creating the recordings map if the work had none. entry is a decoded works
// entry (see DecodeEntry); rec is either a typed recording or an already-decoded
// map.
//
// It is the one place that knows the composite's shape, so every writer that
// edits a recording finishes the same way.
func SetRecording(entry map[string]any, recSlug string, rec any) error {
	if entry == nil {
		return fmt.Errorf("works entry is nil")
	}
	existing, ok := entry["recordings"]
	if !ok || existing == nil {
		entry["recordings"] = map[string]any{recSlug: rec}
		return nil
	}
	recs, ok := existing.(map[string]any)
	if !ok {
		return fmt.Errorf("works entry's %q is %T, not an object", "recordings", existing)
	}
	recs[recSlug] = rec
	return nil
}
