package repair

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// entry.go holds a pack entry as its RAW members rather than as a typed struct,
// and states the BY-VALUE union rules a merge folds two records together with.
//
// The raw-member shape is pkg/pack.WorkEntry's own instruction and
// internal/remediate's obj.go applied a second time, for the same reason:
// marshalling a work or a recording back out through model.Work/model.Recording
// would emit every zero-valued required field - an `"abridged": false` on a
// recording whose source never stated one - and stating a fact nobody gave us is
// the one thing this project does not do. A merge edits the members it decides
// about and carries every other byte through untouched, which for a repair that
// rewrites a few hundred records out of 280k is the whole point: a diff shows the
// decision and nothing else.
//
// pkg/pack's DecodeEntry/SetRecording are not used here for the reason remediate
// states: they hand back `map[string]any`, so a round trip re-renders every number
// the tooling does not model (a chapter's millisecond offsets, a runtime) and
// cannot tell an absent member from a zero-valued one.
//
// THE UNION RULES ARE A RESTATEMENT, deliberately, and the enforcement is not this
// file's comment. Each key below is the key pkg/check's own uniqueness rule uses -
// an ASIN by its value, an ISBN by its value, a credit by (person, role) - and
// every write this package makes is followed by a full check.Load that fails the
// run if the merged record violates one (see repair.Run's post-write gate). A
// drifted key is therefore a failed run, not a bad record. Sharing them with
// internal/remediate was considered and declined: that package is a documented
// one-off that has already run, and making a live tool depend on a spent one to
// save five one-line functions is the worse coupling.

// entry is a JSON object with its members left as raw bytes.
type entry map[string]json.RawMessage

// decodeEntry parses raw as a JSON object.
func decodeEntry(raw json.RawMessage) (entry, error) {
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return e, nil
}

// clone returns an independent copy. The member bytes are shared, which is safe:
// nothing mutates a raw member in place, it is replaced.
func (e entry) clone() entry { return maps.Clone(e) }

// raw renders the object back to JSON. Key order is irrelevant - every write goes
// through pkg/pack, which renders canonically (sorted keys).
func (e entry) raw() (json.RawMessage, error) { return json.Marshal(map[string]json.RawMessage(e)) }

// decodeOr decodes a member into T, yielding T's zero value for an absent member
// or one this package cannot read. Its callers read a tree Run has already
// validated, where an unreadable member is a problem the run refused over rather
// than one a merge has to invent a refusal for.
func decodeOr[T any](raw json.RawMessage) T {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		var zero T
		return zero
	}
	return v
}

// str returns a string member, empty when absent or not a string.
func (e entry) str(k string) string { return decodeOr[string](e[k]) }

// strs returns a string-list member, nil when absent or not a list of strings.
func (e entry) strs(k string) []string { return decodeOr[[]string](e[k]) }

// has reports whether a member is present.
func (e entry) has(k string) bool { _, ok := e[k]; return ok }

// intAt returns an integer member and whether it was there.
func (e entry) intAt(k string) (int, bool) {
	raw, ok := e[k]
	if !ok {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// boolAt returns a boolean member and whether it was there.
func (e entry) boolAt(k string) (bool, bool) {
	raw, ok := e[k]
	if !ok {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, false
	}
	return b, true
}

// set stores v under k. A value this package composes that will not marshal is a
// programmer error rather than a data problem, so it panics rather than threading
// an error every call site would have to invent a refusal for: callers pass a
// string, a number, a list of them, or one of the model structs.
func (e entry) set(k string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("repair: member %q does not marshal: %v", k, err))
	}
	e[k] = b
}

// setRaw stores an already-encoded member.
func (e entry) setRaw(k string, v json.RawMessage) { e[k] = v }

// drop removes a member.
func (e entry) drop(k string) { delete(e, k) }

// asins decodes the region-scoped ASIN list.
func (e entry) asins() []model.ASIN { return decodeOr[[]model.ASIN](e["asin"]) }

// isbns decodes the ISBN list, whose two on-disk spellings model.ISBNRef keeps as
// one type.
func (e entry) isbns() []model.ISBNRef { return decodeOr[[]model.ISBNRef](e["isbn"]) }

// sources decodes the provenance list.
func (e entry) sources() []model.Source { return decodeOr[[]model.Source](e["sources"]) }

