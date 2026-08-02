package importer

import (
	"strings"
	"testing"
)

// runLibexInto runs the libex CREATE importer against an existing (possibly
// seeded) data dir, which the shared runLibex helper cannot do - it always
// starts from an empty tree, and half of these rules are about how an incoming
// row meets a work the catalogue already holds.
func runLibexInto(t *testing.T, dataDir, exportJSON string) Summary {
	t.Helper()
	sum, err := RunLibex(writeBooks(t, exportJSON), Options{
		DataDir: dataDir, ImportDate: testImportDate,
	})
	if err != nil {
		t.Fatalf("libex create run: %v", err)
	}
	return sum
}

// libexRow renders one libex NDJSON row from the fields these tests vary.
// Everything else is held constant so a difference in outcome can only come
// from what the test changed.
type libexRow struct {
	asin      string
	title     string
	subtitle  string
	authors   string // a JSON array body, e.g. `{"name":"A"}`
	narrators string
	series    string // a JSON array body, e.g. `{"name":"S","position":"1"}`
	minutes   int
	language  string
}

func (r libexRow) render() string {
	lang := r.language
	if lang == "" {
		lang = "english"
	}
	mins := r.minutes
	if mins == 0 {
		mins = 500
	}
	narr := r.narrators
	if narr == "" {
		narr = `{"name":"Ann Reader"}`
	}
	var b strings.Builder
	b.WriteString(`{"asin":"` + r.asin + `","title":"` + r.title + `"`)
	if r.subtitle != "" {
		b.WriteString(`,"subtitle":"` + r.subtitle + `"`)
	}
	b.WriteString(`,"region":"us","language":"` + lang + `","bookFormat":"unabridged"`)
	b.WriteString(`,"releaseDate":"2020-01-01 00:00:00+00"`)
	b.WriteString(`,"lengthMinutes":` + itoa(mins))
	b.WriteString(`,"authors":[` + r.authors + `]`)
	b.WriteString(`,"narrators":[` + narr + `]`)
	if r.series != "" {
		b.WriteString(`,"series":[` + r.series + `]`)
	}
	b.WriteString("}")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func rows(rs ...libexRow) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.render())
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// Same-title serial disambiguation

// TestSerialVolumesSharingOneTitleBecomeDistinctWorks is the Bravelands case,
// reduced: the dump really does carry six rows titled exactly "Bravelands" at
// series positions 1-6, with no subtitle for the full-title fallback to use.
// They must not collapse - and above all their ASINs must not merge onto one
// recording, which is what made three different books one production.
func TestSerialVolumesSharingOneTitleBecomeDistinctWorks(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BRAVELN1", title: "Bravelands", authors: author, minutes: 563,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0BRAVELN2", title: "Bravelands", authors: author, minutes: 620,
			series: `{"name":"Bravelands","position":"2"}`},
		libexRow{asin: "B0BRAVELN3", title: "Bravelands", authors: author, minutes: 533,
			series: `{"name":"Bravelands","position":"4"}`},
	), false)

	if sum.NewWorks != 3 || sum.NewRecordings != 3 {
		t.Fatalf("three volumes must be three works with three recordings: %+v", sum)
	}
	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0: a sibling volume's ASIN was folded into another book", sum.MergedASINs)
	}
	for _, slug := range []string{"bravelands-book-1", "bravelands-book-2", "bravelands-book-4"} {
		if !entryExists(t, dataDir, "works/"+shard(slug)+"/"+slug+"/work.json") {
			t.Errorf("no work at %q", slug)
		}
	}
	// And all three sit in the series at their own positions.
	var series struct {
		Works []struct{ Work, Position string } `json:"works"`
	}
	readEntity(t, dataDir, "series/br/bravelands.json", &series)
	if len(series.Works) != 3 {
		t.Fatalf("series membership = %+v, want three volumes", series.Works)
	}
	for _, w := range series.Works {
		if w.Work != "bravelands-book-"+w.Position {
			t.Errorf("series lists %q at position %q", w.Work, w.Position)
		}
	}
}

