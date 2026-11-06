package importer

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The store-listing import (store.go): a maintainer's reviewed file of store
// pages, through the ordinary create path.

const (
	guildURL = "https://audiobookguild.com/products/without-law-13"
	// guildRow is a Guild-only volume: its work, author, narrator and series are
	// all new to the catalogue. Its description and genres must never be read.
	guildRow = `{"store":"audiobookguild","url":"` + guildURL + `","title":"Without Law 13","authors":[{"name":"Eric Vall"}],` +
		`"narrators":[{"name":"Joshua Story"}],"series":[{"name":"Without Law","position":"13"}],"lengthMinutes":612,` +
		`"releaseDate":"2024-03-05","publisher":"Royal Guard Publishing","imageUrl":"https://cdn.shopify.com/s/files/wl13.jpg",` +
		`"language":"english","description":"A publisher blurb that must never land.","genres":[{"name":"Harem"}]}`
	wl13WorkRel = "works/wi/without-law-13/work.json"
	wl13RecRel  = "works/wi/without-law-13/recordings/joshua-story-2024.json"
)

// runStore runs the store-listing import over input against dataDir.
func runStore(t *testing.T, dataDir, input string, opts Options) Summary {
	t.Helper()
	opts.DataDir, opts.ImportDate = dataDir, testImportDate
	sum, err := RunStore(writeBooks(t, input), opts)
	if err != nil {
		t.Fatalf("store import: %v (%v)", err, sum.Warnings)
	}
	return sum
}

func TestStoreImportCreates(t *testing.T) {
	dataDir := t.TempDir()
	sum := runStore(t, dataDir, guildRow, Options{})
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.NewPeople != 2 || sum.NewSeries != 1 {
		t.Fatalf("summary = %+v, want 1 work, 1 recording, 2 people, 1 series", sum)
	}
	var rec model.Recording
	readEntity(t, dataDir, wl13RecRel, &rec)
	if rec.Links == nil || rec.Links.AudiobookGuild != guildURL || rec.Links.Librivox != "" {
		t.Errorf("links = %+v, want the Guild page alone", rec.Links)
	}
	if rec.RuntimeMin != 612 || rec.ReleaseDate != "2024-03-05" || rec.Publisher != "Royal Guard Publishing" ||
		rec.CoverURL != "https://cdn.shopify.com/s/files/wl13.jpg" || len(rec.ASIN) != 0 {
		t.Errorf("recording facts = %+v", rec)
	}
	want := model.Source{Type: model.SourceStoreListing, Ref: guildURL, ImportedAt: testImportDate}
	if len(rec.Sources) != 1 || rec.Sources[0] != want {
		t.Errorf("recording sources = %+v, want %+v", rec.Sources, want)
	}
	var work model.Work
	readEntity(t, dataDir, wl13WorkRel, &work)
	if work.Description != "" || len(work.Genres) != 0 || !slices.Equal(work.Authors, []string{"eric-vall"}) {
		t.Errorf("work = %+v: no description or genre is ever read from a store row", work)
	}
	if raw := string(rawEntity(t, dataDir, wl13WorkRel)); strings.Contains(raw, "blurb") {
		t.Errorf("the row's description reached the work: %s", raw)
	}
	var series model.Series
	readEntity(t, dataDir, seriesAddr("without-law"), &series)
	if len(series.Works) != 1 || series.Works[0] != (model.SeriesWork{Work: "without-law-13", Position: "13"}) {
		t.Errorf("series = %+v", series.Works)
	}

	// A rerun is a skip: the page is the row's identity, in either host spelling.
	again := runStore(t, dataDir, strings.Replace(guildRow, "https://audiobookguild.com", "https://www.audiobookguild.com", 1), Options{})
	if again.Skipped != 1 || again.Produced() != 0 {
		t.Errorf("rerun summary = %+v, want one skip and nothing produced", again)
	}
}

