package audit

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// langmix.go is L-MIX: LANGUAGE MIX, the series whose members state two or more
// primary languages. The shape is a bulk-import artifact - the importer joined a
// same-named series whatever its language, so a German edition's volumes landed in
// the English series of that name (and the reverse) - and it is the audit half of
// the Languages Phase 3 wave-4 membership repair; internal/repair's drop-membership,
// move-membership, split-series and set-work-language ops are the write half. It
// REPLACES S-INTEGRITY's old minority-language review, which named the shape and
// proposed nothing.
//
// Every series is read against ONE keeper language: its derived language
// (model.SeriesLanguage, the strict plurality every reader shares), or for a TIE the
// language a rule picks - the edition decoration the name states ("<name> [German
// Edition]": German keeps the slug), else the INCUMBENT, the half holding the member
// with the earliest added_at (ties by work id). Every member stating another language
// B is a MINORITY membership, and each one is filed under exactly one subclass:
//
//   - ALREADY-HOMED (drop-membership): the work is ALSO a member of another series
//     that derives B. The membership in this series is the misfile; drop it.
//   - TARGET-EXISTS (move-membership): exactly one OTHER series T derives B and is
//     this series under another language - the same name over the base the edition
//     decoration leaves (titlerule.SplitEditionName, then the importer's own series
//     name equality), or a translation_of link either way - and T's slot at the
//     work's position is free (importer.SameSlot). Move the membership there, at the
//     same position. Two or more such T is SEVERAL-TARGETS, a review.
//   - NO-TARGET (split-series): the rest of the B members, as ONE proposal per
//     minority language: they move to a NEW series of the same name (S.Name verbatim,
//     the name those members' claims stated), minted at the importer's chain slug at
//     repair time, positions preserved; the keeper language keeps S's slug.
//
// A proposal is MECHANICAL only where nothing withholds it, and what withholds it is
// named in its reason. The vetoes are the evidence a stated language cannot settle:
//
//   - a TIE decided by incumbency (a rule picked the keeper, a human confirms it);
//   - a CONTESTED majority (contest: a keeper member states a translation, the halves
//     share no author, or the principal author writes mostly in a minority language) -
//     the count names the default keeper, not the series. A contested or tied series'
//     split is also proposed in every OTHER orientation (the other-keeper subclass,
//     always advisory), so a reviewer accepts exactly one in reviewed.json;
//   - a name whose edition decoration states a language other than the keeper;
//   - a COUPLED member: it carries a recording in the keeper language, which is
//     usually why it is in this series at all (English `dune` at `der-wustenplanet`
//     #1, holding the German recording) - relocating that recording is W4.1's pass,
//     and only then is the membership judged;
//   - a member whose NARRATORS contradict its language (check.NarrationProfile, the
//     one rule of record: the narrators' recordings of OTHER works, at least two of
//     them, 80% in one other language) - the work's language may be the error, and
//     narrator evidence may withhold a change but never make one;
//   - a series in an ordering family (a variant, or a primary a variant names): the
//     new series would state no ordering;
//   - a name that slugs away to nothing (no slug to mint the split at);
//   - a record another class proposes to change mechanically in the same audit - a
//     merge-works member, a merge-series member, a membership S-INTEGRITY restates,
//     a slot W-NOSERIES fills - since the two proposals would not commute;
//   - for a split, a doubt about which half the slug names or what the new series
//     would hold (slugVetoes, and a new name stating another language's edition);
//     for a move, a taken or contested slot in the target.
//
// Beside the membership subclasses, NARRATION-CONTRADICTS (set-work-language) names
// the works whose own language is the likely defect: a minority work whose
// recordings all state its language while its narrators contradict it, and - over the
// whole catalogue - a work whose EVERY recording states one other language (the
// Rubinrot shape: work en, recording de) and whose narrators contradict the work's.
// It is ALWAYS advisory: a language is only ever set by a reviewed decision.

// L-MIX subclasses.
const (
	lMixHomed     = "already-homed"
	lMixTarget    = "target-exists"
	lMixTargets   = "several-targets"
	lMixSplit     = "no-target"
	lMixNarration = "narration-contradicts"
	// lMixOtherKeeper is a split's ALTERNATE orientation: another language keeping the
	// slug of a contested (or tied) series, always advisory, so a reviewer accepts
	// exactly one orientation of the series.
	lMixOtherKeeper = "other-keeper"
)

// The CONTEST signals: why which language keeps a majority series' slug is a human
// call (contest, below).
const (
	signalStated    = "stated"
	signalCollision = "collision"
	signalHome      = "home"
)

// fieldPosition and fieldLanguage are the Field a membership op and a language op
// name: the membership's position (what the repair checks the tree still states), and
// the language a split moves out or a work is reset to.
const (
	fieldPosition = "position"
	fieldLanguage = "language"
)

// How a series' keeper language was decided.
const (
	keepMajority   = "majority"
	keepDecoration = "decoration"
	keepIncumbent  = "incumbent"
)

// langMixStats are L-MIX's count-only tallies, the numbers SUMMARY.md reads the class
// in proportion by.
type langMixStats struct {
	MixedSeries          int // series whose members state >= 2 primary languages
	TieSeries            int // ...with no strict plurality
	TieByDecoration      int // ...decided by the name's edition decoration
	Minority             int // memberships outside the keeper language
	Coupled              int // ...carrying a recording in the keeper language
	NarratorContradicted int // ...whose narrators contradict the member's language
	CrossClass           int // ...withheld because another class changes the same record
	Contested            int // majority series whose keeper is contested (any signal)
	ContestedStated      int // ...a keeper-language member states a translation
	ContestedCollision   int // ...the keeper and minority halves share no author
	ContestedHome        int // ...the principal author writes mostly in a minority language
	OtherKeeperSplits    int // alternate-orientation split proposals emitted
	CrossRecordings      int // recordings whose language differs from their work's
	CrossWorks           int // ...over this many works
	AllOther             int // works whose every recording states one other language
	AllOtherContradicted int // ...whose narrators contradict the work's language
}

