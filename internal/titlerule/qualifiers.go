package titlerule

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// qualifiers.go holds the two TITLE QUALIFIERS that state a fact about a product
// rather than about the book: a MARKETPLACE edition ("The Search: International
// Edition") and a NARRATOR credit ("Lock In (Narrated by Amber Benson)", "Harry Potter
// und der Feuerkelch - Gesprochen von Rufus Beck"). Neither is part of a work's
// identity - the marketplace rides on a recording's identifiers and the narrator is
// recording.narrators - so both come off before a title is compared (Clean) and
// before a retitle is proposed (StripDecoration), and the decoration table files them
// under their own codes (DecMarketEdition, DecNarrator).
//
// Both are CLOSED and BOUNDED, the posture every strip here takes: a phrase from a
// fixed vocabulary, standing as a WHOLE title segment (a bracketed group, a quoted
// run, or the text after a segment separator), and nothing else. Measured over the
// 282,052-work tree at c7ef08466 and the libex dump (2026-07-29 snapshot, 1,130,872
// books):
//
//   - "International Edition" is carried by 51 tree titles, every one the marketplace
//     marker, in four spellings: ": International Edition" (31, one more followed by
//     ", Parts 1 and 2"), a quoted "International Edition"/"International Editions"
//     (16, two of them followed by "[Dramatized Adaptation]") and "(International
//     Edition)" (3, which the comparison key's bracket rule already removed). The dump
//     spells it in 108 titles, the same shapes plus "[International Edition]", and
//     uses the phrase no other way.
//   - the narrator lead-ins (NarratorLeadIns, TitleOnlyNarratorLeadIns) stand as a qualifier in 27 tree titles:
//     15 bracketed (which the comparison key already removed) and 12 not - the seven
//     German Harry Potter volumes "- Gesprochen von Rufus Beck", "Die Bibel. Gelesen von
//     Rufus Beck" and four Crossway titles ", Read by <narrator>". Two titles use the
//     words otherwise and the bound leaves both alone: "Narrated by the Author: How to
//     Produce an Audiobook on a Budget" (nothing before it, and it names nobody) and "A
//     Christmas Carol: The Unabridged Classic Narrated by Chandler Craig" (no separator
//     before it - a retailer's sentence, and one title is no basis for reading a bare
//     space as a boundary). In the dump every title-position match is a real narrator
//     credit (21 distinct titles); narratorCredit's digit and bracket bounds come from
//     the two shapes beside them that are not a credit alone (see there).

// NarratorLeadIns are the narrator lead-ins a TITLE spells that internal/importer's
// mid-title strip (stripTitleNarratorQualifier, before a volume marker) and this
// file's trailing strip BOTH read, so "what introduces a narrator credit in a title"
// has one definition. Every entry is lowercase ASCII (the form internal/importer's
// foldCredit produces) and is matched at a word boundary. They moved here from
// internal/importer, where they were measured over the full 1.13M-book dump (counts
// are books in the importer's mid-title shape), unchanged and in their order; they
// are a subset of the importer's SERIES vocabulary, which its drift guard pins.
//
// "horspiele von" is deliberately absent, though the series vocabulary has it: in the
// series position it means "audio dramas read by", but in a title it is an AUTHORSHIP
// phrase - "Die schönsten Märchen-Hörspiele von Grimm, Hauff und Andersen" credits the
// Brothers Grimm, not a narrator - and stripping it would delete the authors.
var NarratorLeadIns = []string{
	"gelesen von",    // 11 of the 13 dump books in the mid-title shape; "Die Bibel. Gelesen von Rufus Beck"
	"narrated by",    // 2; 13 bracketed titles in the tree
	"gesprochen von", // 0 mid-title; the seven German Harry Potter volumes
}

// TitleOnlyNarratorLeadIns are the lead-ins this file's TRAILING strip reads beside
// NarratorLeadIns and the importer's mid-title strip does not, so the importer's
// behaviour stays exactly what was measured for it. "read by" is attested as a title
// qualifier (four unbracketed and two bracketed tree titles: "ESV Audio Bible, Read by
// Ray Ortlund", "Just So Stories (Read by Tony Robinson)"; eleven distinct dump
// titles) and nowhere in the series trailing-bracket position, where the importer's
// series vocabulary refuses it; "A Read by the Sea Wedding Romance" is a title, which
// the segment bound keeps it off.
var TitleOnlyNarratorLeadIns = []string{"read by"}

