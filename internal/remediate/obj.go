package remediate

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// obj.go holds a record as its RAW members rather than as a typed struct.
//
// This is the same call pkg/pack.WorkEntry documents and for the same reason:
// marshalling a work back out through model.Work/model.Recording would emit
// every zero-valued required field - an `"abridged": false` on a recording
// whose source never stated one - and stating a fact nobody gave us is the one
// thing this project does not do. A merge edits the members it decides about
// and carries every other byte through untouched.
//
// ITS SUCCESSOR IS internal/rawentry, which is this file promoted to a leaf for the
// duplicate-merge repair (internal/repair) that needed the same tool. This package was
// deliberately NOT rewritten onto it: it is a documented one-off that has already run, so
// a rewrite would change nothing on disk while touching the code that produced 159 live
// records. A third consumer extends the leaf rather than forking this file again.
//
// THE TRADE, stated explicitly: pkg/pack owns the composite shape and offers
// DecodeEntry/SetRecording, and every other writer in the module uses them.
// This package does not, because they hand back and take `map[string]any`: a
// round trip through it re-renders every number the tooling does not model (a
// chapter's millisecond offsets, a runtime) and cannot tell an absent member
// from a zero-valued one. A remediation that rewrites 159 records out of 133k
// has to leave the bytes it did not decide about exactly as it found them, so
// it keeps its members raw. The composite invariants pkg/pack states - the entry
// key equals Work.ID, each map key equals its recording's ID, each recording's
// work backref equals the entry key - are upheld here by hand (see merge.go),
// and metacheck is the gate that proves it.

// obj is a JSON object with its members left as raw bytes.
type obj map[string]json.RawMessage

// decodeObj parses raw as a JSON object.
func decodeObj(raw json.RawMessage) (obj, error) {
	var o obj
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	if o == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return o, nil
}

// clone returns an independent copy. The member bytes are shared, which is
// safe: nothing mutates a raw member in place, it is replaced.
func (o obj) clone() obj { return maps.Clone(o) }

// raw renders the object back to JSON. Key order is irrelevant - every write
// goes through pkg/pack, which renders canonically (sorted keys).
func (o obj) raw() (json.RawMessage, error) { return json.Marshal(map[string]json.RawMessage(o)) }

// decodeOr decodes a member into T, yielding T's zero value for an absent
// member or one this package cannot read. Its callers read a schema-validated
// tree, where an unreadable member is a defect metacheck reports rather than
// one a merge has to invent a refusal for.
func decodeOr[T any](raw json.RawMessage) T {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		var zero T
		return zero
	}
	return v
}

// str returns a string member, empty when absent or not a string.
func (o obj) str(k string) string { return decodeOr[string](o[k]) }

// strs returns a string-list member, nil when absent or not a list of strings.
func (o obj) strs(k string) []string { return decodeOr[[]string](o[k]) }

// intAt returns an integer member and whether it was there.
func (o obj) intAt(k string) (int, bool) {
	raw, ok := o[k]
	if !ok {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// set stores v under k. A value this package composes that will not marshal is
// a programmer error, not a data problem, so it panics rather than threading an
// error every call site would have to invent a refusal for: they pass a string,
// a number, a list of them, or one of the model structs.
func (o obj) set(k string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("remediate: member %q does not marshal: %v", k, err))
	}
	o[k] = b
}

// setRaw stores an already-encoded member.
func (o obj) setRaw(k string, v json.RawMessage) { o[k] = v }

// drop removes a member.
func (o obj) drop(k string) { delete(o, k) }

// has reports whether a member is present.
func (o obj) has(k string) bool { _, ok := o[k]; return ok }

// asins decodes the region-scoped ASIN list.
func (o obj) asins() []model.ASIN { return decodeOr[[]model.ASIN](o["asin"]) }

// isbns decodes the ISBN list, which has two on-disk spellings (model.ISBNRef
// keeps them one type).
func (o obj) isbns() []model.ISBNRef { return decodeOr[[]model.ISBNRef](o["isbn"]) }

// sources decodes the provenance list.
func (o obj) sources() []model.Source { return decodeOr[[]model.Source](o["sources"]) }

// credits decodes the role-qualified contributor list.
func (o obj) credits() []model.Credit { return decodeOr[[]model.Credit](o["credits"]) }