// TestSameTitleSamePositionStillMergesToOneWork is the other half of the rule:
// two regional editions of ONE volume state the same position, so they are one
// book and must still fold into one work and one recording. Over-splitting the
// serial case would break the merge the importer exists to do.
func TestSameTitleSamePositionStillMergesToOneWork(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BRAVEUS1", title: "Bravelands", authors: author, minutes: 563,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0BRAVEUK1", title: "Bravelands", authors: author, minutes: 563,
			series: `{"name":"Bravelands","position":"1"}`},
	), false)

	if sum.NewWorks != 1 || sum.NewRecordings != 1 {
		t.Fatalf("two editions of one volume must be one work: %+v", sum)
	}
	if sum.MergedASINs != 1 {
		t.Errorf("MergedASINs = %d, want 1", sum.MergedASINs)
	}
	if !entryExists(t, dataDir, "works/br/bravelands/work.json") {
		t.Error("the undisambiguated slug must still be used when nothing needs splitting")
	}
}

// TestDistinctlyTitledVolumesAreUntouched is the control: a serial whose
// volumes carry their own titles never reaches the position pre-pass, so its
// slugs are exactly what they were before the rule existed.
func TestDistinctlyTitledVolumesAreUntouched(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	_, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BRAVEDT1", title: "Broken Pride", authors: author, minutes: 563,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0BRAVEDT2", title: "Code of Honor", authors: author, minutes: 620,
			series: `{"name":"Bravelands","position":"2"}`},
	), false)

	for _, slug := range []string{"broken-pride", "code-of-honor"} {
		if !entryExists(t, dataDir, "works/"+shard(slug)+"/"+slug+"/work.json") {
			t.Errorf("no work at %q: the pre-pass renamed a work it should not have touched", slug)
		}
	}
}

// TestSubtitledVolumesStillUseTheFullTitleFallback pins the ORDER of the two
// title pre-passes: when the full title CAN separate two same-short-titled
// volumes it still does, and the position suffix never fires.
func TestSubtitledVolumesStillUseTheFullTitleFallback(t *testing.T) {
	const author = `{"name":"Erin Hunter"}`
	_, dataDir := runLibex(t, rows(
		libexRow{asin: "B0BRAVEFT1", title: "Bravelands", subtitle: "Broken Pride", authors: author, minutes: 563,
			series: `{"name":"Bravelands","position":"1"}`},
		libexRow{asin: "B0BRAVEFT2", title: "Bravelands", subtitle: "Code of Honor", authors: author, minutes: 620,
			series: `{"name":"Bravelands","position":"2"}`},
	), false)

	for _, slug := range []string{"bravelands-broken-pride", "bravelands-code-of-honor"} {
		if !entryExists(t, dataDir, "works/"+shard(slug)+"/"+slug+"/work.json") {
			t.Errorf("no work at %q: the full-title fallback must still own this case", slug)
		}
	}
}

// TestASINMergeBlockedByADifferentSeriesPosition is the per-row guard on its
// own, reached without the pre-pass: the two rows have DIFFERENT titles, so
// they are two works, but the second row is then forced onto the first work by
// a title the pre-pass cannot group. The recording-level guard is what stops the
// ASIN merge, and it must say so.
func TestASINMergeBlockedByADifferentSeriesPosition(t *testing.T) {
	// One work, two rows: same author, same narrator, compatible runtimes, and
	// the SAME work title - but the second states a different series position.
	// The pre-pass separates them, so to exercise the recording guard alone the
	// series claim is spelled with a name only the second row uses.
	const author = `{"name":"Solomon Kane"}`
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0SOLOMAN1", title: "Solomon Kane", authors: author, minutes: 300,
			series: `{"name":"Solomon Kane","position":"1"}`},
		libexRow{asin: "B0SOLOMAN2", title: "Solomon Kane", authors: author, minutes: 305,
			series: `{"name":"Solomon Kane","position":"2"}`},
	), false)

	if sum.MergedASINs != 0 {
		t.Fatalf("MergedASINs = %d, want 0", sum.MergedASINs)
	}
	if sum.NewRecordings != 2 {
		t.Errorf("NewRecordings = %d, want 2", sum.NewRecordings)
	}
}

