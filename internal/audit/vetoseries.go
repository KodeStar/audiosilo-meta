package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// vetoseries.go holds the SER-DUP merge vetoes found by EXHAUSTIVELY REVIEWING every
// non-advisory merge-series proposal the class made - all 694 of them, against the full
// membership, authorship and language of the 1,443 series involved. 586 were wrong and a
// further 39 undecidable; 36 of the 106 that a repair run would have applied cleanly
// would have folded two different franchises into one public series slug.
//
// The three vetoes the class shipped with (seriesMergeVetoes in seriesdup.go) all read the
// series NAME or its majority language, and that is what the review found structurally
// insufficient: the evidence that two same-looking names are one series lives in the
// MEMBERS - who wrote them, what they are called, where they sit - and none of it was
// being asked. The four mechanisms below ask it, and each is measured over that review:
//
//   - AUTHOR AGREEMENT (vetoSeriesAuthorsDisjoint) - 581 of the 694 proposals were
//     author-disjoint, including ALL 36 that would have been applied. Dominant by a
//     wide margin: a normalized name collision ("Absolution", "The Academy", "Renegades")
//     is two romance franchises far more often than it is two spellings of one.
//   - MEMBER-LEVEL COLLECTION EVIDENCE (vetoSeriesCollectionMembers) - 17 of the 36. The
//     name-level rule cannot see a series whose one member is an omnibus ("The Complete
//     Undead Apocalypse Series" at 0-3, "Asylum Series, Books 1 - 3 Bonus Edition" at 1-3).
//   - ORDERING AGREEMENT (vetoSeriesOrderingDisagrees) - 39 proposals whose two sides
//     disagree about the order itself, which nothing in the audit was asking (the repair
//     refuses them at plan time, so they were "safe" by accident rather than judged).
//   - LANGUAGE, on the MERITS (vetoSeriesLanguagesDisagree) - the existing rule compared
//     strict MAJORITY languages, and a tie has no majority, so the French edition of
//     Hyperion (en 2 / fr 2) was invisible to it. 9 proposals, all of them language
//     editions.
//
// The one-work-loser shape the review measured separately (31 of the 36: a loser holding
// exactly ONE work, landing in a slot the target leaves free, so no ordering conflict can
// arise) is deliberately NOT a veto of its own. It is what author agreement already
// refuses - every one of the 31 is author-disjoint - and on its own it is far too coarse:
// 36 of the 69 proposals the review confirmed CORRECT are one-work losers, so a veto on
// the shape would cost the class half its mechanical path to catch nothing new.

// seriesSide is one series of a SER-DUP cluster with the member-level facts the merge
// vetoes read, derived ONCE per side: the authors its member works credit, and what and
// where those members are. Four vetoes ask for it and each question would otherwise walk
// the series' works again.
type seriesSide struct {
	series  *model.Series
	authors []string // every author of every member work, sorted and deduplicated
	members []seriesMember
}

// seriesMember is one membership plus the member work's own facts.
type seriesMember struct {
	work     string
	position string
	title    string
	authors  []string
	language string
}

// seriesSides derives a cluster's sides in the cluster's own (catalogue) order.
func seriesSides(ix *index, group []seriesKeys) []seriesSide {
	out := make([]seriesSide, 0, len(group))
	for _, k := range group {
		out = append(out, seriesSideOf(ix, k.series))
	}
	return out
}

func seriesSideOf(ix *index, s *model.Series) seriesSide {
	side := seriesSide{series: s, members: make([]seriesMember, 0, len(s.Works))}
	var authors []string
	for _, sw := range s.Works {
		m := seriesMember{work: sw.Work, position: sw.Position}
		if w := ix.workByID[sw.Work]; w != nil {
			m.title, m.authors, m.language = w.Title, sortedUnique(w.Authors), w.Language
			authors = append(authors, w.Authors...)
		}
		side.members = append(side.members, m)
	}
	side.authors = sortedUnique(authors)
	return side
}

