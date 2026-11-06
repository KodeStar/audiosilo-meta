package query

import (
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The factors and the narrator weight a works/match request is scaled by
// beyond the weighted average (positionConflictFactor, languageMismatchFactor,
// runtimeMismatchFactor, weightNarrator): each case pins what it changes and,
// beside it, that a request without the fact scores exactly as before.

// factorPerson and factorWork build the small catalogues below.
func factorPerson(id, name string) *model.Person {
	return &model.Person{ID: id, Name: name, License: "CC0-1.0"}
}

func factorWork(id, title, language, author string, recs ...*model.Recording) *model.Work {
	w := &model.Work{ID: id, Title: title, Language: language, Authors: []string{author}, License: "CC0-1.0"}
	for _, r := range recs {
		r.Work, r.Language, r.License = id, language, "CC0-1.0"
		w.Recordings = append(w.Recordings, r)
	}
	return w
}

func factorRec(id string, minutes int, narrators ...string) *model.Recording {
	return &model.Recording{ID: id, RuntimeMin: minutes, Narrators: narrators}
}

// wantScore fails unless id is among res with score want.
func wantScore(t *testing.T, res []map[string]any, id string, want float64) map[string]any {
	t.Helper()
	for _, r := range res {
		if r["id"] == id {
			if r["score"].(float64) != want {
				t.Errorf("%s scores %v, want %v (reasons %v)", id, r["score"], want, r["reasons"])
			}
			return r
		}
	}
	t.Fatalf("%s not among the results %v", id, matchIDs(res))
	return nil
}

// TestMatchPositionConflictScalesASibling: a request for volume 1 whose title
// agrees with volume 4's once that title's numbering is cut ("I Became the
// Strongest, Vol. 4"). Before positionConflictFactor volume 4 scored 89 (title
// 1, author 1, series conflict 0), close behind volume 1's 100; scaled it is 71.
// A volume listed AT the stated position keeps its score, and an omnibus whose
// range covers the stated volume is no sibling at another position.
func TestMatchPositionConflictScalesASibling(t *testing.T) {
	_, ts := newTestServerForCatalog(t, &model.Catalog{
		People: []*model.Person{factorPerson("kaoru-shinozaki", "Kaoru Shinozaki")},
		Works: []*model.Work{
			factorWork("strongest-1", "I Became the Strongest", "en", "kaoru-shinozaki"),
			factorWork("strongest-4", "I Became the Strongest, Vol. 4", "en", "kaoru-shinozaki"),
			factorWork("collection-1-3", "The Failure Frame Collection", "en", "kaoru-shinozaki"),
			factorWork("collection-4-6", "The Failure Frame Collection 4-6", "en", "kaoru-shinozaki"),
		},
		Series: []*model.Series{{ID: "failure-frame", Name: "Failure Frame", License: "CC0-1.0", Works: []model.SeriesWork{
			{Work: "strongest-1", Position: "1"}, {Work: "collection-1-3", Position: "1-3"},
			{Work: "strongest-4", Position: "4"}, {Work: "collection-4-6", Position: "4-6"},
		}}},
	})
	base := url.Values{"title": {"I Became the Strongest"}, "author": {"Kaoru Shinozaki"}, "series": {"Failure Frame"}}

	stated := url.Values{"position": {"1"}}
	maps.Copy(stated, base)
	res := matchJSON(t, ts.URL, stated)
	if len(res) == 0 || res[0]["id"] != "strongest-1" {
		t.Fatalf("top = %v, want strongest-1", matchIDs(res))
	}
	wantScore(t, res, "strongest-1", 100)
	sibling := wantScore(t, res, "strongest-4", 71)
	if s := sibling["reasons"].(map[string]any)["series"]; s != string(seriesConflict) {
		t.Errorf("strongest-4 series reason %v, want conflict", s)
	}

	// No position stated: no position can conflict, so both titles agree in
	// full and the series by name - exactly the old scoring.
	res = matchJSON(t, ts.URL, base)
	wantScore(t, res, "strongest-1", 100)
	wantScore(t, res, "strongest-4", 100)

	// The omnibus listed at 1-3 covers a stated 2: the series reason is still
	// a conflict (it is not volume 2), but it is not scaled - 89, as before.
	// The omnibus at 4-6 does not cover 2 and is: 81 (its title contained,
	// 0.88) before the factor, 65 now.
	omnibus := url.Values{
		"title": {"The Failure Frame Collection"}, "author": {"Kaoru Shinozaki"},
		"series": {"Failure Frame"}, "position": {"2"},
	}
	res = matchJSON(t, ts.URL, omnibus)
	covered := wantScore(t, res, "collection-1-3", 89)
	if s := covered["reasons"].(map[string]any)["series"]; s != string(seriesConflict) {
		t.Errorf("collection-1-3 series reason %v, want conflict", s)
	}
	wantScore(t, res, "collection-4-6", 65)
}

func TestPositionCovers(t *testing.T) {
	for _, tc := range []struct {
		stored, stated string
		want           bool
	}{
		{"1-3", "2", true},
		{"1-3", "1", true},
		{"1-3", "3", true},
		{"1-3", "2-3", true},
		{"1-3", "4", false},
		{"1-3", "0.5", false},
		{"1-3", "1-4", false},
		{"2", "2", false}, // a point covers nothing; positionsEqual decides it
		{"4", "1", false},
		{"garbage", "1", false},
		{"1-3", "garbage", false},
	} {
		if got := positionCovers(tc.stored, tc.stated); got != tc.want {
			t.Errorf("positionCovers(%q, %q) = %v, want %v", tc.stored, tc.stated, got, tc.want)
		}
	}
}

// TestMatchLanguageScalesAForeignEdition: the German edition, stored as its own
// work under the same title, ties the English one without a language. With
// `language` stated (in any of a tag's spellings) the edition in another KNOWN
// language is scaled by languageMismatchFactor, and a language matchLanguage
// cannot read judges nothing.
func TestMatchLanguageScalesAForeignEdition(t *testing.T) {
	_, ts := newTestServerForCatalog(t, &model.Catalog{
		People: []*model.Person{factorPerson("frank-herbert", "Frank Herbert")},
		Works: []*model.Work{
			factorWork("dune", "Dune", "en", "frank-herbert"),
			factorWork("dune-de", "Dune", "de", "frank-herbert"),
		},
	})
	query := func(language string) []map[string]any {
		q := url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}}
		if language != "" {
			q.Set("language", language)
		}
		return matchJSON(t, ts.URL, q)
	}

	res := query("")
	wantScore(t, res, "dune", 100)
	wantScore(t, res, "dune-de", 100)
	if _, ok := res[0]["reasons"].(map[string]any)["language"]; ok {
		t.Errorf("language reason without a stated language: %v", res[0]["reasons"])
	}

	for _, lang := range []string{"en", "en-US", "en_GB", "eng", "English", " ENGLISH "} {
		res := query(lang)
		if len(res) == 0 || res[0]["id"] != "dune" {
			t.Fatalf("language %q: top = %v, want dune", lang, matchIDs(res))
		}
		same := wantScore(t, res, "dune", 100)
		other := wantScore(t, res, "dune-de", 80)
		if same["reasons"].(map[string]any)["language"] != "same" || other["reasons"].(map[string]any)["language"] != "other" {
			t.Errorf("language %q: reasons %v / %v, want same / other", lang, same["reasons"], other["reasons"])
		}
	}
	// German asked: the English work is the foreign one now.
	res = query("Deutsch")
	if len(res) == 0 || res[0]["id"] != "dune-de" {
		t.Fatalf("language Deutsch: top = %v, want dune-de", matchIDs(res))
	}
	wantScore(t, res, "dune", 80)

	// Unknown on the request's side: nothing is judged.
	for _, lang := range []string{"und", "mul", "Klingon", "en,de", "12"} {
		res := query(lang)
		wantScore(t, res, "dune", 100)
		wantScore(t, res, "dune-de", 100)
	}
}

