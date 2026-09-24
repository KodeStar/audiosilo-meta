package importer

import "strings"

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
//	    REJOINS its predecessor as the source spelled it ("David Posen, MD") and
//	    the ordinary cleaning decides what the name becomes: credential.go folds a
//	    doctorate onto a bare twin the census already holds, and otherwise the
//	    name keeps it - the same slug an un-commaed "David Posen MD" mints - while
//	    a generational "Jr." stays part of the name, as it always does. A suffix
//	    piece with NO predecessor (a list that opens with one) is DROPPED: there is
//	    nobody to attach it to, and it is never a person.
//	a TYPED list (libex rows) is DROPPED piecewise. libex split its own credits
//	    on commas upstream, and its lists carry no order: the export query
//	    aggregates them with no ORDER BY, and on B002V1OQ7O the stranded "Ph.D"
//	    arrives AHEAD of every author (the seed's `ph-d` first author of
//	    the-new-york-times-pocket-mba-ph-d). Attaching it to a neighbour would
//	    state a credential about somebody the source never said held it.
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
// already hold rather than restating them: every doctorate credential.go folds
// (academicCredentials) and the legal-entity suffixes the studio-tail rule
// knows (corporateLegalSuffix). It DECLINES every token that is also a name or
// a pair of initials, measured: "DC" (1 stranded chiropractor credit - and the
// catalogue's `dc` is DC Comics, credited as the author of Superman titles),
// "MA"/"M.A." (3), "J.D." (2), "D.C." (1), "DO." (1), "MS", "RN", "JD", "ND",
// "RD", "SJ" and "OP" - credential.go's initials-shaped ambiguity. And the
// author "Ed" on 6 contes à croquer is a real credit spelled like the front
// half of "Ed. D.": tokens are matched whole, so "Ed" is untouched. The declined
// pieces keep minting what they minted before; a wrong join is worse than a
// stray record.
var suffixPieceExtras = []string{
	// Generational - part of the NAME, which is why they are here and never in
	// academicCredentials.
	"jr", "jr.", "sr", "sr.", "ii", "iii", "iv",
	// Licensure and fellowship.
	"mba", "lcsw", "licsw", "lmft", "lpc", "lcpc", "lmhc", "mft", "msw", "m.s.w.",
	"esq", "esq.", "mph", "cpa", "cfp", "dvm", "dds", "abpp", "msn", "bsn", "cpnp",
	"faap", "facp", "facr", "fache", "rdn", "ibclc", "ncc",
	// The spaced Doctor of Ministry the dump strands ("D. Min"). It is not in
	// academicCredentials because adding it there would widen credential.go's
	// fold too; that is its own decision.
	"d. min",
}

// suffixPieceSpellings is the whole vocabulary, in credentialKey form.
var suffixPieceSpellings = func() map[string]bool {
	out := make(map[string]bool, len(academicCredentials)+len(corporateLegalSuffix)+len(suffixPieceExtras))
	for cred := range academicCredentials {
		out[cred] = true
	}
	for legal := range corporateLegalSuffix {
		out[legal] = true
	}
	for _, s := range suffixPieceExtras {
		out[s] = true
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
// works IN PLACE - the caller owns pieces - and returns nil when nothing is left.
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
		return nil
	}
	return out
}
