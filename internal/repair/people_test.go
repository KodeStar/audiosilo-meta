package repair

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// people_test.go covers merge-people and merge-recordings, the two ops only a reviewed
// assertion sources. The audit-to-repair round trip is internal/audit's
// TestReviewedAssertedFoldsReachMetarepair; these reach the planners directly.

// hunterTree is one author's two person records, each credited somewhere the other is
// too: a work listing both, a recording's narrators, a credit and a series' authors.
func hunterTree(t testing.TB) map[string]string {
	t.Helper()
	jah := testpack.WithFields(t, personJSON(t, "james-a-hunter", "James A. Hunter"),
		map[string]any{"librivox_author_id": 11})
	jh := testpack.WithFields(t, personJSON(t, "james-hunter", "James Hunter"), map[string]any{
		"librivox_author_id": 12, "birth_year": 1980, "sort_name": "Hunter, James",
		"sources": []map[string]string{{"type": "librivox", "ref": "12", "imported_at": "2026-02-02"}},
	})
	return map[string]string{
		"people/ja/james-a-hunter.json": jah,
		"people/ja/james-hunter.json":   jh,
		"people/ja/jane-doe.json":       personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":  personJSON(t, "nate-narrator", "Nate Narrator"),
		"works/vi/viridian-gate/work.json": workJSON(t, "viridian-gate", "Viridian Gate",
			withAuthors("james-hunter", "jane-doe", "james-a-hunter")),
		"works/vi/viridian-gate/recordings/r.json": recJSON(t, "r", "viridian-gate"),
		"works/vi/vigil/work.json": workJSON(t, "vigil", "Vigil",
			withAuthors("jane-doe"), withCredits("james-hunter", "editor")),
		"works/vi/vigil/recordings/r.json": testpack.WithFields(t, recJSON(t, "r", "vigil", withNarrators("james-hunter", "nate-narrator", "james-a-hunter")),
			map[string]any{"librivox_id": 4242, "chapters": []map[string]any{
				{"title": "One", "start_ms": 0, "length_ms": 60000, "readers": []string{"james-hunter"}},
				{"title": "Two", "start_ms": 60000, "length_ms": 1500, "readers": []string{"nate-narrator", "james-hunter", "james-a-hunter"}},
			}}),
		"works/ot/other/work.json":         workJSON(t, "other", "Other", withAuthors("james-a-hunter")),
		"works/ot/other/recordings/r.json": recJSON(t, "r", "other"),
		"series/vi/viridian.json": testpack.WithFields(t, seriesJSON(t, "viridian", "Viridian Gate Online", "viridian-gate@1"),
			map[string]any{"authors": []string{"james-hunter"}}),
	}
}

func peopleFinding(target string, others ...string) audit.Finding {
	return audit.Finding{Class: audit.ClassPersonDup, Subclass: audit.SubclassAsserted, Key: "asserted/" + target,
		Propose: audit.Proposal{Op: audit.OpMergePeople, Target: target, Others: others}}
}