// mixLocks are the records another class proposes to change MECHANICALLY in this
// audit. An L-MIX proposal touching one is advisory: the two would not commute (a
// merge that retires the work first, a restate that rewrites the position the drop
// is checked against), and the set a repair applies must be order-independent.
type mixLocks struct {
	works       map[string]string // work -> the merge-works record naming it
	series      map[string]string // series -> the merge-series record naming it
	memberships map[string]string // "<series>@<work>" -> the restate-position record
	slots       map[string]string // "<series>@<slot>" -> the add-series-member record
}

// newMixLocks reads the locks off the classes already computed.
func newMixLocks(classes ...*findings) mixLocks {
	l := mixLocks{works: map[string]string{}, series: map[string]string{}, memberships: map[string]string{}, slots: map[string]string{}}
	for _, c := range classes {
		for _, r := range c.rows {
			p := r.Propose
			if p.Advisory {
				continue
			}
			label := c.class + " " + r.Key
			switch p.Op {
			case OpMergeWorks:
				for _, id := range Cluster(p.Target, p.Others) {
					l.works[id] = label
				}
			case OpMergeSeries:
				for _, id := range Cluster(p.Target, p.Others) {
					l.series[id] = label
				}
			case OpRestatePosition:
				if p.Target != "" && p.Series != "" {
					l.memberships[p.Series+"@"+p.Target] = label
				}
			case OpAddSeriesMember:
				if slot := positionKey(p.To); p.Series != "" && slot != "" {
					l.slots[p.Series+"@"+slot] = label
				}
			}
		}
	}
	return l
}

// langMix is one run's L-MIX state: the index, the locks, and the lookups built once.
type langMix struct {
	ix    *index
	locks mixLocks
	prof  *check.NarrationProfile
	// byName buckets series by their name over the edition decoration's base, and
	// neighbours are the translation_of links in either direction: the two ways a
	// series in another language is "this series" for move-membership.
	byName     map[string][]*model.Series
	neighbours map[string][]string
	// primaries are the series some variant names as its ordering_of.
	primaries map[string]bool
	// personLangs is each author's works per primary language over the whole
	// catalogue, the HOME signal's evidence, built on first use.
	personLangs map[string]map[string]int
	f           *findings
	st          langMixStats
	// wantLanguage collects each work's set-work-language candidates (the To it would
	// be reset to, and why), so a work that is a minority in two series is one finding.
	wantLanguage map[string]*languageCandidate
}

// languageCandidate retains narration evidence even when it yields no proposed
// language, so a work appearing in several series is profiled only once.
type languageCandidate struct {
	evidence check.NarrationEvidence
	reasons  map[string]string
	// contested names the contested series the work is a minority member of.
	contested []string
}

func (m *langMix) languageCandidate(w *model.Work) *languageCandidate {
	if c := m.wantLanguage[w.ID]; c != nil {
		return c
	}
	c := &languageCandidate{evidence: m.prof.OfWork(w), reasons: map[string]string{}}
	m.wantLanguage[w.ID] = c
	return c
}

// detectLanguageMix runs L-MIX. locks are the mechanical proposals of the classes
// computed before it.
func detectLanguageMix(ix *index, locks mixLocks) (*findings, langMixStats) {
	m := &langMix{
		ix: ix, locks: locks,
		prof:         check.NewNarrationProfile(ix.cat),
		byName:       map[string][]*model.Series{},
		neighbours:   map[string][]string{},
		primaries:    map[string]bool{},
		f:            &findings{class: ClassLangMix},
		wantLanguage: map[string]*languageCandidate{},
	}
	for _, s := range ix.cat.Series {
		if k := seriesBaseKey(s.Name); k != "" {
			m.byName[k] = append(m.byName[k], s)
		}
		for _, t := range s.TranslationOf {
			m.neighbours[s.ID] = append(m.neighbours[s.ID], t)
			m.neighbours[t] = append(m.neighbours[t], s.ID)
		}
		if s.OrderingOf != "" {
			m.primaries[s.OrderingOf] = true
		}
	}
	series := slices.Clone(ix.cat.Series)
	sort.Slice(series, func(i, j int) bool { return series[i].ID < series[j].ID })
	for _, s := range series {
		m.series(s)
	}
	m.settleContestedMoves()
	m.otherLanguageWorks()
	m.languageFindings()
	return m.f, m.st
}

// seriesBaseKey is a series name's equality key over the base its edition decoration
// leaves: the slug and titlerule.SeriesNameKey, the two halves of the importer's own
// SameSeriesName, so "Throne of Glass" and "Throne of Glass [French Edition]" meet
// and "Throne of Glass (Published Order)" does not. "" for a name with no slug.
func seriesBaseKey(name string) string {
	if b, _, ok := titlerule.SplitEditionName(name); ok {
		name = b
	}
	slug := model.Slugify(name)
	if slug == "" {
		return ""
	}
	return slug + "\x00" + titlerule.SeriesNameKey(name)
}

// mixMember is one minority membership and the evidence about it.
type mixMember struct {
	w        *model.Work
	sw       model.SeriesWork
	coupled  []string // recordings in the keeper language
	ev       check.NarrationEvidence
	contrary bool // the narrators contradict the member's language
	locked   bool // another class changes the work or the membership in this audit
	vetoes   []string
}

