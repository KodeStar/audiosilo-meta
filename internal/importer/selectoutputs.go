package importer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// selectoutputs.go is how libex-select writes what it writes - the subset (-o)
// and the two machine-readable worklists (--refusals, --attachments) - and how
// `metaimport libex --skipped` writes its worklist (Worklist). Every output is
// STAGED - written into a temp file beside the destination - and renamed into
// place only once every one of them was written (commitAll). An interrupted or
// failed run leaves none of them, and the previous run's files stay as they
// were: a truncated NDJSON subset is still a perfectly importable file and would
// land as a silently half-sized tranche, and a half-written worklist reads as a
// complete one.

// selectOutput is one output path and the flag that named it, for the overlap
// refusals' messages.
type selectOutput struct{ flag, path string }

// sameFile reports whether two paths may name one file, erring toward yes: the
// same spelling, or - since distinct names can still be one file - a symlink or
// hard link to it (os.SameFile, asked when both exist), or, for a path that does
// not exist yet, the same file once written: the two parent directories resolved
// through their symlinks (filepath.EvalSymlinks) and the base names compared
// CASE-INSENSITIVELY, because the default filesystems of macOS (APFS) and Windows
// (NTFS) are, and "Subset.ndjson" beside "subset.ndjson" is one file there. On a
// case-sensitive filesystem that refuses a pair that would have been two files,
// which costs a rename; the other error costs the subset.
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
	if aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo) {
		return true
	}
	aDir, aBase, aOK := resolvedLocation(a)
	bDir, bBase, bOK := resolvedLocation(b)
	return aOK && bOK && strings.EqualFold(aDir, bDir) && strings.EqualFold(aBase, bBase)
}

// resolvedLocation is where a path's file is or would be written: its parent
// directory, absolute and resolved through every symlink, and its base name. A
// parent that does not exist yet (stage creates it) is resolved through its
// NEAREST EXISTING ancestor, with the missing components appended as spelled,
// so "out/s.ndjson" and "$PWD/out/s.ndjson" are one location before out/ exists.
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

// renameFile is os.Rename, a variable only so a test can make one rename of a
// commit fail and watch the rollback.
var renameFile = os.Rename

// commitAll finishes every staged output and, only when all of them were
// written, renames them into place: the worklists first and the SUBSET LAST, so
// a subset on disk means everything this run wrote is there with it - the
// subset is what a caller acts on.
//
// A destination that already exists (the previous run's file) is first moved
// aside, and the side copies are deleted only once every rename succeeded. A
// failure anywhere discards every temp file, removes what this commit already
// renamed into place and moves the side copies back, so a failed commit leaves
// the previous run's outputs exactly as they were.
func commitAll(subset *stagedFile, worklists ...*stagedFile) error {
	all := append([]*stagedFile{subset}, worklists...)
	type placed struct{ path, backup string }
	var done []placed
	rollback := func(err error) error {
		for _, g := range all {
			g.discard()
		}
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.Remove(done[i].path)
			if done[i].backup != "" {
				_ = os.Rename(done[i].backup, done[i].path)
			}
		}
		return err
	}
	for _, f := range all {
		if f == nil {
			continue
		}
		if err := f.finish(); err != nil {
			return rollback(err)
		}
	}
	order := make([]*stagedFile, 0, len(all))
	order = append(order, worklists...)
	order = append(order, subset)
	for _, f := range order {
		if f == nil {
			continue
		}
		backup, err := moveAside(f.path)
		if err != nil {
			return rollback(fmt.Errorf("write %s: %w", f.path, err))
		}
		if err := renameFile(f.tmp.Name(), f.path); err != nil {
			if backup != "" {
				_ = os.Rename(backup, f.path)
			}
			return rollback(fmt.Errorf("write %s: %w", f.path, err))
		}
		f.done = true
		done = append(done, placed{f.path, backup})
	}
	for _, d := range done {
		if d.backup != "" {
			_ = os.Remove(d.backup)
		}
	}
	return nil
}