// TestMatchLanguageUnknownCandidateIsNotJudged: a work whose language reads as
// unknown ("und") is neither scaled nor given a language reason.
func TestMatchLanguageUnknownCandidateIsNotJudged(t *testing.T) {
	facts := readFacts(matchRequest{titles: []string{"Dune"}, language: "en"})
	c := &candidate{id: "dune-und", title: matchTerms("Dune"), titles: candidateTitleForms("Dune", matchTerms("Dune"), ""), language: matchLanguage("und")}
	v := facts.judge(c)
	if v.score != 1 || v.reasons.Language != "" {
		t.Errorf("unknown-language candidate: score %v, language reason %q; want 1 and none", v.score, v.reasons.Language)
	}
	c.language = "de"
	if v := facts.judge(c); v.score != languageMismatchFactor || v.reasons.Language != "other" {
		t.Errorf("German candidate: score %v, language reason %q; want %v and other", v.score, v.reasons.Language, languageMismatchFactor)
	}
}

func TestMatchLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "EN": "en", "en-US": "en", "en_gb": "en", "eng": "en", "English": "en",
		"de": "de", "ger": "de", "deu": "de", "German": "de", "Deutsch": "de",
		"fr": "fr", "fra": "fr", "Français": "fr", "es-419": "es", "Español": "es",
		"ja": "ja", "jpn": "ja", "nb": "no", "nn": "no", "no": "no", "Norwegian": "no",
		"pt-br": "pt", "sv": "sv",
		// LibriVox's name table, and its 639-2-only tags kept as stored.
		"Catalan": "ca", "Ancient Greek": "grc", "grc": "grc", "ang": "ang", "Multilingual": "",
		// A tag no table maps onto is unknown, never a language of its own
		// ("kor" is not judged against "ko", nor "xx" against anything).
		"kor": "", "ko": "ko", "xx": "", "NA": "", "zz-ZZ": "",
		"": "", "und": "", "mul": "", "zxx": "", "Klingon": "", "en,de": "", "12": "", "English; German": "",
	} {
		if got := matchLanguage(in); got != want {
			t.Errorf("matchLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMatchRuntimeFarOffScalesTheScore: a candidate whose only recording is
// more than runtimeFar off the book is another edition, scaled by
// runtimeMismatchFactor (89 before, 76 now: title, author, runtime 0); one 17%
// or 23% off - past the 10% gate, but within runtimeFar - is unchanged at 89,
// and a request stating no runtime at 100.
func TestMatchRuntimeFarOffScalesTheScore(t *testing.T) {
	_, ts := newTestServerForCatalog(t, &model.Catalog{
		People: []*model.Person{factorPerson("frank-herbert", "Frank Herbert")},
		Works:  []*model.Work{factorWork("dune", "Dune", "en", "frank-herbert", factorRec("dune-rec", 1000))},
	})
	for _, tc := range []struct {
		runtime string
		want    float64
	}{
		// The difference is relative to the book's length, as runtimeFit reads it.
		{"90000", 76},  // a 1,500-minute book: 33% off
		{"84000", 76},  // 1,400 minutes: 29% off
		{"78000", 89},  // 1,300 minutes: 23% off
		{"72000", 89},  // 1,200 minutes: 17% off
		{"60000", 100}, // the recording's own length
	} {
		res := matchJSON(t, ts.URL, url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}, "runtime": {tc.runtime}})
		wantScore(t, res, "dune", tc.want)
	}
	res := matchJSON(t, ts.URL, url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}})
	wantScore(t, res, "dune", 100)
}

