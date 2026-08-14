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
// pack.Store. There is no write path here to disable, the report directory is
// refused if it sits inside the data root, and a repair pass is a separate tool
// acting on the report.
//
// DETERMINISTIC BY CONSTRUCTION: every list a record carries goes through
// sortedUnique, every group is iterated in sorted key order (groupBy), every
// class's records are sorted by (subclass, key, ...) before they are written, and
// no record carries a timestamp or a path outside the data root. Two runs over one
// tree produce byte-identical files, which is what makes the report reviewable in a
// pull request and diffable between runs.
//
// WHERE THE RULES LIVE: not here. The title, series-name and person-name
// primitives are internal/titlerule, a leaf that depends on pkg/model (and, for the
// two rules that already have a home of record, internal/importer) - so metacheck,
// the intake bot and the repair pass can call them without a copy. This package
// assembles records, decides what to report, and writes a report.
//
// The detector classes are listed in finding.go; each writes one NDJSON file, and
// SUMMARY.md carries the counts and a sample of each subclass.
package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// Options configures a run.
type Options struct {
	// DataDir is the data root to audit. It is only ever read.
	DataDir string
	// OutDir is the report directory. It is created if absent; a class file it
	// already holds is overwritten. It may not sit inside DataDir.
	OutDir string
}

// Report is one run's outcome: the per-class records, the count-only advisories,
// and the catalogue totals SUMMARY.md opens with.
type Report struct {
	classes []*findings
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

// class returns one class's accumulated records, or an empty class when the name is
// not one this run produced. It is the ONE lookup - three functions had each
// rebuilt the same name-to-class map.
func (rep *Report) class(name string) *findings {
	for _, c := range rep.classes {
		if c.class == name {
			return c
		}
	}
	return &findings{class: name}
}

// Run loads the tree, runs every detector, and writes the report.
func Run(opts Options) (*Report, error) {
	if err := checkOutDir(opts); err != nil {
		return nil, err
	}
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

// checkOutDir refuses a report directory inside the data root.
//
// The audit's headline property is that it never writes to data/, and "the logic
// has no write path" is only half of that: pointing -o at data/ would have the
// report itself land in the tree, where metafmt and metacheck would then judge
// files that are not records. Enforcing it here rather than trusting the operator
// is what makes the claim hold for any caller.
func checkOutDir(opts Options) error {
	if opts.OutDir == "" {
		return fmt.Errorf("audit: no output directory")
	}
	data, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return fmt.Errorf("audit: resolve %s: %w", opts.DataDir, err)
	}
	out, err := filepath.Abs(opts.OutDir)
	if err != nil {
		return fmt.Errorf("audit: resolve %s: %w", opts.OutDir, err)
	}
	rel, err := filepath.Rel(data, out)
	if err != nil {
		return nil // on different volumes, so certainly not inside
	}
	if rel == "." || !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("audit: the report directory %s is inside the data root %s: the audit never writes to the data tree, "+
			"so point -o somewhere else", opts.OutDir, opts.DataDir)
	}
	return nil
}

// analyze is Run without the I/O, which is what the tests drive.
func analyze(res check.Result) *Report {
	cat := res.Catalog
	ix := newIndex(cat)

	// W-DUP runs first: REF-SIDECAR asks it which works ended up in a cluster, and
	// takes the inverse index it built rather than rebuilding it.
	dup, clusterWorks, clustersOf := detectWorkDup(ix)
	hyg, stats := detectHygiene(ix)
	// The two series-name detectors share one key index; computing it twice would
	// fold 45k names through model.Slugify twice over.
	skeys := seriesKeyIndex(cat.Series)

	rep := &Report{
		Stats:          stats,
		LoaderProblems: len(res.Problems),
		LoaderWarnings: len(res.Warnings),
		Totals: Totals{
			Works:      len(cat.Works),
			Recordings: stats.RecordingCount,
			People:     len(cat.People),
			Series:     len(cat.Series),
			Characters: len(cat.Characters),
			Recaps:     len(cat.Recaps),
		},
	}
	rep.classes = []*findings{
		dup,
		detectWorkTitle(ix),
		detectWorkNoSeries(ix),
		detectSeriesIntegrity(ix),
		detectSeriesDup(ix, skeys),
		detectSeriesParen(ix, skeys),
		detectPersonDup(ix),
		detectRefSidecar(ix, clusterWorks, clustersOf),
		hyg,
		loaderFindings(res),
	}
	// Sort in place and render each record's action prose from its proposal, ONCE:
	// the writer and the summary then read the same ordered slice.
	for _, c := range rep.classes {
		c.finalize()
	}
	return rep
}

// loaderFindings carries pkg/check's own problems and advisories into the report,
// so a reviewer sees the referential and structural anomalies the LOADER found
// beside the ones the audit did rather than having to run metacheck separately.
// They are pass-through: the audit re-judges nothing.
//
// An advisory's subclass is its CLASS name from pkg/check (check.AdvisoryClass), not
// one "warning" bucket - the loader already distinguishes five advisory classes and
// flattening them here would have thrown that away and made the class's counts
// useless for triage.
func loaderFindings(res check.Result) *findings {
	f := &findings{class: ClassLoader}
	for _, p := range res.Problems {
		f.add(Finding{Subclass: "problem", Key: p.Path,
			Propose: Proposal{Op: OpReview, Advisory: true, From: p.Msg,
				Reason: "fix before acting on any other class: the catalogue this report was built from does not validate"}})
	}
	for _, p := range res.Warnings {
		f.add(Finding{Subclass: check.AdvisoryClass(p), Key: p.Path,
			Propose: Proposal{Op: OpReview, Advisory: true, From: p.Msg,
				Reason: "advisory from pkg/check, carried through unchanged"}})
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("audit: create %s: %w", dir, err)
	}
	for _, class := range classOrder {
		if err := writeNDJSON(filepath.Join(dir, classFile(class)), rep.class(class).rows); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "SUMMARY.md"), []byte(summary(rep)), 0o644)
}

// writeNDJSON writes one record per line, compactly, with no HTML escaping (a
// title carrying "&" or "<" must read as itself in the report). The writer is
// buffered: a class file is tens of thousands of lines, and an unbuffered encoder
// makes a syscall per record.
func writeNDJSON(path string, rows []Finding) error {
	fh, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("audit: create %s: %w", path, err)
	}
	buf := bufio.NewWriter(fh)
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			_ = fh.Close()
			return fmt.Errorf("audit: write %s: %w", path, err)
		}
	}
	if err := buf.Flush(); err != nil {
		_ = fh.Close()
		return fmt.Errorf("audit: write %s: %w", path, err)
	}
	if err := fh.Close(); err != nil {
		return fmt.Errorf("audit: close %s: %w", path, err)
	}
	return nil
}

// Write prints the run's per-class record counts, one line per class in report
// order. It is the only view of the classes a caller outside this package gets -
// the records themselves are the report files - and it takes a writer rather than
// returning strings so a CLI does not loop over a slice to print it
// (metaremediate's report does the same).
func (rep *Report) Write(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "audited %d works / %d recordings / %d people / %d series\n",
		rep.Totals.Works, rep.Totals.Recordings, rep.Totals.People, rep.Totals.Series); err != nil {
		return err
	}
	for _, class := range classOrder {
		if _, err := fmt.Fprintf(w, "  %-12s %7d\n", class, rep.class(class).total()); err != nil {
			return err
		}
	}
	return nil
}
