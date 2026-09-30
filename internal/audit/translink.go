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
// the audit half of the Languages Phase 3 link waves; internal/repair's add-link op is
// the write half, and every proposal here is one pkg/check's link rules
// (check.LinkFaults) accept, or it is advisory.
//
// Two subclasses, one op (add-link: state Target's translation_of as including To):
//
//   - SERIES-EDITION. A series named "<base> [<Language> Edition]" whose derived language
//     IS that language, and exactly one OTHER series named <base> (the importer's own
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
//   - WORK-EDITION. A work whose title (or subtitle) carries an own-language edition
//     decoration - "(German Edition)" on a `de` work - states that it is a translation;
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
//     the decoration, articles, case and punctuation aside), which is the retailer
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
// fresh-audit gate - and a record already stating a DIFFERENT original, or an original
// that is itself a translation (a chain), is advisory. So is any proposal pkg/check's
// link rules would refuse, asked of the catalogue with the proposed link overlaid.

// T-LINK subclasses.
const (
	tLinkSeries = "series-edition"
	tLinkWork   = "work-edition"
)

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
// identity index (check.Result.Identity); nil (a failed load) runs the series half alone.
func detectTranslationLinks(ix *index, identity *check.WorkIdentity) (*findings, linkStats) {
	f := &findings{class: ClassTransLink}
	var st linkStats
	lv := newLinkOverlay(ix)
	detectSeriesEditionLinks(ix, lv, f, &st)
	if identity != nil {
		detectWorkEditionLinks(ix, identity, lv, f, &st)
	}
	return f, st
}

// ---- series-edition -------------------------------------------------------------

func detectSeriesEditionLinks(ix *index, lv *linkOverlay, f *findings, st *linkStats) {
	byKey := make(map[string][]*model.Series, len(ix.cat.Series))
	type edition struct {
		s          *model.Series
		base, lang string
	}
	var editions []edition
	for _, s := range ix.cat.Series {
		k := titlerule.SeriesNameKey(s.Name)
		byKey[k] = append(byKey[k], s)
		if base, lang, ok := titlerule.SplitEditionName(s.Name); ok {
			editions = append(editions, edition{s: s, base: base, lang: lang})
		}
	}
	sort.Slice(editions, func(i, j int) bool { return editions[i].s.ID < editions[j].s.ID })
	st.SeriesDecorated = len(editions)

	for _, e := range editions {
		name := titlerule.NewSeriesName(e.base)
		side := seriesSideOf(ix, e.s)
		var cands []*model.Series
		for _, c := range byKey[titlerule.SeriesNameKey(e.base)] {
			if c.ID == e.s.ID || !name.Same(c.Name) {
				continue
			}
			if anySamePerson(ix, side.authors, seriesSideOf(ix, c).authors) {
				cands = append(cands, c)
			}
		}
		switch len(cands) {
		case 0:
			st.SeriesNoCandidate++
			continue
		case 1:
		default:
			st.SeriesAmbiguous++
			continue
		}
		o := cands[0]
		lt, lo := ix.seriesLanguage(e.s), ix.seriesLanguage(o)
		if lt == "" || lo == "" || lt == lo || lt != e.lang {
			st.SeriesLanguageSkips++
			continue
		}
		if slices.Contains(e.s.TranslationOf, o.ID) {
			continue // already stated: nothing to propose, which is what makes a wave idempotent
		}
		fd := Finding{
			Subclass: tLinkSeries,
			Key:      e.s.ID,
			Series:   []SeriesRef{ix.seriesRef(e.s), ix.seriesRef(o)},
			Propose:  linkProposal(model.RedirectSeries, e.s.ID, e.s.TranslationOf, o.ID),
		}
		fd.Notes = []string{
			fmt.Sprintf("the name %q states the %s edition of %q; %s derives %s, %s derives %s",
				e.s.Name, e.lang, e.base, e.s.ID, lt, o.ID, lo),
			"shared member-work author: " + truncateList(sharedAuthors(ix, side.authors, seriesSideOf(ix, o).authors), 4),
		}
		var vetoes []string
		if lo != "en" {
			vetoes = append(vetoes, fmt.Sprintf("the base series %s derives %s, not en: an edition decoration names the US "+
				"marketplace's series, and a base from another marketplace is often itself a translation", o.ID, lo))
		}
		if _, ol, edited := titlerule.SplitEditionName(o.Name); edited {
			vetoes = append(vetoes, fmt.Sprintf("the base series %s is itself named as the %s edition of another series", o.ID, ol))
		}
		if n := translatorCredited(ix, o); n > 0 {
			vetoes = append(vetoes, fmt.Sprintf("%s has %s credited to a translator: its members say it is a translation",
				o.ID, joinCount(n, "member")))
		}
		if n := editionDecorated(ix, o); n > 0 {
			vetoes = append(vetoes, fmt.Sprintf("%s has %s whose title states its own language's edition: its members say "+
				"it is a translation", o.ID, joinCount(n, "member")))
		}
		vetoes = append(vetoes, linkVetoes(lv, model.RedirectSeries, e.s.ID, e.s.TranslationOf, o.ID)...)
		settleLink(&fd, vetoes)
		f.add(fd)
	}
}

