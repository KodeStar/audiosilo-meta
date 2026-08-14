// Package audit is metaaudit's business logic: a READ-ONLY, deterministic
// data-quality audit over the data/ tree.
//
// It answers one question - "what in this catalogue is wrong in a way a rule can
// see" - and answers it as a REPORT, never as a repair. The defects it looks for
// are the ones a bulk retailer import leaves behind (duplicate works minted from
// a decorated title, a series named only in a title, a series spelled two ways, a
// person forked across two spellings) and they matter downstream: a duplicate work
// splits a book's recordings across two records, so the player's `/meta` lookup
// resolves to whichever half the ASIN landed on, and a sidecar written against one
// half is invisible from the other.
//
// READ-ONLY BY CONSTRUCTION, not by care: the whole package reaches the tree
// through pkg/check's Load, which parses and validates packs and never opens a
// pack.Store. There is no write path here to disable, and a repair pass is a
// separate tool acting on this report.
//
// DETERMINISTIC BY CONSTRUCTION: every list a record carries goes through
// sortedUnique, every group is iterated in sorted key order, every class's records
// are sorted by (subclass, key, ...) before they are written, and no record
// carries a timestamp or a path outside the data root. Two runs over one tree
// produce byte-identical files, which is what makes the report reviewable in a
// pull request and diffable between runs.
//
// The detector classes are listed in finding.go; each writes one NDJSON file, and
// SUMMARY.md carries the counts and a sample of each subclass.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// Options configures a run.
type Options struct {
	// DataDir is the data root to audit. It is only ever read.
	DataDir string
	// OutDir is the report directory. It is created if absent; a class file it
	// already holds is overwritten.
	OutDir string
}

// Report is one run's outcome: the per-class records, the count-only advisories,
// and the catalogue totals SUMMARY.md opens with.
type Report struct {
	Classes []*findings
	Stats   hygieneStats
	Totals  Totals
	// LoaderProblems and LoaderWarnings are pkg/check's own counts, carried so a
	// caller can see whether the tree it audited was valid in the first place.
	LoaderProblems int
	LoaderWarnings int
}

// Totals are the catalogue's own sizes, the denominators every count is read
// against.
type Totals struct {
	Works      int
	Recordings int
	People     int
	Series     int
	Characters int
	Recaps     int
}

// Run loads the tree, runs every detector, and writes the report.
func Run(opts Options) (*Report, error) {
	res := check.Load(opts.DataDir)
	if res.Catalog == nil {
		return nil, fmt.Errorf("audit: %s could not be loaded: %s", opts.DataDir, firstProblem(res))
	}
	rep := analyze(res)
	if err := write(rep, opts.OutDir); err != nil {
		return nil, err
	}
	return rep, nil
}

// analyze is Run without the I/O, which is what the tests drive.
func analyze(res check.Result) *Report {
	cat := res.Catalog
	ix := newIndex(cat)

	// W-DUP runs first: REF-SIDECAR asks it which works ended up in a cluster.
	dup, clusters := detectWorkDup(ix)
	hyg, stats := detectHygiene(ix)

	rep := &Report{
		Stats:          stats,
		LoaderProblems: len(res.Problems),
		LoaderWarnings: len(res.Warnings),
		Totals: Totals{
			Works:      len(cat.Works),
			Recordings: stats.Recordings,
			People:     len(cat.People),
			Series:     len(cat.Series),
			Characters: len(cat.Characters),
			Recaps:     len(cat.Recaps),
		},
	}
	rep.Classes = []*findings{
		dup,
		detectWorkTitle(ix),
		detectWorkNoSeries(ix),
		detectSeriesIntegrity(ix),
		detectSeriesDup(ix),
		detectSeriesParen(ix),
		detectPersonDup(ix),
		detectRefSidecar(ix, clusters),
		hyg,
		loaderFindings(res),
	}
	return rep
}

// loaderFindings carries pkg/check's own problems and advisories into the report,
// so a reviewer sees the referential and structural anomalies the LOADER found
// beside the ones the audit did rather than having to run metacheck separately.
// They are pass-through: the audit re-judges nothing.
func loaderFindings(res check.Result) *findings {
	f := &findings{class: ClassLoader}
	for _, p := range res.Problems {
		f.add(Finding{Subclass: "problem", Key: p.Path, Have: p.Msg,
			Action: "fix before acting on any other class: the catalogue this report was built from does not validate"})
	}
	for _, p := range res.Warnings {
		f.add(Finding{Subclass: "warning", Key: p.Path, Have: p.Msg,
			Action: "advisory from pkg/check, carried through unchanged"})
	}
	return f
}

func firstProblem(res check.Result) string {
	if len(res.Problems) == 0 {
		return "no catalog returned and no problem reported"
	}
	return res.Problems[0].String()
}

// classFile is a class's NDJSON file name.
func classFile(class string) string { return class + ".ndjson" }

// write renders the report into dir: one NDJSON file per class plus SUMMARY.md.
// Every class gets a file even when it found nothing, so a consumer never has to
// tell "no findings" from "the tool did not run that detector".
func write(rep *Report, dir string) error {
	if dir == "" {
		return fmt.Errorf("audit: no output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("audit: create %s: %w", dir, err)
	}
	byClass := map[string]*findings{}
	for _, c := range rep.Classes {
		byClass[c.class] = c
	}
	for _, class := range classOrder {
		c := byClass[class]
		if c == nil {
			c = &findings{class: class}
		}
		if err := writeNDJSON(filepath.Join(dir, classFile(class)), c.sorted()); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "SUMMARY.md"), []byte(summary(rep)), 0o644)
}

// writeNDJSON writes one record per line, compactly, with no HTML escaping (a
// title carrying "&" or "<" must read as itself in the report).
func writeNDJSON(path string, rows []Finding) error {
	fh, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("audit: create %s: %w", path, err)
	}
	enc := json.NewEncoder(fh)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			_ = fh.Close()
			return fmt.Errorf("audit: write %s: %w", path, err)
		}
	}
	if err := fh.Close(); err != nil {
		return fmt.Errorf("audit: close %s: %w", path, err)
	}
	return nil
}

// total is the number of records in a class.
func (f *findings) total() int { return len(f.rows) }

// CountLines renders the per-class record counts, one line per class in report
// order, for a CLI to print. It is the only view of the classes a caller outside
// this package gets - the records themselves are the report files.
func (rep *Report) CountLines() []string {
	byClass := map[string]*findings{}
	for _, c := range rep.Classes {
		byClass[c.class] = c
	}
	out := make([]string, 0, len(classOrder))
	for _, class := range classOrder {
		n := 0
		if c := byClass[class]; c != nil {
			n = c.total()
		}
		out = append(out, fmt.Sprintf("  %-12s %7d", class, n))
	}
	return out
}