// TestMatchRuntimeFarOffAllowsTheStoredMinute: runtime_min is whole minutes, so
// a short book whose recording is stored a rounded minute off is not another
// edition by rounding alone (84 s against a stored 1 minute reads 29% off).
func TestMatchRuntimeFarOffAllowsTheStoredMinute(t *testing.T) {
	short := &matchFacts{runtime: 84}
	_, diff := runtimeFit(1, 84)
	if diff <= runtimeFar || short.runtimeFarOff(diff) {
		t.Errorf("84 s against a stored minute: diff %v, far off %v; want past runtimeFar but not far off", diff, short.runtimeFarOff(diff))
	}
	long := &matchFacts{runtime: 84000}
	if _, diff := runtimeFit(1000, 84000); !long.runtimeFarOff(diff) {
		t.Errorf("84,000 s against 1,000 minutes (diff %v): not far off, want far off", diff)
	}
	if long.runtimeFarOff(math.Inf(1)) || (&matchFacts{}).runtimeFarOff(1) {
		t.Error("an unjudged difference, or no runtime stated, reads as far off")
	}
}

// TestMatchNarratorFarOffKeepsTheRuntimesRecording: the only recording crediting
// the named narrator is more than runtimeFar off the book, so it is another
// edition: the narrator still agrees for the work, but the recording is the
// runtime's.
func TestMatchNarratorFarOffKeepsTheRuntimesRecording(t *testing.T) {
	facts := readFacts(matchRequest{titles: []string{"Dune"}, runtime: 75600, narrators: []string{"Simon Vance"}})
	brick, _ := parseName("Scott Brick")
	vance, _ := parseName("Simon Vance")
	c := &candidate{
		id: "dune", title: matchTerms("Dune"), titles: candidateTitleForms("Dune", matchTerms("Dune"), ""),
		runtimes: []recordingRuntime{{id: "a-brick", minute: 1260}, {id: "b-vance-abridged", minute: 600}},
		narrated: []recordingNarrators{{id: "a-brick", names: []personName{brick}}, {id: "b-vance-abridged", names: []personName{vance}}},
	}
	v := facts.judge(c)
	if v.recordingID != "a-brick" || v.reasons.Narrator != authorFull {
		t.Errorf("recording %q, narrator %q; want a-brick and full", v.recordingID, v.reasons.Narrator)
	}
	// Within runtimeFar the narrator's recording wins, as before.
	c.runtimes[1].minute = 1300
	if v := facts.judge(c); v.recordingID != "b-vance-abridged" {
		t.Errorf("recording %q within runtimeFar; want the narrator's b-vance-abridged", v.recordingID)
	}
}

// narratorCatalog is one work in three recordings: Scott Brick's at the book's
// length, two of Simon Vance's at other lengths, and one crediting no narrator.
func narratorCatalog() *model.Catalog {
	return &model.Catalog{
		People: []*model.Person{
			factorPerson("frank-herbert", "Frank Herbert"),
			factorPerson("scott-brick", "Scott Brick"), factorPerson("simon-vance", "Simon Vance"),
		},
		Works: []*model.Work{
			factorWork("dune", "Dune", "en", "frank-herbert",
				factorRec("a-brick", 1260, "scott-brick"),
				factorRec("b-vance", 1300, "simon-vance"),
				factorRec("c-vance", 900, "simon-vance"),
				factorRec("d-nobody", 1262)),
			factorWork("unnarrated", "Unnarrated", "en", "frank-herbert", factorRec("unnarrated-rec", 600)),
		},
	}
}