// translatorCredited counts a series' member works carrying a translator credit.
func translatorCredited(ix *index, s *model.Series) int {
	n := 0
	for _, sw := range s.Works {
		if w := ix.workByID[sw.Work]; w != nil && hasTranslator(w) {
			n++
		}
	}
	return n
}

// editionDecorated counts a series' member works whose title states their own
// language's edition.
func editionDecorated(ix *index, s *model.Series) int {
	n := 0
	for _, sw := range s.Works {
		if w := ix.workByID[sw.Work]; w != nil && ownEditionLanguage(w) != "" {
			n++
		}
	}
	return n
}

// sharedAuthors is the authors two lists have in common by id, else the first side's
// authors that are a spelling of one on the other side - the evidence note's content.
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

func detectWorkEditionLinks(ix *index, identity *check.WorkIdentity, lv *linkOverlay, f *findings, st *linkStats) {
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
		switch len(origs) {
		case 0:
			st.WorksNoCandidate++
			continue
		case 1:
		default:
			st.WorksAmbiguous++
			continue
		}
		o := origs[0]
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
		if isDramatizedOrAdapted(o) {
			vetoes = append(vetoes, fmt.Sprintf("the original %s is a dramatized or adapted production (%q): a translation "+
				"is of the text", o.ID, o.Title))
		}
		if !titlerule.SameUntranslatedTitle(t.Title, o.Title) {
			vetoes = append(vetoes, fmt.Sprintf("the titles differ beyond the edition decoration (%q against %q): the "+
				"normalized key met, but only an untranslated title is the retailer's own statement that these are one book",
				t.Title, o.Title))
		}
		vetoes = append(vetoes, linkVetoes(lv, model.RedirectWorks, t.ID, t.TranslationOf, o.ID)...)
		settleLink(&fd, vetoes)
		f.add(fd)
	}
}

// ownEditionLanguage is the language a work's title or subtitle states its OWN edition
// in - the decoration's language, when it is the work's own primary language - or "".
// A title and subtitle naming two languages state nothing.
func ownEditionLanguage(w *model.Work) string {
	lang := model.PrimarySubtag(w.Language)
	if lang == "" {
		return ""
	}
	l, ok := titlerule.EditionLanguage(w.Title + " " + w.Subtitle)
	if !ok || l != lang {
		return ""
	}
	return l
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
	for _, c := range w.Credits {
		if c.Role == "translator" {
			return true
		}
	}
	return false
}

// isDramatizedOrAdapted reports whether a work's title or subtitle announces a
// dramatized production or a derived edition (titlerule's product vocabulary).
func isDramatizedOrAdapted(w *model.Work) bool {
	for _, t := range []string{w.Title, w.Subtitle} {
		if t != "" && (titlerule.IsDramatization(t) || titlerule.IsYoungReadersAdaptation(t) || titlerule.IsSeriesEdition(t)) {
			return true
		}
	}
	return false
}

// ---- the shared proposal and its link checks ----------------------------------

// linkProposal is the add-link proposal: state target's translation_of as including
// to. From is what the record states now (comma-joined, "" for none), which is what
// the repair checks the tree against before it writes.
func linkProposal(kind model.RedirectKind, target string, have []string, to string) Proposal {
	return Proposal{
		Op:     OpAddLink,
		Kind:   string(kind),
		Target: target,
		Field:  check.FieldTranslationOf,
		From:   strings.Join(have, ","),
		To:     to,
		Reason: "the record states its own language's edition, and exactly one record in another language is the " +
			"same " + nounOf(kind) + " by the project's own rules",
	}
}

