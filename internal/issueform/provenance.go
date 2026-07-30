package issueform

import (
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// sourceLibexImport is the schema source type for a fact that came out of a
// libex lookup (common.schema.json #/$defs/source). It matches the type
// `metaimport libex` stamps, so a book seeded through the site's /add assist
// carries the same provenance as one imported in bulk from the same mirror.
const sourceLibexImport = "libex-import"

// libexMarkerRE matches the structured marker line the site's /add lookup assist
// writes as the FIRST line of the Sources field ("libex: B015RQON6I"). The site
// composes it in exactly this shape (site/src/lib/libex-map.ts composeSources),
// so anything else - a human's own prose, a differently spelled marker - simply
// does not match and rides as free text, exactly as before.
var libexMarkerRE = regexp.MustCompile(`^libex:\s*([A-Za-z0-9]{10})\s*$`)

// splitLibexMarker sniffs the Sources field's first line for the libex marker.
// It returns the normalized (upper-case) ASIN and the remaining sources text
// with the marker line removed. asin is "" when the first line is not a marker,
// in which case rest is the input unchanged - a submission that was not seeded
// by the lookup assist is composed byte-identically to before.
func splitLibexMarker(raw string) (asin, rest string) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	first, tail, _ := strings.Cut(trimmed, "\n")
	m := libexMarkerRE.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", raw
	}
	// The regex already pins the 10-character ASIN grammar; NormalizeASIN is the
	// shared upper-casing so a marker and a rec_asins line yield the same value.
	if asin = importer.NormalizeASIN(m[1]); asin == "" {
		return "", raw
	}
	return asin, strings.TrimSpace(tail)
}

// noteSubmissionASIN records a normalized ASIN the submission stated in its own
// ASIN field. parseASINs is the only caller; sources() reads the set.
func (c *composer) noteSubmissionASIN(asin string) {
	if c.submissionASINs == nil {
		c.submissionASINs = make(map[string]bool, 2)
	}
	c.submissionASINs[asin] = true
}

// sources builds the provenance list every record composed from one submission
// carries. A submission seeded by the site's /add lookup assist opens its
// Sources field with a `libex: <ASIN>` marker line; that is a machine-written
// statement of WHERE the facts came from, so it becomes a TYPED source entry
// (libex-import, ref = the ASIN) rather than being buried in the human free
// text. The human-authored remainder still rides as the `user` entry, so the
// contributor's own provenance note is never lost.
//
// The marker is only honored when its ASIN is one the submission ITSELF states
// in its ASIN field. A typed source is a machine-auditable claim ("these facts
// came from libex's record for this ASIN"), so stamping an ASIN the submission
// does not carry would record a provenance ref nothing in the record backs -
// and, because internal/importer's appendSourceUnique dedups on (type, ref), it
// would also suppress a later genuine libex stamp for that ASIN. The site's own prefill always
// satisfies the check: composeSources and formatAsinLine emit the same ASIN.
// An unbacked marker is not an error - the whole Sources text simply rides as
// free text, exactly as it does for a hand-written field.
//
// When the marker line is all the field held, the user entry keeps the original
// text verbatim: Sources is required for provenance, and a ref-less user source
// would record less than the submitter wrote.
func (c *composer) sources(ref string) []outSource {
	asin, rest := splitLibexMarker(ref)
	if asin == "" || !c.submissionASINs[asin] {
		return []outSource{c.source(ref)}
	}
	if rest == "" {
		rest = strings.TrimSpace(ref)
	}
	return []outSource{
		{Type: sourceLibexImport, Ref: asin, ImportedAt: c.date},
		c.source(rest),
	}
}
