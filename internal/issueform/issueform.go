// Package issueform turns a GitHub issue-form submission into the canonical
// data-tree records a hand-authored pull request would carry. It parses the
// rendered issue body (### <label> headings), composes work/recording/person/
// series records (add-work, add-recording), applies a single-field correction
// (correct-data), or places a community sidecar attachment (characters/recaps),
// deduplicating against the existing catalog. It reuses internal/importer's
// building blocks (slug rules, ASIN normalization, name splitting) so a form
// submission is indistinguishable from a direct edit, and returns a
// machine-readable Result the intake workflow branches on.
//
// SECURITY: the issue body and any fetched attachment are treated strictly as
// untrusted data. Nothing in a submission is ever executed or evaluated;
// attachments are fetched over HTTPS from a pinned host allowlist with a size
// cap (see fetch.go) and only ever JSON-decoded and schema-validated.
package issueform

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/canonical"
	"github.com/kodestar/audiosilo-meta/internal/check"
	"github.com/kodestar/audiosilo-meta/internal/model"
)

// Status is the outcome of processing a submission.
type Status string

const (
	// StatusOK means records were composed, formatted, and validated cleanly.
	StatusOK Status = "ok"
	// StatusDuplicate means the submission's ASIN/ISBN/slug already exists.
	StatusDuplicate Status = "duplicate"
	// StatusNeedsHuman means the submission is well-formed but cannot be applied
	// mechanically (an unresolved reference, a complex correction, an overwrite).
	StatusNeedsHuman Status = "needs-human"
	// StatusInvalid means the submission is malformed (missing required field,
	// unchecked license box, bad JSON, failed validation).
	StatusInvalid Status = "invalid"
)

// Result is the machine-readable outcome written to stdout by cmd/metaissue.
// Template is the normalized routing template the submission was processed as
// (add-work, add-recording, correct-data, characters, recaps, import); the
// intake workflow reads it for the pull-request title/body and license layer.
type Result struct {
	Status   Status   `json:"status"`
	Template string   `json:"template,omitempty"`
	License  string   `json:"license,omitempty"`
	Files    []string `json:"files"`
	Messages []string `json:"messages"`
}

// Fetcher fetches the bytes of a URL (used for issue-form file attachments). It
// is injectable so tests never touch the network; the default (fetch.go) is
// HTTPS-only, host-pinned, and size-capped.
type Fetcher func(url string) ([]byte, error)

// Options configures a run.
type Options struct {
	// DataDir is the data root (contains works/, people/, series/).
	DataDir string
	// Template is the issue-form template id (add-work, add-recording,
	// correct-data, characters, recaps, import). A leading "data:" and legacy
	// aliases are accepted.
	Template string
	// Body is the raw rendered issue-form markdown body.
	Body string
	// Date is the YYYY-MM-DD stamp for composed sources. Defaults to today (UTC).
	Date string
	// Fetch resolves attachment URLs. Defaults to the pinned HTTPS fetcher.
	Fetch Fetcher
}

// composer accumulates the writes, messages, and status for one submission.
type composer struct {
	dataDir string
	date    string
	fetch   Fetcher

	// Identity maps seeded from the existing catalog for dedup.
	people  map[string]bool
	works   map[string]*model.Work
	series  map[string]*model.Series
	asinRec map[string]string // ASIN -> "data/works/.../recordings/<id>.json"
	isbnRec map[string]string // ISBN -> recording path

	writes   map[string][]byte // data-relative slash path -> canonical bytes
	messages []string
	status   Status
	// handled is set by paths that write to disk and validate themselves
	// (import), so Process skips the generic flush.
	handled bool
	// directFiles lists files a self-handling path reports (data/-prefixed).
	directFiles []string
}

// Process turns one issue-form submission into records and returns the outcome.
// It never returns an error; every failure is encoded in the Result so the
// intake workflow can comment it back. The returned Result carries the
// normalized routing template (Result.Template) so the workflow does not
// re-derive it.
func Process(opts Options) Result {
	r := process(opts)
	if r.Template == "" {
		r.Template = normalizeTemplate(opts.Template)
	}
	r.License = licenseLayer(r.Template)
	return r
}

// licenseLayer names the license layer the composed records carry, keyed off the
// normalized routing template: the community expressive sidecars (characters/
// recaps) are CC BY-SA 3.0, every core template (work/recording/correction/
// import) is CC0-1.0. Go owns this classification (the schema enforces the same
// split structurally); the intake workflow reads Result.License for the pull-
// request body instead of re-deriving it in bash.
func licenseLayer(tmpl string) string {
	switch tmpl {
	case "characters", "recaps":
		return "CC BY-SA 3.0 (community expressive layer)"
	default:
		return "CC0-1.0 (factual core)"
	}
}

