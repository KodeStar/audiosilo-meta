package importer

// librivoxauthors.go is the LibriVox import's two AUTHOR rules (meta#2751),
// read beside librivoxmatch.go's work rule:
//
//  1. AN AUTHOR SPELLED WITH MORE (OR FEWER) WORDS IS THE SAME AUTHOR, on the
//     same title. A row whose title key (librivoxTitleKey) names a work by no
//     author of the row's, where each of the row's identity authors pairs with
//     one of the work's by the same surname and one name's words are a subset
//     of the other's ("Henrietta Elizabeth Marshall" / "Henrietta Marshall",
//     "Joseph Sheridan Le Fanu" / "Sheridan Le Fanu"; a generational suffix
//     on both names or neither), attaches to that work and credits the
//     catalogued person: the row's author credit is moved onto that person
//     (credit.person), so the work's credit list is untouched and the person is
//     keyed by the row's LibriVox author id when the stamp's rules allow
//     (stampLibrivoxID) - unless LibriVox's spelling is already a person
//     record (catalogued, keyed by that id, or created earlier in the run),
//     which keeps the credit and its id (splitLibrivoxMoves). The person
//     schema has no alias field, so LibriVox's own spelling is kept in the
//     run's note only. Anything
//     looser - an initial ("E. Nesbit" / "Edith Nesbit"), another given name
//     ("Fanny" / "Frances Burney"), a one-word name ("Ovid"), "Saint" against
//     "St", a Jr. or Sr. on one name only ("Henry James" / "Henry James Sr.":
//     father and son) - is an author-spelling worklist row, never an attach,
//     and so is a work whose author already carries ANOTHER LibriVox author id
//     than the row's (LibriVox keeps them as two people). A surname apart is
//     another author (a new work, as before).
//     It works within one run too: a work an earlier row created is in the
//     title index with its people, so the second spelling meets the first -
//     and an author id an earlier row of the run claimed counts as carried
//     (claimLibrivoxAuthors), so a second spelling with another id is the same
//     worklist row in one run as in two.
//  2. A NAME THAT SLUGS TO NOTHING IS NEVER THE CATCH-ALL. An author or
//     translator credit in a script the slug rule folds away entirely
//     ("Йордан Йовков") would land on the one catch-all person record
//     (model.UnslugPersonID), which every such name shares. One carrying a
//     LibriVox author id is filed by it, at `librivox-author-<id>`
//     (librivoxpeople.go rule 4: its decided person keeps it in the row's
//     credits, splitUnsluggableCredits); one with no id (or whose slot is
//     taken) is dropped from the row before the work is decided and reported
//     as a credit not recorded (reason unsluggable, or
//     librivox-author-slot-taken) - the rule every source but libex follows
//     (unrecorded.go).
//     A row left with no individual identity author (only a translator, or only
//     a collective such as Anonymous) is a worklist row (author-unsluggable):
//     a work credited to nobody, or to "Anonymous" alone, would merge two
//     authors' same-titled books.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// authorSpelling grades two spellings of an author's name.
type authorSpelling int

const (
	// spellingApart: another author (different surnames).
	spellingApart authorSpelling = iota
	// spellingLoose: the same surname, but nothing proves the same person (an
	// initial, another given name, a one-word name).
	spellingLoose
	// spellingVariant: the same surname, and one name's words are a subset of
	// the other's.
	spellingVariant
)

// librivoxNameWords is a name's words as the slug rule folds them (case,
// diacritics, punctuation), its trailing generational suffix
// (generationalSuffixes) split off: "Francis Parkman Jr." has the surname
// Parkman and the suffix jr.
func librivoxNameWords(name string) (words []string, suffix string) {
	for w := range strings.SplitSeq(model.SlugifyWhole(name), "-") {
		if w != "" {
			words = append(words, w)
		}
	}
	if n := len(words); n > 1 && generationalSuffixes[words[n-1]] {
		return words[:n-1], words[n-1]
	}
	return words, ""
}

