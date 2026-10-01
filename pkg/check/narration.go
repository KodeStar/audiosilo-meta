package check

import (
	"slices"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// narration.go is the NARRATION-LANGUAGE PROFILE: the one rule of record for "which
// language do these narrators record in", read by internal/audit's L-MIX class (a
// narrator veto on a membership move, and the evidence behind a set-work-language
// review) and by internal/importer's recording relocation (a veto on relocating a
// recording whose narrators say it is in another language). Two definitions would
// be two answers to "is this work's language wrong", so there is one.
//
// It is EVIDENCE THAT WITHHOLDS, never evidence that sets. A narrator who records in
// German is a strong hint that a recording credited to them and stated `en` is
// misfiled, but a hint is not a statement: the profile can stop a mechanical change
// ("the narrators contradict the language this move assumes"), and it can put a
// language question in front of a human, and that is all. No writer ever SETS a
// language from it.
//
// THE QUESTION IS ALWAYS ABOUT THE OTHER RECORDINGS. A recording's own language is
// what is in doubt, so counting it would let a misfiled record vouch for itself - and
// so would counting its work's other recordings, which were typically filed by the
// same import. So every question names the work whose recordings to EXCLUDE, and the
// answer is what the narrators' recordings of every OTHER work state.
//
// Counted per DISTINCT recording: a recording two of the narrators share is one
// recording, not two, or a co-narrated production would clear the evidence floor on
// its own. Only individuals are counted - a person record whose `kind` says it is not
// one (a full cast, a publisher credit, a synthetic voice) records in every language
// by nature, and the shared catch-all record (model.UnslugPersonID) stands for every
// credit whose name slugs away to nothing, so it is many people at once.

// NarrationMinRecordings is the evidence floor: fewer OTHER recordings than this and
// the profile knows nothing (Dominant is "", Contradicts is false). A narrator seen
// once elsewhere has not established a language.
const NarrationMinRecordings = 2

// narrationDominantShare is the share (in fifths: 4/5 = 80%) of the other recordings
// one language must hold to be the narrators' language at all. Below it the evidence
// is mixed - a bilingual narrator, a language course read in two - and mixed evidence
// contradicts nothing.
const (
	narrationShareNum = 4
	narrationShareDen = 5
)

// NarrationProfile indexes every recording by its narrators, built once over a
// catalogue. It is read-only after construction and safe for concurrent readers.
type NarrationProfile struct {
	byNarrator map[string][]narrated
}

// narrated is one recording as a narrator's index holds it: a per-profile recording
// number (a recording id is unique only inside its work, so the pair is what dedupes),
// its work and its primary language subtag.
type narrated struct {
	rec  int
	work string
	lang string
}

// NewNarrationProfile indexes a catalogue's recordings by narrator. A recording
// stating no language is not counted - it states nothing to count.
func NewNarrationProfile(cat *model.Catalog) *NarrationProfile {
	notIndividual := map[string]bool{model.UnslugPersonID: true}
	for _, p := range cat.People {
		if p.Kind != "" && p.Kind != model.KindEntityPerson {
			notIndividual[p.ID] = true
		}
	}
	prof := &NarrationProfile{byNarrator: map[string][]narrated{}}
	n := 0
	for _, w := range cat.Works {
		for _, r := range w.Recordings {
			n++
			lang := model.PrimarySubtag(r.Language)
			if lang == "" {
				continue
			}
			for _, person := range r.Narrators {
				if notIndividual[person] {
					continue
				}
				prof.byNarrator[person] = append(prof.byNarrator[person], narrated{rec: n, work: w.ID, lang: lang})
			}
		}
	}
	return prof
}

// NarrationEvidence is what a set of narrators' OTHER recordings state: the number of
// distinct recordings per primary language subtag, and their total.
type NarrationEvidence struct {
	Counts map[string]int
	Total  int
}

// Of is the evidence for a set of narrators, counting every recording any of them
// narrates EXCEPT the recordings of excludeWork (the work whose language is in
// question - pass "" to exclude nothing). It is the question a caller holding ONE
// recording asks (its narrators, its work), and the one OfWork asks for a whole work.
func (p *NarrationProfile) Of(narrators []string, excludeWork string) NarrationEvidence {
	ev := NarrationEvidence{Counts: map[string]int{}}
	seen := map[int]bool{}
	for _, person := range narrators {
		for _, nr := range p.byNarrator[person] {
			if nr.work == excludeWork || seen[nr.rec] {
				continue
			}
			seen[nr.rec] = true
			ev.Counts[nr.lang]++
			ev.Total++
		}
	}
	return ev
}

// OfWork is Of over every narrator of a work's recordings, excluding that work.
func (p *NarrationProfile) OfWork(w *model.Work) NarrationEvidence {
	var narrators []string
	for _, r := range w.Recordings {
		narrators = append(narrators, r.Narrators...)
	}
	slices.Sort(narrators)
	return p.Of(slices.Compact(narrators), w.ID)
}

// Dominant is the one language the evidence names, or "" when it names none: fewer
// than NarrationMinRecordings recordings, or no language holding 80% of them.
func (e NarrationEvidence) Dominant() string {
	if e.Total < NarrationMinRecordings {
		return ""
	}
	for lang, n := range e.Counts {
		// At most one language can hold 80% of the total, so the first that does is
		// the answer whatever order the map yields.
		if n*narrationShareDen >= e.Total*narrationShareNum {
			return lang
		}
	}
	return ""
}

// Contradicts reports whether the evidence names a language OTHER than lang (compared
// by primary subtag). Unknown evidence - too few recordings, or mixed - never
// contradicts, and neither does an unknown lang.
func (e NarrationEvidence) Contradicts(lang string) bool {
	d := e.Dominant()
	l := model.PrimarySubtag(lang)
	return d != "" && l != "" && d != l
}
