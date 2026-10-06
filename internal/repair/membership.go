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
// members state two or more languages (plus a reviewed assertion's homeless drop).
// drop-membership removes a work from a series it is misfiled in (it already sits in a
// series of its own language), move-membership
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
// rather than applied on top. The one exception is a drop naming no home (a reviewed
// assertion's): it judges no language, so only its membership is re-read.
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

// dropMembership removes Target from Series at From. With homes in Others (L-MIX's
// drop) the work must still sit in one deriving its language; with none, only a
// reviewed assertion's drop applies, and only the membership is re-read. The work is
// never written.
func (rn *runner) dropMembership(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Target == "" || p.Series == "" || p.Field != fieldPosition {
		return refusef(CatMalformed, "drop-membership names no work, no series or no position to remove (a dangling-member "+
			"record is advisory and names none)")
	}
	var (
		se    entry
		works []model.SeriesWork
		at    int
		err   error
		why   = "asserted by review"
	)
	if len(p.Others) == 0 {
		// Fail closed: no language or home is asked here, so a detector bug emitting a
		// homeless drop must not reach it.
		if fd.Subclass != audit.SubclassAsserted {
			return refusef(CatMalformed, "a drop naming no home is only ever a reviewed assertion, and %s/%s is not one",
				fd.Class, orUnstated(fd.Subclass))
		}
		if se, works, err = rn.liveSeries(t, p.Series); err != nil {
			return err
		}
		if at, err = membershipAt(works, p.Series, p.Target, p.From); err != nil {
			return err
		}
		// The reviewer's reason is the only audit trail an unconditional drop has.
		if p.Reason != "" {
			why = p.Reason
		}
	} else {
		v := &stagedLinkView{t: t, seriesLang: map[string]string{}}
		var lang string
		if se, works, at, lang, err = rn.leavingMembership(t, v, p); err != nil {
			return err
		}
		var home string
		for _, h := range p.Others {
			_, hw, herr := rn.liveSeries(t, h)
			if herr != nil {
				continue // a home an earlier proposal retired is no home; the next may still be
			}
			if slices.ContainsFunc(hw, func(sw model.SeriesWork) bool { return sw.Work == p.Target }) &&
				v.Language(model.RedirectSeries, h) == lang {
				home = h
				break
			}
		}
		if home == "" {
			return refusef(CatStaleValue, "work %s no longer sits in a series of its language (%s) among [%s], so %s is not "+
				"a misfile to drop", p.Target, lang, joinList(p.Others), p.Series)
		}
		why = fmt.Sprintf("it is a member of %s, which derives %s", home, lang)
	}
	next := slices.Delete(slices.Clone(works), at, at+1)
	if len(next) == 0 {
		return refusef(CatStaleValue, "dropping %s would leave series %s with no members", p.Target, p.Series)
	}
	t.setSeries(p.Series, se.Clone(), next)
	t.note("dropped %s (position %q) from series %s: %s", p.Target, works[at].Position, p.Series, why)
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
	v := &stagedLinkView{t: t, seriesLang: map[string]string{}}
	se, works, at, lang, err := rn.leavingMembership(t, v, p)
	if err != nil {
		return err
	}
	de, dworks, err := rn.liveSeries(t, dest)
	if err != nil {
		return err
	}
	if got := v.Language(model.RedirectSeries, dest); got != lang {
		return refusef(CatStaleValue, "series %s now derives %q, not %s, the language of %s", dest, got, lang, p.Target)
	}
	byPosition := map[string]string{}
	slot := slotKey(p.To)
	for _, sw := range dworks {
		if sw.Work == p.Target {
			return refusef(CatStaleValue, "series %s already lists %s at position %q", dest, p.Target, sw.Position)
		}
		byPosition[slotKey(sw.Position)] = sw.Work
		if other, taken := byPosition[slot]; taken {
			return refusef(CatPositionConflict, "position %q of series %s is held by %s", p.To, dest, other)
		}
	}
	left := slices.Delete(slices.Clone(works), at, at+1)
	if len(left) == 0 {
		return refusef(CatStaleValue, "moving %s would leave series %s with no members", p.Target, p.Series)
	}
	t.setSeries(p.Series, se.Clone(), left)
	t.setSeries(dest, de.Clone(), append(slices.Clone(dworks), model.SeriesWork{Work: p.Target, Position: p.To}))
	t.note("moved %s from series %s (position %q) to %s at position %q, the series of its language (%s)",
		p.Target, p.Series, works[at].Position, dest, p.To, lang)
	// refuseLinkFaults judges EVERY series this txn staged, dest included; the
	// argument only names the record a refusal is about.
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
	v := &stagedLinkView{t: t, seriesLang: map[string]string{}}
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
		if lang := v.workLanguage(id); lang != p.To {
			return refusef(CatStaleValue, "work %s now states %q, not the %s the split was proposed for", id, lang, p.To)
		}
	}
	if len(kept) == 0 {
		return refusef(CatStaleValue, "series %s would be left with no members: every member states %s now", p.Target, p.To)
	}
	// Kept members need not derive From (a decided tie stays a tie); only the moving
	// language must leave. Whether two same-named halves are one is SER-DUP's question.
	if got := model.SeriesLanguage(kept, v.workLanguage); got == p.To {
		return refusef(CatStaleValue, "the members of %s that stay would derive %s, the language moving out", p.Target, got)
	}
	name := se.Str("name")
	var lookupErr error
	slug := importer.FreeSeriesSlug(name, func(slug string) bool {
		if _, retired := t.p.retiredBy(pack.FamilySeries, slug); retired {
			return true
		}
		if lookupErr != nil {
			return false // stop the walk: the error is returned below, whatever slug it ends on
		}
		_, held, gerr := t.series.get(slug)
		if gerr != nil {
			lookupErr = gerr
			return false
		}
		return held
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
	// The new series' name and memberships come from the same sources as the series
	// it splits from, so it cites them; nothing is stated that they do not.
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

// membershipAt is where works lists work at pos's slot (importer.SameSlot, so "03" is
// "3"), or a stale-value refusal.
func membershipAt(works []model.SeriesWork, series, work, pos string) (int, error) {
	at := slices.IndexFunc(works, func(sw model.SeriesWork) bool { return sw.Work == work && importer.SameSlot(sw.Position, pos) })
	if at < 0 {
		return 0, refusef(CatStaleValue, "series %s no longer lists %s at position %q", series, work, pos)
	}
	return at, nil
}

// leavingMembership re-reads the source and verifies the membership and its minority
// language before either a drop or a move plans any writes.
func (rn *runner) leavingMembership(t *txn, v *stagedLinkView, p audit.Proposal) (entry, []model.SeriesWork, int, string, error) {
	se, works, err := rn.liveSeries(t, p.Series)
	if err != nil {
		return nil, nil, 0, "", err
	}
	at, err := membershipAt(works, p.Series, p.Target, p.From)
	if err != nil {
		return nil, nil, 0, "", err
	}
	lang := v.workLanguage(p.Target)
	if lang == "" {
		return nil, nil, 0, "", refusef(CatStaleValue, "work %s states no language now", p.Target)
	}
	if got := v.Language(model.RedirectSeries, p.Series); got == lang {
		return nil, nil, 0, "", refusef(CatStaleValue, "series %s now derives %s, the language of %s, so it is no misfile there", p.Series, got, p.Target)
	}
	return se, works, at, lang, nil
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
