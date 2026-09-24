package importer

import (
	"slices"
	"strings"
)

// suffixpiece.go keeps a post-nominal from being minted as a person when a
// credit LIST is split. Commas separate people in every joined credit field
// this project reads, and a credit that carries its own comma splits in two:
// issue #2320's library export credited "David Posen, MD" (author) and
// "Anthony Rao, Ph.D." (narrator), and the split minted a person "Md" credited
// as an author and credited the junk record `ph-d` as a narrator. A comma-piece
// that is NOTHING BUT a credential, generational or legal-entity suffix names
// nobody - it belongs to the name before it.
//
// Two shapes, two answers, decided by whether the list's ORDER is the source's:
//
//	a JOINED string (every user-library export, the audiosilo-books envelope, a
//	    tag pkg/scan reads, an issue-form field) states its order, so the piece
//	    REJOINS its predecessor as the source spelled it ("David Posen, MD"). The
//	    cleaning then peels that ", MD" tail off (peelSuffixChunk), cleans the
//	    name before it - so a role qualifier in front of the post-nominal is still
//	    read, "Jane Doe (translator), PhD" - and puts the suffix back on the NAME:
//	    "David Posen MD", the slug the un-commaed spelling always minted.
//	    credential.go then folds a doctorate or licensure post-nominal onto a bare
//	    twin the census already holds, while a generational "Jr." or a legal
//	    "Inc." stays part of the name. A suffix piece with NO predecessor (a list
//	    that opens with one) is DROPPED: there is nobody to attach it to.
//	a TYPED list (libex rows) is DROPPED piecewise. libex split its own credits
//	    on commas upstream, and its lists carry no order: the export query
//	    aggregates them with no ORDER BY, and on B002V1OQ7O the stranded "Ph.D"
//	    arrives AHEAD of every author (the seed's `ph-d` first author of
//	    the-new-york-times-pocket-mba-ph-d). Attaching it to a neighbour would
//	    state a credential about somebody the source never said held it.
//
// Either way a piece is dropped ONLY beside a real credit. A side made of
// nothing but suffix-shaped pieces keeps them exactly as it did before this rule
// - "Ii" is a Japanese surname, and a row whose only narrator is "IV" would
// otherwise be left with no narrator at all.
//
// Measured over the full 1.13M-book libex dump: the dump holds almost no comma
// inside a credit name (ONE name carries a ", Ph.D." tail), because the upstream
// split already happened - so its residue IS the measurement. 29 credits on 26
// books are a lone piece this vocabulary matches (MSN 4, Ph.D 3, III 3, PhD 3,
// LMFT 2, and one each of FACR, FACP, FACHE, CPNP, LCSW, MFT, M.S.W., M. D.,
// Ed.D., D. Min, Jr, Jr., Inc., Ltd.), every one of them a suffix of a
// neighbouring credit, and none is the only credit on its side, so no book is
// left without an author or a narrator. The catalogue carries six of them as
// person records (`ph-d`, `msn`, `iii`, `jr` plus the two below this rule
// declines), which is a data repair, not this rule's job.
//
// The vocabulary is post-nominals the dump spells as a trailing credential
// (last-token counts over the names of three or more words: Jr. 897, MD 846,
// Ph.D. 354, III 257, M.D. 233, II 172, Sr. 107, LLC 98, MBA 96, LCSW 82, LMFT
// 52, LPC 50, Esq. 43, MPH 41, IV 41, Inc. 39, MSW 25, CPA 21, LMHC 16, DVM 16,
// MFT 14, ABPP 12, DDS 12, LICSW 12, Ltd. 11, MSN 10, BSN 10, FAAP 10, CFP 9,
// RDN 9, IBCLC 9, NCC 9, LCPC 8) plus the four seen ONLY as a stranded piece
// (FACP, FACR, FACHE, CPNP). It reuses the two vocabularies neighbouring rules
// already hold rather than restating them: every post-nominal credential.go
// folds (foldCredentials, the doctorates and the licensure tier) and the
// legal-entity suffixes the studio-tail rule knows (corporateLegalSuffix); the
// generational suffixes are this file's own, being the ones no fold may touch.
// The list is hand-mirrored in site/src/lib/import-parse.ts (the /import
// preview's splitter), and the two must be kept in step. It DECLINES every token that is also a name or
// a pair of initials, measured: "DC" (1 stranded chiropractor credit - and the
// catalogue's `dc` is DC Comics, credited as the author of Superman titles),
// "MA"/"M.A." (3), "J.D." (2), "D.C." (1), "DO." (1), "MS", "RN", "JD", "ND",
// "RD", "SJ" and "OP" - credential.go's initials-shaped ambiguity. And the
// author "Ed" on 6 contes à croquer is a real credit spelled like the front
// half of "Ed. D.": tokens are matched whole, so "Ed" is untouched. The declined
// pieces keep minting what they minted before; a wrong join is worse than a
// stray record.
var generationalSuffixes = map[string]bool{
	"jr": true, "jr.": true, "sr": true, "sr.": true, "ii": true, "iii": true, "iv": true,
}

