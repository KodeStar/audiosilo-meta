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
//     catalogued person: the row's author credit is respelled with the
//     person's name, so the work's credit list is untouched and the person is
//     keyed by the row's LibriVox author id when the stamp's rules allow
//     (stampLibrivoxID) - unless LibriVox's spelling is already a person
//     record (catalogued, keyed by that id, or created earlier in the run),
//     which keeps the credit and its id (splitLibrivoxRespelling). The person
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
//  2. A NAME THAT SLUGS TO NOTHING IS CREDITED NOWHERE. An author or translator
//     credit in a script the slug rule folds away entirely ("Йордан Йовков")
//     would land on the one catch-all person record (model.UnslugPersonID),
//     which every such name shares. It is dropped from the row before the work
//     is decided and reported as a credit not recorded (reason unsluggable).
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
// are spellings of (variants, with the respelling of the row's credit names
// that attaching to it takes) and those they only loosely resemble. A
// spelling whose work author already carries a LibriVox author id other than
// the one row b states for that credit is loose: LibriVox's two ids may well
// be two people.
func (p *planner) librivoxAuthorVariants(b sourceBook, slugs []string, lang string, authors workAuthors, credits []credit) (variants []*workState, renames []map[string]string, loose []*workState) {
	names := map[string]string{}
	for _, c := range credits {
		if slug := p.resolvePerson(c.name).slug; names[slug] == "" {
			names[slug] = c.name
		}
	}
	for _, slug := range slugs {
		ws := p.works[slug]
		if ws == nil || !langCompatible(ws.lang, lang) || matchWork(ws, authors) != matchNone {
			continue
		}
		switch grade, pairs := p.librivoxAuthorPairing(ws, authors.identity, names); grade {
		case spellingVariant:
			rename := map[string]string{}
			for r, w := range pairs {
				rename[names[r]] = p.people[w]
			}
			if !p.respellingResolves(pairs) || p.pairsCarryOtherIDs(b, pairs, names) {
				loose = append(loose, ws)
				continue
			}
			variants = append(variants, ws)
			renames = append(renames, rename)
		case spellingLoose:
			loose = append(loose, ws)
		}
	}
	return variants, renames, loose
}

