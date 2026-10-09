// Package querytest builds a small, REAL release artifact for tests that run
// against pkg/query - a downstream consumer's (a mirror-mode AudioSilo server)
// as much as this module's own. The artifact is compiled by the real builder
// (internal/build) from a fixed in-repo fixture catalogue, so a test against it
// sees true metaserve behaviour: the same lookups, redirects, ordering and JSON
// shapes meta.audiosilo.app serves, at the current schema_version.
//
// The constants name the facts the fixture is known to hold, so a consumer's
// test asserts against them rather than against a copy of the fixture.
package querytest

import (
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

// The fixture's facts. Every id is a slug of the artifact. The literals are
// internal/artifacttest's, which builds the fixture from them.
const (
	// BuiltAt is the artifact's meta(built_at): fixed, so the build is
	// deterministic and a validator or golden naming it is stable.
	BuiltAt = artifacttest.BuiltAtRFC3339
	// Works, Recordings, People and Series are the catalogue's counts
	// (/api/v1/stats).
	Works      = 4
	Recordings = 2
	People     = 5
	Series     = 1

	// ASIN looks up (/api/v1/lookup?asin=) to ASINWork's recording
	// ASINRecording.
	ASIN          = artifacttest.HailMaryASIN
	ASINWork      = artifacttest.HailMary
	ASINRecording = artifacttest.HailMaryRecording
	// ISBN looks up (/api/v1/lookup?isbn=) to ISBNWork's recording
	// ISBNRecording.
	ISBN          = artifacttest.WayOfKingsISBN
	ISBNWork      = artifacttest.WayOfKings
	ISBNRecording = artifacttest.WayOfKingsRecording
	// MissingASIN is an ASIN the artifact does not hold: its lookup is a 404.
	MissingASIN = "B000000000"

	// SeriesID holds SeriesWorks works at numeric positions "1", "2" and "10" -
	// in that order, which a string sort would get wrong. SeriesWork is the work
	// at position "1".
	SeriesID    = artifacttest.Stormlight
	SeriesWorks = 3
	SeriesWork  = artifacttest.WayOfKings

	// ChapteredWork's recording ChapteredRecording carries Chapters chapters
	// (/api/v1/works/{id}/recordings/{rid}/chapters).
	ChapteredWork      = artifacttest.HailMary
	ChapteredRecording = artifacttest.HailMaryRecording
	Chapters           = 3

	// RetiredWork is a work slug a merge retired: /api/v1/works/RetiredWork
	// answers 301 to RetiredWorkTarget, the live work that replaced it.
	RetiredWork       = artifacttest.HailMaryRetired
	RetiredWorkTarget = artifacttest.HailMary

	// CommunityWork carries the CC BY-SA community layer: characters, chaptered
	// recaps, a recap summary (in_short and ending) and a community description.
	CommunityWork = artifacttest.HailMary
)

// Build writes the fixture artifact into dir (as meta.sqlite) and returns its
// path. Two builds are byte-identical.
func Build(tb testing.TB, dir string) string {
	tb.Helper()
	path := filepath.Join(dir, "meta.sqlite")
	artifacttest.BuildAt(tb, artifacttest.Fixture(), path)
	return path
}
