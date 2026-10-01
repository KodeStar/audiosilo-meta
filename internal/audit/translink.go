package audit

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// translink.go is T-LINK: TRANSLATION LINK candidates, proposed from STATED evidence
// only - the one statement a record makes about its own language, a bracketed
// own-language edition decoration (titlerule.EditionLanguage / SplitEditionName). It is
// the audit half of the Languages Phase 3 link waves; internal/repair's add-work-link
// and add-series-link ops are the write half, and every proposal here is one pkg/check's
// link rules (check.LinkFaults) accept, or it is advisory.
//
// Two subclasses, one per op (each op names its family, and states Target's
// translation_of as including To):
//
//   - SERIES-EDITION (add-series-link). A series named "<base> [<Language> Edition]"
//     whose derived language IS that language, and exactly one OTHER series named <base>
//     (the importer's own
//     SeriesNameKey equality) sharing a member-work author (through samePersonSpelling,
//     so a forked person record is not a miss) and deriving a different, known
//     language. The decoration is the US Audible marketplace's convention for a series
//     it sells in another language, so its BASE names the US-marketplace - English -
//     series. A base in another language comes from another marketplace and is often
//     itself a translation (measured: harry-hole de, 13 of 14 members translator-
//     credited; primal-hunter fr; the-horus-heresy de), so linking to it would link a
//     translation to a translation - but genuine non-English originals exist too
//     (Vernon Subutex [English Edition] -> fr), which is why such a proposal is advisory
//     rather than dropped. A base with a translator-credited member, or a member whose
//     own title carries an own-language edition decoration, is likewise advisory: its
//     members say it is a translation.
//
//   - WORK-EDITION (add-work-link). A work whose title (or subtitle) carries an
//     own-language edition decoration - "(German Edition)" on a `de` work - states that
//     it is a translation;
//     its original is the ONE work in another language that is the same book by the
//     project's own identity rule with the language test removed
//     (check.WorkIdentity.SameBookInAnyLanguage: the normalized title key, author
//     nesting, no stated-volume disagreement, the same collection status). A candidate
//     that itself carries an own-language decoration or a translator credit is a
//     translation too, never an original. A translator credit on its own is NOT enough to
//     propose - measured at 11 pairs, several of them wrong (`madame-bovary`, en, carries
//     the credit because it is itself a translation from French) - and it can never make
//     a work an original. MECHANICAL only where the translation's title is the original's
//     title left untranslated (titlerule.SameUntranslatedTitle: the original's title plus
//     the decoration, articles, case and punctuation aside - and the subtitles likewise,
//     titlerule.SameUntranslatedSubtitle, since the key reads the title alone and a
//     "Volume 2" subtitle would otherwise pass unseen), which is the retailer
//     convention the decoration itself comes from. The normalized key is wider than that
//     on purpose - it strips volumes, collections and series names - and a link asserts
//     more than "not contradicted": "Families First, Volume 2 (German Edition)" met
//     "Families First" because one-sided silence is no disagreement to the duplicate
//     rule, and "Mia & Korum (Die komplette Krinar Chroniken Trilogie) [German Edition]"
//     met "Mia & Korum" because the collection vocabulary is English. Both are advisory
//     here. So is an original that is a dramatized or adapted production (a translation
//     is of the text, and the one English record of "City of Thorns" is its dramatized
//     adaptation).
//
// EXACTLY ONE original, in both subclasses: two candidates are no proposal at all, and
// the count of such ambiguous records (and of records whose candidate fails the language
// test) is reported in SUMMARY.md, never guessed at. A link the record already states
// is never proposed again - that is what makes a repair wave idempotent through the
// fresh-audit gate - and a record already stating a DIFFERENT original is advisory. So
// is any proposal that would INTRODUCE a fault of pkg/check's link rules - a chain, a
// same-language link, a retired original - asked through check.LinkOverlay, the same
// one-record overlay the correct-data form judges a correction by, so a proposal the
// audit calls clean is one the form would accept too.

// T-LINK subclasses.
const (
	tLinkSeries = "series-edition"
	tLinkWork   = "work-edition"
)