// respellingResolves reports whether every catalogued name a variant respells a
// credit with (pairs: row slug -> work person) resolves back to the person it
// was read from: a person whose id is not its name's slug (a LibriVox reader's
// `-librivox-<id>` record) cannot be credited by name, so such a pairing is
// never attached.
func (p *planner) respellingResolves(pairs map[string]string) bool {
	for _, w := range pairs {
		if got := p.resolvePerson(p.people[w]); got.create || got.fellBack || got.slug != w {
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
func (p *planner) pairsCarryOtherIDs(b sourceBook, pairs, names map[string]string) bool {
	for r, w := range pairs {
		name := names[r]
		if id := p.librivoxAuthorIDOf(b, name, p.resolvePerson(name)); id > 0 {
			if have := p.lv.authorOf[w]; have != 0 && have != id {
				return true
			}
		}
	}
	return false
}

// soleAuthorVariant is rule 1's attach test over the works under one title
// key that no agreeing work was found among (partial: those sharing a person
// with the row): the one work the row's authors are spellings of, when
// nothing else under the key resembles them, and the respelling attaching to
// it takes. Otherwise related lists every work rule 1 found, for a worklist
// row, and several says two or more of them are variants.
func (p *planner) soleAuthorVariant(b sourceBook, slugs []string, lang string, authors workAuthors, credits []credit, partial []*workState) (ws *workState, rename map[string]string, related []*workState, several bool) {
	variants, renames, loose := p.librivoxAuthorVariants(b, slugs, lang, authors, credits)
	if len(variants)+len(loose) == 0 {
		return nil, nil, nil, false
	}
	others := slices.DeleteFunc(slices.Clone(partial), func(ws *workState) bool { return slices.Contains(variants, ws) })
	if len(variants) == 1 && len(loose) == 0 && len(others) == 0 {
		return variants[0], renames[0], nil, false
	}
	related = slices.Concat(variants, loose, others)
	slices.SortFunc(related, func(a, b *workState) int { return strings.Compare(a.slug, b.slug) })
	return nil, nil, slices.Compact(related), len(variants) > 1
}

// routeLibrivoxAuthorVariant decides a row librivoxCandidates found no agreeing
// work for: one variant (and nothing else the row resembles) is an attach,
// with the credits respelled (splitLibrivoxRespelling); anything else rule 1
// found is a worklist skip (several-works for two variants, else
// author-spelling). ok=false leaves the row to the rules after it.
func (p *planner) routeLibrivoxAuthorVariant(b sourceBook, workTitle string, slugs []string, lang string, authors workAuthors, credits []credit, partial []*workState) (librivoxRoute, bool) {
	ws, rename, related, several := p.soleAuthorVariant(b, slugs, lang, authors, credits, partial)
	switch {
	case ws != nil:
		respell, kept := p.splitLibrivoxRespelling(rename)
		p.noteLibrivoxAuthorVariant(fmt.Sprintf("%d %q: %s -> %q", b.lv.id, workTitle, renameText(respell, kept), ws.slug))
		return librivoxRoute{walk: &workWalk{title: workTitle, ws: ws}, renames: respell}, true
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

// splitLibrivoxRespelling splits an attach's respelling (credit name ->
// catalogued name): a credit whose own spelling already names a person record
// is kept - a catalogued person, one keyLibrivoxCredits spelled by the LibriVox
// author id that person already carries, or one this run created - since
// respelling it would move the credit, and with it its LibriVox id, between two
// records of one surname, which is a person merge for a maintainer to make; it
// stays an unrecorded credit of the work the row attaches to
// (librivoxRowAuthors), and its id stays on its own person. The rest are
// respelled; respell is nil when none is.
func (p *planner) splitLibrivoxRespelling(rename map[string]string) (respell map[string]string, kept []string) {
	for from, to := range rename {
		if !p.resolvePerson(from).create {
			kept = append(kept, from)
			continue
		}
		if respell == nil {
			respell = map[string]string{}
		}
		respell[from] = to
	}
	slices.Sort(kept)
	return respell, kept
}

// noteLibrivoxAuthorVariant counts one row rule 1 attached or respelled, with
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
// credit a second record of the same person - is respelled with the person's
// name, as keyLibrivoxCredits respells a catalogued id. The rows are then
// planned like any other.
//
// Only an id no person carries yet, whose LibriVox spelling is no person
// record yet either (splitLibrivoxRespelling: moving every project of the id
// off an existing record is a person merge), onto a person carrying no
// LibriVox author id (so the stamp can key the person by it), and only when
// every row proposing the id names the same person: anything else is left to
// the per-row rule (routeLibrivoxAuthorVariant), which still attaches the row
// it decides. A row proposes only what routeLibrivox would decide for it: one
// the planner admits (a language and a reader), and one the ordinary walk does
// not already resolve (a LibriVox row states no series, so it has no claim).
// The title index holds the catalogue alone at this point, so the decision
// does not depend on the order the rows arrive in.
func (p *planner) keyLibrivoxAuthorVariants(books []sourceBook, titles, suffixes []string) {
	proposed := map[int]string{}
	conflicting := map[int]bool{}
	for i := range books {
		b := &books[i]
		if b.lv == nil || p.lv.ids[b.lv.id] {
			continue
		}
		if _, held := p.librivoxSameNameOf(*b); held {
			continue // a same-name-author worklist skip (librivoxpeople.go rule 2)
		}
		lang, ok := b.language()
		if !ok || len(p.rowNarratorNames(*b)) == 0 {
			continue
		}
		credits, _, noAuthor := p.dropUnsluggableLibrivoxCredits(p.rowAuthorCredits(*b))
		key, nonLatin := librivoxTitleKeyOf(titles[i])
		slugs := p.librivoxTitleIndex()[key]
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
		if ws, rename, _, _ := p.soleAuthorVariant(*b, slugs, lang, authors, credits, partial); ws != nil {
			for _, c := range b.lv.credits {
				from := p.soleCreditName(c)
				to, ok := rename[from]
				if !ok || c.id <= 0 || c.kind != librivoxAuthor {
					continue
				}
				if prev, seen := proposed[c.id]; (seen && prev != to) || !p.resolvePerson(from).create {
					conflicting[c.id] = true
				}
				proposed[c.id] = to
			}
		}
	}
	if len(proposed) == 0 {
		return
	}
	for id, to := range proposed {
		if _, held := p.lv.people[librivoxKey{librivoxAuthor, id}]; held || conflicting[id] || p.lv.authorOf[p.resolvePerson(to).slug] != 0 {
			delete(proposed, id)
		}
	}
	if len(proposed) == 0 {
		return
	}
	for i := range books {
		b := &books[i]
		if b.lv == nil || p.lv.ids[b.lv.id] {
			continue
		}
		if _, held := p.librivoxSameNameOf(*b); held {
			continue
		}
		var rename map[string]string
		for j := range b.lv.credits {
			c := &b.lv.credits[j]
			to, ok := proposed[c.id]
			if !ok || c.kind != librivoxAuthor || c.name == to {
				continue
			}
			if rename == nil {
				rename = map[string]string{}
			}
			rename[c.name] = to
			c.name = to
			b.authors[c.index] = to + c.suffix
		}
		if rename == nil {
			continue
		}
		if b.creditsCached {
			b.creditsCached = false
			b.authorCredits, b.creditsCached = rowAuthorCreditsIn(p.credits, *b), true
		}
		p.noteLibrivoxAuthorVariant(fmt.Sprintf("%d %q: %s", b.lv.id, titles[i], renameText(rename, nil)))
	}
}

// soleCreditName is the one name a LibriVox credit cleans to, "" when it
// cleans to none or several.
func (p *planner) soleCreditName(c librivoxCredit) string {
	if names := p.librivoxCreditNames(c); len(names) == 1 {
		return names[0]
	}
	return ""
}

// renameText is a respelling as the run's note prints it, sorted, then the
// credits it kept (splitLibrivoxRespelling): "Henrietta Elizabeth Marshall"
// credited as "Henrietta Marshall", "Sheridan Le Fanu" kept (a person record).
func renameText(rename map[string]string, kept []string) string {
	parts := make([]string, 0, len(rename)+len(kept))
	for _, k := range slices.Sorted(maps.Keys(rename)) {
		parts = append(parts, fmt.Sprintf("%q credited as %q", k, rename[k]))
	}
	for _, k := range kept {
		parts = append(parts, fmt.Sprintf("%q kept (a person record)", k))
	}
	return strings.Join(parts, ", ")
}

// respellLibrivoxAuthors applies an attach's respelling to the row: its author
// credits (a copy, returned) and its LibriVox credits, so the people stamp
// reads the catalogued person the row now credits.
func (p *planner) respellLibrivoxAuthors(b sourceBook, credits []credit, rename map[string]string) []credit {
	out := slices.Clone(credits)
	for i := range out {
		if to, ok := rename[out[i].name]; ok {
			out[i].name = to
		}
	}
	for i := range b.lv.credits {
		c := &b.lv.credits[i]
		if to, ok := rename[p.soleCreditName(*c)]; ok && c.kind == librivoxAuthor {
			c.name = to
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

// librivoxUnsluggable is the reason a credit not recorded carries when its
// name slugs to nothing (rule 2).
const librivoxUnsluggable = "unsluggable"

// dropUnsluggableLibrivoxCredits is rule 2: the row's author credits without
// those whose name slugs to nothing, and those dropped. noAuthor reports that a
// credit was dropped and no individual identity author is left, so the row
// must not land.
func (p *planner) dropUnsluggableLibrivoxCredits(credits []credit) (kept, dropped []credit, noAuthor bool) {
	for _, c := range credits {
		if _, fellBack := personSlug(c.name); fellBack {
			dropped = append(dropped, c)
		} else {
			kept = append(kept, c)
		}
	}
	if len(dropped) == 0 {
		return credits, nil, false
	}
	// identityAuthors falls back to the whole list when every credit is
	// role-qualified, so a translator alone would read as the identity: only a
	// person some kept credit names WITHOUT a role counts (an author who also
	// translated the book still does).
	bare := map[string]bool{}
	for _, c := range kept {
		if len(c.roles) == 0 {
			bare[p.resolvePerson(c.name).slug] = true
		}
	}
	for _, a := range p.rowWorkAuthorsRO(kept).identity {
		if bare[a] && individualAuthor(a) {
			return kept, dropped, false
		}
	}
	return kept, dropped, true
}

// unsluggableLibrivoxCredits is rule 2's report for a row that landed on work:
// each dropped credit, with the LibriVox author id its name carries.
func (p *planner) unsluggableLibrivoxCredits(b sourceBook, work string, dropped []credit) []LibrivoxUnrecordedCredit {
	out := make([]LibrivoxUnrecordedCredit, 0, len(dropped))
	for _, c := range dropped {
		out = append(out, LibrivoxUnrecordedCredit{
			Project:  b.lv.id,
			AuthorID: p.librivoxAuthorIDOf(b, c.name, p.resolvePerson(c.name)),
			Name:     c.name,
			Roles:    slices.Clone(c.roles),
			Work:     work,
			Reason:   librivoxUnsluggable,
		})
	}
	return out
}
