package importer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

const relocateRow = `{"asin":"B000000001","title":"Das Buch","language":"german","region":"de","authors":[{"name":"Anne Author"}],"narrators":[{"name":"Nora Reader"}],"series":[{"name":"German Saga","position":"1"}],"lengthMinutes":100}`

func relocationFixture(t *testing.T, existing bool) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{}
	for _, p := range []struct{ id, name string }{{"anne-author", "Anne Author"}, {"nora-reader", "Nora Reader"}, {"english-reader", "English Reader"}} {
		files["people/"+p.id[:2]+"/"+p.id+".json"] = fmt.Sprintf(`{"id":%q,"name":%q,"license":"CC0-1.0","sources":[{"type":"user"}]}`, p.id, p.name)
	}
	addWork := func(id, title, lang string) {
		files["works/"+id[:2]+"/"+id+"/work.json"] = fmt.Sprintf(`{"id":%q,"title":%q,"language":%q,"authors":["anne-author"],"license":"CC0-1.0","sources":[{"type":"user"}]}`, id, title, lang)
	}
	addRec := func(work, id, lang, narr string) {
		files["works/"+work[:2]+"/"+work+"/recordings/"+id+".json"] = fmt.Sprintf(`{"id":%q,"work":%q,"language":%q,"narrators":[%q],"license":"CC0-1.0","sources":[{"type":"user"}]}`, id, work, lang, narr)
	}
	addWork("the-book", "The Book", "en")
	addRec("the-book", "english", "en", "english-reader")
	files["works/th/the-book/recordings/nora-reader-2020.json"] = `{"id":"nora-reader-2020","work":"the-book","language":"de","narrators":["nora-reader"],"runtime_min":100,"cover_url":"https://example.com/cover.jpg","chapters":[{"title":"One","start_ms":0,"length_ms":6000000}],"added_at":"2020-01-01","asin":[{"asin":"B000000001","region":"de"}],"isbn":["9780140328721"],"license":"CC0-1.0","sources":[{"type":"libex-import","ref":"B000000001","imported_at":"2020-01-01"}]}`
	for _, id := range []string{"volume-two", "volume-three"} {
		addWork(id, id, "de")
		addRec(id, "english-reader", "de", "english-reader")
	}
	if existing {
		addWork("das-buch", "Das Buch", "de")
		addRec("das-buch", "other", "de", "english-reader")
	}
	files["series/ge/german-saga.json"] = `{"id":"german-saga","name":"German Saga","license":"CC0-1.0","sources":[{"type":"user"}],"works":[{"work":"the-book","position":"1.0"},{"work":"volume-two","position":"2"},{"work":"volume-three","position":"3"}]}`
	seedTree(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatal(res.Problems)
	}
	return dir
}

func relocationObjects(t *testing.T, dir string) map[string]rawentry.Obj {
	t.Helper()
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res := check.LoadStore(s)
	if !res.OK() {
		t.Fatal(res.Problems)
	}
	out := map[string]rawentry.Obj{}
	for _, w := range res.Catalog.Works {
		b, ok, err := s.Get(pack.FamilyWorks, w.ID)
		if err != nil || !ok {
			t.Fatal(w.ID, err)
		}
		o, err := rawentry.Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		out[w.ID] = o
	}
	return out
}

