package audit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// R-REPACKAGE subclasses: which signal carried the proposal beside the reader set.
const (
	repackRuntime   = "same-readers-runtime"
	repackPublisher = "same-readers-publisher"
)

// The runtime window, in minutes, a repackage of a LibriVox project falls in. A
// repackage is the project's own audio, so it is never LONGER than the project beyond
// rounding (repackLongerMin, or one percent of a long project), and it is usually a
// little SHORTER: LibriVox opens every section with a spoken preamble and closes it
// with an end line, which a retailer's edition trims. Up to one minute a section (or
// repackShorterMin for a project with few sections) is that trim. Calibrated on the
// real tree (2026-11): 149 of the 158 same-reader pairs fall inside it, shorter by a
// few minutes a book as trimmed preambles predict; of the nine outside it, eight are
// LONGER than the project (by 7 to 136 minutes: a re-recording, often beside a second
// edition of the same reader that does fall inside) and one is shorter by more than
// a minute a section.
const (
	repackLongerMin  = 2
	repackShorterMin = 5
)

// repackPublisherMin is how many RUNTIME-confirmed proposals a publisher must carry
// across the catalogue before its name alone may stand in for a runtime that either
// recording does not state: a publisher repackaging LibriVox projects does so by the
// dozen (Erika, Paperless and Saga Egmont carry 47, 33 and 28 of the real tree's
// pairs), while a one-off imprint says nothing about where its audio came from.
const repackPublisherMin = 3

// repackStats are R-REPACKAGE's count-only tallies: the same-reader pairs the class
// judged and why the ones it did not propose were left out. They are what makes the
// near-misses visible without filing them as findings.
type repackStats struct {
	// Pairs are the (retail recording, LibriVox recording) pairs of one work read by
	// the same set of people.
	Pairs int
	// SharedReader are the pairs whose reader sets overlap without being equal: a
	// LibriVox group project and a retail recording by one of its volunteers.
	SharedReader int
	// LanguageDiffers are same-reader pairs whose recordings state two languages.
	LanguageDiffers int
	// OutsideWindow are same-reader pairs whose stated runtimes fall outside the window.
	OutsideWindow int
	// NoRuntimeNoPublisher are same-reader pairs missing a runtime whose publisher
	// carries too few runtime-confirmed proposals to stand in for it.
	NoRuntimeNoPublisher int
	// Linked and Suppressed are the proposals withdrawn by a reviewed acceptance (the
	// link it records) or rejection.
	Linked, Suppressed int
}

// repackPair is one same-reader pair with the evidence a proposal cites.
type repackPair struct {
	work     *model.Work
	cand, lv *model.Recording
	readers  string // the shared reader set, rendered
	// window is the runtime judgement in words, "" when a runtime is unstated (the
	// pair then stands on its publisher).
	window string
	// matched are every LibriVox recording of the work the candidate's readers read,
	// this pair's own included: one slice shared by the candidate's pairs.
	matched *[]string
}

