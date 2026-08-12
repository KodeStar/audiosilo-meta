package importer

import "strings"

// credential.go implements the academic-credential merge: "Philip Zimbardo
// Ph.D." and "Philip Zimbardo" are one person, and the catalogue held both
// (philip-zimbardo-phd beside philip-zimbardo, emily-nagoski-ph-d beside
// emily-nagoski, john-m-gottman-ph-d, james-k-harter-ph-d - issue #1800).
//
// It is honorific.go's mirror image - the same merge from the other end of the
// name - and it keeps that rule's posture exactly: a spelling variant is
// resolved onto the record that already exists, decided against the catalogue
// PLUS this batch, and never the other way round.
//
// The three guards are honorific.go's three, and they matter for the same
// reasons:
//
// FIRST, the de-credentialed name must ALREADY be a credit somewhere - the
// credit census. Measured over the full 1.13M-book dump, 3,703 credit names end
// in one of the credential spellings below and 1,054 of them have a bare twin
// credited on the same side; the other 2,649 (Dr.-style publishing names whose
// bare form nobody has ever been credited under) are what this clause protects.
//
// SECOND, that census is the SAME-SIDE one (creditCensus.sameSide), for
// honorific.go's reason verbatim: the author and narrator sides are different
// populations of humans, and a narrator sharing a surname with a credentialed
// author is not evidence about the author.
//
// THIRD, the bare name must be at least two words, so a credential stacked on a
// mononym ("Rahim MD") can never resolve onto a one-word credit that is a
// different person.
//
// The vocabulary is the DOCTORATES, and only those. The dump's licensure tier
// (mba 107 names / 26 same-side twins, lcsw 91/12, rn 69/17, lpc 61/7, lmft
// 59/5, jd 51/8, esq 61/9, msw 27/4, cpa 22/5, lmhc 20/2, dvm 19/6, msc 14/3,
// mph 49/0, dds 15/0 and a dozen more - 585 names / ~110 twins across 28
// spellings) is measured and deliberately NOT here: it is a bigger and more
// heterogeneous population, and two of its commonest entries are initials-shaped
// ("J. D.", "R. N." are somebody's initials as readily as a credential), which
// is exactly the ambiguity a fold must not carry. Each spelling is a one-line
// addition on this measurement if a maintainer wants it.
//
// NOTE the deliberate overlap with credentialTitles (mapping.go). The two lists
// look alike and do different jobs: that one TRIMS a credential off a captured
// role qualifier so the role can match its own vocabulary entry, and hands the
// trimmed words back to the name; this one FOLDS a name onto its bare twin. It
// is why "jr."/"jr" are in that list and can never be in this one - a
// generational suffix is part of a person's identity, and merging "Theodore C.
// Van Alst Jr." onto "Theodore C. Van Alst" would merge a man with his father.

// academicCredentials are the trailing post-nominal spellings the merge reads.
// Keys are in foldCredit form (lowercased, diacritics folded, single-spaced),
// pinned by TestCredentialVocabulary, and may be one or two tokens - the dump
// spells the dotted forms both welded ("Ph.D.") and spaced ("Ph. D.").
//
// Counts are distinct dump credit names carrying that exact spelling, and the
// second number is how many of them have a bare same-side twin - the pairs this
// rule exists to merge. A spelling with no measured twin is still listed: it is
// an attested spelling of a listed family, and which spellings have a twin is an
// accident of the dump rather than a property of the form.
var academicCredentials = map[string]bool{
	// Doctor of Philosophy.
	"phd":    true, // 1,761 names / 482 twins
	"ph.d.":  true, // 398 / 149
	"ph.d":   true, // 69 / 25
	"ph. d.": true, // 9 / 6 - the spaced spelling
	"phd.":   true, // 7 / 3
	// Doctor of Medicine.
	"md":    true, // 961 / 248
	"m.d.":  true, // 272 / 84
	"m.d":   true, // 6 / 0
	"m. d.": true, // 3 / 3 - the spaced spelling
	"md.":   true, // 1 / 1
	// Doctor of Psychology.
	"psyd":   true, // 99 / 19
	"psy.d.": true, // 9 / 3
	"psy.d":  true, // 2 / 0
	// Doctor of Education.
	"edd":    true, // 55 / 14
	"ed.d.":  true, // 17 / 6
	"ed. d.": true, // 4 / 0 - the spaced spelling
	"ed.d":   true, // 2 / 1
	"ed d.":  true, // 2 / 0
	// Doctor of Ministry and of Theology.
	"dmin":   true, // 9 / 2
	"d.min.": true, // 3 / 1
	"thd":    true, // 7 / 4
	"th.d.":  true, // 5 / 2
}

