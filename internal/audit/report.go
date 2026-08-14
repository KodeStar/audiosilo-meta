package audit

import (
	"fmt"
	"strconv"
	"strings"
)

// classDoc is the one-line description SUMMARY.md prints under each class
// heading. It lives beside the renderer rather than in the detectors so the
// report reads as one document.
var classDoc = map[string]string{
	ClassWorkDup: "near-duplicate work clusters: one book stored as two or more works, usually because a retailer's decorated title minted a second identity. " +
		"Works in different languages are never clustered together.",
	ClassWorkTitle:    "titles still carrying retailer decoration (edition markers, volume markers, an embedded series name, a genre subtitle). Title-only: no slug is ever proposed for change.",
	ClassWorkNoSeries: "works belonging to no series whose title names one, states a volume number, or both.",
	ClassSeriesInteg:  "per-series problems: shared or malformed positions, dangling members, sequence gaps (advisory), a minority-language member, an omnibus sitting on a single slot.",
	ClassSeriesDup:    "series whose names are one name spelled two ways.",
	ClassSeriesParen:  "series names carrying a parenthetical. Reported, never merged: a parenthetical is often a deliberate alternative ordering the data model cannot otherwise express.",
	ClassPersonDup:    "possible duplicate people. ADVISORY throughout, high false-positive rate: two real people can share a name or sit one typo apart, so nothing here proposes an action.",
	ClassRefSidecar:   "works-community sidecar hazards: a spoiler-gated sidecar attached to a work that turns out to be one of a duplicate pair, or keyed by a work slug nothing holds.",
	ClassHygiene:      "field-level gaps and slug-convention oddities.",
	ClassLoader:       "pkg/check's own problems and advisories over the same load, carried through unchanged.",
}

// sampleCount is how many example records SUMMARY.md prints per subclass. Small
// on purpose: the NDJSON file is the data, the summary is the orientation.
const sampleCount = 3