// splitSides separates the cluster's sides into the proposed survivor and the spellings
// that would be retired into it. The vetoes that ask a directional question - what does
// folding THIS side into THAT one assert - need the split; the symmetric ones do not.
func splitSides(sides []seriesSide, targetID string) (target seriesSide, losers []seriesSide, ok bool) {
	for _, s := range sides {
		if s.series.ID == targetID {
			target, ok = s, true
			continue
		}
		losers = append(losers, s)
	}
	return target, losers, ok
}

// vetoSeriesAuthorsDisjoint: the two sides' member works share no author, so nothing but
// the normalized name says they are one series.
//
// This is the veto the class was missing, and the review found it dominant: 581 of 694
// non-advisory proposals, and all 36 that a repair run would have applied. The shapes are
// ordinary - "Absolution", "The Academy", "Renegades", "The Elite" are titles two
// unrelated romance or LitRPG franchises reach for independently - and folding them mints
// one public series slug holding two authors' books in one contradictory order.
//
// It is asked TWICE, at the two levels a fold makes a claim at:
//
//   - the SIDE level: a loser whose authors are disjoint from the target's is a different
//     franchise. Asked against the TARGET rather than pairwise across the cluster,
//     because a merge folds every loser onto that one survivor: `doctor` is two E L Todd
//     spellings PLUS Alexander Leithes' "The Doctor", and a pairwise test is satisfied by
//     the two that agree while the third rides along.
//   - the MEMBER level: a work the fold MOVES that shares no author with the target is
//     the same claim about one book. It is what catches the loser of
//     `kingkillerchronicle`, whose authors overlap through Rothfuss while the membership
//     it contributes is the Dozois/Martin/Gaiman/Flynn anthology "Rogues" at 1.5.
//
// Author identity is compared through samePersonSpelling, not by id: `girlfromthestars`
// is disjoint only because Cheree Alsop's records forked into `cheree-alsop` and
// `cheree-lynn-alsop`, and a person duplicate must not be able to manufacture a veto.
// Measured over the review, that tolerance rescues exactly that one proposal and costs
// none: the one proposal it un-vetoes (`harbinger`, Jennifer Armentrout beside Jennifer L
// Armentrout) is a language edition, so vetoSeriesLanguagesDisagree still withholds it.
//
// The one CORRECT proposal this refuses is `jackryanjr`, and refusing it is right: the
// Jack Ryan Jr. novels are a ghostwritten franchise, so Tom Clancy's own "The Teeth of
// the Tiger" shares no author with the fourteen continuations that follow it. That a
// human should confirm is exactly what an advisory says.
func vetoSeriesAuthorsDisjoint(ix *index, sides []seriesSide, targetID string) (string, bool) {
	target, losers, ok := splitSides(sides, targetID)
	if !ok {
		return "", false
	}
	for _, l := range losers {
		if len(target.authors) == 0 || len(l.authors) == 0 {
			continue // nothing stated is not a disagreement
		}
		if !anySamePerson(ix, target.authors, l.authors) {
			return fmt.Sprintf("%s and %s share no member-work author (%s against %s): a name collision is two "+
				"franchises far more often than it is one series spelled twice",
				target.series.ID, l.series.ID, truncateList(target.authors, 3), truncateList(l.authors, 3)), true
		}
	}
	for _, l := range losers {
		held := make(map[string]bool, len(target.members))
		for _, m := range target.members {
			held[m.work] = true
		}
		for _, m := range l.members {
			if held[m.work] || len(m.authors) == 0 || len(target.authors) == 0 {
				continue // already there, or nothing stated to disagree with
			}
			if !anySamePerson(ix, target.authors, m.authors) {
				return fmt.Sprintf("the fold would move %s into %s, and it shares no author with it (%s against %s): "+
					"a membership by other people is a different book, not this series' order",
					m.work, target.series.ID, truncateList(m.authors, 3), truncateList(target.authors, 3)), true
			}
		}
	}
	return "", false
}

// anySamePerson reports whether two author lists name a person in common, under
// samePersonSpelling rather than by id.
func anySamePerson(ix *index, a, b []string) bool {
	set := make(map[string]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		if set[id] {
			return true
		}
	}
	for _, x := range a {
		for _, y := range b {
			if samePersonSpelling(ix, x, y) {
				return true
			}
		}
	}
	return false
}

