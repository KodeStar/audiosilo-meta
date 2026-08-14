package repair

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/internal/reportdir"
)

// report.go renders a run. The DRY RUN's output is the review artifact - a maintainer
// reads it before anything is written, and a wave's pull request body is composed from
// it - so every applied change and every refusal is printed with its reason, not
// summarized away.
//
// Deterministic, like the audit it consumes: no timestamps, no paths outside the data
// root, every list already sorted by the time it gets here. Two runs over one tree
// produce byte-identical files, which is what makes a wave reviewable in a diff. The
// plumbing (the containment guard, the buffered NDJSON writer, the comma-grouped tables)
// is internal/reportdir's, shared with the audit so the two reports read as one family.

// The apply-report's file names.
const (
	appliedFile = "APPLIED.ndjson"
	refusedFile = "REFUSED.ndjson"
	summaryFile = "SUMMARY.md"
)

// writeFiles renders the apply-report into dir. An empty dir writes nothing: the summary
// still reaches the caller's writer, which is what a quick dry run wants.
func (r *Report) writeFiles(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("repair: create %s: %w", dir, err)
	}
	if err := reportdir.WriteNDJSON(filepath.Join(dir, appliedFile), len(r.Applied),
		func(i int) any { return r.Applied[i] }); err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	if err := reportdir.WriteNDJSON(filepath.Join(dir, refusedFile), len(r.Refused),
		func(i int) any { return r.Refused[i] }); err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, summaryFile), []byte(r.Summary()), 0o644)
}

// counts is the run's accounting, in the order a reader wants it: what happened, then
// what was left and why. ONE derivation, read by both output formats - the markdown
// summary and the terminal lines differ only in how they render a line.
func (r *Report) counts() []reportdir.Row {
	rows := []reportdir.Row{
		{Label: "proposals applied", N: len(r.Applied)},
		{Label: "proposals refused", N: len(r.Refused)},
		{Label: "slugs tombstoned in data/redirects.json", N: r.Redirects},
		{Label: "considered (fresh, non-advisory, in scope)", N: r.Considered},
		{Label: "left: advisory (a rule may not apply these)", N: r.Advisory},
		{Label: "left: op this pass does not apply", N: r.Unsupported},
		{Label: "left: excluded by --op/--subclass/--only", N: r.NotRequested},
		{Label: "left: beyond --limit", N: r.BeyondLimit},
	}
	if r.WorklistRecords > 0 || r.NotInWorklist > 0 {
		rows = append(rows,
			reportdir.Row{Label: "left: not in the -report worklist", N: r.NotInWorklist},
			reportdir.Row{Label: "appliable records in the worklist", N: r.WorklistRecords})
	}
	return rows
}

// group is one bucket of a grouped list: its name, how many records it holds, and where
// they start - a RANGE rather than a copy, since the slice it indexes is already in the
// report's own order.
type group struct {
	name  string
	n     int
	first int
}

// appliedByOp is the applied records bucketed by op, for the summary's headline table. It
// is derived from the records rather than from a list of ops, so the count can never
// disagree with the records that follow it.
func (r *Report) appliedByOp() []reportdir.Row {
	byOp := map[string]int{}
	for _, a := range r.Applied {
		byOp[a.Op]++
	}
	rows := make([]reportdir.Row, 0, len(byOp))
	for _, op := range rawentry.SortedKeys(byOp) {
		rows = append(rows, reportdir.Row{Label: op, N: byOp[op]})
	}
	return rows
}

// refusalGroups buckets the refusals by category, and is read by BOTH output formats - the
// markdown summary and the terminal lines differ only in how they render a line, so a
// category can never be counted one way and listed another. Refused is already sorted by
// (category, key, reason), so a bucket is a contiguous run and no second pass is needed.
func (r *Report) refusalGroups() []group {
	var out []group
	for i, ref := range r.Refused {
		if len(out) > 0 && out[len(out)-1].name == string(ref.Category) {
			out[len(out)-1].n++
			continue
		}
		out = append(out, group{name: string(ref.Category), n: 1, first: i})
	}
	return out
}