// series reads one series.
func (m *langMix) series(s *model.Series) {
	byLang := map[string][]model.SeriesWork{}
	seen := map[string]int{}
	for _, sw := range s.Works {
		w := m.ix.workByID[sw.Work]
		if w == nil {
			continue
		}
		if lang := model.PrimarySubtag(w.Language); lang != "" {
			if at, duplicate := seen[w.ID]; duplicate {
				// Retain the same position sortedMembers would visit first, even
				// when auditing an invalid series that lists one work twice.
				if sw.Position < byLang[lang][at].Position {
					byLang[lang][at] = sw
				}
				continue
			}
			seen[w.ID] = len(byLang[lang])
			byLang[lang] = append(byLang[lang], sw)
		}
	}
	if len(byLang) < 2 {
		return
	}
	m.st.MixedSeries++
	keeper, how := m.ix.seriesLanguage(s), keepMajority
	if keeper == "" {
		m.st.TieSeries++
		keeper, how = m.tieKeeper(s, byLang)
		if how == keepDecoration {
			m.st.TieByDecoration++
		}
	}
	seriesVetoes := m.seriesVetoes(s, byLang, keeper, how)
	var ct contest
	if how == keepMajority {
		ct = m.contest(s, byLang, keeper)
		if ct.contested() {
			seriesVetoes = append(seriesVetoes, ct.veto(s, keeper))
		}
	}
	start := len(m.f.rows)

	for _, lang := range otherLanguages(byLang, keeper) {
		targets := m.targets(s, lang)
		var split []*mixMember
		for _, sw := range sortedMembers(byLang[lang]) {
			mm := m.member(s, sw, keeper, lang)
			m.noteLanguageCandidate(mm, keeper)
			if ct.contested() {
				c := m.languageCandidate(mm.w)
				c.contested = append(c.contested, s.ID+" ("+strings.Join(ct.signals, ", ")+")")
			}
			if homed := m.homedIn(s, mm.w, lang); len(homed) > 0 {
				m.emitDrop(s, mm, homed, keeper, append(slices.Clone(seriesVetoes), mm.vetoes...))
				continue
			}
			switch len(targets) {
			case 0:
				split = append(split, mm)
			case 1:
				m.emitMove(s, mm, targets[0], keeper, append(slices.Clone(seriesVetoes), mm.vetoes...))
			default:
				m.emitSeveral(s, mm, targets, keeper)
			}
		}
		if len(split) > 0 {
			m.emitSplit(s, split, byLang[keeper], keeper, lang, how, seriesVetoes)
		}
	}
	if ct.contested() {
		for i := range m.f.rows[start:] {
			fd := &m.f.rows[start+i]
			fd.Notes = append(fd.Notes, ct.notes...)
		}
	}
	// A contested series and a tie by incumbency are proposed in EVERY orientation:
	// each other language keeping the slug in turn, so a reviewer accepts exactly one
	// (the reviewed-decision consistency check refuses two orientations of one series).
	if ct.contested() || how == keepIncumbent {
		why := ct.veto(s, keeper)
		if how == keepIncumbent {
			why = fmt.Sprintf("the languages of %s tie (%s), so which half keeps its slug is a human decision", s.ID, languageCounts(byLang))
		}
		for _, other := range otherLanguages(byLang, keeper) {
			m.emitOtherKeeper(s, byLang, other, why, ct.notes)
		}
	}
}

// otherLanguages is the languages of byLang other than keeper, sorted.
func otherLanguages(byLang map[string][]model.SeriesWork, keeper string) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for l := range byLang {
			if l != keeper && !yield(l) {
				return
			}
		}
	})
}

// contest is why which language keeps a MAJORITY series' slug is a human call rather
// than the count's: the majority is only mechanical uncontested. The three signals
// each come from a wrong-way split wave 4.2 applied:
//
//   - STATED: a keeper-language member states that it is a translation (statesTranslation:
//     a translation_of link, an own-language edition decoration, a translator credit) -
//     the German Horus Heresy translations kept the slug from their English originals;
//   - COLLISION: no author of a keeper-language member is the same person
//     (samePersonSpelling) as an author of a minority member - two franchises of one
//     name, Amber Auburn's German Zodiac Academy beside Peckham and Valenti's English one;
//   - HOME: the works of the series' principal author (principalAuthors) across the
//     whole catalogue are mostly in a minority language of the series (a strict
//     plurality, personLanguages) - Orphan X's German majority under an author who
//     writes in English.
type contest struct {
	signals []string // in signal order: stated, collision, home
	notes   []string // the evidence, one note per signal
}

func (c contest) contested() bool { return len(c.signals) > 0 }

// veto is the contest as a proposal's veto reason.
func (c contest) veto(s *model.Series, keeper string) string {
	return fmt.Sprintf("which language keeps %s's slug is contested (%s): the %s majority is only the default, and every "+
		"orientation is proposed for review", s.ID, strings.Join(c.signals, ", "), keeper)
}

// contest reads the three signals over one majority series.
func (m *langMix) contest(s *model.Series, byLang map[string][]model.SeriesWork, keeper string) contest {
	var c contest
	var stating, keptAuthors, minorityAuthors []string
	for _, w := range m.worksOf(byLang[keeper]) {
		if statesTranslation(w) {
			stating = append(stating, w.ID)
		}
		keptAuthors = append(keptAuthors, w.Authors...)
	}
	minority := otherLanguages(byLang, keeper)
	for _, lang := range minority {
		for _, w := range m.worksOf(byLang[lang]) {
			minorityAuthors = append(minorityAuthors, w.Authors...)
		}
	}
	keptAuthors, minorityAuthors = sortedUnique(keptAuthors), sortedUnique(minorityAuthors)
	if stating = sortedUnique(stating); len(stating) > 0 {
		m.st.ContestedStated++
		c.signals = append(c.signals, signalStated)
		c.notes = append(c.notes, fmt.Sprintf("contested, %s: %s of the %s members state a translation (%s)",
			signalStated, joinCount(len(stating), "work"), keeper, truncateList(stating, 6)))
	}
	if len(keptAuthors) > 0 && len(minorityAuthors) > 0 && len(sharedAuthors(m.ix, keptAuthors, minorityAuthors)) == 0 {
		m.st.ContestedCollision++
		c.signals = append(c.signals, signalCollision)
		c.notes = append(c.notes, fmt.Sprintf("contested, %s: the %s members (by %s) share no author with the %s members (by %s): "+
			"two series of one name", signalCollision, keeper, truncateList(keptAuthors, 4), strings.Join(minority, ", "),
			truncateList(minorityAuthors, 4)))
	}
	principal := m.principalAuthors(byLang)
	if home, counts := m.homeLanguage(principal); home != "" && home != keeper && len(byLang[home]) > 0 {
		m.st.ContestedHome++
		c.signals = append(c.signals, signalHome)
		c.notes = append(c.notes, fmt.Sprintf("contested, %s: the works of the series' principal author (%s) across the catalogue "+
			"are mostly %s (%s), a minority language here", signalHome, truncateList(principal, 4), home, counts))
	}
	if c.contested() {
		m.st.Contested++
	}
	return c
}

