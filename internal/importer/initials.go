package importer

import (
	"slices"
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
//
// WHICH spelling survives is decided in a batch PRE-PASS (initialsCensus), not
// by arrival order. A group's survivor is the catalogue's spelling if it already
// holds one, else the batch's majority spelling, ties broken by slug order. The
// probe then only ever resolves a name ONTO that decision, so the ids a run
// mints do not depend on the order the rows arrive in - or on where a `split -l`
// happened to cut the input.

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
	raw      string // the token as the name spells it; empty for a merged run
	initials bool
}

// markedKey is the identity key of a name: every token tagged I: or W:, joined.
// Two names may merge only if their marked keys are equal, which is what stops
// an initials group from meeting a same-spelled short word.
func markedKey(name string) string { return markedKeyOf(parseNameTokens(name)) }

// MarkedNameKey exposes markedKey for in-module reuse: internal/audit groups
// near-duplicate people by the same initials identity the importer MERGES
// spellings on, so the audit reporting "these two records are one person" and the
// importer acting on it read the one rule rather than two copies of it. The key's
// exact bytes are an implementation detail and mean nothing outside a comparison
// between two of them.
func MarkedNameKey(name string) string { return markedKey(name) }

// markedKeyOf is markedKey over an already-parsed name, for a caller that needs
// the tokens for something else too and should not parse twice.
func markedKeyOf(tokens []nameToken) string {
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
		out = append(out, nameToken{letters: letters, raw: raw, initials: kind != tokenWord})
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
	if shouted || !isAllUpper(raw) {
		// No case evidence, so nothing here is an initials group. This is the
		// gate that keeps a DOTTED HONORIFIC off the initials of the same
		// letters: "Mr." is spelled exactly like a dotted cluster (letters and
		// dots, nothing else), so without it markedKey("Mr. James") equals
		// markedKey("M. R. James") and a catalogue "Mr. James" absorbs the real
		// M. R. James - 246 credits in the dump, and 61 records in the committed
		// tree sit on Mr./Dr./St./Jr./Ms. spellings. Capitalized-word honorifics
		// carry a lowercase letter; genuine initials ("A.B.", "J.R.R.", "P.J.",
		// "M.D.") do not.
		return letters, tokenWord
	}
	// A dotted cluster states its own boundaries, which is what a spelling
	// without them ("AB") elides - so unlike the bare cluster it carries no
	// length cap: "J.R.R." and "L.M.R." are as much one group as "A.B." is.
	if isDottedCluster(raw) {
		return letters, tokenCluster
	}
	if len([]rune(letters)) <= maxInitialsCluster {
		return letters, tokenCluster
	}
	return letters, tokenWord
}

// isDottedCluster reports whether raw is letters separated by dots ("A.B.",
// "J.R.R.", "M.D."), the spelling that states an initials group explicitly. At
// least two letters and at least one interior dot are required, so "Smith." is
// a word and "A." is handled as a single letter before this is reached.
//
// The SHAPE is all this answers. It is deliberately not the whole test:
// "Mr."/"Dr."/"St."/"Jr."/"Ms." have exactly this shape, and classifyToken only
// asks once the token's case has already said "initials". A dotted cluster
// written in lowercase therefore keys as a word, which is a missed merge - the
// safe direction for a rule whose only job is to decide two spellings are one
// person.
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
//
// Unlike every neighbouring fold (Slugify, foldCredit) it deliberately does NOT
// decompose diacritics: "Á." and "A." therefore key apart and do not merge. That
// is a MISSED merge, not a wrong one - the two records stay separate for a
// maintainer to join - and it is the safe direction for a rule whose only job is
// to decide that two spellings are one person.
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
//
// The word tokens are re-rendered from the token's RAW spelling, not from its
// comparison letters: foldToLetters drops the punctuation Slugify keeps, so a
// letters-rendered "A.B. Hyde-White" probed "ab-hydewhite" - an address nothing
// mints - and the whole hyphenated-surname population (every "Hyde-White",
// "Lloyd-Jones", "Sklodowska-Curie") was unreachable. The markedKey re-check on
// the record found there is unchanged, so this only ADDS merges the rule already
// judged safe.
func initialsVariantSlugs(name string) []string {
	return initialsVariantSlugsOf(name, parseNameTokens(name))
}

// probeableGroups counts a parsed name's initials groups and reports whether the
// probe applies at all: a name with none has no other spelling, and one with
// more than maxProbeGroups is the pathological case the cap exists for.
func probeableGroups(tokens []nameToken) (groups int, ok bool) {
	for _, t := range tokens {
		if t.initials {
			groups++
		}
	}
	return groups, groups > 0 && groups <= maxProbeGroups
}

