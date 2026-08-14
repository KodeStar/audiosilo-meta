// Package rawentry is the RAW-MEMBER editor for a pack entry: a JSON object whose
// members stay as bytes, plus the by-value union rules that fold two records into one.
//
// WHY RAW. pkg/pack.WorkEntry states the reason and this is its tool: marshalling a
// work or a recording back out through model.Work/model.Recording emits every
// zero-valued required field - an `"abridged": false` on a recording whose source never
// stated one - and stating a fact nobody gave us is the one thing this project does not
// do. A writer that edits the members it decided about and carries every other byte
// through untouched leaves a diff that shows the decision and nothing else, which is
// what makes a repair of a few hundred records out of 280k reviewable.
//
// pkg/pack's DecodeEntry/SetRecording are the alternative and are deliberately not used
// here: they hand back `map[string]any`, so a round trip re-renders every number the
// tooling does not model (a chapter's millisecond offsets, a runtime) and cannot tell an
// absent member from a zero-valued one.
//
// WHY A PACKAGE. internal/remediate/obj.go is this code's ANCESTOR - the GraphicAudio
// fold needed exactly this and spelled it inline. That package is a documented one-off
// that has already run, so it was not rewritten onto this leaf (rewriting a spent tool
// changes nothing on disk); its header now names this package as the successor, so the
// third consumer extends a leaf instead of forking a second copy.
//
// THE UNION RULES ARE THE POINT of it being more than a helper type. Each key below is
// the key pkg/check's own uniqueness rule compares on - an ASIN by its value, an ISBN by
// its value, a credit by (person, role), a source by its whole entry - so a writer that
// unions with them produces records pkg/check accepts by construction, and the writers
// that follow a merge with a full check.Load prove it on every run.
package rawentry

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// Obj is a JSON object with its members left as raw bytes.
type Obj map[string]json.RawMessage

// Decode parses raw as a JSON object.
func Decode(raw json.RawMessage) (Obj, error) {
	var o Obj
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	if o == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return o, nil
}

// Clone returns an independent copy. The member bytes are shared, which is safe:
// nothing here mutates a raw member in place, it replaces it.
func (o Obj) Clone() Obj { return maps.Clone(o) }

// Raw renders the object back to JSON. Key order is irrelevant - every write goes
// through pkg/pack, which renders canonically (sorted keys).
func (o Obj) Raw() (json.RawMessage, error) { return json.Marshal(map[string]json.RawMessage(o)) }

// MustRaw renders an object the caller composed from members it just decoded. Such a
// value cannot fail to marshal, and threading an error would give every call site a
// refusal branch no data can reach.
func (o Obj) MustRaw() json.RawMessage {
	raw, err := o.Raw()
	if err != nil {
		panic(fmt.Sprintf("rawentry: composed object does not marshal: %v", err))
	}
	return raw
}

// DecodeOr decodes a member into T, yielding T's zero value for an absent member or one
// it cannot read. Callers read a tree that has already been validated, where an
// unreadable member is a problem the run refused over rather than one a merge has to
// invent a refusal for.
func DecodeOr[T any](raw json.RawMessage) T {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		var zero T
		return zero
	}
	return v
}

// Str returns a string member, empty when absent or not a string.
func (o Obj) Str(k string) string { return DecodeOr[string](o[k]) }

// Strs returns a string-list member, nil when absent or not a list of strings.
func (o Obj) Strs(k string) []string { return DecodeOr[[]string](o[k]) }

// Has reports whether a member is present.
func (o Obj) Has(k string) bool { _, ok := o[k]; return ok }

// IntAt returns an integer member and whether it was there.
func (o Obj) IntAt(k string) (int, bool) {
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

// BoolPtr returns a boolean member as a TRI-STATE: nil for absent (or unreadable),
// otherwise the value. The pointer shape is the one the schema's optional booleans are
// judged in - importer.AbridgedConflict takes exactly this - so absent and false stay
// two different facts all the way to the rule.
func (o Obj) BoolPtr(k string) *bool {
	raw, ok := o[k]
	if !ok {
		return nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil
	}
	return &b
}

// Set stores v under k. A value the caller composed that will not marshal is a
// programmer error rather than a data problem, so it panics rather than threading an
// error every call site would have to invent a refusal for: callers pass a string, a
// number, a list of them, or one of the model structs.
func (o Obj) Set(k string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("rawentry: member %q does not marshal: %v", k, err))
	}
	o[k] = b
}

// SetRaw stores an already-encoded member.
func (o Obj) SetRaw(k string, v json.RawMessage) { o[k] = v }

// Drop removes a member.
func (o Obj) Drop(k string) { delete(o, k) }

// ASINs decodes the region-scoped ASIN list.
func (o Obj) ASINs() []model.ASIN { return DecodeOr[[]model.ASIN](o["asin"]) }

// ISBNs decodes the ISBN list, whose two on-disk spellings model.ISBNRef keeps as one
// type.
func (o Obj) ISBNs() []model.ISBNRef { return DecodeOr[[]model.ISBNRef](o["isbn"]) }

// Sources decodes the provenance list.
func (o Obj) Sources() []model.Source { return DecodeOr[[]model.Source](o["sources"]) }

// Credits decodes the role-qualified contributor list.
func (o Obj) Credits() []model.Credit { return DecodeOr[[]model.Credit](o["credits"]) }