// linkOps is the op that adds a translation_of link in each family's namespace.
var linkOps = map[model.RedirectKind]string{
	model.RedirectWorks:  OpAddWorkLink,
	model.RedirectSeries: OpAddSeriesLink,
}

// linkOpKind reads the same family/op definition used when proposing links.
func linkOpKind(op string) (model.RedirectKind, bool) {
	for kind, linkOp := range linkOps {
		if op == linkOp {
			return kind, true
		}
	}
	return "", false
}

// linkStats are T-LINK's count-only tallies: the candidates that yielded no proposal,
// by why. They are the numbers a reviewer needs to read the class in proportion (a
// low proposal count over many decorated records is a gap, not a clean catalogue).
type linkStats struct {
	SeriesDecorated     int // series whose name ends in an own-language edition decoration
	SeriesNoCandidate   int // ...naming no same-author series
	SeriesAmbiguous     int // ...naming two or more
	SeriesLanguageSkips int // ...whose one candidate fails the language test
	WorksDecorated      int // works whose title states their own language's edition
	WorksNoCandidate    int // ...with no same-book work in another language
	WorksAmbiguous      int // ...with two or more candidate originals
}

// detectTranslationLinks runs both subclasses. identity is the load's normalized work
// identity index (check.Result.Identity); nil (a failed load) runs the series half
// alone. skeys is the series key index the SER-DUP detectors share.
func detectTranslationLinks(ix *index, identity *check.WorkIdentity, skeys []seriesKeys) (*findings, linkStats) {
	f := &findings{class: ClassTransLink}
	var st linkStats
	base := check.NewLinkView(ix.workByID, ix.seriesByID, ix.cat.Redirects)
	detectSeriesEditionLinks(ix, skeys, base, f, &st)
	if identity != nil {
		detectWorkEditionLinks(ix, identity, base, f, &st)
	}
	return f, st
}

// onlyOne is the one element of xs, or false after counting xs as none or many.
func onlyOne[T any](xs []T, none, many *int) (T, bool) {
	switch len(xs) {
	case 1:
		return xs[0], true
	case 0:
		*none++
	default:
		*many++
	}
	var zero T
	return zero, false
}

// ---- series-edition -------------------------------------------------------------

func detectSeriesEditionLinks(ix *index, skeys []seriesKeys, base check.LinkView, f *findings, st *linkStats) {
	// Candidates are bucketed by the SER-DUP tight key, which strips every bracketed
	// group: an edition's key is its base's, and SameSeriesName below is the precise
	// filter inside the bucket.
	byTight, _ := groupBy(skeys, func(k seriesKeys) string { return k.tight })
	type edition struct {
		k          seriesKeys
		base, lang string
	}
	var editions []edition
	for _, k := range skeys {
		if titlerule.EditionLanguageOfDecoration(k.decor) == "" {
			continue
		}
		if b, lang, ok := titlerule.SplitEditionName(k.series.Name); ok {
			editions = append(editions, edition{k: k, base: b, lang: lang})
		}
	}
	sort.Slice(editions, func(i, j int) bool { return editions[i].k.series.ID < editions[j].k.series.ID })
	st.SeriesDecorated = len(editions)

	type candidate struct {
		s      *model.Series
		shared []string // the member-work authors it shares with the edition
	}
	for _, e := range editions {
		s := e.k.series
		name := titlerule.NewSeriesName(e.base)
		authors := seriesSideOf(ix, s).authors
		var cands []candidate
		for _, c := range byTight[e.k.tight] {
			if c.series.ID == s.ID || !name.Same(c.series.Name) {
				continue
			}
			if shared := sharedAuthors(ix, authors, seriesSideOf(ix, c.series).authors); len(shared) > 0 {
				cands = append(cands, candidate{s: c.series, shared: shared})
			}
		}
		c, ok := onlyOne(cands, &st.SeriesNoCandidate, &st.SeriesAmbiguous)
		if !ok {
			continue
		}
		o := c.s
		lt, lo := ix.seriesLanguage(s), ix.seriesLanguage(o)
		if lt == "" || lo == "" || lt == lo || lt != e.lang {
			st.SeriesLanguageSkips++
			continue
		}
		if slices.Contains(s.TranslationOf, o.ID) {
			continue // already stated: nothing to propose, which is what makes a wave idempotent
		}
		fd := Finding{
			Subclass: tLinkSeries,
			Key:      s.ID,
			Series:   []SeriesRef{ix.seriesRef(s), ix.seriesRef(o)},
			Propose:  linkProposal(model.RedirectSeries, s.ID, s.TranslationOf, o.ID),
		}
		fd.Notes = []string{
			fmt.Sprintf("the name %q states the %s edition of %q; %s derives %s, %s derives %s",
				s.Name, e.lang, e.base, s.ID, lt, o.ID, lo),
			"shared member-work author: " + truncateList(c.shared, 4),
		}
		var vetoes []string
		if lo != "en" {
			vetoes = append(vetoes, fmt.Sprintf("the base series %s derives %s, not en: an edition decoration names the US "+
				"marketplace's series, and a base from another marketplace is often itself a translation", o.ID, lo))
		}
		if _, ol, edited := titlerule.SplitEditionName(o.Name); edited {
			vetoes = append(vetoes, fmt.Sprintf("the base series %s is itself named as the %s edition of another series", o.ID, ol))
		}
		if n := countMembers(ix, o, hasTranslator); n > 0 {
			vetoes = append(vetoes, fmt.Sprintf("%s has %s credited to a translator: its members say it is a translation",
				o.ID, joinCount(n, "member")))
		}
		if n := countMembers(ix, o, func(w *model.Work) bool { return ownEditionLanguage(w) != "" }); n > 0 {
			vetoes = append(vetoes, fmt.Sprintf("%s has %s whose title states its own language's edition: its members say "+
				"it is a translation", o.ID, joinCount(n, "member")))
		}
		vetoes = append(vetoes, linkVetoes(base, model.RedirectSeries, s.ID, s.TranslationOf, o.ID)...)
		settleLink(&fd, vetoes)
		f.add(fd)
	}
}

