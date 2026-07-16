// Command metabuild compiles the data/ tree into a SQLite artifact. It runs the
// full validation first and refuses to build invalid data.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	out := flag.String("o", "meta.sqlite", "output SQLite file")
	builtAt := flag.String("built-at", "", "build timestamp (RFC3339); defaults to now (UTC)")
	addedFile := flag.String("added", "", "tab-separated <ISO8601 date>\\t<work.json path> file giving each work's first-added date")
	flag.Parse()

	res := check.Load(*dataDir)
	if !res.OK() {
		for _, p := range res.Problems {
			fmt.Fprintln(os.Stderr, p.String())
		}
		fmt.Fprintf(os.Stderr, "refusing to build: %d validation problem(s)\n", len(res.Problems))
		os.Exit(1)
	}

	var ts time.Time
	if *builtAt != "" {
		parsed, err := time.Parse(time.RFC3339, *builtAt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metabuild: invalid --built-at:", err)
			os.Exit(2)
		}
		ts = parsed
	}

	var added map[string]string
	if *addedFile != "" {
		var err error
		added, err = parseAdded(*addedFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metabuild: --added:", err)
			os.Exit(2)
		}
	}

	if err := build.Build(res.Catalog, *out, ts, added); err != nil {
		fmt.Fprintln(os.Stderr, "metabuild:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "built %s: %d works, %d people, %d series\n",
		*out, len(res.Catalog.Works), len(res.Catalog.People), len(res.Catalog.Series))
}

// parseAdded reads a tab-separated "<ISO8601 date>\t<path>" file, where path is
// a repo-relative works/<shard>/<slug>/work.json. It returns a work-id -> date
// map, keying on the slug (the directory that contains work.json, which equals
// the work id). The first line for a given path wins; later duplicates and
// non-work.json lines are ignored.
func parseAdded(name string) (map[string]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[string]string{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		date, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		p = path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
		if path.Base(p) != "work.json" || seen[p] {
			continue
		}
		seen[p] = true
		slug := path.Base(path.Dir(p))
		if slug == "" || slug == "." {
			continue
		}
		if _, dup := out[slug]; !dup {
			out[slug] = strings.TrimSpace(date)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