func process(opts Options) Result {
	tmpl := normalizeTemplate(opts.Template)
	date := opts.Date
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	fetch := opts.Fetch
	if fetch == nil {
		fetch = defaultFetch
	}

	c := &composer{
		dataDir: opts.DataDir,
		date:    date,
		fetch:   fetch,
		people:  map[string]bool{},
		works:   map[string]*model.Work{},
		series:  map[string]*model.Series{},
		asinRec: map[string]string{},
		isbnRec: map[string]string{},
		writes:  map[string][]byte{},
	}
	c.loadExisting()

	sections := parseBody(opts.Body)

	switch tmpl {
	case "add-work":
		c.addWork(sections)
	case "add-recording":
		c.addRecording(sections)
	case "correct-data":
		c.correctData(sections)
	case "characters":
		c.addSidecar(sections, model.KindCharacters)
	case "recaps":
		c.addSidecar(sections, model.KindRecaps)
	case "import":
		c.importLibrary(sections)
	default:
		return Result{Status: StatusInvalid, Messages: []string{fmt.Sprintf("unknown template %q", opts.Template)}}
	}

	// Self-handling paths (import) produced their own outcome.
	if c.handled {
		if c.status == "" {
			c.status = StatusOK
		}
		return Result{Status: c.status, Files: c.directFiles, Messages: c.messages}
	}

	// A terminal status (duplicate/needs-human/invalid) short-circuits: never
	// write partial records for a submission we are not accepting.
	if c.status != "" && c.status != StatusOK {
		return Result{Status: c.status, Messages: c.messages}
	}
	if len(c.writes) == 0 {
		return Result{Status: StatusInvalid, Messages: appendIfEmpty(c.messages, "nothing to write")}
	}

	if msg := c.flush(); msg != "" {
		return Result{Status: StatusInvalid, Messages: append(c.messages, msg)}
	}
	if probs := c.validate(); len(probs) > 0 {
		return Result{Status: StatusInvalid, Messages: append(c.messages, probs...)}
	}
	return Result{Status: StatusOK, Files: c.fileList(), Messages: c.messages}
}

// loadExisting seeds the dedup maps from the committed data tree.
func (c *composer) loadExisting() {
	cat := check.Load(c.dataDir).Catalog
	if cat == nil {
		return
	}
	for _, p := range cat.People {
		c.people[p.ID] = true
	}
	for _, w := range cat.Works {
		c.works[w.ID] = w
		for _, r := range w.Recordings {
			recPath := recordingPath(w.ID, r.ID)
			for _, a := range r.ASIN {
				c.asinRec[a.ASIN] = recPath
			}
			for _, isbn := range r.ISBN {
				c.isbnRec[isbn] = recPath
			}
		}
	}
	for _, s := range cat.Series {
		c.series[s.ID] = s
	}
}

// emit canonicalizes v and queues it for writing at rel (a data-relative,
// slash-separated path).
func (c *composer) emit(rel string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		c.fail(StatusInvalid, "marshal %s: %v", rel, err)
		return
	}
	formatted, err := canonical.Format(data)
	if err != nil {
		c.fail(StatusInvalid, "canonicalize %s: %v", rel, err)
		return
	}
	c.writes[filepath.ToSlash(rel)] = formatted
}

// writeRaw canonicalizes an already-decoded record (a map read back from disk or
// assembled field-by-field) and queues it at rel (a data-relative, slash path).
// It returns false and fails the run on a marshal/canonicalize error, so a caller
// writes `if !c.writeRaw(rel, obj) { return }`. Unlike emit, it takes the decoded
// object so a path can edit an existing file in place while preserving every
// field the form does not manage.
func (c *composer) writeRaw(rel string, obj map[string]any) bool {
	data, err := json.Marshal(obj)
	if err != nil {
		c.fail(StatusInvalid, "marshal %s: %v", "data/"+rel, err)
		return false
	}
	formatted, err := canonical.Format(data)
	if err != nil {
		c.fail(StatusInvalid, "canonicalize %s: %v", "data/"+rel, err)
		return false
	}
	c.writes[rel] = formatted
	return true
}

// dedupIdentifiers fails with StatusDuplicate when any of the submission's ASINs
// or ISBNs already resolves to a recording in the catalog, returning true so the
// caller stops before writing anything. asinHint is appended to the ASIN-
// duplicate message: the add-work path steers the submitter to the add-recording
// form, add-recording passes "".
func (c *composer) dedupIdentifiers(asins []outASIN, isbns []string, asinHint string) bool {
	for _, a := range asins {
		if p, ok := c.asinRec[a.ASIN]; ok {
			c.fail(StatusDuplicate, "ASIN %s already exists (duplicate of %s)%s", a.ASIN, "data/"+p, asinHint)
			return true
		}
	}
	for _, isbn := range isbns {
		if p, ok := c.isbnRec[isbn]; ok {
			c.fail(StatusDuplicate, "ISBN %s already exists (duplicate of %s)", isbn, "data/"+p)
			return true
		}
	}
	return false
}