// TestSeriesPositionGuardReportsTheRefusal pins that a blocked merge is
// VISIBLE. A guard that silently changes the shape of the output is how the
// original defect stayed hidden for four waves.
func TestSeriesPositionGuardReportsTheRefusal(t *testing.T) {
	ri := &recInfo{seriesPos: map[string]string{"bravelands": "1"}}
	b := sourceBook{series: []seriesRef{makeSeriesRef("Bravelands", "4")}}
	series, incumbent, want, conflict := seriesPosConflict(ri, b)
	if !conflict || series != "Bravelands" || incumbent != "1" || want != "4" {
		t.Fatalf("seriesPosConflict = %q %q %q %v", series, incumbent, want, conflict)
	}
	// A recording with no recorded claims - every recording loaded from disk -
	// never blocks: the guard fires on evidence, never on absence.
	if _, _, _, conflict := seriesPosConflict(&recInfo{}, b); conflict {
		t.Error("a recording with no recorded claim must never block a merge")
	}
}

// ---------------------------------------------------------------------------
// Credited-contributor identity exclusion

const (
	sapkowskiPerson = `{"id":"andrzej-sapkowski","license":"CC0-1.0","name":"Andrzej Sapkowski","sources":[{"type":"user"}]}`
	witcherReader   = `{"id":"ann-reader","license":"CC0-1.0","name":"Ann Reader","sources":[{"type":"user"}]}`
	translatorPers  = `{"id":"marie-traductrice","license":"CC0-1.0","name":"Marie Traductrice","sources":[{"type":"user"}]}`
	// The catalogued work as an earlier wave wrote it: the translator is in
	// authors[] AND carries the role credit that says what they actually did.
	witcherWork = `{"authors":["andrzej-sapkowski","marie-traductrice"],` +
		`"credits":[{"person":"marie-traductrice","role":"translator"}],` +
		`"id":"le-sang-des-elfes","language":"fr","license":"CC0-1.0",` +
		`"sources":[{"type":"user"}],"title":"Le Sang des Elfes"}`
	witcherRec = `{"asin":[{"asin":"B0WITCHER1","region":"fr"}],"id":"ann-reader-2019","language":"fr",` +
		`"license":"CC0-1.0","narrators":["ann-reader"],"runtime_min":500,` +
		`"sources":[{"type":"user"}],"work":"le-sang-des-elfes"}`
)

// TestTranslatorOnlyRowMatchesAWorkWithoutOne is the first direction: the
// catalogue holds the work under the author alone, and an edition arrives whose
// author column also lists the translator. It is the same book.
func TestTranslatorOnlyRowMatchesAWorkWithoutOne(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0WITCHFR1", title: "Le Sang des Elfes", language: "french",
			authors: `{"name":"Andrzej Sapkowski"}`},
		libexRow{asin: "B0WITCHFR2", title: "Le Sang des Elfes", language: "french",
			authors: `{"name":"Andrzej Sapkowski"},{"name":"Marie Traductrice - Traducteur"}`},
	), false)

	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1: the translator forked the work", sum.NewWorks)
	}
	if entryExists(t, dataDir, "works/le/le-sang-des-elfes-andrzej-sapkowski/work.json") {
		t.Error("the author-suffixed duplicate work was created")
	}
	// The translator is still an author and still credited: identity is the only
	// thing that changed.
	var work struct {
		Authors []string                        `json:"authors"`
		Credits []struct{ Person, Role string } `json:"credits"`
	}
	readEntity(t, dataDir, "works/le/le-sang-des-elfes/work.json", &work)
	if len(work.Authors) != 1 || work.Authors[0] != "andrzej-sapkowski" {
		t.Errorf("authors = %v", work.Authors)
	}
}

