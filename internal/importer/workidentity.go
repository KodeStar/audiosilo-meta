package importer

import (
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// workidentity.go holds the two rules that decide WHICH WORK a row is: who
// counts as an author for identity purposes, and when two rows that spell the
// same title are nevertheless different books.
//
// Both exist because a work's identity in this catalogue is (title slug, author
// set) and the retailer data violates that pair's assumptions in two measured
// ways:
//
//   - a source lists a translator/illustrator/editor in the AUTHOR column on
//     some editions of a book and not on others, so one book forks into two
//     works whose only difference is a contributor credit;
//   - a serial's volumes are all published under the bare series name, so six
//     different books arrive with byte-identical titles and only their series
//     POSITION tells them apart.
//
// Neither is a hypothetical. Seed wave 5 measured 28 same-language duplicate
// work pairs of the first kind (the whole French Witcher run: le-sang-des-elfes
// against le-sang-des-elfes-andrzej-sapkowski, the delta being one translator)
// across 120 subset-author groups, and the second kind merged Bravelands books
// 1, 4 and 5 onto ONE recording.

// ---------------------------------------------------------------------------
// Credited-contributor identity exclusion
//
// A person whose every appearance in a row's author list carried a
// contributor-role qualifier ("Barbara Bright - Übersetzer") is a CREDIT, not
// an author for identity purposes. They stay in authors[] and in credits[]
// exactly as before - the change is only to the set two works are compared on.
//
// The rule is stated in terms of the ROLE CREDIT rather than of the qualifier
// text, so the two sides of the comparison can agree. A row states its roles
// inline (credit.roles); a work already on disk states them in work.credits.
// Those are the same fact recorded twice, which is what lets identityAuthors
// judge an incoming row and a catalogued work by one definition instead of two
// that could drift.
//
// A qualifier that STRIPS but states no role (roleQualifiers' nil entries -
// "narrator", "director", "ghostwriter") deliberately does not exclude anybody:
// it puts no credit on the work, so the disk side could never see it, and a
// rule only one side can apply is a rule that splits works.
//
// The fallback is what keeps the rule from ever emptying a work's identity: an
// edition whose author column is ENTIRELY translators (they exist - a
// re-translation credited only to its translator) falls back to the full author
// set rather than matching every other authorless work in the catalogue.

// workAuthors carries a row's author slugs in the two roles they play: `all` is
// the credit list as the record stores it (source order, deduplicated by slug),
// and `identity` is the subset work identity is matched and disambiguated on.
type workAuthors struct {
	all      []string
	identity []string
}

// set is the identity author set, for comparison against a workState's.
func (a workAuthors) set() map[string]bool { return ToSet(a.identity) }

// allSet is the FULL credit set (identity plus the role-credited people), which
// is the set a work's authors[] array is written from. The subsumption match
// below compares it against a work's own, so a bare and a role-qualified
// spelling of one book's credit list can recognize each other.
func (a workAuthors) allSet() map[string]bool { return ToSet(a.all) }

// first is the author slug a collision suffix is built from. Reading it from
// the IDENTITY list rather than from `all` is what makes the disambiguated slug
// of a book independent of whether this particular edition listed a translator.
func (a workAuthors) first() string { return a.identity[0] }

// ---------------------------------------------------------------------------
// Matching a row against a work
//
// workMatch grades how well a candidate work's author sets answer a row's.
// Grading rather than deciding is what lets getOrCreateWork prefer the RIGHT
// one of two candidates that both answer: the-iliad (Homer, bare) and
// the-iliad-robert-fitzgerald (Homer plus the translator) reduce to the same
// IDENTITY set - Homer - so a Fitzgerald row matches both, and only the full
// credit list can say which of the two it is.

type workMatch int

const (
	// matchNone: the row is about a different book.
	matchNone workMatch = iota
	// matchIdentity: the identity sets agree, directly or by subsumption.
	matchIdentity
	// matchExact: the whole credit lists agree, so this candidate is the row's
	// book and no other candidate can be a better answer.
	matchExact
)

// matchWork grades a candidate work against a row's resolved authors.
//
// The identity test is deliberately two-sided. SameSet alone assumes both sides
// reduce a book's credit list the same way, and they cannot: a work whose
// record carries no credits[] (9,907 of them in the tree, every one minted
// before the exclusion rule) has its FULL list as its identity, while a row
// that role-qualifies the same translator has only the author - so one book
// forked into two works, in whichever direction the first row happened to
// spell it.
//
// Subsumption closes that in both directions: one side's identity must CONTAIN
// the other's, and the containing identity must itself be credited by the
// smaller side. Reading it as a sentence: the two sides agree on who wrote the
// book as far as either of them reduced the list, and neither names an author
// the other does not credit at all.
//
// The nesting is what keeps it from conflating two genuinely different books,
// and the case that proves it is a mutual translation: "The Tower by Ada One,
// translated by Bea Two" against "The Tower by Bea Two, translated by Ada One".
// Both rows carry the same two people, so each identity IS contained in the
// other's whole list - and they are different books. Their identities are
// DISJOINT, so requiring one to contain the other refuses the pair while
// costing none of the bare/qualified pairs, whose identities are always nested.
func matchWork(ws *workState, a workAuthors) workMatch {
	want, wantAll := a.set(), a.allSet()
	rowReduced := subsetOf(want, ws.authors) && subsetOf(ws.authors, wantAll)
	workReduced := subsetOf(ws.authors, want) && subsetOf(want, ws.all)
	if !rowReduced && !workReduced {
		return matchNone
	}
	// The whole credit lists agree too, so no other candidate can be a better
	// answer: this is the row's book, translator credits and all.
	if SameSet(ws.all, wantAll) {
		return matchExact
	}
	return matchIdentity
}

// langCompatible reports whether a candidate work may absorb a row in language
// want. A work is language-scoped: a translated edition is a different work
// from its original, and merging them makes the work's own language a lie for
// half its recordings. An unknown language on either side never blocks, which
// is the same "evidence only" posture every other merge guard takes.
//
// It is not a hypothetical: the tree holds base/fork pairs where the bare slug
// is the English work and the suffixed one its German translation
// (enchantra / enchantra-julian-muller), whose identity sets are equal once the
// translators are excluded. Measured on a 20,000-row wave-6 simulation over one
// baseline: 368 cross-language recordings in the tree without this test against
// 286 with it, and none of the run's legitimate merges lost. Run against the
// migrated tree with the whole fix set, the same wave added ZERO.
func langCompatible(have, want string) bool {
	return have == "" || want == "" || have == want
}

// subsetOf reports whether every member of a is in b.
func subsetOf(a, b map[string]bool) bool {
	if len(a) > len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// splitWorkAuthors resolves a row's author credits to person slugs and splits
// them into the record list and the identity list. resolve turns one credit
// name into the slug the person is (or will be) stored under; the two callers
// differ only in whether resolving may CREATE the person.
//
// Every credit is resolved, including a repeated one, because the exclusion is
// decided per PERSON: a name spelled bare on one entry and role-qualified on
// another is one person with a role credit, and the disk side - which sees only
// the finished credits[] array - can read them no other way.
func splitWorkAuthors(credits []credit, resolve func(string) string) workAuthors {
	var wa workAuthors
	seen := make(map[string]bool, len(credits))
	roleCredited := make(map[string]bool, len(credits))
	for _, c := range credits {
		slug := resolve(c.name)
		if len(c.roles) > 0 {
			roleCredited[slug] = true
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		wa.all = append(wa.all, slug)
	}
	wa.identity = identityAuthors(wa.all, roleCredited)
	return wa
}

// identityAuthors drops the role-credited people from an author list, falling
// back to the whole list when that would leave nothing.
func identityAuthors(authors []string, roleCredited map[string]bool) []string {
	if len(roleCredited) == 0 {
		return authors
	}
	out := make([]string, 0, len(authors))
	for _, slug := range authors {
		if !roleCredited[slug] {
			out = append(out, slug)
		}
	}
	if len(out) == 0 {
		return authors
	}
	return out
}

// diskIdentityAuthors is identityAuthors for a work loaded from the catalogue:
// the role credits are read from the record's own credits[] array, which is the
// same fact a row states inline.
func diskIdentityAuthors(authors []string, credits []model.Credit) map[string]bool {
	if len(credits) == 0 {
		return ToSet(authors)
	}
	roleCredited := make(map[string]bool, len(credits))
	for _, c := range credits {
		roleCredited[c.Person] = true
	}
	return ToSet(identityAuthors(authors, roleCredited))
}

// rowWorkAuthors resolves a row's author credits on the CREATE path, minting
// person records as it goes (getOrCreatePerson), which is what p.creditSlugs
// did for the same list before identity and storage parted company.
func (p *planner) rowWorkAuthors(credits []credit, warn func(string, ...any)) workAuthors {
	return splitWorkAuthors(credits, func(name string) string {
		return p.getOrCreatePerson(name, warn)
	})
}

// rowWorkAuthorsRO is rowWorkAuthors' read-only twin: it resolves through the
// same identity rules (personSlug plus the initials merge) but creates nothing,
// for the batch pre-passes and for the recordings-only work matcher.
func (p *planner) rowWorkAuthorsRO(credits []credit) workAuthors {
	return splitWorkAuthors(credits, func(name string) string {
		slug, _ := personSlug(name)
		return p.personSlugTarget(slug)
	})
}

// ---------------------------------------------------------------------------
// Same-title serial disambiguation
//
// A serial published under its bare series name arrives as N rows with one
// title. The existing full-title fallback (resolveWorkTitles) cannot separate
// them: it re-derives the work title from the row's fuller "Title: Subtitle"
// field, and these rows have no subtitle, so every candidate is the same string
// and all N rows walk the same slug chain.
//
// Measured: the dump carries six Bravelands rows titled exactly "Bravelands",
// at series positions 1-6, alongside six properly-titled ones ("Bravelands #1:
// Broken Pride", ...). Every bare row collided with the position its
// properly-titled twin had already taken, so its series placement was dropped -
// and a work that is not IN a series trivially satisfies the same-position
// merge test, so rows 2..6 merged onto row 1's work and their runtime-
// compatible ASINs merged onto its recording. Three different books, one
// recording. The same shape produced a Perry Rhodan omnibus block (3060-3069
// merged into 3040-3049), a Solomon Kane episode merge, and the wave-4 tik-tak
// class.
//
// The fix is a batch pre-pass, in the same spirit as resolveWorkTitles: rows
// that share a resolved title AND an identity author set but claim DIFFERENT
// positions in one series are different books, so each one's work slug carries
// its position. The suffix is the established style - a disambiguating tail
// appended to the title base, bounded by BoundedSlugTail - so the collision
// chain above it (author suffix, then numeric) is unchanged.
//
// It fires ONLY on a group that would otherwise collapse. A serial whose
// volumes have distinct titles never reaches the test (the rows are in
// different groups), and neither does a group whose rows all claim one
// position - two editions of one book, which SHOULD merge.

// serialPositionSuffix is the disambiguating tail for a work whose title cannot
// tell it from its siblings: "book-<position>", with the position slugified so
// an omnibus range ("1-3.5") stays a valid slug.
//
// The range separator is spelled "-to-" rather than slugified away. A slug may
// not carry a '.', so a plain Slugify maps the DECIMAL position "2.5" and the
// RANGE "2-5" onto one tail, "book-2-5" - and two volumes given one slug is the
// collapse this whole pre-pass exists to prevent. Writing the range out keeps
// them apart ("book-2-5" against "book-2-to-5") and reads as what it is.
func serialPositionSuffix(pos string) string {
	slug := Slugify(strings.ReplaceAll(pos, "-", " to "))
	if slug == "" {
		return ""
	}
	return "book-" + slug
}

// seriesScopedSuffix is serialPositionSuffix with the SERIES named too, for a
// group whose rows claim positions in more than one series. Without it, "book
// 1 of Alpha" and "book 1 of Beta" - two different books, published under one
// title by one author - mint the same suffix and collapse into one work, which
// is the very outcome the suffix was added to prevent. The series comes first
// so the reading is "the Alpha serial, book 1".
//
// It is spent only where it is needed: naming the series on every suffixed work
// would put "bravelands-bravelands-book-1" in the tree for the ordinary
// single-series case, where the title already says which serial it is.
func seriesScopedSuffix(series, pos string) string {
	tail := serialPositionSuffix(pos)
	name := Slugify(series)
	if tail == "" || name == "" {
		return tail
	}
	return name + "-" + tail
}

// ---------------------------------------------------------------------------
// Finding a suffixed work again
//
// The pre-pass only fires on a BATCH that carries two same-titled volumes at
// once. A later run bringing ONE of those volumes composes the bare base and
// would mint a duplicate beside the suffixed work already in the tree - the tree
// holds the first of them since seed wave 6 (id-rather-have-a-cat-...-book-1 and
// -book-2), so this is a live gap rather than a hypothetical one.
//
// A row that states a series position can address the suffixed slugs itself:
// the tail is a pure function of (series, position), both of which the row
// carries. So the claim-bearing row PROBES them (posSuffixSlugs, wired into
// workCandidates), and only a row that states the claim ever looks there - which
// is what keeps the probe off the 258 works whose slug merely LOOKS suffixed
// because their title ends "... Book 3".

// positionClaim is a row's serial claim reduced to the two strings the suffix
// formulas need. The zero value states no claim and probes nothing.
type positionClaim struct {
	series string
	pos    string
}

// posSuffixSlugs returns the slugs a work carrying this claim's position suffix
// would sit at: the plain "book-<position>" tail and the series-scoped form, in
// that order (the scoped one is spent only on a multi-series group, so it is the
// rarer place to look). Composition is BoundedSlugTail over the same tails
// getOrCreateWork appends, so a probe lands where the pre-pass would have minted
// and the two cannot drift.
//
// A tail that adds nothing (an unslugifiable position) and a slug equal to the
// base or to an earlier probe are dropped: the base is already candidate zero.
func posSuffixSlugs(base string, c positionClaim) []string {
	if c.pos == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, tail := range []string{serialPositionSuffix(c.pos), seriesScopedSuffix(c.series, c.pos)} {
		if tail == "" {
			continue
		}
		slug := BoundedSlugTail(base, "-"+tail)
		if slug == base || slices.Contains(out, slug) {
			continue
		}
		out = append(out, slug)
	}
	return out
}

// serialTitleKey is the grouping key for the pre-pass: the resolved work title's
// slug plus the row's identity author set, sorted so the key is order-free.
func serialTitleKey(title string, authors workAuthors) string {
	ids := append([]string(nil), authors.identity...)
	slices.Sort(ids)
	return Slugify(title) + "\x00" + strings.Join(ids, ",")
}

// serialPositionSuffixes is the batch pre-pass: for every book it returns the
// position suffix its work slug must carry, or "" when the row needs none.
//
// titles are resolveWorkTitles' output, so this runs on the titles that will
// actually be used - a group the full-title fallback already separated is never
// reached. Rows in a firing group that state no valid series claim get no
// suffix: they keep the bare base, which is the behaviour they have today, and
// there is nothing to name them by.
func (p *planner) serialPositionSuffixes(books []sourceBook, titles []string) []string {
	suffixes := make([]string, len(books))
	type rowClaim struct {
		idx    int
		series string
		pos    string
	}
	groups := map[string][]rowClaim{}
	for i, b := range books {
		name, pos, ok := b.primarySeriesClaim()
		if !ok {
			continue
		}
		credits := p.rowAuthorCredits(b)
		if len(credits) == 0 {
			continue
		}
		key := serialTitleKey(titles[i], p.rowWorkAuthorsRO(credits))
		groups[key] = append(groups[key], rowClaim{idx: i, series: strings.ToLower(name), pos: pos})
	}
	for _, rows := range groups {
		if len(rows) < 2 {
			continue
		}
		positions := map[string]map[string]bool{}
		for _, r := range rows {
			if positions[r.series] == nil {
				positions[r.series] = map[string]bool{}
			}
			positions[r.series][r.pos] = true
		}
		split := false
		for _, seen := range positions {
			if len(seen) > 1 {
				split = true
				break
			}
		}
		if !split {
			continue
		}
		// A group spanning several series needs the SERIES in the tail as well
		// as the position, or its two "book 1"s land on one slug. A group inside
		// one series does not, and pays nothing for the case it is not in.
		scoped := len(positions) > 1
		for _, r := range rows {
			if scoped {
				suffixes[r.idx] = seriesScopedSuffix(r.series, r.pos)
			} else {
				suffixes[r.idx] = serialPositionSuffix(r.pos)
			}
		}
	}
	return suffixes
}
