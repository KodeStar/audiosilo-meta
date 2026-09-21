package recorddiff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// fields.go renders what CHANGED inside one record, structurally.
//
// It is deliberately not a text diff of the two entries. A record is re-rendered
// canonically on every write, so a textual comparison of two versions reports
// reflowed neighbours, moved commas and re-indented nesting - the same storage
// noise the entry-level keying exists to cancel. Comparing the two decoded
// objects field by field reports only the facts that differ, which is what a
// reviewer judges.
//
// Three shapes get special treatment, each because the generic rendering of it
// would be worse than useless:
//   - chapters collapses to a count. A backfilled recording gains 28 chapter
//     objects; printing them is the whole budget for one record's least
//     interesting change.
//   - recordings is walked ONE LEVEL DOWN, so an edit to a narration reads as
//     "recordings.<slug>.release_date: ..." rather than as one enormous
//     before/after blob, and an added narration is announced as a narration.
//   - arrays are rendered as the items added and removed rather than as
//     before/after, because an ASIN list gaining one entry is one fact.

const (
	// chaptersKey and recordingsKey are the two members with a rendering of
	// their own (see above).
	chaptersKey   = "chapters"
	recordingsKey = "recordings"

	// maxFieldLines bounds one record's field diff. A record with more changed
	// fields than this is a rewrite, and what a reviewer needs to see about a
	// rewrite is that it is one.
	maxFieldLines = 24
	// maxFieldDepth bounds the walk into nested objects (xref, a recording's
	// members). Below it, a changed object renders as a clipped before/after.
	maxFieldDepth = 3
	// maxFieldChars bounds one rendered value.
	maxFieldChars = 120
	// maxArrayItems bounds the items an array delta names.
	maxArrayItems = 8
)

// diffFields renders the field-level differences between two canonical versions
// of one entry.
func diffFields(base, head json.RawMessage) []string {
	// pack.DecodeEntry, not encoding/json, for the same reason every writer uses
	// it: json.Number keeps a runtime or a chapter offset exactly as written, so a
	// number the change never touched cannot be reported as changed by a float
	// round trip.
	a, aerr := pack.DecodeEntry(base)
	b, berr := pack.DecodeEntry(head)
	if aerr != nil || berr != nil {
		return []string{"(the entry could not be decoded for a field-level diff)"}
	}
	var out []string
	diffMap("", a, b, &out, 0)
	if len(out) == 0 {
		// Two entries that differ in bytes but in no field the walk can see. It
		// should not happen (canonical bytes are a function of the value), so say
		// so rather than printing an empty modification.
		return []string{"(changed, but no field-level difference was found)"}
	}
	if len(out) > maxFieldLines {
		n := len(out) - maxFieldLines
		out = append(out[:maxFieldLines:maxFieldLines], fmt.Sprintf("... %d more field change(s)", n))
	}
	return out
}

// diffMap walks two objects key by key, in sorted order, appending one line per
// difference. prefix is the dotted path of the object itself, empty at the top.
func diffMap(prefix string, a, b map[string]any, out *[]string, depth int) {
	for _, k := range unionAnyKeys(a, b) {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		av, inA := a[k]
		bv, inB := b[k]
		switch {
		case !inB:
			*out = append(*out, path+": "+renderValue(av)+" -> (absent)")
		case !inA:
			*out = append(*out, path+": (absent) -> "+renderValue(bv))
		default:
			diffValue(k, path, av, bv, out, depth)
		}
	}
}

// diffValue renders the difference between one field's two values. name is the
// member's own key (the special cases test it), path its dotted address.
func diffValue(name, path string, a, b any, out *[]string, depth int) {
	if reflect.DeepEqual(a, b) {
		return
	}
	aMap, aIsMap := a.(map[string]any)
	bMap, bIsMap := b.(map[string]any)
	aArr, aIsArr := a.([]any)
	bArr, bIsArr := b.([]any)
	switch {
	case name == chaptersKey && aIsArr && bIsArr:
		if len(aArr) != len(bArr) {
			*out = append(*out, fmt.Sprintf("%s: %d -> %d chapters", path, len(aArr), len(bArr)))
		} else {
			*out = append(*out, fmt.Sprintf("%s: %d chapters, contents changed", path, len(aArr)))
		}
	case name == recordingsKey && aIsMap && bIsMap:
		diffRecordings(path, aMap, bMap, out, depth)
	case aIsMap && bIsMap && depth < maxFieldDepth:
		diffMap(path, aMap, bMap, out, depth+1)
	case aIsArr && bIsArr:
		*out = append(*out, path+": "+arrayDelta(aArr, bArr))
	default:
		*out = append(*out, path+": "+renderValue(a)+" -> "+renderValue(b))
	}
}