// TestStoreImportOnePagePerRun: two rows naming one page land once.
func TestStoreImportOnePagePerRun(t *testing.T) {
	dataDir := t.TempDir()
	twin := strings.Replace(strings.Replace(guildRow, "Joshua Story", "Someone Else", 1), "https://audiobookguild.com", "https://www.audiobookguild.com", 1)
	sum := runStore(t, dataDir, guildRow+"\n"+twin+"\n", Options{})
	if sum.NewRecordings != 1 || sum.Skipped != 1 {
		t.Errorf("summary = %+v, want one recording and one skip", sum)
	}
}

func TestStoreImportParseRefusals(t *testing.T) {
	cases := []struct {
		name, row, want string
	}{
		{"unknown store", strings.Replace(guildRow, `"store":"audiobookguild"`, `"store":"audiobooksnow"`, 1), `store "audiobooksnow" is not one of audiobookguild, graphicaudio`},
		{"another store's host", strings.Replace(guildRow, `"store":"audiobookguild"`, `"store":"graphicaudio"`, 1), "is not an https page on graphicaudio.net or www.graphicaudio.net"},
		{"plain http", strings.Replace(guildRow, "https://audiobookguild.com", "http://audiobookguild.com", 1), "is not an https page on audiobookguild.com"},
		{"no url", strings.Replace(guildRow, `"url":"`+guildURL+`",`, "", 1), `url "" is not an https page`},
		{"a query string", strings.Replace(guildRow, guildURL, guildURL+"?variant=2", 1), "(a path past the host, no query string or fragment)"},
		{"a fragment", strings.Replace(guildRow, guildURL, guildURL+"#reviews", 1), "(a path past the host, no query string or fragment)"},
		{"a bare homepage", strings.Replace(guildRow, guildURL, "https://audiobookguild.com/", 1), `url "https://audiobookguild.com/" is not an https page`},
		{"an ASIN", strings.Replace(guildRow, `"title"`, `"asin":"B0STORE001","title"`, 1), "a listing with an ASIN is imported through `metaimport libex`"},
		{"an AI narrator", strings.Replace(guildRow, "Joshua Story", "Virtual Voice", 1), "row skipped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataDir := t.TempDir()
			sum := runStore(t, dataDir, c.row, Options{})
			if sum.NewRecordings != 0 || sum.SkippedRows != 1 {
				t.Fatalf("summary = %+v, want the row refused at parse", sum)
			}
			if !slices.ContainsFunc(sum.Warnings, func(w string) bool { return strings.Contains(w, c.want) }) {
				t.Errorf("warnings = %q, want one containing %q", sum.Warnings, c.want)
			}
		})
	}
}

func TestStoreImportCreateOnly(t *testing.T) {
	if _, err := RunStore(writeBooks(t, guildRow), Options{DataDir: t.TempDir(), Mode: ModeEnrich}); err == nil {
		t.Error("a store import in enrich mode must be refused")
	}
}

// graphicRow is a full-cast production of a book the seed holds: a held work,
// a new production.
const (
	graphicURL = "https://www.graphicaudio.net/harry-potter-and-the-chamber-of-secrets.html"
	graphicRow = `{"store":"graphicaudio","url":"` + graphicURL + `","title":"Harry Potter and the Chamber of Secrets","authors":[{"name":"J.K. Rowling"}],` +
		`"narrators":[{"name":"A Full Cast"}],"lengthMinutes":400,"releaseDate":"2025-01-01","language":"english"}`
)

// TestStoreImportNewProductionOfAHeldWork: the identity walk lands the row on
// the held work as another recording, never a second work.
func TestStoreImportNewProductionOfAHeldWork(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	sum := runStore(t, dataDir, graphicRow, Options{})
	if sum.NewWorks != 0 || sum.NewRecordings != 1 {
		t.Fatalf("summary = %+v (%q), want one recording under the held work", sum, sum.Warnings)
	}
	recs := recSlugsOf(t, dataDir, "harry-potter-and-the-chamber-of-secrets")
	if !slices.Contains(recs, "full-cast-2025") {
		t.Fatalf("recordings = %v", recs)
	}
	var rec model.Recording
	readEntity(t, dataDir, recAddr("harry-potter-and-the-chamber-of-secrets", "full-cast-2025"), &rec)
	if rec.Links == nil || rec.Links.GraphicAudio != graphicURL {
		t.Errorf("links = %+v", rec.Links)
	}
}

