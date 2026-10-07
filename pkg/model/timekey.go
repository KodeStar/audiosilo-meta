package model

import "time"

// TimeKey returns a value that orders the project's date-or-timestamp values
// (`$defs/date_or_datetime`, `sources[].imported_at`) CHRONOLOGICALLY under
// string comparison. A plain date ("2026-07-29") is already such a key; a full
// RFC 3339 timestamp is not, because the offset travels with it -
// "2026-07-29T01:00:00Z" is later than "2026-07-29T02:00:00+05:00" but sorts
// before it - so it is normalized to UTC first. A date sorts before every
// timestamp on the same day (a date-only value is the start of its day), and a
// value that parses as neither is returned as it is, so an unvalidated caller
// gets an ordering rather than a panic.
//
// It is a comparison key only: nothing stores what it returns. internal/build
// orders added_at by it, internal/audit orders works by it, and the importer's
// genre regeneration
// dates a work's newest provenance by it - a leaf here, so the importer need not
// link the SQLite builder for one comparison.
func TimeKey(s string) string {
	// A value no longer than a plain date ("2026-07-29") can never be an RFC 3339
	// timestamp (the shortest is 20 bytes), so it is returned as it is without
	// the parse attempt - the result the parse would have given.
	if len(s) <= len(time.DateOnly) {
		return s
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}
