package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestReservedWorkSlugStepsOntoTheAuthorSuffix is the work half of the
// reserved-slug rule. "Search" slugs to an API route literal
// (/api/v1/works/search), so the work may not be stored there - a record at that
// id is findable by search and then unopenable. The row still imports: it takes
// the author-suffixed candidate, exactly as it would for a slug another book had
// already claimed. This is the shape the catalogue really held (the work "Search"
// by Alyssa Rose Ivy), which is why the rule shipped with a rename.
func TestReservedWorkSlugStepsOntoTheAuthorSuffix(t *testing.T) {
	row := `{"asin":"B0RESERV01","title":"Search","region":"us","language":"english",` +
		`"bookFormat":"unabridged","lengthMinutes":400,"authors":[{"name":"Alyssa Rose Ivy"}],` +
		`"narrators":[{"name":"James Fouhey"}]}`
	sum, dataDir := runLibex(t, row+"\n", false)

	if sum.NewWorks != 1 || sum.NewRecordings != 1 {
		t.Fatalf("summary = %d works / %d recordings, want 1/1: the row still imports", sum.NewWorks, sum.NewRecordings)
	}
	if entryExists(t, dataDir, workAddr("search")) {
		t.Error("a work was minted at the reserved slug \"search\"")
	}
	want := AuthorSuffixedWorkSlug("search", "alyssa-rose-ivy")
	if want != "search-alyssa-rose-ivy" {
		t.Fatalf("AuthorSuffixedWorkSlug = %q; the fixture assumes the plain author suffix", want)
	}
	if !entryExists(t, dataDir, workAddr(want)) {
		t.Errorf("no work at %q: the reserved base must step onto the author-suffixed candidate", want)
	}
	if !hasWarning(sum.Warnings, `work slug "search" is reserved`) {
		t.Errorf("no warning naming the reserved slug: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// TestReservedSeriesSlugStepsOntoTheNumericCandidate is the series half. The
// convention there is numeric, and the whole chain shifts by one (SeriesSlugAt),
// so every walker still stops at the first free slug.
func TestReservedSeriesSlugStepsOntoTheNumericCandidate(t *testing.T) {
	rows := []string{
		`{"asin":"B0RESERV02","title":"Volume One","region":"us","language":"english",` +
			`"bookFormat":"unabridged","lengthMinutes":300,"authors":[{"name":"Ada Mapper"}],` +
			`"narrators":[{"name":"Olga Volkova"}],"series":[{"name":"Latest","position":"1"}]}`,
		// A second volume of the SAME series, to prove the resolve path finds the
		// stepped slug rather than minting a third one beside it.
		`{"asin":"B0RESERV03","title":"Volume Two","region":"us","language":"english",` +
			`"bookFormat":"unabridged","lengthMinutes":300,"authors":[{"name":"Ada Mapper"}],` +
			`"narrators":[{"name":"Olga Volkova"}],"series":[{"name":"Latest","position":"2"}]}`,
	}
	sum, dataDir := runLibex(t, strings.Join(rows, "\n")+"\n", false)

	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1: both volumes belong to one series", sum.NewSeries)
	}
	if entryExists(t, dataDir, seriesAddr("latest")) {
		t.Error("a series was minted at the reserved slug \"latest\"")
	}
	want := SeriesSlugAt("latest", 0)
	if want != "latest-2" {
		t.Fatalf("SeriesSlugAt(\"latest\", 0) = %q; the fixture assumes the first numeric candidate", want)
	}
	var ser struct {
		Name  string `json:"name"`
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readEntity(t, dataDir, seriesAddr(want), &ser)
	if ser.Name != "Latest" || len(ser.Works) != 2 {
		t.Errorf("series at %q = %+v, want both volumes of \"Latest\"", want, ser)
	}
	if !hasWarning(sum.Warnings, `series slug "latest" is reserved`) {
		t.Errorf("no warning naming the reserved slug: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// TestReservedPersonSlugIsMintedAsTheCanonicalVariant is the person half, and it
// is the one that could not be solved by stepping onto a numbered sibling: a
// person's id must BE the slug of their name (pkg/check's checkPersonSlug), so
// the variant has to be derivable from the name alone. model.PersonSlug is that
// one derivation, and this pins that the importer mints exactly what the check
// rule expects - the assertion runs the real check over the written tree.
func TestReservedPersonSlugIsMintedAsTheCanonicalVariant(t *testing.T) {
	row := `{"asin":"B0RESERV04","title":"A Quiet Book","region":"us","language":"english",` +
		`"bookFormat":"unabridged","lengthMinutes":300,"authors":[{"name":"Search"}],` +
		`"narrators":[{"name":"Latest"}]}`
	_, dataDir := runLibex(t, row+"\n", false)

	for _, name := range []string{"Search", "Latest"} {
		want, fellBack := model.PersonSlug(name)
		if fellBack || !model.IsReservedSlug(strings.TrimSuffix(want, "-person")) {
			t.Fatalf("PersonSlug(%q) = %q; the fixture assumes a reserved name", name, want)
		}
		if entryExists(t, dataDir, "people/"+shard(strings.ToLower(name))+"/"+strings.ToLower(name)+".json") {
			t.Errorf("a person was minted at the reserved slug %q", strings.ToLower(name))
		}
		var p struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		readEntity(t, dataDir, "people/"+shard(want)+"/"+want+".json", &p)
		if p.ID != want || p.Name != name {
			t.Errorf("person = %+v, want id %q for name %q", p, want, name)
		}
	}
	assertTreeValid(t, dataDir)
}

// assertTreeValid runs the real validator over a written tree. For the reserved
// rule it is the whole point: minting and checking have to agree, and the only
// way to prove they do is to check what was minted.
func assertTreeValid(t *testing.T, dataDir string) {
	t.Helper()
	if res := check.Load(dataDir); !res.OK() {
		t.Errorf("the imported tree does not validate: %v", res.Problems)
	}
}