// gradeAuthorSpelling compares two names by rule 1 of this file's header.
func gradeAuthorSpelling(a, b string) authorSpelling {
	wa, sa := librivoxNameWords(a)
	wb, sb := librivoxNameWords(b)
	if len(wa) == 0 || len(wb) == 0 || wa[len(wa)-1] != wb[len(wb)-1] {
		return spellingApart
	}
	if sa != sb {
		return spellingLoose // Jr. against Sr., or a suffix on one name only: father and son
	}
	short, long := wa, wb
	if len(short) > len(long) {
		short, long = long, short
	}
	if len(short) < 2 {
		return spellingLoose
	}
	for _, w := range short {
		if !slices.Contains(long, w) {
			return spellingLoose
		}
	}
	return spellingVariant
}

// librivoxAuthorPairing grades a work's identity authors against a row's: a
// variant pairing names, for each row author the work does not credit, the
// work author it is a spelling of. Only individual authors pair; a collective
// matches only itself. Each row author's variants are read against every work
// author the row does not credit, never against what an earlier row author
// paired, so the grade cannot depend on the order the row lists its authors:
// a row author that is a spelling of two work authors, or two row authors that
// are spellings of one, is loose.
func (p *planner) librivoxAuthorPairing(ws *workState, row []string, names map[string]string) (authorSpelling, map[string]string) {
	if len(row) != len(ws.authors) {
		return spellingApart, nil
	}
	pairs := map[string]string{}
	paired := map[string]bool{}
	grade := spellingVariant
	for _, r := range row {
		if ws.authors[r] {
			continue
		}
		if !individualAuthor(r) {
			return spellingApart, nil
		}
		var variants []string
		loose := false
		for w := range ws.authors {
			if slices.Contains(row, w) || !individualAuthor(w) {
				continue
			}
			switch gradeAuthorSpelling(names[r], p.people[w]) {
			case spellingVariant:
				variants = append(variants, w)
			case spellingLoose:
				loose = true
			}
		}
		switch {
		case len(variants) == 1 && !paired[variants[0]]:
			pairs[r] = variants[0]
			paired[variants[0]] = true
		case len(variants) > 0 || loose:
			grade = spellingLoose
		default:
			return spellingApart, nil
		}
	}
	if len(pairs) == 0 && grade == spellingVariant {
		return spellingApart, nil // every author agrees: matchWork's case, not this one
	}
	return grade, pairs
}

// librivoxAuthorVariants is rule 1 over the works under one title key (slugs)
// that librivoxCandidates found no agreement in: the works the row's authors
// are spellings of (variants, with the move of the row's credits onto the
// catalogued people that attaching to it takes: row person -> work person)
// and those they only loosely resemble. A spelling whose work author already
// carries a LibriVox author id other than the one row b states for that
// credit is loose: LibriVox's two ids may well be two people.
func (p *planner) librivoxAuthorVariants(b sourceBook, slugs []string, lang string, authors workAuthors, byPerson map[string]credit) (variants []*workState, moves []map[string]string, loose []*workState) {
	names := make(map[string]string, len(byPerson))
	for slug, c := range byPerson {
		names[slug] = c.name
	}
	for _, slug := range slugs {
		ws := p.works[slug]
		if ws == nil || !langCompatible(ws.lang, lang) || matchWork(ws, authors) != matchNone {
			continue
		}
		switch grade, pairs := p.librivoxAuthorPairing(ws, authors.identity, names); grade {
		case spellingVariant:
			if !p.movesOntoOwnNames(pairs) || p.pairsCarryOtherIDs(b, pairs, byPerson) {
				loose = append(loose, ws)
				continue
			}
			variants = append(variants, ws)
			moves = append(moves, pairs)
		case spellingLoose:
			loose = append(loose, ws)
		}
	}
	return variants, moves, loose
}

// creditsByPerson is the row's credits by the person each resolves to
// (resolveCredit), the first credit of a person kept.
func (p *planner) creditsByPerson(credits []credit) map[string]credit {
	out := make(map[string]credit, len(credits))
	for _, c := range credits {
		if slug := p.resolveCredit(c).slug; out[slug].name == "" {
			out[slug] = c
		}
	}
	return out
}