// countMembers counts a series' member works pred holds for.
func countMembers(ix *index, s *model.Series, pred func(*model.Work) bool) int {
	n := 0
	for _, sw := range s.Works {
		if w := ix.workByID[sw.Work]; w != nil && pred(w) {
			n++
		}
	}
	return n
}

// sharedAuthors is the authors of a that are one person with an author of b under
// samePersonSpelling (an identical id included), sorted - the candidate test and the
// evidence note's content at once.
func sharedAuthors(ix *index, a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			if samePersonSpelling(ix, x, y) {
				out = append(out, x)
				break
			}
		}
	}
	return sortedUnique(out)
}

// ---- work-edition ---------------------------------------------------------------

func detectWorkEditionLinks(ix *index, identity *check.WorkIdentity, base check.LinkView, f *findings, st *linkStats) {
	for _, t := range ix.cat.Works {
		lang := ownEditionLanguage(t)
		if lang == "" {
			continue
		}
		st.WorksDecorated++
		var origs []*model.Work
		for _, m := range identity.SameBookInAnyLanguage(t) {
			ml := model.PrimarySubtag(m.Work.Language)
			if ml == "" || ml == lang || isStatedTranslation(m.Work) {
				continue
			}
			origs = append(origs, m.Work)
		}
		o, ok := onlyOne(origs, &st.WorksNoCandidate, &st.WorksAmbiguous)
		if !ok {
			continue
		}
		if slices.Contains(t.TranslationOf, o.ID) {
			continue
		}
		fd := Finding{
			Subclass: tLinkWork,
			Key:      t.ID,
			Works:    []WorkRef{ix.workBrief(t), ix.workBrief(o)},
			Propose:  linkProposal(model.RedirectWorks, t.ID, t.TranslationOf, o.ID),
		}
		fd.Notes = []string{fmt.Sprintf("%q states the %s edition on a %s work; %s (%s) is the one same-book work in another language",
			editionText(t), lang, t.Language, o.ID, o.Language)}
		var vetoes []string
		if p := titlerule.ProductOf("", o.Title, o.Subtitle); p.Adapted ||
			titlerule.IsDramatization(o.Title) || titlerule.IsDramatization(o.Subtitle) {
			vetoes = append(vetoes, fmt.Sprintf("the original %s is a dramatized or adapted production (%q): a translation "+
				"is of the text", o.ID, o.Title))
		}
		// The subtitles too: the identity key and its volume statement read the TITLE
		// alone, so "A Game of Fate: Volume 2 (French Edition)" met "A Game of Fate".
		if !titlerule.SameUntranslatedTitle(t.Title, o.Title) || !titlerule.SameUntranslatedSubtitle(t.Subtitle, o.Subtitle) {
			vetoes = append(vetoes, fmt.Sprintf("the titles differ beyond the edition decoration (%q against %q): the "+
				"normalized key met, but only an untranslated title is the retailer's own statement that these are one book",
				editionText(t), editionText(o)))
		}
		vetoes = append(vetoes, linkVetoes(base, model.RedirectWorks, t.ID, t.TranslationOf, o.ID)...)
		settleLink(&fd, vetoes)
		f.add(fd)
	}
}

