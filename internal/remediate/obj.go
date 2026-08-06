package remediate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
func (o obj) clone() obj {
	out := make(obj, len(o))
	for k, v := range o {
		out[k] = v
	}
	return out
}

// raw renders the object back to JSON. Key order is irrelevant - every write
// goes through pkg/pack, which renders canonically (sorted keys).
func (o obj) raw() (json.RawMessage, error) {
	b, err := json.Marshal(map[string]json.RawMessage(o))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// str returns a string member, empty when absent or not a string.
func (o obj) str(k string) string {
	var s string
	if err := json.Unmarshal(o[k], &s); err != nil {
		return ""
	}
	return s
}

// strs returns a string-list member, nil when absent or not a list of strings.
func (o obj) strs(k string) []string {
	var out []string
	if err := json.Unmarshal(o[k], &out); err != nil {
		return nil
	}
	return out
}

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

// set stores v under k, marshalled.
func (o obj) set(k string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o[k] = b
	return nil
}

// setRaw stores an already-encoded member.
func (o obj) setRaw(k string, v json.RawMessage) { o[k] = v }

// drop removes a member.
func (o obj) drop(k string) { delete(o, k) }

// has reports whether a member is present.
func (o obj) has(k string) bool { _, ok := o[k]; return ok }

// asins decodes the region-scoped ASIN list.
func (o obj) asins() []model.ASIN {
	var out []model.ASIN
	if err := json.Unmarshal(o["asin"], &out); err != nil {
		return nil
	}
	return out
}

// isbns decodes the ISBN list, which has two on-disk spellings (model.ISBNRef
// keeps them one type).
func (o obj) isbns() []model.ISBNRef {
	var out []model.ISBNRef
	if err := json.Unmarshal(o["isbn"], &out); err != nil {
		return nil
	}
	return out
}

// sources decodes the provenance list.
func (o obj) sources() []model.Source {
	var out []model.Source
	if err := json.Unmarshal(o["sources"], &out); err != nil {
		return nil
	}
	return out
}

// credits decodes the role-qualified contributor list.
func (o obj) credits() []model.Credit {
	var out []model.Credit
	if err := json.Unmarshal(o["credits"], &out); err != nil {
		return nil
	}
	return out
}

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
	return o.set("recordings", m)
}

// sortStrings is sort.Strings, named so the intent reads at the call site.
func sortStrings(s []string) { sort.Strings(s) }

// appendUnique appends the values of src that dst does not already hold,
// preserving dst's order and then src's. First-seen order everywhere is what
// keeps a merge from churning a list nobody asked it to reorder.
func appendUnique(dst, src []string) []string {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range src {
		if !seen[v] {
			seen[v] = true
			dst = append(dst, v)
		}
	}
	return dst
}

// unionASINs appends src's identifiers to dst, deduplicating BY VALUE so the
// same ASIN recorded under two regions collapses to the first one seen. That is
// the same key recording-ASIN uniqueness is enforced on.
func unionASINs(dst, src []model.ASIN) []model.ASIN {
	seen := make(map[string]bool, len(dst))
	for _, a := range dst {
		seen[a.ASIN] = true
	}
	for _, a := range src {
		if !seen[a.ASIN] {
			seen[a.ASIN] = true
			dst = append(dst, a)
		}
	}
	return dst
}

// unionISBNs appends src's ISBNs to dst, deduplicating by value for the same
// reason unionASINs does: the bare-string and region-scoped spellings of one
// identifier are one identifier.
func unionISBNs(dst, src []model.ISBNRef) []model.ISBNRef {
	seen := make(map[string]bool, len(dst))
	for _, r := range dst {
		seen[r.ISBN] = true
	}
	for _, r := range src {
		if !seen[r.ISBN] {
			seen[r.ISBN] = true
			dst = append(dst, r)
		}
	}
	return dst
}

// unionSources appends src's provenance entries to dst, deduplicating on the
// whole entry: two runs of one importer over one ASIN on two days are two
// facts, the same run recorded twice is one.
func unionSources(dst, src []model.Source) []model.Source {
	key := func(s model.Source) string { return s.Type + "\x00" + s.Ref + "\x00" + s.ImportedAt }
	seen := make(map[string]bool, len(dst))
	for _, s := range dst {
		seen[key(s)] = true
	}
	for _, s := range src {
		if !seen[key(s)] {
			seen[key(s)] = true
			dst = append(dst, s)
		}
	}
	return dst
}

// unionCredits appends src's credits to dst, deduplicating on (person, role).
func unionCredits(dst, src []model.Credit) []model.Credit {
	key := func(c model.Credit) string { return c.Person + "\x00" + c.Role }
	seen := make(map[string]bool, len(dst))
	for _, c := range dst {
		seen[key(c)] = true
	}
	for _, c := range src {
		if !seen[key(c)] {
			seen[key(c)] = true
			dst = append(dst, c)
		}
	}
	sort.Slice(dst, func(i, j int) bool {
		if dst[i].Person != dst[j].Person {
			return dst[i].Person < dst[j].Person
		}
		return dst[i].Role < dst[j].Role
	})
	return dst
}

// unionGenres unions two genre lists and sorts them ascending, which is the
// form pkg/check's checkGenresSorted requires.
func unionGenres(dst, src []string) []string {
	out := appendUnique(append([]string(nil), dst...), src)
	sortStrings(out)
	return out
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

// earlierStamp returns the earlier of two added_at values. They come in two
// shapes - a plain YYYY-MM-DD and the migration's full RFC 3339 timestamp - so
// the DAY decides and the full spelling only breaks a tie, which keeps the
// comparison from reading "2026-07-25T17:12:12+01:00" as later than
// "2026-07-25" when they are the same day.
func earlierStamp(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	da, db := a, b
	if len(da) > 10 {
		da = da[:10]
	}
	if len(db) > 10 {
		db = db[:10]
	}
	if da != db {
		if da < db {
			return a
		}
		return b
	}
	if a < b {
		return a
	}
	return b
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
	for v, n := range counts {
		if n > bestN || (n == bestN && v < best) {
			best, bestN = v, n
		}
	}
	return best
}
