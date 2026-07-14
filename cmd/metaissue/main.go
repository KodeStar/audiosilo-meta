// Command metaissue turns a GitHub issue-form submission into canonical data-
// tree records (or a single-field correction, or a placed community sidecar),
// deduplicating against the existing catalog. It is the tool CONTRIBUTING.md
// promises: the bridge from an issue form to a validated bot pull request.
//
// Usage:
//
//	metaissue (--template <id> | --labels <json-array>) --body <file|-> [--data data] [--date YYYY-MM-DD]
//
// --template is the issue-form template id (add-work, add-recording,
// correct-data, characters, recaps, import; a leading "data:" is accepted so a
// routing label can be passed verbatim). --labels is the JSON array of the
// issue's label names (github.event.issue.labels.*.name); the routing template
// is derived from its data:<template> label, so the intake workflow does not
// re-implement the routing allowlist. When both are given, --labels wins.
// --body reads the rendered issue-form markdown from a file, or from stdin when
// "-".
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
	labelsJSON := flag.String("labels", "", "JSON array of the issue's label names; the routing template is derived from its data:<template> label (overrides --template)")
	bodyPath := flag.String("body", "-", "path to the rendered issue-form body, or - for stdin")
	date := flag.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	flag.Parse()

	if *date != "" && !dateRE.MatchString(*date) {
		fmt.Fprintf(os.Stderr, "metaissue: --date %q must be YYYY-MM-DD\n", *date)
		os.Exit(2)
	}

	tmpl := *template
	if *labelsJSON != "" {
		labels, err := parseLabels(*labelsJSON)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metaissue:", err)
			os.Exit(2)
		}
		// Derive the routing template from the issue's labels. An absent routing
		// label is a data error, not a usage error: emit an invalid verdict (exit
		// 0) so the intake workflow comments it back instead of ending silently.
		tmpl = issueform.TemplateFromLabels(labels)
		if tmpl == "" {
			emitResult(issueform.Result{
				Status:   issueform.StatusInvalid,
				Messages: []string{"no data-routing template label on the issue (expected a data:<template> label such as data:add-work)"},
			})
			return
		}
	}
	if tmpl == "" {
		fmt.Fprintln(os.Stderr, "metaissue: --template or --labels is required")
		os.Exit(2)
	}

	body, err := readBody(*bodyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaissue:", err)
		os.Exit(2)
	}

	emitResult(issueform.Process(issueform.Options{
		DataDir:  *data,
		Template: tmpl,
		Body:     body,
		Date:     *date,
	}))
}

// parseLabels decodes the --labels JSON array of issue label names.
func parseLabels(s string) ([]string, error) {
	var labels []string
	if err := json.Unmarshal([]byte(s), &labels); err != nil {
		return nil, fmt.Errorf("parse --labels JSON array: %w", err)
	}
	return labels, nil
}

// emitResult writes the JSON verdict to stdout. A write failure is the only
// non-zero exit from here; every processing outcome is a status in the JSON.
func emitResult(r issueform.Result) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
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