// TestMatchNarratorPicksTheRecording: a named narrator picks the recording that
// credits them over the one whose runtime is closest, the runtime breaking a
// tie between two of theirs, and agreement adds weightNarrator: in full here
// (100), against nothing for a narrator no recording credits (90).
func TestMatchNarratorPicksTheRecording(t *testing.T) {
	_, ts := newTestServerForCatalog(t, narratorCatalog())
	for _, tc := range []struct {
		name      string
		params    url.Values
		recording string
		score     float64
		narrator  any
	}{
		{"no narrator: the closest runtime, as before", url.Values{"runtime": {"75600"}}, "a-brick", 100, nil},
		{"a narrator over the closest runtime", url.Values{"runtime": {"75600"}, "narrator": {"Simon Vance"}}, "b-vance", 100, "full"},
		{"the runtime between two of theirs", url.Values{"runtime": {"54000"}, "narrator": {"Simon Vance"}}, "c-vance", 100, "full"},
		{"a narrator with no runtime", url.Values{"narrator": {"Vance, Simon"}}, "b-vance", 100, "full"},
		{"a credit naming two", url.Values{"narrator": {"Someone Else & Scott Brick"}}, "a-brick", 100, "full"},
		{"the surname alone", url.Values{"narrator": {"S. Brick"}}, "a-brick", 100, "full"},
		{"another narrator's first name", url.Values{"narrator": {"Sarah Brick"}}, "a-brick", math.Round(100 * (60 + 20 + 10*authorSurname.value()) / 90), "surname"},
		{"no recording credits them", url.Values{"runtime": {"75600"}, "narrator": {"Nobody Known"}}, "a-brick", 90, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}}
			maps.Copy(q, tc.params)
			res := matchJSON(t, ts.URL, q)
			top := wantScore(t, res, "dune", tc.score)
			if got, _ := top["recording_id"].(string); got != tc.recording {
				t.Errorf("recording_id = %q, want %q", got, tc.recording)
			}
			if got := top["reasons"].(map[string]any)["narrator"]; got != tc.narrator {
				t.Errorf("narrator reason = %v, want %v", got, tc.narrator)
			}
		})
	}
}

// TestMatchNarratorAbsentHasNoEffect: a narrator named for a work whose
// recordings credit none is not judged - no weight, no reason, the runtime's
// recording.
func TestMatchNarratorAbsentHasNoEffect(t *testing.T) {
	_, ts := newTestServerForCatalog(t, narratorCatalog())
	for _, q := range []url.Values{
		{"title": {"Unnarrated"}, "author": {"Frank Herbert"}, "runtime": {"36000"}},
		{"title": {"Unnarrated"}, "author": {"Frank Herbert"}, "runtime": {"36000"}, "narrator": {"Simon Vance"}},
	} {
		res := matchJSON(t, ts.URL, q)
		top := wantScore(t, res, "unnarrated", 100)
		if top["recording_id"] != "unnarrated-rec" {
			t.Errorf("%v: recording_id %v, want unnarrated-rec", q, top["recording_id"])
		}
		if _, ok := top["reasons"].(map[string]any)["narrator"]; ok {
			t.Errorf("%v: a narrator reason for a work no recording of which credits one: %v", q, top["reasons"])
		}
	}
}

// TestParseMatchRequestLanguageAndNarrators: language is read through
// matchLanguage (one value), narrator is repeatable and bounded like author.
func TestParseMatchRequestLanguageAndNarrators(t *testing.T) {
	r, ok := parseMatchRequest(url.Values{
		"title":    {"Dune"},
		"language": {"English"},
		"narrator": {"A", "A", "B", "C", "D", "E"},
	})
	if !ok || r.language != "en" {
		t.Fatalf("ok %v, language %q; want true and en", ok, r.language)
	}
	if len(r.narrators) != matchHypothesesMax || r.narrators[0] != "A" || r.narrators[1] != "B" {
		t.Errorf("narrators = %q, want the first %d distinct", r.narrators, matchHypothesesMax)
	}
	if r, _ := parseMatchRequest(url.Values{"title": {"Dune"}}); r.language != "" || r.narrators != nil {
		t.Errorf("absent language/narrator read as %q / %q", r.language, r.narrators)
	}
}

// A plain book matches a work that holds a dramatization, because that is the same
// book, but the dramatization is never named as the book's recording by length.
func TestMatchRuntimeNeverNamesADramatizationForAPlainBook(t *testing.T) {
	plain := recordingRuntime{id: "a-plain", minute: 700}
	drama := recordingRuntime{id: "b-drama", minute: 400, dramatized: true}
	newCand := func(rs ...recordingRuntime) *candidate {
		return &candidate{id: "dune", title: matchTerms("Dune"), titles: candidateTitleForms("Dune", matchTerms("Dune"), ""), runtimes: rs}
	}
	cases := []struct {
		name string
		req  matchRequest
		recs []recordingRuntime
		want string
	}{
		{"denied: nearer by length, but a plain book", matchRequest{titles: []string{"Dune"}, runtime: 24000}, []recordingRuntime{plain, drama}, "a-plain"},
		{"denied: the only recording", matchRequest{titles: []string{"Dune"}, runtime: 24000}, []recordingRuntime{drama}, ""},
		{"allowed: the title says so", matchRequest{titles: []string{"Dune [Dramatized Adaptation]"}, runtime: 24000}, []recordingRuntime{plain, drama}, "b-drama"},
		{"allowed: a folder's series says so", matchRequest{titles: []string{"Dune"}, series: []string{"Dune (Full Cast)"}, runtime: 24000}, []recordingRuntime{plain, drama}, "b-drama"},
		{"allowed: the narrator credit says so", matchRequest{titles: []string{"Dune"}, narrators: []string{"Full Cast"}, runtime: 24000}, []recordingRuntime{plain, drama}, "b-drama"},
		{"allowed: a folder names the imprint", matchRequest{titles: []string{"Mistborn (GraphicAudio)"}, runtime: 24000}, []recordingRuntime{plain, drama}, "b-drama"},
		{"a plain recording is picked as ever", matchRequest{titles: []string{"Dune"}, runtime: 42000}, []recordingRuntime{plain, drama}, "a-plain"},
	}
	for _, c := range cases {
		v := readFacts(c.req).judge(newCand(c.recs...))
		if v.recordingID != c.want {
			t.Errorf("%s: recording %q, want %q", c.name, v.recordingID, c.want)
		}
		if v.score == 0 {
			t.Errorf("%s: the work no longer matches", c.name)
		}
	}
	// The dramatization still speaks for the work: the fit is the closest recording's.
	v := readFacts(matchRequest{titles: []string{"Dune"}, runtime: 24000}).judge(newCand(plain, drama))
	if v.reasons.Runtime == nil || *v.reasons.Runtime != 0 {
		t.Errorf("runtime reason %v, want the dramatization's exact fit", v.reasons.Runtime)
	}
}