// suffixPieceSpellings is the whole vocabulary, in credentialKey form: every
// post-nominal credential.go folds (foldCredentials), the generational suffixes
// it must never fold, and the studio-tail rule's legal-entity suffixes.
var suffixPieceSpellings = func() map[string]bool {
	out := map[string]bool{}
	for _, m := range []map[string]bool{foldCredentials, generationalSuffixes, corporateLegalSuffix} {
		for k := range m {
			out[k] = true
		}
	}
	return out
}()

// suffixTail is credential.go's matcher over this file's vocabulary.
var suffixTail = newTailVocab(suffixPieceSpellings)

// isSuffixPiece reports whether one piece of a split credit list is nothing but
// suffixes - one ("MD", "Ph. D.") or several stacked in one piece ("MD PhD"):
// it strips listed spellings off the end until nothing is left, and fails at the
// first trailing token that is not one, which for an ordinary name is its last.
func isSuffixPiece(piece string) bool {
	rest, ok := suffixTail.cut(piece)
	for ok {
		if strings.TrimSpace(rest) == "" {
			return true
		}
		rest, ok = suffixTail.cut(rest)
	}
	return false
}

// mergeSuffixPieces is the JOINED-list rule, over a split list in the source's
// order: every suffix-only piece rejoins the piece before it ("David Posen",
// "MD" -> "David Posen, MD"), and one with nothing before it is dropped. It
// works IN PLACE - the caller owns pieces.
//
// A list with NO real credit in it is returned untouched, because dropping is
// only safe beside a real credit: "Ii" is a Japanese surname, and a side whose
// one credit reads like a suffix keeps it exactly as it was split before this
// rule, rather than leaving the book with nobody on that side. That case writes
// nothing into pieces (an append only ever copies a real credit), so the input
// is still whole to hand back.
func mergeSuffixPieces(pieces []string) []string {
	out := pieces[:0]
	for _, p := range pieces {
		switch {
		case !isSuffixPiece(p):
			out = append(out, p)
		case len(out) > 0:
			out[len(out)-1] += ", " + p
		}
	}
	if len(out) == 0 {
		return pieces
	}
	return out
}

// hasRealCredit is what the TYPED-list rule asks first (libexNames): a
// list keeps its suffix-shaped entries when none of them is anything else.
func hasRealCredit(names []string) bool {
	for _, n := range names {
		if !isSuffixPiece(n) {
			return true
		}
	}
	return false
}

// peelSuffixChunk splits a credit that ends in one or more ", <suffix>" chunks
// into the name before them and the suffixes, space-joined ("Jane Doe
// (translator), MD, PhD" -> "Jane Doe (translator)", "MD PhD"). The cleaning
// runs on the name alone and the suffixes go back on the NAME afterwards, so a
// role qualifier the source wrote in front of the post-nominal - a dash or a
// bracket, both of which the qualifier rules read at the END of the credit - is
// still read. It is the mechanism "X - editor Jr." already uses, one step
// earlier. A credit with no such tail returns suffix "" and costs one byte scan.
func peelSuffixChunk(name string) (head, suffix string) {
	head = name
	var parts []string
	for {
		i := strings.LastIndexByte(head, ',')
		if i < 0 || !isSuffixPiece(head[i+1:]) {
			break
		}
		rest := strings.TrimSpace(head[:i])
		if rest == "" {
			break
		}
		parts = append(parts, strings.TrimSpace(head[i+1:]))
		head = rest
	}
	if len(parts) == 0 {
		return name, ""
	}
	slices.Reverse(parts)
	return head, strings.Join(parts, " ")
}
