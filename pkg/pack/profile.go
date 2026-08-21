package pack

import (
	"fmt"
	"sort"
	"strings"
)

// profile.go is the TREE PROFILE: which families a data root deliberately holds,
// and whether it carries the slug tombstone table beside them.
//
// It exists because the CC BY-SA community layer is moving to a repository of its
// own (the community-repo split, phase 1). Two roots then exist, each holding a
// SUBSET of the four families this package defines: the core tree keeps
// works/people/series plus data/redirects.json, and the community tree holds
// works-community alone. Everything else about a root - the pack math, the caps,
// the placement rules, the schemas - is identical, so a profile is deliberately
// the SMALLEST thing that can express the difference: a family set, plus the one
// bit about the aux file.
//
// The load-bearing consequence is the file ACCOUNTING (see Listing.add). A family
// that is not in the profile is not "ignored": its root is no longer a family
// root, so every file under it falls into Stray and pkg/check reports it as an
// unrecognized location. That is what makes a leftover data/works-community/ in
// the core repo, or a stray data/works/ in the community repo, a RED check rather
// than a directory nobody reads - and it costs no rule of its own, because "every
// file is accounted for" was already total.
//
// ProfileAll is the DEFAULT everywhere: the zero value resolves to it, and every
// entry point that does not name a profile takes it. Nothing in this repo yet
// runs on anything else - the profile flag on metacheck/metafmt is how the
// community tree gets checked before the split lands, and flipping core's own
// default is a later change.
type Profile string

const (
	// ProfileAll is every family plus the tombstone table: one tree holding the
	// whole database, which is what this repo is today. It is the default.
	ProfileAll Profile = "all"
	// ProfileCore is the CC0 core: works, people and series, plus the tombstone
	// table (a retired slug is core glue - it names a core record).
	ProfileCore Profile = "core"
	// ProfileCommunity is the CC BY-SA layer alone: works-community, and NO
	// tombstone table. A redirects.json under a community root is an
	// unrecognized file, not an empty table: the slugs it would retire are not
	// this tree's to retire.
	ProfileCommunity Profile = "community"
)

// profileDef is one profile's content.
type profileDef struct {
	families  []Family
	redirects bool
}

var profileDefs = map[Profile]profileDef{
	ProfileAll: {
		families:  []Family{FamilyWorks, FamilyWorksCommunity, FamilyPeople, FamilySeries},
		redirects: true,
	},
	ProfileCore: {
		families:  []Family{FamilyWorks, FamilyPeople, FamilySeries},
		redirects: true,
	},
	ProfileCommunity: {
		families:  []Family{FamilyWorksCommunity},
		redirects: false,
	},
}

// A profile's family SET and ORDER are static, and the set (Has, via Store.def
// and Listing.add) sits on per-record and per-file hot paths, so both are
// precomputed here once: the membership set Has reads, and the family order
// Families walks (sorted through sortedDefs, whose key is the family name, so
// the order cannot depend on the caps). The DEFINITIONS are deliberately not
// snapshotted - defs is read at call time, because tests narrow a family's caps
// through it (withCaps) and a stale copy would answer with the wrong caps. The
// init loop panics on a profileDefs entry naming a family absent from defs -
// table integrity is a programmer error by the same standard mustHold applies -
// so a typo'd future profile fails the first test run instead of silently
// shrinking.
var (
	profileFamilyOrder = map[Profile][]Family{}
	profileHas         = map[Profile]map[Family]bool{}
)

func init() {
	for p, d := range profileDefs {
		set := make(map[Family]bool, len(d.families))
		fams := make([]FamilyDef, 0, len(d.families))
		for _, f := range d.families {
			fd, ok := defs[f]
			if !ok {
				panic(fmt.Sprintf("pack: profile %q names unknown family %q", p, f))
			}
			set[f] = true
			fams = append(fams, fd)
		}
		order := make([]Family, 0, len(fams))
		for _, fd := range sortedDefs(fams) {
			order = append(order, fd.Family)
		}
		profileFamilyOrder[p] = order
		profileHas[p] = set
	}
}

