package recorddiff

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	errBuf  *bytes.Buffer
}

// OpenGitSource starts a reader for the data tree at rev inside the repository at
// repoDir. rev is resolved to a commit id once, so a symbolic revision ("HEAD~1")
// is read consistently and can be reported back as what it actually was.
func OpenGitSource(repoDir, dataDir, rev string) (*GitSource, error) {
	sha, err := RevParse(repoDir, rev)
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
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return &GitSource{
		rev:     sha,
		dataDir: dataDir,
		cmd:     cmd,
		in:      stdin,
		out:     bufio.NewReaderSize(stdout, 64<<10),
		errBuf:  &errBuf,
	}, nil
}

// Rev returns the commit id the source reads, as resolved at Open.
func (g *GitSource) Rev() string { return g.rev }

// Read returns the file's bytes at this revision. A path the revision does not
// hold reads as not found, which is how a file the change created or deleted is
// handled.
func (g *GitSource) Read(rel string) ([]byte, bool, error) {
	spec := g.rev + ":" + path.Join(g.dataDir, rel)
	if _, err := io.WriteString(g.in, spec+"\n"); err != nil {
		return nil, false, fmt.Errorf("git cat-file: %w%s", err, g.stderr())
	}
	header, err := g.out.ReadString('\n')
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file: reading the header for %s: %w%s", spec, err, g.stderr())
	}
	fields := strings.Fields(strings.TrimRight(header, "\n"))
	if len(fields) >= 2 {
		// git answers an object it cannot resolve with "<spec> missing" (or, for
		// an ambiguous or broken one, its own last word). None of them is a file.
		switch fields[len(fields)-1] {
		case "missing", "ambiguous", "dangling", "notdir":
			return nil, false, nil
		}
	}
	if len(fields) != 3 {
		return nil, false, fmt.Errorf("git cat-file: unexpected header %q for %s", header, spec)
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file: unreadable size in %q: %w", header, err)
	}
	// The payload is followed by one newline the protocol adds; read and discard
	// it, or the next header would start mid-stream.
	buf := make([]byte, size+1)
	if _, err := io.ReadFull(g.out, buf); err != nil {
		return nil, false, fmt.Errorf("git cat-file: reading %s: %w%s", spec, err, g.stderr())
	}
	return buf[:size], true, nil
}

// Close shuts the batch process down. It is not an error for git to have exited
// already.
func (g *GitSource) Close() error {
	if g.cmd == nil {
		return nil
	}
	closeErr := g.in.Close()
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

// RevParse resolves a revision to the commit id it names.
func RevParse(repoDir, rev string) (string, error) {
	out, err := git(repoDir, "rev-parse", rev+"^{commit}")
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
func GitChangedPaths(repoDir, dataDir, base, head string) ([]string, error) {
	out, err := git(repoDir, "diff", "--name-only", base+"..."+head, "--", dataDir+"/")
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
	sort.Strings(rels)
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

// DirSource reads the data tree from a directory on disk. It is what the unit
// tests diff two fixture trees with.
type DirSource struct{ Dir string }

// Read returns the file's bytes, or not-found for a path this tree does not hold.
func (d DirSource) Read(rel string) ([]byte, bool, error) {
	raw, err := os.ReadFile(filepath.Join(d.Dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// DirPaths lists every JSON file either directory holds, data-relative and
// sorted. It is the directory equivalent of GitChangedPaths, deliberately
// WIDER: it names every file rather than the changed ones, which Compute
// classifies to the same answer (an entry identical on both sides is unchanged
// however it was reached) at a cost only a fixture tree can afford.
func DirPaths(dirs ...string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) && p == dir {
					return nil
				}
				return err
			}
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