// TestStoreImportAddsThePageToTheHeldProduction: a listing of a production the
// catalogue holds (same narrators, compatible runtime) adds its page there.
func TestStoreImportAddsThePageToTheHeldProduction(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	row := strings.NewReplacer("A Full Cast", "Stephen Fry", `"lengthMinutes":400`, `"lengthMinutes":580`).Replace(graphicRow)
	sum := runStore(t, dataDir, row, Options{})
	if sum.NewRecordings != 0 || sum.StoreLinksAdded != 1 {
		t.Fatalf("summary = %+v (%q), want the page added to the held recording", sum, sum.Warnings)
	}
	var rec model.Recording
	readEntity(t, dataDir, hpChamberFryRel, &rec)
	if rec.Links == nil || rec.Links.GraphicAudio != graphicURL || len(rec.ASIN) != 1 || rec.RuntimeMin != 583 {
		t.Errorf("recording = %+v links %+v: only the page is added", rec, rec.Links)
	}
	if !slices.ContainsFunc(rec.Sources, func(s model.Source) bool { return s.Type == model.SourceStoreListing && s.Ref == graphicURL }) {
		t.Errorf("sources = %+v, want the store stamp", rec.Sources)
	}

	// The same production listed under ANOTHER page is refused; the stated page
	// wins, and the refused row places nothing (no series founded for its claim).
	other := strings.Replace(strings.Replace(row, graphicURL, "https://www.graphicaudio.net/hp-2-reissue.html", 1),
		`"lengthMinutes"`, `"series":[{"name":"Wizarding Reissues","position":"2"}],"lengthMinutes"`, 1)
	sum = runStore(t, dataDir, other, Options{})
	if sum.Produced() != 0 || !slices.ContainsFunc(sum.Warnings, func(w string) bool { return strings.Contains(w, "already states links.graphicaudio") }) {
		t.Errorf("summary = %+v (%q), want a refusal naming the stated page", sum, sum.Warnings)
	}
	readEntity(t, dataDir, hpChamberFryRel, &rec)
	if rec.Links.GraphicAudio != graphicURL {
		t.Errorf("the stated page was replaced: %+v", rec.Links)
	}
}

// frySecond is a second Stephen Fry production of Chamber of Secrets, for the
// "exactly one qualifies" cases; runtime is its runtime_min ("" for none).
func frySecond(runtime string) string {
	rt := ""
	if runtime != "" {
		rt = `"runtime_min":` + runtime + `,`
	}
	return `{"id":"stephen-fry-2020","language":"en","license":"CC0-1.0","narrators":["stephen-fry"],` + rt +
		`"sources":[{"type":"user"}],"work":"harry-potter-and-the-chamber-of-secrets"}`
}

const frySecondRel = "works/ha/harry-potter-and-the-chamber-of-secrets/recordings/stephen-fry-2020.json"

// seedStoreTree seeds the recordings catalogue with extra records added or
// replaced, in one write (a work's recordings share its entry).
func seedStoreTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := recordingsSeedFiles()
	maps.Copy(files, extra)
	dataDir := t.TempDir()
	seedTree(t, dataDir, files)
	return dataDir
}