// Summary is the human-readable report, in markdown, suitable as a pull request body for
// the wave the run applies. It is what SUMMARY.md holds.
func (r *Report) Summary() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) } //nolint:errcheck // a strings.Builder never fails

	mode := "PLAN ONLY - nothing was written"
	if r.Wrote {
		mode = "APPLIED"
	}
	p("# metarepair - %s\n\n", mode)
	b.WriteString("What this is: the non-advisory proposals of a metaaudit run over `" + r.DataDir + "`, applied (or\n")
	b.WriteString("planned) one at a time. `APPLIED.ndjson` holds one record per change this run carried out,\n")
	b.WriteString("with a note for every recording, sidecar member and slug it touched; `REFUSED.ndjson` holds\n")
	b.WriteString("one record per proposal it declined, with the reason a human needs in order to decide. Both\n")
	b.WriteString("are sorted, so two runs over one tree produce byte-identical files. Every proposal came from\n")
	b.WriteString("a FRESH audit of the tree being modified: a `-report` worklist can only narrow that set.\n\n")

	b.WriteString("## Catalogue at load\n\n")
	reportdir.Table(&b, "entity", []reportdir.Row{
		{Label: "works", N: r.Totals.Works},
		{Label: "recordings", N: r.Totals.Recordings},
		{Label: "people", N: r.Totals.People},
		{Label: "series", N: r.Totals.Series},
	})
	b.WriteString("\n## This run\n\n")
	reportdir.Table(&b, "outcome", r.counts())
	b.WriteString("\n")

	if r.LoaderProblems > 0 {
		p("> **This tree does not validate**: pkg/check reported %s problem(s) before the run. A record the "+
			"loader dropped is invisible to the plan, so `--write` is refused until `go run ./cmd/metacheck` is clean.\n\n",
			reportdir.Comma(r.LoaderProblems))
	}

	if len(r.Applied) > 0 {
		b.WriteString("## Applied\n\n")
		reportdir.Table(&b, "op", r.appliedByOp())
		b.WriteString("\n")
		for _, a := range r.Applied {
			p("### %s %s\n\n", a.Op, a.Key)
			p("- class: `%s`", a.Class)
			if a.Subclass != "" {
				p(" / `%s`", a.Subclass)
			}
			p("\n")
			if a.Target != "" {
				p("- target: `%s`\n", a.Target)
			}
			if len(a.Others) > 0 {
				p("- folded in: %s\n", codeList(a.Others))
			}
			if a.Field != "" {
				p("- %s: %q -> %q\n", a.Field, a.From, a.To)
			}
			for _, n := range a.Notes {
				p("- %s\n", n)
			}
			p("\n")
		}
	}

	if len(r.Refused) > 0 {
		b.WriteString("## Refused (left for a human)\n\n")
		groups := r.refusalGroups()
		rows := make([]reportdir.Row, 0, len(groups))
		for _, g := range groups {
			rows = append(rows, reportdir.Row{Label: g.name, N: g.n})
		}
		reportdir.Table(&b, "category", rows)
		b.WriteString("\n")
		for _, g := range groups {
			p("### %s\n\n", g.name)
			for _, ref := range r.Refused[g.first : g.first+g.n] {
				p("- `%s`", ref.Key)
				if ref.Op != "" {
					p(" (%s)", ref.Op)
				}
				p(": %s\n", ref.Reason)
			}
			p("\n")
		}
	}

	if r.Wrote {
		b.WriteString("## Write\n\n")
		p("- pack files written: %s, removed: %s\n", reportdir.Comma(len(r.PacksWritten)), reportdir.Comma(len(r.PacksRemoved)))
		p("- healed by the formatting pass: %d written, %d removed, %d reformatted\n",
			len(r.HealedWrote), len(r.HealedGone), len(r.Formatted))
		p("- post-write validation: ")
		if len(r.PostProblems) == 0 {
			p("clean (`metacheck` and `metafmt --check` both pass)\n")
		} else {
			p("**%d problem(s)**\n", len(r.PostProblems))
			for _, pb := range r.PostProblems {
				p("  - %s\n", pb)
			}
		}
	}
	return b.String()
}

// Write prints the run's counts and, with verbose, every applied change and every
// refusal. It takes a writer rather than returning strings so a CLI does not loop over a
// slice to print it (metaremediate's report does the same), and it reads the SAME
// derivations Summary does - only the per-line rendering differs.
func (r *Report) Write(w io.Writer, verbose bool) error {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) } //nolint:errcheck // a strings.Builder never fails

	mode := "PLAN (nothing written)"
	if r.Wrote {
		mode = "APPLIED"
	}
	p("metarepair %s over %s\n", mode, r.DataDir)
	p("  catalogue           %s works / %s recordings / %s people / %s series\n",
		reportdir.Comma(r.Totals.Works), reportdir.Comma(r.Totals.Recordings),
		reportdir.Comma(r.Totals.People), reportdir.Comma(r.Totals.Series))
	for _, row := range r.counts() {
		p("  %-42s %s\n", row.Label, reportdir.Comma(row.N))
	}
	if r.LoaderProblems > 0 {
		p("  WARNING: the tree had %s validation problem(s) at load; --write is refused until metacheck is clean\n",
			reportdir.Comma(r.LoaderProblems))
	}

	if verbose {
		for _, a := range r.Applied {
			p("\n  %s %s\n", a.Op, a.Key)
			for _, n := range a.Notes {
				p("      %s\n", n)
			}
		}
	}
	if len(r.Refused) > 0 {
		p("\nrefusals (left for a human):\n")
		for _, g := range r.refusalGroups() {
			p("  %s (%d):\n", g.name, g.n)
			for _, ref := range r.Refused[g.first : g.first+g.n] {
				p("      %s: %s\n", ref.Key, ref.Reason)
			}
		}
	}
	if r.Wrote {
		p("\nwrote %d pack files, removed %d; post-write validation %s\n",
			len(r.PacksWritten)+len(r.HealedWrote), len(r.PacksRemoved)+len(r.HealedGone), validationWord(r.PostProblems))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func validationWord(problems []string) string {
	if len(problems) == 0 {
		return "clean"
	}
	return fmt.Sprintf("FAILED with %d problem(s)", len(problems))
}

// codeList renders a slug list as markdown code spans, sorted.
func codeList(ss []string) string {
	out := make([]string, 0, len(ss))
	for _, s := range sortStrings(ss) {
		out = append(out, "`"+s+"`")
	}
	return strings.Join(out, ", ")
}

// sortStrings is a sorted copy, for the lists this file renders. Its one caller is
// codeList: a proposal's `others` arrives in the audit's order, and a rendered list reads
// better sorted.
func sortStrings(ss []string) []string {
	out := slices.Clone(ss)
	slices.Sort(out)
	return out
}
