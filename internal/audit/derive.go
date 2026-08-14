package audit

import (
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// workDerived is everything about a work's TITLE the detectors ask for: which
// series name it is cleaned against, whether that name came from a membership or
// from the title itself, the decorations it carries, the cleaned title, the volume
// number the title spells out, and the article-plus-series prefix shape.
//
// It is derived once per work and memoized (index.derivedCache), because four
// detectors ask and the answer costs a series-name lookup, a clean and a volume
// probe - and because a title that cleaned one way for W-DUP and another way for
// W-TITLE would make the two classes disagree about the same record.
//
// Everything expensive is computed HERE, in one place, rather than at whichever
// call site happened to want it: the volume probe strips the series name again
// (which allocates), and the article-prefix tuple was being computed twice per work
// (once for its decoration code and once for the slug finding that reports it).
type workDerived struct {
	// seriesName is the series name the title is cleaned against, or "".
	seriesName string
	// embedded reports that the name came from the TITLE, not from a membership -
	// the weaker of the two claims.
	embedded bool
	seriesID string

	// markers are the decoration codes the title carries, in priority order.
	markers []string
	// want is the cleaned title.
	want string
	// plain is the title cleaned against NO series, which a series-less work
	// contributes as its second cluster key.
	plain string
	// wantKey and plainKey are those two titles' NORMALIZED IDENTITY keys, through
	// titlerule.IdentityTitleKey - the one rule pkg/check's census and both writers'
	// duplicate guards key by. This package calibrated that rule and must not spell
	// it a second time; W-DUP's own addition to it (keying an identity-less residual
	// by its series as well) is layered on the key rather than replacing it.
	wantKey  string
	plainKey string

	// seq is the volume number the title itself spells out, against seriesName.
	seq    float64
	hasSeq bool

	// The article-plus-series prefix shape, if the title has it.
	artArticle string
	artSeries  string
	artRest    string
	artOK      bool
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

	d.want = titlerule.Clean(w.Title, d.seriesName)
	d.wantKey = titlerule.IdentityTitleKey(w.Title, d.seriesName)
	if d.seriesName == "" {
		d.plain, d.plainKey = d.want, d.wantKey
	} else {
		d.plain = titlerule.Clean(w.Title, "")
		d.plainKey = titlerule.IdentityTitleKey(w.Title, "")
	}
	d.seq, d.hasSeq = titlerule.BareSeq(w.Title, d.seriesName)
	d.artArticle, d.artSeries, d.artRest, d.artOK = titlerule.ArticleSeriesPrefix(w.Title, ix.resolveSeries)
	d.markers = titlerule.Decorations(titlerule.TitleFacts{
		Title:   w.Title,
		Series:  d.seriesName,
		Resolve: ix.resolveSeries,
	})
	return d
}

// resolveSeries is the index's titlerule.SeriesResolver: the spelling that matched
// plus the series' CANONICAL name, which is what lets the article-prefix rule tell a
// retailer's spurious "A" from a series genuinely called "A Thousand Li".
func (ix *index) resolveSeries(text string) (form, name string, ok bool) {
	form, id, ok := ix.seriesNameIdx.find(text)
	if !ok {
		return "", "", false
	}
	if s := ix.seriesByID[id]; s != nil {
		name = s.Name
	}
	return form, name, true
}
