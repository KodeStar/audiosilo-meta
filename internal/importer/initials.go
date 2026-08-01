package importer

import (
	"strings"
	"unicode"
)

// initials.go closes a hole in "the person slug IS the identity": Slugify maps
// every non-alphanumeric rune to a hyphen, so the dot AND the space in
// "A.B. Kovacs" both become separators ("a-b-kovacs") while "AB Kovacs" carries
// only the space ("ab-kovacs"). One person, two records - and the four spellings
// the dump carries ("A.B.", "A. B.", "A B", "AB") split along exactly that line.
//
// Measured over the full 1.13M-book libex dump: 740 groups spanning 1,480 slugs
// and 26,649 credits. In the committed tree the count went from 4 collision
// groups to 60 in one seed wave, so it grows with every import.
//
// The rule is a MARKED IDENTITY KEY computed from the name string, in which
// every token is tagged as an initials group ("I:") or a word ("W:"):
//
//	I  a maximal run of single-letter tokens ("A. B.", "A B"), a dotted cluster
//	   ("A.B.", "J.R.R.", "M.D.") and an ALL-CAPS 2-4 letter cluster ("AB",
//	   "JRR", "MD") are three spellings of one initials group, all keyed "I:ab".
//	W  a MIXED-CASE short token ("Em", "Al", "Ed", "Xe", "Mr") is a real given
//	   name or an honorific, so it keys "W:em" and can never meet "I:em".
//
// That case-evidence gate is the whole safety argument. The naive rule -
// collapse every run of single-character slug tokens - merges 789 groups but
// also merges "E. M. Brown" into the romance author "Em Brown" (108 credits),
// "M. R. James" (246) into "Mr James", "E. D. Baker" into "Ed Baker" and
// "A.L. Brooks" into "Al Brooks". The marked key keeps every one of those apart
// while covering the same real-initials population.
//
// An entirely-uppercase name carries no case evidence at all, so no token in it
// is read as a cluster ("AB KOVACS" is two words, not an initials group).
//
// What it accepts: "X E Sands" will not merge into "Xe Sands", because "Xe" is
// mixed-case and could be a given name - which it is, and in that one case it is
// also the same person. A missed merge leaves two records for a maintainer to
// join; a wrong merge fabricates an identity. The rule never guesses.

// maxInitialsCluster bounds an ALL-CAPS token read as an initials cluster.
// Two-to-four letters covers every real form ("AB", "JRR", "JCH"); a longer
// run of capitals is a word being shouted, or an acronym that names an entity.
const maxInitialsCluster = 4

// maxProbeGroups bounds how many initials groups one name may have before the
// probe stops enumerating spellings (2^n of them). No real name reaches it; the
// cap only exists so a pathological string cannot cost a run.
const maxProbeGroups = 3

// nameToken is one token of a parsed name: either an initials group (the
// letters, lowercased) or a plain word.
type nameToken struct {
	letters  string // for an initials group: "ab"; for a word: the folded token
	initials bool
}

// markedKey is the identity key of a name: every token tagged I: or W:, joined.
// Two names may merge only if their marked keys are equal, which is what stops
// an initials group from meeting a same-spelled short word.
func markedKey(name string) string {
	tokens := parseNameTokens(name)
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		tag := "W:"
		if t.initials {
			tag = "I:"
		}
		parts = append(parts, tag+t.letters)
	}
	return strings.Join(parts, "|")
}

// parseNameTokens splits a name into marked tokens, merging each maximal run of
// single-letter tokens into ONE initials group.
func parseNameTokens(name string) []nameToken {
	shouted := isAllUpper(name)
	var out []nameToken
	for _, raw := range strings.Fields(name) {
		letters, kind := classifyToken(raw, shouted)
		if letters == "" {
			continue
		}
		// A single letter continues the initials run before it, so "A. B." and
		// "A B" key exactly as the cluster "AB" does.
		if kind == tokenSingleLetter && len(out) > 0 && out[len(out)-1].initials {
			out[len(out)-1].letters += letters
			continue
		}
		out = append(out, nameToken{letters: letters, initials: kind != tokenWord})
	}
	return out
}

// tokenKind is what one token of a name looks like.
type tokenKind int

const (
	tokenWord         tokenKind = iota // an ordinary word, including a mixed-case short one
	tokenSingleLetter                  // "A" or "A." - joins the run before it
	tokenCluster                       // "A.B." or the ALL-CAPS "AB" - an initials group of its own
)