// collectiveCredits are the canonical records a nameless credit folds onto
// (internal/importer/collective.go, plus the catch-all `person` and the synthetic
// `virtual-voice`): every language's books credit them, so their works say nothing
// about where an author writes. A record whose kind is set (a group, a publisher, a
// synthetic voice) is left out for the same reason.
var collectiveCredits = map[string]bool{
	"anonymous": true, "full-cast": true, "person": true, "uncredited": true, "unknown": true, "various": true,
	"virtual-voice": true,
}

// personLanguages is each individual author's works per primary language over the
// whole catalogue, built once per run on first use. A collective or a classified
// record has no entry.
func (m *langMix) personLanguages() map[string]map[string]int {
	if m.personLangs != nil {
		return m.personLangs
	}
	m.personLangs = map[string]map[string]int{}
	for _, w := range m.ix.cat.Works {
		lang := model.PrimarySubtag(w.Language)
		if lang == "" {
			continue
		}
		for _, a := range sortedUnique(w.Authors) {
			if collectiveCredits[a] {
				continue
			}
			if p := m.ix.personByID[a]; p != nil && p.Kind != "" {
				continue
			}
			if m.personLangs[a] == nil {
				m.personLangs[a] = map[string]int{}
			}
			m.personLangs[a][lang]++
		}
	}
	return m.personLangs
}

// principalAuthors is the individual authors credited on the most members of the
// series, ties all kept. Not every author: a translation credits its translators as
// authors often enough (Eddie Flynn's German volume names two) that their all-German
// catalogues outvote the author whose series it is.
func (m *langMix) principalAuthors(byLang map[string][]model.SeriesWork) []string {
	langs := m.personLanguages()
	credits := map[string]int{}
	for _, members := range byLang {
		for _, w := range m.worksOf(members) {
			for _, a := range sortedUnique(w.Authors) {
				if langs[a] != nil {
					credits[a]++
				}
			}
		}
	}
	best := 0
	for _, n := range credits {
		best = max(best, n)
	}
	var out []string
	for _, a := range sortedKeys(credits) {
		if credits[a] == best {
			out = append(out, a)
		}
	}
	return out
}

// homeLanguage is the strict plurality of the authors' works per language summed over
// them, "" on a tie or with no evidence, and the counts rendered "en 40, de 12".
func (m *langMix) homeLanguage(authors []string) (string, string) {
	langs := m.personLanguages()
	sum := map[string]int{}
	for _, a := range authors {
		for l, n := range langs[a] {
			sum[l] += n
		}
	}
	home, best, tied := "", 0, false
	for _, l := range sortedKeys(sum) {
		switch n := sum[l]; {
		case n > best:
			home, best, tied = l, n, false
		case n == best:
			tied = true
		}
	}
	if tied {
		return "", ""
	}
	return home, evidenceText(check.NarrationEvidence{Counts: sum})
}

// tieKeeper decides which language keeps a tied series' slug: the language its name's
// edition decoration states, when a member states it; else the incumbent's - the
// member with the earliest added_at, a record with none sorting last, ties by work id.
func (m *langMix) tieKeeper(s *model.Series, byLang map[string][]model.SeriesWork) (string, string) {
	if _, lang, ok := titlerule.SplitEditionName(s.Name); ok && len(byLang[lang]) > 0 {
		return lang, keepDecoration
	}
	var first *model.Work
	for _, members := range byLang {
		for _, sw := range members {
			w := m.ix.workByID[sw.Work]
			if first == nil || addedBefore(w, first) {
				first = w
			}
		}
	}
	return model.PrimarySubtag(first.Language), keepIncumbent
}

// addedBefore orders two works by added_at chronologically (build.TimeKey, the
// artifact's own comparison), an unstated one last, then by id.
func addedBefore(a, b *model.Work) bool {
	ka, kb := build.TimeKey(a.AddedAt), build.TimeKey(b.AddedAt)
	if (ka == "") != (kb == "") {
		return ka != ""
	}
	if ka != kb {
		return ka < kb
	}
	return a.ID < b.ID
}

// seriesVetoes are the reasons no proposal over this series is mechanical, whichever
// member it moves.
func (m *langMix) seriesVetoes(s *model.Series, byLang map[string][]model.SeriesWork, keeper, how string) []string {
	var out []string
	if how == keepIncumbent {
		var first *model.Work
		for _, sw := range byLang[keeper] {
			if w := m.ix.workByID[sw.Work]; first == nil || addedBefore(w, first) {
				first = w
			}
		}
		out = append(out, fmt.Sprintf("the languages of %s tie (%s): the incumbency rule keeps its slug for %s, the half "+
			"holding the member added first (%s, %s), and which half the series is has to be confirmed by hand",
			s.ID, languageCounts(byLang), keeper, first.ID, orDash(first.AddedAt)))
	}
	if _, lang, ok := titlerule.SplitEditionName(s.Name); ok && lang != keeper {
		out = append(out, fmt.Sprintf("the name %q states the %s edition, but %s keeps the slug", s.Name, lang, keeper))
	}
	if s.Ordering != "" || s.OrderingOf != "" || m.primaries[s.ID] {
		out = append(out, fmt.Sprintf("%s is in an ordering family (ordering %q, ordering_of %q, a primary: %v), and the "+
			"series a member moves to would state no ordering", s.ID, s.Ordering, s.OrderingOf, m.primaries[s.ID]))
	}
	if by, locked := m.locks.series[s.ID]; locked {
		out = append(out, fmt.Sprintf("%s is merged by %s in this audit", s.ID, by))
	}
	return out
}