func TestMergePeopleRecreditsEveryRecordAndRetiresTheLoser(t *testing.T) {
	rn, tx := planFixture(t, seedTree(t, hunterTree(t)))
	if err := rn.mergePeople(tx, peopleFinding("james-a-hunter", "james-hunter")); err != nil {
		t.Fatal(err)
	}
	staged := func(st *stage, slug string) entry {
		t.Helper()
		e, ok := st.puts[slug]
		if !ok {
			t.Fatalf("%s not staged", slug)
		}
		return e
	}
	if got := staged(tx.works, "viridian-gate").Strs("authors"); !slices.Equal(got, []string{"james-a-hunter", "jane-doe"}) {
		t.Errorf("viridian-gate authors = %v, want the loser re-pointed and the repeat dropped in first-seen order", got)
	}
	vigil := staged(tx.works, "vigil")
	if got := vigil.Credits(); len(got) != 1 || got[0].Person != "james-a-hunter" {
		t.Errorf("vigil credits = %+v", got)
	}
	recs, err := vigil.Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if got := recs["r"].Strs("narrators"); !slices.Equal(got, []string{"james-a-hunter", "nate-narrator"}) {
		t.Errorf("vigil narrators = %v", got)
	}
	chapters := recs["r"].Chapters()
	if len(chapters) != 2 || !slices.Equal(chapters[0].Readers, []string{"james-a-hunter"}) ||
		!slices.Equal(chapters[1].Readers, []string{"nate-narrator", "james-a-hunter"}) || chapters[1].LengthMS != 1500 {
		t.Errorf("vigil chapters = %+v, want the readers re-pointed and the timeline untouched", chapters)
	}
	if _, touched := tx.works.puts["other"]; touched {
		t.Error("other names only the survivor and must not be rewritten")
	}
	if got := staged(tx.series, "viridian").Strs("authors"); !slices.Equal(got, []string{"james-a-hunter"}) {
		t.Errorf("series authors = %v", got)
	}
	p := staged(tx.people, "james-a-hunter")
	if id, _ := p.IntAt("librivox_author_id"); id != 11 {
		t.Errorf("librivox_author_id = %d, want the survivor's own", id)
	}
	if got := rawentry.DecodeOr[[]int](p["librivox_author_aliases"]); !slices.Equal(got, []int{12}) {
		t.Errorf("aliases = %v, want the loser's id kept as an alias", got)
	}
	if p.Str("sort_name") != "Hunter, James" || len(p.Sources()) != 2 {
		t.Errorf("person = %v, want the gap filled and the sources unioned", p)
	}
	if !tx.people.dels["james-hunter"] {
		t.Error("the loser is not deleted")
	}
	if len(tx.tombs) != 1 || tx.tombs[0] != (tomb{kind: model.RedirectPeople, from: "james-hunter", to: "james-a-hunter"}) {
		t.Errorf("tombstones = %+v", tx.tombs)
	}
}

// The applied fold leaves a tree pkg/check accepts, and the index learns it: a later
// fold of the survivor reaches the records the first one re-credited.
func TestMergePeopleWritesAValidTreeAndChainsFolds(t *testing.T) {
	files := hunterTree(t)
	files["people/jd/j-a-hunter.json"] = personJSON(t, "j-a-hunter", "J. A. Hunter")
	data := seedTree(t, files)
	rn, tx := planFixture(t, data)
	if err := rn.mergePeople(tx, peopleFinding("james-a-hunter", "james-hunter")); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit("first"); err != nil {
		t.Fatal(err)
	}
	tx = rn.plan.begin()
	if err := rn.mergePeople(tx, peopleFinding("j-a-hunter", "james-a-hunter")); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit("second"); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"viridian-gate", "vigil", "other"} {
		e, _, _ := rn.plan.works.get(slug)
		if raw := string(e.MustRaw()); slices.ContainsFunc([]string{`"james-hunter"`, `"james-a-hunter"`}, func(s string) bool {
			return strings.Contains(raw, s)
		}) {
			t.Errorf("%s still names a retired person: %s", slug, raw)
		}
	}
	if err := rn.apply(rn.plan.people.write); err != nil {
		t.Fatalf("apply: %v (post-write problems %v)", err, rn.rep.PostProblems)
	}
	reds := loadRedirects(t, data)
	if reds.Live(model.RedirectPeople, "james-hunter") != "j-a-hunter" {
		t.Errorf("james-hunter resolves to %q", reds.Live(model.RedirectPeople, "james-hunter"))
	}
}

func TestMergePeopleRefusesAMissingPerson(t *testing.T) {
	rn, tx := planFixture(t, seedTree(t, hunterTree(t)))
	assertRefusal(t, rn.mergePeople(tx, peopleFinding("james-a-hunter", "jim-hunter")), CatMissing, "jim-hunter")
	assertRefusal(t, rn.mergePeople(tx, peopleFinding("james-a-hunter")), CatMalformed, "no other members")
}

// reissueTree is one work holding the same production twice: one narrator, runtimes
// within a minute, two ASINs from two publishers.
func reissueTree(t testing.TB, moverOpts ...recOpt) map[string]string {
	t.Helper()
	return map[string]string{
		"people/ja/jane-doe.json":      personJSON(t, "jane-doe", "Jane Doe"),
		"people/ra/ralph-cosham.json":  personJSON(t, "ralph-cosham", "Ralph Cosham"),
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
		"works/po/postwar/work.json":   workJSON(t, "postwar", "Postwar"),
		"works/po/postwar/recordings/ralph-cosham-2011.json": recJSON(t, "ralph-cosham-2011", "postwar",
			withNarrators("ralph-cosham"), withRuntime(2581), withASIN("B004I6XPO0")),
		"works/po/postwar/recordings/ralph-cosham-2017.json": recJSON(t, "ralph-cosham-2017", "postwar",
			append([]recOpt{withNarrators("ralph-cosham"), withRuntime(2580), withASIN("B078GVB8P3"),
				testpack.WithPublisher("Vintage Digital")}, moverOpts...)...),
	}
}