// initialsVariantSlugsOf is initialsVariantSlugs over an already-parsed name.
func initialsVariantSlugsOf(name string, tokens []nameToken) []string {
	groups, ok := probeableGroups(tokens)
	if !ok {
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
				parts = append(parts, t.raw)
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

// initialsSurvivor is the record one initials group resolves to: the slug that
// survives, and the name that record carries.
type initialsSurvivor struct {
	slug string
	name string
}

// initialsSurvivors maps EVERY member slug of a decided group onto that group's
// survivor - including the survivor's own slug, so one lookup answers both "does
// this spelling merge somewhere else?" and "what name should the record I am
// about to create carry?".
//
// A group with only ONE slug is absent: there is nothing to decide, and a lookup
// miss is what tells getOrCreatePerson to mint the name exactly as it always did.
type initialsSurvivors map[string]initialsSurvivor

// initialsCensus accumulates the spellings each marked key is written in, over
// the catalogue and the batch, so the survivor can be decided BEFORE the first
// row is planned. It is the same batch-pre-pass shape resolveWorkTitles and
// creditCensusOf use, and for the same reason: a decision read off a map that
// grows during the run is a decision that depends on row order - which means on
// where a `split -l` cut the input.
type initialsCensus struct{ groups map[string]*initialsGroup }

// initialsGroup is one marked key's spellings.
type initialsGroup struct {
	// catalogue is the committed records in this group, slug -> the name the
	// record carries. Its presence is decisive: an existing id is never
	// redirected onto a spelling the batch happens to prefer.
	catalogue map[string]string
	// counts is how many batch credits each slug was spelled as, and names is
	// the spellings behind each slug. Both are needed: the SLUG is the identity
	// the majority decides, and the NAME is what the created record carries.
	counts map[string]int
	names  map[string]map[string]int
}

func newInitialsCensus() *initialsCensus {
	return &initialsCensus{groups: map[string]*initialsGroup{}}
}

// group returns the group for a name, or nil when the name has no initials group
// to respell (the overwhelming majority of credits) and so can never merge.
func (c *initialsCensus) group(name string) (*initialsGroup, string, bool) {
	tokens := parseNameTokens(name)
	if _, ok := probeableGroups(tokens); !ok {
		return nil, "", false
	}
	slug, fellBack := personSlug(name)
	if fellBack {
		// The shared catch-all record, which is not an identity: never a merge
		// target and never a member of one.
		return nil, "", false
	}
	key := markedKeyOf(tokens)
	g := c.groups[key]
	if g == nil {
		g = &initialsGroup{
			catalogue: map[string]string{},
			counts:    map[string]int{},
			names:     map[string]map[string]int{},
		}
		c.groups[key] = g
	}
	return g, slug, true
}

// addCatalogue records a committed person record. The record's OWN id is used
// rather than the slug of its name: a record whose id predates a Slugify change
// sits where it sits, and pointing a merge at an address that holds nothing
// would strand the credit (checkPersonSlug is what stops that state arising).
func (c *initialsCensus) addCatalogue(slug, name string) {
	g, _, ok := c.group(name)
	if !ok {
		return
	}
	g.catalogue[slug] = name
}

// addBatch records one credit name the run is about to import, in the form
// getOrCreatePerson will receive it.
func (c *initialsCensus) addBatch(name string) {
	g, slug, ok := c.group(name)
	if !ok {
		return
	}
	g.counts[slug]++
	if g.names[slug] == nil {
		g.names[slug] = map[string]int{}
	}
	g.names[slug][name]++
}

// decide resolves every group that spans more than one slug.
//
//	catalogue first  a spelling already committed always wins, so no existing id
//	                 moves and no run rewrites what is on disk. Two committed
//	                 spellings of one group are a pre-existing pair a maintainer
//	                 owns; the lower slug is named as the survivor, and the
//	                 consumers' "an existing record is never redirected" guard
//	                 keeps the other one serving its own credits.
//	then majority    the spelling the batch writes most often, which is the
//	                 project's own "the surviving record is the one the world
//	                 writes more often" preference.
//	then slug order  a tie is broken lexicographically, never by arrival.
func (c *initialsCensus) decide() initialsSurvivors {
	out := initialsSurvivors{}
	for _, g := range c.groups {
		members := make([]string, 0, len(g.catalogue)+len(g.counts))
		for slug := range g.catalogue {
			members = append(members, slug)
		}
		for slug := range g.counts {
			if _, dup := g.catalogue[slug]; !dup {
				members = append(members, slug)
			}
		}
		if len(members) < 2 {
			continue
		}
		slices.Sort(members)

		survivor := initialsSurvivor{}
		if len(g.catalogue) > 0 {
			for _, slug := range members {
				if name, committed := g.catalogue[slug]; committed {
					survivor = initialsSurvivor{slug: slug, name: name}
					break
				}
			}
		} else {
			best := 0
			for _, slug := range members {
				if n := g.counts[slug]; n > best {
					best, survivor.slug = n, slug
				}
			}
			survivor.name = topSpelling(g.names[survivor.slug])
		}
		for _, slug := range members {
			out[slug] = survivor
		}
	}
	return out
}

// topSpelling is the most frequent name string, ties broken lexicographically -
// the survivor record's name has to be decided as deterministically as its slug.
func topSpelling(spellings map[string]int) string {
	best, top := 0, ""
	for name, n := range spellings {
		if n > best || (n == best && name < top) {
			best, top = n, name
		}
	}
	return top
}

// initialsMerge reports the record a name should resolve to, when the pre-pass
// decided that its initials group merges.
//
// The decision is re-checked against the name's OWN variant spellings, which is
// what keeps the marked key from being trusted further than it was measured: a
// survivor is only accepted when it is a slug THIS name's initials, respelled,
// actually produce. "E. M. Brown" cannot reach "em-brown" through it, because
// the two are not in one group at all - "Em" is mixed-case and keys as a word -
// and no future widening of the key can make the merge happen without the
// respelling also lining up.
func (p *planner) initialsMerge(name string) (initialsSurvivor, bool) {
	if len(p.initialsSurvivors) == 0 {
		return initialsSurvivor{}, false
	}
	tokens := parseNameTokens(name)
	if _, ok := probeableGroups(tokens); !ok {
		return initialsSurvivor{}, false
	}
	slug, fellBack := personSlug(name)
	if fellBack {
		return initialsSurvivor{}, false
	}
	survivor, decided := p.initialsSurvivors[slug]
	if !decided || survivor.slug == "" {
		return initialsSurvivor{}, false
	}
	if survivor.slug == slug {
		return survivor, true
	}
	if slices.Contains(initialsVariantSlugsOf(name, tokens), survivor.slug) {
		return survivor, true
	}
	return initialsSurvivor{}, false
}
