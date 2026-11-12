package importer

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The list-scoped doubled-credit rule (dropListDoubles): "X X" beside the same
// list's "X" is the source restating one person, while "X X" alone is a name.

// libex's BK_RECO_021995 credits ["XKarnation","XKarnation XKarnation"]: one
// person, and the doubled spelling's roles move onto the survivor.
func TestADoubledCreditBesideItsHalfIsDropped(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"one word, half first", []string{"XKarnation", "XKarnation XKarnation"}, []string{"XKarnation"}},
		{"one word, doubled first", []string{"XKarnation XKarnation", "XKarnation"}, []string{"XKarnation"}},
		{"case and diacritics fold", []string{"Zoë", "zoe ZOE"}, []string{"Zoë"}},
		{"beside a third credit", []string{"Ada Mapmaker", "Kenichi", "Kenichi Kenichi"}, []string{"Ada Mapmaker", "Kenichi"}},
		{"two doubles of one name", []string{"Kenichi Kenichi", "Kenichi", "kenichi kenichi"}, []string{"Kenichi"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := creditNamesOf(sourceCredits(c.in, "", creditCensus{})); !reflect.DeepEqual(got, c.want) {
				t.Errorf("sourceCredits(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	got := sourceCredits([]string{"Nemorosus", "Nemorosus Nemorosus - translator"}, "", creditCensus{})
	want := []credit{{name: "Nemorosus", roles: []string{model.RoleTranslator}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roles: got %+v, want %+v", got, want)
	}
	// The comma-joined shape reads the same list.
	if got := creditNamesOf(sourceCredits(nil, "XKarnation, XKarnation XKarnation", creditCensus{})); !reflect.DeepEqual(got, []string{"XKarnation"}) {
		t.Errorf("joined: got %q", got)
	}
}

// Credited alone, a one-word doubled name is a name (rule 4 keeps it), and so
// is one beside a credit it does not restate.
func TestADoubledCreditAloneStays(t *testing.T) {
	for _, in := range [][]string{
		{"Duran Duran"},
		{"Fang Fang"},
		{"Lang Lang"},
		{"Lang Lang", "Ada Mapmaker"},
		{"Fang Fang", "Fang Li"},
		{"Mitz Mitz Vah", "Mitz"},
	} {
		if got := creditNamesOf(sourceCredits(in, "", creditCensus{})); !reflect.DeepEqual(got, in) {
			t.Errorf("sourceCredits(%q) = %q, want it unchanged", in, got)
		}
	}
	// The name-only door (SplitNames: pkg/scan's tags, internal/issueform's
	// hand submissions) reads the same list.
	if got := SplitNames("Lang Lang"); !reflect.DeepEqual(got, []string{"Lang Lang"}) {
		t.Errorf("SplitNames = %q", got)
	}
	if got := SplitNames("Lang, Lang Lang"); !reflect.DeepEqual(got, []string{"Lang"}) {
		t.Errorf("SplitNames = %q", got)
	}
}

// The rule reads ONE list: a narrator "Lang Lang" is not dropped because the
// author list credits "Lang", and the other way round.
func TestTheListRuleIsPerSide(t *testing.T) {
	b := sourceBook{authors: []string{"Lang"}, narrators: []string{"Lang Lang"}}
	if got := creditNamesOf(rawAuthorCreditsIn(creditContext{}, b)); !reflect.DeepEqual(got, []string{"Lang"}) {
		t.Errorf("authors = %q", got)
	}
	if got := rawNarratorNamesIn(creditContext{}, b); !reflect.DeepEqual(got, []string{"Lang Lang"}) {
		t.Errorf("narrators = %q, want the doubled name kept", got)
	}
	b = sourceBook{authors: []string{"Lang Lang"}, narrators: []string{"Lang", "Lang Lang"}}
	if got := creditNamesOf(rawAuthorCreditsIn(creditContext{}, b)); !reflect.DeepEqual(got, []string{"Lang Lang"}) {
		t.Errorf("authors = %q, want the doubled name kept", got)
	}
	if got := rawNarratorNamesIn(creditContext{}, b); !reflect.DeepEqual(got, []string{"Lang"}) {
		t.Errorf("narrators = %q", got)
	}
}

// A LibriVox row's readers line up with its reader credits by index, so its
// narrator list keeps every name: two volunteers "Kim" and "Kim Kim" are two
// people, each found by reader id.
func TestALibriVoxReaderListKeepsItsIndex(t *testing.T) {
	b := sourceBook{narrators: []string{"Kim", "Kim Kim"}, lv: &librivoxFacts{}}
	if got := rawNarratorNamesIn(creditContext{}, b); !reflect.DeepEqual(got, []string{"Kim", "Kim Kim"}) {
		t.Errorf("LibriVox narrators = %q, want both kept", got)
	}

	snap := writeSnapshot(t, readProject(t, 91101,
		LibrivoxReader{ReaderID: lvInt(501), DisplayName: "Kim"},
		LibrivoxReader{ReaderID: lvInt(502), DisplayName: "Kim Kim"}))
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewRecordings != 1 {
		t.Fatalf("NewRecordings = %d, want 1\nwarnings: %v", sum.NewRecordings, sum.Warnings)
	}
	for slug, want := range map[string]int{"kim": 501, "kim-kim": 502} {
		if got := readerIDOf(t, dataDir, slug); got != want {
			t.Errorf("%s carries reader %d, want %d", slug, got, want)
		}
	}
}

// The Reborn shape (work reborn-as-a-demonic-tree-8): the catalogue holds the
// work credited to `xkarnation` with its CA recording only, and every other
// region's row credits ["XKarnation","XKarnation XKarnation"]. Read as two
// people, the row's authors were not the series' and libex-select refused it
// series-other-authors (and an import minted `xkarnation-xkarnation`); read as
// one, --attach-editions attaches it and the import merges its ASIN.
func TestSelectAttachesTheRebornShapeWithADoubledCredit(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/xk/xkarnation.json":      `{"id":"xkarnation","license":"CC0-1.0","name":"XKarnation","sources":[{"type":"user"}]}`,
		"people/ra/ramon-de-ocampo.json": `{"id":"ramon-de-ocampo","license":"CC0-1.0","name":"Ramon de Ocampo","sources":[{"type":"user"}]}`,
		"works/re/reborn-as-a-demonic-tree-8/work.json": `{"authors":["xkarnation"],"id":"reborn-as-a-demonic-tree-8","language":"en",` +
			`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"Reborn as a Demonic Tree 8"}`,
		"works/re/reborn-as-a-demonic-tree-8/recordings/ramon-de-ocampo-2026.json": `{"asin":[{"asin":"B0GV5B7VF2","region":"ca"}],` +
			`"id":"ramon-de-ocampo-2026","language":"en","license":"CC0-1.0","narrators":["ramon-de-ocampo"],"sources":[{"type":"user"}],` +
			`"work":"reborn-as-a-demonic-tree-8"}`,
		"series/re/reborn-as-a-demonic-tree.json": `{"id":"reborn-as-a-demonic-tree","license":"CC0-1.0","name":"Reborn as a Demonic Tree",` +
			`"sources":[{"type":"user"}],"works":[{"position":"8","work":"reborn-as-a-demonic-tree-8"}]}`,
	})
	row := `{"asin":"B0GV5C28QN","title":"Reborn as a Demonic Tree 8","region":"us","language":"english",` +
		`"authors":[{"name":"XKarnation"},{"name":"XKarnation XKarnation"}],"narrators":[{"name":"Ramon de Ocampo"}],` +
		`"series":[{"name":"Reborn as a Demonic Tree","position":"8"}]}`
	res, subset, refused := selectInto(t, dataDir, []string{row})
	if len(refused) != 0 || len(res.Attachments) != 1 {
		t.Fatalf("refusals = %+v, attachments = %+v; want the row kept for attachment", refused, res.Attachments)
	}
	sum, err := RunLibex(subset, Options{DataDir: dataDir, ImportDate: testImportDate, ExistingSeriesOnly: true, AttachEditions: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.Attached != 1 || sum.MergedASINs != 1 || sum.NewPeople != 0 || sum.NewWorks != 0 {
		t.Errorf("Attached/MergedASINs/NewPeople/NewWorks = %d/%d/%d/%d, want 1/1/0/0",
			sum.Attached, sum.MergedASINs, sum.NewPeople, sum.NewWorks)
	}
	if entryExists(t, dataDir, personAddr("xkarnation-xkarnation")) {
		t.Error("the doubled credit minted a person")
	}
	assertTreeValid(t, dataDir)
}
