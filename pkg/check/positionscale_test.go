package check

import (
	"encoding/json"
	"strings"
	"testing"
)

// scaledFixture is scaleFixture with a position_scale member on its entry.
func scaledFixture(chapters int, scale string, at ...int) map[string]string {
	files := scaleFixture(chapters, at...)
	files["works/bo/book-one/position_scale.json"] = scale
	return files
}

// TestPositionScaleValid is the passing fixture: the member loads, lands in the
// Catalog under its entry's work, and carries the exact wire shape.
func TestPositionScaleValid(t *testing.T) {
	res := loadTree(t, scaledFixture(80, `{"length":20,"partial":true,"unit":"act"}`, 3, 7, 11, 15, 19))
	if !res.OK() {
		t.Fatalf("a valid position_scale reported problems: %v", res.Problems)
	}
	if len(res.Catalog.PositionScales) != 1 {
		t.Fatalf("PositionScales = %v, want one", res.Catalog.PositionScales)
	}
	ps := res.Catalog.PositionScales[0]
	if ps.Work != "book-one" || ps.Length != 20 || ps.Unit != "act" || !ps.Partial {
		t.Errorf("loaded %+v", ps)
	}
	wire, err := json.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(wire), `{"length":20,"unit":"act","partial":true}`; got != want {
		t.Errorf("wire shape = %s, want %s", got, want)
	}
}

// TestPositionScaleRejected are the violating fixtures, one per way a scale can be
// wrong: the schema's closed shape, and the ruler a position runs past.
func TestPositionScaleRejected(t *testing.T) {
	for _, tc := range []struct{ name, scale, want string }{
		{"unknown unit", `{"length":20,"unit":"page"}`, "unit"},
		{"zero length", `{"length":0,"unit":"act"}`, "length"},
		{"no unit", `{"length":20}`, "unit"},
		{"extra field", `{"length":20,"unit":"act","why":"a reason"}`, "why"},
		{"past the ruler", `{"length":18,"unit":"chapter"}`, "through chapter 19 is past the position_scale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := loadTree(t, scaledFixture(80, tc.scale, 3, 7, 11, 15, 19))
			if res.OK() {
				t.Fatal("expected a problem")
			}
			if !strings.Contains(strings.Join(problemLines(res.Problems), "\n"), tc.want) {
				t.Errorf("problems %v do not mention %q", res.Problems, tc.want)
			}
		})
	}
}

// TestPositionScaleNeedsASidecar: a scale is a statement about the sidecars beside
// it, so it cannot make an entry alone.
func TestPositionScaleNeedsASidecar(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one/position_scale.json"] = `{"length":20,"unit":"act"}`
	if res := loadTree(t, files); res.OK() {
		t.Error("an entry holding only a position_scale loaded")
	}
}

// TestAdvisoryPositionScaleIsARuler pins the stated scale as a RULER, not an
// exemption: it answers the recording's count for a sidecar that reaches it, and it
// still reports one that does not, short lists included. A scale is no suppression,
// so nothing it answers reaches the census's reviewed count.
func TestAdvisoryPositionScaleIsARuler(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tracks   int
		scale    string
		at       []int
		want     int
		mentions string
	}{
		{"reaches a stated length the recording would report", 80, `{"length":20,"unit":"chapter"}`, []int{3, 7, 11, 15, 19}, 0, ""},
		{"stops short of a stated length", 80, `{"length":60,"unit":"chapter"}`, []int{3, 7, 11, 15, 19}, 2, "of the 60 its position_scale states"},
		{"a short recording is no excuse", 10, `{"length":50,"unit":"chapter"}`, []int{1, 2, 3, 4, 5}, 2, "of the 50 its position_scale states"},
		{"partial says it stops short on purpose", 80, `{"length":60,"partial":true,"unit":"chapter"}`, []int{3, 7, 11, 15, 19}, 0, ""},
		{"a short length is judged", 80, `{"length":5,"unit":"act"}`, []int{1, 2, 3, 4, 5}, 0, ""},
		{"a length under the recording minimum still reports", 80, `{"length":15,"unit":"act"}`, []int{1, 2, 3, 4, 5}, 2, "act 5 of the 15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := loadTree(t, scaledFixture(tc.tracks, tc.scale, tc.at...))
			got := advisoryMatching(problemLines(res.Warnings), scaleMarker)
			if len(got) != tc.want {
				t.Fatalf("reported %v, want %d", got, tc.want)
			}
			if tc.want > 0 && !strings.Contains(got[0], tc.mentions) {
				t.Errorf("%q does not mention %q", got[0], tc.mentions)
			}
			if res.Suppressed != 0 {
				t.Errorf("Suppressed = %d: a stated scale is not a review", res.Suppressed)
			}
		})
	}
}