// TestRowWithoutTranslatorMatchesACataloguedWorkThatHasOne is the other
// direction, and the one only the disk-side rule can serve: the work on disk
// carries the translator in authors[] and the role in credits[], and a plain
// edition of the same book must still find it.
func TestRowWithoutTranslatorMatchesACataloguedWorkThatHasOne(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/an/andrzej-sapkowski.json":                           sapkowskiPerson,
		"people/an/ann-reader.json":                                  witcherReader,
		"people/ma/marie-traductrice.json":                           translatorPers,
		"works/le/le-sang-des-elfes/work.json":                       witcherWork,
		"works/le/le-sang-des-elfes/recordings/ann-reader-2019.json": witcherRec,
	})

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0WITCHFR3", title: "Le Sang des Elfes", language: "french",
			narrators: `{"name":"Bo Lecteur"}`, authors: `{"name":"Andrzej Sapkowski"}`},
	))

	if sum.NewWorks != 0 {
		t.Fatalf("NewWorks = %d, want 0: the row minted a duplicate of a catalogued work", sum.NewWorks)
	}
	if sum.NewRecordings != 1 {
		t.Errorf("NewRecordings = %d, want 1", sum.NewRecordings)
	}
}

// TestADifferentAuthorSetStillSplits is the guard on the rule: dropping the
// role-credited people from identity must not make two genuinely different
// books one.
func TestADifferentAuthorSetStillSplits(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0SPLITAU1", title: "The Tower", authors: `{"name":"Ada One"}`},
		libexRow{asin: "B0SPLITAU2", title: "The Tower", authors: `{"name":"Bea Two"}`},
	), false)
	if sum.NewWorks != 2 {
		t.Fatalf("NewWorks = %d, want 2", sum.NewWorks)
	}
}

// TestQualifierWithNoRoleDoesNotChangeIdentity pins the narrow definition: only
// a qualifier that actually STATES a role excludes anybody, because only such a
// qualifier reaches credits[], and a rule the disk side cannot apply would split
// works rather than join them.
func TestQualifierWithNoRoleDoesNotChangeIdentity(t *testing.T) {
	// "director" is in the strip vocabulary but maps to no schema role.
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0NOROLEQ1", title: "The Play", authors: `{"name":"Ada One"}`},
		libexRow{asin: "B0NOROLEQ2", title: "The Play",
			authors: `{"name":"Ada One"},{"name":"Bea Two - director"}`},
	), false)
	if sum.NewWorks != 2 {
		t.Fatalf("NewWorks = %d, want 2: a role-less qualifier must not change identity", sum.NewWorks)
	}
}

// TestIdentityFallsBackWhenEveryAuthorIsCredited covers the degenerate row: an
// edition whose author column is entirely translators must keep an identity of
// its own rather than matching every other such work.
func TestIdentityFallsBackWhenEveryAuthorIsCredited(t *testing.T) {
	wa := splitWorkAuthors(
		[]credit{{name: "Marie Traductrice", roles: []string{"translator"}}},
		func(name string) string { return Slugify(name) },
	)
	if len(wa.identity) != 1 || wa.identity[0] != "marie-traductrice" {
		t.Fatalf("identity = %v, want the full list as a fallback", wa.identity)
	}
}

// ---------------------------------------------------------------------------
// Series narrator qualifiers