// movesOntoOwnNames reports whether every catalogued person a variant moves a
// credit onto (pairs: row slug -> work person) is recorded at its own name's
// slug: a person whose id is not (a LibriVox reader's `-librivox-<id>` or
// `librivox-reader-<id>` record, or an author's `-librivox-author-<id>` or
// `librivox-author-<id>` one: model.PersonIDMatches' LibriVox forms) was kept
// apart from a same-name person by their LibriVox id, never an author
// spelling, so such a pairing is never attached. A person id is otherwise
// always its name's slug (pkg/check's person-slug rule), so no initials
// spelling stands between the two.
func (p *planner) movesOntoOwnNames(pairs map[string]string) bool {
	for _, w := range pairs {
		if own, fellBack := personSlug(p.people[w]); fellBack || own != w {
			return false
		}
	}
	return true
}

// pairsCarryOtherIDs reports whether a work author a pairing names already
// carries a LibriVox author id other than the one row b states for the paired
// credit (librivoxAuthorIDOf): catalogued, or claimed by a row already planned
// in this run (authorOf, claimLibrivoxAuthors), so two spellings with two ids
// are refused alike whether they arrive in one run or two.
func (p *planner) pairsCarryOtherIDs(b sourceBook, pairs map[string]string, byPerson map[string]credit) bool {
	for r, w := range pairs {
		c := byPerson[r]
		if id := p.librivoxAuthorIDOf(b, c, p.resolveCredit(c)); id > 0 && p.heldByOtherAuthorID(w, id) {
			return true
		}
	}
	return false
}

// heldByOtherAuthorID reports whether person slug is held by a LibriVox author
// id other than id: the one its record carries or a planned row claimed
// (authorOf), or one the run decided onto it before planning (authorPins:
// resolveLibrivoxAuthors and keyLibrivoxAuthorVariants). The pins are what a
// later run would read as catalogued, so a second id meeting the person is
// refused alike in one run and in two, whichever row arrives first.
func (p *planner) heldByOtherAuthorID(slug string, id int) bool {
	if have := p.lv.authorOf[slug]; have != 0 {
		return !p.answersTo(librivoxAuthor, slug, id)
	}
	for other, pin := range p.lv.authorPins {
		if other != id && pin != nil && pin.slug == slug {
			return true
		}
	}
	return false
}

// soleAuthorVariant is rule 1's attach test over the works under one title
// key that no agreeing work was found among (partial: those sharing a person
// with the row): the one work the row's authors are spellings of, when
// nothing else under the key resembles them, and the move of the row's
// credits attaching to it takes (row person -> work person), with the row's
// credits by person it was read through (creditsByPerson). Otherwise related
// lists every work rule 1 found, for a worklist row, and several says two or
// more of them are variants.
func (p *planner) soleAuthorVariant(b sourceBook, slugs []string, lang string, authors workAuthors, credits []credit, partial []*workState) (ws *workState, move map[string]string, byPerson map[string]credit, related []*workState, several bool) {
	byPerson = p.creditsByPerson(credits)
	variants, moves, loose := p.librivoxAuthorVariants(b, slugs, lang, authors, byPerson)
	if len(variants)+len(loose) == 0 {
		return nil, nil, byPerson, nil, false
	}
	others := slices.DeleteFunc(slices.Clone(partial), func(ws *workState) bool { return slices.Contains(variants, ws) })
	if len(variants) == 1 && len(loose) == 0 && len(others) == 0 {
		return variants[0], moves[0], byPerson, nil, false
	}
	related = slices.Concat(variants, loose, others)
	slices.SortFunc(related, func(a, b *workState) int { return strings.Compare(a.slug, b.slug) })
	return nil, nil, byPerson, slices.Compact(related), len(variants) > 1
}

