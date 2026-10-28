package model

import "strings"

// slugtail.go is the BOUNDED SLUG TAIL: a base slug joined to a disambiguating
// tail within MaxSlugLen. It lives in this leaf package (not internal/importer,
// where it was written) because a slug rule pkg/check verifies has to be
// composed by the one function both the writer and the checker call -
// LibrivoxReaderSlug is the first such rule that needs a tail.

// BoundedSlugTail joins a base slug and a disambiguating tail (an author credit,
// a release year, a numeric collision suffix) into a slug of at most
// MaxSlugLen, shortening only the BASE. The tail's END survives every regime by
// design: it carries whatever tells one candidate from the next, so cutting it
// would collapse a collision chain onto one repeated slug - the defect this
// exists to prevent.
//
// The base is cut at a hyphen (word) boundary when one leaves room, which also
// keeps the join a valid slug; a base whose leading word alone overruns the room
// is hard-cut instead, with the cut edge trimmed so no stray hyphen meets the
// tail. Callers pass a NONEMPTY base that is already a valid slug (every caller
// substitutes a fallback token for an empty Slugify result), so the result can
// never be empty or start with a hyphen. Slugs are ASCII by construction
// (Slugify folds everything else away), so every cut here is a byte index that
// cannot split a rune.
//
// Bounding does NOT make a candidate globally unique: two bases agreeing up to
// the cut produce the same candidate. Chain walkers are built for that - see
// internal/importer's NumberedSlugAt and workSlugAt.
func BoundedSlugTail(base, tail string) string {
	slug, _ := BoundedSlugTailCut(base, tail)
	return slug
}

// BoundedSlugTailCut is BoundedSlugTail reporting whether it had to SHORTEN base -
// the signal a work-chain candidate carries (workCandidate.shortened), so "the
// title was cut" is read off the one place that cuts it rather than re-derived.
func BoundedSlugTailCut(base, tail string) (slug string, cut bool) {
	if slug, ok := WordBoundedSlugTail(base, tail); ok {
		return slug, len(base)+len(tail) > MaxSlugLen
	}
	room := MaxSlugLen - len(tail)
	if room <= 0 {
		// Defensive regime no current caller reaches (the longest tail any of them
		// composes is an author credit plus a numeric suffix, ~53 bytes): a tail
		// that alone fills the cap leaves nothing of the base, and the suffix lives
		// at the tail's END, so keep the END and drop the tail's own head - at a
		// hyphen boundary when there is one.
		at := len(tail) - MaxSlugLen
		if b := strings.IndexByte(tail[at:], '-'); b >= 0 {
			at += b
		}
		return strings.Trim(tail[at:], "-"), true
	}
	return strings.TrimRight(base[:room], "-") + tail, true
}

// WordBoundedSlugTail is BoundedSlugTail's whole-words half: it reports the
// joined slug when base+tail fits within MaxSlugLen outright or after cutting
// base at a hyphen boundary, and ok=false when only a hard cut can fit it.
// Callers that want to shrink their own tail before conceding to a hard cut ask
// this first.
func WordBoundedSlugTail(base, tail string) (string, bool) {
	if len(base)+len(tail) <= MaxSlugLen {
		return base + tail, true
	}
	// len(base) > room follows from the overflow, so base[:room+1] is in range.
	if room := MaxSlugLen - len(tail); room > 0 {
		if cut := strings.LastIndexByte(base[:room+1], '-'); cut > 0 {
			return base[:cut] + tail, true
		}
	}
	return "", false
}