// resolve maps the zero value onto the default. A profile travels in struct
// fields (a Listing's, a Store's), so "unset" has to mean something, and the only
// meaning that keeps every existing caller byte-identical is "the whole tree".
func (p Profile) resolve() Profile {
	if p == "" {
		return ProfileAll
	}
	return p
}

// def returns the profile's content. An unknown profile has none, which is
// unreachable through the constructors: each validates before it walks (see
// ListProfile), so a profile that reaches here names a table entry.
func (p Profile) def() profileDef { return profileDefs[p.resolve()] }

// Valid reports whether p names a profile. The zero value is valid - it is
// ProfileAll.
func (p Profile) Valid() bool {
	_, ok := profileDefs[p.resolve()]
	return ok
}

// String returns the profile's name, which is also its flag spelling.
func (p Profile) String() string { return string(p.resolve()) }

// Has reports whether family f is one this profile's root may hold. Every rule
// that is about TWO families asks it before it runs: a rule whose other side is
// not in the tree has nothing to check, and reporting the absence as a violation
// would make a valid single-layer tree red (see check.Load).
func (p Profile) Has(f Family) bool { return profileHas[p.resolve()][f] }

// Excluded returns the data-relative paths this profile DISCLAIMS, sorted: the
// root directory of every family it does not hold, plus RedirectsFile when it
// carries no tombstone table. It is empty for ProfileAll.
//
// It is the subtractive twin of Families, for the one pass that walks the tree
// by PATH rather than by family - metafmt's canonical formatting. The full
// rationale (why subtractive, and the failure the scoping prevents) lives on
// format.CheckProfile; a path here is not "ignored" but out of this tree's
// scope, which pkg/check reports as an unrecognized location.
func (p Profile) Excluded() []string {
	var out []string
	for _, d := range Families() {
		if !p.Has(d.Family) {
			out = append(out, d.Family.Root())
		}
	}
	if !p.Redirects() {
		out = append(out, RedirectsFile)
	}
	sort.Strings(out)
	return out
}

// Redirects reports whether this profile's root carries the slug tombstone table
// (RedirectsFile). A profile without it treats that path like any other
// unrecognized file.
func (p Profile) Redirects() bool { return p.def().redirects }

// Families returns the definitions of the families in this profile, in the same
// stable order the package-level Families uses - sorted through the same
// sortedDefs at init, so a profile's list is a sub-sequence of the full table's
// rather than a second ordering that happens to agree. The definitions are read
// from defs at call time (see the precompute note above); the returned slice is
// the caller's to keep.
func (p Profile) Families() []FamilyDef {
	fams := profileFamilyOrder[p.resolve()]
	out := make([]FamilyDef, 0, len(fams))
	for _, f := range fams {
		out = append(out, defs[f])
	}
	return out
}

// Roots returns the family root directory names in this profile, sorted. It is
// what a message naming "the roots a file could have been under" reads from.
func (p Profile) Roots() []string {
	fams := p.Families()
	out := make([]string, 0, len(fams))
	for _, d := range fams {
		out = append(out, d.Family.Root())
	}
	return out
}

// ParseProfile maps a flag value onto a profile. It is the one place a profile
// name is read from text, so a CLI never has to spell the set of them.
func ParseProfile(s string) (Profile, error) {
	p := Profile(s)
	if !p.Valid() {
		return "", fmt.Errorf("unknown tree profile %q (want one of: %s)", s, strings.Join(Profiles(), ", "))
	}
	return p.resolve(), nil
}

// Profiles returns every profile name, widest first, for a flag's usage text.
func Profiles() []string {
	return []string{string(ProfileAll), string(ProfileCore), string(ProfileCommunity)}
}

// ProfileFlagUsage is the usage text the metacheck and metafmt CLIs give their
// --profile flag - stated once so the two (and any later consumer, metabuild's
// compose being next) cannot drift on the flag's contract.
var ProfileFlagUsage = "which families this data root holds: " + strings.Join(Profiles(), "|")
