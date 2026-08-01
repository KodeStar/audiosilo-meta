package migrate

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"strings"
)

// The one-time added_at backfill.
//
// Until this migration, a work's added_at was derived at RELEASE time by walking
// git history for the commit that first added its work.json
// (.github/workflows/release.yml, the added.tsv step). Pack files break that
// derivation - a pack's add-date is not its entries' - so the value moves into
// the records themselves, once, here.
//
// The walk below is release.yml's, verbatim in its semantics, with recordings
// added to what it keys on:
//
//	git log --reverse --diff-filter=A --date-order \
//	  --format='COMMIT%x09%aI' --name-only -- data/works \
//	| awk -F'\t' '/^COMMIT\t/{d=$2; next} /work\.json$/{if (!($0 in seen)) {seen[$0]=1; print d "\t" $0}}'
//
// --reverse + --date-order walk oldest-first and the seen guard keeps the FIRST
// appearance of a path, so a later move never overwrites the original date. The
// author date %aI is spliced in VERBATIM, full RFC 3339 offset and all: the
// release artifact's added_at column has to hold the same bytes after the
// migration as before it, which is what the equivalence proof checks.
type addedDates struct {
	// works maps a work slug to the date its work.json first appeared.
	works map[string]string
	// recs maps a work slug to its recording slugs' own first-add dates.
	recs map[string]map[string]string
}

func newAddedDates() addedDates {
	return addedDates{works: map[string]string{}, recs: map[string]map[string]string{}}
}

// empty reports whether the walk found nothing at all.
func (a addedDates) empty() bool { return len(a.works) == 0 && len(a.recs) == 0 }

// rec returns a recording's date, empty when the walk did not date it.
func (a addedDates) rec(workSlug, recSlug string) string { return a.recs[workSlug][recSlug] }

// gitAddedDates runs the history walk in repoDir over pathspec (the works root,
// repo-relative) and returns the first-add dates it found.
func gitAddedDates(repoDir, pathspec string) (addedDates, error) {
	cmd := exec.Command("git", "log", "--reverse", "--diff-filter=A", "--date-order",
		"--format=COMMIT\t%aI", "--name-only", "--", pathspec)
	cmd.Dir = repoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return addedDates{}, fmt.Errorf("git log in %s: %w: %s", repoDir, err, strings.TrimSpace(stderr.String()))
	}
	return parseAddedLog(out), nil
}

// parseAddedLog turns the git-log output into first-add dates. It is the awk
// one-liner: a COMMIT line sets the current date, any other non-empty line is a
// path, and only a path's FIRST appearance counts.
func parseAddedLog(out []byte) addedDates {
	dates := newAddedDates()
	seen := map[string]bool{}

	date := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "COMMIT\t"); ok {
			date = strings.TrimSpace(rest)
			continue
		}
		if date == "" || seen[line] {
			continue
		}
		seen[line] = true
		dates.put(line, date)
	}
	return dates
}

// put records one path's date. Paths are repo-relative
// ("data/works/bo/book-one/work.json"), so the slugs come from the tail of the
// path exactly as the retired cmd/metabuild parseAdded took them: a work is the
// directory holding work.json, a recording is its file name under recordings/.
//
// A slug's first path wins, mirroring parseAdded's map-insert guard: two shard
// directories could hold the same slug, and the older commit is the answer.
func (d addedDates) put(p, date string) {
	p = path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
	base := path.Base(p)
	dir := path.Dir(p)
	switch {
	case base == "work.json":
		slug := path.Base(dir)
		if slug == "" || slug == "." || slug == "/" {
			return
		}
		if _, dup := d.works[slug]; !dup {
			d.works[slug] = date
		}
	case path.Base(dir) == "recordings" && isJSON(base):
		recSlug := trimJSON(base)
		workSlug := path.Base(path.Dir(dir))
		if recSlug == "" || workSlug == "" || workSlug == "." || workSlug == "/" {
			return
		}
		if d.recs[workSlug] == nil {
			d.recs[workSlug] = map[string]string{}
		}
		if _, dup := d.recs[workSlug][recSlug]; !dup {
			d.recs[workSlug][recSlug] = date
		}
	}
}

// resolveRepo locates the git repository dataDir sits in and the works pathspec
// to walk, both as release.yml's checkout would have seen them: the repository
// root, and the data directory's repo-relative path plus "works".
func resolveRepo(dataDir string) (repoDir, pathspec string, err error) {
	top, err := gitOutput(dataDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	prefix, err := gitOutput(dataDir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", "", err
	}
	return top, path.Join(strings.TrimSuffix(prefix, "/"), "works"), nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s",
			strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
