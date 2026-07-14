// Command metaissue turns a GitHub issue-form submission into canonical data-
// tree records (or a single-field correction, or a placed community sidecar),
// deduplicating against the existing catalog. It is the tool CONTRIBUTING.md
// promises: the bridge from an issue form to a validated bot pull request.
//
// Usage:
//
//	metaissue --template <id> --body <file|-> [--data data] [--date YYYY-MM-DD]
//
// --template is the issue-form template id (add-work, add-recording,
// correct-data, characters, recaps, import; a leading "data:" is accepted so a
// routing label can be passed verbatim). --body reads the rendered issue-form
// markdown from a file, or from stdin when "-".
//
// It writes a machine-readable JSON result to stdout so the intake workflow can
// branch on it:
//
//	{"status":"ok"|"duplicate"|"needs-human"|"invalid","files":[...],"messages":[...]}
//
// SECURITY: the issue body and any attachment are untrusted data. Nothing in a
// submission is executed; attachments are fetched HTTPS-only from GitHub's
// user-attachment hosts with a size cap. The CLI exits 0 whenever it produced a
// result (any status) and only non-zero on a usage/IO error, so the workflow
// always gets a JSON verdict to act on.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/kodestar/audiosilo-meta/internal/issueform"
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func main() {
	data := flag.String("data", "data", "path to the data directory")
	template := flag.String("template", "", "issue-form template id (add-work, add-recording, correct-data, characters, recaps, import)")
	bodyPath := flag.String("body", "-", "path to the rendered issue-form body, or - for stdin")
	date := flag.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	flag.Parse()

	if *template == "" {
		fmt.Fprintln(os.Stderr, "metaissue: --template is required")
		os.Exit(2)
	}
	if *date != "" && !dateRE.MatchString(*date) {
		fmt.Fprintf(os.Stderr, "metaissue: --date %q must be YYYY-MM-DD\n", *date)
		os.Exit(2)
	}

	body, err := readBody(*bodyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaissue:", err)
		os.Exit(2)
	}

	result := issueform.Process(issueform.Options{
		DataDir:  *data,
		Template: *template,
		Body:     body,
		Date:     *date,
	})

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "metaissue: encode result:", err)
		os.Exit(1)
	}
}

func readBody(pathArg string) (string, error) {
	if pathArg == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(pathArg)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	return string(data), nil
}