func TestSeriesNarratorQualifierIsNormalizedAway(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Sherlock Holmes - Die galaktischen Fälle (gelesen von Peter Bocek)", "Sherlock Holmes - Die galaktischen Fälle"},
		{"Harry Potter (Narrated by Stephen Fry)", "Harry Potter"},
		{"Sherlock Holmes - Die übernatürlichen Fälle (gesprochen von Christoph Hackenberg)", "Sherlock Holmes - Die übernatürlichen Fälle"},
		{"Kalle Blomquist (Hörspiele von Kurt Vethake)", "Kalle Blomquist"},
		// Left alone: a marker that is not a narrator qualifier, a phrase the
		// vocabulary deliberately excludes, a qualifier naming nobody, and a
		// name that is nothing but its marker.
		{"Discworld (German Edition)", "Discworld (German Edition)"},
		{"Classic Stories Read by Celebrities", "Classic Stories Read by Celebrities"},
		{"Sherlock Holmes (gelesen von)", "Sherlock Holmes (gelesen von)"},
		{"(gelesen von Peter Bocek)", "(gelesen von Peter Bocek)"},
	}
	for _, c := range cases {
		if got := cleanSeriesName(c.in); got != c.want {
			t.Errorf("cleanSeriesName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOneBookIsOneSeriesAcrossNarrations is the defect the rule was measured
// for: libex names the series per narrator, so one book minted four works in
// four series.
func TestOneBookIsOneSeriesAcrossNarrations(t *testing.T) {
	const author = `{"name":"Arthur Conan Doyle"}`
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0SHERLOK1", title: "Die galaktischen Fälle", authors: author, minutes: 300,
			narrators: `{"name":"Peter Bocek"}`,
			series:    `{"name":"Sherlock Holmes (gelesen von Peter Bocek)","position":"1"}`},
		libexRow{asin: "B0SHERLOK2", title: "Die galaktischen Fälle", authors: author, minutes: 300,
			narrators: `{"name":"Andreas Lange"}`,
			series:    `{"name":"Sherlock Holmes (gelesen von Andreas Lange)","position":"1"}`},
	), false)

	if sum.NewSeries != 1 {
		t.Fatalf("NewSeries = %d, want 1: the series forked per narrator", sum.NewSeries)
	}
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1", sum.NewWorks)
	}
	if !entryExists(t, dataDir, "series/sh/sherlock-holmes.json") {
		t.Error("the normalized series slug was not used")
	}
}

// ---------------------------------------------------------------------------
// The list-credit refusal

func TestSemicolonJoinedCreditRefusesTheRow(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0LISTCRD1", title: "Perry Rhodan 3060",
			authors: `{"name":"Roman; Thomas; Michael G.; Dieter Schleifer; Frick; Rosenberg"}`},
	), false)

	if sum.NewWorks != 0 || sum.NewPeople != 0 {
		t.Fatalf("a semicolon-joined credit must mint nothing: %+v", sum)
	}
	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1", sum.SkippedRows)
	}
	if len(sum.Warnings) == 0 || !strings.Contains(sum.Warnings[0], "semicolon-joined list") {
		t.Errorf("the refusal must name itself: %v", sum.Warnings)
	}
}

// TestHTMLEscapedNameIsNotAList is the measured false positive the rule must
// not take: seven credit names in the dump contain ';' only because the name
// reached it HTML-escaped, and every one of them is a real person.
func TestHTMLEscapedNameIsNotAList(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0ENTITYC1", title: "A Book",
			authors: `{"name":"Erika B&aacute;lint"}`, narrators: `{"name":"Leo Kni&#382;ka"}`},
	), false)
	if sum.NewWorks != 1 {
		t.Fatalf("an HTML-escaped name must not be read as a list: %+v", sum)
	}
}

// ---------------------------------------------------------------------------
// Series placements lost with their row

// TestRefusedRowReportsTheSeriesPlacementItTakesDown is the silent path the
// review found: a row refused for its own reasons also takes a series
// membership down, and nothing said so. 388 rows did this in seed wave 5.
func TestRefusedRowReportsTheSeriesPlacementItTakesDown(t *testing.T) {
	sum, _ := runLibex(t, rows(
		// A language the map does not carry: the row goes, and so does its
		// claim on position 3 of a series nothing else fills.
		libexRow{asin: "B0LOSTSER1", title: "Ein Buch", language: "luo",
			authors: `{"name":"Ada One"}`, series: `{"name":"The Long Series","position":"3"}`},
	), false)

	var line string
	for _, w := range sum.Warnings {
		if strings.Contains(w, "series placements lost") {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("a refused row's series claim must be reported: %v", sum.Warnings)
	}
	if !strings.Contains(line, "1 series placements lost") || !strings.Contains(line, "The Long Series") {
		t.Errorf("warning = %q", line)
	}
}

// TestARowThatImportsReportsNoLostPlacement is the counterpart: the counter
// must not fire for rows that were placed.
func TestARowThatImportsReportsNoLostPlacement(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0KEPTSER1", title: "A Book", authors: `{"name":"Ada One"}`,
			series: `{"name":"The Long Series","position":"3"}`},
	), false)
	for _, w := range sum.Warnings {
		if strings.Contains(w, "series placements lost") {
			t.Errorf("a placed row reported a lost placement: %q", w)
		}
	}
}

