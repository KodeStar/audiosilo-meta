// Package atomicfile writes output files so that a reader never sees a half of
// one: each is STAGED into a temp file beside its destination and renamed into
// place only once every file of the set was written (CommitInOrder). A run that
// fails leaves none of the files it was writing; the caller's non-zero exit is
// the rest of the contract (a destination the failed run did not reach keeps
// whatever it held before).
package atomicfile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// File is one staged output. A nil *File is an output nobody asked for, and
// every method is a no-op on it.
type File struct {
	path string
	tmp  *os.File
	w    *bufio.Writer
	enc  *json.Encoder
	err  error
	done bool
}

// Stage opens the temp file an output at path is written into, creating path's
// directory first.
func Stage(path string) (*File, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".atomicfile-*.tmp")
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriterSize(tmp, 1<<20)
	return &File{path: path, tmp: tmp, w: w, enc: json.NewEncoder(w)}, nil
}

// StageIf is Stage for an optional output: an empty path stages nothing and
// returns a nil *File.
func StageIf(path string) (*File, error) {
	if path == "" {
		return nil, nil
	}
	return Stage(path)
}

// Write appends b verbatim, keeping the first error for the commit.
func (f *File) Write(b []byte) {
	if f == nil || f.err != nil {
		return
	}
	_, f.err = f.w.Write(b)
}

// Encode appends v as one JSON line (encoding/json's Encoder, one per file),
// keeping the first error for the commit.
func (f *File) Encode(v any) {
	if f == nil || f.err != nil {
		return
	}
	f.err = f.enc.Encode(v)
}

// Discard removes an uncommitted output's temp file.
func (f *File) Discard() {
	if f == nil || f.done {
		return
	}
	f.done = true
	_ = f.tmp.Close()
	_ = os.Remove(f.tmp.Name())
}

// finish flushes and closes the temp file, readable like ordinary data (0600
// from CreateTemp would make it the operator's alone).
func (f *File) finish() error {
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

// rename is os.Rename, a variable only so a test can fail one rename.
var rename = os.Rename

// CommitInOrder finishes every file and, only when all of them were written,
// renames them into place IN THE ORDER GIVEN - a caller puts the file its readers
// act on last (the COMMIT MARKER), so its presence means the others are there
// too. In a commit of SEVERAL files the marker's previous destination is
// removed before anything is renamed, so a commit that fails part-way leaves NO
// marker on disk - never a stale one beside new or missing companions. A
// commit of ONE file is a plain atomic replace: the rename swaps it in, and a
// failure leaves the previous file exactly as it was, with no moment at which
// the path is missing. nil files are skipped. A failure discards every temp file
// and removes the files this commit already renamed into place.
func CommitInOrder(files ...*File) error {
	fail := func(renamed []*File, err error) error {
		for _, f := range files {
			f.Discard()
		}
		for _, f := range renamed {
			_ = os.Remove(f.path)
		}
		return err
	}
	for _, f := range files {
		if f == nil {
			continue
		}
		if err := f.finish(); err != nil {
			return fail(nil, err)
		}
	}
	var marker *File
	count := 0
	for _, f := range files {
		if f != nil {
			marker = f
			count++
		}
	}
	if count > 1 {
		if err := os.Remove(marker.path); err != nil && !os.IsNotExist(err) {
			return fail(nil, fmt.Errorf("write %s: %w", marker.path, err))
		}
	}
	var renamed []*File
	for _, f := range files {
		if f == nil {
			continue
		}
		if err := rename(f.tmp.Name(), f.path); err != nil {
			return fail(renamed, fmt.Errorf("write %s: %w", f.path, err))
		}
		f.done = true
		renamed = append(renamed, f)
	}
	return nil
}