// flush writes every queued file to disk, creating parent directories. Returns
// a non-empty error message on an I/O failure.
func (c *composer) flush() string {
	rels := make([]string, 0, len(c.writes))
	for rel := range c.writes {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		full := filepath.Join(c.dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Sprintf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, c.writes[rel], 0o644); err != nil {
			return fmt.Sprintf("write %s: %v", rel, err)
		}
	}
	return ""
}

// validate runs the full metacheck over the tree after writing and returns any
// problems as messages. The tree is green on main, so any problem is caused by
// this submission's writes.
func (c *composer) validate() []string {
	res := check.Load(c.dataDir)
	if res.OK() {
		return nil
	}
	msgs := make([]string, 0, len(res.Problems))
	for _, p := range res.Problems {
		msgs = append(msgs, p.String())
	}
	return msgs
}

// fileList returns the written paths, data/-prefixed and sorted, for the Result.
func (c *composer) fileList() []string {
	out := make([]string, 0, len(c.writes))
	for rel := range c.writes {
		out = append(out, "data/"+rel)
	}
	sort.Strings(out)
	return out
}

// fail sets the terminal status and appends a formatted message. The first
// terminal status wins; later notes still accumulate.
func (c *composer) fail(status Status, format string, args ...any) {
	if c.status == "" || c.status == StatusOK {
		c.status = status
	}
	c.messages = append(c.messages, fmt.Sprintf(format, args...))
}

// note appends an informational message without changing the status.
func (c *composer) note(format string, args ...any) {
	c.messages = append(c.messages, fmt.Sprintf(format, args...))
}

// source builds the provenance stamp for a composed record from the form's
// free-text sources/evidence field.
func (c *composer) source(ref string) outSource {
	return outSource{Type: sourceUser, Ref: strings.TrimSpace(ref), ImportedAt: c.date}
}

// getOrCreatePerson returns the slug for a person name, emitting a new person
// record when the slug is not already in the catalog or this run.
func (c *composer) getOrCreatePerson(name, sourceRef string) (string, bool) {
	slug := slugify(name)
	if slug == "" {
		c.fail(StatusInvalid, "name %q produced an empty slug", name)
		return "", false
	}
	if c.people[slug] {
		return slug, true
	}
	c.people[slug] = true
	c.emit(filepath.ToSlash(filepath.Join("people", model.Shard(slug), slug+".json")), outPerson{
		ID: slug, Name: strings.TrimSpace(name), License: licenseCC0, Sources: []outSource{c.source(sourceRef)},
	})
	return slug, true
}

// normalizeTemplate maps a template id (possibly "data:"-prefixed or a legacy
// alias) to the canonical form used by the switch in Process.
func normalizeTemplate(t string) string {
	t = strings.TrimSpace(strings.ToLower(t))
	t = strings.TrimPrefix(t, "data:")
	switch t {
	case "add-work", "work":
		return "add-work"
	case "add-recording", "recording":
		return "add-recording"
	case "correct-data", "correction", "correct":
		return "correct-data"
	case "add-characters", "characters":
		return "characters"
	case "add-recaps", "recaps":
		return "recaps"
	case "import", "import-library":
		return "import"
	default:
		return t
	}
}

// routingTemplates is the set of "data:<t>" label suffixes that name an intake
// template, in the canonical routing form Process accepts. It deliberately
// EXCLUDES the outcome labels the intake workflow adds on a non-ok run
// (data:duplicate, data:needs-human, data:invalid) and the bare "data" label, so
// a re-run's own labels can never be mistaken for a routing template.
var routingTemplates = map[string]bool{
	"add-work":      true,
	"add-recording": true,
	"correction":    true,
	"characters":    true,
	"recaps":        true,
	"import":        true,
}

// TemplateFromLabels picks the intake routing template from an issue's label
// names. A label routes when it is "data:<t>" for a known template <t> (add-work,
// add-recording, correction, characters, recaps, import) OR - for issues opened
// before the templates' labels were renamed to the data: namespace - the bare
// legacy label name <t> against the same allowlist; the first routing label (in
// the given order) wins. The bare "data" label and the outcome labels
// (data:duplicate, data:needs-human, data:invalid) never route, because they are
// absent from routingTemplates under either spelling. It returns "" when no label
// routes, which cmd/metaissue surfaces as an invalid verdict. This is the single
// source of the routing allowlist the intake workflow used to duplicate in jq.
func TemplateFromLabels(labels []string) string {
	for _, l := range labels {
		name := strings.ToLower(strings.TrimSpace(l))
		if suffix, ok := strings.CutPrefix(name, "data:"); ok {
			// The current data:<t> routing label.
			if routingTemplates[suffix] {
				return suffix
			}
			continue
		}
		// A legacy bare label from before the data: rename (add-work, correction, ...).
		if routingTemplates[name] {
			return name
		}
	}
	return ""
}

func appendIfEmpty(msgs []string, msg string) []string {
	if len(msgs) == 0 {
		return []string{msg}
	}
	return msgs
}
