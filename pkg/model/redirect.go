package model

import (
	"errors"
	"fmt"
	"slices"
)

// redirect.go holds the slug TOMBSTONE table. Redirects below is the canonical
// statement of what it is for and what must hold of it; everything else in the
// project that touches it points here rather than restating it.

// RedirectKind is the id namespace a redirect applies to. Its value is both the
// family directory name under the data root and the API path segment that
// addresses it (/api/v1/works/{id} and friends), which is what lets the server
// build a redirect's Location from the kind it looked the redirect up by rather
// than from a second table mapping one vocabulary onto another.
//
// It is deliberately NOT the artifact's search_fts kind vocabulary ("work",
// "person", "series"), which names an ENTITY: this names the ROUTE. And it is a
// type of its own rather than pack.Family, close as the two spellings are,
// because the import direction forbids it: pkg/pack imports pkg/model, and this
// is the leaf.
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

// Redirects is the slug TOMBSTONE table: per namespace, a map from a retired
// slug to the live slug that replaced it. It is stored as one file at the data
// root (data/redirects.json - pack.RedirectsFile; pkg/redirects reads and writes
// it, pkg/check validates it, internal/build compiles it into the artifact and
// internal/serve answers a retired slug with a 301 from it).
//
// WHY IT EXISTS. A slug is public API: meta.audiosilo.app addresses every record
// by it, every audiosilo-sidecars install stores it in books.work_id, a
// contributed sidecar references its work by it, and audiosilo-server's
// community-metadata seam carries it. So a data-quality repair that MERGES two
// duplicate records cannot simply drop the slug it retires - the id has to keep
// resolving, or every consumer holding it is broken.
//
// WHAT MUST HOLD OF IT, and this is the property every reader depends on:
// a source is never a live id, a target always is, and no target is itself a
// source. Chains are COLLAPSED as they are written (pkg/redirects.Add repoints
// both directions) and REFUSED when they are read (pkg/check's checkRedirects),
// so resolving a retired slug is exactly ONE lookup and can never loop.
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

// Len is the number of redirects across every namespace. It is the emptiness
// test callers need: the outer map always holds all three namespaces, so its own
// length says nothing about whether anything has been retired.
func (r Redirects) Len() int {
	n := 0
	for _, table := range r {
		n += len(table)
	}
	return n
}

// ErrReservedRedirectSlug is what a redirect naming an API route literal fails
// with. It is a sentinel so a caller can tell it from a malformed slug.
var ErrReservedRedirectSlug = errors.New("is a reserved slug: it names an API route segment, not a record")

// ValidRedirectSlug reports what is wrong with one side of a redirect, or nil.
// It is the ONE definition both the writer (pkg/redirects.Add, which refuses)
// and the checker (pkg/check's checkRedirects, which reports) ask, so the two can
// never disagree about what may appear in the table. The returned error reads as
// the tail of "source %q ..." / "target %q ...".
//
// A reserved word is excluded on BOTH sides for the same reason it is excluded
// from every id namespace (see reserved.go): as a target it would name a record
// that cannot exist, and as a source it names a route rather than a retired
// record.
func ValidRedirectSlug(s string) error {
	if !ValidSlug(s) {
		return fmt.Errorf("is not a valid slug (%s, at most %d characters)", slugPattern, MaxSlugLen)
	}
	if IsReservedSlug(s) {
		return ErrReservedRedirectSlug
	}
	return nil
}
