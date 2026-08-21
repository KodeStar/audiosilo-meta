package testpack

import (
	"encoding/json"
	"strings"
	"testing"
)

// fixtures.go renders the minimal, SCHEMA-VALID records a fixture tree is seeded with.
//
// It is here, beside Seed, because two suites need the same records and had each grown
// their own builders: internal/audit's detectors and internal/repair's writers. The forks
// had already drifted - a `<work>@<position>` splitter that cut at the FIRST separator
// rather than the last, and two different ASIN derivations, one of which keys on the
// recording id alone and so hands two recordings of one slug the same globally-unique
// identifier. A fixture that differs between the suite that PROPOSES a repair and the
// suite that APPLIES it is the one kind of drift neither suite can catch.
//
// Every record is valid on purpose: a fixture that failed its schema would test the
// tooling's refusals rather than the rule under test, which is the repo's rule for a
// rule's fixtures. The suites keep their own helpers for the INVALID shapes they
// deliberately need (a work missing its language, a sidecar with no work).

// WorkOpt customizes a work record.
type WorkOpt func(map[string]any)

// WithLanguage sets the work language ("" is not valid; use the suite's own helper to
// remove a required field).
func WithLanguage(l string) WorkOpt {
	return func(m map[string]any) { m["language"] = l }
}

// WithAuthors sets the author person ids.
func WithAuthors(ids ...string) WorkOpt {
	return func(m map[string]any) { m["authors"] = ids }
}

// WithGenres sets the work's genres. They must be values of the schema's enum and sorted
// ascending, which is what pkg/check requires.
func WithGenres(gs ...string) WorkOpt {
	return func(m map[string]any) { m["genres"] = gs }
}

// WithSubtitle sets the subtitle.
func WithSubtitle(s string) WorkOpt {
	return func(m map[string]any) { m["subtitle"] = s }
}

// WithCredits sets the role-qualified credits, as alternating person and role arguments.
// A credit is load-bearing for the IDENTITY rule as well: check.IdentityEqualWorks
// discounts a role-credited person from a work's identity set, which is what lets two
// records whose author lists differ by a contributor meet.
func WithCredits(pairs ...string) WorkOpt {
	return func(m map[string]any) {
		credits := make([]map[string]string, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			credits = append(credits, map[string]string{"person": pairs[i], "role": pairs[i+1]})
		}
		m["credits"] = credits
	}
}

// WithWorkXref sets the work's cross-references to a print-ISBN list.
func WithWorkXref(isbns ...string) WorkOpt {
	return func(m map[string]any) { m["xref"] = map[string]any{"isbn": isbns} }
}

// WithAddedAt overwrites the added_at stamp, so a fixture can carry the RFC 3339
// spelling the storage migration's backfill wrote beside the plain date.
func WithAddedAt(v string) WorkOpt {
	return func(m map[string]any) { m["added_at"] = v }
}

// WorkJSON renders a minimal valid work.
func WorkJSON(t testing.TB, id, title string, opts ...WorkOpt) string {
	t.Helper()
	m := map[string]any{
		"id":       id,
		"title":    title,
		"authors":  []string{"jane-doe"},
		"language": "en",
		"license":  "CC0-1.0",
		"added_at": "2026-01-01",
		"sources":  []map[string]string{{"type": "libex-import", "ref": id, "imported_at": "2026-01-01"}},
	}
	for _, o := range opts {
		o(m)
	}
	return mustJSON(t, m)
}

// RecOpt customizes a recording record.
type RecOpt func(map[string]any)

// WithRuntime sets runtime_min.
func WithRuntime(min int) RecOpt {
	return func(m map[string]any) { m["runtime_min"] = min }
}

// WithNarrators sets the narrator person ids.
func WithNarrators(ids ...string) RecOpt {
	return func(m map[string]any) { m["narrators"] = ids }
}

// WithASIN replaces the derived identifier with a stated one, for a test that asserts on
// the value.
func WithASIN(asin string) RecOpt {
	return func(m map[string]any) { m["asin"] = []map[string]string{{"region": "us", "asin": asin}} }
}

// WithISBN sets the recording's ISBN list, in the bare (region-unstated) spelling.
func WithISBN(isbns ...string) RecOpt {
	return func(m map[string]any) { m["isbn"] = isbns }
}

// WithAbridged states the tri-state abridged flag. Absence is UNKNOWN, so a fixture that
// needs "unstated" simply omits this.
func WithAbridged(v bool) RecOpt {
	return func(m map[string]any) { m["abridged"] = v }
}

// WithoutIdentifiers removes the ASIN, which is what makes a recording answer no lookup.
func WithoutIdentifiers() RecOpt {
	return func(m map[string]any) { delete(m, "asin") }
}

// WithoutPublisher removes the publisher of record.
func WithoutPublisher() RecOpt {
	return func(m map[string]any) { delete(m, "publisher") }
}