func runRelocationTest(t *testing.T, dir, rows string) Summary {
	t.Helper()
	sum, err := RunLibex(writeBooks(t, rows), Options{DataDir: dir, ImportDate: testImportDate, Mode: ModeRelocate})
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func changeRelocationWork(t *testing.T, dir, id string, change func(rawentry.Obj)) {
	t.Helper()
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Get(pack.FamilyWorks, id)
	if err != nil {
		t.Fatal(err)
	}
	o, err := rawentry.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	change(o)
	if err = s.Upsert(pack.FamilyWorks, id, o.MustRaw()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestRelocateRawAndRepoint(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			dir := relocationFixture(t, existing)
			before := relocationObjects(t, dir)
			sum := runRelocationTest(t, dir, relocateRow)
			if len(sum.Relocations) != 1 || sum.MembershipsRepointed != 1 || sum.NewSeries != 0 {
				t.Fatalf("summary: %+v", sum)
			}
			if existing && sum.RelocatedToExisting != 1 || !existing && (sum.RelocatedToNewWork != 1 || sum.NewWorks != 1) {
				t.Fatalf("counts: %+v", sum)
			}
			after := relocationObjects(t, dir)
			oldRecs, _ := before["the-book"].Recordings()
			want := oldRecs["nora-reader-2020"].Clone()
			newRecs, _ := after["das-buch"].Recordings()
			got := newRecs["nora-reader-2020"].Clone()
			if len(got.Sources()) != 2 {
				t.Fatal("missing run provenance", got.Sources())
			}
			got.Drop("work")
			got.Drop("sources")
			want.Drop("work")
			want.Drop("sources")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("raw recording changed: %s / %s", got.MustRaw(), want.MustRaw())
			}
			delete(oldRecs, "nora-reader-2020")
			if err := before["the-book"].SetRecordings(oldRecs); err != nil {
				t.Fatal(err)
			}
			for id, w := range before {
				if id == "das-buch" {
					w = w.Clone()
					w.Drop("recordings")
					a := after[id].Clone()
					a.Drop("recordings")
					if !reflect.DeepEqual(w, a) {
						t.Fatal("destination work facts changed")
					}
					continue
				}
				if !reflect.DeepEqual(rawentry.DecodeOr[any](w.MustRaw()), rawentry.DecodeOr[any](after[id].MustRaw())) {
					t.Fatalf("unrelated fields changed on %s", id)
				}
			}
			var series model.Series
			readEntity(t, dir, "series/ge/german-saga.json", &series)
			if series.Works[0].Work != "das-buch" || series.Works[0].Position != "1.0" {
				t.Fatal(series.Works)
			}
			again := runRelocationTest(t, dir, relocateRow)
			if len(again.Files) != 0 || len(again.Relocations) != 0 || len(again.Skips) != 0 {
				t.Fatalf("not idempotent: %+v", again)
			}
		})
	}
}

func TestRelocateRefusals(t *testing.T) {
	tests := []struct {
		name, rows, code string
		change           func(rawentry.Obj)
	}{
		{"rows disagree", relocateRow + "\n" + strings.Replace(relocateRow, "german", "french", 1), RefusalRelocateRowsLanguage, nil},
		{"unknown language", strings.Replace(relocateRow, "german", "unknown", 1), RefusalRelocateRowsLanguage, nil},
		{"recording disagrees", strings.Replace(relocateRow, "german", "french", 1), RefusalRelocateRecordingLanguage, nil},
		{"no home narration", relocateRow, RefusalRelocateNoHomeRecording, func(o rawentry.Obj) {
			rs, _ := o.Recordings()
			delete(rs, "english")
			if err := o.SetRecordings(rs); err != nil {
				panic(err)
			}
		}},
		{"bad copy still votes", relocateRow + "\n" + strings.Replace(strings.Replace(relocateRow, "german", "french", 1), `"de"`, `"bogus"`, 1), RefusalRelocateRowsLanguage, nil},
		{"bad copy disqualifies", relocateRow + "\n" + strings.Replace(relocateRow, `"de"`, `"bogus"`, 1), RefusalRelocateRowUnusable, nil},
		{"missing author", strings.Replace(relocateRow, `[{"name":"Anne Author"}]`, `[]`, 1), RefusalMissingAuthor, nil},
		{"missing title", strings.Replace(relocateRow, `"Das Buch"`, `""`, 1), RefusalMissingTitle, nil},
		{"destinations disagree", relocateRow + "\n" + strings.Replace(relocateRow, "Das Buch", "Anderes Buch", 1), RefusalRelocateDestination, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := relocationFixture(t, false)
			if tt.change != nil {
				changeRelocationWork(t, dir, "the-book", tt.change)
			}
			before := snapshotTree(t, dir)
			sum := runRelocationTest(t, dir, tt.rows)
			if sum.RelocationRefusals[tt.code] != 1 || len(sum.Relocations) != 0 {
				t.Fatalf("summary: %+v", sum)
			}
			found := false
			for _, s := range sum.Skips {
				if s.Reason == tt.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing worklist refusal: %+v", sum.Skips)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, dir)) {
				t.Fatal("refusal changed tree")
			}
		})
	}
}