func nounOf(kind model.RedirectKind) string {
	if kind == model.RedirectSeries {
		return "series"
	}
	return "book"
}

// settleLink turns a proposal advisory when anything vetoes it.
func settleLink(fd *Finding, vetoes []string) {
	if len(vetoes) == 0 {
		return
	}
	fd.Propose.Advisory = true
	fd.Propose.Reason = "a human should confirm: " + truncateList(vetoes, 4)
}

// linkVetoes are the reasons the link itself may not be added mechanically: the
// record already states a different original (a second one is an omnibus claim a
// human makes), the original is itself a translation (a chain), or pkg/check's link
// rules - asked of the catalogue with this one link overlaid - report a problem.
func linkVetoes(lv *linkOverlay, kind model.RedirectKind, id string, have []string, to string) []string {
	var out []string
	noun := string(kind)
	if len(have) > 0 {
		out = append(out, fmt.Sprintf("%s already states translation_of [%s]", id, strings.Join(have, ", ")))
	}
	if originals := lv.base.TranslationOf(kind, to); len(originals) > 0 {
		out = append(out, fmt.Sprintf("%s is itself a translation of [%s]: link to the original instead",
			to, strings.Join(originals, ", ")))
	}
	lv.set(kind, id, to)
	defer lv.clear()
	for _, fault := range check.LinkFaults(lv, kind, id) {
		if fault.Severity == check.LinkProblem {
			out = append(out, fmt.Sprintf("pkg/check would refuse the link (%s %s: %s rule on %s naming %s)",
				noun, fault.From, fault.Code, fault.Field, fault.To))
		}
	}
	return sortedUnique(out)
}

// linkOverlay is the catalogue's check.LinkView with ONE proposed link added: id's
// translation_of gains to, and to's inverse gains id. It is built once per run over
// the index's id maps and re-pointed per proposal (set/clear), so asking the rule of
// record costs a handful of map reads per candidate.
//
// The tombstone table is empty here: the audit never reads data/redirects.json, and a
// proposal only ever names live ids. An existing link naming a retired id reads as
// dead rather than retired - a different wording of the same problem.
type linkOverlay struct {
	base check.LinkView
	kind model.RedirectKind
	id   string
	to   string
}

func newLinkOverlay(ix *index) *linkOverlay {
	return &linkOverlay{base: check.NewLinkView(ix.workByID, ix.seriesByID, model.NewRedirects())}
}

func (o *linkOverlay) set(kind model.RedirectKind, id, to string) { o.kind, o.id, o.to = kind, id, to }
func (o *linkOverlay) clear()                                     { o.id, o.to = "", "" }

func (o *linkOverlay) Holds(kind model.RedirectKind, id string) bool { return o.base.Holds(kind, id) }
func (o *linkOverlay) Survivor(kind model.RedirectKind, id string) (string, bool) {
	return o.base.Survivor(kind, id)
}
func (o *linkOverlay) Language(kind model.RedirectKind, id string) string {
	return o.base.Language(kind, id)
}

func (o *linkOverlay) TranslationOf(kind model.RedirectKind, id string) []string {
	have := o.base.TranslationOf(kind, id)
	if o.id == "" || kind != o.kind || id != o.id || slices.Contains(have, o.to) {
		return have
	}
	return slices.Sorted(slices.Values(append(slices.Clone(have), o.to)))
}

func (o *linkOverlay) TranslatedBy(kind model.RedirectKind, id string) []string {
	have := o.base.TranslatedBy(kind, id)
	if o.id == "" || kind != o.kind || id != o.to || slices.Contains(have, o.id) {
		return have
	}
	return slices.Sorted(slices.Values(append(slices.Clone(have), o.id)))
}

func (o *linkOverlay) Ordering(series string) string   { return o.base.Ordering(series) }
func (o *linkOverlay) OrderingOf(series string) string { return o.base.OrderingOf(series) }
func (o *linkOverlay) Variants(series string) []string { return o.base.Variants(series) }
func (o *linkOverlay) Members(series string) []string  { return o.base.Members(series) }
