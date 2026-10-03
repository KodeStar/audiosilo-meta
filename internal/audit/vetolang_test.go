package audit

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// THE PERSIAN GAMBLER SHAPE, which is what this veto exists for: a translation whose
// language tag is wrong (`en`) beside the English original it translates. The tag puts
// both in one stated language, so identityClusters lets them meet, and every other rung
// clears - the title is the one thing that says what the record is.
func TestWorkDupVetoesAnEditionStatingAnotherLanguage(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/th/the-gambler/work.json":         workJSON(t, "the-gambler", "The Gambler"),
		"works/th/the-gambler/recordings/a.json": recJSON(t, "a", "the-gambler", withRuntime(400)),
		"works/th/the-gambler-persian-edition/work.json": workJSON(t, "the-gambler-persian-edition",
			"The Gambler [Persian Edition]"),
		"works/th/the-gambler-persian-edition/recordings/b.json": recJSON(t, "b", "the-gambler-persian-edition",
			withRuntime(410), withNarrators("other-narrator")),
		"people/ot/other-narrator.json": personJSON(t, "other-narrator", "Other Narrator"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster over the original and its edition, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if !got[0].Propose.Advisory {
		t.Fatalf("a translation was proposed for merge into its original: %+v", got[0].Propose)
	}
	if !strings.Contains(got[0].Propose.Reason, "states a fa edition") {
		t.Errorf("reason = %q, want it to name the stated language", got[0].Propose.Reason)
	}
}

// The positive it must not touch: an edition decoration stating the language both
// records ARE is no disagreement, and the decorated twin still merges.
func TestWorkDupKeepsAnEditionStatingItsOwnLanguage(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/th/the-gambler/work.json":         workJSON(t, "the-gambler", "The Gambler"),
		"works/th/the-gambler/recordings/a.json": recJSON(t, "a", "the-gambler", withRuntime(400)),
		"works/th/the-gambler-english-edition/work.json": workJSON(t, "the-gambler-english-edition",
			"The Gambler [English Edition]"),
		"works/th/the-gambler-english-edition/recordings/b.json": recJSON(t, "b", "the-gambler-english-edition",
			withRuntime(410), withNarrators("other-narrator")),
		"people/ot/other-narrator.json": personJSON(t, "other-narrator", "Other Narrator"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if strings.Contains(got[0].Propose.Reason, "edition in its own title") {
		t.Errorf("an edition stating its own language was vetoed: %q", got[0].Propose.Reason)
	}
}

// The rule over values: the statement beats the tag on the stating side and is read
// from the subtitle too, a member with no statement and no tag is never judged, and two
// members that merely carry different TAGS are identityClusters' business, not this
// veto's.
func TestEditionLanguageVetoReadsStatementsOnly(t *testing.T) {
	member := func(id, title, subtitle, lang string) dupMember {
		return dupMember{work: &model.Work{ID: id, Title: title, Subtitle: subtitle, Language: lang}}
	}
	cases := []struct {
		name    string
		members []dupMember
		veto    bool
	}{
		{"a statement against a tag", []dupMember{
			member("a", "X", "", "en"), member("b", "X (Persian Edition)", "", "en"),
		}, true},
		{"a statement in the subtitle", []dupMember{
			member("a", "X", "", "en"), member("b", "X", "(German Edition)", "en"),
		}, true},
		{"two statements that disagree", []dupMember{
			member("a", "X (French Edition)", "", "en"), member("b", "X (German Edition)", "", "en"),
		}, true},
		{"a statement beside an unknown language", []dupMember{
			member("a", "X", "", ""), member("b", "X (German Edition)", "", "de"),
		}, false},
		{"a statement matching a regional tag", []dupMember{
			member("a", "X", "", "de-at"), member("b", "X (German Edition)", "", "de"),
		}, false},
		{"tags alone are not this veto's", []dupMember{
			member("a", "X", "", "en"), member("b", "X", "", "de"),
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := vetoEditionLanguageDiffers(c.members); got != c.veto {
				t.Errorf("veto = %v, want %v", got, c.veto)
			}
		})
	}
}
