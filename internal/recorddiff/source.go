package recorddiff

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// source.go is where the two versions of the tree come FROM. Compute takes a pair
// of Sources and knows nothing else about them, so the command reads two git
// revisions out of a checkout while the tests read two directories - one
// comparison, two ways of being handed its input.

// GitSource reads the data tree at one revision of a repository.
//
// It holds ONE `git cat-file --batch` process open for its lifetime rather than
// running `git show` per file. A tranche is hundreds of files and both sides of
// each are read, so the per-process spelling of this is a thousand forks on a job
// that has a minute; the batch protocol is strictly request/response, so the
// saving costs only the small reader below.
type GitSource struct {
	rev     string
	dataDir string
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	errBuf  *syncBuffer
	// broken latches the first read failure. The protocol is a stream, so a read
	// that stopped part-way through a reply leaves the next header starting
	// mid-object: every answer after it would be fiction. Refusing outright is
	// the only honest continuation, and it is a property of the reader rather
	// than of whatever the caller does with the error.
	broken error
}

// OpenGitSource starts a reader for the data tree at rev inside the repository at
// repoDir. rev is resolved to a commit id once, so a symbolic revision ("HEAD~1")
// is read consistently and can be reported back as what it actually was.
func OpenGitSource(repoDir, dataDir, rev string) (*GitSource, error) {
	sha, err := revParse(repoDir, rev)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = repoDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errBuf := &syncBuffer{}
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return &GitSource{
		rev:     sha,
		dataDir: dataDir,
		cmd:     cmd,
		in:      stdin,
		out:     bufio.NewReaderSize(stdout, 64<<10),
		errBuf:  errBuf,
	}, nil
}

// syncBuffer is the stderr sink, and it is mutex-guarded because os/exec fills
// it from a goroutine of its own: a Stderr that is not an *os.File is copied
// into by a helper that runs until Wait returns, while stderr() below reads what
// git has complained about SO FAR - on a failed read, long before Wait. A plain
// bytes.Buffer read that way is a data race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Rev returns the commit id the source reads, as resolved at Open.
func (g *GitSource) Rev() string { return g.rev }

// Read returns the file's bytes at this revision. A path the revision does not
// hold reads as not found, which is how a file the change created or deleted is
// handled.
//
// A reply is ALWAYS consumed whole - the "missing" one-liner as much as an
// object's header, payload and trailing newline - because the next request's
// answer begins where this one ended. The only reads that do not consume their
// reply are the ones that failed, and those latch the reader broken.
func (g *GitSource) Read(rel string) ([]byte, bool, error) {
	if g.broken != nil {
		return nil, false, g.broken
	}
	spec := g.rev + ":" + path.Join(g.dataDir, rel)
	if _, err := io.WriteString(g.in, spec+"\n"); err != nil {
		return nil, false, g.fail(fmt.Errorf("git cat-file: %w%s", err, g.stderr()))
	}
	header, err := g.out.ReadString('\n')
	if err != nil {
		return nil, false, g.fail(fmt.Errorf("git cat-file: reading the header for %s: %w%s", spec, err, g.stderr()))
	}
	fields := strings.Fields(strings.TrimRight(header, "\n"))
	if len(fields) >= 2 {
		// git answers an object it cannot resolve with "<spec> missing" (or, for
		// an ambiguous or broken one, its own last word). None of them is a file,
		// and each is the WHOLE reply - there is no payload to skip past.
		switch fields[len(fields)-1] {
		case "missing", "ambiguous", "dangling", "notdir":
			return nil, false, nil
		}
	}
	if len(fields) != 3 {
		return nil, false, g.fail(fmt.Errorf("git cat-file: unexpected header %q for %s", header, spec))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, false, g.fail(fmt.Errorf("git cat-file: unreadable size in %q: %w", header, err))
	}
	// The payload is followed by one newline the protocol adds; read and discard
	// it, or the next header would start mid-stream.
	buf := make([]byte, size+1)
	if _, err := io.ReadFull(g.out, buf); err != nil {
		return nil, false, g.fail(fmt.Errorf("git cat-file: reading %s: %w%s", spec, err, g.stderr()))
	}
	return buf[:size], true, nil
}

// fail latches the reader broken and hands the error back for returning.
func (g *GitSource) fail(err error) error {
	g.broken = err
	return err
}

// Close shuts the batch process down and REAPS it. It is not an error for git to
// have exited already, and calling it twice is a no-op.
//
// Closing stdin is not on its own enough to end the process: a reply this reader
// abandoned part-way (the broken-stream case above) can leave git blocked
// writing into a full pipe, where it never looks at stdin again and Wait would
// hang forever. So the remaining output is drained to EOF first - which is also
// what releases git from that write - and only then is the process waited for.
func (g *GitSource) Close() error {
	if g.cmd == nil {
		return nil
	}
	closeErr := g.in.Close()
	_, _ = io.Copy(io.Discard, g.out)
	waitErr := g.cmd.Wait()
	g.cmd = nil
	if closeErr != nil {
		return closeErr
	}
	return waitErr
}

// stderr returns what git complained about, formatted for appending to an error.
func (g *GitSource) stderr() string {
	s := strings.TrimSpace(g.errBuf.String())
	if s == "" {
		return ""
	}
	return ": " + s
}

// revParse resolves a revision to the commit id it names.
func revParse(repoDir, rev string) (string, error) {
	out, err := git(repoDir, "rev-parse", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// MergeBase resolves the commit base and head last had in common - the revision
// the CONTENT of the base side must be read at.
//
// It exists because the two halves of a comparison have to agree about what
// "base" is. A pull request's base sha is the base BRANCH's tip, which moves
// while the branch sits open, and the changed-path listing below is the
// three-dot comparison (the merge base against head) a reviewer's "Files
// changed" tab shows. Reading the base side's bytes at the branch tip instead
// would compare the tranche's files against commits the branch never saw: every
// record another pull request merged into main in the meantime reads as REMOVED
// here, which is exactly the finding this summary exists to make trustworthy.
func MergeBase(repoDir, base, head string) (string, error) {
	out, err := git(repoDir, "merge-base", base, head)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// GitChangedPaths lists the files under dataDir that differ between base and
// head, DATA-RELATIVE and sorted.
//
// The comparison is the three-dot one - base...head, the merge base against head
// - so a branch that is merely behind main does not report main's own commits as
// its changes. That is the same comparison a pull request's "Files changed" tab
// shows, which is what a reviewer of one is judging.
//
// RENAME DETECTION IS OFF, and that is load-bearing rather than tidy. A pack
// rebind or a directory split RENAMES a pack file, and `--name-only` reports a
// rename as its destination path ALONE - so the source path never reaches
// Compute, its entries are absent from the base side, and every record in a
// merely-renamed pack is reported as ADDED. That is the precise storage-churn
// lie this package was written to end, so both halves of a rename are listed and
// the entries cancel out as moved-only.
func GitChangedPaths(repoDir, dataDir, base, head string) ([]string, error) {
	out, err := git(repoDir, "diff", "--no-renames", "--name-only", base+"..."+head, "--", dataDir+"/")
	if err != nil {
		return nil, err
	}
	prefix := dataDir + "/"
	var rels []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		rels = append(rels, rel)
	}
	slices.Sort(rels)
	return rels, nil
}

// git runs one git command in repoDir and returns its stdout.
func git(repoDir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// The other implementation of Source - the one that reads two fixture trees off
// disk - is dirSource in source_fixture_test.go: it has no production caller, so
// it is not production API.