// member gathers the evidence about one minority membership, and counts it.
func (m *langMix) member(s *model.Series, sw model.SeriesWork, keeper, lang string) *mixMember {
	mm := m.memberEvidence(s, sw, keeper, lang)
	m.st.Minority++
	if len(mm.coupled) > 0 {
		m.st.Coupled++
	}
	if mm.contrary {
		m.st.NarratorContradicted++
	}
	if mm.locked {
		m.st.CrossClass++
	}
	return mm
}

// memberEvidence is member without the tallies: an alternate orientation reads the
// same evidence against another keeper and must not count the membership twice.
func (m *langMix) memberEvidence(s *model.Series, sw model.SeriesWork, keeper, lang string) *mixMember {
	w := m.ix.workByID[sw.Work]
	mm := &mixMember{w: w, sw: sw, ev: m.languageCandidate(w).evidence}
	for _, r := range w.Recordings {
		if model.PrimarySubtag(r.Language) == keeper {
			mm.coupled = append(mm.coupled, r.ID)
		}
	}
	slices.Sort(mm.coupled)
	if len(mm.coupled) > 0 {
		mm.vetoes = append(mm.vetoes, fmt.Sprintf("%s carries a recording in %s (%s): relocating that recording to a work in "+
			"its own language comes first, and the membership is judged after", w.ID, keeper, strings.Join(mm.coupled, ", ")))
	}
	if mm.ev.Contradicts(lang) {
		mm.contrary = true
		mm.vetoes = append(mm.vetoes, fmt.Sprintf("the narrators of %s record in %s (%s): its stated %s may be the error",
			w.ID, mm.ev.Dominant(), evidenceText(mm.ev), w.Language))
	}
	if by, locked := m.locks.works[w.ID]; locked {
		mm.locked = true
		mm.vetoes = append(mm.vetoes, fmt.Sprintf("%s is merged by %s in this audit", w.ID, by))
	}
	if by, locked := m.locks.memberships[s.ID+"@"+w.ID]; locked {
		mm.locked = true
		mm.vetoes = append(mm.vetoes, fmt.Sprintf("the membership's position is restated by %s in this audit", by))
	}
	return mm
}

// homedIn is the other series holding the work that derive its language, sorted.
func (m *langMix) homedIn(s *model.Series, w *model.Work, lang string) []string {
	var out []string
	for _, ms := range m.ix.memberships[w.ID] {
		if ms.series == s.ID {
			continue
		}
		if o := m.ix.seriesByID[ms.series]; o != nil && m.ix.seriesLanguage(o) == lang {
			out = append(out, o.ID)
		}
	}
	return sortedUnique(out)
}

// targets is every OTHER series deriving lang that is this series under another
// language: the same name over the decoration's base, or a translation_of link either
// way. Sorted by id.
func (m *langMix) targets(s *model.Series, lang string) []*model.Series {
	set := map[string]*model.Series{}
	for _, t := range m.byName[seriesBaseKey(s.Name)] {
		set[t.ID] = t
	}
	for _, id := range m.neighbours[s.ID] {
		if t := m.ix.seriesByID[id]; t != nil {
			set[id] = t
		}
	}
	var out []*model.Series
	for _, id := range sortedKeys(set) {
		if t := set[id]; id != s.ID && m.ix.seriesLanguage(t) == lang {
			out = append(out, t)
		}
	}
	return out
}

// memberFinding is the record shape every membership subclass shares.
func (m *langMix) memberFinding(sub string, s *model.Series, mm *mixMember, keeper string, others ...*model.Series) Finding {
	refs := []SeriesRef{m.ix.seriesRef(s)}
	for _, o := range others {
		refs = append(refs, m.ix.seriesRef(o))
	}
	fd := Finding{
		Subclass: sub,
		Key:      s.ID + "@" + mm.w.ID,
		Works:    []WorkRef{m.ix.workRef(mm.w, "")},
		Series:   refs,
	}
	fd.Notes = []string{fmt.Sprintf("%s states %s in %s, whose members are read against %s", mm.w.ID, mm.w.Language, s.ID, keeper)}
	if mm.ev.Total > 0 {
		fd.Notes = append(fd.Notes, "the narrators' recordings of other works: "+evidenceText(mm.ev))
	}
	return fd
}

func (m *langMix) emitDrop(s *model.Series, mm *mixMember, homed []string, keeper string, vetoes []string) {
	var homes []*model.Series
	for _, id := range homed {
		homes = append(homes, m.ix.seriesByID[id])
		// A home a merge retires in this audit is no home once that merge lands, so the
		// drop would apply or go stale by the order the two are run in.
		if by, locked := m.locks.series[id]; locked {
			vetoes = append(vetoes, fmt.Sprintf("%s is merged by %s in this audit", id, by))
		}
	}
	fd := m.memberFinding(lMixHomed, s, mm, keeper, homes...)
	fd.Propose = Proposal{
		Op: OpDropMembership, Target: mm.w.ID, Series: s.ID, Others: homed,
		Field: fieldPosition, From: mm.sw.Position,
		Reason: fmt.Sprintf("%s is also a member of %s, which derives its language", mm.w.ID, strings.Join(homed, ", ")),
	}
	settleMix(&fd, vetoes)
	m.f.add(fd)
}

