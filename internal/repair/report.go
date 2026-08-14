package repair

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// report.go renders a run. The DRY RUN's output is the review artifact - a maintainer
// reads it before anything is written, and a wave's pull request body is composed from
// it - so every applied change and every refusal is printed with its reason, not
// summarized away.
//
// Deterministic, like the audit it consumes: no timestamps, no paths outside the data
// root, every list already sorted by the time it gets here. Two runs over one tree
// produce byte-identical files, which is what makes the report reviewable in a diff.

// The apply-report's file names.
const (
	appliedFile = "APPLIED.ndjson"
	refusedFile = "REFUSED.ndjson"
	summaryFile = "SUMMARY.md"
)

// writeFiles renders the apply-report into dir. An empty dir writes nothing: the
// summary still reaches the caller's writer, which is what a quick dry run wants.
func (r *Report) writeFiles(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("repair: create %s: %w", dir, err)
	}
	if err := writeNDJSON(filepath.Join(dir, appliedFile), len(r.Applied), func(i int) any { return r.Applied[i] }); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(dir, refusedFile), len(r.Refused), func(i int) any { return r.Refused[i] }); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, summaryFile), []byte(r.Summary()), 0o644)
}

// writeNDJSON writes n records, one per line, compactly and without HTML escaping (a
// title carrying "&" must read as itself). Buffered: a wave's applied list is
// thousands of lines.
func writeNDJSON(path string, n int, at func(int) any) error {
	fh, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("repair: create %s: %w", path, err)
	}
	buf := bufio.NewWriter(fh)
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	for i := range n {
		if err := enc.Encode(at(i)); err != nil {
			_ = fh.Close()
			return fmt.Errorf("repair: write %s: %w", path, err)
		}
	}
	if err := buf.Flush(); err != nil {
		_ = fh.Close()
		return fmt.Errorf("repair: write %s: %w", path, err)
	}
	if err := fh.Close(); err != nil {
		return fmt.Errorf("repair: close %s: %w", path, err)
	}
	return nil
}

// Summary is the human-readable report, in markdown, suitable as a pull request body
// for the wave the run applies. It is what SUMMARY.md holds.
func (r *Report) Summary() string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) } //nolint:errcheck // a strings.Builder never fails

	mode := "plan only (nothing written)"
	if r.Wrote {
		mode = "applied"
	}
	p("# metarepair - %s\n\n", mode)
	p("Catalogue at load: %d works / %d recordings / %d people / %d series.\n\n",
		r.Totals.Works, r.Totals.Recordings, r.Totals.People, r.Totals.Series)

	p("| | |\n|---|---:|\n")
	p("| proposals applied | %d |\n", len(r.Applied))
	p("| proposals refused | %d |\n", len(r.Refused))
	p("| slugs tombstoned in data/redirects.json | %d |\n", r.Redirects)
	p("| considered (fresh, non-advisory, in scope) | %d |\n", r.Considered)
	p("| left: advisory (a rule may not apply these) | %d |\n", r.Advisory)
	p("| left: op this pass does not apply | %d |\n", r.Unsupported)
	p("| left: excluded by --op/--subclass/--only | %d |\n", r.NotRequested)
	p("| left: beyond --limit | %d |\n", r.BeyondLimit)
	if r.WorklistRecords > 0 || r.NotInWorklist > 0 {
		p("| left: not in the -report worklist | %d |\n", r.NotInWorklist)
		p("| appliable records in the worklist | %d |\n", r.WorklistRecords)
	}
	p("\n")

	if r.LoaderProblems > 0 {
		p("> **This tree does not validate**: pkg/check reported %d problem(s) before the run. A record the loader dropped is "+
			"invisible to the plan, so --write is refused until `go run ./cmd/metacheck` is clean.\n\n", r.LoaderProblems)
	}

	if len(r.Applied) > 0 {
		p("## Applied\n\n")
		byOp := map[string]int{}
		for _, a := range r.Applied {
			byOp[a.Op]++
		}
		for _, op := range sortedKeys(byOp) {
			p("- **%s**: %d\n", op, byOp[op])
		}
		p("\n")
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
		p("## Refused (left for a human)\n\n")
		byCat := map[string]int{}
		for _, ref := range r.Refused {
			byCat[ref.Category]++
		}
		for _, cat := range sortedKeys(byCat) {
			p("- **%s**: %d\n", cat, byCat[cat])
		}
		p("\n")
		cat := ""
		for _, ref := range r.Refused {
			if ref.Category != cat {
				cat = ref.Category
				p("### %s\n\n", cat)
			}
			p("- `%s`", ref.Key)
			if ref.Op != "" {
				p(" (%s)", ref.Op)
			}
			p(": %s\n", ref.Reason)
		}
		p("\n")
	}

	if r.Wrote {
		p("## Write\n\n")
		p("- pack files written: %d, removed: %d\n", len(r.PacksWritten), len(r.PacksRemoved))
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
// refusal. It takes a writer rather than returning strings so a CLI does not loop
// over a slice to print it (metaremediate's report does the same).
func (r *Report) Write(w io.Writer, verbose bool) error {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) } //nolint:errcheck // a strings.Builder never fails

	mode := "PLAN (nothing written)"
	if r.Wrote {
		mode = "APPLIED"
	}
	p("metarepair %s over %s\n", mode, r.DataDir)
	p("  catalogue           %d works / %d recordings / %d people / %d series\n",
		r.Totals.Works, r.Totals.Recordings, r.Totals.People, r.Totals.Series)
	p("  considered          %d\n", r.Considered)
	p("  applied             %d\n", len(r.Applied))
	p("  refused             %d\n", len(r.Refused))
	p("  slugs tombstoned    %d\n", r.Redirects)
	p("  left alone          %d advisory, %d op not applied here, %d filtered out, %d beyond --limit",
		r.Advisory, r.Unsupported, r.NotRequested, r.BeyondLimit)
	if r.WorklistRecords > 0 || r.NotInWorklist > 0 {
		p(", %d not in the worklist", r.NotInWorklist)
	}
	p("\n")
	if r.LoaderProblems > 0 {
		p("  WARNING             the tree had %d validation problem(s) at load; --write is refused until metacheck is clean\n",
			r.LoaderProblems)
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
		counts := map[string]int{}
		for _, ref := range r.Refused {
			counts[ref.Category]++
		}
		p("\nrefusals (left for a human):\n")
		cat := ""
		for _, ref := range r.Refused {
			if ref.Category != cat {
				cat = ref.Category
				p("  %s (%d):\n", cat, counts[cat])
			}
			p("      %s: %s\n", ref.Key, ref.Reason)
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

// codeList renders a slug list as markdown code spans.
func codeList(ss []string) string {
	out := make([]string, 0, len(ss))
	for _, s := range sortStrings(ss) {
		out = append(out, "`"+s+"`")
	}
	return strings.Join(out, ", ")
}