// samePersonSpelling reports whether two person ids may be ONE person under two
// spellings. It exists so a person duplicate cannot manufacture an author-disagreement
// veto: an id is the identity, so two records for one author read as two authors, and
// the merge evidence would be lost to a defect in a different family.
//
// The first three rungs are P-DUP's own three keys, asked through the same calls, so this
// tolerates exactly the person pairs the audit itself reports as possible duplicates -
// the importer's initials identity, a punctuation-only difference, and one edit over the
// same length floor. The fourth is the MIDDLE-NAME insertion P-DUP does not report (a
// whole extra word is neither one edit nor an initials cluster) and which is the shape
// that masked `girlfromthestars`: `cheree-alsop` beside `cheree-lynn-alsop`.
//
// Every rung WEAKENS a veto, so each is bounded to a form that is a spelling of one name
// rather than a resemblance between two: a shared surname alone is never enough.
func samePersonSpelling(ix *index, a, b string) bool {
	if a == b {
		return true
	}
	pa, pb := ix.personByID[a], ix.personByID[b]
	if pa == nil || pb == nil {
		return false // an id with no record is only ever itself
	}
	if importer.MarkedNameKey(pa.Name) == importer.MarkedNameKey(pb.Name) {
		return true
	}
	fa, fb := titlerule.FoldKey(pa.Name), titlerule.FoldKey(pb.Name)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb {
		return true
	}
	if len(fa) >= minEditDistanceLen && len(fb) >= minEditDistanceLen && titlerule.OneEditApart(fa, fb) {
		return true
	}
	return middleNameVariant(pa.Name, pb.Name)
}

// middleNameVariant reports whether two names differ only by MIDDLE words: they open and
// close on the same word and one's word sequence is the other's with extra words in
// between ("Cheree Alsop" / "Cheree Lynn Alsop", "J K Rowling" / "J K J Rowling").
//
// The first and last word must both match, which is what keeps it off the resemblance a
// surname alone is: "Sarah Maas" and "Sarah J Maas" are one person, "Sarah Maas" and
// "Sarah Pinsker" are two.
func middleNameVariant(a, b string) bool {
	wa, wb := foldedWords(a), foldedWords(b)
	if len(wa) < 2 || len(wb) < 2 || len(wa) == len(wb) {
		return false
	}
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	if wa[0] != wb[0] || wa[len(wa)-1] != wb[len(wb)-1] {
		return false
	}
	i := 0
	for _, w := range wb {
		if i < len(wa) && wa[i] == w {
			i++
		}
	}
	return i == len(wa)
}