func (m *langMix) emitMove(s *model.Series, mm *mixMember, t *model.Series, keeper string, vetoes []string) {
	fd := m.memberFinding(lMixTarget, s, mm, keeper, t)
	pos := mm.sw.Position
	fd.Propose = Proposal{
		Op: OpMoveMembership, Target: mm.w.ID, Series: s.ID, Others: []string{t.ID},
		Field: fieldPosition, From: pos, To: pos,
		Reason: fmt.Sprintf("%s is %s in %s, the one series of this name or translation link that derives it",
			t.ID, s.ID, m.ix.seriesLanguage(t)),
	}
	if norm, ok := importer.NormalizeSequence(pos); !ok || norm != pos {
		vetoes = append(vetoes, fmt.Sprintf("the position %q is not a canonical one to write into %s", pos, t.ID))
	}
	for _, sw := range t.Works {
		if importer.SameSlot(sw.Position, pos) {
			vetoes = append(vetoes, fmt.Sprintf("position %q of %s is held by %s", pos, t.ID, sw.Work))
			break
		}
	}
	if slot := positionKey(pos); slot != "" {
		if by, locked := m.locks.slots[t.ID+"@"+slot]; locked {
			vetoes = append(vetoes, fmt.Sprintf("position %q of %s is filled by %s in this audit", pos, t.ID, by))
		}
	}
	if by, locked := m.locks.series[t.ID]; locked {
		vetoes = append(vetoes, fmt.Sprintf("%s is merged by %s in this audit", t.ID, by))
	}
	settleMix(&fd, vetoes)
	m.f.add(fd)
}

func (m *langMix) emitSeveral(s *model.Series, mm *mixMember, targets []*model.Series, keeper string) {
	fd := m.memberFinding(lMixTargets, s, mm, keeper, targets...)
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	fd.Propose = Proposal{
		Op: OpReview, Target: mm.w.ID, Series: s.ID, Others: ids, Advisory: true,
		Reason: fmt.Sprintf("%d series derive %s and are this series under another language (%s): which one the work "+
			"belongs in is a human decision", len(ids), model.PrimarySubtag(mm.w.Language), strings.Join(ids, ", ")),
	}
	m.f.add(fd)
}

func (m *langMix) emitSplit(s *model.Series, members []*mixMember, keepers []model.SeriesWork, keeper, lang, how string, seriesVetoes []string) {
	fd, vetoes := m.splitFinding(s, members, keepers, keeper, lang, how, seriesVetoes)
	settleMix(&fd, vetoes)
	m.f.add(fd)
}

// emitOtherKeeper proposes, for a contested or tied series, the orientation in which
// keeper (not the language the class reads the series against) keeps the slug: one
// split per other language, of its members the majority reading would split too -
// not those already homed in a series of their language, and no split at all for a
// language that has a target series (its members would move, not found a series).
// Always advisory: it exists to be accepted, or not, by a reviewed decision.
func (m *langMix) emitOtherKeeper(s *model.Series, byLang map[string][]model.SeriesWork, keeper, why string, notes []string) {
	for _, lang := range otherLanguages(byLang, keeper) {
		if len(m.targets(s, lang)) > 0 {
			continue
		}
		var members []*mixMember
		for _, sw := range sortedMembers(byLang[lang]) {
			if w := m.ix.workByID[sw.Work]; len(m.homedIn(s, w, lang)) == 0 {
				members = append(members, m.memberEvidence(s, sw, keeper, lang))
			}
		}
		if len(members) == 0 {
			continue
		}
		fd, vetoes := m.splitFinding(s, members, byLang[keeper], keeper, lang, "the other orientation", nil)
		fd.Subclass = lMixOtherKeeper
		fd.Key = s.ID + "/" + lang + "/keep-" + keeper
		fd.Notes = append(fd.Notes, notes...)
		fd.Propose.Advisory = true
		fd.Propose.Reason = "the other orientation, for review: " + why
		if vetoes = sortedUnique(vetoes); len(vetoes) > 0 {
			fd.Propose.Reason += "; were it chosen, still to confirm: " + truncateList(vetoes, 3)
		}
		m.st.OtherKeeperSplits++
		m.f.add(fd)
	}
}