// A narrator credit naming the production's own cast still names its recording: the
// request has said which edition it is. An identifier does as well.
func TestMatchNarratorOrIdentifierStillNamesADramatization(t *testing.T) {
	hill, _ := parseName("Alex Hill Knight")
	c := &candidate{
		id: "dune", title: matchTerms("Dune"), titles: candidateTitleForms("Dune", matchTerms("Dune"), ""),
		runtimes: []recordingRuntime{{id: "a-plain", minute: 700}, {id: "b-drama", minute: 400, dramatized: true}},
		narrated: []recordingNarrators{{id: "b-drama", names: []personName{hill}}},
	}
	v := readFacts(matchRequest{titles: []string{"Dune"}, runtime: 24000, narrators: []string{"Alex Hill Knight"}}).judge(c)
	if v.recordingID != "b-drama" {
		t.Errorf("narrator: recording %q, want b-drama", v.recordingID)
	}
	// A narrator credited on both has not said which it is, and length names no
	// dramatization, however close.
	c.narrated = []recordingNarrators{{id: "a-plain", names: []personName{hill}}, {id: "b-drama", names: []personName{hill}}}
	v = readFacts(matchRequest{titles: []string{"Dune"}, runtime: 24000, narrators: []string{"Alex Hill Knight"}}).judge(c)
	if v.recordingID != "a-plain" {
		t.Errorf("narrator on both: recording %q, want a-plain", v.recordingID)
	}
	v = readFacts(matchRequest{titles: []string{"Dune [Dramatized Adaptation]"}, runtime: 24000, narrators: []string{"Alex Hill Knight"}}).judge(c)
	if v.recordingID != "b-drama" {
		t.Errorf("narrator on both, the request states it: recording %q, want b-drama", v.recordingID)
	}
	c.narrated = nil
	c.identified = identification{recording: "b-drama", by: "asin"}
	v = readFacts(matchRequest{asin: "B0DUNE0001", runtime: 42000}).judge(c)
	if v.recordingID != "b-drama" {
		t.Errorf("identifier: recording %q, want b-drama", v.recordingID)
	}
}

// Through the database: the recording's publisher or full-cast credit says it, and the
// work's label is the fallback for a recording stating no publisher.
func TestMatchReadsADramatizationFromTheRecording(t *testing.T) {
	rec := func(id string, minutes int, publisher string, narrators ...string) *model.Recording {
		r := factorRec(id, minutes, narrators...)
		r.Publisher = publisher
		return r
	}
	_, ts := newTestServerForCatalog(t, &model.Catalog{
		People: []*model.Person{
			factorPerson("ada-one", "Ada One"), factorPerson(model.FullCastPerson, "Full Cast"),
			factorPerson("the-full-cast-family", "the Full Cast Family"), factorPerson("bea-reader", "Bea Reader"),
		},
		Works: []*model.Work{
			factorWork("both", "Both", "en", "ada-one", rec("a-reader", 700, "Tantor Audio", "bea-reader"), rec("b-ga", 400, "Graphic Audio LLC", "bea-reader")),
			factorWork("cast", "Cast", "en", "ada-one", rec("a-reader", 700, "Tantor Audio", "bea-reader"), rec("b-cast", 400, "Corey Press", model.FullCastPerson)),
			factorWork("family", "Family", "en", "ada-one", rec("a-reader", 700, "Tantor Audio", "bea-reader"), rec("b-cast", 400, "Full Cast Audio", "the-full-cast-family")),
			factorWork("only-drama", "Only Drama", "en", "ada-one", rec("b-ga", 400, "GraphicAudio", "bea-reader")),
			factorWork("stays", "Stays (Dramatized)", "en", "ada-one", rec("f-latw", 400, "L.A. Theatre Works", "bea-reader")),
			factorWork("label-unknown", "Label Unknown [Dramatized Adaptation]", "en", "ada-one", rec("d-none", 400, "", "bea-reader")),
			factorWork("label-plain", "Label Plain [Dramatized Adaptation]", "en", "ada-one", rec("e-plain", 400, "Tantor Audio", "bea-reader"), rec("f-ga", 700, "GraphicAudio", "bea-reader")),
			factorWork("label-alone", "Label Alone [Dramatized Adaptation]", "en", "ada-one", rec("g-only", 400, "Simon & Schuster Audio", "bea-reader")),
			// A GraphicAudio recording with no runtime still says the work holds one, so
			// the label doesn't cover the plain reading beside it.
			factorWork("label-untimed", "Label Untimed [Dramatized Adaptation]", "en", "ada-one", rec("h-plain", 400, "Tantor Audio", "bea-reader"), rec("i-ga", 0, "GraphicAudio", "bea-reader")),
		},
	})
	for _, x := range []struct{ title, id, want string }{
		{"Both", "both", "a-reader"},
		{"Cast", "cast", "a-reader"},
		{"Family", "family", "a-reader"},
		{"Only Drama", "only-drama", ""},
		{"Stays", "stays", ""},
		{"Label Unknown", "label-unknown", ""},
		{"Label Plain", "label-plain", "e-plain"},
		{"Label Alone", "label-alone", ""},
		{"Label Untimed", "label-untimed", "h-plain"},
	} {
		res := matchJSON(t, ts.URL, url.Values{"title": {x.title}, "author": {"Ada One"}, "runtime": {"24000"}})
		for _, r := range res {
			if r["id"] == x.id {
				if got, _ := r["recording_id"].(string); got != x.want {
					t.Errorf("%s: recording_id %q, want %q", x.title, got, x.want)
				}
			}
		}
		if !slices.Contains(matchIDs(res), any(x.id)) {
			t.Errorf("%s: the work no longer matches: %v", x.title, matchIDs(res))
		}
	}
}