// TestPositionScaleReadWithoutRecordings: the ruler needs no recording, so a sidecar
// PR's own metacheck over the community root sees the advisory too.
func TestPositionScaleReadWithoutRecordings(t *testing.T) {
	files := baseValid()
	chars, recaps := sidecarSpread(3, 7, 11, 15, 19)
	files["works/bo/book-one/characters.json"] = chars
	files["works/bo/book-one/recaps.json"] = recaps
	files["works/bo/book-one/position_scale.json"] = `{"length":60,"unit":"chapter"}`
	if got := advisoryMatching(problemLines(loadTree(t, files).Warnings), scaleMarker); len(got) != 2 {
		t.Errorf("no recording: reported %v, want 2", got)
	}
}

// The compose: both loads read a stated scale, so the community's pass must not
// double the compose's.
func TestComposedPositionScaleReportsOnce(t *testing.T) {
	core := composeCore()
	chars, recaps := sidecarSpread(3, 7, 11, 15, 19)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"book-one": `{"characters":` + chars + `,"recaps":` + recaps + `,"position_scale":{"length":60,"unit":"chapter"}}`,
	}))
	res := LoadComposed(coreDir, comDir)
	if !res.OK() {
		t.Fatalf("problems: %v", res.Problems)
	}
	if got := advisoryMatching(problemLines(res.Warnings), scaleMarker); len(got) != 2 {
		t.Errorf("reported %v, want 2 (one per member, not doubled)", got)
	}
}

// A scale and a member keyed apart meet only at the compose: a scale riding a
// retired key rules on what the survivor holds.
func TestComposeJudgesAScaleAfterTheReKey(t *testing.T) {
	core := composeCore()
	core["redirects.json"] = redirectsOf(`"old-book":"book-one"`)
	chars, _ := sidecarSpread(1, 2, 3)
	_, recaps := sidecarSpread(10, 20, 30)
	scaled := strings.ReplaceAll(chars, `"book-one"`, `"old-book"`)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"old-book": `{"characters":` + scaled + `,"position_scale":{"length":5,"unit":"act"}}`,
		"book-one": `{"recaps":` + recaps + `}`,
	}))
	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("recaps past a scale that rode onto them composed clean")
	}
	// One per recap past the length (10, 20, 30), and each exactly once.
	got := problemsNaming(res.Problems, "past the position_scale")
	if len(got) != 3 {
		t.Fatalf("reported %v, want 3", got)
	}
	if !strings.Contains(got[0], "recap through act 10") {
		t.Errorf("%q does not name the unit", got[0])
	}
}

// The community load has already reported a same-key bound error, so the compose's
// pass stays out: it runs only on a green pass, which that error rules out.
func TestComposeReportsASameKeyBoundOnce(t *testing.T) {
	core := composeCore()
	chars, recaps := sidecarPair([]int{1, 2}, []int{7})
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"book-one": `{"characters":` + chars + `,"recaps":` + recaps + `,"position_scale":{"length":5,"unit":"act"}}`,
	}))
	got := problemsNaming(LoadComposed(coreDir, comDir).Problems, "past the position_scale")
	if len(got) != 1 {
		t.Errorf("reported %v, want exactly one", got)
	}
}

// problemsNaming returns the problem lines containing substr.
func problemsNaming(ps []Problem, substr string) []string {
	return advisoryMatching(problemLines(ps), substr)
}
