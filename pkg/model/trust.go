package model

// trust.go is the SOURCE TRUST TIER model: the one place that ranks a
// sources[] entry by the kind of act that produced it, and the one place that
// answers "is this record still nothing but a bulk-mirror seed?".
//
// It exists because the catalogue is about to hold ~135k records seeded from
// the libex mirror. Those records are real facts, but nobody has attested them:
// no person has said "this is my book and these are its details". When someone
// eventually does - by importing their own library, or by submitting a form -
// their data must be able to WIN over the seed, so that over time real users
// become the provenance. After that first attestation the record leaves the
// bulk-mirror tier and the ordinary rules resume (existing wins, fill only what
// is absent), so two attesting users can never churn a record back and forth.
//
// The policy this encodes is written up in LICENSING.md ("Trust tiers and the
// user-overwrite rule"); the writers that apply it are internal/importer and
// internal/issueform. Both go through the helpers here: the tier of a source
// type is decided ONCE, in one table, so no caller compares a source-type
// string of its own.
type SourceTier int

// The trust ordering. Only the two ends carry policy today; the ranks are
// comparable so a future rule can express "at least this tier" without adding a
// second vocabulary.
const (
	// TierBulkMirror is the lowest rank: a record (or a fact) that arrived in a
	// bulk import from a third-party catalogue mirror. It is machine-seeded and
	// unattested, so a higher tier may overwrite it.
	TierBulkMirror SourceTier = iota
	// TierReference is the middle rank: a crosswalk or reference lookup
	// (openlibrary, wikidata, inventaire, audible-lookup, community) and any
	// source type this table does not know. No policy attaches to it - its
	// practical effect is that such a record is NOT bulk-mirror-only, so the
	// overwrite rule never touches it. Unknown types land here deliberately: a
	// source type added to the schema without a decision here must not become
	// silently overwritable.
	TierReference
	// TierUserLibrary is the highest rank: a real person's own library or a
	// hand-authored submission - an OpenAudible, Libation or audiosilo-books
	// (metascan / site import) export, or an issue-form submission. This is the
	// tier that carries overwrite authority over TierBulkMirror.
	TierUserLibrary
)

// bulkMirrorSources are the source types that mean "seeded in bulk from a
// third-party mirror". Today this is exactly libex-import, which is why the
// policy is written as "libex-only" in the docs; a second mirror source would
// join this table and nothing else would change.
var bulkMirrorSources = map[string]bool{
	"libex-import": true,
}

// userLibrarySources are the source types that mean "a person attested this":
// their own library export, or their own submission. Keep this list and the
// schema's source-type enum (schema/common.schema.json #/$defs/source) in step
// - TestSourceTiersCoverSchemaEnum pins that every enum value is classified.
var userLibrarySources = map[string]bool{
	"user":                   true, // an issue-form submission or a hand edit
	"openaudible-import":     true,
	"libation-import":        true,
	"audiosilo-books-import": true, // metascan folder scans and the site's /import
}

// TierOfSource ranks one sources[] entry type. An unrecognized type is
// TierReference (see the constant): unknown means "not classified", never
// "overwritable".
func TierOfSource(sourceType string) SourceTier {
	switch {
	case userLibrarySources[sourceType]:
		return TierUserLibrary
	case bulkMirrorSources[sourceType]:
		return TierBulkMirror
	default:
		return TierReference
	}
}

// BulkMirrorOnly reports whether a record is still nothing but a bulk-mirror
// seed: it has at least one source and EVERY one of them is bulk-mirror-tier.
// This is the "libex-only" test the provenance policy is written in terms of.
//
// A record with no sources at all is not bulk-mirror-only. The schema requires
// sources[], so that shape should not exist; reading it as "not overwritable"
// is the safe answer for a record whose provenance we cannot see.
func BulkMirrorOnly(sources []Source) bool {
	types := make([]string, 0, len(sources))
	for _, s := range sources {
		types = append(types, s.Type)
	}
	return BulkMirrorOnlyTypes(types)
}

// BulkMirrorOnlyTypes is BulkMirrorOnly over bare type strings, for a writer
// holding a record as decoded JSON rather than as a typed entity. Both forms
// fold the same table, so the two callers can never drift on the rule.
func BulkMirrorOnlyTypes(types []string) bool {
	if len(types) == 0 {
		return false
	}
	for _, t := range types {
		if TierOfSource(t) != TierBulkMirror {
			return false
		}
	}
	return true
}