// TestMatchTitleNumberIsNotAStatedPosition: a series whose volumes are titled
// with their own numbers ("Dungeon Diving 101" is volume 1, "Dungeon Diving
// 102" volume 2). With a position stated, a title that is the series' name and
// a number DISAGREEING with it is a title, not volume 102: each volume is found
// by its title, which is judged. A number that agrees, or no position at all,
// is still numbering ("The Primal Hunter 1", "He Who Fights with Monsters 4").
func TestMatchTitleNumberIsNotAStatedPosition(t *testing.T) {
	titles := []string{"101", "102", "103", "201", "202", "203", "301"}
	catalog := &model.Catalog{People: []*model.Person{factorPerson("mike-dungeon", "Mike Dungeon")}}
	dd := &model.Series{ID: "dungeon-diving", Name: "Dungeon Diving", License: "CC0-1.0"}
	for i, n := range titles {
		id := "dungeon-diving-" + n
		catalog.Works = append(catalog.Works, factorWork(id, "Dungeon Diving "+n, "en", "mike-dungeon"))
		dd.Works = append(dd.Works, model.SeriesWork{Work: id, Position: strconv.Itoa(i + 1)})
	}
	catalog.Series = []*model.Series{dd}
	_, ts := newTestServerForCatalog(t, catalog)
	for i, n := range titles {
		position := strconv.Itoa(i + 1)
		facts := readFacts(matchRequest{titles: []string{"Dungeon Diving " + n}, series: []string{"Dungeon Diving"}, position: position})
		if len(facts.titles) != 1 || facts.position != position || facts.tiedVolume {
			t.Errorf("Dungeon Diving %s at %s: titles %d, position %q, tied %v; want the title kept at %s", n, position, len(facts.titles), facts.position, facts.tiedVolume, position)
		}
		res := matchJSON(t, ts.URL, url.Values{
			"title": {"Dungeon Diving " + n}, "author": {"Mike Dungeon"},
			"series": {"Dungeon Diving"}, "position": {position},
		})
		if len(res) == 0 || res[0]["id"] != "dungeon-diving-"+n {
			t.Fatalf("Dungeon Diving %s at %s: top = %v", n, position, matchIDs(res))
		}
		top := wantScore(t, res, "dungeon-diving-"+n, 100)
		if title := top["reasons"].(map[string]any)["title"]; title != 1.0 {
			t.Errorf("Dungeon Diving %s: title reason %v, want 1 (judged as a title)", n, title)
		}
	}

	for _, tc := range []struct {
		name, title, series, position, wantPosition string
	}{
		{"the number agrees", "The Primal Hunter 1", "The Primal Hunter", "1", "1"},
		{"the number agrees, tied to the name", "He Who Fights with Monsters 4", "He Who Fights with Monsters", "4", "4"},
		{"no position stated", "He Who Fights with Monsters 4", "He Who Fights with Monsters", "", "4"},
	} {
		facts := readFacts(matchRequest{titles: []string{tc.title}, series: []string{tc.series}, position: tc.position})
		if len(facts.titles) != 0 || facts.position != tc.wantPosition || !facts.tiedVolume {
			t.Errorf("%s: titles %d, position %q, tied %v; want numbering at %s", tc.name, len(facts.titles), facts.position, facts.tiedVolume, tc.wantPosition)
		}
	}
}

