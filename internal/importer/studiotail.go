package importer

import (
	"regexp"
	"strings"
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

// minPersonTokens is the floor on a person half. One token is not enough
// evidence to cut a name on: "Ken" is itself an attested mononym credit, so a
// one-token floor turns "Ken Clark Smooth Voiceovers" into "Ken". Tier 1 is
// exempt because its tail is a closed vocabulary rather than an attestation.
const minPersonTokens = 2

// attestFunc reports whether a name is independently attested as a credit -
// somewhere in the catalogue, or elsewhere in the batch being imported. A nil
// attestFunc attests nothing, which leaves tiers 1 and 2 (the two that need no
// attestation) and disables tier 3 entirely. See planner.creditAttestation.
type attestFunc func(name string) bool

// attests is the nil-safe call.
func (a attestFunc) attests(name string) bool { return a != nil && a(name) }

// stripStudioConcat removes a concatenated studio/production credit from name,
// returning the name unchanged when no tier's evidence bar is met. It only ever
// shortens the name, which is what lets CreditWithRoles' fixpoint converge.
func stripStudioConcat(name string, attested attestFunc) string {
	for _, tier := range []func(string, attestFunc) (string, bool){
		tier1RoleLabel,
		tier2ForConnector,
		tier3AttestedSplit,
	} {
		if cleaned, ok := tier(name, attested); ok {
			return cleaned
		}
	}
	return name
}

// tier1RoleLabel strips a closed multi-word role-label tail. The remaining
// prefix is then walked DOWN to the longest independently attested form (floored
// at minPersonTokens) so a brand adjective the label dragged along goes with it
// ("Ken Clark Smooth Voiceovers"); with nothing attested the whole prefix is
// kept, minus any trailing connector or possessive.
func tier1RoleLabel(name string, attested attestFunc) (string, bool) {
	tokens := strings.Fields(name)
	for _, label := range roleLabelTails {
		n := len(strings.Fields(label))
		if len(tokens) <= n {
			continue
		}
		if foldCredit(strings.Join(tokens[len(tokens)-n:], " ")) != label {
			continue
		}
		person := trimPersonTail(walkDownAttested(tokens[:len(tokens)-n], attested))
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
// word>" with no attestation required.
func tier2ForConnector(name string, _ attestFunc) (string, bool) {
	person, tail, ok := splitAtConnector(strings.Fields(name), func(t string) bool { return t == tier2Connector })
	if !ok || !tailIsStudio(tail, studioTail) || !personHalfOK(person, minPersonTokens) {
		return "", false
	}
	return strings.Join(person, " "), true
}

// tier3AttestedSplit is the attested tier: the person half must be a credit in
// its own right before the string is cut, and for the BARE form (the only shape
// with no boundary marker at all) the removed tail must be attested too.
//
// The explicit-separator forms are tried before the bare scan, so a string that
// carries a boundary marker is always cut AT it.
func tier3AttestedSplit(name string, attested attestFunc) (string, bool) {
	for _, split := range []func(string) (person, tail string, ok bool){
		splitTrailingParen, splitAtRegexp(dashSepRE), splitAtRegexp(slashPipeRE),
	} {
		personText, tailText, ok := split(name)
		if !ok {
			continue
		}
		person, tail := strings.Fields(personText), strings.Fields(tailText)
		if tailIsStudio(tail, studioTail) && personHalfOK(person, minPersonTokens) && attested.attests(personText) {
			return strings.Join(person, " "), true
		}
	}

	tokens := strings.Fields(name)
	if person, tail, ok := splitAtConnector(tokens, func(t string) bool { return tier3Connectors[t] }); ok {
		personText := strings.Join(person, " ")
		if tailIsStudio(tail, studioTail) && personHalfOK(person, minPersonTokens) && attested.attests(personText) {
			return personText, true
		}
	}

	// The bare concatenation. The tail's final token is the whole name's final
	// token, so the vocabulary test is made once; the split point is then the
	// LONGEST person half whose two sides are both attested, so a cut keeps as
	// much of the name as the evidence supports.
	if !isBareShape(name, tokens) || !tailIsStudio(tokens, bareStudioTail()) {
		return "", false
	}
	for i := len(tokens) - 1; i >= minPersonTokens; i-- {
		person, tail := tokens[:i], tokens[i:]
		personText, tailText := strings.Join(person, " "), strings.Join(tail, " ")
		if personHalfOK(person, minPersonTokens) && attested.attests(personText) && attested.attests(tailText) {
			return personText, true
		}
	}
	return "", false
}

// bareStudioTail is studioTail without the corporate legal suffixes. Built per
// call from the two vocabularies rather than stored, so the exclusion can never
// drift out of step with the list it excludes from.
func bareStudioTail() map[string]bool {
	out := make(map[string]bool, len(studioTail))
	for word := range studioTail {
		if !corporateLegalSuffix[word] {
			out[word] = true
		}
	}
	return out
}

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
	m := trailingParenRE.FindStringSubmatchIndex(name)
	if m == nil {
		return "", "", false
	}
	return strings.TrimSpace(name[:m[0]]), name[m[2]:m[3]], true
}

// splitAtRegexp splits at the LAST match of sep, so a name carrying an earlier
// one keeps it and only the trailing tail is considered.
func splitAtRegexp(sep *regexp.Regexp) func(string) (string, string, bool) {
	return func(name string) (person, tail string, ok bool) {
		all := sep.FindAllStringIndex(name, -1)
		if len(all) == 0 {
			return "", "", false
		}
		last := all[len(all)-1]
		return strings.TrimSpace(name[:last[0]]), strings.TrimSpace(name[last[1]:]), true
	}
}

// walkDownAttested returns the longest strictly-shorter prefix of tokens that is
// independently attested, floored at minPersonTokens, and the whole slice when
// none is.
func walkDownAttested(tokens []string, attested attestFunc) []string {
	for k := len(tokens) - 1; k >= minPersonTokens; k-- {
		if attested.attests(strings.Join(tokens[:k], " ")) {
			return tokens[:k]
		}
	}
	return tokens
}

// trimPersonTail drops the trailing tokens a person's name never ends on: a
// connector, a conjunction, or a standalone possessive (see personTailStop).
func trimPersonTail(tokens []string) []string {
	for len(tokens) > 0 {
		last := foldCredit(tokens[len(tokens)-1])
		if !personTailStop[last] && !tier3Connectors[last] && last != tier2Connector {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

// personHalfOK applies the guards every tier shares to a candidate person half:
// a minimum token count, and a final token that is neither a studio word (which
// would mean the cut landed inside the entity's own name) nor a connector or
// conjunction (which would mean it landed one token too late).
func personHalfOK(tokens []string, minTokens int) bool {
	if len(tokens) < minTokens || len(tokens) == 0 {
		return false
	}
	last := foldCredit(tokens[len(tokens)-1])
	return !studioTail[last] && !personTailStop[last] && !tier3Connectors[last] && last != tier2Connector
}

// tailIsStudio reports whether the tail's FINAL token is in vocab - the whole
// test for "is this half an entity rather than a person".
func tailIsStudio(tail []string, vocab map[string]bool) bool {
	return len(tail) > 0 && vocab[foldCredit(tail[len(tail)-1])]
}

// creditAttestation builds this run's attestation universe: the set of slugs a
// name must land on to count as "independently a credit somewhere".
//
// The report that specified this rule measured attestation against the whole
// 1.13M-book libex dump, which the importer does not have and must never carry a
// copy of (LICENSING.md's import posture: a bounded source, never a mirror). The
// two things it DOES have are exactly the two the report names as the practical
// substitute, and both are evidence of the same kind - a name somebody actually
// credited:
//
//	the catalogue  every person record already committed, as loaded (14.9k after
//	               seed wave 1). This is where "Alex Hyde-White" and "Punch
//	               Audio" both come from: the studio has a record of its own,
//	               which is precisely what makes the concatenation visible.
//	the batch      a census of every credit name in the rows being imported,
//	               author and narrator alike, in the source's own spelling AND
//	               in its self-evidencing cleaned form (tiers 1-2, which need no
//	               attestation). The cleaned form is what lets one row's
//	               "<narrator> for HotGhost Productions" attest the narrator for
//	               a second row that spells the same credit bare.
//
// It is deliberately a SNAPSHOT, taken after loadExisting and before the first
// row is planned, rather than a live read of p.people: consulting a set that
// grows as records are created would make a name's cleaning depend on the order
// the rows happen to arrive in, and two runs over the same export could disagree.
// This is the same batch-pre-pass shape resolveWorkTitles uses, for the same
// reason.
//
// The universe being SMALLER than the dump only ever costs a missed cleanup
// (the name imports as the source spelled it, which is what happens today and is
// a maintainer PR away from fixed). It cannot cost a wrong one: every tier that
// consults it requires MORE evidence than the tiers that do not.
func (p *planner) creditAttestation(books []sourceBook) attestFunc {
	universe := make(map[string]bool, len(p.people)+len(books)*2)
	for slug := range p.people {
		universe[slug] = true
	}
	record := func(name string) {
		if slug := Slugify(name); slug != "" {
			universe[slug] = true
		}
	}
	for _, b := range books {
		for _, name := range rowRawCreditNames(b) {
			record(name)
			// The name as the two self-evidencing tiers would clean it. Passing
			// nil is what keeps this bootstrap honest: the census is built from
			// rules that never consult the census.
			cleaned, _ := CreditWithRolesAttested(name, nil)
			record(cleaned)
		}
	}
	return func(name string) bool { return universe[Slugify(name)] }
}

// rowRawCreditNames is every credit name a row states, author and narrator, in
// the SOURCE's own spelling - the structured list when the source parsed one,
// else its comma-joined string, which is the same choice sourceCredits makes.
func rowRawCreditNames(b sourceBook) []string {
	var out []string
	for _, list := range []struct {
		typed  []string
		joined string
	}{
		{b.authors, b.str("author")},
		{b.narrators, b.str("narrated_by")},
	} {
		if len(list.typed) > 0 {
			out = append(out, list.typed...)
			continue
		}
		out = append(out, splitRawNames(list.joined)...)
	}
	return out
}

// isBareShape reports whether the name really is the bare shape - a run of name
// words with NO boundary marker in the string at all. It is what stops the bare
// scan from re-reading a string the separator and connector tiers have already
// judged and refused: "Way of Life Press" is a corporate name whose connector
// tier declined it (the person half would be the single token "Way"), and
// letting the bare scan cut it at "Way of Life | Press" would resurrect exactly
// the false positive the connector tier exists to prevent.
func isBareShape(name string, tokens []string) bool {
	if dashSepRE.MatchString(name) || slashPipeRE.MatchString(name) || trailingParenRE.MatchString(name) {
		return false
	}
	for _, t := range tokens {
		folded := foldCredit(t)
		if tier3Connectors[folded] || folded == tier2Connector || personTailStop[folded] {
			return false
		}
	}
	return true
}
