package importer

import (
	"regexp"
	"strings"
	"sync"
)

// studiotail.go is the fourth step of CreditWithRoles' bounded fixpoint: it
// removes a STUDIO or PRODUCTION credit a source concatenated onto a person's
// name, so "Alex Hyde-White Punch Audio" imports as the narrator Alex Hyde-White
// (with Punch Audio keeping the record of its own it already has) instead of
// minting a third identity that is neither.
//
// Every rule here is evidence-driven, measured over the full 1.13M-book libex
// dump (the credit universe: 481,309 author+narrator credit rows over 436,101
// distinct name strings). The naive form of this rule - "a multi-word name whose
// final token is a studio word is a concatenation" - matches 1,736 names and
// 154,336 credits, and it is WRONG: 88.4% of those are single entities (real
// surnames like John Voce / Barry Press / Katherine Press, and whole corporate
// credits like "Innovative Language Learning LLC"). What separates the two is
// whether the string's PERSON half is independently a credit somewhere else.
//
// So the rule is three tiers, ordered by how much evidence the string itself
// carries:
//
//	tier 1  a closed multi-word ROLE-LABEL tail ("Aery Talento de Voz"). The
//	        tail is a job title, never an entity name, so the string is its own
//	        evidence and no attestation is needed.
//	tier 2  "<person, 2+ tokens> for <tail ending in a studio word>". Measured
//	        97% precision over the 79 dump names carrying it; the `for`
//	        connector between a name and a production house is unambiguous.
//	tier 3  everything else - the of/at/with/by/from connectors, a
//	        whitespace-delimited dash, ( ) [ ] / | separators, and the bare
//	        concatenation with no boundary marker at all. Here the string
//	        carries no evidence, so the PERSON half must be independently
//	        attested as a credit; for the bare form the removed TAIL must be
//	        attested too, because both halves being real credits is the only
//	        thing that distinguishes "Alex Hyde-White Punch Audio" from "Walt
//	        Disney Company Ltd." (whose "Walt Disney Company" is not a credit -
//	        cutting it would fabricate a name nobody wrote).
//
// Measured over the whole dump the three tiers clean 139 names / 529 credits,
// against the naive rule's 1,736 / 154,336.
//
// Guards that killed earlier drafts, each pinned by a test in studiotail_test.go:
//
//   - The dash separator must be WHITESPACE-delimited, or a hyphenated surname
//     is cut in half ("Alex Hyde-White" -> "Alex Hyde").
//   - A person half is never one token (tier 1 excepted, where the tail is a
//     closed vocabulary), so two-token names ("Barry Press", "Brianna Vox") are
//     structurally unreachable by the bare tier.
//   - A person half may not itself end in a studio word, nor in a connector or
//     "&"/"and" ("Bob Carter & the Neighborhood Studio" -> "Bob Carter &").
//   - The corporate LEGAL suffixes (llc/ltd/inc/gmbh) are excluded from the BARE
//     tier: trimming them is corporate-name normalization, not a person/studio
//     split, and it would rewrite records the project deliberately models as
//     corporate credits. They stay in for the connector and separator tiers,
//     where an explicit boundary marker is present.
//   - SINGLE-WORD role labels (vox, voz, voce, sprecher, stimme, narrator) are
//     NEVER in the role-label vocabulary: each is a real surname in the dump
//     (John Voce, 57 credits; Brianna Vox, 27; Sarah Sprecher). Only multi-word
//     phrases are safe. voiceover/voiceovers are studio-tail tokens instead, so
//     they still need attestation via tier 3 rather than cleaning on sight.
//
// What the rule deliberately does NOT fix: "A.J Watkins of Books.Audio". The
// prefix has zero credits anywhere in the dump and "Books.Audio" has zero as a
// standalone credit, so no attestation-based rule can justify the cut, and the
// whole "of <domain>" family is n=2 dump-wide. It imports as it stands and is a
// hand-remediation item, not a rule target.

