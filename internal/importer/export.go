package importer

import (
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// export.go holds the thin exported wrappers other writers in this module reach
// this package's normalization rules through.
//
// They exist so a second writer never re-derives a rule this package already
// owns - the vocabularies here are evidence-driven and measured over the full
// libex dump, and a re-implementation elsewhere is a contract with two
// definitions. BoundedSlugTail (exported for internal/issueform) set the
// precedent; internal/remediate is the second caller.
//
// Every one of these is a one-liner over the unexported original. Nothing about
// the rules lives here.

// MapRegion lowercases a marketplace word, resolves the known aliases (libex's
// ISO-shaped "gb" is the schema's "uk"), and reports whether the result is a
// region the schema accepts. It is the one place a source's region spelling
// becomes a stored one.
func MapRegion(word string) (region string, ok bool) { return mapRegion(word) }

// ISODatePart reduces an ISO or SQL timestamp to its YYYY-MM-DD date part,
// cutting either separator a source may use. A value that is already a date, or
// a partial one ("2018-10"), is returned unchanged for the caller to validate.
func ISODatePart(ts string) string { return isoDatePart(ts) }

// SortCredits puts a work's credits in the canonical (person, role) order, the
// one byte-form a given set of credits ever has on disk.
func SortCredits(credits []model.Credit) { sortCredits(credits) }

// Chapters maps a source's chapters array onto the recording chapter shape,
// applying the same structural rules every import does: titles trimmed (an
// empty one numbered), offsets read under either spelling a source documents
// (snake_case or camelCase), and the whole list DROPPED with a warning on any
// violation of the monotonic-from-zero rule metacheck enforces.
//
// raw is the decoded JSON value of the row's "chapters" member; anything that
// is not an array of objects yields nil.
func Chapters(raw any, warn func(string, ...any)) []model.Chapter {
	built := buildChapters(rawBook{"chapters": raw}.chapters(), warn)
	if len(built) == 0 {
		return nil
	}
	out := make([]model.Chapter, 0, len(built))
	for _, c := range built {
		out = append(out, model.Chapter{Title: c.Title, StartMS: c.StartMS, LengthMS: c.LengthMS})
	}
	return out
}

// CollectiveIDs returns the person ids the collective fold produces: the
// records that stand for a cast or for an absent identity rather than for a
// named performer (full-cast, various, anonymous, uncredited, unknown).
//
// It is derived from the fold table rather than restated, so a sixth bucket
// added there is a sixth id here - which is what stops a caller that reasons
// about "did this credit name anybody" from silently going stale.
func CollectiveIDs() map[string]bool {
	out := make(map[string]bool, 8)
	for _, canonical := range collectiveCredits {
		id, _ := model.PersonSlug(canonical)
		out[id] = true
	}
	return out
}