// diffRecordings walks a work's recordings map one level down: a narration added
// or removed is announced as such, and one that changed is walked field by field.
func diffRecordings(path string, a, b map[string]any, out *[]string, depth int) {
	for _, slug := range unionAnyKeys(a, b) {
		av, inA := a[slug]
		bv, inB := b[slug]
		p := path + "." + slug
		switch {
		case !inB:
			*out = append(*out, p+": recording REMOVED"+recordingTag(av))
		case !inA:
			*out = append(*out, p+": recording ADDED"+recordingTag(bv))
		default:
			if reflect.DeepEqual(av, bv) {
				continue
			}
			aMap, aOK := av.(map[string]any)
			bMap, bOK := bv.(map[string]any)
			if aOK && bOK {
				diffMap(p, aMap, bMap, out, depth+1)
				continue
			}
			*out = append(*out, p+": "+renderValue(av)+" -> "+renderValue(bv))
		}
	}
}

// recordingTag is the parenthetical that says what an added or removed narration
// was, so the line stands on its own.
func recordingTag(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	narrators := "(none)"
	if arr, ok := m["narrators"].([]any); ok && len(arr) > 0 {
		names := make([]string, 0, len(arr))
		for _, n := range arr {
			names = append(names, plainString(n))
		}
		narrators = list(names)
	}
	chapters := 0
	if arr, ok := m[chaptersKey].([]any); ok {
		chapters = len(arr)
	}
	return fmt.Sprintf(" (narrators %s, %d chapters)", narrators, chapters)
}

// arrayDelta renders an array change as the items added and removed. Comparison
// is by rendered item, as a MULTISET, so a list that only reordered says so
// rather than reporting every element twice.
func arrayDelta(a, b []any) string {
	ai, bi := itemStrings(a), itemStrings(b)
	removed := multisetMinus(ai, bi)
	added := multisetMinus(bi, ai)
	if len(added) == 0 && len(removed) == 0 {
		return fmt.Sprintf("reordered (%d items)", len(a))
	}
	parts := make([]string, 0, len(added)+len(removed))
	for _, s := range capItems(added) {
		parts = append(parts, "+"+s)
	}
	for _, s := range capItems(removed) {
		parts = append(parts, "-"+s)
	}
	if n := len(added) + len(removed) - len(parts); n > 0 {
		parts = append(parts, fmt.Sprintf("(+%d more)", n))
	}
	return strings.Join(parts, ", ")
}

// capItems keeps at most maxArrayItems of a delta's items.
func capItems(items []string) []string {
	if len(items) <= maxArrayItems {
		return items
	}
	return items[:maxArrayItems]
}

// itemStrings renders each element of an array for comparison and display: a
// string as itself, anything else as compact JSON.
func itemStrings(arr []any) []string {
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		out = append(out, clip(plainString(v), maxFieldChars))
	}
	return out
}

// multisetMinus returns the elements of a that b does not cover, preserving a's
// order, so the result is deterministic.
func multisetMinus(a, b []string) []string {
	count := make(map[string]int, len(b))
	for _, s := range b {
		count[s]++
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if count[s] > 0 {
			count[s]--
			continue
		}
		out = append(out, s)
	}
	return out
}

// renderValue renders one JSON value for a "old -> new" line.
func renderValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(clip(t, maxFieldChars))
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return clip(plainString(v), maxFieldChars)
}

// plainString renders a value without quoting a bare string: the form an array
// item and a narrator slug read best in.
func plainString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// unionAnyKeys returns every key of either object, sorted.
func unionAnyKeys(a, b map[string]any) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