// routeLibrivoxAuthorVariant decides a row librivoxCandidates found no agreeing
// work for: one variant (and nothing else the row resembles) is an attach,
// with the credits moved onto the work's people (splitLibrivoxMoves);
// anything else rule 1 found is a worklist skip (several-works for two
// variants, else author-spelling). ok=false leaves the row to the rules after
// it.
func (p *planner) routeLibrivoxAuthorVariant(b sourceBook, workTitle string, slugs []string, lang string, authors workAuthors, credits []credit, partial []*workState) (librivoxRoute, bool) {
	ws, move, byPerson, related, several := p.soleAuthorVariant(b, slugs, lang, authors, credits, partial)
	switch {
	case ws != nil:
		moves, kept := p.splitLibrivoxMoves(move)
		return librivoxRoute{walk: &workWalk{title: workTitle, ws: ws}, moves: moves,
			authorVariant: fmt.Sprintf("%d %q: %s -> %q", b.lv.id, workTitle, p.moveText(byPerson, moves, kept), ws.slug)}, true
	case len(related) == 0:
		return librivoxRoute{}, false
	}
	reason := LibrivoxAuthorSpelling
	if several {
		reason = LibrivoxSeveralWorks
	}
	p.noteLibrivoxWorklist(b, workTitle, reason, candidatesOf(related))
	return librivoxRoute{skip: true}, true
}

// splitLibrivoxMoves splits an attach's move (row person -> catalogued
// person): a credit whose own person is already a record is kept - a
// catalogued person, the person a known LibriVox author id carries, or one
// this run created - since moving it would move the credit, and with it its
// LibriVox id, between two records of one surname, which is a person merge
// for a maintainer to make; it stays an unrecorded credit of the work the row
// attaches to (attachRowAuthors), and its id stays on its own person. The rest
// are moved; moves is nil when none is.
func (p *planner) splitLibrivoxMoves(move map[string]string) (moves map[string]string, kept []string) {
	for from, to := range move {
		if _, live := p.livePerson(from); live {
			kept = append(kept, from)
			continue
		}
		if moves == nil {
			moves = map[string]string{}
		}
		moves[from] = to
	}
	slices.Sort(kept)
	return moves, kept
}

// noteLibrivoxAuthorVariant counts one row rule 1 attached or moved, with
// its example.
func (p *planner) noteLibrivoxAuthorVariant(example string) {
	p.summary.LibrivoxAuthorVariants++
	if len(p.lv.authorVariantExamples) < maxWarnExamples {
		p.lv.authorVariantExamples = append(p.lv.authorVariantExamples, example)
	}
}

