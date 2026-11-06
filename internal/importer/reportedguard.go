package importer

import (
	"regexp"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// reportedguard.go is the duplicate guard `libex-select --reported` applies to a
// STANDALONE (selectStandalone): a series-less row that would create a work is
// refused, as identity-duplicate, when a held work shares an author with it and
// the two titles key alike. GOVERNANCE.md ("The reported-ASIN lane") states it.
//
// WHY THE LANE NEEDS MORE THAN THE CREATE GUARD (dupidentity.go). That guard
// refuses only on a key both FULL titles reduce to and author sets that NEST (one
// contains the other, and the containing set is credited by the other side). A
// reported ASIN is mostly a new edition of a book the catalogue already holds, and
// a standalone has no series position to anchor its identity, so the guard's two
// gaps are the common case here, not the edge. The 2026-11-05 dry run minted both
// of its tranche's new works beside held ones: "Micro" credited to Michael
// Crichton alone, held with Crichton and Richard Preston (the sets nest, but the
// row does not credit the larger one, so neither the slug chain's matchWork nor
// IdentityAuthorsMatch sees one book), and "The New World" beside "The New World:
// Prequel to the Chaos Walking Trilogy" (a subtitle Clean does not read as
// decoration, so the keys differ and the probe never fires). A wider import of the
// same queue added "Infinite Jest" (held with a foreword writer as a second
// author) and "Ford County" beside "Ford County: Stories".
//
// THE RULE. A held work is the same book when it shares at least one identity
// author with the row (workState.authors against the row's read-only resolution,
// the people the import would credit), its language is compatible, and the
// identity keys (titlerule.IdentityTitleKey, through check.WorkIdentity.Key, the
// held side read against the series its own key was derived against) of one of
// these pairs are equal: the two full titles, the row's main title against the
// held full title, or the row's full title against the held main title. A main
// title is what precedes the first subtitle separator (titlerule.MainTitle). Two MAIN titles
// are not compared: "Dune: House Atreides" and "Dune: House Harkonnen" share their
// authors and their main title and are two books.
//
// A pair also keys alike when the two forms agree once their conjunctions are
// spelled one way (foldConjunctions: "&" and the German "und" read as "and", in the
// title and the series it is cleaned against). The identity key drops "&" and keeps
// "and", so the lane's first pull request minted "Daisy Jones and the Six" beside a
// held "Daisy Jones & The Six" (KodeStar/audiosilo-meta#2890). The fold is this
// guard's alone: titlerule.IdentityTitleKey and check.WorkIdentity.Key are shared by
// every import path and the census, and widening them is #2890's open question.
// It is a second comparison, never a replacement, so a pair the plain keys join
// stays joined ("Q&A" and "QA"), and "and" is kept as a word rather than dropped,
// so "War and Peace" does not name a held "War Peace". The spellings are the ones
// the catalogue holds as twins (30 English "&"/"and" pairs by one author, among
// them "Crime & Punishment" and "Angels & Demons", and two German "&"/"und": "Dr.
// Jekyll & Mr. Hyde", "Rumo & Die Wunder im Dunkeln"); "et", "y", "e", "+" and
// "'n'" had none.
//
// Two of the identity machinery's vetoes still release a pair, read so they do
// not undo the rule: the COLLECTION status of the two forms that matched
// (titlerule.SameCollectionStatus: a boxed set is not the volume it collects; read
// on the compared forms, since "Prequel to the Chaos Walking Trilogy" makes a
// collection of a held title whose main part is the book itself, and the same
// subtitle on the ROW names a held "The New World" just as well; so a subtitle
// reading as a collection releases neither side, and a boxed set beside its first
// volume, either way round, is refused with the accepted cost below), and the stated
// VOLUME of the two full titles, in the create guard's positive form: a row whose
// own title states a volume is a duplicate only of a held work whose title states
// the same one (VolumeStatement.Agrees alone reads one-sided silence as agreement,
// which would refuse "Circus of the Dead, Book 2" against a held "Circus of the
// Dead").
//
// A row the create path resolves to a held work (resolveWork) is untouched: it
// lands there as another recording or another ASIN, and minting nothing it needs
// no guard.
//
// REFUSING IS THE SAFE DIRECTION, and the rule is wider than the create guard on
// purpose. A refused row is memoized by the sync bot and comes back when its memo
// expires; a merged-in duplicate needs a repair wave. The accepted cost is a
// distinct book that shares a main title with a held work by the same author
// ("Empires of EVE" beside a held "Empires of EVE: Volume 2", or "Red Rising:
// The Complete Boxed Set" beside a held "Red Rising"), and a bare title
// naming several held volumes (the create guard's ambiguity veto lets it mint).
// Only works the catalogue HOLDS are compared: two rows of one tranche for one new
// book are regional siblings the import folds into one work, and the lane's batch
// gap (a decorated and a plain title of one new book in one tranche) is the create
// guard's run half's to close.

// reportedDuplicate reports whether a standalone would be a second record of a
// held work. ctx is the batch's credit context and workTitle the title the import
// resolves the row's work by (confirmBatch derives both as the import does).
func (p *planner) reportedDuplicate(ctx creditContext, b sourceBook, workTitle string) bool {
	if p.identity == nil || workTitle == "" {
		return false
	}
	authors := p.rowWorkAuthorsROIn(ctx, rowAuthorCreditsIn(ctx, b))
	if len(authors.identity) == 0 {
		return false
	}
	lang, _ := b.language()
	if p.resolveWork(workTitle, b.str("title"), b.qualifiedTitles, "", authors, lang, nil).ws != nil {
		return false
	}
	row := identityForms(workTitle, "", p.identity.Key)
	if row.full.key == "" && row.main.key == "" {
		return false
	}
	rowVolume := titlerule.StatementOf(workTitle, "", titlerule.WriterPolicy)
	for _, slug := range p.heldWorksOf(authors.identity) {
		ws := p.works[slug]
		if !langCompatible(ws.lang, lang) {
			continue
		}
		held, ok := p.heldForms[slug]
		if !ok {
			if p.heldForms == nil {
				p.heldForms = map[string]titleForms{}
			}
			held = identityForms(ws.title, p.identity.SeriesNameOf(slug), p.identity.Key)
			p.heldForms[slug] = held
		}
		if !row.names(held) {
			continue
		}
		if rowVolume.States {
			heldVolume := titlerule.StatementOf(ws.title, held.full.series, titlerule.WriterPolicy)
			if !heldVolume.States || !rowVolume.Agrees(heldVolume) {
				continue
			}
		}
		return true
	}
	return false
}

// titleForm is one spelling of a title with the identity key it reduces to, and
// the key of the same spelling with its conjunctions folded (equal to key when
// the title and series hold none to fold).
type titleForm struct {
	title, series, key, folded string
}

// titleForms are a title's full form and, when it has a subtitle, its main form
// (a zero main otherwise).
type titleForms struct {
	full, main titleForm
}

func identityForms(title, series string, key func(title, series string) string) titleForms {
	form := func(t string) titleForm {
		f := titleForm{title: t, series: series, key: key(t, series)}
		f.folded = f.key
		if ft, fs := foldConjunctions(t), foldConjunctions(series); ft != t || fs != series {
			f.folded = key(ft, fs)
		}
		return f
	}
	f := titleForms{full: form(title)}
	if m, ok := titlerule.MainTitle(title); ok {
		f.main = form(m)
	}
	return f
}

// conjunctionSpelling is every spelling foldConjunctions reads as "and": an
// ampersand with any space around it, and the German "und" as a whole word.
var conjunctionSpelling = regexp.MustCompile(`\s*&\s*|(?i:\bund\b)`)

// foldConjunctions spells a title's conjunctions as the word "and" (the file
// header says why the guard reads them alike, and why only the guard).
func foldConjunctions(s string) string {
	if !strings.Contains(s, "&") && !strings.Contains(strings.ToLower(s), "und") {
		return s
	}
	return conjunctionSpelling.ReplaceAllString(s, " and ")
}

// keyedAlike reports whether two forms reduce to one identity key, plainly or
// with their conjunctions folded.
func keyedAlike(a, b titleForm) bool {
	return (a.key != "" && a.key == b.key) || (a.folded != "" && a.folded == b.folded)
}

// names reports whether the row's forms (f) name the held work's (h): one of the
// three pairs reportedDuplicate compares keys alike, under the same collection
// status.
func (f titleForms) names(h titleForms) bool {
	for _, pair := range [][2]titleForm{{f.full, h.full}, {f.main, h.full}, {f.full, h.main}} {
		a, b := pair[0], pair[1]
		if keyedAlike(a, b) && titlerule.SameCollectionStatus(a.title, a.series, b.title, b.series) {
			return true
		}
	}
	return false
}

// heldWorksOf is every held work crediting any of the given identity authors, in
// slug order. The by-author index is built on first use from the works the load
// read, which the selector's planner never adds to, so it holds the catalogue
// alone.
func (p *planner) heldWorksOf(authors []string) []string {
	if p.heldByAuthor == nil {
		p.heldByAuthor = map[string][]string{}
		for slug, ws := range p.works {
			for a := range ws.authors {
				p.heldByAuthor[a] = append(p.heldByAuthor[a], slug)
			}
		}
	}
	var out []string
	for _, a := range authors {
		out = append(out, p.heldByAuthor[a]...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
