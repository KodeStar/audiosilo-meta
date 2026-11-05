package query

import (
	"maps"
	"math"
	"net/url"
	"slices"
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