// credits decodes the role-qualified contributor list.
func (e entry) credits() []model.Credit { return decodeOr[[]model.Credit](e["credits"]) }

// seriesWorks decodes a series entry's membership list. SeriesWork carries exactly
// the two properties the schema allows (additionalProperties: false), so decoding
// and re-encoding it loses nothing - unlike a work or a recording, where the raw
// members are load-bearing.
func (e entry) seriesWorks() []model.SeriesWork {
	return decodeOr[[]model.SeriesWork](e["works"])
}

// recordings returns a work entry's recordings map, keyed by recording slug.
func (e entry) recordings() (map[string]entry, error) {
	raw, ok := e["recordings"]
	if !ok {
		return map[string]entry{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(map[string]entry, len(m))
	for k, v := range m {
		rec, err := decodeEntry(v)
		if err != nil {
			return nil, fmt.Errorf("recording %q: %w", k, err)
		}
		out[k] = rec
	}
	return out, nil
}

// setRecordings replaces the recordings map.
func (e entry) setRecordings(recs map[string]entry) error {
	m := make(map[string]json.RawMessage, len(recs))
	for k, v := range recs {
		raw, err := v.raw()
		if err != nil {
			return fmt.Errorf("recording %q: %w", k, err)
		}
		m[k] = raw
	}
	e.set("recordings", m)
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
	return union(slices.Clone(dst), src, func(s string) string { return s })
}

// unionASINs unions two identifier lists BY VALUE, so the same ASIN recorded under
// two regions collapses to the first one seen.
func unionASINs(dst, src []model.ASIN) []model.ASIN {
	return union(slices.Clone(dst), src, func(a model.ASIN) string { return a.ASIN })
}

// unionISBNs unions two ISBN lists by value, for the same reason unionASINs does:
// the bare-string and region-scoped spellings of one identifier are one identifier,
// which is the key pkg/check's ISBN uniqueness rule compares on too.
func unionISBNs(dst, src []model.ISBNRef) []model.ISBNRef {
	return union(slices.Clone(dst), src, func(r model.ISBNRef) string { return r.ISBN })
}

// unionSources unions two provenance lists on the WHOLE entry: two runs of one
// importer over one ASIN on two days are two facts, the same run recorded twice is
// one.
func unionSources(dst, src []model.Source) []model.Source {
	return union(slices.Clone(dst), src, func(s model.Source) string {
		return s.Type + "\x00" + s.Ref + "\x00" + s.ImportedAt
	})
}

// unionCredits unions two credit lists on (person, role) - the pair pkg/check's
// credit rule refuses a repeat of - and restores the canonical order through the
// importer's own comparator, so one set of credits has one byte-form however it was
// assembled.
func unionCredits(dst, src []model.Credit) []model.Credit {
	out := union(slices.Clone(dst), src, func(c model.Credit) string { return c.Person + "\x00" + c.Role })
	importer.SortCredits(out)
	return out
}

// unionGenres unions two genre lists and sorts them ascending, which is the form
// pkg/check's checkGenresSorted requires.
func unionGenres(dst, src []string) []string {
	out := appendUnique(dst, src)
	slices.Sort(out)
	return out
}

// setListOrDrop stores a list member, or removes it when the list is empty: an
// absent member is how this schema spells "unstated", and an empty array would say
// something else.
func setListOrDrop[T any](e entry, key string, values []T) {
	if len(values) == 0 {
		e.drop(key)
		return
	}
	e.set(key, values)
}

// fillAbsentString copies a string member from src into dst when dst states none
// and src states one, and reports whether it wrote. It is the whole of the
// merge's scalar policy: the canonical record's own value always wins, and a
// loser only ever fills a gap.
func fillAbsentString(dst, src entry, key string) bool {
	if dst.str(key) != "" {
		return false
	}
	v := src.str(key)
	if v == "" {
		return false
	}
	dst.set(key, v)
	return true
}

// fillAbsentRaw copies a member from src into dst when dst does not carry it at
// all, whatever its type, and reports whether it wrote. Used for the members whose
// bytes must survive verbatim (a chapter list) and for the tri-state ones where
// "absent" and "false" are different facts (abridged).
func fillAbsentRaw(dst, src entry, key string) bool {
	if dst.has(key) || !src.has(key) {
		return false
	}
	dst.setRaw(key, src[key])
	return true
}
