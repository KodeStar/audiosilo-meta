package audit

import (
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// SER-DUP / SER-PAREN subclasses.
const (
	serDupName   = "normalized-name" // the names differ only by case, diacritics, articles or a decoration suffix
	serDupDecor  = "same-decoration" // ...the members of a vetoed normalized-name group sharing ONE decoration
	serDupSaga   = "suffix-saga"     // ...and the only difference is a trailing " Saga"
	serParenSolo = "decorated-only"  // a parenthetical-decorated name with no undecorated sibling
	serParenPair = "decorated-pair"  // ...with one, which may well be a deliberate second ordering
)

// seriesKeys is one series' two comparison keys plus whether its name carries a
// parenthetical and what that parenthetical says. SER-DUP, SER-PAREN and T-LINK's
// series-edition half read it, and it is computed ONCE per series: the keys fold
// through model.Slugify (NFD normalization plus a builder) and were being computed four
// times over 45k series across two detectors and their tie-breaks.
type seriesKeys struct {
	series *model.Series
	tight  string
	saga   string
	paren  bool
	// decor is titlerule.DecorationKey of the name: what the parenthetical SAYS, with
	// the bracket style, spacing and case folded away. Empty for an undecorated name
	// and for a paren name whose groups say nothing comparable (an unclosed bracket),
	// which decorClass keeps apart from every other member.
	decor string
}

// seriesKeyIndex is every series' keys, in catalogue order.
func seriesKeyIndex(all []*model.Series) []seriesKeys {
	out := make([]seriesKeys, 0, len(all))
	for _, s := range all {
		out = append(out, seriesKeys{
			series: s,
			tight:  titlerule.SeriesKey(s.Name),
			saga:   titlerule.SeriesSagaKey(s.Name),
			paren:  strings.ContainsAny(s.Name, "(["),
			decor:  titlerule.DecorationKey(s.Name),
		})
	}
	return out
}

// detectSeriesDup groups series whose names are the same name spelled two ways.
// clustersOf is W-DUP's work -> cluster keys, which a family review names.
func detectSeriesDup(ix *index, keys []seriesKeys, clustersOf map[string][]string) *findings {
	f := &findings{class: ClassSeriesDup}

	const foldReason = "fold the members onto the canonical name, then delete the empty spelling"
	byTight, tightOrder := groupBy(keys, func(k seriesKeys) string { return k.tight })
	for _, key := range tightOrder {
		group := byTight[key]
		if len(group) < 2 {
			continue
		}
		fd := seriesDupFinding(ix, serDupName, key, group, foldReason)
		f.add(fd)
		if !fd.Propose.Advisory {
			continue // the whole group folds onto one survivor, so no part of it needs a proposal of its own
		}
		// SAME-DECORATION SUBGROUPS. The tight key strips parentheticals, so "Throne of
		// Glass" (en), "Throne of Glass[French Edition]", "Throne of Glass [French
		// Edition]" and "Throne of Glass[German Edition]" are ONE group - and that group
		// is rightly vetoed, since a plain series and its language editions are not one
		// series. But the two French spellings are one series spelled twice, and a veto
		// over the whole group withheld that fold too. So the members that carry ONE
		// decoration are proposed again as a group of their own, judged by every veto
		// exactly as the whole group was (a same-decoration pair in two languages still
		// stops on the language veto), while the whole group's record is left as it was.
		//
		// Sub-grouping rather than a veto that only fires ACROSS differing decorations,
		// because a merge-series names ONE target for the whole group: the canonical
		// choice over {plain, fr, fr-2, de} is the plain series, and no veto adjustment
		// can turn "fold everything onto the English series" into "fold fr-2 onto fr".
		// The subgroup gets its own survivor from the same SeriesRank ladder, its own
		// key (the group's key plus the decoration, so a report or --only file addresses
		// it apart from the group) and its own subclass. It is emitted only under a
		// vetoed group, which is what keeps the non-advisory set consistent: a mechanical
		// whole-group fold would already fold these members onto a different survivor.
		claimed := map[string]bool{}
		for _, sub := range sameDecorationSubgroups(group) {
			sfd := seriesDupFinding(ix, serDupDecor, key+"["+sub[0].decor+"]", sub, foldReason)
			f.add(sfd)
			if !sfd.Propose.Advisory {
				for _, k := range sub {
					claimed[k.series.ID] = true
				}
			}
		}
		// FAMILY FOLDS (seriesfamily.go): a group withheld for holding a reading-order
		// family or a translation may still hold plain spellings of ONE family member,
		// each proposed apart - never a series a mechanical subgroup above already folds.
		// The family-member lookup is the cheap gate: without one there is no target.
		if !hasFamilyMember(ix, group) {
			continue
		}
		sides := seriesSides(ix, group)
		if !familyVetoed(group, sides) {
			continue
		}
		byID := make(map[string]seriesSide, len(sides))
		for _, sd := range sides {
			byID[sd.series.ID] = sd
		}
		for _, ffd := range familySpellingFolds(ix, key, group, byID, claimed, clustersOf) {
			f.add(ffd)
		}
	}

	// The saga key is strictly looser, so it only ever adds groups the tight key
	// did not already report - and it proposes nothing, because a trailing "Saga"
	// is usually part of the name.
	bySaga, sagaOrder := groupBy(keys, func(k seriesKeys) string { return k.saga })
	for _, key := range sagaOrder {
		group := bySaga[key]
		if len(group) < 2 || allShareKey(group, func(k seriesKeys) string { return k.tight }) {
			continue
		}
		f.add(seriesDupFinding(ix, serDupSaga, key, group,
			`the names differ only by a trailing "Saga", which is part of a real series name at least as often as it is `+
				"catalogue decoration - no merge is proposed"))
	}
	return f
}

func seriesDupFinding(ix *index, sub, key string, group []seriesKeys, reason string) Finding {
	fd := Finding{Subclass: sub, Key: key}
	var names, ids []string
	best, bestRank := group[0].series, titlerule.SeriesRank{}
	for i, k := range group {
		fd.Series = append(fd.Series, ix.seriesRef(k.series))
		names = append(names, k.series.Name)
		ids = append(ids, k.series.ID)
		// Through titlerule's ladder, so the report and a repair pass agree on
		// which spelling survives.
		r := titlerule.SeriesRank{Works: len(k.series.Works), ID: k.series.ID}
		if i == 0 || r.Better(bestRank) {
			best, bestRank = k.series, r
		}
	}
	others := make([]string, 0, len(ids))
	for _, id := range sortedUnique(ids) {
		if id != best.ID {
			others = append(others, id)
		}
	}
	fd.Propose = Proposal{
		Op:     OpMergeSeries,
		Target: best.ID,
		Others: others,
		Reason: reason,
	}
	vetoes := seriesMergeVetoes(ix, group, best.ID)
	if sub == serDupSaga {
		vetoes = append([]string{reason}, vetoes...)
	}
	if len(vetoes) > 0 {
		fd.Propose.Op = OpReview
		fd.Propose.Advisory = true
		fd.Propose.Reason = "do not fold on this evidence: " + truncateList(vetoes, 4)
	}
	fd.Notes = []string{"spellings: " + truncateList(sortedUnique(names), 8)}
	return fd
}

// seriesMergeVetoes lists the reasons two same-looking series must not be folded
// mechanically. Each one was a wrong proposal.
//
// The three NAME-level rules and the stated ordering-family rule are here; the four
// MEMBER-level ones (author agreement,
// member collection evidence, ordering agreement and the language rule on the merits) are
// in vetoseries.go, which records the exhaustive review of all 694 non-advisory proposals
// that found them. target is the spelling the proposal would keep, which the directional
// rules need: folding a loser INTO it is what makes a claim.
func seriesMergeVetoes(ix *index, group []seriesKeys, target string) []string {
	return seriesMergeVetoesOver(ix, group, seriesSides(ix, group), target)
}

// seriesMergeVetoesOver is seriesMergeVetoes over sides the caller already holds,
// one per group member in the group's order.
func seriesMergeVetoesOver(ix *index, group []seriesKeys, sides []seriesSide, target string) []string {
	var out []string

	// ORDERING FAMILY: two series the data STATES are orderings of one franchise are
	// deliberately two records, however alike their names read.
	if reason, vetoed := vetoSeriesOrderingFamily(group); vetoed {
		out = append(out, reason)
	}

	// LANGUAGE, the same rule W-DUP has and for the same reason: a French and a
	// Polish series are not the English one spelled differently. Asked of the MEMBERS'
	// stated languages rather than of the sides' majority ones - see
	// vetoSeriesLanguagesDisagree for what a majority could not see.
	if reason, vetoed := vetoSeriesLanguagesDisagree(sides); vetoed {
		out = append(out, reason)
	}

	// AUTHOR AGREEMENT: the dominant veto of the class, and the one it shipped without.
	if reason, vetoed := vetoSeriesAuthorsDisjoint(ix, sides, target); vetoed {
		out = append(out, reason)
	}

	// ORDERING AGREEMENT: a fold either retires a spelling whose memberships the
	// survivor already holds, or adds ones it has room for. Everything else needs a human.
	if reason, vetoed := vetoSeriesOrderingDisagrees(sides, target); vetoed {
		out = append(out, reason)
	}

	// COLLECTION EVIDENCE off the MEMBERS, which the name-level rule below cannot see.
	if reason, vetoed := vetoSeriesCollectionMembers(sides, target); vetoed {
		out = append(out, reason)
	}

	// COLLECTION vs BOOK SERIES: "Pack Collection" beside "The Pack" is a series of
	// collections and a series of books, and folding them would put an omnibus in a
	// volume's slot.
	var coll, plain []string
	for _, k := range group {
		if titlerule.IsCollection(k.series.Name) {
			coll = append(coll, k.series.ID)
		} else {
			plain = append(plain, k.series.ID)
		}
	}
	if len(coll) > 0 && len(plain) > 0 {
		out = append(out, truncateList(coll, 3)+" name a collection and "+truncateList(plain, 3)+
			" do not: a series of collections is not the same series as the books it collects")
	}

	// PARENTHETICAL decoration is SER-PAREN's whole subject - see vetoSeriesDecoration.
	if reason, vetoed := vetoSeriesDecoration(group, sides, target); vetoed {
		out = append(out, reason)
	}
	return out
}

// decorationsRestate reports whether every parenthetical in the group is an ordering
// qualifier naming exactly the ordering the TARGET states in its field - so no
// decoration says anything the surviving record does not ("The Chronicles of Narnia
// (Author's Preferred Order)" stating ordering=recommended, beside a plain "Chronicles
// of Narnia"). A target stating no ordering never makes a decoration redundant, and
// neither is a decoration that is not one ordering group (an edition, an author
// disambiguator, two groups at once).
func decorationsRestate(group []seriesKeys, target string) bool {
	var ordering string
	for _, k := range group {
		if k.series.ID == target {
			ordering = k.series.Ordering
		}
	}
	if ordering == "" {
		return false
	}
	for _, k := range group {
		// A member whose own ordering FIELD states another order is a different list,
		// whatever its decoration says.
		if k.series.Ordering != "" && k.series.Ordering != ordering {
			return false
		}
		if k.paren && (k.decor == "" || titlerule.OrderingOfDecoration(k.decor) != ordering) {
			return false
		}
	}
	return true
}

// vetoSeriesDecoration: the members carry parentheticals that tell them apart, so a fold
// would erase the one thing distinguishing two series.
//
// A parenthetical is often a deliberate alternative ordering ("Ascend Online
// [chronological order]" beside "[publication order]"), an edition ("[French
// Edition]") or an author disambiguator that five different "Atlantis" series depend
// on. What distinguishes is what the decoration SAYS, compared through
// titlerule.DecorationKey, so the veto fires in exactly two shapes:
//
//   - two DIFFERENT decorations in one group: two orderings, two editions or two
//     authors' series, which is what the decorations are there to say;
//   - a decoration on ONE side only: folding the decorated spelling into the plain
//     one erases the decoration.
//
// It does NOT fire on a group whose members all carry the SAME decoration: "Throne of
// Glass[French Edition]" and "Throne of Glass [French Edition]" differ by a space and
// a bracket, and the decoration they share is not what tells them apart - so every
// other veto judges them as it would two plain spellings (the same decoration in two
// languages still stops on the language veto). A member whose parenthetical folds to
// nothing comparable is a decoration of its own, never equal to another's.
//
// The one-sided shape has TWO narrow exemptions, one mechanism: a decoration that says
// nothing about the series' LIST, on a series whose list the plain one already holds.
// It stands down only when every decoration has a oneSidedCover, the survivor is
// UNDECORATED, and every decorated member's memberships are each already in the survivor
// at the same slot (foldMovesNothing, the collection veto's own "nothing moves" test) -
// so the fold retires a spelling and changes no order.
//
//   - an ORDERING qualifier whose series is the plain one's list over again. "The
//     MaddAddam Trilogy (Published Order)" holds the same three works at the same slots
//     as "The MaddAddam Trilogy", and an ordering whose list is identical to its plain
//     sibling's is not a second ordering. It must also hold as MANY memberships: a
//     partial ordering that agrees on the volumes it has listed so far is still a list
//     nobody has shown to be the plain one.
//   - a FORMAT qualifier saying the series is ABRIDGED ("(Abridged)", "(gekürzt)").
//     Abridgement is a fact about a RECORDING (its `abridged` field), so "X (Abridged)"
//     holding works the plain "X" already lists at the same slots is the plain series'
//     list a second time. A SUBSET is enough here: only some volumes of a series are
//     ever abridged, and a part of the plain list at the plain slots states no order of
//     its own. Only those two words: "unabridged"/"ungekürzt" is what the plain name
//     already means, and a dramatization, a Hörspiel, a radio or full-cast production is
//     a product line with its own numbering. And the recording must SAY it: every work
//     the abridged spelling lists needs a recording stating `abridged: true`, or the
//     series name is the only record that an abridged production exists and folding
//     it away would erase that fact - so the group stays vetoed, naming those works
//     (3 of the 47 folds the arm made when it landed, all carrying only unabridged or
//     unstated recordings).
//
// Any other one-sided decoration still vetoes, even where nothing moves: an edition, an
// author or any other qualifier says something about the series that the plain name
// does not.
//
// And none of it applies when every decoration only RESTATES the target's own stated
// ordering field and no member's own ordering field states another (decorationsRestate):
// "The Chronicles of Narnia (Author's Preferred Order)" stating ordering=recommended, or
// "Ranger's Apprentice (published order)" beside a "Ranger's Apprentice" stating
// publication, says nothing the survivor does not.
// Measured when it was pushed in here from the family folds' caller option: no
// whole-group record on the 282,027-work tree moved.
func vetoSeriesDecoration(group []seriesKeys, sides []seriesSide, target string) (string, bool) {
	var decorated []string
	plain := 0
	classes := map[string]bool{}
	for _, k := range group {
		if !k.paren {
			plain++
			continue
		}
		decorated = append(decorated, k.series.ID)
		classes[decorClass(k)] = true
	}
	switch {
	case len(decorated) == 0 || decorationsRestate(group, target):
		return "", false
	case len(classes) >= 2:
		return truncateList(decorated, 4) + " carry different parenthetical decorations: that decoration is what " +
			"tells them apart (an alternative ordering, an edition, or an author disambiguator) - see SER-PAREN", true
	case plain == 0:
		return "", false // one decoration, carried by every member: it tells none of them apart
	}
	movesNothing, unstated := oneSidedFoldMovesNothing(group, sides, target)
	switch {
	case movesNothing && len(unstated) == 0:
		return "", false
	case movesNothing:
		return truncateList(decorated, 4) + " carry a parenthetical decoration the others do not, and no recording of " +
			truncateList(unstated, 4) + " states it is abridged, so the name is the only record of the abridgement: " +
			"folding would erase it - see SER-PAREN", true
	}
	return truncateList(decorated, 4) + " carry a parenthetical decoration the others do not: folding would erase " +
		"the decoration that distinguishes them - see SER-PAREN", true
}

// decorClass is the class a decorated member's decoration puts it in: its
// DecorationKey, or - where the parenthetical folds to nothing comparable - a class of
// its own, so an unreadable decoration never counts as agreeing with another.
func decorClass(k seriesKeys) string {
	if k.decor == "" {
		return "\x00" + k.series.ID
	}
	return k.decor
}

// oneSidedCover is how much of the survivor's list a one-sided decoration
// vetoSeriesDecoration may fold away must cover (which words are in and out, and why,
// is stated there), read off its titlerule.DecorationKey so case, bracket style and
// diacritics are not a difference; 0 for a decoration that is never folded away.
// Both are closed vocabularies. An ORDERING is titlerule's one ordering vocabulary
// (titlerule.OrderingOfDecoration, the reader the importer's qualifier index keys by,
// so the audit and the importer cannot disagree about which decorations name a
// reading order) and must cover the whole list. Abridged, from the languages plan's
// Phase 6 census of the format-qualified series (abridged 179 and gekürzt 27 of 254),
// may cover any part of it.
func oneSidedCover(decorKey string) listCover {
	if titlerule.OrderingOfDecoration(decorKey) != "" {
		return coverWhole
	}
	return abridgedDecorations[decorKey]
}

// abridgedDecorations are the one-sided ABRIDGED decorations, keyed by
// titlerule.DecorationKey.
var abridgedDecorations = func() map[string]listCover {
	out := map[string]listCover{}
	for _, phrase := range []string{"abridged", "gekürzt"} {
		out[titlerule.DecorationKey("("+phrase+")")] = coverPart
	}
	return out
}()

// listCover is how much of the survivor's list an exempt one-sided decoration's list
// must be. Its zero value is no rule at all, so a lookup that misses can never read as
// either arm.
type listCover int

const (
	_          listCover = iota
	coverWhole           // every membership, and as many of them: an ordering
	coverPart            // any part of it: an abridged spelling
)

// oneSidedFoldMovesNothing is vetoSeriesDecoration's one-sided exemption: the survivor
// is undecorated, every decorated member carries an exempt qualifier, and none of their
// memberships would move - each already in the survivor at the same slot, and for an
// ordering as many of them as the survivor holds. unstated is, in group then series
// order and each work once, every work an ABRIDGED spelling lists that no recording
// states is abridged: a fold that moves nothing still stands only when it is empty.
// (Two abridged spellings in one group - "(Abridged)" and "[abridged]" share a
// DecorationKey - may list the same work, which is still one work to name.)
func oneSidedFoldMovesNothing(group []seriesKeys, sides []seriesSide, target string) (movesNothing bool, unstated []string) {
	tgt, losers, ok := splitSides(sides, target)
	if !ok {
		return false, nil
	}
	decor := make(map[string]seriesKeys, len(group))
	for _, k := range group {
		decor[k.series.ID] = k
	}
	if decor[target].paren {
		return false, nil
	}
	for _, l := range losers {
		k := decor[l.series.ID]
		if !k.paren {
			continue
		}
		cover := oneSidedCover(k.decor)
		if cover == 0 || (cover == coverWhole && len(l.members) != len(tgt.members)) || !foldMovesNothing(tgt, l) {
			return false, nil
		}
		if cover == coverPart {
			for _, m := range l.members {
				if !m.abridged && !slices.Contains(unstated, m.work) {
					unstated = append(unstated, m.work)
				}
			}
		}
	}
	return true, unstated
}

// sameDecorationSubgroups is, for each comparable decoration at least two members of a
// group carry, those members - when they are not the whole group (a group that is all
// one decoration is already judged whole). In decoration-key order, each subgroup in
// the group's own order.
//
// An EMPTY decor is never a decoration two members share - decorClass's invariant.
// It is what an undecorated name has, and what a paren name whose groups fold to
// nothing comparable has, and neither is "the same decoration" as anything: two plain
// spellings are the normalized-name group's own business, and two unreadable
// parentheticals are not known to agree. So "" is skipped here by name rather than
// left to groupBy's habit of dropping an empty key.
func sameDecorationSubgroups(group []seriesKeys) [][]seriesKeys {
	byDecor, order := groupBy(group, func(k seriesKeys) string { return k.decor })
	var out [][]seriesKeys
	for _, d := range order {
		if d == "" {
			continue
		}
		if sub := byDecor[d]; len(sub) >= 2 && len(sub) < len(group) {
			out = append(out, sub)
		}
	}
	return out
}

// detectSeriesParen reports series names carrying a parenthetical decoration. It
// proposes nothing: "Vorkosigan Saga (chronological)" beside "Vorkosigan Saga" is
// very likely a DELIBERATE second ordering of one series, which the data model has
// no other way to express, so the only honest output is "a human should look". (The
// folds SER-DUP makes over such a pair are an ordering whose list IS the plain series'
// list and an abridged spelling whose list the plain series already holds, every work
// of it with a recording stating the abridgement - see
// vetoSeriesDecoration - and those are SER-DUP's proposals, not this class's.)
//
// It reads the SAME key index detectSeriesDup does, and in ONE pass: the plain
// siblings are collected as the loop goes rather than in a first loop of their own,
// which is what a decorated name later in catalogue order needs anyway - so the
// sibling lookup is completed after the walk, not during it.
func detectSeriesParen(ix *index, keys []seriesKeys) *findings {
	f := &findings{class: ClassSeriesParen}
	plain := map[string][]string{}
	var decorated []seriesKeys
	for _, k := range keys {
		if k.paren {
			decorated = append(decorated, k)
			continue
		}
		plain[k.tight] = append(plain[k.tight], k.series.ID)
	}
	for _, k := range decorated {
		s := k.series
		siblings := sortedUnique(plain[k.tight])
		fd := Finding{
			Key:    s.ID,
			Series: []SeriesRef{ix.seriesRef(s)},
		}
		fd.Propose = Proposal{
			Op:       OpReview,
			Target:   s.ID,
			Field:    "name",
			From:     s.Name,
			To:       titlerule.TidyTitle(titlerule.StripParenGroups(s.Name)),
			Advisory: true,
		}
		if len(siblings) > 0 {
			fd.Subclass = serParenPair
			fd.Propose.Others = siblings
			fd.Propose.Reason = "the parenthetical may be a deliberate alternative ordering of the sibling series - not merged automatically, " +
				"except the folds SER-DUP proposes: an ordering qualifier whose list IS the sibling's, slot for slot, " +
				"and an abridged spelling whose memberships the sibling already holds at the same slots, " +
				"each of whose works has a recording stating it is abridged"
			fd.Notes = []string{"undecorated sibling: " + truncateList(siblings, 8)}
			for _, id := range siblings {
				if sib := ix.seriesByID[id]; sib != nil {
					fd.Series = append(fd.Series, ix.seriesRef(sib))
				}
			}
		} else {
			fd.Subclass = serParenSolo
			fd.Propose.Reason = "the parenthetical is decoration with nothing to fold onto, so the name itself may want cleaning"
		}
		f.add(fd)
	}
	return f
}