// SeriesWorks decodes a series entry's membership list. model.SeriesWork carries exactly
// the two properties the schema allows (additionalProperties: false), so decoding and
// re-encoding it loses nothing - unlike a work or a recording, where the raw members are
// load-bearing.
func (o Obj) SeriesWorks() []model.SeriesWork { return DecodeOr[[]model.SeriesWork](o["works"]) }

// Recordings returns a work entry's recordings map, keyed by recording slug.
func (o Obj) Recordings() (map[string]Obj, error) {
	raw, ok := o["recordings"]
	if !ok {
		return map[string]Obj{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(map[string]Obj, len(m))
	for k, v := range m {
		rec, err := Decode(v)
		if err != nil {
			return nil, fmt.Errorf("recording %q: %w", k, err)
		}
		out[k] = rec
	}
	return out, nil
}

// SetRecordings replaces the recordings map.
func (o Obj) SetRecordings(recs map[string]Obj) error {
	m := make(map[string]json.RawMessage, len(recs))
	for k, v := range recs {
		raw, err := v.Raw()
		if err != nil {
			return fmt.Errorf("recording %q: %w", k, err)
		}
		m[k] = raw
	}
	o.Set("recordings", m)
	return nil
}

// SortedKeys returns a map's keys in sorted order, so every walk over one is
// deterministic.
func SortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

// Union appends the members of src that dst does not already hold, comparing by key and
// preserving dst's order and then src's. First-seen order everywhere is what keeps a
// merge from churning a list nobody asked it to reorder.
func Union[T any](dst, src []T, key func(T) string) []T {
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

// AppendUnique unions two string lists.
func AppendUnique(dst, src []string) []string {
	return Union(slices.Clone(dst), src, func(s string) string { return s })
}

// UnionASINs unions two identifier lists on the (REGION, ASIN) PAIR - which is exactly
// the key pkg/check's ASIN uniqueness rule compares, so the union can never mint a
// duplicate, and the same identifier recorded in two marketplaces keeps BOTH statements.
//
// It keyed on the value alone at first, folding `{us, B0X}` and `{uk, B0X}` into one entry
// and silently dropping a stated region - a fact loss in a pass whose whole promise is
// that a merge loses nothing. The asymmetry with UnionISBNs below is deliberate and
// mirrors pkg/check exactly: an ASIN is unique per marketplace, an ISBN is unique
// outright.
func UnionASINs(dst, src []model.ASIN) []model.ASIN {
	return Union(slices.Clone(dst), src, func(a model.ASIN) string { return a.Region + "\x00" + a.ASIN })
}

// UnionISBNs unions two ISBN lists by value, for the reason UnionASINs does: the
// bare-string and region-scoped spellings of one identifier are one identifier, which is
// the key pkg/check's ISBN uniqueness rule compares on too.
func UnionISBNs(dst, src []model.ISBNRef) []model.ISBNRef {
	return Union(slices.Clone(dst), src, func(r model.ISBNRef) string { return r.ISBN })
}

// UnionSources unions two provenance lists on the WHOLE entry: two runs of one importer
// over one ASIN on two days are two facts, the same run recorded twice is one.
func UnionSources(dst, src []model.Source) []model.Source {
	return Union(slices.Clone(dst), src, func(s model.Source) string {
		return s.Type + "\x00" + s.Ref + "\x00" + s.ImportedAt
	})
}

// UnionCredits unions two credit lists on (person, role) - the pair pkg/check's credit
// rule refuses a repeat of - and restores the canonical order through the importer's own
// comparator, so one set of credits has one byte-form however it was assembled.
func UnionCredits(dst, src []model.Credit) []model.Credit {
	out := Union(slices.Clone(dst), src, func(c model.Credit) string { return c.Person + "\x00" + c.Role })
	importer.SortCredits(out)
	return out
}

// UnionGenres unions two genre lists and sorts them ascending, which is the form
// pkg/check's checkGenresSorted requires.
func UnionGenres(dst, src []string) []string {
	out := AppendUnique(dst, src)
	slices.Sort(out)
	return out
}

// SetListOrDrop stores a list member, or removes it when the list is empty: an absent
// member is how this schema spells "unstated", and an empty array would say something
// else.
func SetListOrDrop[T any](o Obj, key string, values []T) {
	if len(values) == 0 {
		o.Drop(key)
		return
	}
	o.Set(key, values)
}

// FillAbsentString copies a string member from src into dst when dst states none and src
// states one, and reports whether it wrote. It is the whole of a merge's scalar policy:
// the surviving record's own value always wins, and the other only ever fills a gap.
func FillAbsentString(dst, src Obj, key string) bool {
	if dst.Str(key) != "" {
		return false
	}
	v := src.Str(key)
	if v == "" {
		return false
	}
	dst.Set(key, v)
	return true
}

// FillAbsentRaw copies a member from src into dst when dst does not carry it at all,
// whatever its type, and reports whether it wrote. It is for the members whose bytes must
// survive verbatim (a chapter list) and for the tri-state ones where absent and false are
// different facts (abridged).
func FillAbsentRaw(dst, src Obj, key string) bool {
	if dst.Has(key) || !src.Has(key) {
		return false
	}
	dst.SetRaw(key, src[key])
	return true
}
