package importer

import (
	"slices"

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
// Two of the identity machinery's vetoes still release a pair, read so they do
// not undo the rule: the COLLECTION status of the two forms that matched
// (titlerule.SameCollectionStatus: a boxed set is not the volume it collects; read
// on the compared forms, since "Prequel to the Chaos Walking Trilogy" makes a
// collection of a held title whose main part is the book itself), and the stated
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
// ("Empires of EVE" beside a held "Empires of EVE: Volume 2"), and a bare title
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
		series := p.identity.SeriesNameOf(slug)
		held := identityForms(ws.title, series, p.identity.Key)
		if !row.names(held) {
			continue
		}
		if rowVolume.States {
			heldVolume := titlerule.StatementOf(ws.title, series, titlerule.WriterPolicy)
			if !heldVolume.States || !rowVolume.Agrees(heldVolume) {
				continue
			}
		}
		return true
	}
	return false
}

// titleForm is one spelling of a title with the identity key it reduces to.
type titleForm struct {
	title, series, key string
}

// titleForms are a title's full form and, when it has a subtitle, its main form
// (a zero main otherwise).
type titleForms struct {
	full, main titleForm
}

func identityForms(title, series string, key func(title, series string) string) titleForms {
	f := titleForms{full: titleForm{title, series, key(title, series)}}
	if m, ok := titlerule.MainTitle(title); ok {
		f.main = titleForm{m, series, key(m, series)}
	}
	return f
}

// names reports whether the row's forms (f) name the held work's (h): one of the
// three pairs reportedDuplicate compares keys alike, under the same collection
// status.
func (f titleForms) names(h titleForms) bool {
	for _, pair := range [][2]titleForm{{f.full, h.full}, {f.main, h.full}, {f.full, h.main}} {
		a, b := pair[0], pair[1]
		if a.key != "" && a.key == b.key && titlerule.SameCollectionStatus(a.title, a.series, b.title, b.series) {
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