// detectRepackage proposes, for a recording that is not a LibriVox project on a work
// that also holds one, that it is a retailer's REPACKAGE of that project: the same
// audio, sold under an imprint (Metamorphosis: david-barnes-2016, Paperless, 152
// minutes, beside LibriVox project 527 read by David Barnes in 154). The two stay two
// recordings - a repackage has its own ASINs, cover and publisher, and LibriVox's own
// links and per-section audio belong to the project - so the proposal is a LINK for a
// person to review, never a merge, and nothing applies it: the reviewed decision in
// reviewed.json IS the record (applyRepackageReviews).
//
// Evidence, all of it within ONE work (a candidate on another work is a different
// question, a duplicate work, which W-DUP owns):
//
//   - REQUIRED: the candidate's narrators are exactly the LibriVox recording's readers,
//     as person slugs in any order. Overlap is not enough: a LibriVox group project
//     shares a reader with every retail recording one of its volunteers ever made.
//   - REQUIRED: the two recordings state one language (model.SameLanguage).
//   - SUPPORTING, one of: both runtimes stated and inside the window (subclass
//     same-readers-runtime); or, when either runtime is unstated, a publisher carrying
//     at least repackPublisherMin runtime-confirmed proposals (same-readers-publisher).
//     A stated runtime outside the window refuses the pair whatever the publisher: a
//     narrator re-recording a book they read for LibriVox is the case the window exists
//     to keep out.
//
// ADVISORY throughout: a review queue, never a pass/fail and never mechanical.
func detectRepackage(ix *index) (*findings, repackStats) {
	f := &findings{class: ClassRepackage}
	var st repackStats
	var pairs []repackPair
	confirmed := map[string]int{} // importer.PublisherKey -> runtime-confirmed pairs
	type lvReading struct {
		rec     *model.Recording
		readers []string
	}
	for _, w := range ix.cat.Works {
		if !slices.ContainsFunc(w.Recordings, func(r *model.Recording) bool { return r.LibrivoxID != 0 }) {
			continue
		}
		var lvs []lvReading
		var cands []*model.Recording
		for _, r := range w.Recordings {
			if r.LibrivoxID != 0 {
				lvs = append(lvs, lvReading{r, sortedUnique(r.Narrators)})
			} else {
				cands = append(cands, r)
			}
		}
		for _, c := range cands {
			readers := sortedUnique(c.Narrators)
			if len(readers) == 0 {
				continue
			}
			matched := new([]string)
			rendered := strings.Join(readers, ", ")
			for _, lv := range lvs {
				if !slices.Equal(readers, lv.readers) {
					if slices.ContainsFunc(readers, func(n string) bool { return slices.Contains(lv.readers, n) }) {
						st.SharedReader++
					}
					continue
				}
				st.Pairs++
				if !model.SameLanguage(c.Language, lv.rec.Language) {
					st.LanguageDiffers++
					continue
				}
				p := repackPair{work: w, cand: c, lv: lv.rec, readers: rendered, matched: matched}
				if c.RuntimeMin > 0 && lv.rec.RuntimeMin > 0 {
					ok, window := repackWindow(c.RuntimeMin, lv.rec.RuntimeMin, len(lv.rec.Chapters))
					if !ok {
						st.OutsideWindow++
						continue
					}
					p.window = window
					if key := importer.PublisherKey(c.Publisher); key != "" {
						confirmed[key]++
					}
				}
				*matched = append(*matched, lv.rec.ID)
				pairs = append(pairs, p)
			}
		}
	}
	for _, p := range pairs {
		key := importer.PublisherKey(p.cand.Publisher)
		subclass := repackRuntime
		if p.window == "" {
			if key == "" || confirmed[key] < repackPublisherMin {
				st.NoRuntimeNoPublisher++
				continue
			}
			subclass = repackPublisher
		}
		f.add(repackFinding(ix, p, subclass, confirmed[key]))
	}
	return f, st
}

// repackWindow reports whether a candidate of cand minutes may be the audio of a
// LibriVox project of lv minutes in sections sections, and the window it was judged
// by, in words.
func repackWindow(cand, lv, sections int) (bool, string) {
	longer := max(repackLongerMin, (lv+99)/100)
	shorter := max(repackShorterMin, sections)
	d := lv - cand // minutes the candidate is SHORTER by
	gap := fmt.Sprintf("%d shorter", d)
	switch {
	case d < 0:
		gap = fmt.Sprintf("%d longer", -d)
	case d == 0:
		gap = "the same"
	}
	return d >= -longer && d <= shorter, fmt.Sprintf("runtime %d min against the project's %d, %s (window %d longer to %d shorter)",
		cand, lv, gap, longer, shorter)
}

// repackFinding renders one pair: the work, both recordings, the LibriVox project and
// every piece of evidence, in the proposal's reason (what SUMMARY.md and the action
// prose show) and in the notes (one fact a line). pubCount is the runtime-confirmed
// proposals of the candidate's publisher (importer.PublisherKey), 0 for none.
func repackFinding(ix *index, p repackPair, subclass string, pubCount int) Finding {
	c, lv := p.cand, p.lv
	notes := []string{
		fmt.Sprintf("LibriVox project %d is recording %s: read by %s, %s, %s", lv.LibrivoxID, lv.ID, p.readers,
			runtimeWords(lv.RuntimeMin), joinCount(len(lv.Chapters), "section")),
		fmt.Sprintf("candidate %s: read by %s, %s, publisher %s, released %s", c.ID, p.readers,
			runtimeWords(c.RuntimeMin), orPlaceholder(c.Publisher, "(unstated)"), orPlaceholder(c.ReleaseDate, "(unstated)")),
	}
	evidence := "same readers " + p.readers
	if p.window != "" {
		notes = append(notes, p.window)
		evidence += "; " + p.window
	} else {
		notes = append(notes, "a runtime is unstated, so the publisher stands in for it")
	}
	if c.Publisher != "" {
		pn := fmt.Sprintf("publisher %q (%s in the catalogue)", c.Publisher,
			joinCount(pubCount, "runtime-confirmed proposal"))
		notes = append(notes, pn)
		evidence += "; " + pn
	}
	if others := slices.DeleteFunc(slices.Clone(*p.matched), func(id string) bool { return id == lv.ID }); len(others) > 0 {
		notes = append(notes, "the candidate's readers also read LibriVox recording "+strings.Join(others, ", ")+
			" of this work: accept the one it repackages")
	}
	return Finding{
		Subclass:  subclass,
		Key:       p.work.ID,
		Recording: c.ID,
		Works:     []WorkRef{ix.workBrief(p.work)},
		Notes:     notes,
		Propose: Proposal{Op: OpLinkLibrivoxRepackage, Target: p.work.ID, From: c.ID, To: lv.ID, Advisory: true,
			Reason: fmt.Sprintf("LibriVox project %d; %s", lv.LibrivoxID, evidence)},
	}
}

