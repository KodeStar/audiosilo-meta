package importer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// selectoutputs.go is how libex-select writes what it writes: the subset (-o)
// and the two machine-readable worklists (--refusals, --attachments). All three
// are STAGED - written into a temp file beside the destination - and renamed into
// place only once every one of them was written (commitAll). An interrupted or
// failed run leaves none of them: a truncated NDJSON subset is still a perfectly
// importable file and would land as a silently half-sized tranche, and a
// half-written worklist reads as a complete one.

// selectOutput is one output path and the flag that named it, for the overlap
// refusals' messages.
type selectOutput struct{ flag, path string }

// sameFile reports whether two paths name one file: the same spelling, the same
// absolute path, or - since distinct names can still be one file - a symlink or
// hard link to it (os.SameFile, asked only when both exist).
func sameFile(a, b string) bool {
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

// refuseSelfOverwrite fails when the subset would land on the input export.
// The subset is always a strict reduction of its input, so this can only ever
// be a mistake - and the mistake it guards is destructive: `-o subset.ndjson
// full.ndjson` under a naive argument split once read subset.ndjson as the
// input and truncated the operator's multi-GB dump to nothing, reporting
// success.
func refuseSelfOverwrite(exportPath, outPath string) error {
	switch {
	case !sameFile(exportPath, outPath):
		return nil
	case outPath == exportPath:
		return fmt.Errorf("refusing to write the subset over the input export: -o names the same file (%s)", exportPath)
	default:
		return fmt.Errorf("refusing to write the subset over the input export: -o %s and %s are the same file", outPath, exportPath)
	}
}

// refuseOverlappingOutputs fails when any output would land on the input
// export or on another output: the second write would replace the first, and a
// subset with its rows swapped for worklist lines is still a file the import
// reads. outputs[0] is the subset.
func refuseOverlappingOutputs(exportPath string, outputs []selectOutput) error {
	if err := refuseSelfOverwrite(exportPath, outputs[0].path); err != nil {
		return err
	}
	for i, o := range outputs {
		if i > 0 && sameFile(exportPath, o.path) {
			return fmt.Errorf("refusing to write %s over the input export: %s and %s are the same file", o.flag, o.path, exportPath)
		}
		for _, other := range outputs[:i] {
			if sameFile(other.path, o.path) {
				return fmt.Errorf("refusing to write %s and %s to one file: %s and %s are the same file", other.flag, o.flag, other.path, o.path)
			}
		}
	}
	return nil
}

// stagedFile is one output being written: a temp file beside path, a buffered
// writer over it and the first error any write met. A nil stagedFile is an
// output nobody asked for, and every method is a no-op on it.
type stagedFile struct {
	path, flag string
	tmp        *os.File
	w          *bufio.Writer
	err        error
	done       bool
}

// stage opens the temp file an output is written into.
func stage(path, flag string) (*stagedFile, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("%s: mkdir %s: %w", flag, dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".metaimport-select-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flag, err)
	}
	return &stagedFile{path: path, flag: flag, tmp: tmp, w: bufio.NewWriterSize(tmp, 1<<20)}, nil
}

// fail records err as the file's first error (nil changes nothing).
func (f *stagedFile) fail(err error) {
	if f != nil && f.err == nil && err != nil {
		f.err = err
	}
}

// write appends b, keeping the first error for commitAll.
func (f *stagedFile) write(b []byte) {
	if f == nil || f.err != nil {
		return
	}
	_, err := f.w.Write(b)
	f.fail(err)
}

// finish flushes and closes the temp file, readable like ordinary data (0600
// from CreateTemp would make it the operator's alone).
func (f *stagedFile) finish() error {
	err := f.err
	if err == nil {
		err = f.w.Flush()
	}
	if err == nil {
		err = f.tmp.Chmod(0o644)
	}
	if cerr := f.tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	return nil
}

// discard removes an uncommitted output's temp file.
func (f *stagedFile) discard() {
	if f == nil || f.done {
		return
	}
	f.done = true
	_ = f.tmp.Close()
	_ = os.Remove(f.tmp.Name())
}

// commitAll finishes every staged output and, only when all of them were
// written, renames each into place. A failure discards every temp file, so the
// outputs are all-or-nothing up to the renames themselves.
func commitAll(files ...*stagedFile) error {
	for _, f := range files {
		if f == nil {
			continue
		}
		if err := f.finish(); err != nil {
			for _, g := range files {
				g.discard()
			}
			return err
		}
	}
	for _, f := range files {
		if f == nil {
			continue
		}
		if err := os.Rename(f.tmp.Name(), f.path); err != nil {
			for _, g := range files {
				g.discard()
			}
			return fmt.Errorf("write %s: %w", f.path, err)
		}
		f.done = true
	}
	return nil
}

// refusalLog is the --refusals worklist over its staged file. A nil log is "not
// asked for".
type refusalLog struct{ f *stagedFile }

// refusalLine is one --refusals line. The field order is the line's key order.
type refusalLine struct {
	ASIN   string `json:"asin"`
	Reason string `json:"reason"`
}

// add writes one refused row, under the contract code of its report reason.
func (l *refusalLog) add(asin, reason string) {
	if l == nil {
		return
	}
	code, ok := refusalCodeOf[reason]
	if !ok {
		l.f.fail(fmt.Errorf("--refusals: no refusal code for reason %q", reason))
		return
	}
	line, err := json.Marshal(refusalLine{ASIN: asin, Reason: code})
	l.f.fail(err)
	l.f.write(append(line, '\n'))
}