// recordings returns the work entry's recordings map, keyed by recording slug.
func (o obj) recordings() (map[string]obj, error) {
	raw, ok := o["recordings"]
	if !ok {
		return map[string]obj{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(map[string]obj, len(m))
	for k, v := range m {
		rec, err := decodeObj(v)
		if err != nil {
			return nil, fmt.Errorf("recording %q: %w", k, err)
		}
		out[k] = rec
	}
	return out, nil
}

// setRecordings replaces the recordings map.
func (o obj) setRecordings(recs map[string]obj) error {
	m := make(map[string]json.RawMessage, len(recs))
	for k, v := range recs {
		raw, err := v.raw()
		if err != nil {
			return fmt.Errorf("recording %q: %w", k, err)
		}
		m[k] = raw
	}
	o.set("recordings", m)
	return nil
}

// sortedKeys returns a map's keys in sorted order, so every walk over one is
// deterministic.
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

// union appends the members of src that dst does not already hold, comparing by
// key and preserving dst's order and then src's. First-seen order everywhere is
// what keeps a merge from churning a list nobody asked it to reorder.
func union[T any](dst, src []T, key func(T) string) []T {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[key(v)] = true
	}
	for _, v := range src {
		if k := key(v); !seen[k] {
			seen[k] = true
			dst = append(dst, v)
		}
	}
	return dst
}

// appendUnique unions two string lists.
func appendUnique(dst, src []string) []string {
	return union(dst, src, func(s string) string { return s })
}

// unionASINs unions two identifier lists BY VALUE, so the same ASIN recorded
// under two regions collapses to the first one seen. That is the same key
// recording-ASIN uniqueness is enforced on.
func unionASINs(dst, src []model.ASIN) []model.ASIN {
	return union(dst, src, func(a model.ASIN) string { return a.ASIN })
}

// unionISBNs unions two ISBN lists by value, for the same reason unionASINs
// does: the bare-string and region-scoped spellings of one identifier are one
// identifier.
func unionISBNs(dst, src []model.ISBNRef) []model.ISBNRef {
	return union(dst, src, func(r model.ISBNRef) string { return r.ISBN })
}

// unionSources unions two provenance lists on the WHOLE entry: two runs of one
// importer over one ASIN on two days are two facts, the same run recorded twice
// is one.
func unionSources(dst, src []model.Source) []model.Source {
	return union(dst, src, func(s model.Source) string {
		return s.Type + "\x00" + s.Ref + "\x00" + s.ImportedAt
	})
}

// unionCredits unions two credit lists on (person, role) and restores the
// canonical order through the importer's own comparator, so one set of credits
// has one byte-form however it was assembled.
func unionCredits(dst, src []model.Credit) []model.Credit {
	out := union(dst, src, func(c model.Credit) string { return c.Person + "\x00" + c.Role })
	importer.SortCredits(out)
	return out
}

// unionGenres unions two genre lists and sorts them ascending, which is the
// form pkg/check's checkGenresSorted requires.
func unionGenres(dst, src []string) []string {
	out := appendUnique(slices.Clone(dst), src)
	slices.Sort(out)
	return out
}

// setListOrDrop stores a list member, or removes it when the list is empty: an
// absent member is how this schema spells "unstated", and an empty array would
// say something else.
func setListOrDrop[T any](o obj, key string, values []T) {
	if len(values) == 0 {
		o.drop(key)
		return
	}
	o.set(key, values)
}

// setStringOrDrop stores a string member, or removes it when it is empty.
func setStringOrDrop(o obj, key, value string) {
	if value == "" {
		o.drop(key)
		return
	}
	o.set(key, value)
}

// earlierDate returns the earlier of two partial dates ("2020", "2020-05",
// "2020-05-01"), with the SPECIFIC spelling winning over a prefix of itself.
// It is the importer's fillReleaseDate rule stated once more: a recorded value
// that is a strict extension of the other states the same date more precisely,
// so coarsening it would be a loss dressed up as agreement.
func earlierDate(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case strings.HasPrefix(b, a):
		return b
	case strings.HasPrefix(a, b):
		return a
	case a < b:
		return a
	default:
		return b
	}
}

// earlierStamp returns the earlier of two added_at values.
//
// They come in two shapes - a plain YYYY-MM-DD and the migration's full RFC 3339
// timestamp with an offset - and this comparison has to agree with the one
// internal/build's timeKey makes when it derives the artifact's added_at from
// the same values, or the two could order one pair of records differently. So
// the method is timeKey's: a parseable RFC 3339 value is normalized to UTC
// before it is compared, anything else is compared as written. The two forms
// still sort correctly against each other, because a UTC RFC 3339 rendering
// begins with the very date a plain date states.
//
// Relocating this beside timeKey is out of scope; the two are pinned against
// each other by TestEarlierStampAgreesWithTheArtifactOrdering.
func earlierStamp(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	if stampKey(a) <= stampKey(b) {
		return a
	}
	return b
}

// stampKey is internal/build timeKey's normalization: RFC 3339 in UTC, anything
// else as written.
func stampKey(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

// modal returns the value seen most often, ties broken by the value itself so
// the answer never depends on the order the inputs arrived in.
func modal(values []string) string {
	counts := map[string]int{}
	for _, v := range values {
		if v != "" {
			counts[v]++
		}
	}
	best, bestN := "", 0
	for _, v := range sortedKeys(counts) {
		if counts[v] > bestN {
			best, bestN = v, counts[v]
		}
	}
	return best
}