// WithRecAddedAt overwrites the recording's added_at stamp.
func WithRecAddedAt(v string) RecOpt {
	return func(m map[string]any) { m["added_at"] = v }
}

// RecJSON renders a minimal valid recording of work.
//
// The ASIN and the provenance ref are derived from the WORK and the recording id
// together, not from the id alone: pkg/check enforces globally unique ASINs, and two
// records of one book carrying a recording under the same slug - which is what a
// duplicate cluster looks like - would otherwise be seeded with the same identifier.
func RecJSON(t testing.TB, id, work string, opts ...RecOpt) string {
	t.Helper()
	ref := work + "/" + id
	m := map[string]any{
		"id":        id,
		"work":      work,
		"narrators": []string{"nate-narrator"},
		"language":  "en",
		"license":   "CC0-1.0",
		"added_at":  "2026-01-01",
		"asin":      []map[string]string{{"region": "us", "asin": ASIN(ref)}},
		"sources":   []map[string]string{{"type": "libex-import", "ref": ref, "imported_at": "2026-01-01"}},
		// Publisher and release date are on every fixture recording because they are the
		// evidence the duplicate classes cite: a reviewer separates a dramatized
		// part-product from the plain edition by them.
		"publisher":    "Fixture Audio",
		"release_date": "2020-01-01",
	}
	for _, o := range opts {
		o(m)
	}
	return mustJSON(t, m)
}

// ASIN derives a unique, schema-valid ASIN from a seed string.
func ASIN(seed string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(seed); i++ {
		h = (h ^ uint64(seed[i])) * 1099511628211
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	out := []byte("B000000000")
	for i := 1; i < len(out); i++ {
		out[i] = alphabet[h%uint64(len(alphabet))]
		h /= uint64(len(alphabet))
	}
	return string(out)
}

// PersonJSON renders a minimal valid person. The id must be model.PersonSlug(name) or
// pkg/check refuses it.
func PersonJSON(t testing.TB, id, name string) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"id":      id,
		"name":    name,
		"license": "CC0-1.0",
		"sources": []map[string]string{{"type": "libex-import", "imported_at": "2026-01-01"}},
	})
}

// SeriesJSON renders a series over "<work>@<position>" members.
//
// The work and the position are split at the LAST separator, because a position may not
// contain one but a work slug can be anything - and splitting at the first turned
// "a@b@1" into a membership of "a" at "b@1".
func SeriesJSON(t testing.TB, id, name string, members ...string) string {
	t.Helper()
	works := make([]map[string]string, 0, len(members))
	for _, m := range members {
		work, pos, ok := cutLast(m, "@")
		if !ok {
			t.Fatalf("testpack: %q is not <work>@<position>", m)
		}
		works = append(works, map[string]string{"work": work, "position": pos})
	}
	return mustJSON(t, map[string]any{
		"id":      id,
		"name":    name,
		"works":   works,
		"license": "CC0-1.0",
		"sources": []map[string]string{{"type": "libex-import", "imported_at": "2026-01-01"}},
	})
}

// CharactersJSON renders a minimal valid characters sidecar, whose one entry carries the
// given id so a test can tell two sidecars apart.
func CharactersJSON(t testing.TB, work, charID string) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"work": work,
		"characters": []map[string]any{{
			"id": charID, "name": "Someone", "reveal": map[string]int{"chapter": 1},
			"description": "A character, described in the community's own words.",
		}},
		"license": "CC-BY-SA-4.0",
		"sources": []map[string]string{{"type": "community", "imported_at": "2026-01-01"}},
	})
}

// RecapsJSON renders a minimal valid recaps sidecar with one entry at the given chapter.
func RecapsJSON(t testing.TB, work string, throughChapter int) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"work": work,
		"recaps": []map[string]any{{
			"through": map[string]int{"chapter": throughChapter},
			"text":    "The story so far, in the community's own words.",
		}},
		"license": "CC-BY-SA-4.0",
		"sources": []map[string]string{{"type": "community", "imported_at": "2026-01-01"}},
	})
}

// WithField rewrites one member of an already-rendered record, and WithoutField removes
// one - the two edits a suite needs for the fixtures that must be schema-INVALID (a work
// with no language is the only way F-HYGIENE reports a missing one).
func WithField(t testing.TB, record, field string, value any) string {
	t.Helper()
	m := decodeRecord(t, record)
	m[field] = value
	return mustJSON(t, m)
}

// WithoutField removes a member from an already-rendered record.
func WithoutField(t testing.TB, record, field string) string {
	t.Helper()
	m := decodeRecord(t, record)
	delete(m, field)
	return mustJSON(t, m)
}

func decodeRecord(t testing.TB, record string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(record), &m); err != nil {
		t.Fatalf("testpack: %v", err)
	}
	return m
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("testpack: fixture marshal: %v", err)
	}
	return string(b)
}

// cutLast is strings.Cut from the RIGHT.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