// foldedWords is a name's words, each folded through titlerule.FoldKey so a diacritic or
// a dot is not a difference. Empty folds are dropped.
func foldedWords(name string) []string {
	var out []string
	for _, w := range strings.Fields(name) {
		if f := titlerule.FoldKey(w); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// vetoSeriesCollectionMembers: one side is a series of COLLECTIONS and the other a series
// of books, read off the MEMBERS rather than off the series name.
//
// The name-level rule beside it ("Pack Collection" against "The Pack") only sees a series
// that says so in its own name, and the review found that is not how the tree spells it:
// 17 of the 36 proposals a repair would have applied are a plainly-named series whose one
// member is a box set. `afflicted` is Derek Shupert's "The Complete Undead Apocalypse
// Series" at 0-3 beside four volumes of somebody else's "The Afflicted"; `asylum` is
// "Asylum Series, Books 1 - 3 Bonus Edition" at 1-3 beside Madeleine Roux's five;
// `deepblack` is three James David Victor omnibuses at 1-3/4-6/7-9 beside seven Coonts
// volumes. Folding either way puts an omnibus in a volume's slot.
//
// TWO independent arms, because either is the evidence on its own:
//
//   - the member TITLES say collection (titlerule.IsCollection, the same multilingual
//     vocabulary the name-level rule and W-DUP's collection veto read);
//   - the member POSITIONS are RANGES on one side and single slots on the other. A range
//     IS the statement "this product covers those volumes", which is why it reaches the
//     three Bayne omnibuses whose titles the vocabulary would have had to guess at.
//
// A side is judged WHOLE - every member a collection, every position a range - so a real
// series that also holds an omnibus is untouched; the arms speak about a series that is
// nothing but collections.
//
// A loser whose members are ALREADY in the target at the same slots is exempt from both:
// the fold moves no membership, so no omnibus can land in a volume's place, and refusing
// it would withhold the pure duplicate-spelling retirements this class exists for
// (`mephistosmagiconline`, whose two spellings both hold one omnibus at 1-3).
//
// Measured cost: 2 of the 69 confirmed-correct proposals go advisory, both on the range
// arm and both the same judgement call - `crowbrothers` and `londoncoven` fold a box set
// at 1-4/1-3 into the series of the volumes it collects. Which slot a box set should hold
// beside its own volumes is a human decision, so an advisory is the honest verdict.
func vetoSeriesCollectionMembers(sides []seriesSide, targetID string) (string, bool) {
	target, losers, ok := splitSides(sides, targetID)
	if !ok {
		return "", false
	}
	for _, l := range losers {
		if foldMovesNothing(target, l) {
			continue
		}
		if allCollectionTitles(l) != allCollectionTitles(target) {
			coll, plain := l, target
			if allCollectionTitles(target) {
				coll, plain = target, l
			}
			return fmt.Sprintf("every member of %s is titled as a collection (%s) and none of %s is: a series of "+
				"box sets is not the series of the books it collects",
				coll.series.ID, truncateList(memberTitles(coll), 2), plain.series.ID), true
		}
		if allRangePositions(l) && allSlotPositions(target) || allRangePositions(target) && allSlotPositions(l) {
			rng, slots := l, target
			if allRangePositions(target) {
				rng, slots = target, l
			}
			return fmt.Sprintf("every member of %s spans a RANGE of positions (%s) while every member of %s holds a "+
				"single slot: folding them would put an omnibus in a volume's place",
				rng.series.ID, truncateList(memberPositions(rng), 3), slots.series.ID), true
		}
	}
	return "", false
}

// allCollectionTitles reports whether every member of a side is titled as a collection.
// An empty side is not (S-INTEGRITY's finding, not evidence here).
func allCollectionTitles(s seriesSide) bool {
	if len(s.members) == 0 {
		return false
	}
	for _, m := range s.members {
		if m.title == "" || !titlerule.IsCollection(m.title) {
			return false
		}
	}
	return true
}

// allRangePositions reports whether every member position is a RANGE.
func allRangePositions(s seriesSide) bool {
	if len(s.members) == 0 {
		return false
	}
	for _, m := range s.members {
		if _, _, isRange := importer.PositionRange(m.position); !isRange {
			return false
		}
	}
	return true
}

// allSlotPositions reports whether every member position names a single slot - the
// complement of a range, and not merely "not a range": a position the grammar rejects is
// neither, and is S-INTEGRITY's finding.
func allSlotPositions(s seriesSide) bool {
	if len(s.members) == 0 {
		return false
	}
	for _, m := range s.members {
		if positionKey(m.position) == "" {
			return false
		}
	}
	return true
}

func memberTitles(s seriesSide) []string {
	out := make([]string, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m.title)
	}
	return sortedUnique(out)
}

func memberPositions(s seriesSide) []string {
	out := make([]string, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m.position)
	}
	return sortedUnique(out)
}