// ownEditionLanguage is the language a work's title and subtitle state its OWN edition
// in - the decoration's language, when it is the work's own primary language - or "".
// A title and subtitle naming two languages state nothing.
func ownEditionLanguage(w *model.Work) string {
	lang := model.PrimarySubtag(w.Language)
	if lang == "" {
		return ""
	}
	if l, ok := titlerule.EditionLanguage(w.Title, w.Subtitle); ok && l == lang {
		return l
	}
	return ""
}

// editionText is the title (and subtitle) a work-edition note quotes.
func editionText(w *model.Work) string {
	if w.Subtitle == "" {
		return w.Title
	}
	return w.Title + ": " + w.Subtitle
}

// isStatedTranslation reports whether a work STATES that it is a translation: an
// own-language edition decoration, or a translator credit. Either one disqualifies it
// as an original; neither alone makes the other side one.
func isStatedTranslation(w *model.Work) bool {
	return ownEditionLanguage(w) != "" || hasTranslator(w)
}

func hasTranslator(w *model.Work) bool {
	return slices.ContainsFunc(w.Credits, func(c model.Credit) bool { return c.Role == model.RoleTranslator })
}

// ---- the shared proposal and its link checks ----------------------------------

// linkProposal is the link op's proposal: state target's translation_of as including
// to. From is what the record states now (comma-joined, "" for none), which is what
// the repair checks the tree against before it writes.
func linkProposal(kind model.RedirectKind, target string, have []string, to string) Proposal {
	return Proposal{
		Op:     linkOps[kind],
		Target: target,
		Field:  check.FieldTranslationOf,
		From:   strings.Join(have, ","),
		To:     to,
		Reason: "the record states its own language's edition, and exactly one record in another language is the " +
			"same " + check.FamilyNoun(kind) + " by the project's own rules",
	}
}

// settleLink turns a proposal advisory when anything vetoes it.
func settleLink(fd *Finding, vetoes []string) {
	if len(vetoes) == 0 {
		return
	}
	fd.Propose.Advisory = true
	fd.Propose.Reason = "a human should confirm: " + truncateList(vetoes, 4)
}

// linkVetoes are the reasons the link itself may not be added mechanically: the record
// already states a different original (a second one is an omnibus claim a human
// makes), or the link would introduce a fault of pkg/check's link rules - asked over
// the catalogue with this one link overlaid (check.LinkOverlay.IntroducedFaults), so a
// chain in either direction, a same-language link or a retired original is seen as the
// rule of record words it.
func linkVetoes(base check.LinkView, kind model.RedirectKind, id string, have []string, to string) []string {
	var out []string
	if len(have) > 0 {
		out = append(out, fmt.Sprintf("%s already states translation_of [%s]", id, strings.Join(have, ", ")))
	}
	rec := check.LinkRecordOf(base, kind, id)
	rec.TranslationOf = slices.Sorted(slices.Values(append(slices.Clone(have), to)))
	for _, fault := range check.NewLinkOverlay(base, kind, id, rec).IntroducedFaults() {
		out = append(out, fmt.Sprintf("pkg/check would refuse the link (%s %s: %s rule on %s naming %s)",
			kind, fault.From, fault.Code, fault.Field, fault.To))
	}
	return sortedUnique(out)
}
