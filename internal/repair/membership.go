package repair

import (
	"fmt"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// membership.go applies the audit's L-MIX proposals: the memberships of a series whose
// members state two or more languages. drop-membership removes a work from a series it
// is misfiled in (it already sits in a series of its own language), move-membership
// moves it to the one series of its language that is this series under another name,
// split-series moves a minority language's members out to a NEW series of the same
// name, and set-work-language resets a work's language. The first three write the
// series family alone - a work does not reference its series - and the last writes one
// work entry.
//
// Every one of them re-reads what it changes and checks the proposal against it, the
// fresh-audit gate restated at the write: the membership is still at the position the
// proposal names, the work still states the language it was judged in, the series it
// leaves still does not derive that language, and the series it joins still does. An
// earlier proposal in the same run that moved any of it makes a later one stale-value
// rather than applied on top.
//
// The new series a split mints takes its slug from the importer's own chain
// (importer.FreeSeriesSlug over the series name, a held, retired or reserved candidate
// stepped past), RE-DERIVED here against the plan so far rather than read off the
// audit's note, so two splits of one name in a run take two slugs. The split series is
// held to txn.refuseLinkFaults like every other series this pass writes.

// fieldPosition and fieldLanguage are the fields the L-MIX ops name.
const (
	fieldPosition = "position"
	fieldLanguage = "language"
	// licenseCC0 is the core layer's one license (schema $defs/license), which a series
	// this pass mints carries like every other core record.
	licenseCC0 = "CC0-1.0"
)

// dropMembership removes Target from Series at From.
func (rn *runner) dropMembership(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || p.Series == "" || p.Field != fieldPosition {
		return refusef(CatMalformed, "drop-membership names no work, no series or no position to remove (a dangling-member "+
			"record is advisory and names none)")
	}
	se, works, err := rn.liveSeries(t, p.Series)
	if err != nil {
		return err
	}
	at, err := membershipAt(works, p.Series, p.Target, p.From)
	if err != nil {
		return err
	}
	lang, err := rn.leavingLanguage(t, p.Series, works, p.Target)
	if err != nil {
		return err
	}
	var home string
	for _, h := range p.Others {
		_, hw, herr := rn.liveSeries(t, h)
		if herr != nil {
			continue // a home an earlier proposal retired is no home; the next may still be
		}
		if slices.ContainsFunc(hw, func(sw model.SeriesWork) bool { return sw.Work == p.Target }) &&
			rn.seriesLanguage(t, hw) == lang {
			home = h
			break
		}
	}
	if home == "" {
		return refusef(CatStaleValue, "work %s no longer sits in a series of its language (%s) among [%s], so %s is not "+
			"a misfile to drop", p.Target, lang, joinList(p.Others), p.Series)
	}
	next := slices.Delete(slices.Clone(works), at, at+1)
	t.setSeries(p.Series, se.Clone(), next)
	t.note("dropped %s (position %q) from series %s: it is a member of %s, which derives %s", p.Target, p.From, p.Series, home, lang)
	return t.refuseLinkFaults(p.Series)
}

// moveMembership moves Target from Series at From to Others[0] at To.
func (rn *runner) moveMembership(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || p.Series == "" || p.Field != fieldPosition || len(p.Others) != 1 {
		return refusef(CatMalformed, "move-membership names a work, the series it leaves, its position there and ONE series to "+
			"move it to")
	}
	if p.To == "" {
		return refusef(CatNoValue, "the proposal states no position to move %s to", p.Target)
	}
	if err := validPosition(p.To); err != nil {
		return err
	}
	dest := p.Others[0]
	if dest == p.Series {
		return refusef(CatMalformed, "move-membership would move %s from %s to itself", p.Target, dest)
	}
	se, works, err := rn.liveSeries(t, p.Series)
	if err != nil {
		return err
	}
	at, err := membershipAt(works, p.Series, p.Target, p.From)
	if err != nil {
		return err
	}
	lang, err := rn.leavingLanguage(t, p.Series, works, p.Target)
	if err != nil {
		return err
	}
	de, dworks, err := rn.liveSeries(t, dest)
	if err != nil {
		return err
	}
	if got := rn.seriesLanguage(t, dworks); got != lang {
		return refusef(CatStaleValue, "series %s now derives %q, not %s, the language of %s", dest, got, lang, p.Target)
	}
	for _, sw := range dworks {
		if sw.Work == p.Target {
			return refusef(CatStaleValue, "series %s already lists %s at position %q", dest, p.Target, sw.Position)
		}
		if importer.SameSlot(sw.Position, p.To) {
			return refusef(CatPositionConflict, "position %q of series %s is held by %s", p.To, dest, sw.Work)
		}
	}
	t.setSeries(p.Series, se.Clone(), slices.Delete(slices.Clone(works), at, at+1))
	t.setSeries(dest, de.Clone(), append(slices.Clone(dworks), model.SeriesWork{Work: p.Target, Position: p.To}))
	t.note("moved %s from series %s (position %q) to %s at position %q, the series of its language (%s)",
		p.Target, p.Series, p.From, dest, p.To, lang)
	return t.refuseLinkFaults(p.Series)
}

// splitSeries moves Others, the members of Target stating To, to a new series of
// Target's name; the members in every other language keep Target.
func (rn *runner) splitSeries(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || len(p.Others) == 0 || p.Field != fieldLanguage {
		return refusef(CatMalformed, "split-series names no series, no members to move, or not the language they move in")
	}
	if p.To == "" {
		return refusef(CatNoValue, "the proposal states no language for the members it moves")
	}
	se, works, err := rn.liveSeries(t, p.Target)
	if err != nil {
		return err
	}
	moving := importer.ToSet(p.Others)
	var moved, kept []model.SeriesWork
	for _, sw := range works {
		if moving[sw.Work] {
			moved = append(moved, sw)
		} else {
			kept = append(kept, sw)
		}
	}
	for _, id := range p.Others {
		if !slices.ContainsFunc(moved, func(sw model.SeriesWork) bool { return sw.Work == id }) {
			return refusef(CatStaleValue, "series %s no longer lists %s", p.Target, id)
		}
		if lang := rn.workLanguage(t, id); lang != p.To {
			return refusef(CatStaleValue, "work %s now states %q, not the %s the split was proposed for", id, lang, p.To)
		}
	}
	if len(kept) == 0 {
		return refusef(CatStaleValue, "series %s would be left with no members: every member states %s now", p.Target, p.To)
	}
	if got := rn.seriesLanguage(t, kept); got == p.To {
		return refusef(CatStaleValue, "the members of %s that stay would derive %s, the language moving out", p.Target, got)
	}
	name := se.Str("name")
	var lookupErr error
	slug := importer.FreeSeriesSlug(name, func(slug string) bool {
		if _, retired := t.p.retiredBy(pack.FamilySeries, slug); retired {
			return true
		}
		_, held, gerr := t.series.get(slug)
		if gerr != nil && lookupErr == nil {
			lookupErr = gerr
		}
		return held || gerr != nil
	}, t.p.redirects)
	if lookupErr != nil {
		return lookupErr
	}
	if slug == "" {
		return refusef(CatMalformed, "the name %q slugs away to nothing, so the new series has no slug to be minted at", name)
	}
	ne := entry{}
	ne.Set("id", slug)
	ne.Set("name", name)
	ne.Set("license", licenseCC0)
	if raw, ok := se["sources"]; ok {
		ne.SetRaw("sources", raw)
	}
	t.setSeries(slug, ne, moved)
	t.setSeries(p.Target, se.Clone(), kept)
	t.note("split series %s: moved its %d %s member(s) (%s) to the new series %s named %q, positions preserved; the %s "+
		"members keep %s", p.Target, len(moved), p.To, joinList(memberStrings(moved)), slug, name, orUnstated(p.From), p.Target)
	return t.refuseLinkFaults(p.Target)
}

// setWorkLanguage sets Target's language from From to To, and every recording stating
// From with it, when every recording then states To.
func (rn *runner) setWorkLanguage(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || p.Field != fieldLanguage || p.From == "" {
		return refusef(CatMalformed, "set-work-language names no work, not the language field, or no language to replace")
	}
	if p.To == "" {
		return refusef(CatNoValue, "the proposal states no language to set on %s", p.Target)
	}
	if p.To == p.From {
		return refusef(CatMalformed, "set-work-language would set %s's language to the one it states", p.Target)
	}
	e, err := rn.liveWork(t, p.Target)
	if err != nil {
		return err
	}
	if got := e.Str(fieldLanguage); got != p.From {
		return refusef(CatStaleValue, "work %s now states the language %q, not the %q the proposal was written against", p.Target, got, p.From)
	}
	next := e.Clone()
	next.Set(fieldLanguage, p.To)
	recs, err := next.Recordings()
	if err != nil {
		return fmt.Errorf("parse the recordings of work %q: %w", p.Target, err)
	}
	var reset []string
	for _, key := range rawentry.SortedKeys(recs) {
		rec := recs[key]
		switch lang := rec.Str(fieldLanguage); {
		case lang == p.From:
			rec.Set(fieldLanguage, p.To)
			reset = append(reset, key)
		case model.PrimarySubtag(lang) != model.PrimarySubtag(p.To):
			return refusef(CatStaleValue, "recording %s of %s states %q, which is neither %q nor %q: after the change its "+
				"recordings would not all state %s", key, p.Target, lang, p.From, p.To, p.To)
		}
	}
	if len(reset) > 0 {
		if err := next.SetRecordings(recs); err != nil {
			return fmt.Errorf("render the recordings of work %q: %w", p.Target, err)
		}
	}
	t.works.put(p.Target, next)
	t.note("set the language of %s from %q to %q, and of its recordings stating %q (%s)", p.Target, p.From, p.To, p.From,
		orUnstated(joinList(reset)))
	return t.refuseLinkFaults(p.Target)
}

// membershipAt is where works lists work at exactly pos, or a stale-value refusal.
func membershipAt(works []model.SeriesWork, series, work, pos string) (int, error) {
	at := slices.IndexFunc(works, func(sw model.SeriesWork) bool { return sw.Work == work && sw.Position == pos })
	if at < 0 {
		return 0, refusef(CatStaleValue, "series %s no longer lists %s at position %q", series, work, pos)
	}
	return at, nil
}

// leavingLanguage is the language a work leaving a series states, refusing the move
// when the work states none or the series now derives that very language - the work is
// no longer a minority there, which is the only thing the proposal was about.
func (rn *runner) leavingLanguage(t *txn, series string, works []model.SeriesWork, work string) (string, error) {
	lang := rn.workLanguage(t, work)
	if lang == "" {
		return "", refusef(CatStaleValue, "work %s states no language now", work)
	}
	if got := rn.seriesLanguage(t, works); got == lang {
		return "", refusef(CatStaleValue, "series %s now derives %s, the language of %s, so it is no misfile there", series, got, work)
	}
	return lang, nil
}

// workLanguage is a work's primary language subtag as the txn sees it (a language an
// earlier proposal set is the one judged), read the way the link rules read it.
func (rn *runner) workLanguage(t *txn, id string) string {
	return (&stagedLinkView{t: t, seriesLang: map[string]string{}}).workLanguage(id)
}

// seriesLanguage is a membership list's derived language over the txn's view.
func (rn *runner) seriesLanguage(t *txn, works []model.SeriesWork) string {
	return model.SeriesLanguage(works, func(id string) string { return rn.workLanguage(t, id) })
}

func memberStrings(ws []model.SeriesWork) []string {
	out := make([]string, 0, len(ws))
	for _, sw := range ws {
		out = append(out, sw.Work+"@"+sw.Position)
	}
	return out
}

func orUnstated(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
