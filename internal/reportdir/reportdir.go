// Package reportdir is the shared plumbing of the two tools that write a REPORT
// DIRECTORY beside the data tree: internal/audit (the read-only data-quality audit) and
// internal/repair (the pass that applies its non-advisory proposals).
//
// Four things live here, and each was spelled twice before it did: the containment guard
// that refuses an output directory inside the data root, the buffered NDJSON writer both
// reports are made of, the thousands-separator renderer their markdown tables read
// better for, and the one-line summary of a load's first problem. None of them is a rule
// about DATA - that is why they can be shared without either tool reaching into the
// other - but all four are load-bearing for the same reason: a report that lands in
// data/ becomes a file metafmt and metacheck then judge as if it were a record, and an
// unbuffered encoder makes a syscall per line over reports that run to hundreds of
// thousands of them.
package reportdir

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// RefuseInsideData rejects an output directory that sits inside the data root. note is
// appended to the message so each caller can say what its own posture is.
//
// Enforcing it rather than trusting the operator is what makes "this tool never writes
// into data/" hold for any caller, and the comparison is over the ABSOLUTE,
// SYMLINK-RESOLVED forms: a link outside the tree pointing into it defeats a lexical
// comparison entirely, and pointing -o at such a link would put the report in data/ with
// the guard none the wiser.
func RefuseInsideData(dataDir, outDir, note string) error {
	data, err := Resolve(dataDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dataDir, err)
	}
	out, err := Resolve(outDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", outDir, err)
	}
	rel, err := filepath.Rel(data, out)
	if err != nil {
		return nil // on different volumes, so certainly not inside
	}
	if rel == "." || !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("the report directory %s is inside the data root %s: %s", outDir, dataDir, note)
	}
	return nil
}

// Resolve is a path's absolute, symlink-resolved form, resolved AS FAR AS IT EXISTS - a
// report directory is normally created by the run, so "not there yet" must not be an
// error.
func Resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Walk up to the first existing ancestor, resolve THAT, then re-append.
	rest, cur := "", abs
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil // nothing on the path exists; the lexical form is all there is
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// WriteNDJSON writes n records to path, one per line, compactly and without HTML
// escaping - a title carrying "&" or "<" must read as itself in a report. The writer is
// buffered because a class file is tens of thousands of lines and an unbuffered encoder
// makes a syscall per record.
func WriteNDJSON(path string, n int, at func(int) any) error {
	fh, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	buf := bufio.NewWriter(fh)
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	for i := range n {
		if err := enc.Encode(at(i)); err != nil {
			_ = fh.Close()
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if err := buf.Flush(); err != nil {
		_ = fh.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := fh.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// Comma renders a non-negative integer with thousands separators, so a six-figure count
// is readable in a table. Every count it is given is a length or a tally.
func Comma(n int) string {
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

// FirstProblem is a load's first problem as one line, for the error a tool exits with
// when a tree could not be loaded at all.
func FirstProblem(res check.Result) string {
	if len(res.Problems) == 0 {
		return "no catalog returned and no problem reported"
	}
	return res.Problems[0].String()
}

// Table renders a two-column markdown table with a right-aligned, comma-grouped count.
func Table(b *strings.Builder, head string, rows []Row) {
	fmt.Fprintf(b, "| %s | count |\n|---|---:|\n", head) //nolint:errcheck // a strings.Builder never fails
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %s |\n", r.Label, Comma(r.N)) //nolint:errcheck // as above
	}
}

// Row is one line of a Table.
type Row struct {
	Label string
	N     int
}
