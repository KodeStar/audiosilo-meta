package titlerule

import (
	"regexp"
	"strings"
)

// edition.go holds the EDITION-MARKER rule: what a trailing
// "(Unabridged)"/"[Abridged]" decoration is, how to detect one and how to remove
// one.
//
// It USED TO LIVE in internal/importer, which strips it before work identity, and
// this package read it from there through the exported HasEditionMarker door. That
// arrangement had exactly one consequence, stated in the package doc: internal/
// importer and pkg/check could not import this package, because the importer
// imports pkg/check and either direction would then be a cycle.
//
// Both of those packages are now consumers. The intake-time duplicate prevention
// needs the normalized-title identity key (IdentityTitleKey) in three places - the
// intake gate (internal/issueform), the bulk importer's create guard
// (internal/importer) and the tree census (pkg/check's advisory) - so the rule had
// to become reachable from the importer and from pkg/check, and the way to do that
// is for the one definition to sit at the LEAF, exactly as model.Slugify does.
//
// So the rule moved DOWN rather than being copied: internal/importer's
// cleanWorkTitle now strips through StripEditionMarkers, this package's decoration
// detector asks HasEditionMarker, and there is still one regexp. The reason the
// importer's version was the rule of record in the first place is unchanged and is
// why the move is a move and not a second copy: a hand-written twin here had
// already drifted (it made the brackets optional, matching a bare trailing
// "Unabridged", and missed stacked markers).
//
// What did NOT move: abridgedFromMarker, which reads WHICH edition a marker states
// to seed a recording's tri-state flag. That is an importer field-mapping decision
// over its own source rows, not a title rule, and nothing else asks it.

// editionMarkerRE matches one or more stacked trailing (Unabridged)/(Abridged)
// edition markers (parens or brackets), with their surrounding whitespace, so a
// work title carries no edition decoration - unabridged-ness lives on the
// recording's tri-state abridged flag, not in the work's identity.
var editionMarkerRE = regexp.MustCompile(`(?i)(?:\s*[([](?:un)?abridged[)\]])+\s*$`)

// HasEditionMarker reports whether a title carries a trailing
// (Unabridged)/(Abridged) edition marker.
func HasEditionMarker(title string) bool { return editionMarkerRE.MatchString(title) }

// StripEditionMarkers removes every stacked trailing edition marker in one pass
// and trims what is left. It may return "" for a title that is nothing BUT a
// marker; the caller decides what to do with that (internal/importer's
// cleanWorkTitle returns the title unchanged rather than emptying it).
func StripEditionMarkers(title string) string {
	return strings.TrimSpace(editionMarkerRE.ReplaceAllString(strings.TrimSpace(title), ""))
}