// maxCredentialWords is the longest key credentialTail can reach: it probes one
// token, then two. A longer spelling would need a longer probe, which is what
// TestCredentialVocabulary pins rather than leaving to be discovered by a key
// that silently never matches.
const maxCredentialWords = 2

// credentialSecondWords are the FINAL words of the multi-word keys ("d." for
// "ph. d.", "m. d.", "ed. d."). Every credit name of every import goes through
// credentialTail, so the multi-word probe - which has to join and fold two
// tokens - is gated on the cheap single-token lookup this set answers. Derived
// from the vocabulary, so a new spaced spelling cannot be added without the
// probe learning to reach it.
var credentialSecondWords = func() map[string]bool {
	out := map[string]bool{}
	for cred := range academicCredentials {
		if words := strings.Fields(cred); len(words) > 1 {
			out[words[len(words)-1]] = true
		}
	}
	return out
}()

// minDeCredentialedWords is the smallest bare name the merge will accept. See
// the third guard in this file's header.
const minDeCredentialedWords = 2

// credentialTail reports how many TRAILING words of fields spell one listed
// credential. The spaced spellings are reached through credentialSecondWords, so
// "Ph. D." is read as one credential rather than leaving "Ph." behind - and the
// two-token probe only runs when the last token is one a spaced key can end on.
// A trailing comma is trimmed off the lookup key, because the dump punctuates
// the list ("John Doe, PhD, MD").
func credentialTail(fields []string) int {
	if len(fields) == 0 {
		return 0
	}
	last := credentialKey(fields[len(fields)-1])
	if academicCredentials[last] {
		return 1
	}
	if len(fields) >= 2 && credentialSecondWords[last] {
		if academicCredentials[credentialKey(fields[len(fields)-2])+" "+last] {
			return 2
		}
	}
	return 0
}

// credentialKey is one token in vocabulary form: folded, and stripped of the
// comma the source may have separated the list with.
func credentialKey(word string) string {
	return strings.TrimRight(foldCredit(word), ",")
}

// deCredentialed strips every trailing academic credential from a credit name,
// reporting whether one was there and left an acceptable full name behind. It
// strips REPEATEDLY because the dump stacks them ("DANIEL K. ASIEDU MD PhD",
// "Frank Lipman MD MD"), and it stops the moment stripping would take the bare
// name below the two-word floor.
//
// The separating comma the source may have used goes with the credential
// ("Philip Zimbardo, Ph.D." -> "Philip Zimbardo"): it punctuates the list, and
// leaving it on would make the bare name a different string from the twin the
// census holds.
func deCredentialed(name string) (bare string, stripped bool) {
	fields := strings.Fields(strings.TrimSpace(name))
	for {
		n := credentialTail(fields)
		if n == 0 || len(fields)-n < minDeCredentialedWords {
			break
		}
		fields = fields[:len(fields)-n]
		stripped = true
	}
	if !stripped {
		return "", false
	}
	bare = strings.TrimRight(strings.Join(fields, " "), " ,")
	if len(strings.Fields(bare)) < minDeCredentialedWords {
		return "", false
	}
	return bare, true
}

// stripCredential resolves a credentialed credit onto its bare spelling when the
// census has already seen that spelling. seen is the run's SAME-SIDE credit
// census; a nil census has seen nothing, so the rule never fires for a caller
// with no catalogue in hand - the bootstrap honesty honorific.go and the
// studio-tail rule's third tier both keep, and what makes the public
// CleanCreditName leave a name alone.
func stripCredential(name string, seen creditSeenFunc) string {
	if seen == nil {
		return name
	}
	bare, stripped := deCredentialed(name)
	if !stripped || !seen(bare) {
		return name
	}
	return bare
}
