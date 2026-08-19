package model

import "slices"

// reserved.go holds the slugs the API's own routing has taken out of the id
// namespaces, and the one deterministic way to step off them.
//
// The API addresses a record by its slug - /api/v1/works/{id},
// /api/v1/people/{id}, /api/v1/series/{id} - and it also serves LITERAL segments
// in those same namespaces: /api/v1/works/latest and the three type-scoped
// searches (/api/v1/works/search, /api/v1/people/search, /api/v1/series/search).
// A literal beats a wildcard in ServeMux's precedence rules, so a record whose id
// IS one of those words is unreachable through its family's {id} route: it can be
// found by search and then never opened. The catalogue really did hold one - the
// work "Search" by Alyssa Rose Ivy, minted before the typed search routes existed
// and renamed in the same change that added this rule.
//
// Both words are reserved in ALL THREE families rather than only where a route
// exists today. The families are symmetric by design (every one of them has a
// {id} route and may grow a literal), and a rule a contributor can state in one
// sentence - "no record is called search or latest" - is worth more than a table
// of which family reserves which word.
//
// Recording ids are deliberately NOT covered: a recording is addressed at
// /works/{id}/recordings/{rid}/chapters, where the literal FOLLOWS the wildcard,
// so no recording slug can shadow anything.
//
// Enforced by pkg/check (checkReservedSlug) and honoured at every mint:
// internal/importer's work, series and person chains and internal/issueform's
// compose paths all step off these words rather than claim them.
// The list is sorted, so a message that names it reads the same every time.
var reservedSlugs = []string{"latest", "search"}

// IsReservedSlug reports whether s is a route literal no record may be stored
// under. See the file comment for why.
func IsReservedSlug(s string) bool { return slices.Contains(reservedSlugs, s) }

// ReservedSlugs returns the reserved words, sorted, for a message that has to
// name them.
func ReservedSlugs() []string { return slices.Clone(reservedSlugs) }

// reservedPersonSuffix is what a reserved person slug takes instead. It is a
// word rather than a number because a person slug is derived from the NAME and
// verified against it (checkPersonSlug), so the variant has to be a pure
// function of the reserved word - "search-2" would raise "which one is -3?" the
// moment a second person named Search arrives, and neither minting nor
// verification could answer it from the name alone.
const reservedPersonSuffix = "-person"

// The two COMMUNITY GUIDE path segments: the literals that follow a work's slug
// on the pages internal/serve renders at /works/{id}/recap and
// /works/{id}/characters.
//
// They live here for the same reason the reserved words above do - they are
// route literals two packages have to agree on, letter for letter. internal/serve
// composes the route pattern, the canonical URL, the work page's cross-links and
// the sitemap families' suffixes from them; internal/issueform composes the
// pattern that RESOLVES such a URL back to a work slug, because a contributor
// who spots a gap in a recap pastes the guide page's URL into the form. A second
// spelling on either side is a contribution the bot cannot place, or a link to a
// 404, and neither side would fail a test.
//
// They are deliberately NOT reserved slugs: a literal that FOLLOWS the {id}
// wildcard shadows no record, exactly as the chapters route's literal does not
// (see the note on recording ids above).
const (
	GuideSegmentRecap      = "recap"
	GuideSegmentCharacters = "characters"
)

// ReservedPersonSlug is the single canonical id a person whose name slugs to a
// reserved word is stored under: the reserved word plus a fixed suffix, e.g. a
// person actually named "Search" is `search-person`.
//
// It exists because two rules would otherwise contradict each other. A person's
// id must BE the slug of their name (checkPersonSlug, the rule that keeps a
// record reachable from the name the next import will slug), and no id may be a
// route literal (checkReservedSlug) - a person named Search could satisfy
// neither. This is the one variant BOTH rules accept: PersonSlug returns it, so
// the importer and the issue forms mint it, and checkPersonSlug expects it,
// because both sides ask the same function.
//
// The suffix cannot itself be reserved (no reserved word ends in "-person"), so
// one application always lands on a legal slug.
func ReservedPersonSlug(reserved string) string { return reserved + reservedPersonSuffix }