func runtimeWords(min int) string {
	if min <= 0 {
		return "runtime unstated"
	}
	return fmt.Sprintf("%d min", min)
}

// repackLinkStatus is the outcome of a reviewed link-librivox-repackage decision over
// the live catalogue, p already resolved through the tombstones (a merged work carries
// its recordings to the survivor, so the link resolves with it). The decision is in
// force - linked for an acceptance, suppressed for a rejection - while the work holds
// both recordings, From a retail recording and To a LibriVox project, whether or not
// the detector still proposes the pair: the decision is the record of the link, not a
// reaction to a proposal. Otherwise it is STALE, naming what moved.
func repackLinkStatus(r reviewedDecision, p Proposal, ix *index) (outcomeStatus, string) {
	in := statusLinked
	if r.Decision == "reject" {
		in = statusSuppressed
	}
	if ix == nil {
		return in, ""
	}
	w := ix.workByID[p.Target]
	if w == nil {
		return statusStale, "no live work " + p.Target
	}
	var cand, lv *model.Recording
	for _, rec := range w.Recordings {
		switch rec.ID {
		case p.From:
			cand = rec
		case p.To:
			lv = rec
		}
	}
	switch {
	case cand == nil || lv == nil:
		var gone []string
		if cand == nil {
			gone = append(gone, p.From)
		}
		if lv == nil {
			gone = append(gone, p.To)
		}
		return statusStale, "work " + p.Target + " no longer holds recording " + strings.Join(gone, ", ")
	case cand.LibrivoxID != 0:
		return statusStale, fmt.Sprintf("recording %s is itself LibriVox project %d", p.From, cand.LibrivoxID)
	case lv.LibrivoxID == 0:
		return statusStale, "recording " + p.To + " is not a LibriVox project"
	}
	return in, ""
}

// applyRepackageReviews settles every record-only (recordOnlyOps) decision. It is a
// sibling of unmetRejection and distinctKind: decisions in force WITHOUT a fresh
// proposal meeting them. Each gets its status (repackLinkStatus) and, while in force,
// withdraws the pair's proposal from the queue - an accepted pair is a recorded link, a
// rejected one a reviewed "not this project's audio", and neither is re-asked. Nothing
// is promoted to mechanical and nothing reaches metarepair: no repair applies the op.
// applyReviewed calls it before matching, and keeps these decisions out of its
// proposal index.
func applyRepackageReviews(rep *Report, rs []reviewedDecision, resolved []Proposal, statuses []outcomeStatus,
	whys []string, ix *index) {
	withdraw := map[proposalKey]outcomeStatus{}
	for j, r := range rs {
		if !recordOnlyOps[r.Op] {
			continue
		}
		statuses[j], whys[j] = repackLinkStatus(r, resolved[j], ix)
		if statuses[j] != statusStale {
			// A converging rejection wins, as it does for every other op.
			if k := keyOf(resolved[j]); withdraw[k] != statusSuppressed {
				withdraw[k] = statuses[j]
			}
		}
	}
	if len(withdraw) == 0 {
		return
	}
	c := rep.class(ClassRepackage)
	c.rows = slices.DeleteFunc(c.rows, func(fd Finding) bool {
		switch withdraw[keyOf(fd.Propose)] {
		case statusLinked:
			rep.Repackage.Linked++
		case statusSuppressed:
			rep.Repackage.Suppressed++
		default:
			return false
		}
		return true
	})
}