// moveAside renames an existing destination to a fresh side name in its own
// directory and returns that name ("" when there was nothing there).
func moveAside(path string) (string, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "", nil
	}
	side, err := os.CreateTemp(filepath.Dir(path), ".metaimport-prev-*")
	if err != nil {
		return "", err
	}
	name := side.Name()
	_ = side.Close()
	if err := os.Rename(path, name); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// writeRowSkip writes one {"asin","reason"} line - the one line shape
// --refusals and --skipped share - refusing a line whose reason is not a code of
// the contract (RefusalCodes), so no line ever carries "reason":"".
func writeRowSkip(f *stagedFile, s RowSkip) {
	if !isRefusalCode(s.Reason) {
		f.fail(fmt.Errorf("%s: %q is not a refusal code (row %s)", f.flag, s.Reason, s.ASIN))
		return
	}
	line, err := json.Marshal(s)
	f.fail(err)
	f.write(append(line, '\n'))
}

// isRefusalCode reports whether code is one of RefusalCodes.
func isRefusalCode(code string) bool {
	for _, c := range RefusalCodes() {
		if c == code {
			return true
		}
	}
	return false
}

// refusalCodeFor is the contract code of a report reason, or an error naming a
// reason that has none - a caller records the error rather than a line with an
// empty reason.
func refusalCodeFor(reason string) (string, error) {
	code, ok := refusalCodeOf[reason]
	if !ok {
		return "", fmt.Errorf("no refusal code for reason %q", reason)
	}
	return code, nil
}

// Worklist is one staged {"asin","reason"} NDJSON output outside libex-select -
// `metaimport libex --skipped`. Open it BEFORE the run, so a bad path fails
// before anything is written, then Write and Commit once the run completed, or
// Discard.
type Worklist struct{ f *stagedFile }

// OpenWorklist stages the worklist at path (creating its directory); flag names
// it in errors.
func OpenWorklist(path, flag string) (*Worklist, error) {
	f, err := stage(path, flag)
	if err != nil {
		return nil, err
	}
	return &Worklist{f: f}, nil
}

// Write appends one line per skip.
func (w *Worklist) Write(skips []RowSkip) {
	for _, s := range skips {
		writeRowSkip(w.f, s)
	}
}

// Commit renames the worklist into place (keeping the previous file on
// failure, like commitAll).
func (w *Worklist) Commit() error { return commitAll(w.f) }

// Discard drops an uncommitted worklist.
func (w *Worklist) Discard() { w.f.discard() }

// refusalLog is the --refusals worklist over its staged file. A nil log is "not
// asked for".
//
// Two rules keep every line one a reader can act on. A line never names an ASIN
// the subset carries: the only refusal that can share an ASIN with a selected
// row is duplicate-asin (the other copy was kept), so those lines are HELD and
// written at the end (flush), minus every ASIN the subset carries. And a line
// never carries an empty ASIN: a row stating none cannot be named, so it is
// counted (unnamed, printed by the report) instead of written.
type refusalLog struct {
	f          *stagedFile
	duplicates []string
	unnamed    int
}

// add writes one refused row, under the contract code of its report reason.
func (l *refusalLog) add(asin, reason string) {
	if l == nil {
		return
	}
	switch {
	case asin == "":
		l.unnamed++
	case reason == reasonDuplicateASIN:
		l.duplicates = append(l.duplicates, asin)
	default:
		l.line(asin, reason)
	}
}

func (l *refusalLog) line(asin, reason string) {
	code, err := refusalCodeFor(reason)
	if err != nil {
		l.f.fail(fmt.Errorf("--refusals: %w", err))
		return
	}
	writeRowSkip(l.f, RowSkip{ASIN: asin, Reason: code})
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
	for _, asin := range l.duplicates {
		if !kept[asin] {
			l.line(asin, reasonDuplicateASIN)
		}
	}
	return l.unnamed
}