// studioTail is the closed vocabulary of FINAL tokens that mark a tail as a
// studio/production/publishing entity. Counts are the dump's census of that
// token as a credit name's final word (names / credits); the completions of a
// word family already in the list carry no separate census row.
//
// Words the census offers that are deliberately NOT here: "voice" (22 names but
// 143,762 credits, essentially all "Virtual Voice", which the AI-narration rule
// already refuses), and the single-word role labels of A.4 above.
var studioTail = map[string]bool{
	"publishing":    true, // 351 / 1,459
	"press":         true, // 206 / 1,658
	"productions":   true, // 185 / 1,127
	"media":         true, // 144 / 1,222
	"llc":           true, // 113 / 960
	"studio":        true, // 100 / 239
	"audio":         true, // 67 / 371
	"studios":       true, // 65 / 550
	"inc.":          true, // 52 / 146
	"ltd":           true, // 34 / 278
	"entertainment": true, // 27 / 366
	"audiobooks":    true, // 24 / 139
	"voices":        true, // 22 / 118
	"inc":           true, // 19 / 31
	"gmbh":          true, // 17 / 51
	"production":    true, // 16 / 155
	"publishers":    true, // 16 / 141
	"ltd.":          true, // 12 / 172
	"labs":          true, // 12 / 30
	"publisher":     true, // 9 / 16
	"narration":     true, // 9 / 15
	"records":       true, // 7 / 14
	"recordings":    true, // 2 / 8
	"recording":     true, // 2 / 9
	"voiceovers":    true, // 5 / 40 - a single-word label, so it lives HERE
	"voiceover":     true, // 2 / 3     (attestation-gated) and not in roleLabelTails
	"audios":        true, // family completions of "audio" / "audiobooks" /
	"audiobook":     true, // "narration": the dump spells each both ways, and a
	"narrations":    true, // one-sided vocabulary would be arbitrary
}

// corporateLegalSuffix is the sub-family excluded from the BARE tier. 12 of the
// 34 bare-tier hits measured over the dump were pure legal-suffix trims ("Radio
// Spirits Inc." -> "Radio Spirits", "Elle McNicoll Ltd", "McKinsey & Company
// Inc."), which is corporate-name normalization rather than a person/studio
// concatenation - and it changes the identity of records the project
// deliberately models as corporate credits (LICENSING.md). Dropping them from
// the bare tier removes all 12 with no loss on the real target.
var corporateLegalSuffix = map[string]bool{
	"llc": true, "ltd": true, "ltd.": true, "inc": true, "inc.": true, "gmbh": true,
}

// roleLabelTails is tier 1's closed vocabulary: MULTI-WORD phrases that state a
// job rather than name an entity, so their presence is the whole evidence. Each
// is stored in foldCredit form (lowercase, diacritics folded, single-spaced) and
// that is pinned by TestStudioTailVocabulary.
//
// Single-word labels are excluded on purpose - see the file comment's A.4 guard.
var roleLabelTails = []string{
	"talento de voz", // 1 name / 10 credits - Spanish "voice talent"
	"voice talent",   // 1 / 1
	"voice over",     // 3 / 11
	"voice overs",    // the plural spelling of the same phrase
}

// roleLabel is one tier-1 vocabulary entry with its token count precomputed.
// The vocabulary is constant, so re-splitting it on every credit name is pure
// waste; it is derived FROM roleLabelTails so the two cannot drift.
type roleLabel struct {
	folded string // the label in foldCredit form, exactly as roleLabelTails spells it
	tokens int    // how many tokens it occupies at the end of a name
}

var roleLabelsParsed = func() []roleLabel {
	out := make([]roleLabel, 0, len(roleLabelTails))
	for _, label := range roleLabelTails {
		out = append(out, roleLabel{folded: label, tokens: len(strings.Fields(label))})
	}
	return out
}()

// tier2Connector is the one connector whose evidence stands on its own: 79 dump
// names, ~97% of them a narrator credited to a production house (66 are
// "<narrator> for {HotGhost,Hot Ghost,HG} Production(s)" spelling variants), and
// the two contaminants are already refused by the person-half guards.
const tier2Connector = "for"

// tier3Connectors are the connectors that need attestation. "of" is the reason
// they cannot share tier 2's rule: it is only ~56% clean, because it appears
// INSIDE whole corporate names ("Way of Life Press", "Acts of The Word
// Productions", "The Staff of Entrepreneur Media", "48 Laws of Power Studio").
var tier3Connectors = map[string]bool{
	"of": true, "at": true, "with": true, "by": true, "from": true,
}

