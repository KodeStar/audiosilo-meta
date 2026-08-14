package model

import "slices"

// redirect.go holds the slug TOMBSTONE table: which retired slug now stands for
// which live record.
//
// A slug is public API. meta.audiosilo.app addresses every record by it, every
// audiosilo-sidecars install stores it in books.work_id, a contributed sidecar
// references its work by it, and audiosilo-server's community-metadata seam
// carries it. So a data-quality repair that MERGES two duplicate records cannot
// simply drop the slug it retires: the id has to keep resolving, or every
// consumer holding it is broken.
//
// The table is one file at the data root (data/redirects.json, see pkg/pack's
// RedirectsFile and pkg/redirects), deliberately NOT a pack family: it is a
// single small map keyed by a slug that is no longer a record, so it has nothing
// to place, split or bound, and a retired slug is not an entity to validate
// against a family's schema.
//
// The rules pkg/check enforces (checkRedirects) are what make it a tombstone
// rather than a second addressing scheme: a source is never a live id, a target
// always is, and no target is itself a source - the writer collapses chains
// (pkg/redirects.Add), the checker refuses them, so resolution is always ONE
// lookup and can never loop.

// RedirectKind is the id namespace a redirect applies to. Its value is both the
// family directory name under the data root and the API path segment that
// addresses it (/api/v1/works/{id} and friends), which is what lets the server
// build a redirect's Location from the kind it looked the redirect up by rather
// than from a second table mapping one vocabulary onto another.
//
// It is deliberately NOT the artifact's search_fts kind vocabulary ("work",
// "person", "series"), which names an ENTITY: this names the ROUTE.
type RedirectKind string

const (
	// RedirectWorks is the works id namespace (/api/v1/works/{id}).
	RedirectWorks RedirectKind = "works"
	// RedirectPeople is the people id namespace (/api/v1/people/{id}).
	RedirectPeople RedirectKind = "people"
	// RedirectSeries is the series id namespace (/api/v1/series/{id}).
	RedirectSeries RedirectKind = "series"
)

// redirectKinds is the closed set of namespaces a redirect may name, sorted -
// which is also the order the canonical file renders them in. Recordings are
// deliberately absent: a recording is addressed inside its work, so a merge
// retires the WORK slug and the recording travels with it.
var redirectKinds = []RedirectKind{RedirectPeople, RedirectSeries, RedirectWorks}

// RedirectKinds returns the namespaces a redirect may name, sorted.
func RedirectKinds() []RedirectKind { return slices.Clone(redirectKinds) }

// ValidRedirectKind reports whether k is one of the three namespaces.
func ValidRedirectKind(k RedirectKind) bool { return slices.Contains(redirectKinds, k) }

// Redirects is the whole tombstone table: per namespace, a map from a retired
// slug to the live slug that replaced it.
//
// It is a map rather than a struct with three fields so that every reader,
// writer and rule iterates RedirectKinds() and there is no field list a fourth
// namespace could be left out of. Its JSON form is exactly the file's -
// {"people": {...}, "series": {...}, "works": {...}} - because RedirectKind is a
// string kind, so no marshalling code stands between the type and the contract.
type Redirects map[RedirectKind]map[string]string

// NewRedirects returns an empty table with every namespace present, which is the
// shape the file always carries (the schema requires all three keys, so the
// canonical bytes do not change shape the first time a namespace gains an
// entry).
func NewRedirects() Redirects {
	out := make(Redirects, len(redirectKinds))
	for _, k := range redirectKinds {
		out[k] = map[string]string{}
	}
	return out
}

// Target returns the live slug from redirects to in namespace kind, or "" when
// nothing does. One lookup is always enough: chains are refused by pkg/check and
// collapsed by pkg/redirects.Add.
func (r Redirects) Target(kind RedirectKind, from string) string { return r[kind][from] }

// Len is the number of redirects across every namespace.
func (r Redirects) Len() int {
	n := 0
	for _, table := range r {
		n += len(table)
	}
	return n
}