// listedCatalog holds the shapes the listed-at-the-stated-position rule
// (matchFacts.listedHere) was measured on: series-named first volumes with a
// subtitle, a volume filed under its series' name and number, and the five
// rivals that rose in its first prototype - a sub-series fitting the named
// one fuzzily (Frontlines: Evolution), a shorter series contained in the named
// one (Summoner beside Summoner School), a translated series of the same name
// (the Polish Star Force) and a spin-off listed at the same number (System
// Apocalypse: Kismet). Every recording but the Carl edition at 6.5% off is
// within 1% of the book its request states.
func listedCatalog() *model.Catalog {
	series := func(id, name string, works ...model.SeriesWork) *model.Series {
		return &model.Series{ID: id, Name: name, License: "CC0-1.0", Works: works}
	}
	at := func(work, position string) model.SeriesWork { return model.SeriesWork{Work: work, Position: position} }
	return &model.Catalog{
		People: []*model.Person{
			factorPerson("matt-dinniman", "Matt Dinniman"), factorPerson("aaron-crash", "Aaron Crash"),
			factorPerson("marko-kloos", "Marko Kloos"), factorPerson("eric-vall", "Eric Vall"),
			factorPerson("b-v-larson", "B. V. Larson"), factorPerson("tao-wong", "Tao Wong"),
		},
		Works: []*model.Work{
			factorWork("dungeon-crawler-carl", "Dungeon Crawler Carl", "en", "matt-dinniman", factorRec("dcc", 811)),
			factorWork("dungeon-crawler-carl-de", "Dungeon Crawler Carl (German Edition)", "de", "matt-dinniman", factorRec("dcc-de", 864)),
			factorWork("denver-fury", "Denver Fury: An Urban Fantasy Harem Adventure", "en", "aaron-crash", factorRec("denver", 478)),
			factorWork("terms-of-enlistment", "Terms of Enlistment", "en", "marko-kloos", factorRec("terms", 576)),
			factorWork("lines-of-departure", "Lines of Departure", "en", "marko-kloos", factorRec("lines", 576)),
			factorWork("scorpio", "Scorpio", "en", "marko-kloos", factorRec("scorpio", 579)),
			factorWork("summoner-school", "Summoner School", "en", "eric-vall", factorRec("school", 452)),
			factorWork("summoner", "Summoner", "en", "eric-vall", factorRec("summoner", 455)),
			factorWork("conquest", "Conquest: Star Force, Book 4", "en", "b-v-larson", factorRec("conquest", 691)),
			factorWork("podboj", "Podbój", "pl", "b-v-larson", factorRec("podboj", 691)),
			factorWork("the-cost-of-survival", "The Cost of Survival", "en", "tao-wong", factorRec("cost", 516)),
			factorWork("fools-last-dance", "Fool's Last Dance", "en", "tao-wong", factorRec("fools", 519)),
		},
		Series: []*model.Series{
			series("dungeon-crawler-carl", "Dungeon Crawler Carl", at("dungeon-crawler-carl", "1")),
			{ID: "dungeon-crawler-carl-german-edition", Name: "Dungeon Crawler Carl", License: "CC0-1.0", TranslationOf: []string{"dungeon-crawler-carl"}, Works: []model.SeriesWork{at("dungeon-crawler-carl-de", "1")}},
			series("american-dragons-series", "American Dragons Series", at("denver-fury", "1")),
			series("frontlines", "Frontlines", at("terms-of-enlistment", "1"), at("lines-of-departure", "2")),
			series("frontlines-evolution", "Frontlines: Evolution", at("scorpio", "1")),
			series("summoner-school", "Summoner School", at("summoner-school", "1")),
			series("summoner", "Summoner", at("summoner", "1")),
			series("star-force", "Star Force", at("conquest", "4")),
			{ID: "star-force-polish-edition", Name: "Star Force", License: "CC0-1.0", TranslationOf: []string{"star-force"}, Works: []model.SeriesWork{at("podboj", "4")}},
			series("the-system-apocalypse", "The System Apocalypse", at("the-cost-of-survival", "3")),
			series("system-apocalypse-kismet", "System Apocalypse: Kismet", at("fools-last-dance", "3")),
		},
	}
}

// TestMatchListedAtTheStatedPositionVouches: a work its series lists at exactly
// the stated position, whose recording agrees with the runtime, keeps its
// series agreement over a title in the distrust band and counts that title at
// genericTitleValue at least - the series-named first volume whose file names
// only its subtitle (56 before: title 0.5 distrusted), a volume filed as
// "American Dragons, Book 1" in "American Dragons Series" (55 before: its title
// 0.25, below the band, counted as it is).
// Without the runtime, or off it, nothing changes.
func TestMatchListedAtTheStatedPositionVouches(t *testing.T) {
	_, ts := newTestServerForCatalog(t, listedCatalog())
	carl := url.Values{
		"title": {"Dungeon Crawler Carl - Book 001 - The Apocalypse Will be Televised"}, "author": {"Matt Dinniman"},
		"series": {"Dungeon Crawler Carl"}, "position": {"1"},
	}
	for _, tc := range []struct {
		name    string
		runtime string
		want    float64
		series  string
	}{
		{"the runtime agrees", "48660", 91, string(seriesPosition)},
		{"no runtime: as before", "", 56, string(seriesConflict)},
		{"5% off: as before", "46200", 56, string(seriesConflict)},
	} {
		q := maps.Clone(carl)
		if tc.runtime != "" {
			q.Set("runtime", tc.runtime)
		}
		res := matchJSON(t, ts.URL, q)
		if len(res) == 0 || res[0]["id"] != "dungeon-crawler-carl" {
			t.Fatalf("%s: top = %v, want dungeon-crawler-carl", tc.name, matchIDs(res))
		}
		top := wantScore(t, res, "dungeon-crawler-carl", tc.want)
		if s := top["reasons"].(map[string]any)["series"]; s != tc.series {
			t.Errorf("%s: series reason %v, want %s", tc.name, s, tc.series)
		}
	}

	res := matchJSON(t, ts.URL, url.Values{
		"title": {"American Dragons, Book 1"}, "author": {"Aaron Crash"},
		"series": {"American Dragons", "Aaron Crash (American Dragons)"}, "position": {"1"}, "runtime": {"28680"},
	})
	if len(res) == 0 || res[0]["id"] != "denver-fury" {
		t.Fatalf("American Dragons 1: top = %v, want denver-fury", matchIDs(res))
	}
	wantScore(t, res, "denver-fury", 91)
}

