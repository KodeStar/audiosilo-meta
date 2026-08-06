package remediate

import (
	"fmt"
	"io"
	"strings"
)

// report.go renders a run. The dry run's output IS the review artifact - a
// maintainer reads it before anything is written - so every planned action and
// every refusal is printed, not summarized away.

// Write renders the report to w. verbose adds every merge and every series
// change; without it the per-book detail collapses to the counts, but the
// refusals and the missing plain editions are never collapsed - they are the
// two things somebody has to act on.
func (r *Report) Write(w io.Writer, verbose bool) error {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) } //nolint:errcheck // a strings.Builder never fails

	mode := "PLAN (nothing written)"
	if r.Applied {
		mode = "APPLIED"
	}
	p("metaremediate %s\n", mode)
	p("  part works found        %d in %d part groups\n", r.PartWorks, r.Groups)
	p("  complete-set rows read  %d (%d matched a group, %d row problems)\n",
		r.CompleteSets, r.MatchedSets, len(r.SetProblems))

	minted, enriched := 0, 0
	for _, m := range r.Merged {
		if m.Minted {
			minted++
		} else {
			enriched++
		}
	}
	p("  merged works            %d (%d minted, %d existing complete-set works enriched)\n",
		len(r.Merged), minted, enriched)
	p("  works deleted           %d\n", len(r.Deleted))
	p("  same-ASIN twins merged  %d\n", len(r.Twins))
	p("  series repaired         %d (%d slots given to a plain edition)\n", len(r.Series), r.SwappedToPlain)
	p("  books still lacking a plain edition  %d\n", len(r.MissingPlain))
	p("  refusals                %d\n", len(r.Refusals))

	if verbose {
		if len(r.Merged) > 0 {
			p("\nmerges:\n")
			for _, m := range r.Merged {
				kind := "enrich"
				if m.Minted {
					kind = "mint  "
				}
				runtime := "runtime omitted"
				if m.Runtime > 0 {
					runtime = fmt.Sprintf("%d min", m.Runtime)
				}
				p("  %s %s  %q  <- %s  (%s)\n", kind, m.Slug, m.Title, strings.Join(m.Parts, ", "), runtime)
			}
		}
		if len(r.Twins) > 0 {
			p("\nsame-ASIN twins:\n")
			for _, t := range r.Twins {
				p("  %s <- %s (shared ASIN %s)\n", t.Survivor, strings.Join(t.Absorbed, ", "), t.ASIN)
			}
		}
		if len(r.Series) > 0 {
			p("\nseries repairs:\n")
			for _, s := range r.Series {
				p("  %s\n", s.Slug)
				for _, c := range s.Changes {
					p("      %s\n", c)
				}
			}
		}
		if len(r.SetProblems) > 0 {
			p("\ncomplete-set rows declined or trimmed:\n")
			for _, rp := range r.SetProblems {
				p("  line %d (%s): %s\n", rp.Line, rp.ASIN, rp.Reason)
			}
		}
	}

	if len(r.MissingPlain) > 0 {
		// The identifiers are the point of this list: they are what an operator
		// matches a dump row by when importing the plain editions.
		p("\nbooks with no plain edition in the catalogue (import these, then re-run):\n")
		for _, m := range r.MissingPlain {
			p("  %q\n", m.Base)
			p("      authors=%s  series=%s\n", strings.Join(m.Authors, ", "), strings.Join(m.Series, ", "))
			p("      merged into %s (%q); asins=%s\n", m.Work, m.Title, strings.Join(m.ASINs, ", "))
		}
	}

	if len(r.Refusals) > 0 {
		// Refusals arrive sorted by category, so one pass emits a header
		// whenever the category changes. The counts are worth the one cheap
		// pre-pass: how MANY books a category covers is the first thing a
		// maintainer reads off this list.
		counts := map[string]int{}
		for _, ref := range r.Refusals {
			counts[ref.Category]++
		}
		p("\nrefusals (left for a human):\n")
		category := ""
		for _, ref := range r.Refusals {
			if ref.Category != category {
				category = ref.Category
				p("  %s (%d):\n", category, counts[category])
			}
			p("      %s: %s", ref.Subject, ref.Reason)
			if len(ref.Entries) > 0 {
				p(" [%s]", strings.Join(ref.Entries, ", "))
			}
			p("\n")
		}
	}

	if r.Applied {
		p("\nwrote %d pack files, removed %d\n", len(r.Wrote), len(r.RemovedPacks))
	}

	_, err := io.WriteString(w, b.String())
	return err
}