// ---------------------------------------------------------------------------
// Placeholder credits

// TestPlaceholderCreditRefusesTheRow covers what is LEFT of this vocabulary
// after the collective forms flipped to normalization (collective.go): the
// booking placeholders, which name nobody at all. The flipped forms are pinned
// on the other side of the line in collective_test.go.
func TestPlaceholderCreditRefusesTheRow(t *testing.T) {
	for _, name := range []string{"To Be Announced", "To Be Confirmed", "TBA", "TBD", "TBC", "N/A"} {
		sum, _ := runLibex(t, rows(
			libexRow{asin: "B0PLACEHL1", title: "A Book",
				authors: `{"name":"Ada One"}`, narrators: `{"name":"` + name + `"}`},
		), false)
		if sum.NewWorks != 0 || sum.NewPeople != 0 {
			t.Errorf("credit %q must mint nothing: %+v", name, sum)
		}
	}
}

// TestPlaceholderCreditRefusesASuffixedForm pins the phrase tier: a booking
// placeholder does not always arrive alone. Every name here is a dump form
// (10 names / 16 credits / 15 books beyond the whole-name table), and each one
// evaded the exact compare that came before.
func TestPlaceholderCreditRefusesASuffixedForm(t *testing.T) {
	for _, name := range []string{
		"To Be Confirmed Audio",
		"To Be Confirmed Atria",
		"To Be Confirmed Gallery",
		"To Be Confirmed Avid Reader Press",
		"To Be Confirmed Simon & Schuster UK",
		"Author to be Announced",
		"Holt Author To Be Announced",
		"BFYR Author to Be Confirmed",
		"Tor May 2027 Author to Be Announced",
	} {
		sum, dataDir := runLibex(t, rows(
			libexRow{asin: "B0PLACESF1", title: "A Book",
				authors: `{"name":"Ada One"}`, narrators: `{"name":"` + name + `"}`},
		), false)
		if sum.NewWorks != 0 || sum.NewPeople != 0 {
			t.Errorf("credit %q must mint nothing: %+v", name, sum)
		}
		if entryExists(t, dataDir, personAddr("ada-one")) {
			t.Errorf("credit %q refused only its own name, not the whole row", name)
		}
	}
}

// TestPlaceholderCreditRefusesARoleQualifiedForm is the LAYER half of the fix,
// and the gap collective.go's header used to record as open: this table compares
// raw names at the parse layer, the credit-cleaning fixpoint runs later, so a
// role qualifier hid the placeholder from the compare. The refusal now takes a
// look at the cleaned name too - the shape firstAICredit already used.
//
// The abbreviation is what makes this observable: "To Be Announced - narrator"
// is caught on the RAW name by the phrase tier, but "TBD" is an exact-only entry,
// so only the cleaned look can reach it.
func TestPlaceholderCreditRefusesARoleQualifiedForm(t *testing.T) {
	for _, name := range []string{"TBD - narrator", "To Be Announced - narrator"} {
		sum, _ := runLibex(t, rows(
			libexRow{asin: "B0PLACERQ1", title: "A Book",
				authors: `{"name":"Ada One"}`, narrators: `{"name":"` + name + `"}`},
		), false)
		if sum.NewWorks != 0 || sum.NewPeople != 0 {
			t.Errorf("credit %q must mint nothing: %+v", name, sum)
		}
	}
}