// personTailStop are tokens a person's name never ends on. A trailing connector
// is a cut made in the wrong place, and "&"/"and" is the head of a co-credit
// ("Bob Carter & the Neighborhood Studio" must not become "Bob Carter &").
var personTailStop = map[string]bool{"&": true, "and": true, "'s": true, "’s": true}

// dashSepRE is the dash separator, and it is deliberately asymmetric: an ASCII
// hyphen must be surrounded by whitespace, because a hyphenated surname carries
// one without ("Alex Hyde-White"), while an en/em dash is a separator wherever
// it appears - no name contains one.
var dashSepRE = regexp.MustCompile(`\s+-{1,2}\s+|\s*[–—]\s*`)

// slashPipeRE is the slash/pipe separator ("Eileen Rizzo/Eye Hear Voices"). No
// whitespace is required: neither character appears inside a name.
var slashPipeRE = regexp.MustCompile(`\s*[/|]\s*`)

// Each separator regex is prefiltered on the characters it cannot match
// without. These are NECESSARY conditions, so a prefilter can never change a
// result - and the overwhelming majority of credit names contain none of them,
// which is what makes the three regexes free on the common path.
const (
	dashSepChars   = "-–—"
	slashPipeChars = "/|"
	// trailingParenRE anchors on a CLOSING bracket at the end of the string.
	closeBracketChars = ")]"
)

// minPersonTokens is the floor on a person half. One token is not enough
// evidence to cut a name on: "Ken" is itself an attested mononym credit, so a
// one-token floor turns "Ken Clark Smooth Voiceovers" into "Ken". Tier 1 is
// exempt because its tail is a closed vocabulary rather than an attestation.
const minPersonTokens = 2

// creditSeenFunc reports whether a name has been SEEN as a credit in its own
// right - somewhere in the catalogue, or elsewhere in the batch being imported.
// A nil creditSeenFunc has seen nothing, which leaves tiers 1 and 2 (the two
// that carry their own evidence) and disables tier 3 entirely. See
// planner.creditCensusOf.
//
// It is deliberately NOT called "attested": the importer's trust-tier
// vocabulary (attest.go, LICENSING.md) already owns that word for a different
// question - whether a record's facts came from a user rather than a bulk
// mirror. This one is only "has anyone else been credited under this name".
type creditSeenFunc func(name string) bool

// seen is the nil-safe call.
func (f creditSeenFunc) seen(name string) bool { return f != nil && f(name) }

// stripStudioConcat removes a concatenated studio/production credit from name,
// returning the name unchanged when no tier's evidence bar is met. It only ever
// shortens the name, which is what lets CreditWithRoles' fixpoint converge.
//
// The name is tokenized ONCE here and the tokens handed down: all three tiers
// work over the same split, and this runs per credit per row.
func stripStudioConcat(name string, seen creditSeenFunc) string {
	tokens := strings.Fields(name)
	if cleaned, ok := tier1RoleLabel(tokens, seen); ok {
		return cleaned
	}
	if cleaned, ok := tier2ForConnector(tokens); ok {
		return cleaned
	}
	if cleaned, ok := tier3SeenSplit(name, tokens, seen); ok {
		return cleaned
	}
	return name
}

// tier1RoleLabel strips a closed multi-word role-label tail. The remaining
// prefix is then walked DOWN to the longest independently seen form (floored
// at minPersonTokens) so a brand adjective the label dragged along goes with it
// ("Ken Clark Smooth Voiceovers"); with nothing seen the whole prefix is
// kept, minus any trailing connector or possessive.
func tier1RoleLabel(tokens []string, seen creditSeenFunc) (string, bool) {
	for _, label := range roleLabelsParsed {
		if len(tokens) <= label.tokens {
			continue
		}
		if foldCredit(strings.Join(tokens[len(tokens)-label.tokens:], " ")) != label.folded {
			continue
		}
		person := trimPersonTail(walkDownSeen(tokens[:len(tokens)-label.tokens], seen))
		// The 2-token floor does not apply (the tail is closed-vocabulary
		// evidence, so "Aery Talento de Voz" -> "Aery" is sound), but the
		// remaining guards do.
		if !personHalfOK(person, 1) {
			continue
		}
		return strings.Join(person, " "), true
	}
	return "", false
}