// keyLibrivoxAuthorVariants takes rule 1 per LibriVox AUTHOR ID, before any row
// is planned, for the case it can decide once for the whole batch: a row whose
// author is a spelling of a CATALOGUED person on a same-titled catalogued work.
// The id is then that person, so every credit of it in the batch - the row
// that attaches and the id's other projects, whose works would otherwise
// credit a second record of the same person - carries that person
// (librivoxCredit.person), as a catalogued id carries its holder. The rows are
// then planned like any other.
//
// Only an id no person carries yet, whose decided person is no record yet
// either (splitLibrivoxMoves: moving every project of the id off an
// existing record is a person merge), onto a person no other LibriVox
// author id holds - catalogued, or decided onto it for this run
// (heldByOtherAuthorID: "Henry Adams" 42 resolved onto a catalogued id-less
// `henry-adams` keeps "Henry Brooks Adams" 7030 off it, in one run as in two)
// - so the stamp can key the person by it, only when every row proposing the
// id names the same person, and only for the first id (in project order)
// proposing that person: anything else is left to the
// per-row rule (routeLibrivoxAuthorVariant), which still attaches the row it
// decides. A row proposes only what routeLibrivox would decide for it: one the
// planner admits (a language and a reader), and one the ordinary walk does not
// already resolve (a LibriVox row states no series, so it has no claim). The
// title index holds the catalogue alone at this point, so the decision does
// not depend on the order the rows arrive in.
func (p *planner) keyLibrivoxAuthorVariants(books []sourceBook, titles, suffixes []string) {
	proposed := map[int]string{}
	conflicting := map[int]bool{}
	// order is the proposing ids in project order of their first proposal.
	var order []int
	propose := func(b *sourceBook, move map[string]string) {
		for _, c := range b.lv.credits {
			if c.id <= 0 || c.kind != librivoxAuthor {
				continue
			}
			to, ok := move[p.librivoxCreditPerson(c)]
			if !ok {
				continue
			}
			prev, seen := proposed[c.id]
			if (seen && prev != to) || p.librivoxCreditIsRecord(c) {
				conflicting[c.id] = true
			}
			if !seen {
				order = append(order, c.id)
			}
			proposed[c.id] = to
		}
	}
	for _, i := range p.librivoxRows(books, lvPlannedRows) {
		b := &books[i]
		if _, held := p.librivoxSameNameOf(*b); held {
			continue // a same-name-author worklist skip (librivoxpeople.go rule 2)
		}
		lang, ok := b.language()
		if !ok || len(p.rowNarratorNames(*b)) == 0 {
			continue
		}
		credits, dropped := rowAuthorCreditSplitIn(p.credits, *b)
		noAuthor := p.unsluggableLeftNoAuthor(credits, dropped)
		// A decision places its row, not rule 1 (librivoxreviewed.go), but an attach's
		// moves are rule 1's answer about the id, so they key it for the batch as well.
		if r, decided := p.lv.reviewed[b.lv.id]; decided {
			if !noAuthor && reviewedTitleHolds(r, b.lv.title) {
				live, _ := p.liveReviewedMoves(r)
				propose(b, live)
			}
			continue
		}
		slugs, _, nonLatin := p.librivoxTitleWorks(titles[i])
		if noAuthor || len(credits) == 0 || len(slugs) == 0 {
			continue
		}
		authors := p.rowWorkAuthorsRO(credits)
		if !nonLatin && p.resolveWork(titles[i], b.str("title"), b.qualifiedTitle, suffixes[i], authors, lang, nil).ws != nil {
			continue
		}
		matched, partial := p.librivoxCandidates(slugs, lang, authors)
		if len(matched) > 0 {
			continue
		}
		if ws, move, _, _, _ := p.soleAuthorVariant(*b, slugs, lang, authors, credits, partial); ws != nil {
			propose(b, move)
		}
	}
	// Only the first id (in project order) still proposing a person keeps it: a
	// later one proposing the same person is left to the per-row rule, which
	// refuses it as a later run would once the first is stamped. An id refused
	// for any other reason keys nothing, so it never keeps a later id off a
	// person a later run would key it onto.
	keyed := map[string]bool{}
	for _, id := range order {
		to := proposed[id]
		if _, held := p.lv.people[librivoxKey{librivoxAuthor, id}]; held || conflicting[id] || keyed[to] || p.heldByOtherAuthorID(to, id) {
			delete(proposed, id)
			continue
		}
		keyed[to] = true
	}
	if len(proposed) == 0 {
		return
	}
	for id, to := range proposed {
		p.lv.authorPins[id] = pinAt(to, p.people[to])
	}
	for i := range books {
		b := &books[i]
		if b.lv == nil || p.lv.ids[b.lv.id] {
			continue
		}
		if _, held := p.librivoxSameNameOf(*b); held {
			continue
		}
		var moved []string
		for j, c := range b.lv.credits {
			if c.kind != librivoxAuthor || proposed[c.id] == "" {
				continue
			}
			pin := p.lv.authorPins[c.id]
			if c.person == pin {
				continue
			}
			from := c.name
			if c.person != nil {
				from = c.person.spelled
			}
			if from != pin.spelled {
				moved = append(moved, fmt.Sprintf("%q credited as %q", from, pin.spelled))
			}
			p.setLibrivoxPerson(b, j, pin)
		}
		if len(moved) > 0 {
			slices.Sort(moved)
			moved = slices.Compact(moved)
			// Counted once the row lands (noteLibrivoxLanded), as a routed
			// attach is.
			if p.lv.keyedVariants == nil {
				p.lv.keyedVariants = map[int]string{}
			}
			p.lv.keyedVariants[b.lv.id] = fmt.Sprintf("%d %q: %s", b.lv.id, titles[i], strings.Join(moved, ", "))
		}
	}
}

// librivoxCreditPerson is the person slug a LibriVox credit names as the row's
// credits resolve it: an author credit's decided person, a reader id's
// decided person, else its name's resolution ("" when it cleans to no single
// name).
func (p *planner) librivoxCreditPerson(c librivoxCredit) string {
	if c.person != nil {
		return p.resolveCredit(credit{person: c.person}).slug
	}
	if c.kind == librivoxReader {
		if slug, ok := p.lv.readers[c.id]; ok && c.id > 0 {
			return slug
		}
	}
	if name := p.soleCreditName(c); name != "" {
		return p.resolvePerson(name).slug
	}
	return ""
}

