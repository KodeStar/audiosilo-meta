package audit

import (
	"fmt"
	"strconv"
	"strings"
)

// opPhrase is what each op DOES, in words, as a sentence opener. The prose a report
// shows is composed from it plus the proposal's own fields, so a detector states its
// intent once (typed) and the wording exists once (here) - rather than every
// detector carrying a hand-written sentence that could describe something other than
// what it proposed.
var opPhrase = map[string]func(Proposal) string{
	OpMergeWorks: func(p Proposal) string {
		return "review as one work: fold " + truncateList(p.Others, 8) + " onto " + p.Target
	},
	OpMergeSeries: func(p Proposal) string {
		return "review as one series: fold " + truncateList(p.Others, 8) + " onto " + p.Target
	},
	OpRetitle: func(p Proposal) string {
		return fmt.Sprintf("retitle %s: %s -> %s", p.Target, quoteOrDash(p.From), quoteOrDash(p.To))
	},
	OpAddSeriesMember: func(p Proposal) string {
		if p.To == "" {
			return fmt.Sprintf("add %s to series %s (position unknown)", p.Target, p.Series)
		}
		return fmt.Sprintf("add %s to series %s at position %s", p.Target, p.Series, p.To)
	},
	OpRestatePosition: func(p Proposal) string {
		s := "restate the position"
		if p.Target != "" {
			s += " of " + p.Target
		}
		if p.Series != "" {
			s += " in " + p.Series
		}
		if p.To != "" {
			return s + fmt.Sprintf(": %s -> %s", quoteOrDash(p.From), quoteOrDash(p.To))
		}
		return s + ": " + quoteOrDash(p.From)
	},
	OpDropMembership: func(p Proposal) string {
		return fmt.Sprintf("drop the membership naming %s from series %s", quoteOrDash(p.From), p.Series)
	},
	OpFillField: func(p Proposal) string {
		return fmt.Sprintf("state %s on %s", p.Field, p.Target)
	},
	OpRenameCandidate: func(p Proposal) string {
		s := "candidate for a rename pass: " + p.Target
		if p.To != "" {
			s += " -> " + p.To
		}
		return s
	},
	OpRepointSidecar: func(p Proposal) string {
		return "re-point the works-community sidecar keyed by " + p.Target
	},
	OpReview: func(Proposal) string { return "review by hand" },
}

// renderAction is the proposal in words. It is the ONE place a Finding's Action
// string is produced (findings.finalize calls it), so the prose and the typed
// proposal cannot disagree.
func renderAction(p Proposal) string {
	if p.Op == OpNone {
		return p.Reason
	}
	phrase, ok := opPhrase[p.Op]
	if !ok {
		// A new op with no phrase: say what it is rather than nothing, so the gap
		// is visible in the report instead of silently rendering blank.
		phrase = func(q Proposal) string { return q.Op }
	}
	s := phrase(p)
	if p.Advisory && p.Op != OpReview {
		s = "do NOT apply mechanically - " + s
	}
	if p.Reason != "" {
		s += " (" + p.Reason + ")"
	}
	return s
}

// classDoc is the one-line description SUMMARY.md prints under each class
// heading. It lives beside the renderer rather than in the detectors so the
// report reads as one document.
var classDoc = map[string]string{
	ClassWorkDup: "near-duplicate work clusters: one book stored as two or more works, usually because a retailer's decorated title minted a second identity. " +
		"Grouped by the cleaned title plus the project's own work-identity rule, so a fork whose author list gained a role-credited contributor still meets its twin. " +
		"Works in incompatible languages are never clustered together.",
	ClassWorkTitle:    "titles still carrying retailer decoration (edition markers, volume markers, an embedded series name, a genre subtitle). Title-only: no slug is ever proposed for change.",
	ClassWorkNoSeries: "works belonging to no series whose title names one, states a volume number, or both.",
	ClassSeriesInteg:  "per-series problems: shared, malformed or non-canonically spelled positions, dangling members, sequence gaps (advisory), a minority-language member, an omnibus sitting on a single slot.",
	ClassSeriesDup:    "series whose names are one name spelled two ways.",
	ClassSeriesParen:  "series names carrying a parenthetical. Reported, never merged: a parenthetical is often a deliberate alternative ordering the data model cannot otherwise express.",
	ClassPersonDup:    "possible duplicate people. ADVISORY throughout, high false-positive rate: two real people can share a name or sit one typo apart, so nothing here proposes an action.",
	ClassRefSidecar:   "works-community sidecar hazards: a spoiler-gated sidecar attached to a work that turns out to be one of a duplicate pair, or keyed by a work slug nothing holds.",
	ClassHygiene:      "field-level gaps and slug-convention oddities.",
	ClassLoader:       "pkg/check's own problems and advisories over the same load, carried through unchanged and filed under the loader's own advisory class names.",
}

// sampleCount is how many example records SUMMARY.md prints per subclass. Small
// on purpose: the NDJSON file is the data, the summary is the orientation.
const sampleCount = 3

// row is one line of a summary table: a label and a count.
type row struct {
	label string
	n     int
}