// splitFinding is the split-series record moving members (stating lang) out of s while
// keeper keeps its slug, and the vetoes standing against it.
func (m *langMix) splitFinding(s *model.Series, members []*mixMember, keepers []model.SeriesWork, keeper, lang, how string, seriesVetoes []string) (Finding, []string) {
	vetoes := append(slices.Clone(seriesVetoes), m.slugVetoes(s, members, keepers, keeper)...)
	works := make([]*model.Work, 0, len(members))
	ids := make([]string, 0, len(members))
	positions := make([]string, 0, len(members))
	for _, mm := range members {
		works = append(works, mm.w)
		ids = append(ids, mm.w.ID)
		positions = append(positions, mm.w.ID+"@"+mm.sw.Position)
		vetoes = append(vetoes, mm.vetoes...)
	}
	slug := importer.FreeSeriesSlug(s.Name, func(id string) bool { return m.ix.seriesByID[id] != nil }, m.ix.cat.Redirects)
	if slug == "" {
		vetoes = append(vetoes, fmt.Sprintf("the name %q slugs away to nothing, so a series of that name has no slug to be minted at", s.Name))
	}
	if _, d, ok := titlerule.SplitEditionName(s.Name); ok && d != lang {
		// The new series carries S.Name verbatim, and a name stating the German edition
		// on a series of English works is a false statement a human has to rename.
		vetoes = append(vetoes, fmt.Sprintf("the new series would be named %q, the %s edition, while holding %s works", s.Name, d, lang))
	}
	refs := make([]WorkRef, 0, len(works))
	for _, w := range works {
		refs = append(refs, m.ix.workRef(w, ""))
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	fd := Finding{
		Subclass: lMixSplit,
		Key:      s.ID + "/" + lang,
		Works:    refs,
		Series:   []SeriesRef{m.ix.seriesRef(s)},
		Propose: Proposal{
			Op: OpSplitSeries, Target: s.ID, Others: sortedUnique(ids),
			Field: fieldLanguage, From: keeper, To: lang,
			Reason: fmt.Sprintf("no series of this name or translation link derives %s: its members move to a new series of "+
				"the same name, and %s keeps the slug", lang, keeper),
		},
	}
	fd.Notes = []string{
		fmt.Sprintf("%s keeps %s (%s); the %s members move to a new series named %q, at %s as the tree stands (the repair "+
			"re-derives the slug)", keeper, s.ID, how, lang, s.Name, orDash(slug)),
		"moving, positions preserved: " + truncateList(sortedUnique(positions), 12),
	}
	if len(s.TranslationOf) > 0 || len(m.neighbours[s.ID]) > 0 {
		fd.Notes = append(fd.Notes, fmt.Sprintf("%s's translation links stay on %s; the new series states none", s.ID, s.ID))
	}
	return fd, vetoes
}

// slugVetoes are the reasons a split's two halves are not simply one franchise in two
// languages, each found reviewing the tree's own mixed series. A split separates the
// halves; what it decides beside that is which half the public slug names and what the
// new series holds, and the majority only settles both when the halves are one
// franchise with the original keeping the slug:
//
//   - the halves share NO author (samePersonSpelling, so a forked person record is no
//     miss): two series of one name - Peckham and Valenti's English Zodiac Academy
//     beside Amber Auburn's German one - where a count of members is no claim to the
//     name, which is the series-hijack shape;
//   - the MOVING half is itself several author groups: the new series would found
//     that hijack anew ("Alien": Tracy Lauren's romance beside Tim Lebbon's novel);
//   - the moving half was catalogued a DAY before any keeper member: the majority is
//     the later arrival;
//   - the keeper half STATES that it is a translation (an own-language edition
//     decoration, a translator credit, a translation_of link) and the moving half
//     states nothing of the kind: the English Horus Heresy originals would leave the
//     slug to their German editions.
func (m *langMix) slugVetoes(s *model.Series, members []*mixMember, keepers []model.SeriesWork, keeper string) []string {
	var moving, kept []string
	movingStates := 0
	for _, mm := range members {
		moving = append(moving, mm.w.Authors...)
		if statesTranslation(mm.w) {
			movingStates++
		}
	}
	keptStates := 0
	for _, sw := range keepers {
		if w := m.ix.workByID[sw.Work]; w != nil {
			kept = append(kept, w.Authors...)
			if statesTranslation(w) {
				keptStates++
			}
		}
	}
	var out []string
	if len(sharedAuthors(m.ix, sortedUnique(moving), sortedUnique(kept))) == 0 {
		out = append(out, fmt.Sprintf("the members moving out share no author with the %s members of %s: two series of one "+
			"name, where the larger is no claim to the slug", keeper, s.ID))
	}
	if first, keptFirst := earliest(membersWorks(members)), earliest(m.worksOf(keepers)); first != nil && keptFirst != nil &&
		addedDay(first) != "" && (addedDay(keptFirst) == "" || addedDay(first) < addedDay(keptFirst)) {
		out = append(out, fmt.Sprintf("the members moving out were catalogued first (%s on %s, the %s half's first on %s): "+
			"the majority is the later arrival", first.ID, first.AddedAt, keeper, orDash(keptFirst.AddedAt)))
	}
	if groups := authorGroups(m.ix, members); groups > 1 {
		out = append(out, fmt.Sprintf("the members moving out fall into %d groups that share no author: the new series "+
			"would hold %d series of one name", groups, groups))
	}
	if keptStates > 0 && movingStates == 0 {
		out = append(out, fmt.Sprintf("%s of %s's %s members state a translation and none of the members moving out do: "+
			"the originals would leave the slug to their translations", joinCount(keptStates, "work"), s.ID, keeper))
	}
	return out
}

// authorGroups is how many groups the members fall into when two members sharing an
// author (samePersonSpelling) are one group - the moving half of "Alien" is Tracy
// Lauren's romance beside Tim Lebbon's franchise novel, two series the split would
// found as one.
func authorGroups(ix *index, members []*mixMember) int {
	groups := newUnionFind(len(members))
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			if len(sharedAuthors(ix, sortedUnique(members[i].w.Authors), sortedUnique(members[j].w.Authors))) > 0 {
				groups.union(i, j)
			}
		}
	}
	n := 0
	for i := range groups {
		if groups.find(i) == i {
			n++
		}
	}
	return n
}

// addedDay is the UTC day a work was catalogued on, "" when it states none. The
// incumbency veto compares DAYS: one bulk wave stamps a whole series within a day,
// and an order inside it says nothing about which half the series was.
func addedDay(w *model.Work) string {
	if k := build.TimeKey(w.AddedAt); len(k) >= len("2006-01-02") {
		return k[:len("2006-01-02")]
	}
	return ""
}

// earliest is the work added first (addedBefore), or nil for none.
func earliest(ws []*model.Work) *model.Work {
	var first *model.Work
	for _, w := range ws {
		if first == nil || addedBefore(w, first) {
			first = w
		}
	}
	return first
}

func membersWorks(members []*mixMember) []*model.Work {
	out := make([]*model.Work, 0, len(members))
	for _, mm := range members {
		out = append(out, mm.w)
	}
	return out
}

func (m *langMix) worksOf(sws []model.SeriesWork) []*model.Work {
	out := make([]*model.Work, 0, len(sws))
	for _, sw := range sws {
		if w := m.ix.workByID[sw.Work]; w != nil {
			out = append(out, w)
		}
	}
	return out
}

// statesTranslation reports whether a work states that it is a translation: an
// own-language edition decoration, a translator credit (isStatedTranslation), or a
// translation_of link.
func statesTranslation(w *model.Work) bool { return isStatedTranslation(w) || len(w.TranslationOf) > 0 }

// settleContestedMoves withholds the moves that contend with each other: two series
// moving works into one slot of one target, or one work into one target twice (it is a
// minority in two series that both have it). Which one lands depends on the order they
// are applied in, and the set a repair applies must not.
func (m *langMix) settleContestedMoves() {
	settleContestedClaims(m.f, func(p Proposal) []string {
		if p.Op != OpMoveMembership || p.Advisory {
			return nil
		}
		dest := p.Others[0]
		keys := []string{dest + "@" + p.Target}
		if slot := positionKey(p.To); slot != "" {
			keys = append(keys, dest+"#"+slot)
		}
		return keys
	}, func(fd *Finding, claimants []string) {
		settleMix(fd, []string{"another move in this audit claims the same place in " +
			fd.Propose.Others[0] + " (" + strings.Join(claimants, ", ") + ")"})
	})
}