// trailingLeadIns is every lead-in the trailing strip reads.
var trailingLeadIns = slices.Concat(NarratorLeadIns, TitleOnlyNarratorLeadIns)

// narratorObjectLeads are the words that begin a narration credit naming NOBODY - a
// reflexive or a generic object rather than a person ("as Narrated by Himself",
// "Narrated by the Author"). None of them can begin a narrator's name, so a
// qualifier leading with one is never stripped. Moved from internal/importer with the
// vocabulary it guards.
var narratorObjectLeads = map[string]bool{
	"himself": true, "herself": true, "themselves": true,
	"the": true, "a": true, "an": true,
}

// NarratorObjectLead reports whether a (lowercased) word begins a narration credit
// that names nobody - see narratorObjectLeads.
func NarratorObjectLead(word string) bool { return narratorObjectLeads[word] }

// maxNarratorWords bounds the credit a trailing qualifier may carry. The longest in
// the tree is three words ("David Cochran Heath"); six leaves room for a pair of
// narrators and stops a lead-in that happens to open a long sentence from taking the
// sentence with it.
const maxNarratorWords = 6

// nameConnectives are the lowercase words a credit of two narrators, or one name with
// a particle, may hold between capitalized words.
var nameConnectives = map[string]bool{
	"and": true, "&": true, "und": true, "y": true, "et": true, "e": true,
	"van": true, "von": true, "de": true, "der": true, "den": true, "da": true,
	"di": true, "du": true, "le": true, "la": true,
}

// narratorCredit reports whether text is a narrator qualifier and nothing else: a
// lead-in at its start, then a credit that LOOKS LIKE A NAME - two to
// maxNarratorWords words, the first and last capitalized and every other one
// capitalized or a name connective, the first no object lead - which opens no further
// segment and holds no digit or bracket.
//
// The name shape is what keeps the rule off prose: "Murder: Read by Candlelight",
// "Stop. Read by Moonlight" and "The Book Thief: Narrated by Death" each carry a
// lead-in at a segment boundary followed by one capitalized word. Measured over every
// tree and dump title holding a lead-in (29 and 64), every credit the rule exists for
// is a full name of two or more capitalized words (Rufus Beck, Stephen Fry, Ray
// Ortlund, Tinasha LaRay&eacute;, Jackie Hill Perry - 27 tree and 52 dump titles,
// identical with and without the two-word floor), so the floor costs nothing measured;
// the one-word credits that do exist ("Narrated by Declan", "Read by Amish") are
// CHAPTER titles, which no identity reads. The digit and bracket bounds came from the
// libex dump (2026-07-29 snapshot, 1,130,872 books): "Tarzan - Narrated by William
// Martin 0", "... 2" and "... 4" are three volumes whose number rides on the credit,
// and "Leopold Epstein. Poetry. Read By Mikhail Kozakov [Russian Edition]" carries a
// language statement after it.
func narratorCredit(text string) bool {
	t := strings.TrimSpace(text)
	for _, lead := range trailingLeadIns {
		if len(t) <= len(lead) || !strings.EqualFold(t[:len(lead)], lead) || t[len(lead)] != ' ' {
			continue
		}
		credit := strings.TrimSpace(t[len(lead):])
		words := strings.Fields(credit)
		if len(words) < 2 || len(words) > maxNarratorWords || !looksLikeAName(words) ||
			NarratorObjectLead(strings.ToLower(words[0])) ||
			!hasLetter(credit) || strings.Contains(credit, ": ") || strings.Contains(credit, " - ") ||
			strings.ContainsAny(credit, "0123456789()[]") {
			continue
		}
		return true
	}
	return false
}

// internationalEdition is the marketplace edition phrase, singular or plural.
const internationalEdition = `international\s+editions?`