// summary renders SUMMARY.md. It carries NO timestamp and no absolute path, so
// two runs over one tree produce identical bytes.
func summary(rep *Report) string {
	var b strings.Builder
	b.WriteString("# metaaudit report\n\n")
	b.WriteString("A deterministic, read-only data-quality audit of the `data/` tree. Every class below has a\n")
	b.WriteString("matching `<CLASS>.ndjson` file in this directory, one JSON record per line, sorted so two\n")
	b.WriteString("runs over the same tree produce byte-identical output. Nothing here has been applied to the\n")
	b.WriteString("data: a record's `propose` is a typed repair for a later pass, and its `action` is that\n")
	b.WriteString("same proposal rendered in words - neither is a change that was made.\n\n")

	b.WriteString("## Catalogue\n\n")
	writeTable(&b, "entity", []row{
		{"works", rep.Totals.Works},
		{"recordings", rep.Totals.Recordings},
		{"people", rep.Totals.People},
		{"series", rep.Totals.Series},
		{"characters sidecars", rep.Totals.Characters},
		{"recaps sidecars", rep.Totals.Recaps},
	})
	fmt.Fprintf(&b, "\nLoader (`pkg/check`): %s, %s.\n\n",
		joinCount(rep.LoaderProblems, "problem"), joinCount(rep.LoaderWarnings, "warning"))

	b.WriteString("## Findings per class\n\n")
	classRows := make([]row, 0, len(classOrder))
	for _, class := range classOrder {
		classRows = append(classRows, row{class, rep.class(class).total()})
	}
	writeTable(&b, "class", classRows)
	b.WriteString("\n")

	for _, class := range classOrder {
		c := rep.class(class)
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", class, classDoc[class])
		if c.total() == 0 {
			b.WriteString("No findings.\n\n")
			continue
		}
		subRows := make([]row, 0, 8)
		for _, sc := range c.counts() {
			name := sc.Subclass
			if name == "" {
				name = "(none)"
			}
			subRows = append(subRows, row{name, sc.Count})
		}
		writeTable(&b, "subclass", subRows)
		b.WriteString("\n")
		writeSamples(&b, c)
	}

	writeCountOnly(&b, rep)
	return b.String()
}

// writeTable renders a two-column markdown table with a right-aligned count.
func writeTable(b *strings.Builder, head string, rows []row) {
	fmt.Fprintf(b, "| %s | count |\n|---|---:|\n", head)
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %s |\n", r.label, comma(r.n))
	}
}

// writeSamples prints the first records of each subclass, in the file's own
// order, so the summary and the NDJSON agree about what "first" means.
func writeSamples(b *strings.Builder, c *findings) {
	seen := map[string]int{}
	var lines []string
	for _, r := range c.rows {
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
	parts := []string{"`" + r.Key + "`"}
	p := r.Propose
	switch {
	case p.Field != "" && p.From == "" && p.To == "":
		parts = append(parts, p.Field+" missing")
	case p.Field != "" && p.To == "":
		// A class that flags a value without proposing a replacement - every slug
		// rule, since a slug is identity and a rename is not a mechanical change.
		parts = append(parts, p.Field+": "+quoteOrDash(p.From))
	case p.Field != "":
		parts = append(parts, p.Field+": "+quoteOrDash(p.From)+" -> "+quoteOrDash(p.To))
	case len(r.Works) > 1:
		parts = append(parts, truncateList(refIDs(r.Works), 4))
	case len(r.People) > 1:
		parts = append(parts, truncateList(quotedNames(r.People), 4))
	case len(r.Series) > 1:
		parts = append(parts, truncateList(seriesNames(r.Series), 4))
	case p.From != "":
		parts = append(parts, oneLine(p.From))
	}
	if p.Op != OpNone && p.Op != OpReview {
		parts = append(parts, "`"+p.Op+"`")
	}
	if p.Target != "" && p.Op == OpMergeWorks || p.Op == OpMergeSeries {
		parts = append(parts, "keep `"+p.Target+"`")
	}
	return strings.Join(parts, " - ")
}

func refIDs(ws []WorkRef) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.ID)
	}
	return out
}

func quotedNames(ps []PersonRef) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, `"`+p.Name+`"`)
	}
	return out
}

func seriesNames(ss []SeriesRef) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, `"`+s.Name+`"`)
	}
	return out
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
	writeTable(b, "measure", []row{
		{"works with `added_at` as a date", st.Works.Date},
		{"works with `added_at` as an RFC 3339 timestamp", st.Works.Stamp},
		{"works with no `added_at`", st.Works.None},
		{"recordings with `added_at` as a date", st.Recordings.Date},
		{"recordings with `added_at` as an RFC 3339 timestamp", st.Recordings.Stamp},
		{"recordings with no `added_at`", st.Recordings.None},
		{"recordings with no runtime", st.RecordingsNoRun},
		{"chapters across all recordings", st.Chapters},
	})
	b.WriteString("\n")
}

// comma renders a non-negative integer with thousands separators, so a six-figure
// count is readable in a table. Every count it is given is a length or a tally.
func comma(n int) string {
	s := strconv.Itoa(n)
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