// summary renders SUMMARY.md. It carries NO timestamp and no absolute path, so
// two runs over one tree produce identical bytes.
func summary(rep *Report) string {
	var b strings.Builder
	b.WriteString("# metaaudit report\n\n")
	b.WriteString("A deterministic, read-only data-quality audit of the `data/` tree. Every class below has a\n")
	b.WriteString("matching `<CLASS>.ndjson` file in this directory, one JSON record per line, sorted so two\n")
	b.WriteString("runs over the same tree produce byte-identical output. Nothing here has been applied to the\n")
	b.WriteString("data: a record's `action` is a proposal for a repair pass, not a change that was made.\n\n")

	b.WriteString("## Catalogue\n\n")
	b.WriteString("| entity | count |\n|---|---:|\n")
	for _, row := range [][2]any{
		{"works", rep.Totals.Works},
		{"recordings", rep.Totals.Recordings},
		{"people", rep.Totals.People},
		{"series", rep.Totals.Series},
		{"characters sidecars", rep.Totals.Characters},
		{"recaps sidecars", rep.Totals.Recaps},
	} {
		fmt.Fprintf(&b, "| %s | %s |\n", row[0], comma(row[1].(int)))
	}
	fmt.Fprintf(&b, "\nLoader (`pkg/check`): %s, %s.\n\n",
		joinCount(rep.LoaderProblems, "problem"), joinCount(rep.LoaderWarnings, "warning"))

	b.WriteString("## Findings per class\n\n")
	b.WriteString("| class | records |\n|---|---:|\n")
	byClass := map[string]*findings{}
	for _, c := range rep.Classes {
		byClass[c.class] = c
	}
	for _, class := range classOrder {
		n := 0
		if c := byClass[class]; c != nil {
			n = c.total()
		}
		fmt.Fprintf(&b, "| %s | %s |\n", class, comma(n))
	}
	b.WriteString("\n")

	for _, class := range classOrder {
		c := byClass[class]
		if c == nil {
			c = &findings{class: class}
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", class, classDoc[class])
		if c.total() == 0 {
			b.WriteString("No findings.\n\n")
			continue
		}
		b.WriteString("| subclass | records |\n|---|---:|\n")
		for _, sc := range c.counts() {
			name := sc.Subclass
			if name == "" {
				name = "(none)"
			}
			fmt.Fprintf(&b, "| %s | %s |\n", name, comma(sc.Count))
		}
		b.WriteString("\n")
		writeSamples(&b, c)
	}

	writeCountOnly(&b, rep)
	return b.String()
}

// writeSamples prints the first records of each subclass, in the file's own
// order, so the summary and the NDJSON agree about what "first" means.
func writeSamples(b *strings.Builder, c *findings) {
	rows := c.sorted()
	seen := map[string]int{}
	var lines []string
	for _, r := range rows {
		if seen[r.Subclass] >= sampleCount {
			continue
		}
		seen[r.Subclass]++
		lines = append(lines, "- `"+r.Subclass+"` "+sampleLine(r))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("Examples:\n\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
}

// sampleLine renders one record as a single readable line.
func sampleLine(r Finding) string {
	var parts []string
	parts = append(parts, "`"+r.Key+"`")
	switch {
	case r.Field != "" && r.Have == "" && r.Want == "":
		parts = append(parts, r.Field+" missing")
	case r.Field != "" && r.Want == "":
		// A class that flags a value without proposing a replacement - every slug
		// rule, since a slug is identity and a rename is not a mechanical change.
		parts = append(parts, r.Field+": "+quoteOrDash(r.Have))
	case r.Field != "":
		parts = append(parts, r.Field+": "+quoteOrDash(r.Have)+" -> "+quoteOrDash(r.Want))
	case len(r.Works) > 1:
		ids := make([]string, 0, len(r.Works))
		for _, w := range r.Works {
			ids = append(ids, w.ID)
		}
		parts = append(parts, truncateList(ids, 4))
	case len(r.People) > 1:
		names := make([]string, 0, len(r.People))
		for _, p := range r.People {
			names = append(names, `"`+p.Name+`"`)
		}
		parts = append(parts, truncateList(names, 4))
	case len(r.Series) > 1:
		names := make([]string, 0, len(r.Series))
		for _, s := range r.Series {
			names = append(names, `"`+s.Name+`"`)
		}
		parts = append(parts, truncateList(names, 4))
	case r.Have != "":
		parts = append(parts, oneLine(r.Have))
	}
	if r.Canonical != "" {
		parts = append(parts, "canonical `"+r.Canonical+"`")
	}
	return strings.Join(parts, " - ")
}

func quoteOrDash(s string) string {
	if s == "" {
		return "(unset)"
	}
	return `"` + oneLine(s) + `"`
}

// oneLine keeps a value from breaking the markdown list it sits in.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// writeCountOnly prints the advisories that are counts rather than records: the
// added_at spelling split (documented and expected) and the coverage numbers that
// put the other classes in proportion.
func writeCountOnly(b *strings.Builder, rep *Report) {
	st := rep.Stats
	b.WriteString("## Count-only advisories\n\n")
	b.WriteString("These are not defects to fix one by one, so they have no NDJSON records - the numbers are\n")
	b.WriteString("the whole finding. The `added_at` split is documented and expected: a plain `YYYY-MM-DD`\n")
	b.WriteString("date is what the importer and the intake bot stamp at creation, and a full RFC 3339\n")
	b.WriteString("timestamp is what the storage migration's one-time git-history backfill wrote.\n\n")
	b.WriteString("| measure | count |\n|---|---:|\n")
	for _, row := range [][2]any{
		{"works with `added_at` as a date", st.WorkAddedDate},
		{"works with `added_at` as an RFC 3339 timestamp", st.WorkAddedStamp},
		{"works with no `added_at`", st.WorkAddedNone},
		{"recordings with `added_at` as a date", st.RecAddedDate},
		{"recordings with `added_at` as an RFC 3339 timestamp", st.RecAddedStamp},
		{"recordings with no `added_at`", st.RecAddedNone},
		{"recordings with no runtime", st.RecordingsNoRun},
		{"chapters across all recordings", st.Chapters},
	} {
		fmt.Fprintf(b, "| %s | %s |\n", row[0], comma(row[1].(int)))
	}
	b.WriteString("\n")
}

// comma renders an integer with thousands separators, so a six-figure count is
// readable in a table.
func comma(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