var (
	// intlGroupRE is the phrase as a whole bracketed group, quoted inside it or not
	// ("Next to Last Stand ("International Edition")" is in the dump).
	intlGroupRE = regexp.MustCompile(`(?i)[(\[]\s*["“”]?\s*` + internationalEdition + `\s*["“”]?\s*[)\]]`)
	// intlQuotedRE is the phrase as a whole quoted run - the shape one retailer prints
	// it in ("Cold Fire "International Edition"").
	intlQuotedRE = regexp.MustCompile(`(?i)["“”]\s*` + internationalEdition + `\s*["“”]`)
	// intlSegmentRE is the phrase opening a segment, after a separator. What may
	// follow it is checked in code (segmentEndsAt), since Go's regexp has no lookahead.
	intlSegmentRE = regexp.MustCompile(`(?i)(?:[:;,|]|\s[-–])\s*(` + internationalEdition + `)\b`)
)

// segmentEndsAt reports whether a segment may end at s[i:]: the end of the title, a
// separator opening the next segment, or a bracketed group.
func segmentEndsAt(s string, i int) bool {
	rest := strings.TrimLeft(s[i:], " ")
	if rest == "" {
		return true
	}
	if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "– ") {
		return true
	}
	return strings.ContainsRune(":;,|([", rune(rest[0]))
}

// dropMarketEdition removes every "International Edition" qualifier that stands as a
// whole segment, a whole quoted run or a whole bracketed group, leaving the separator
// that introduced a segment for the run collapse to settle. lower is s lowercased,
// taken once by the caller for both qualifiers' cheap guards.
func dropMarketEdition(s, lower string) string {
	if !strings.Contains(lower, "international") {
		return s // the common case, kept off the regexps
	}
	s = cutAll(s, intlGroupRE, nil)
	s = cutAll(s, intlQuotedRE, nil)
	for _, m := range slices.Backward(intlSegmentRE.FindAllStringSubmatchIndex(s, -1)) {
		if segmentEndsAt(s, m[3]) {
			s = s[:m[2]] + s[m[3]:]
		}
	}
	return s
}

// cutAll is the qualifiers' bracket and quote removal; its siblings are rules.go's
// dropSeriesBracketGroup (a series-only group, for a proposal) and match.go's
// stripParenGroups (every group, for a series name).
//
// cutAll removes every match of re that keep (nil: every match) accepts, together with
// the whitespace around it, putting a single space back only where a word follows - so
// a group removed from before a separator leaves "NKJV: Complete Bible", never "NKJV :
// Complete Bible", which a retitle would otherwise write.
func cutAll(s string, re *regexp.Regexp, keep func(string) bool) string {
	for _, loc := range slices.Backward(re.FindAllStringIndex(s, -1)) {
		m := loc[1]
		if keep != nil && !keep(s[loc[0]:m]) {
			continue
		}
		head := strings.TrimRight(s[:loc[0]], " ")
		tail := strings.TrimLeft(s[m:], " ")
		sep := ""
		if head != "" && tail != "" {
			if r, _ := utf8.DecodeRuneInString(tail); !strings.ContainsRune(":;,.!?)]", r) {
				sep = " "
			}
		}
		s = head + sep + tail
	}
	return s
}

// dropNarratorQualifier removes a narrator qualifier that stands as a whole bracketed
// group (anywhere) or as the title's LAST segment, after a separator - ": ", " - ",
// ", " or ". " - and running to the end of the title. A lead-in that does not open a
// segment, or whose credit fails narratorCredit, is left exactly as written.
func dropNarratorQualifier(s, lower string) string {
	if !mentionsNarratorLeadIn(lower) {
		return s // the common case
	}
	s = cutAll(s, parenGroup, func(g string) bool { return narratorCredit(g[1 : len(g)-1]) })
	for i := range s {
		if i == 0 || !opensTrailingSegment(s[:i]) || !narratorCredit(s[i:]) {
			continue
		}
		return s[:i]
	}
	return s
}

// looksLikeAName reports whether a credit's words read as a name: the first and last
// capitalized, every word between capitalized or a name connective.
func looksLikeAName(words []string) bool {
	for i, w := range words {
		r, _ := utf8.DecodeRuneInString(w)
		if unicode.IsUpper(r) {
			continue
		}
		if i == 0 || i == len(words)-1 || !nameConnectives[w] {
			return false
		}
	}
	return true
}

