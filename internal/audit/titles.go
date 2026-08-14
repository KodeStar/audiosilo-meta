package audit

import (
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// markerPriority orders the decoration subclasses from most to least specific, so
// a title carrying several is filed under the one that most describes it. The
// full list always travels in Markers, and SUMMARY.md counts both.
var markerPriority = []string{
	decArticleSeries, decBracketSuffix, decSeriesName, decVolume,
	decEdition, decGenreSubtitle, decTrailingPunct,
}

// workDerived is everything about a work's TITLE the detectors ask for: which
// series name it is cleaned against, whether that name came from a membership or
// from the title itself, the decorations it carries, and the cleaned title.
//
// It is derived once per work and memoized (index.derivedCache), because four
// detectors ask and the answer costs a series-name lookup - and because a title
// that cleaned one way for W-DUP and another way for W-TITLE would make the two
// classes disagree about the same record.
type workDerived struct {
	seriesName string // the series name the title is cleaned against, or ""
	embedded   bool   // the name came from the title, not from a membership
	seriesID   string
	markers    []string
	want       string
}

// derived returns a work's memoized title derivation.
func (ix *index) derived(w *model.Work) *workDerived {
	if d, ok := ix.derivedCache[w.ID]; ok {
		return d
	}
	d := ix.deriveTitle(w)
	ix.derivedCache[w.ID] = d
	return d
}

// deriveTitle is the derivation itself: the ONE place the audit decides which
// series name a title is read against and what it therefore carries.
func (ix *index) deriveTitle(w *model.Work) *workDerived {
	d := &workDerived{}
	if id, own := ix.seriesNameOf(w); own != "" {
		d.seriesName, d.seriesID = own, id
	} else if form, sid, ok := ix.seriesNameIdx.find(w.Title); ok {
		d.seriesName, d.seriesID, d.embedded = form, sid, true
	}

	title := w.Title
	d.want = auditCleanTitle(title, d.seriesName)

	var markers []string
	if editionMarker.MatchString(title) {
		markers = append(markers, decEdition)
	}
	if volumeMarkerAnywhere.MatchString(title) {
		markers = append(markers, decVolume)
	}
	if bracketSuffix.MatchString(title) {
		markers = append(markers, decBracketSuffix)
	}
	if dropWideGenreSubtitle(title) != title {
		markers = append(markers, decGenreSubtitle)
	}
	if d.seriesName != "" {
		if _, ok := seriesRefIn(strings.ToLower(title), d.seriesName); ok {
			markers = append(markers, decSeriesName)
		}
	}
	if _, _, _, ok := articleSeriesPrefix(title, ix.seriesFormIn); ok {
		markers = append(markers, decArticleSeries)
	}
	if trailingSeparator.MatchString(title) {
		markers = append(markers, decTrailingPunct)
	}
	d.markers = sortedUnique(markers)
	return d
}

// seriesFormIn resolves a series name occurring in text, for the detectors that
// ask the question without a work in hand.
func (ix *index) seriesFormIn(text string) (string, bool) {
	form, _, ok := ix.seriesNameIdx.find(text)
	return form, ok
}

// primaryMarker is the subclass a set of markers files under.
func primaryMarker(markers []string) string {
	for _, want := range markerPriority {
		for _, m := range markers {
			if m == want {
				return want
			}
		}
	}
	return ""
}

// detectWorkTitle reports every work whose title carries retailer decoration AND
// for which the audit can propose a cleaner one. A title-only class: it never
// proposes a slug change, because a slug is identity and moving one is a rename
// with its own referential consequences (every series membership, every sidecar).
func detectWorkTitle(ix *index) *findings {
	f := &findings{class: ClassWorkTitle}
	for _, w := range ix.cat.Works {
		d := ix.derived(w)
		markers, want := d.markers, d.want
		if len(markers) == 0 || want == w.Title || want == "" {
			continue
		}
		sub := primaryMarker(markers)
		if sub == "" {
			continue
		}
		f.add(Finding{
			Subclass: sub,
			Key:      w.ID,
			Works:    []WorkRef{ix.workBrief(w)},
			Markers:  markers,
			Field:    "title",
			Have:     w.Title,
			Want:     want,
			Action:   "retitle the work; the slug is identity and is deliberately left alone",
		})
	}
	return f
}

// W-NOSERIES subclasses.
const (
	noSeriesAndPosition = "series-and-position"
	noSeriesOnly        = "series-only"
	noPositionOnly      = "position-only"
)

// detectWorkNoSeries reports works that belong to no series but whose title names
// one, states a volume number, or both - the shape a bulk retailer import leaves
// when the series column was empty and the series was only ever in the title.
func detectWorkNoSeries(ix *index) *findings {
	f := &findings{class: ClassWorkNoSeries}
	for _, w := range ix.cat.Works {
		if len(ix.memberships[w.ID]) > 0 {
			continue
		}
		d := ix.derived(w)
		form, sid, found := d.seriesName, d.seriesID, d.embedded
		if !found {
			// No series resolved. A title that still spells a volume number out
			// is a membership nobody modeled, but the audit cannot say of what.
			if m := markerSeq.FindStringSubmatch(w.Title); m != nil {
				f.add(Finding{
					Subclass: noPositionOnly,
					Key:      w.ID,
					Works:    []WorkRef{ix.workBrief(w)},
					Field:    "series",
					Have:     "",
					Want:     m[1],
					Action:   "identify the series this volume number belongs to; no series in the catalogue matches the title",
				})
			}
			continue
		}
		s := ix.seriesByID[sid]
		fd := Finding{
			Key:   w.ID,
			Works: []WorkRef{ix.workBrief(w)},
			Field: "series",
		}
		if s != nil {
			fd.Series = []SeriesRef{ix.seriesRef(s)}
		}
		seq, hasSeq := bareSeq(w.Title, form)
		if hasSeq {
			fd.Subclass = noSeriesAndPosition
			fd.Want = seqKey(seq)
			fd.Action = fmt.Sprintf("add %s to series %s at position %s", w.ID, sid, seqKey(seq))
			if held := ix.positions[sid][seqKey(seq)]; held != "" && held != w.ID {
				fd.Notes = append(fd.Notes, "position already held by "+held+
					" - resolve as a duplicate (see W-DUP) before adding a membership")
			}
		} else {
			fd.Subclass = noSeriesOnly
			fd.Action = fmt.Sprintf("verify and add %s to series %s; the title names the series but states no position", w.ID, sid)
		}
		fd.Notes = append(fd.Notes, `title spells the series name "`+form+`"`)
		f.add(fd)
	}
	return f
}
