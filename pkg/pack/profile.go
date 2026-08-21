package pack

import (
	"fmt"
	"slices"
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

// profileOrder is the order Profiles reports and a flag's usage text reads in:
// widest first, which is also the order they were introduced.
var profileOrder = []Profile{ProfileAll, ProfileCore, ProfileCommunity}

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
func (p Profile) Has(f Family) bool { return slices.Contains(p.def().families, f) }

// Excluded returns the data-relative paths this profile DISCLAIMS, sorted: the
// root directory of every family it does not hold, plus RedirectsFile when it
// carries no tombstone table. It is empty for ProfileAll.
//
// It is the subtractive twin of Families, and it exists because a pass that
// walks the tree by PATH rather than by family still has to honour the profile.
// metafmt's canonical formatting is the one: it is layout-agnostic on purpose
// (pkg/canonical knows nothing about families), so it walks the whole root and
// is handed this list to skip. Without it a subset-profile --write reformatted -
// and, for a legacy family the profile's own legacy check no longer reached,
// rewrote in place - files the profile disclaims: working-tree churn in exactly
// the directory that is about to be extracted byte-for-byte into another
// repository.
//
// A path here is NOT "ignored": it is out of this tree's scope, and pkg/check
// still reports every file under it as an unrecognized location. One tree, one
// answer about what belongs in it.
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
// stable order the package-level Families uses - through the same sortedDefs, so
// a profile's list is a sub-sequence of the full table's rather than a second
// ordering that happens to agree.
func (p Profile) Families() []FamilyDef {
	fams := p.def().families
	out := make([]FamilyDef, 0, len(fams))
	for _, f := range fams {
		if d, ok := defs[f]; ok {
			out = append(out, d)
		}
	}
	return sortedDefs(out)
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

// Profiles returns every profile name, for a flag's usage text.
func Profiles() []string {
	out := make([]string, 0, len(profileOrder))
	for _, p := range profileOrder {
		out = append(out, string(p))
	}
	return out
}