// settleMix turns a proposal advisory when anything vetoes it.
func settleMix(fd *Finding, vetoes []string) {
	vetoes = sortedUnique(vetoes)
	if len(vetoes) == 0 {
		return
	}
	fd.Propose.Advisory = true
	fd.Propose.Reason = "a human should confirm: " + truncateList(vetoes, 4)
}

// noteLanguageCandidate records a minority member whose recordings all state its own
// language while its narrators name the keeper's: the work language, not the
// membership, is the likely defect.
func (m *langMix) noteLanguageCandidate(mm *mixMember, keeper string) {
	if !mm.contrary {
		return
	}
	lang, _ := recordingLanguage(mm.w)
	if lang == "" || model.PrimarySubtag(lang) != model.PrimarySubtag(mm.w.Language) {
		return // the Rubinrot shape, or mixed recordings: otherLanguageWorks and W4.1 own those
	}
	why := fmt.Sprintf("a minority member of a %s series whose narrators record in %s (%s)", keeper, mm.ev.Dominant(), evidenceText(mm.ev))
	m.wantLanguageFor(mm.w.ID, keeper, why)
}

func (m *langMix) wantLanguageFor(work, to, why string) {
	want := m.wantLanguage[work].reasons
	if _, have := want[to]; !have {
		want[to] = why
	}
}

// recordingLanguage is the one language every recording of a work states - the exact
// tag when they all spell it alike, else the primary subtag - or "" when they state
// several (or the work has none).
func recordingLanguage(w *model.Work) (string, bool) {
	if len(w.Recordings) == 0 {
		return "", false
	}
	exact, primary := w.Recordings[0].Language, model.PrimarySubtag(w.Recordings[0].Language)
	same := true
	for _, r := range w.Recordings[1:] {
		if model.PrimarySubtag(r.Language) != primary {
			return "", false
		}
		same = same && r.Language == exact
	}
	if primary == "" {
		return "", false
	}
	if same {
		return exact, true
	}
	return primary, true
}

// otherLanguageWorks walks the whole catalogue for the works whose recordings state
// a language their work does not: the count W4.1's relocation pass works from, and the
// Rubinrot shape - every recording in one other language - whose narrators, where they
// contradict the work's language too, make a set-work-language review.
func (m *langMix) otherLanguageWorks() {
	for _, w := range m.ix.cat.Works {
		wl := model.PrimarySubtag(w.Language)
		cross := 0
		for _, r := range w.Recordings {
			if model.PrimarySubtag(r.Language) != wl {
				cross++
			}
		}
		if cross == 0 {
			continue
		}
		m.st.CrossRecordings += cross
		m.st.CrossWorks++
		lang, _ := recordingLanguage(w)
		if lang == "" || model.PrimarySubtag(lang) == wl {
			continue
		}
		m.st.AllOther++
		ev := m.languageCandidate(w).evidence
		if !ev.Contradicts(w.Language) {
			continue
		}
		m.st.AllOtherContradicted++
		m.wantLanguageFor(w.ID, lang, fmt.Sprintf("every recording states %s and the narrators record in %s (%s)",
			lang, ev.Dominant(), evidenceText(ev)))
	}
}

// languageFindings emits one narration-contradicts record per work, in work order.
func (m *langMix) languageFindings() {
	for _, id := range sortedKeys(m.wantLanguage) {
		w := m.ix.workByID[id]
		want := m.wantLanguage[id].reasons
		if len(want) == 0 {
			continue
		}
		tos := sortedKeys(want)
		ev := m.languageCandidate(w).evidence
		fd := Finding{
			Subclass: lMixNarration,
			Key:      id,
			Works:    []WorkRef{m.ix.workRef(w, "")},
		}
		for _, to := range tos {
			fd.Notes = append(fd.Notes, "-> "+to+": "+want[to])
		}
		if c := m.wantLanguage[id].contested; len(c) > 0 {
			fd.Notes = append(fd.Notes, "a minority member of a series whose keeper language is contested: "+strings.Join(sortedUnique(c), "; "))
		}
		dominant := ev.Dominant()
		switch {
		case len(tos) != 1:
			fd.Propose = Proposal{Op: OpReview, Target: id, Field: fieldLanguage, From: w.Language, Advisory: true,
				Reason: "the evidence names several languages this work could be (" + strings.Join(tos, ", ") + "); a human decides"}
		case model.PrimarySubtag(tos[0]) != dominant:
			fd.Propose = Proposal{Op: OpReview, Target: id, Field: fieldLanguage, From: w.Language, Advisory: true,
				Reason: fmt.Sprintf("the stated evidence names %s but the narrators record in %s; a human decides", tos[0], dominant)}
		default:
			fd.Propose = Proposal{Op: OpSetWorkLanguage, Target: id, Field: fieldLanguage, From: w.Language, To: tos[0],
				Advisory: true,
				Reason: "never inferred: narrator evidence can withhold a change but never make one, so a reviewed " +
					"decision is what applies this"}
		}
		m.f.add(fd)
	}
}

// languageCounts renders a series' member languages as "de 2, en 2", sorted.
func languageCounts(byLang map[string][]model.SeriesWork) string {
	parts := make([]string, 0, len(byLang))
	for _, l := range sortedKeys(byLang) {
		parts = append(parts, fmt.Sprintf("%s %d", l, len(byLang[l])))
	}
	return strings.Join(parts, ", ")
}

// evidenceText renders narration evidence as "de 12, en 1", most first, then by tag.
func evidenceText(ev check.NarrationEvidence) string {
	langs := sortedKeys(ev.Counts)
	sort.SliceStable(langs, func(i, j int) bool { return cmp.Compare(ev.Counts[langs[j]], ev.Counts[langs[i]]) < 0 })
	parts := make([]string, 0, len(langs))
	for _, l := range langs {
		parts = append(parts, fmt.Sprintf("%s %d", l, ev.Counts[l]))
	}
	return strings.Join(parts, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