// vetoSeriesOrderingDisagrees: the two sides do not agree about the ORDER, so the fold
// cannot be performed without a human choosing which ordering survives.
//
// A merge-series is only ever sound as one of two things: retiring a spelling whose
// memberships the survivor already holds at the same slots, or adding memberships the
// survivor leaves room for. This veto is the statement of that, and what it refuses is
// every third case - 39 proposals the review could not confirm, and which the repair
// pass was refusing at plan time (series-position-conflict) rather than on the merits, so
// the audit was publishing them as mechanical work.
//
// Two arms, both of them a disagreement between the two lists:
//
//   - the same SLOT holds different works (`barsoom` puts `the-warlord-of-mars` at 3 and
//     `warlord-of-mars` at 3 - one book under two work slugs, which is W-DUP's finding
//     and has to be resolved there FIRST);
//   - the same WORK sits at different slots (`parasolprotectorate` places "Meat Cute" at
//     0 and at 0.75, `somethingmore` places "In Ruins" at 1 and at 3).
//
// Slots are compared through importer.SameSlot, the project's one position-identity rule,
// so "3" and "03" are one place in the order and "1-3" is not the slot "1" - the same
// comparison internal/repair refuses on, which is what keeps the two passes from
// disagreeing about what a conflict is.
//
// It costs NONE of the 69 confirmed-correct proposals: 55 of them are the exact
// subset-agree shape (every loser membership already in the target, same slot) and the
// other 14 add memberships to free positions.
func vetoSeriesOrderingDisagrees(sides []seriesSide, targetID string) (string, bool) {
	target, losers, ok := splitSides(sides, targetID)
	if !ok {
		return "", false
	}
	for _, l := range losers {
		for _, a := range target.members {
			for _, b := range l.members {
				switch {
				case a.work == b.work:
					if !importer.SameSlot(a.position, b.position) {
						return fmt.Sprintf("%s and %s both place %s, at %s and at %s: two orderings are not one series",
							target.series.ID, l.series.ID, a.work, a.position, b.position), true
					}
				case importer.SameSlot(a.position, b.position):
					return fmt.Sprintf("%s puts %s at %s and %s puts %s there: the two lists contradict each other "+
						"about who holds that place in the order",
						target.series.ID, a.work, a.position, l.series.ID, b.work), true
				}
			}
		}
	}
	return "", false
}

// foldMovesNothing reports whether every membership of a loser is already in the target
// at the same slot, so folding it changes no ordering - the pure duplicate-spelling
// retirement this class exists for.
func foldMovesNothing(target, loser seriesSide) bool {
	at := make(map[string]string, len(target.members))
	for _, m := range target.members {
		at[m.work] = m.position
	}
	for _, m := range loser.members {
		pos, held := at[m.work]
		if !held || !importer.SameSlot(pos, m.position) {
			return false
		}
	}
	return true
}

// vetoSeriesLanguagesDisagree: two members on DIFFERENT sides state different languages,
// so at least one side is a translation - a different series, the same rule W-DUP has.
//
// It replaced a comparison of the sides' strict MAJORITY languages, which asked the wrong
// question of the wrong population. A veto asks "does anything here disagree", and a
// majority answers "what is this series mostly in": a side with no strict majority has no
// majority language at all, so a tie contributed NOTHING to compare. That is not an edge
// case in this tree - the French edition of Hyperion is stored as two French works beside
// two English ones (en 2 / fr 2), which is exactly a tie - and the review found 9 such
// proposals, every one of them a language edition folding into its original
// (`hyperion`, `pawnandthepuppet`'s German pair, `andromeda`, `avatar`, `chronosfiles`,
// `edgeofglass`, `harbinger`, `mattdrake`, `salvation`).
//
// An UNKNOWN language still separates nothing, as everywhere else in the project
// (languagesCompatible): the disagreement has to be between two STATED values.
//
// Symmetric across the cluster rather than target-against-loser: a translation anywhere in
// a cluster is a reason not to fold it, whichever spelling would survive.
func vetoSeriesLanguagesDisagree(sides []seriesSide) (string, bool) {
	for i := range sides {
		for j := i + 1; j < len(sides); j++ {
			a, b := sides[i], sides[j]
			for _, la := range statedLanguages(a) {
				for _, lb := range statedLanguages(b) {
					if la != lb {
						return fmt.Sprintf("%s holds %s member works and %s holds %s: a translation is a different "+
							"series, not the same one spelled differently",
							a.series.ID, la, b.series.ID, lb), true
					}
				}
			}
		}
	}
	return "", false
}

// statedLanguages is the languages a side's member works actually state, sorted. An
// unstated language is not a value.
func statedLanguages(s seriesSide) []string {
	var out []string
	for _, m := range s.members {
		if m.language != "" {
			out = append(out, m.language)
		}
	}
	out = sortedUnique(out)
	sort.Strings(out)
	return out
}
