package importer

import "strings"

// honorific.go implements the honorific-prefix merge: "Sir Arthur Conan Doyle"
// and "Arthur Conan Doyle" are one person, and the catalogue held both.
//
// It is the third rule of its family, after the initials probe (initials.go)
// and the studio-tail rule (studiotail.go), and it shares their posture: a
// spelling variant is resolved onto the record that already exists, decided
// against the catalogue PLUS this batch, and never the other way round. Twenty
// such pairs were measured across the tree, four of them minted by seed wave 5.
//
// The rule is deliberately timid, in three ways that all matter.
//
// FIRST, it only fires when the de-honorified name is ALREADY a credit
// somewhere - a credit census like the one the studio-tail rule's third tier
// consults. "Dr. Seuss" has no bare "Seuss" and "Mr. Lawrence" (a SpongeBob
// stage name) has no bare "Lawrence", so both survive untouched. Measured over
// the full dump, 2,298 honorific-prefixed credit names have a bare twin and
// 3,868 do not; the 3,868 are what this clause protects.
//
// FIRST-AND-A-HALF, that census is the SAME-SIDE one (creditCensus.sameSide):
// the bare twin must be credited as an author to attest an author, as a
// narrator to attest a narrator. The any-side census the studio rule uses looks
// like more evidence and is in fact the wrong evidence, because the two sides
// are different populations of humans: "Steve West" has 285 NARRATIONS and "Dr.
// Steve West" wrote one book, and they are not the same man. Seven such pairs
// were measured - "Sir Richard Burton" the Victorian translator against Richard
// Burton the actor is the clearest - and the side test refuses all seven while
// costing none of the 41 correct merges, because a courtesy title and its bare
// spelling are, in every real pair, credited for the same kind of work.
//
// SECOND, the bare name must be at least two words. The census answers "does
// this STRING exist as a credit", not "is it the same person", and a one-word
// remainder is where those two questions come apart: "Mr. Peter" would strip to
// "Peter" and "Saint Germain" to "Germain", both of which exist as credits and
// neither of which is the same person. Requiring a full name costs nothing on
// the real targets - every measured pair worth merging ("Sir Arthur Conan
// Doyle", "Dr. Wayne W. Dyer", "Sir P. G. Wodehouse", "Dame Judi Dench") has
// two or more words left - and it is a second, independent guard on the stage
// names the census clause already protects.
//
// The honorific list is the brief's, not the dump's whole title vocabulary.
// "Lord", "St.", "Rev.", "Prof." and the military ranks are deliberately absent:
// they are frequently part of the name of record rather than a courtesy prefix
// ("Lord Dunsany" and "St. Augustine" ARE how those authors are published), and
// merging them is a maintainer's call on a name-by-name basis, not a mechanical
// rule.

// honorificPrefixes are the courtesy titles a credit may carry in front of an
// otherwise ordinary name. Each is matched case-insensitively at a word
// boundary, with an optional trailing period, and must be followed by
// whitespace - so a fused token like "Dr.Rebis" (44 of them in the dump) is not
// a prefixed name and is never touched.
var honorificPrefixes = []string{"sir", "dame", "dr", "mr", "mrs", "ms"}

// minDeHonorifiedWords is the smallest bare name the merge will accept. See the
// second guard in this file's header.
const minDeHonorifiedWords = 2

// deHonorified strips a leading courtesy title from a credit name, reporting
// whether one was there and left an acceptable full name behind.
func deHonorified(name string) (bare string, stripped bool) {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) <= minDeHonorifiedWords {
		return "", false
	}
	head := strings.ToLower(strings.TrimSuffix(fields[0], "."))
	for _, h := range honorificPrefixes {
		if head == h {
			return strings.Join(fields[1:], " "), true
		}
	}
	return "", false
}

// stripHonorific resolves an honorific-prefixed credit onto its bare spelling
// when the census has already seen that spelling. seen is the run's credit
// census (the catalogue as loaded plus every credit name in the batch); a nil
// census has seen nothing, so the rule never fires for a caller with no
// catalogue in hand - the same bootstrap honesty the studio-tail rule's third
// tier keeps, and what makes the public CleanCreditName leave a name alone.
func stripHonorific(name string, seen creditSeenFunc) string {
	if seen == nil {
		return name
	}
	bare, stripped := deHonorified(name)
	if !stripped || !seen(bare) {
		return name
	}
	return bare
}