// TestPlaceholderRuleLeavesTheAbbreviationsAlone pins the restraint the phrase
// tier is bounded by. The multi-word phrases generalize because no name contains
// them; the three-letter abbreviations do not, because a short opaque token can
// be part of one ("TBA Studios" is a real production company). The first three
// names are the dump forms that leaves importable - measured, small, and a
// one-line addition to the whole-name table if a maintainer ever wants them.
func TestPlaceholderRuleLeavesTheAbbreviationsAlone(t *testing.T) {
	for _, name := range []string{"Reader tbd 1", "Kulturplattformen TBA", "SW TBC", "TBA Studios"} {
		sum, _ := runLibex(t, rows(
			libexRow{asin: "B0PLACEAB1", title: "A Book",
				authors: `{"name":"Ada One"}`, narrators: `{"name":"` + name + `"}`},
		), false)
		if sum.NewWorks != 1 {
			t.Errorf("credit %q was refused by an abbreviation rule that does not exist: %+v", name, sum)
		}
	}
}

// TestPlaceholderPhraseLeavesRealNamesAlone is the phrase tier's negative
// control. The words the phrases are built from are ordinary ones, and a
// word-bounded match is what keeps them off names that merely contain them.
func TestPlaceholderPhraseLeavesRealNamesAlone(t *testing.T) {
	for _, name := range []string{
		"Tobe Announcer",   // "tobe" is not the word "to be"
		"Anne Confirmed",   // the phrase's last word alone is not the phrase
		"Ann To Bee",       // two of the three words, in order, and not the phrase
		"Announcer To Bea", // the phrase's words, none of them the phrase
	} {
		sum, _ := runLibex(t, rows(
			libexRow{asin: "B0PLACENC1", title: "A Book",
				authors: `{"name":"Ada One"}`, narrators: `{"name":"` + name + `"}`},
		), false)
		if sum.NewWorks != 1 {
			t.Errorf("real name %q was refused as a placeholder: %+v", name, sum)
		}
	}
}

// TestCollectiveCreditsSurvive is the boundary this vocabulary must not cross:
// a collective names a real if unnamed party, and the project keeps those - now
// including the language variants that used to be refused here. What each one
// lands AS is collective_test.go's job; this is only the refusal boundary.
func TestCollectiveCreditsSurvive(t *testing.T) {
	for _, name := range []string{
		"Full Cast", "Uncredited", "Anonymous", "Various", "Cast", "Unknown",
		"Various Narrators", "Narratori Vari", "Elenco", "Diverse Sprecher", "N.N.",
	} {
		sum, _ := runLibex(t, rows(
			libexRow{asin: "B0COLLECT1", title: "A Book",
				authors: `{"name":"Ada One"}`, narrators: `{"name":"` + name + `"}`},
		), false)
		if sum.NewWorks != 1 {
			t.Errorf("collective credit %q was refused: %+v", name, sum)
		}
	}
}

// ---------------------------------------------------------------------------
// Honorific merge

// TestHonorificMergesOntoTheBareName is the measured pair: the catalogue held
// both sir-arthur-conan-doyle and arthur-conan-doyle.
func TestHonorificMergesOntoTheBareName(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0HONORIF1", title: "A Study in Scarlet", authors: `{"name":"Arthur Conan Doyle"}`},
		libexRow{asin: "B0HONORIF2", title: "The Sign of Four", authors: `{"name":"Sir Arthur Conan Doyle"}`},
	), false)

	if sum.NewPeople != 2 { // the one author plus the shared narrator
		t.Fatalf("NewPeople = %d, want 2: the honorific forked the author", sum.NewPeople)
	}
	if entryExists(t, dataDir, "people/si/sir-arthur-conan-doyle.json") {
		t.Error("the honorific-prefixed person record was created")
	}
	if !entryExists(t, dataDir, "people/ar/arthur-conan-doyle.json") {
		t.Error("the bare person record is missing")
	}
}