// classifyToken folds one raw token to its comparison letters and says which
// shape it is. shouted reports that the WHOLE name is uppercase, in which case
// no token carries case evidence and none is read as a cluster.
func classifyToken(raw string, shouted bool) (letters string, kind tokenKind) {
	letters = foldToLetters(raw)
	if letters == "" {
		return "", tokenWord
	}
	if len([]rune(letters)) == 1 {
		return letters, tokenSingleLetter
	}
	// A dotted cluster states its own shape: the dots are the boundaries a
	// spelling without them ("AB") elides, so no case evidence is needed.
	if isDottedCluster(raw) {
		return letters, tokenCluster
	}
	if !shouted && isAllUpper(raw) && len([]rune(letters)) <= maxInitialsCluster {
		return letters, tokenCluster
	}
	return letters, tokenWord
}

// isDottedCluster reports whether raw is letters separated by dots ("A.B.",
// "J.R.R.", "M.D."), the spelling that states an initials group explicitly. At
// least two letters and at least one interior dot are required, so "Smith." is
// a word and "A." is handled as a single letter before this is reached.
func isDottedCluster(raw string) bool {
	letters, dots := 0, 0
	for _, r := range raw {
		switch {
		case r == '.':
			dots++
		case unicode.IsLetter(r):
			letters++
		default:
			return false
		}
	}
	return letters >= 2 && dots >= letters-1
}

// foldToLetters reduces a token to its lowercase letters and digits, dropping
// the punctuation a spelling hangs on them. It is the comparison form the key is
// built from, so "A.B." and "AB" fold alike.
func foldToLetters(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isAllUpper reports whether s contains at least one letter and no lowercase
// one - the "no case evidence" test, applied both to a whole name and to a
// single token.
func isAllUpper(s string) bool {
	seen := false
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsLetter(r) {
			seen = true
		}
	}
	return seen
}

// initialsVariantSlugs is every slug the name's OTHER initials spellings
// produce - the name's own slug is never returned. Each variant is rendered as
// a name string and put through Slugify, rather than assembled from slug
// fragments, so a variant can only ever be a slug the importer itself could
// mint.
func initialsVariantSlugs(name string) []string {
	tokens := parseNameTokens(name)
	groups := 0
	for _, t := range tokens {
		if t.initials {
			groups++
		}
	}
	if groups == 0 || groups > maxProbeGroups {
		return nil
	}

	own := Slugify(name)
	seen := map[string]bool{own: true}
	var out []string
	// Each initials group is spelled either joined ("AB") or separated
	// ("A. B."); enumerate every combination, which for one group is simply the
	// other spelling.
	for mask := 0; mask < 1<<groups; mask++ {
		parts := make([]string, 0, len(tokens))
		g := 0
		for _, t := range tokens {
			if !t.initials {
				parts = append(parts, t.letters)
				continue
			}
			if mask&(1<<g) != 0 {
				parts = append(parts, spacedInitials(t.letters))
			} else {
				parts = append(parts, t.letters)
			}
			g++
		}
		if slug := Slugify(strings.Join(parts, " ")); slug != "" && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}

// spacedInitials renders an initials group in its separated spelling ("ab" ->
// "a. b."), the form whose slug carries a hyphen between the letters.
func spacedInitials(letters string) string {
	parts := make([]string, 0, len(letters))
	for _, r := range letters {
		parts = append(parts, string(r)+".")
	}
	return strings.Join(parts, " ")
}

// findInitialsVariant probes the catalogue (and what this run has already
// created) for a record holding the SAME person under a different initials
// spelling, returning its slug.
//
// The re-check on the candidate's own stored name is what makes the probe safe,
// and it is not optional: "E. M. Brown" probes straight onto "em-brown", which
// is a real and different author. Only when the record found there carries a
// name with the same marked key - initials meeting initials - is it the same
// person. The comparison is the marked key rather than the slug precisely
// because the slug is what conflated them.
func (p *planner) findInitialsVariant(name string) (slug string, found bool) {
	key := markedKey(name)
	for _, candidate := range initialsVariantSlugs(name) {
		stored, known := p.people[candidate]
		if !known || markedKey(stored) != key {
			continue
		}
		return candidate, true
	}
	return "", false
}