// opensTrailingSegment reports whether the text before a candidate lead-in ends in a
// segment separator followed by a space. The set is its own: unlike rules.go's
// subtitleSeps/segmentPunct and identity.go's endsSegment/divisionSeparators it takes
// ". " ("Die Bibel. Gelesen von Rufus Beck"), which is safe here only because a
// closed lead-in must follow it.
func opensTrailingSegment(head string) bool {
	if !strings.HasSuffix(head, " ") {
		return false
	}
	h := strings.TrimRight(head, " ")
	r, _ := utf8.DecodeLastRuneInString(h)
	switch r {
	case ':', ',', '.', ';', '|':
		return true
	case '-', '–':
		return strings.HasSuffix(strings.TrimSuffix(strings.TrimSuffix(h, "-"), "–"), " ")
	}
	return false
}

// mentionsNarratorLeadIn is dropNarratorQualifier's cheap guard over an
// already-lowercased title.
func mentionsNarratorLeadIn(lower string) bool {
	for _, lead := range trailingLeadIns {
		if strings.Contains(lower, lead) {
			return true
		}
	}
	return false
}

// dropTitleQualifiers removes both qualifiers and settles the separators they leave.
// It never empties a title: a title that is nothing but a qualifier is returned as it
// was, for the caller's own fallback to judge.
func dropTitleQualifiers(title string) string {
	lower := strings.ToLower(title)
	s := dropNarratorQualifier(dropMarketEdition(title, lower), lower)
	if s == title {
		return title
	}
	s = collapseSeparatorRuns(tidyTitle(s))
	if !hasAlnum(s) {
		return title
	}
	return s
}

// dropsQualifier is the decoration table's test for one qualifier: drop removes
// something AND leaves a title behind - the same never-empty fallback
// dropTitleQualifiers applies, so a title that is nothing but a qualifier is not filed
// as carrying a decoration no strip will ever take off.
func dropsQualifier(title string, drop func(s, lower string) string) bool {
	out := drop(title, strings.ToLower(title))
	return out != title && hasAlnum(out)
}

// leadingNamePossessive matches a title that OPENS with a possessive of a name of two
// or three capitalized words - the franchise BRAND a ghostwritten continuation is sold
// under ("Tom Clancy's Oath of Office", "Clive Cussler's The Heist"). The first word
// is captured on its own so an article can be refused (nameArticles).
var leadingNamePossessive = regexp.MustCompile(`^(\p{Lu}[\p{L}.]*)((?:\s+\p{Lu}[\p{L}.]*){1,2})['’]s\s`)

// nameArticles are the leading words that make a capitalized run a TITLE rather than a
// name: "A Doll's House" and "The Pilgrim's Progress" open with an article, and a
// possessive after one is the title's own grammar ("A Doll House" is a different
// translation's title, not a brand spelled two ways).
var nameArticles = map[string]bool{
	"a": true, "an": true, "the": true,
	"der": true, "die": true, "das": true, "ein": true, "eine": true,
	"le": true, "la": true, "les": true, "un": true, "une": true,
	"el": true, "los": true, "las": true, "il": true, "lo": true, "gli": true,
	"het": true, "een": true,
}

// foldBrandPossessive drops the possessive from a leading brand name, so "Tom Clancy's
// Oath of Office" compares as "Tom Clancy Oath of Office" - the spelling a second
// retailer listing of the same book carries. It is a COMPARISON rule only (Clean reads
// it; a proposed retitle never writes it).
//
// The apostrophe is all it removes, and only at the title's head, after a two- or
// three-word capitalized name. A possessive fold ANYWHERE was measured first and
// declined: over same-author, same-language works it forms 22 new groups, 21 of them
// this brand shape (nineteen Tom Clancy and Clive Cussler continuations, "Rudyard
// Kipling's The Jungle Book", "George Washington's Farewell Address") and one a
// different book ("Dragon Magic" beside "Dragon's Magic"), which a one-word head can
// never reach here.
//
// Two accepted limits. The apostrophe-less spelling "Tom Clancys X" folds to nothing
// and so misses the possessive record (the tree holds none today). And any title-case
// possessive head folds, not only a brand ("My Sister's Keeper" compares as "My Sister
// Keeper"): harmless, because a key collision is only a candidate and every consumer
// also requires the authors to match.
func foldBrandPossessive(s string) string {
	if !strings.ContainsAny(s, "'’") {
		return s // the common case, kept off the regexp: Clean runs on every title
	}
	m := leadingNamePossessive.FindStringSubmatchIndex(s)
	if m == nil || nameArticles[strings.ToLower(s[m[2]:m[3]])] {
		return s
	}
	return s[:m[5]] + " " + s[m[1]:]
}