func TestRelocateNarratorVetoExcludesOrigin(t *testing.T) {
	dir := relocationFixture(t, false)
	for _, id := range []string{"volume-two", "volume-three"} {
		changeRelocationWork(t, dir, id, func(o rawentry.Obj) {
			o.Set("language", "fr")
			rs, _ := o.Recordings()
			for _, r := range rs {
				r.Set("language", "fr")
				r.Set("narrators", []string{"nora-reader"})
			}
			if err := o.SetRecordings(rs); err != nil {
				t.Fatal(err)
			}
		})
	}
	sum := runRelocationTest(t, dir, relocateRow)
	if sum.RelocationRefusals[RefusalRelocateNarration] != 1 {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestRelocateSiblingAndCollision(t *testing.T) {
	for _, kind := range []string{"merge", "runtime", "abridged", "narrators"} {
		t.Run(kind, func(t *testing.T) {
			dir := relocationFixture(t, true)
			changeRelocationWork(t, dir, "das-buch", func(o rawentry.Obj) {
				rs, _ := o.Recordings()
				r := rs["other"]
				delete(rs, "other")
				r.Set("id", "nora-reader-2020")
				r.Set("narrators", []string{"nora-reader"})
				r.Set("runtime_min", 100)
				r.Set("asin", []model.ASIN{{Region: "us", ASIN: "B000000002"}})
				switch kind {
				case "runtime":
					r.Set("runtime_min", 200)
				case "abridged":
					r.Set("abridged", true)
				case "narrators":
					r.Set("narrators", []string{"english-reader"})
				}
				rs["nora-reader-2020"] = r
				if err := o.SetRecordings(rs); err != nil {
					t.Fatal(err)
				}
			})
			sum := runRelocationTest(t, dir, relocateRow)
			if len(sum.Relocations) != 1 {
				t.Fatal(sum)
			}
			rs, _ := relocationObjects(t, dir)["das-buch"].Recordings()
			if kind == "merge" {
				r := rs["nora-reader-2020"]
				if sum.MergedIntoSibling != 1 || len(rs) != 1 || len(r.ASINs()) != 2 || len(r.ISBNs()) != 1 {
					t.Fatal(sum, rs)
				}
				for _, field := range []string{"cover_url=", "chapters=", "added_at="} {
					if !strings.Contains(strings.Join(sum.Notes, "\n"), field) {
						t.Fatal("lost value not noted", field)
					}
				}
			} else if len(rs) != 2 || sum.Relocations[0].DestinationRecording != "nora-reader-2020-2" {
				t.Fatal(sum, rs)
			}
		})
	}
}

func TestRelocateRowOrderAndNewWorkFacts(t *testing.T) {
	a := strings.Replace(relocateRow, `"Anne Author"`, `"Anne Author"},{"name":"Tina Translator - translator"`, 1)
	a = strings.Replace(a, `"lengthMinutes":100`, `"genres":[{"name":"Fantasy"}],"lengthMinutes":100`, 1)
	b := strings.Replace(a, `"B000000001"`, `"B000000003"`, 1)
	var want treeSnapshot
	for _, rows := range []string{a + "\n" + b, b + "\n" + a} {
		dir := relocationFixture(t, false)
		changeRelocationWork(t, dir, "the-book", func(o rawentry.Obj) {
			rs, _ := o.Recordings()
			rs["nora-reader-2020"].Set("asin", []model.ASIN{{Region: "de", ASIN: "B000000001"}, {Region: "de", ASIN: "B000000003"}})
			if err := o.SetRecordings(rs); err != nil {
				t.Fatal(err)
			}
		})
		sum := runRelocationTest(t, dir, rows)
		if sum.NewWorks != 1 || sum.Credits != 1 {
			t.Fatalf("create facts: %+v", sum)
		}
		w := relocationObjects(t, dir)["das-buch"]
		if len(w.Credits()) != 1 || len(w.Strs("genres")) == 0 {
			t.Fatal("missing creation facts", string(w.MustRaw()))
		}
		got := snapshotTree(t, dir)
		for k, v := range got {
			v.modTime = 0
			got[k] = v
		}
		if want != nil && !reflect.DeepEqual(got, want) {
			t.Fatal("row order changed output")
		}
		want = got
	}
}

func TestRelocateWorkLanguageVeto(t *testing.T) {
	// Already-homed recordings are omitted by candidate discovery, so a repeat
	// run is quiet. Pin the guard too for callers holding a stale candidate.
	p := &planner{works: map[string]*workState{"book": {lang: "de"}}, relocation: &relocationPlan{evidence: map[string][]string{"B000000001": {"de"}}}}
	if got := p.relocationVeto(&model.Recording{Work: "book", Language: "de"}, map[string]bool{"B000000001": true}); got != reasonRelocateWorkLanguage {
		t.Fatal(got)
	}
}

func TestRelocateNeverFoundsSeries(t *testing.T) {
	dir := relocationFixture(t, false)
	before := rawEntity(t, dir, "series/ge/german-saga.json")
	sum := runRelocationTest(t, dir, strings.Replace(relocateRow, "German Saga", "Uncatalogued Saga", 1))
	if sum.NewSeries != 0 || sum.SeriesClaimsDropped != 1 || sum.MembershipsRepointed != 0 {
		t.Fatal(sum)
	}
	if string(before) != string(rawEntity(t, dir, "series/ge/german-saga.json")) {
		t.Fatal("unclaimed membership changed")
	}
}

func TestRelocateIdentityGuard(t *testing.T) {
	dir := relocationFixture(t, false)
	seedTree(t, dir, map[string]string{"works/da/das-buch-german-edition/work.json": `{"id":"das-buch-german-edition","title":"Das Buch [German Edition]","language":"de","authors":["anne-author"],"license":"CC0-1.0","sources":[{"type":"user"}]}`})
	sum := runRelocationTest(t, dir, relocateRow)
	if sum.RelocationRefusals[RefusalIdentityDuplicate] != 1 {
		b, _ := json.Marshal(sum)
		t.Fatal(string(b))
	}
}

func TestRelocateExcludesAllOriginNarration(t *testing.T) {
	dir := relocationFixture(t, false)
	changeRelocationWork(t, dir, "the-book", func(o rawentry.Obj) {
		rs, _ := o.Recordings()
		for _, id := range []string{"english-two", "english-three"} {
			r := rs["english"].Clone()
			r.Set("id", id)
			r.Set("narrators", []string{"nora-reader"})
			rs[id] = r
		}
		if err := o.SetRecordings(rs); err != nil {
			t.Fatal(err)
		}
	})
	sum := runRelocationTest(t, dir, relocateRow)
	if len(sum.Relocations) != 1 {
		t.Fatal(sum)
	}
}

func TestRelocateOnlyMatchingLanguageAndSlotRepoints(t *testing.T) {
	for _, kind := range []string{"other-slot", "other-language", "tie"} {
		t.Run(kind, func(t *testing.T) {
			dir := relocationFixture(t, false)
			row := relocateRow
			switch kind {
			case "other-slot":
				row = strings.Replace(row, `"position":"1"`, `"position":"4"`, 1)
			case "other-language":
				for _, id := range []string{"volume-two", "volume-three"} {
					changeRelocationWork(t, dir, id, func(o rawentry.Obj) { o.Set("language", "en") })
				}
			case "tie":
				s, err := pack.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				raw, _, err := s.Get(pack.FamilySeries, "german-saga")
				if err != nil {
					t.Fatal(err)
				}
				o, err := rawentry.Decode(raw)
				if err != nil {
					t.Fatal(err)
				}
				o.Set("works", []model.SeriesWork{{Work: "the-book", Position: "1"}, {Work: "volume-two", Position: "2"}})
				if err = s.Upsert(pack.FamilySeries, "german-saga", o.MustRaw()); err != nil {
					t.Fatal(err)
				}
				if _, err = s.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			sum := runRelocationTest(t, dir, row)
			if sum.MembershipsRepointed != 0 || len(sum.Relocations) != 1 {
				t.Fatal(sum)
			}
			var series model.Series
			readEntity(t, dir, "series/ge/german-saga.json", &series)
			found := false
			for _, w := range series.Works {
				if w.Work == "the-book" {
					found = true
				}
			}
			if !found {
				t.Fatal("unmatched membership removed")
			}
		})
	}
}

func TestRelocateDryRunAndMissingRows(t *testing.T) {
	dir := relocationFixture(t, false)
	before := snapshotTree(t, dir)
	sum, err := RunLibex(writeBooks(t, relocateRow), Options{DataDir: dir, Mode: ModeRelocate, DryRun: true, ImportDate: testImportDate})
	if err != nil || len(sum.Relocations) != 1 {
		t.Fatal(sum, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, dir)) {
		t.Fatal("dry run wrote files")
	}
	sum = runRelocationTest(t, dir, strings.Replace(relocateRow, "B000000001", "B000000009", 1))
	if len(sum.Relocations) != 0 || len(sum.Files) != 0 || len(sum.Skips) != 0 {
		t.Fatal(sum)
	}
}