func recordingsFinding(work, from, to string) audit.Finding {
	return audit.Finding{Class: audit.ClassWorkDup, Subclass: audit.SubclassAsserted, Key: "asserted/" + work + "/" + from,
		Propose: audit.Proposal{Op: audit.OpMergeRecordings, Target: work, From: from, To: to}}
}

func TestMergeRecordingsFoldsOneProduction(t *testing.T) {
	rn, tx := planFixture(t, seedTree(t, reissueTree(t)))
	if err := rn.mergeRecordings(tx, recordingsFinding("postwar", "ralph-cosham-2017", "ralph-cosham-2011")); err != nil {
		t.Fatal(err)
	}
	recs, err := tx.works.puts["postwar"].Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if _, left := recs["ralph-cosham-2017"]; left || len(recs) != 1 {
		t.Fatalf("recordings = %v, want the one keeper", rawentry.SortedKeys(recs))
	}
	keep := recs["ralph-cosham-2011"]
	if asins := keep.ASINs(); len(asins) != 2 || len(keep.Sources()) != 2 {
		t.Errorf("keeper = %v, want both ASINs and both sources", keep)
	}
	if keep.Str("publisher") != "Fixture Audio" {
		t.Errorf("publisher = %q, want the keeper's own", keep.Str("publisher"))
	}
	if !slices.ContainsFunc(tx.notes, func(n string) bool { return strings.Contains(n, "publisher: kept") }) {
		t.Errorf("notes %v do not name the publisher chosen away", tx.notes)
	}
}

func TestMergeRecordingsRefusesTwoProductions(t *testing.T) {
	rn, tx := planFixture(t, seedTree(t, reissueTree(t, withNarrators("nate-narrator"))))
	assertRefusal(t, rn.mergeRecordings(tx, recordingsFinding("postwar", "ralph-cosham-2017", "ralph-cosham-2011")),
		CatProduction, "narrators differ")
	assertRefusal(t, rn.mergeRecordings(tx, recordingsFinding("postwar", "ralph-cosham-2019", "ralph-cosham-2011")),
		CatMissing, "ralph-cosham-2019")
	if len(tx.works.puts) != 0 {
		t.Errorf("a refused fold staged %v", rawentry.SortedKeys(tx.works.puts))
	}
}

// A LibriVox recording read by two records of one reader collapses to one narrator:
// the chapter readers go too, since pkg/check refuses readers on every chapter of a
// one-narrator recording, and the applied tree validates.
func TestMergePeopleDropsTheReadersOfASoleNarrator(t *testing.T) {
	files := hunterTree(t)
	files["works/vi/vigil/recordings/r.json"] = testpack.WithFields(t, recJSON(t, "r", "vigil", withNarrators("james-hunter", "james-a-hunter")),
		map[string]any{"librivox_id": 4242, "chapters": []map[string]any{
			{"title": "One", "start_ms": 0, "length_ms": 60000, "readers": []string{"james-hunter"}},
			{"title": "Two", "start_ms": 60000, "length_ms": 1500, "readers": []string{"james-a-hunter"}},
		}})
	rn, tx := planFixture(t, seedTree(t, files))
	if err := rn.mergePeople(tx, peopleFinding("james-a-hunter", "james-hunter")); err != nil {
		t.Fatal(err)
	}
	recs, err := tx.works.puts["vigil"].Recordings()
	if err != nil {
		t.Fatal(err)
	}
	for i, ch := range recs["r"].Chapters() {
		if len(ch.Readers) != 0 || (i == 1 && ch.LengthMS != 1500) {
			t.Errorf("chapter %d = %+v, want no readers and the timeline untouched", i, ch)
		}
	}
	if err := tx.commit("fold"); err != nil {
		t.Fatal(err)
	}
	if err := rn.apply(rn.plan.people.write); err != nil {
		t.Fatalf("apply: %v (post-write problems %v)", err, rn.rep.PostProblems)
	}
}
