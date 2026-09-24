package importer

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

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
// The vocabulary is the doctorates (academicCredentials) plus the professional
// and licensure tier (licensureCredentials), and never a generational or
// legal-entity suffix. The licensure tier was first measured and declined here
// (585 names / ~110 twins across 28 spellings); it joined when issue #2320's
// comma-split repair (suffixpiece.go) started keeping "Jane Doe, LCSW" as ONE
// credit - the split had matched her existing `jane-doe` record, the rejoined
// name would otherwise mint `jane-doe-lcsw` beside it. Measured over the full
// dump, the widened spellings strip 678 credit names the doctorates alone did
// not, and 99 of them (217 book-credits) have a bare same-side twin and fold -
// "Eric Tyson MBA" (17 books), "Deb Dana LCSW", "Karen E. Mueller DVM", "Garrett
// Sutton Esq. Esq.", "Stan Tatkin PsyD MFT"; a hand review of the 99 found no
// shape the doctorate fold does not already carry (a common name meeting its
// bare twin is the census posture both tiers share). The initials-shaped entries
// stay OUT ("J. D.", "R. N.", "MA", "MS" are somebody's initials as readily as a
// credential - exactly the ambiguity a fold must not carry), so "Jane
// John-Nwankwo RN MSN" folds to "Jane John-Nwankwo RN", not further.
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

// licensureCredentials is the professional and licensure tier, folded under the
// same three guards as the doctorates - see this file's header. Every entry is a
// post-nominal the dump spells as a trailing credential (suffixpiece.go carries
// the counts); the initials-shaped ones are deliberately absent.
var licensureCredentials = map[string]bool{
	"mba": true, "lcsw": true, "licsw": true, "lmft": true, "lpc": true, "lcpc": true,
	"lmhc": true, "mft": true, "msw": true, "m.s.w.": true, "esq": true, "esq.": true,
	"mph": true, "cpa": true, "cfp": true, "dvm": true, "dds": true, "abpp": true,
	"msn": true, "bsn": true, "cpnp": true, "faap": true, "facp": true, "facr": true,
	"fache": true, "rdn": true, "ibclc": true, "ncc": true,
	"d. min": true, // the spaced Doctor of Ministry the dump strands
}

// foldCredentials is every post-nominal the fold strips: the doctorates plus
// the licensure tier. Never a generational suffix and never a legal-entity one.
var foldCredentials = func() map[string]bool {
	out := make(map[string]bool, len(academicCredentials)+len(licensureCredentials))
	for _, m := range []map[string]bool{academicCredentials, licensureCredentials} {
		for k := range m {
			out[k] = true
		}
	}
	return out
}()

// maxCredentialWords is the longest key a tailVocab can reach: cut probes one
// token, then two. It bounds EVERY tail vocabulary (foldCredentials here,
// suffixPieceSpellings in suffixpiece.go); a longer spelling would need a longer
// probe, which the vocabulary tests pin rather than leaving to be discovered by
// a key that silently never matches.
const maxCredentialWords = 2

// tailVocab is the one trailing-post-nominal matcher, shared by this file's fold
// (which strips credentials off a name) and suffixpiece.go (which asks whether a
// split list piece is nothing BUT suffixes). keys are in credentialKey form;
// second holds the FINAL words of the multi-word keys ("d." for "ph. d.", "m.
// d.", "ed. d."). Every credit name of every import is probed, so the two-token
// probe - which has to fold a second token - is gated on the cheap single-token
// lookup second answers. It is derived from the keys, so a new spaced spelling
// cannot be added without the probe learning to reach it.
type tailVocab struct {
	keys   map[string]bool
	second map[string]bool
}

func newTailVocab(keys map[string]bool) tailVocab {
	second := map[string]bool{}
	for k := range keys {
		if words := strings.Fields(k); len(words) > 1 {
			second[words[len(words)-1]] = true
		}
	}
	return tailVocab{keys: keys, second: second}
}

// credentialTail is the fold's matcher.
var credentialTail = newTailVocab(foldCredentials)

// minDeCredentialedWords is the smallest bare name the merge will accept. See
// the third guard in this file's header.
const minDeCredentialedWords = 2

// cut strips ONE listed spelling off the end of s and returns what precedes it,
// or ok=false when s does not end in one. The spaced spellings are reached
// through the second-word gate, so "Ph. D." is read as one credential rather
// than leaving "Ph." behind. A trailing comma is trimmed off each lookup key,
// because the dump punctuates the list ("John Doe, PhD, MD").
//
// It reads from the END and folds only the tokens it needs - the last one
// always, the one before it only when the last is a registered second word - so
// an ordinary name is rejected by its last token alone, and without allocating
// when that token is ASCII.
func (v tailVocab) cut(s string) (rest string, ok bool) {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	i := afterLastSpace(s)
	if i == len(s) {
		return "", false
	}
	var lastBuf [48]byte
	last := appendCredentialKey(lastBuf[:0], s[i:])
	if v.keys[string(last)] {
		return s[:i], true
	}
	if !v.second[string(last)] {
		return "", false
	}
	head := strings.TrimRightFunc(s[:i], unicode.IsSpace)
	j := afterLastSpace(head)
	if j == len(head) {
		return "", false
	}
	var keyBuf [64]byte
	key := appendCredentialKey(keyBuf[:0], head[j:])
	key = append(append(key, ' '), last...)
	if v.keys[string(key)] {
		return head[:j], true
	}
	return "", false
}

// afterLastSpace is the byte offset where s's last whitespace-delimited token
// begins (0 when s holds no whitespace). It steps over the WHOLE separator rune:
// strings.Fields - which this matcher replaced - splits on every Unicode space,
// and a no-break space is two bytes, so a bare "+1" would start the token on a
// continuation byte and the credential after it ("Anthony Rao\u00a0PhD") would
// silently never match.
func afterLastSpace(s string) int {
	i := strings.LastIndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return 0
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return i + size
}

// credentialKey is one token in vocabulary form: folded, and stripped of the
// comma the source may have separated the list with.
func credentialKey(word string) string {
	return strings.TrimRight(foldCredit(word), ",")
}

// appendCredentialKey appends credentialKey(word) to dst. An ASCII token - which
// is every post-nominal and nearly every name's last word - is lowercased
// straight into dst, which is all foldCredit does to one; anything else takes
// the full fold.
func appendCredentialKey(dst []byte, word string) []byte {
	start := len(dst)
	for k := 0; k < len(word); k++ {
		c := word[k]
		if c >= utf8.RuneSelf {
			return append(dst[:start], credentialKey(word)...)
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	for len(dst) > start && dst[len(dst)-1] == ',' {
		dst = dst[:len(dst)-1]
	}
	return dst
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
	s := strings.TrimSpace(name)
	for {
		rest, ok := credentialTail.cut(s)
		if !ok || len(strings.Fields(rest)) < minDeCredentialedWords {
			break
		}
		s, stripped = rest, true
	}
	if !stripped {
		return "", false
	}
	bare = strings.TrimRight(strings.Join(strings.Fields(s), " "), " ,")
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
	return stripOntoSeenTwin(name, seen, deCredentialed)
}