// TestHonorificWithoutABareTwinSurvives is the rule's first guard, and the
// reason it consults the census at all.
func TestHonorificWithoutABareTwinSurvives(t *testing.T) {
	_, dataDir := runLibex(t, rows(
		libexRow{asin: "B0SEUSSXX1", title: "Green Eggs and Ham", authors: `{"name":"Dr. Seuss"}`},
		libexRow{asin: "B0SEUSSXX2", title: "The Lorax", authors: `{"name":"Mr. Lawrence Doe Roe"}`},
	), false)

	if !entryExists(t, dataDir, "people/dr/dr-seuss.json") {
		t.Error("Dr. Seuss lost his title: nothing in the batch is called Seuss")
	}
	if !entryExists(t, dataDir, "people/mr/mr-lawrence-doe-roe.json") {
		t.Error("a stage name with no bare twin must survive")
	}
}

// TestHonorificNeedsAFullNameBehindIt is the second, independent guard: the
// census answers "does this string exist", not "is it the same person", and a
// one-word remainder is where those come apart.
func TestHonorificNeedsAFullNameBehindIt(t *testing.T) {
	seen := func(name string) bool { return true } // the most permissive census there is
	if got := stripHonorific("Mr. Peter", seen); got != "Mr. Peter" {
		t.Errorf("stripHonorific(%q) = %q: a one-word remainder must never merge", "Mr. Peter", got)
	}
	if got := stripHonorific("Sir Arthur Conan Doyle", seen); got != "Arthur Conan Doyle" {
		t.Errorf("stripHonorific = %q", got)
	}
	// A fused token is not a prefixed name.
	if got := stripHonorific("Dr.Rebis Something", seen); got != "Dr.Rebis Something" {
		t.Errorf("stripHonorific(%q) = %q", "Dr.Rebis Something", got)
	}
	// And with no census in hand the rule never fires at all, which is what the
	// public CleanCreditName door must keep doing.
	if got := CleanCreditName("Sir Arthur Conan Doyle"); got != "Sir Arthur Conan Doyle" {
		t.Errorf("CleanCreditName = %q, want the name unchanged", got)
	}
}

// ---------------------------------------------------------------------------
// Role vocabulary additions

func TestEditeurAndCoverRoleQualifiers(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		roles []string
	}{
		{"Murielle Szac - Éditeur", "Murielle Szac", []string{"editor"}},
		{"Nick Kyme - Editeur", "Nick Kyme", []string{"editor"}},
		{"Henrik Koitzsch - cover design", "Henrik Koitzsch", nil},
		{"Grit Bomhauer - Cover Artwork", "Grit Bomhauer", nil},
		{"Jane Doe - cover illustration", "Jane Doe", []string{"illustrator"}},
	}
	for _, c := range cases {
		got, roles := CreditWithRoles(c.in)
		if got != c.want {
			t.Errorf("CreditWithRoles(%q) name = %q, want %q", c.in, got, c.want)
		}
		if len(roles) != len(c.roles) || (len(roles) > 0 && roles[0] != c.roles[0]) {
			t.Errorf("CreditWithRoles(%q) roles = %v, want %v", c.in, roles, c.roles)
		}
	}
}

// ---------------------------------------------------------------------------
// Language map

func TestDanishAndSwedishRowsImport(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0DANISHR1", title: "En Bog", language: "danish", authors: `{"name":"Dan Forfatter"}`},
		libexRow{asin: "B0SWEDISH1", title: "En Bok", language: "swedish", authors: `{"name":"Sven Forfattare"}`},
	), false)
	if sum.NewWorks != 2 {
		t.Fatalf("NewWorks = %d, want 2: %v", sum.NewWorks, sum.Warnings)
	}
}

func TestLanguageMapCodes(t *testing.T) {
	for word, want := range map[string]string{
		"danish": "da", "swedish": "sv", "norwegian": "no", "finnish": "fi",
		"czech": "cs", "hungarian": "hu", "greek": "el", "hebrew": "he",
		"arabic": "ar", "hindi": "hi",
	} {
		got, ok := mapLanguage(word)
		if !ok || got != want {
			t.Errorf("mapLanguage(%q) = %q %v, want %q", word, got, ok, want)
		}
	}
	// Still unknown, and deliberately so: "luo" has no ISO 639-1 code at all.
	if _, ok := mapLanguage("luo"); ok {
		t.Error("luo has no ISO 639-1 code and must stay unmapped")
	}
}
