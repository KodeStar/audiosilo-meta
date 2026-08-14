package model

import (
	"strconv"
	"strings"
)

// position.go holds the ONE reading of a series position's numeric span.
//
// It lives in pkg/model - the leaf every other package can reach - beside the
// schema's own position rules, because two packages that may not import each
// other both need it: internal/serve orders a series listing by it and compares a
// user's stated volume against it, and internal/audit judges range bounds and
// slot identity with it. internal/serve's own doc comment nominated this home for
// exactly that case, and the audit is the outside consumer it was waiting for.
//
// What it deliberately does NOT do is decide whether a position is WELL-FORMED.
// ParseFloat is liberal - it accepts "1e2", "+2", "Inf" and " 2 " - and the
// grammar the data model allows is narrower (a number, or two joined by a hyphen;
// see series.schema.json and internal/importer.NormalizeSequence, the rule of
// record for acceptance and canonical spelling). A caller that cares about
// well-formedness normalizes FIRST and reads the span second; a caller that only
// needs a sort key or a span comparison can read it directly, which is what
// internal/serve does with values a validated tree already vouched for.

// ParsePositionRange parses a series position string into its numeric span:
// "2" -> (2, 2), "2.5" -> (2.5, 2.5), "1-3.5" -> (1, 3.5). ok is false when
// either bound fails to parse.
//
// Callers apply their own policy to the span (sort key, integer coverage,
// equality, range validity). See the package note above on why this is a span
// reader and not a validator.
func ParsePositionRange(pos string) (lo, hi float64, ok bool) {
	pos = strings.TrimSpace(pos)
	if i := strings.IndexByte(pos, '-'); i > 0 {
		var err1, err2 error
		lo, err1 = strconv.ParseFloat(strings.TrimSpace(pos[:i]), 64)
		hi, err2 = strconv.ParseFloat(strings.TrimSpace(pos[i+1:]), 64)
		return lo, hi, err1 == nil && err2 == nil
	}
	f, err := strconv.ParseFloat(pos, 64)
	return f, f, err == nil
}