// librivoxCreditIsRecord reports whether a LibriVox credit's person is already
// a record (catalogued, or created this run).
func (p *planner) librivoxCreditIsRecord(c librivoxCredit) bool {
	_, ok := p.librivoxCreditSlug(c)
	return ok
}

// soleCreditName is the one name a LibriVox credit cleans to, "" when it
// cleans to none or several.
func (p *planner) soleCreditName(c librivoxCredit) string {
	if names := p.librivoxCreditNames(c); len(names) == 1 {
		return names[0]
	}
	return ""
}

// moveText is an attach's move as the run's note prints it (byPerson the
// row's credits by person), sorted, then the credits it kept
// (splitLibrivoxMoves): "Henrietta Elizabeth Marshall" credited as
// "Henrietta Marshall", "Sheridan Le Fanu" kept (a person record).
func (p *planner) moveText(byPerson map[string]credit, moves map[string]string, kept []string) string {
	moved := make(map[string]string, len(moves))
	for from, to := range moves {
		moved[byPerson[from].name] = p.people[to]
	}
	keptNames := make([]string, 0, len(kept))
	for _, from := range kept {
		keptNames = append(keptNames, byPerson[from].name)
	}
	slices.Sort(keptNames)
	parts := make([]string, 0, len(moved)+len(keptNames))
	for _, k := range slices.Sorted(maps.Keys(moved)) {
		parts = append(parts, fmt.Sprintf("%q credited as %q", k, moved[k]))
	}
	for _, k := range keptNames {
		parts = append(parts, fmt.Sprintf("%q kept (a person record)", k))
	}
	return strings.Join(parts, ", ")
}

// moveLibrivoxAuthors applies an attach's move (row person -> work person)
// to the row: its author credits (a copy, returned) and its LibriVox credits,
// so the people stamp reads the catalogued person the row now credits.
func (p *planner) moveLibrivoxAuthors(b sourceBook, credits []credit, moves map[string]string) []credit {
	out := slices.Clone(credits)
	for i := range out {
		if to, ok := moves[p.resolveCredit(out[i]).slug]; ok {
			out[i].name, out[i].person = p.people[to], pinAt(to, p.people[to])
		}
	}
	for i := range b.lv.credits {
		c := &b.lv.credits[i]
		if c.kind != librivoxAuthor {
			continue
		}
		if to, ok := moves[p.librivoxCreditPerson(*c)]; ok {
			// Not setLibrivoxPerson: the row is being planned, and every
			// later reader of its credits reads out, not the row's cache.
			c.person = pinAt(to, p.people[to])
		}
	}
	return out
}

// reportLibrivoxAuthorVariants is the run's one note about the rows rule 1
// attached, with a few examples: each one's LibriVox spelling is recorded only
// here.
func (p *planner) reportLibrivoxAuthorVariants() {
	if n := p.summary.LibrivoxAuthorVariants; n > 0 {
		p.summary.Notes = append(p.summary.Notes, withExamples(
			fmt.Sprintf("%d LibriVox project(s) credit an author LibriVox spells with more or fewer words than the author of a same-titled work (same surname), or share that author's LibriVox id; credited to that person instead, unless LibriVox's spelling is a person record of its own (kept)", n),
			p.lv.authorVariantExamples))
	}
}

// librivoxSlotTaken is the reason a credit not recorded carries when its name
// slugs to nothing and its LibriVox author id's librivox-author-<id> slot
// another person or a tombstone holds (librivoxpeople.go rule 4), so the id
// could not file it (reportUnsluggable).
const librivoxSlotTaken = "librivox-author-slot-taken"

// librivoxSlotTakenCredit reports whether a dropped credit of row b carries an
// author id whose librivox-author-<id> slot was taken (librivoxSlotTaken).
func (p *planner) librivoxSlotTakenCredit(b sourceBook, dropped []credit) bool {
	if b.lv == nil {
		return false
	}
	for _, c := range dropped {
		if _, taken := p.lv.slotTaken[p.librivoxAuthorIDOf(b, c, p.resolveCredit(c))]; taken {
			return true
		}
	}
	return false
}