// tier2ForConnector strips "<person, 2+ tokens> for <tail ending in a studio
// word>". It consults no census at all - the "for" connector between a name and
// a production house is its own evidence - which is why it takes none.
func tier2ForConnector(tokens []string) (string, bool) {
	person, tail, ok := splitAtConnector(tokens, func(t string) bool { return t == tier2Connector })
	if !ok || !tailIsStudio(tail, studioTail) || !personHalfOK(person, minPersonTokens) {
		return "", false
	}
	return strings.Join(person, " "), true
}

// tier3SeenSplit is the census-backed tier: the person half must be a credit in
// its own right before the string is cut, and for the BARE form (the only shape
// with no boundary marker at all) the removed tail must have been seen too.
//
// The explicit-separator forms are tried before the bare scan, so a string that
// carries a boundary marker is always cut AT it.
func tier3SeenSplit(name string, tokens []string, seen creditSeenFunc) (string, bool) {
	// With no census, every branch below fails its seen() guard. That is the
	// majority caller (the libex parse layer, the census bootstrap itself,
	// SplitNames, pkg/scan), so it is worth not running the separator regexes
	// and the bare scan to reach a foregone "no".
	if seen == nil {
		return "", false
	}

	// accept applies the guards the separator and connector forms share. The
	// person half is looked up in the census under the text the split produced,
	// but returned in its whitespace-normalized form.
	accept := func(person, tail []string, lookupText string) (string, bool) {
		if !tailIsStudio(tail, studioTail) || !personHalfOK(person, minPersonTokens) || !seen.seen(lookupText) {
			return "", false
		}
		return strings.Join(person, " "), true
	}

	sawSeparator := false
	for _, split := range []func(string) (person, tail string, ok bool){
		splitTrailingParen, splitAtRegexp(dashSepRE, dashSepChars), splitAtRegexp(slashPipeRE, slashPipeChars),
	} {
		personText, tailText, ok := split(name)
		if !ok {
			continue
		}
		sawSeparator = true
		if cleaned, ok := accept(strings.Fields(personText), strings.Fields(tailText), personText); ok {
			return cleaned, true
		}
	}

	if person, tail, ok := splitAtConnector(tokens, func(t string) bool { return tier3Connectors[t] }); ok {
		if cleaned, ok := accept(person, tail, strings.Join(person, " ")); ok {
			return cleaned, true
		}
	}

	// The bare concatenation. The tail's final token is the whole name's final
	// token, so the vocabulary test is made once; the split point is then the
	// LONGEST person half whose two sides have both been seen, so a cut keeps as
	// much of the name as the evidence supports.
	if !isBareShape(tokens, sawSeparator) || !tailIsStudio(tokens, bareStudioTail()) {
		return "", false
	}
	for i := len(tokens) - 1; i >= minPersonTokens; i-- {
		person, tail := tokens[:i], tokens[i:]
		personText, tailText := strings.Join(person, " "), strings.Join(tail, " ")
		if personHalfOK(person, minPersonTokens) && seen.seen(personText) && seen.seen(tailText) {
			return personText, true
		}
	}
	return "", false
}

// bareStudioTail is studioTail without the corporate legal suffixes. It is
// DERIVED from the two vocabularies rather than spelled out, so the exclusion
// can never drift out of step with the list it excludes from - and computed
// once rather than per credit name.
var bareStudioTail = sync.OnceValue(func() map[string]bool {
	out := make(map[string]bool, len(studioTail))
	for word := range studioTail {
		if !corporateLegalSuffix[word] {
			out[word] = true
		}
	}
	return out
})

// splitAtConnector splits tokens at the FIRST connector match that leaves a
// non-empty half on each side.
func splitAtConnector(tokens []string, isConnector func(string) bool) (person, tail []string, ok bool) {
	for i := 1; i < len(tokens)-1; i++ {
		if isConnector(foldCredit(tokens[i])) {
			return tokens[:i], tokens[i+1:], true
		}
	}
	return nil, nil, false
}