// TestStoreImportRefusesAnUnprovenProduction: a store row adds its page to a
// held same-narrator recording only when both state a runtime, the runtimes
// agree and exactly one recording qualifies. Every other case is a refusal
// with a warning - never the page on the first match, never a twin recording.
func TestStoreImportRefusesAnUnprovenProduction(t *testing.T) {
	fry := func(runtime string) string {
		return strings.NewReplacer("A Full Cast", "Stephen Fry", `"lengthMinutes":400`, runtime).Replace(graphicRow)
	}
	cases := []struct {
		name, row string
		seed      map[string]string
		want      string
	}{
		{"the row states no runtime", fry(`"lengthMinutes":0`), nil, "do not both state a runtime"},
		{"the held recording states no runtime", fry(`"lengthMinutes":580`),
			map[string]string{hpChamberFryRel: strings.Replace(hpChamberFry, `"runtime_min":583,`, "", 1)}, "do not both state a runtime"},
		{"the runtimes disagree", fry(`"lengthMinutes":300`), nil, "runs 583 minutes and this row 300"},
		{"two held recordings qualify", fry(`"lengthMinutes":580`), map[string]string{frySecondRel: frySecond("590")}, "2 same-narrator recordings could be this row's production"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataDir := seedStoreTree(t, c.seed)
			before := recSlugsOf(t, dataDir, "harry-potter-and-the-chamber-of-secrets")
			sum := runStore(t, dataDir, c.row, Options{})
			if sum.Produced() != 0 || sum.StoreLinksAdded != 0 {
				t.Fatalf("summary = %+v (%q), want the row refused", sum, sum.Warnings)
			}
			if !slices.ContainsFunc(sum.Warnings, func(w string) bool { return strings.Contains(w, c.want) && strings.Contains(w, "not added") }) {
				t.Errorf("warnings = %q, want one containing %q", sum.Warnings, c.want)
			}
			if after := recSlugsOf(t, dataDir, "harry-potter-and-the-chamber-of-secrets"); !slices.Equal(after, before) {
				t.Errorf("recordings = %v, want unchanged %v", after, before)
			}
			for _, rel := range []string{hpChamberFryRel, frySecondRel} {
				if !entryExists(t, dataDir, rel) {
					continue
				}
				var rec model.Recording
				readEntity(t, dataDir, rel, &rec)
				if rec.Links != nil {
					t.Errorf("%s gained a page it is not proven to be: %+v", rel, rec.Links)
				}
			}
		})
	}
}

// TestStoreImportOneQualifyingAmongSeveral: two same-narrator recordings, one
// whose runtime agrees with the row - that one alone gains the page.
func TestStoreImportOneQualifyingAmongSeveral(t *testing.T) {
	dataDir := seedStoreTree(t, map[string]string{frySecondRel: frySecond("300")})
	row := strings.NewReplacer("A Full Cast", "Stephen Fry", `"lengthMinutes":400`, `"lengthMinutes":305`).Replace(graphicRow)
	sum := runStore(t, dataDir, row, Options{})
	if sum.StoreLinksAdded != 1 || sum.NewRecordings != 0 {
		t.Fatalf("summary = %+v (%q), want the page on the one qualifying recording", sum, sum.Warnings)
	}
	var second, first model.Recording
	readEntity(t, dataDir, frySecondRel, &second)
	readEntity(t, dataDir, hpChamberFryRel, &first)
	if second.Links == nil || second.Links.GraphicAudio != graphicURL || first.Links != nil {
		t.Errorf("links = %+v / %+v, want the page on stephen-fry-2020 alone", second.Links, first.Links)
	}
}

// A store row's conflict line names its page, its identifier in place of an
// ASIN, so a worklist row traces back to the listing that raised it.
func TestStoreConflictLineNamesThePage(t *testing.T) {
	var buf strings.Builder
	p := &planner{conflicts: &buf, sourceType: model.SourceStoreListing, importDate: testImportDate}
	p.recordConflict(sourceBook{raw: rawBook{}, store: &storeFacts{key: model.StoreAudiobookGuild, url: guildURL}},
		RecRef{Work: "w"}, conflictFieldWorkIdentity, "Held Title", "Row Title")
	if !strings.Contains(buf.String(), `"store_url":"`+guildURL+`"`) {
		t.Errorf("conflict line = %s, want it to carry the store page", buf.String())
	}
}
