package repair

import (
	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// fields.go applies the four single-field ops: retitle-work, restate-position,
// add-series-member and fill-field. None of them touches a slug, so none of them
// retires an id or needs a redirect - which is what makes them the small half of
// this package.
//
// Every one of them re-reads the record and checks the proposal's FROM against what
// the tree states now. That is the same rule the fresh-audit gate applies one level
// up, restated at the write: a proposal is a statement about a record at a moment,
// and rewriting a value that has since changed would apply a decision to data nobody
// judged.

// workScalarFields are the work members a fill-field proposal may write. It is
// deliberately narrow: a scalar the schema types as a string, where "stated" and
// "absent" are the only two states, so filling one cannot half-state a list.
//
// The list is not the same as the set of fields F-HYGIENE REPORTS. Today no detector
// states a VALUE for a missing field (a language nobody recorded is not derivable
// from the record), so every fill-field proposal in a real report refuses here for
// want of a value - see fillField. The op is wired rather than rejected wholesale so
// that a later detector which does state one is applied instead of silently skipped,
// and so the refusal names the reason rather than the op.
var workScalarFields = map[string]bool{
	"language":        true,
	"first_published": true,
	"subtitle":        true,
	"description":     true,
}

// retitleWork sets a work's title. The slug is untouched: it is identity, and W-TITLE
// deliberately proposes no rename (F-HYGIENE lists the rename candidates instead).
func (rn *runner) retitleWork(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Field != "title" {
		return refusef(catMalformed, "retitle-work names field %q; this pass rewrites titles only", p.Field)
	}
	if p.To == "" {
		return refusef(catNoValue, "the proposal states no replacement title")
	}
	e, err := rn.liveWork(t, p.Target)
	if err != nil {
		return err
	}
	if got := e.str("title"); got != p.From {
		return refusef(catStaleValue, "work %q now states the title %q, not the %q the proposal was written against", p.Target, got, p.From)
	}
	next := e.clone()
	next.set("title", p.To)
	t.works.set(p.Target, next)
	t.note("retitled %s: %q -> %q", p.Target, p.From, p.To)
	return nil
}

// fillField states a fact a record is missing. See workScalarFields for why a real
// report's fill-field records refuse.
func (rn *runner) fillField(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.To == "" {
		return refusef(catNoValue,
			"the proposal reports %q missing on %s but states no value to fill it with: the fact has to come from a source, "+
				"which is the correct-data issue form's job, not a mechanical pass's", p.Field, p.Target)
	}
	if !workScalarFields[p.Field] {
		return refusef(catMalformed, "fill-field %q is not one of the work scalars this pass may write (%s)",
			p.Field, joinList(sortedKeys(workScalarFields)))
	}
	e, err := rn.liveWork(t, p.Target)
	if err != nil {
		return err
	}
	if got := e.str(p.Field); got != "" {
		return refusef(catStaleValue, "work %q already states %s = %q", p.Target, p.Field, got)
	}
	next := e.clone()
	next.set(p.Field, p.To)
	t.works.set(p.Target, next)
	t.note("stated %s = %q on %s", p.Field, p.To, p.Target)
	return nil
}

// restatePosition rewrites one membership's position, in place, keeping the
// membership's place in the list.
func (rn *runner) restatePosition(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || p.Series == "" {
		return refusef(catMalformed, "restate-position names no work or no series, so there is no one membership to rewrite")
	}
	if p.To == "" {
		return refusef(catNoValue, "the proposal states no replacement position")
	}
	if err := validPosition(p.To); err != nil {
		return err
	}
	se, works, err := rn.liveSeries(t, p.Series)
	if err != nil {
		return err
	}
	at := -1
	for i, sw := range works {
		if sw.Work == p.Target && sw.Position == p.From {
			at = i
			break
		}
	}
	if at < 0 {
		return refusef(catStaleValue, "series %s no longer lists %s at position %q", p.Series, p.Target, p.From)
	}
	for i, sw := range works {
		if i != at && samePosition(sw.Position, p.To) {
			return refusef(catPositionConflict, "position %q of series %s is held by %s", p.To, p.Series, sw.Work)
		}
	}
	next := append([]model.SeriesWork(nil), works...)
	next[at].Position = p.To
	t.setSeries(p.Series, se.clone(), next)
	t.note("restated %s in series %s: position %q -> %q", p.Target, p.Series, p.From, p.To)
	return nil
}

// addSeriesMember models a membership the catalogue was missing. The membership is
// APPENDED, which is where the importer's own addToSeries puts one: the list's order
// is not the ordering (the positions are), so inserting by position would rewrite
// lines nobody asked about.
func (rn *runner) addSeriesMember(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || p.Series == "" {
		return refusef(catMalformed, "add-series-member names no work or no series")
	}
	if p.To == "" {
		return refusef(catNoValue, "the proposal states no position to add the work at")
	}
	if err := validPosition(p.To); err != nil {
		return err
	}
	if _, err := rn.liveWork(t, p.Target); err != nil {
		return err
	}
	se, works, err := rn.liveSeries(t, p.Series)
	if err != nil {
		return err
	}
	for _, sw := range works {
		if sw.Work == p.Target {
			return refusef(catStaleValue, "series %s already lists %s at position %q", p.Series, p.Target, sw.Position)
		}
		if samePosition(sw.Position, p.To) {
			return refusef(catPositionConflict, "position %q of series %s is held by %s", p.To, p.Series, sw.Work)
		}
	}
	next := append(append([]model.SeriesWork(nil), works...), model.SeriesWork{Work: p.Target, Position: p.To})
	t.setSeries(p.Series, se.clone(), next)
	t.note("added %s to series %s at position %q", p.Target, p.Series, p.To)
	return nil
}

// liveWork reads a work the plan still holds, refusing one an earlier proposal
// retired or one the tree does not carry.
func (rn *runner) liveWork(t *txn, slug string) (entry, error) {
	if by, ok := t.p.retiredBy(pack.FamilyWorks, slug); ok {
		return nil, refusef(catRetired, "work %q was retired by an earlier proposal in this run (%s)", slug, by)
	}
	e, ok, err := t.works.get(slug)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refusef(catMissing, "no work %q in the tree (a recording-scoped proposal names the recording, not its work)", slug)
	}
	return e, nil
}

// liveSeries reads a series and its membership list.
func (rn *runner) liveSeries(t *txn, slug string) (entry, []model.SeriesWork, error) {
	if by, ok := t.p.retiredBy(pack.FamilySeries, slug); ok {
		return nil, nil, refusef(catRetired, "series %q was retired by an earlier proposal in this run (%s)", slug, by)
	}
	e, ok, err := t.series.get(slug)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, refusef(catMissing, "no series %q in the tree", slug)
	}
	return e, e.seriesWorks(), nil
}

// validPosition refuses a position this pass may not write: one the grammar rejects,
// and one whose canonical spelling is not what the proposal states. The grammar is
// importer.NormalizeSequence, the rule of record for what a position may be - the
// same call the audit's own S-INTEGRITY findings are derived through.
func validPosition(pos string) error {
	norm, ok := importer.NormalizeSequence(pos)
	if !ok {
		return refusef(catMalformed, "%q is not a position the data model accepts", pos)
	}
	if norm != pos {
		return refusef(catMalformed, "%q is not the canonical spelling of that position (%q is)", pos, norm)
	}
	return nil
}