// TestMatchListedRivalsDoNotRise: the five rivals the rule's first prototype
// raised to 91 beside the right volume - each listed at the stated position,
// its recording on the runtime - score as they did without it, because the
// named series does not resolve to theirs (a fuzzy sub-series, a shorter
// series, a spin-off: a series spelled as named is in the pool) or theirs
// translates the one named. A stated language decides the translation instead,
// and a sibling at another position stays damped by positionConflictFactor.
func TestMatchListedRivalsDoNotRise(t *testing.T) {
	_, ts := newTestServerForCatalog(t, listedCatalog())
	for _, tc := range []struct {
		name         string
		q            url.Values
		right, rival string
		rivalScore   float64
		rivalSeries  string
	}{
		{"a fuzzy sub-series", url.Values{
			"title": {"Terms of Enlistment: Frontlines, Book 1"}, "author": {"Marko Kloos"},
			"series": {"Frontlines"}, "position": {"1"}, "runtime": {"34560"},
		}, "terms-of-enlistment", "scorpio", 40, string(seriesPosition)},
		{"a shorter series", url.Values{
			"title": {"Summoner School"}, "author": {"Eric Vall"},
			"series": {"Summoner School"}, "position": {"1"}, "runtime": {"27120"},
		}, "summoner-school", "summoner", 70, string(seriesConflict)},
		{"a translated series of the same name", url.Values{
			"title": {"Conquest: Star Force, Book 4 (Unabridged)"}, "author": {"B. V. Larson"},
			"series": {"Star Force"}, "position": {"4"}, "runtime": {"41460"},
		}, "conquest", "podboj", 48, string(seriesPosition)},
		{"a spin-off at the same number", url.Values{
			"title": {"The Cost of Survival"}, "author": {"Tao Wong"},
			"series": {"System Apocalypse"}, "position": {"3"}, "runtime": {"30960"},
		}, "the-cost-of-survival", "fools-last-dance", 49, string(seriesPosition)},
		{"a sibling at another position", url.Values{
			"title": {"Terms of Enlistment"}, "author": {"Marko Kloos"},
			"series": {"Frontlines"}, "position": {"1"}, "runtime": {"34560"},
		}, "terms-of-enlistment", "lines-of-departure", 24, string(seriesConflict)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := matchJSON(t, ts.URL, tc.q)
			if len(res) == 0 || res[0]["id"] != tc.right {
				t.Fatalf("top = %v, want %s", matchIDs(res), tc.right)
			}
			wantScore(t, res, tc.right, 100)
			rival := wantScore(t, res, tc.rival, tc.rivalScore)
			if s := rival["reasons"].(map[string]any)["series"]; s != tc.rivalSeries {
				t.Errorf("%s series reason %v, want %s", tc.rival, s, tc.rivalSeries)
			}
		})
	}

	// A stated language: the Polish edition is the volume for a Polish request,
	// and the English one, in another language, is not lifted.
	q := url.Values{
		"title": {"Podbój"}, "author": {"B. V. Larson"},
		"series": {"Star Force"}, "position": {"4"}, "runtime": {"41460"}, "language": {"pl"},
	}
	res := matchJSON(t, ts.URL, q)
	if len(res) == 0 || res[0]["id"] != "podboj" {
		t.Fatalf("Polish request: top = %v, want podboj", matchIDs(res))
	}
	q.Set("title", "Conquest")
	res = matchJSON(t, ts.URL, q)
	wantScore(t, res, "podboj", 91)
	english := wantScore(t, res, "conquest", 80)
	if s := english["reasons"].(map[string]any)["language"]; s != "other" {
		t.Errorf("conquest language reason %v, want other", s)
	}
}

// TestResolveSeries: the pool's series a request's series resolves to - those
// spelled as named (an article or a trailing "Series" aside) when any is, else
// every one that fits.
func TestResolveSeries(t *testing.T) {
	cand := func(names ...string) *candidate {
		c := &candidate{}
		for _, n := range names {
			f := newTermForm(matchTerms(n))
			c.series = append(c.series, candidateSeries{name: &f, position: "1"})
		}
		return c
	}
	for _, tc := range []struct {
		name   string
		series []string
		pool   []string
		want   []string
	}{
		{"spelled as named", []string{"Frontlines"}, []string{"Frontlines", "Frontlines: Evolution"}, []string{"frontlines"}},
		{"an article aside", []string{"System Apocalypse"}, []string{"The System Apocalypse", "System Apocalypse: Kismet"}, []string{"the system apocalypse"}},
		{"a trailing Series aside", []string{"Dark Road"}, []string{"Dark Road Series", "Dark Road", "Dark Roads"}, []string{"dark road series", "dark road"}},
		{"none spelled: every fit", []string{"New Era Online; Life Reset"}, []string{"New Era Online", "New Era", "Unrelated"}, []string{"new era online", "new era"}},
		{"no series named", nil, []string{"Frontlines"}, nil},
	} {
		f := readFacts(matchRequest{titles: []string{"x y"}, series: tc.series})
		f.resolveSeries([]*candidate{cand(tc.pool...)})
		got := slices.Sorted(maps.Keys(f.resolved))
		want := slices.Sorted(slices.Values(tc.want))
		if !slices.Equal(got, want) {
			t.Errorf("%s: resolved %q, want %q", tc.name, got, want)
		}
	}
}