// splitTrailingParen splits "<person> (<tail>)" / "<person> [<tail>]".
func splitTrailingParen(name string) (person, tail string, ok bool) {
	if !strings.ContainsAny(name, closeBracketChars) {
		return "", "", false
	}
	m := trailingParenRE.FindStringSubmatchIndex(name)
	if m == nil {
		return "", "", false
	}
	return strings.TrimSpace(name[:m[0]]), name[m[2]:m[3]], true
}

// splitAtRegexp splits at the LAST match of sep, so a name carrying an earlier
// one keeps it and only the trailing tail is considered. trigger is the set of
// characters sep cannot match without (see dashSepChars).
func splitAtRegexp(sep *regexp.Regexp, trigger string) func(string) (string, string, bool) {
	return func(name string) (person, tail string, ok bool) {
		if !strings.ContainsAny(name, trigger) {
			return "", "", false
		}
		all := sep.FindAllStringIndex(name, -1)
		if len(all) == 0 {
			return "", "", false
		}
		last := all[len(all)-1]
		return strings.TrimSpace(name[:last[0]]), strings.TrimSpace(name[last[1]:]), true
	}
}

// walkDownSeen returns the longest strictly-shorter prefix of tokens that has
// independently been seen as a credit, floored at minPersonTokens, and the
// whole slice when none has.
func walkDownSeen(tokens []string, seen creditSeenFunc) []string {
	for k := len(tokens) - 1; k >= minPersonTokens; k-- {
		if seen.seen(strings.Join(tokens[:k], " ")) {
			return tokens[:k]
		}
	}
	return tokens
}

// isBoundaryToken reports whether a FOLDED token marks a boundary rather than
// naming a person: either tier's connector, or the head of a co-credit. It is
// the single spelling of that question - a person half may not END on one
// (personHalfOK), trimPersonTail drops it, and its presence ANYWHERE in a string
// means the string is not the bare shape (isBareShape).
func isBoundaryToken(folded string) bool {
	return personTailStop[folded] || tier3Connectors[folded] || folded == tier2Connector
}

// trimPersonTail drops the trailing tokens a person's name never ends on: a
// connector, a conjunction, or a standalone possessive (see personTailStop).
func trimPersonTail(tokens []string) []string {
	for len(tokens) > 0 && isBoundaryToken(foldCredit(tokens[len(tokens)-1])) {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

// personHalfOK applies the guards every tier shares to a candidate person half:
// a minimum token count (minTokens is always at least 1, so this also rejects an
// empty half), and a final token that is neither a studio word (which would mean
// the cut landed inside the entity's own name) nor a boundary token (which would
// mean it landed one token too late).
func personHalfOK(tokens []string, minTokens int) bool {
	if len(tokens) < minTokens {
		return false
	}
	last := foldCredit(tokens[len(tokens)-1])
	return !studioTail[last] && !isBoundaryToken(last)
}

// tailIsStudio reports whether the tail's FINAL token is in vocab - the whole
// test for "is this half an entity rather than a person".
func tailIsStudio(tail []string, vocab map[string]bool) bool {
	return len(tail) > 0 && vocab[foldCredit(tail[len(tail)-1])]
}

// isBareShape reports whether the name really is the bare shape - a run of name
// words with NO boundary marker in the string at all. It is what stops the bare
// scan from re-reading a string the separator and connector tiers have already
// judged and refused: "Way of Life Press" is a corporate name whose connector
// tier declined it (the person half would be the single token "Way"), and
// letting the bare scan cut it at "Way of Life | Press" would resurrect exactly
// the false positive the connector tier exists to prevent.
//
// sawSeparator is the caller's record of whether any of the three separator
// splitters matched. tier3SeenSplit has just run all three by the time it gets
// here (it only returns early on an ACCEPTED cut), so re-running the regexes to
// ask the same question would be pure duplicate work.
func isBareShape(tokens []string, sawSeparator bool) bool {
	if sawSeparator {
		return false
	}
	for _, t := range tokens {
		if isBoundaryToken(foldCredit(t)) {
			return false
		}
	}
	return true
}
