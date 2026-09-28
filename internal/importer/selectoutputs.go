package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/atomicfile"
)

// selectoutputs.go holds what libex-select needs around its outputs beyond the
// staging itself (internal/atomicfile): the refusal worklist's two naming rules,
// and the guard that no output lands on the input export or on another output.

// sameFile reports whether an output path may name the input export's file,
// erring toward yes: the same spelling or absolute path, a symlink or hard link
// to it (os.SameFile, asked when both exist), or - for an output not written yet
// - the same file once written: the parent directories resolved through their
// symlinks (a parent that does not exist yet through its nearest existing
// ancestor, see resolvedLocation) and the base names compared CASE-INSENSITIVELY,
// because the default filesystems of macOS (APFS) and Windows (NTFS) are. On a
// case-sensitive filesystem that refuses a pair that would have been two files,
// which costs a rename; the other error costs the operator's export.
func sameFile(a, b string) bool {
	if samePath(a, b) {
		return true
	}
	aDir, aBase, aOK := resolvedLocation(a)
	bDir, bBase, bOK := resolvedLocation(b)
	return aOK && bOK && strings.EqualFold(aDir, bDir) && strings.EqualFold(aBase, bBase)
}

// samePath is the plain half of sameFile: one spelling, one absolute path, or
// one existing file (os.SameFile). It is what keeps two OUTPUTS apart - each is
// a file this run writes, so nothing is destroyed that the run did not make.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr == nil && bErr == nil && aAbs == bAbs {
		return true
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

// resolvedLocation is where a path's file is or would be written: its parent
// directory, absolute and resolved through every symlink, and its base name. A
// parent that does not exist yet (atomicfile.Stage creates it) is resolved
// through its NEAREST EXISTING ancestor, with the missing components appended as
// spelled, so "out/s.ndjson" and "$PWD/out/s.ndjson" are one location before
// out/ exists.
func resolvedLocation(path string) (dir, base string, ok bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", false
	}
	dir, rest := filepath.Dir(abs), ""
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(resolved, rest), filepath.Base(abs), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// refuseOverlappingOutputs fails when an output would land on the input export
// (sameFile - the mistake it guards is destructive: `-o subset.ndjson
// full.ndjson` under a naive argument split once read subset.ndjson as the input
// and truncated the operator's multi-GB dump to nothing, reporting success) or
// two outputs on one file (samePath): the second write would replace the first,
// and a subset with its rows swapped for worklist lines is still a file the
// import reads. An empty worklist path is an output nobody asked for.
func refuseOverlappingOutputs(exportPath, outPath, refusalsPath, attachmentsPath string) error {
	switch {
	case sameFile(exportPath, outPath) && outPath == exportPath:
		return fmt.Errorf("refusing to write the subset over the input export: -o names the same file (%s)", exportPath)
	case sameFile(exportPath, outPath):
		return fmt.Errorf("refusing to write the subset over the input export: -o %s and %s are the same file", outPath, exportPath)
	}
	outputs := []struct{ flag, path string }{{"-o", outPath}, {"--refusals", refusalsPath}, {"--attachments", attachmentsPath}}
	for i, o := range outputs {
		if o.path == "" {
			continue
		}
		if i > 0 && sameFile(exportPath, o.path) {
			return fmt.Errorf("refusing to write %s over the input export: %s and %s are the same file", o.flag, o.path, exportPath)
		}
		for _, other := range outputs[:i] {
			if other.path != "" && samePath(other.path, o.path) {
				return fmt.Errorf("refusing to write %s and %s to one file: %s and %s are the same file", other.flag, o.flag, other.path, o.path)
			}
		}
	}
	return nil
}

// refusalLog is the --refusals worklist: one RowSkip line per refused row. A nil
// log is "not asked for", and every method is a no-op on it.
//
// Three rules keep every line one a reader can act on. There is AT MOST ONE
// line per ASIN, and the first real rule wins: a repeated row of an ASIN the
// worklist already names (the catalogue's ASIN twice, one malformed value twice,
// a copy the batch re-check dropped after its duplicate was refused) writes
// nothing more. A line never names an ASIN the subset carries: the only refusal
// that can share an ASIN with a selected row is duplicate-asin whose FIRST copy
// was kept at stream time, so only those are held (bounded by the kept rows) and
// written at the end (flush), minus every ASIN the subset carries or a line
// already names. And a line never carries an empty ASIN: a row stating none
// cannot be named, so it is counted (unnamed, printed by the report) instead.
type refusalLog struct {
	f       *atomicfile.File
	written map[string]bool
	held    []string
	unnamed int
}

// add writes one refused row under its rule, unless its ASIN has a line.
func (l *refusalLog) add(asin string, r refusal) {
	if l == nil {
		return
	}
	if asin == "" {
		l.unnamed++
		return
	}
	if l.written[asin] {
		return
	}
	if l.written == nil {
		l.written = map[string]bool{}
	}
	l.written[asin] = true
	l.f.Encode(RowSkip{ASIN: asin, Reason: r.code})
}

// hold keeps a duplicate-asin line for flush, unless the ASIN already has one.
func (l *refusalLog) hold(asin string) {
	if l != nil && !l.written[asin] {
		l.held = append(l.held, asin)
	}
}

// flush writes the held duplicate-asin lines whose ASIN the subset does not
// carry, and returns how many refused rows the worklist could not name.
func (l *refusalLog) flush(selected []selectedRow) int {
	if l == nil {
		return 0
	}
	kept := make(map[string]bool, len(selected))
	for _, r := range selected {
		kept[r.asin] = true
	}
	for _, asin := range l.held {
		if !kept[asin] {
			l.add(asin, reasonDuplicateASIN)
		}
	}
	return l.unnamed
}
