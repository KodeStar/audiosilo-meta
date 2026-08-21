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
	"sort"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
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

// MarshalJSON guarantees Files always serializes as [] (never null): the intake
// workflow's PR-body step runs jq over .files[], which errors on a JSON null.
// Several producers leave Files nil (the no-routing-label verdict in
// cmd/metaissue builds a Result literal directly; the import path leaves it nil
// because the workflow diffs the tree instead), so the guarantee lives on the
// type rather than in any one producer. The alias avoids infinite recursion.
func (r Result) MarshalJSON() ([]byte, error) {
	type alias Result
	a := alias(r)
	if a.Files == nil {
		a.Files = []string{}
	}
	return json.Marshal(a)
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

// recRef locates a recording by its work and recording slugs. It is the
// importer's type: both writers address a recording the same way, because a
// recording has no identity of its own - it is a key inside its work's
// composite entry - and out.go already shares every record shape the two paths
// have in common.
type recRef = importer.RecRef

// composer accumulates the writes, messages, and status for one submission.
type composer struct {
	dataDir string
	date    string
	fetch   Fetcher
	// store is the write layer: composed records land as pack entries and
	// nothing reaches disk until flush. Its reads are queued-write-first, so a
	// submission that writes a work and then its first recording composes both
	// into one works-family entry.
	store *pack.Store

	// Identity maps seeded from the existing catalog for dedup.
	people  map[string]bool
	works   map[string]*model.Work
	series  map[string]*model.Series
	asinRec map[string]recRef // ASIN -> the recording carrying it
	// isbnRec maps an ISBN to the recording carrying it, keyed through
	// isbnKey: an ISBN-10's check digit is X or x in the schema, and the two
	// are one identifier.
	isbnRec map[string]recRef

	// identity is the normalized-identity index over the catalogue, taken from the
	// load that seeded the dedup maps rather than built here (check.Result.Identity).
	// It is the same index the bulk importer's create guard reads, so the two writers
	// cannot disagree about what one book is (dupidentity.go).
	identity *check.WorkIdentity

	// submissionASINs is the set of normalized ASINs THIS submission stated in
	// its own ASIN field, filled by parseASINs. provenance.go checks a libex
	// marker against it, so a marker can only vouch for a book the submission
	// actually identifies. Nil until an ASIN is parsed (lookups on a nil map are
	// fine), so a form with no ASIN field never mints a typed libex source.
	submissionASINs map[string]bool

	// queued counts the entries this submission wrote through the store. The
	// store's queue is not introspectable, and "nothing to write" is a verdict,
	// so the count is kept here.
	queued int
	// wrote is the pack files flush actually rewrote, data-relative.
	wrote    []string
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
// recaps) are CC BY-SA 4.0, every core template (work/recording/correction/
// import) is CC0-1.0. Go owns this classification (the schema enforces the same
// split structurally); the intake workflow reads Result.License for the pull-
// request body instead of re-deriving it in bash.
func licenseLayer(tmpl string) string {
	switch tmpl {
	case "characters", "recaps":
		return licenseBySALabel + " (community expressive layer)"
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
		asinRec: map[string]recRef{},
		isbnRec: map[string]recRef{},
	}
	store, err := openStore(opts.DataDir, tmpl)
	if err != nil {
		// needs-human, not invalid: the submission is fine, the repository is
		// mid-migration. StatusInvalid is the contributor-fault verdict, and the
		// intake workflow would tell a submitter THEIR form was wrong.
		return Result{Status: StatusNeedsHuman, Messages: []string{err.Error()}}
	}
	c.store = store
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
	if c.failed() {
		return Result{Status: c.status, Messages: c.messages}
	}
	if c.queued == 0 {
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
//
// It loads THROUGH the store, so the catalogue read and the submission's own
// entry reads share one walk and one parse of each pack.
func (c *composer) loadExisting() {
	res := check.LoadStore(c.store)
	// The normalized-identity index comes OFF the load (its own duplicate census
	// builds it), so the add-work gates probe the index this load already has instead
	// of cleaning every catalogued title a second time - see identityIndex, which
	// substitutes an empty index when a load produced none.
	c.identity = res.Identity
	cat := res.Catalog
	if cat == nil {
		return
	}
	for _, p := range cat.People {
		c.people[p.ID] = true
	}
	for _, w := range cat.Works {
		c.works[w.ID] = w
		for _, r := range w.Recordings {
			ref := recRef{Work: w.ID, Rec: r.ID}
			for _, a := range r.ASIN {
				c.asinRec[a.ASIN] = ref
			}
			for _, isbn := range r.ISBN {
				// Indexed by the ISBN value: a submitted identifier is a
				// duplicate of a recorded one whether or not the record
				// scopes it to a region.
				c.isbnRec[isbnKey(isbn.ISBN)] = ref
			}
		}
	}
	for _, s := range cat.Series {
		c.series[s.ID] = s
	}
}

// coreFamilies are the CC0 families every template may touch: a work, its
// recordings, the people they credit, and the series they sit in.
var coreFamilies = []pack.Family{pack.FamilyWorks, pack.FamilyPeople, pack.FamilySeries}

// writeFamilies are the families a template writes. Gating per template rather
// than on the union matters during the dual-layout window: works-community
// converts on its own schedule, and an add-work submission that never touches a
// sidecar must not be refused because that family is still legacy.
func writeFamilies(tmpl string) []pack.Family {
	switch tmpl {
	case "characters", "recaps":
		// The sidecar entry only; the compose path reads the work from the
		// catalogue but writes nothing outside works-community.
		return []pack.Family{pack.FamilyWorksCommunity}
	default:
		return coreFamilies
	}
}

// openStore opens dataDir for the families tmpl writes, refusing a legacy one
// before anything is composed (pack.OpenFor owns that rule). The clause it
// appends names who refused, since the wrapped error only says what is wrong
// with the tree.
func openStore(dataDir, tmpl string) (*pack.Store, error) {
	s, err := pack.OpenFor(dataDir, writeFamilies(tmpl)...)
	if err != nil {
		return nil, fmt.Errorf("%w (the intake bot writes the pack layout only)", err)
	}
	return s, nil
}

// putEntry marshals v and queues it as family f's entry for slug. It returns
// false and fails the run on a marshal error, so a caller writes
// `if !c.putEntry(...) { return }`.
func (c *composer) putEntry(f pack.Family, slug string, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		c.fail(StatusInvalid, "marshal %s entry %q: %v", f.Root(), slug, err)
		return false
	}
	if err := c.store.Upsert(f, slug, data); err != nil {
		c.fail(StatusInvalid, "queue %s entry %q: %v", f.Root(), slug, err)
		return false
	}
	c.queued++
	return true
}

// putNewEntry queues a record the submission is CREATING, refusing to write over
// an entry that is already there.
//
// The guard is not belt-and-braces: the dedup maps come from check.Load's
// Catalog, which is best-effort. An entry the loader could not decode is
// reported as a problem but is ABSENT from the Catalog, so the composer's
// duplicate check passes and the create path would replace the whole entry -
// for a work, destroying every recording it held - while the run reported ok,
// because what it wrote back is itself valid. The verdict is needs-human: the
// submission is fine, the repository is not.
func (c *composer) putNewEntry(f pack.Family, slug string, v any) bool {
	taken, err := c.store.Has(f, slug)
	if err != nil {
		c.fail(StatusInvalid, "read %s entry %q: %v", f.Root(), slug, err)
		return false
	}
	if taken {
		c.fail(StatusNeedsHuman, "an entry already exists at %s but is missing from the loaded catalogue; "+
			"a maintainer must run metacheck and fix what it reports before this can be composed",
			c.entryLocation(f, slug, ""))
		return false
	}
	return c.putEntry(f, slug, v)
}

// entryRaw reads family f's entry for slug into a mutable map, queued-write
// first. found is false when there is no such entry; ok is false when the run
// has already been failed (a read or decode error).
func (c *composer) entryRaw(f pack.Family, slug string) (obj map[string]any, found, ok bool) {
	raw, found, err := c.store.Get(f, slug)
	if err != nil {
		c.fail(StatusInvalid, "read %s entry %q: %v", f.Root(), slug, err)
		return nil, false, false
	}
	if !found {
		return nil, false, true
	}
	obj, err = pack.DecodeEntry(raw)
	if err != nil {
		c.fail(StatusInvalid, "parse %s entry %q: %v", f.Root(), slug, err)
		return nil, false, false
	}
	return obj, true, true
}

// entryLocation names where an entity lives: the pack file plus the entry key
// (and the recording key inside it). It is the MAINTAINER-facing address form,
// and every intake message is maintainer-facing - a verdict is read by whoever
// has to act on it, and acting means opening a file. The importer's per-row
// warnings use the entity form instead (see importer.recLabel), because a
// warning is a run report with nothing to open.
func (c *composer) entryLocation(f pack.Family, slug, recSlug string) string {
	loc := model.PackLocation{Family: f.Root(), Slug: slug, RecSlug: recSlug}
	ref, err := c.store.Locate(f, slug)
	if err != nil {
		// Only reachable for a family the store may not touch, where naming the
		// entry is still more use than naming nothing.
		return "entry " + slug
	}
	loc.Pack = ref.Path()
	return "data/" + loc.String()
}

// recLocation names a recording inside its work's composite entry.
func (c *composer) recLocation(ref recRef) string {
	return c.entryLocation(pack.FamilyWorks, ref.Work, ref.Rec)
}

// dedupIdentifiers fails when any of the submission's ASINs or ISBNs already
// resolves to a recording in the catalog, returning true so the caller stops
// before writing anything. asinHint is appended to the ASIN-duplicate message:
// the add-work path steers the submitter to the add-recording form,
// add-recording passes "".
//
// The verdict depends on the matched record's TRUST TIER (LICENSING.md). An
// ordinary duplicate is StatusDuplicate, as it always was. A duplicate of a
// record that is still bulk-mirror-only is StatusNeedsHuman instead: the
// submitter is a person attesting a book nobody has attested yet, so their data
// should take over the mirror seed - but the compose paths only ever CREATE
// records, so the bot cannot apply it mechanically and a maintainer does. That
// is the intake-side expression of the same rule the bulk importer applies
// automatically for a whole library export.
func (c *composer) dedupIdentifiers(asins []outASIN, isbns []model.ISBNRef, asinHint string) bool {
	for _, a := range asins {
		if ref, ok := c.asinRec[a.ASIN]; ok {
			c.failDuplicate(ref, "ASIN %s already exists (duplicate of %s)%s", a.ASIN, c.recLocation(ref), asinHint)
			return true
		}
	}
	// Keyed on the ISBN VALUE, so a submission that scopes its ISBN to a region
	// still collides with a recorded bare one: they are one identifier in two
	// spellings, and the index (loadExisting) reads the recorded side the same
	// way.
	for _, isbn := range isbns {
		if ref, ok := c.isbnRec[isbnKey(isbn.ISBN)]; ok {
			c.failDuplicate(ref, "ISBN %s already exists (duplicate of %s)", isbn.ISBN, c.recLocation(ref))
			return true
		}
	}
	return false
}

// isbnKey folds an ISBN to the form the dedup index compares on. NormalizeISBN
// (which both the index's source records and a typed form field have already
// been through) strips the separators an ISBN is printed with but leaves case
// alone, and the schema accepts an ISBN-10 check digit as either X or x - so
// without this a recorded 012345678X and a submitted 012345678x are two keys
// for one identifier. The submission then walks past every duplicate gate and
// is written, and metacheck's own case-insensitive rule (pkg/check normISBN)
// rejects the tree afterwards: the submitter gets a raw uniqueness violation
// instead of the duplicate-or-takeover verdict the gate exists to give them.
// Same fold, same reason, as the importer's claimISBNs.
func isbnKey(isbn string) string { return strings.ToUpper(isbn) }

// failDuplicate records the terminal verdict for a submission that collides with
// the catalogued recording at ref, choosing the status by that record's trust
// tier (see dedupIdentifiers). The duplicate message is the caller's; the
// bulk-mirror case replaces it with one that tells the submitter their data is
// wanted rather than that they duplicated something.
func (c *composer) failDuplicate(ref recRef, format string, args ...any) {
	if c.bulkMirrorOnly(ref) {
		c.failMirrorSeed(c.recLocation(ref))
		return
	}
	c.fail(StatusDuplicate, format, args...)
}

// failNoop is the verdict for a submission that states exactly what the record
// it addresses ALREADY says. It is a plain duplicate, with no tier lookup, and
// that is the whole point of its existing separately from failDuplicate.
//
// failDuplicate's tier branch answers "you are the first person to attest a
// record only the mirror has stated, so your data should REPLACE what is there,
// and the bot only composes new records" - true and useful when the collision is
// with a DIFFERENT record. Asked of the record the submitter is correcting, it
// is false twice over: nothing needs replacing (the fact is identical), and the
// correction path can rewrite a record, which is exactly what it does. Since the
// bulk-mirror seed is the dominant population, routing every such no-op to a
// maintainer with an untrue explanation would be the common case, not the edge.
func (c *composer) failNoop(format string, args ...any) {
	c.fail(StatusDuplicate, format+" - nothing to change", args...)
}

// failDuplicateWork is failDuplicate for the WORK-slug collision on the add-work
// form - the third duplicate gate, and the one that fires when a submission
// names a book the catalogue holds under a DIFFERENT edition (a fresh ASIN and a
// different narrator set pass the identifier and narrator gates). The tier
// question is the same one, asked of the work record rather than a recording: a
// work nobody but the mirror has ever stated is a takeover a maintainer applies,
// not a duplicate to close.
//
// A work the catalogue load could not decode is absent from c.works and reads as
// not-overwritable, the same safe answer bulkMirrorOnly gives.
func (c *composer) failDuplicateWork(workSlug, format string, args ...any) {
	if w := c.works[workSlug]; w != nil && model.BulkMirrorOnly(w.Sources) {
		c.failMirrorSeed(c.entryLocation(pack.FamilyWorks, workSlug, ""))
		return
	}
	c.fail(StatusDuplicate, format, args...)
}

// failMirrorSeed is the verdict every duplicate gate shares for a record that is
// still nothing but a bulk-mirror seed: the submitter's data is wanted, and the
// message says so rather than telling them they duplicated something.
func (c *composer) failMirrorSeed(location string) {
	c.fail(StatusNeedsHuman, "%s was seeded from the libex mirror and no user has attested it yet, "+
		"so your submission should replace what is recorded there - a maintainer will apply it "+
		"(the intake bot only composes new records, it cannot rewrite one)", location)
}

// bulkMirrorOnly reports whether the catalogued recording at ref is still
// nothing but a bulk-mirror seed. It is the intake side's only tier test, and it
// asks pkg/model rather than comparing source-type strings of its own.
//
// A recording the catalogue load could not decode is absent from c.works and
// reads as false: not overwritable, which is the safe answer for a record whose
// provenance we cannot see.
func (c *composer) bulkMirrorOnly(ref recRef) bool {
	w := c.works[ref.Work]
	if w == nil {
		return false
	}
	for _, r := range w.Recordings {
		if r.ID == ref.Rec {
			return model.BulkMirrorOnly(r.Sources)
		}
	}
	return false
}

// flush writes every queued entry, performing any due pack or directory splits.
// Returns a non-empty error message on an I/O failure.
func (c *composer) flush() string {
	w, err := c.store.Flush()
	if err != nil {
		return fmt.Sprintf("write packs: %v", err)
	}
	c.wrote = w.Wrote
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

// fileList returns the pack files flush rewrote, data/-prefixed and sorted. It
// is what the intake workflow commits, so it names FILES rather than entries -
// one pack usually carries several of a submission's records.
func (c *composer) fileList() []string {
	out := make([]string, 0, len(c.wrote))
	for _, rel := range c.wrote {
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

// failed reports whether a terminal status has already been set, so a compose
// path can stop rather than build on a half-composed submission. It is not
// specific to one status: a person record that could not be created fails
// needs-human, and the work that would have credited it must not be written
// either.
func (c *composer) failed() bool {
	return c.status != "" && c.status != StatusOK
}

// note appends an informational message without changing the status.
func (c *composer) note(format string, args ...any) {
	c.messages = append(c.messages, fmt.Sprintf(format, args...))
}

// source builds the `user` provenance stamp for a composed record from the
// form's free-text sources/evidence field. Composition paths call sources()
// (provenance.go) instead, which adds a typed entry when the field carries a
// machine-written lookup marker.
func (c *composer) source(ref string) outSource {
	return outSource{Type: sourceUser, Ref: strings.TrimSpace(ref), ImportedAt: c.date}
}

// getOrCreatePerson returns the slug for a person name, emitting a new person
// record when the slug is not already in the catalog or this run.
//
// The id comes from model.PersonSlug - the same call the bulk importer mints
// with and pkg/check verifies against - rather than from a bare Slugify, so a
// name whose slug is an API route literal lands on the one variant both rules
// accept ("Search" -> search-person). fellBack is the empty-slug case, which
// this path still REFUSES rather than folding onto the shared catch-all: a form
// names one person, and the submitter can respell them.
func (c *composer) getOrCreatePerson(name, sourceRef string) (string, bool) {
	slug, fellBack := model.PersonSlug(name)
	if fellBack {
		c.fail(StatusInvalid, "name %q produced an empty slug", name)
		return "", false
	}
	if c.people[slug] {
		return slug, true
	}
	c.people[slug] = true
	if !c.putNewEntry(pack.FamilyPeople, slug, outPerson{
		ID: slug, Name: strings.TrimSpace(name), License: licenseCC0, Sources: c.sources(sourceRef),
	}) {
		return "", false
	}
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
