// Package importer converts external audiobook-library exports into
// audiosilo-meta records on disk. It maps one OpenAudible books.json entry to a
// work + recording (+ people, + series), deduplicating against the existing
// catalog so a contributor's upload lands as a reviewable diff. Only factual
// fields are imported (see LICENSING.md); publisher copy and covers-as-files are
// never touched.
package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/canonical"
	"github.com/kodestar/audiosilo-meta/internal/check"
	"github.com/kodestar/audiosilo-meta/internal/model"
)

var (
	asinPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	datePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)
)

// recInfo remembers enough about a recording under a work to detect a
// same-identity re-import (idempotency) versus a genuine slug collision.
type recInfo struct {
	narrators map[string]bool
}

// workState tracks a work's identity (slug + author set) and its recordings.
type workState struct {
	slug    string
	authors map[string]bool
	recs    map[string]*recInfo
}

// seriesState tracks a series' membership so works dedupe and positions never
// collide. Existing series carry their full raw JSON so extending one preserves
// every field the importer does not manage.
type seriesState struct {
	slug      string
	name      string
	path      string
	isNew     bool
	dirty     bool
	out       *OutSeries        // populated for a newly created series
	raw       map[string]any    // populated lazily for an existing series
	members   map[string]string // work slug -> position
	positions map[string]string // position -> work slug
}

// planner accumulates the writes and warnings for a run.
type planner struct {
	dataDir string
	// people is the set of known person slugs. The slug IS the normalized
	// identity: two names that slug the same are the same person.
	people    map[string]bool
	works     map[string]*workState
	series    map[string]*seriesState
	asins     map[string]bool
	writes    map[string][]byte
	curSource OutSource
	fatal     error
	summary   Summary
}

// Run imports booksPath (an OpenAudible export) into opts.DataDir. On a dry run
// it only computes the plan. On a real run it writes the new/changed files and
// then validates the whole tree, returning an error if the post-write check
// fails. The Summary is always returned so the caller can print the plan.
func Run(booksPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(booksPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", booksPath, err)
	}
	books, err := parseOpenAudible(raw)
	if err != nil {
		return Summary{}, err
	}
	return runBooks(books, sourceOpenAud, opts)
}

// sourceBook is the parsed, source-independent view of one export entry. raw
// carries only the shared-key passthrough fields the planner reads directly
// (asin, title, title_short, author, narrated_by, language, region,
// release_date, publisher, image_url, and OpenAudible's chapters array); any
// fact a source derives differently at parse time is promoted to a typed field
// here, never smuggled through raw. Invariant: every seriesRef carries a
// non-empty name (the parsers skip empties and never emit one).
type sourceBook struct {
	raw        rawBook
	series     []seriesRef // the book's series claims (>1 only for Libation)
	runtimeMin int         // whole minutes; 0 = unknown
	abridged   *bool       // tri-state: nil = the source did not state it
}

// str is a convenience passthrough to the underlying raw entry.
func (s sourceBook) str(key string) string { return s.raw.str(key) }

// primarySeriesClaim returns the book's first fully-valid series claim (a name
// with a valid position), for the work-title disambiguation pre-pass.
func (s sourceBook) primarySeriesClaim() (name, pos string, ok bool) {
	for _, r := range s.series {
		if r.seqOK {
			return r.name, r.seq, true
		}
	}
	return "", "", false
}

// RunLibation imports exportPath (a Libation "Export Library" JSON export) into
// opts.DataDir. Each Libation entry is normalized into the same internal
// sourceBook the OpenAudible path produces (factual fields only; see
// libation.go), so the two sources share every mapping/dedup rule. Behaviour is
// otherwise identical to Run.
func RunLibation(exportPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(exportPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	books, err := parseLibation(raw)
	if err != nil {
		return Summary{}, err
	}
	return runBooks(books, sourceLibation, opts)
}

// runBooks is the shared import core: it plans every book into records against
// the existing catalog, then (on a real run) writes and re-validates the tree.
// sourceType is the provenance stamped on every created record.
func runBooks(books []sourceBook, sourceType string, opts Options) (Summary, error) {
	p := &planner{
		dataDir: opts.DataDir,
		people:  map[string]bool{},
		works:   map[string]*workState{},
		series:  map[string]*seriesState{},
		asins:   map[string]bool{},
		writes:  map[string][]byte{},
	}
	p.loadExisting()

	titles := resolveWorkTitles(books)
	for i, b := range books {
		p.curSource = OutSource{Type: sourceType, Ref: NormalizeASIN(b.str("asin")), ImportedAt: opts.ImportDate}
		p.addBook(b, titles[i])
		if p.fatal != nil {
			return p.summary, p.fatal
		}
	}
	p.finalizeSeries()
	if p.fatal != nil {
		return p.summary, p.fatal
	}

	if opts.DryRun {
		return p.summary, nil
	}

	if err := p.flush(); err != nil {
		return p.summary, err
	}
	if res := check.Load(opts.DataDir); !res.OK() {
		return p.summary, fmt.Errorf("post-import validation failed:\n%s", problemLines(res.Problems))
	}
	return p.summary, nil
}

// loadExisting seeds the planner's identity maps from the current data tree so
// new records dedupe against what is already committed.
func (p *planner) loadExisting() {
	cat := check.Load(p.dataDir).Catalog
	if cat == nil {
		return
	}
	for _, person := range cat.People {
		p.people[person.ID] = true
	}
	for _, w := range cat.Works {
		ws := &workState{slug: w.ID, authors: ToSet(w.Authors), recs: map[string]*recInfo{}}
		for _, r := range w.Recordings {
			ws.recs[r.ID] = &recInfo{narrators: ToSet(r.Narrators)}
			for _, a := range r.ASIN {
				p.asins[a.ASIN] = true
			}
		}
		p.works[w.ID] = ws
	}
	for _, s := range cat.Series {
		ss := &seriesState{
			slug:      s.ID,
			name:      s.Name,
			path:      filepath.Join("series", model.Shard(s.ID), s.ID+".json"),
			members:   map[string]string{},
			positions: map[string]string{},
		}
		for _, sw := range s.Works {
			ss.members[sw.Work] = sw.Position
			ss.positions[sw.Position] = sw.Work
		}
		p.series[s.ID] = ss
	}
}

// resolveWorkTitles is the deterministic pre-pass over the parsed batch that
// picks each book's work title. The default is title_short (falling back to
// title). But series where every volume shares title_short ("Dragon Heart" for
// volumes whose full titles are "Dragon Heart - Book 10: Land of War", ...)
// would collapse into one work - so books are grouped by title slug ONLY (not
// by author set: Audible's author field varies per volume, listing extra
// translator/introduction credits on some, which would let a volume escape the
// group and squat the bare slug), and when a group carries more than one
// distinct (series, position) claim, EVERY book in the group derives its work
// title from the full title field verbatim, so the incumbent volume does not
// squat the ambiguous slug either. Renaming to full titles is harmless even
// when the group spans genuinely different books - full titles are still
// correct titles - and single-claim groups are never touched.
func resolveWorkTitles(books []sourceBook) []string {
	titles := make([]string, len(books))
	groups := map[string][]int{}
	for i, b := range books {
		titles[i] = firstNonEmpty(b.str("title_short"), b.str("title"))
		key := Slugify(titles[i])
		groups[key] = append(groups[key], i)
	}
	for _, idxs := range groups {
		claims := map[string]bool{}
		for _, i := range idxs {
			name, pos, ok := books[i].primarySeriesClaim()
			if !ok {
				continue
			}
			claims[strings.ToLower(name)+"\x00"+pos] = true
		}
		if len(claims) < 2 {
			continue
		}
		for _, i := range idxs {
			if full := books[i].str("title"); full != "" {
				titles[i] = full
			}
		}
	}
	return titles
}

// addBook maps one export entry to records. workTitle is the pre-pass-resolved
// title for the book's work. It returns quietly (recording a warning or a skip)
// whenever the entry cannot be imported cleanly.
func (p *planner) addBook(b sourceBook, workTitle string) {
	label := bookLabel(b)
	warn := func(format string, args ...any) {
		p.summary.Warnings = append(p.summary.Warnings, label+": "+fmt.Sprintf(format, args...))
	}

	asin := NormalizeASIN(b.str("asin"))

	// Dedup first: an already-present ASIN is a skip, not a warning.
	if asin != "" && p.asins[asin] {
		p.summary.Skipped++
		return
	}

	lang, ok := mapLanguage(b.str("language"))
	if !ok {
		warn("unknown language %q; skipped", b.str("language"))
		return
	}
	narratorNames := SplitNames(b.str("narrated_by"))
	if len(narratorNames) == 0 {
		warn("no narrator; a recording requires narrators; skipped")
		return
	}
	authorNames := SplitNames(b.str("author"))
	if len(authorNames) == 0 {
		warn("no author; a work requires an author; skipped")
		return
	}

	if workTitle == "" {
		warn("no title; skipped")
		return
	}

	authorSlugs := make([]string, 0, len(authorNames))
	for _, name := range authorNames {
		authorSlugs = append(authorSlugs, p.getOrCreatePerson(name, warn))
	}
	narratorSlugs := make([]string, 0, len(narratorNames))
	for _, name := range narratorNames {
		narratorSlugs = append(narratorSlugs, p.getOrCreatePerson(name, warn))
	}

	// The book's series claims (one for OpenAudible, possibly several for
	// Libation). The first that resolves to an already-known series (on disk or
	// created earlier this run) is used to refuse merging into a same-titled work
	// that sits in that series at a different position.
	var claim *seriesClaim
	for _, r := range b.series {
		if !r.seqOK {
			continue
		}
		if ss := p.findSeries(r.name); ss != nil {
			claim = &seriesClaim{ss: ss, pos: r.seq}
			break
		}
	}

	ws := p.getOrCreateWork(workTitle, b.str("title"), authorSlugs, lang, claim, warn)
	p.addRecording(ws, b, asin, lang, narratorSlugs, warn)

	if asin != "" {
		p.asins[asin] = true
	}

	for _, r := range b.series {
		if !r.seqOK {
			warn("series %q: missing or invalid position %q; not placed in series", r.name, r.rawSeq)
		} else {
			p.addToSeries(r.name, ws.slug, r.seq, warn)
		}
	}
}

// seriesRef is a book's claim to a position in a named series. name is always
// non-empty (the sourceBook invariant). seqOK reports whether seq passed
// position validation; rawSeq is the original text (for the "invalid position"
// warning). A book may carry several (Libation multi-series).
type seriesRef struct {
	name   string
	seq    string
	seqOK  bool
	rawSeq string
}

// getOrCreatePerson returns the slug for name, creating the person record when
// it is new. The slug is the normalized identity: "B.V. Larson", "B. V. Larson"
// and "Ramón De Ocampo"/"Ramon de Ocampo" all slug the same, so they are the
// same person - the first record (existing catalog first, then batch order)
// wins and keeps its name; spelling variants never fork a numbered duplicate.
func (p *planner) getOrCreatePerson(name string, warn func(string, ...any)) string {
	slug := Slugify(name)
	if slug == "" {
		slug = "person"
		warn("name %q produced an empty slug; using %q", name, slug)
	}
	if p.people[slug] {
		return slug
	}
	p.people[slug] = true
	p.emit(filepath.Join("people", model.Shard(slug), slug+".json"), OutPerson{
		ID: slug, Name: name, License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewPeople++
	return slug
}

// seriesClaim is a book's claim to a position in an already-known series.
type seriesClaim struct {
	ss  *seriesState
	pos string
}

// compatible reports whether merging the book into work ws is consistent with
// its series claim. No claim, a work not yet in the series, or the same
// position all merge; the same series at a DIFFERENT position means ws is a
// different volume that merely shares the title.
func (c *seriesClaim) compatible(ws *workState) bool {
	if c == nil {
		return true
	}
	existing, in := c.ss.members[ws.slug]
	return !in || existing == c.pos
}

// getOrCreateWork returns the work identified by (title-slug, author set),
// creating it when new. A same-author work that the book's series claim rules
// out (same series, different position) is not a merge target: the slug is
// re-derived from the full title, with the candidate chain (author suffix,
// then numeric) only as the last-resort collision fallback. A collision with a
// different author set appends the first author's slug, then numeric suffixes,
// and warns.
func (p *planner) getOrCreateWork(title, fullTitle string, authorSlugs []string, lang string, claim *seriesClaim, warn func(string, ...any)) *workState {
	base := Slugify(title)
	if base == "" {
		base = "untitled"
		warn("title %q produced an empty slug; using %q", title, base)
	}
	want := ToSet(authorSlugs)
	for _, slug := range workCandidates(base, authorSlugs[0]) {
		ws, exists := p.works[slug]
		if !exists {
			if slug != base {
				warn("work slug %q taken by a different book; using %q for %q", base, slug, title)
			}
			ws = &workState{slug: slug, authors: want, recs: map[string]*recInfo{}}
			p.works[slug] = ws
			p.emit(filepath.Join("works", model.Shard(slug), slug, "work.json"), outWork{
				ID: slug, Title: title, Authors: authorSlugs, Language: lang,
				License: licenseCC0, Sources: []OutSource{p.curSource},
			})
			p.summary.NewWorks++
			return ws
		}
		if SameSet(ws.authors, want) {
			if claim.compatible(ws) {
				return ws
			}
			// Same authors, but this slug's work sits in the book's series at a
			// different position: a different volume sharing the short title.
			// Re-derive from the full title (once); the candidate chain below is
			// the last resort when that is unusable.
			if full := Slugify(fullTitle); fullTitle != title && full != "" && full != base {
				return p.getOrCreateWork(fullTitle, "", authorSlugs, lang, claim, warn)
			}
		}
	}
	// Unreachable: workCandidates yields an unbounded numeric tail.
	return nil
}

// findSeries returns the already-known series (existing on disk or created this
// run) that name resolves to, or nil - it never creates. It walks the same
// candidate chain as getOrCreateSeries so both resolve a name identically.
func (p *planner) findSeries(name string) *seriesState {
	base := Slugify(name)
	if base == "" {
		base = "series"
	}
	for i := 0; ; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		ss, exists := p.series[slug]
		if !exists {
			return nil
		}
		if strings.EqualFold(ss.name, name) {
			return ss
		}
	}
}

// addRecording builds and emits the recording for a book under work ws, unless
// an identical recording already exists there (a re-import no-op).
func (p *planner) addRecording(ws *workState, b sourceBook, asin, lang string, narratorSlugs []string, warn func(string, ...any)) {
	base := narratorSlugs[0]
	if year := YearOf(b.str("release_date")); year != "" {
		base += "-" + year
	}
	if base == "" {
		base = "unknown-narrator"
	}
	slug, present := uniqueRecSlug(ws, base, ToSet(narratorSlugs))
	if present {
		return // identical recording already imported
	}

	rec := outRecording{
		ID: slug, Work: ws.slug, Narrators: narratorSlugs, Language: lang,
		License: licenseCC0, Sources: []OutSource{p.curSource},
	}
	rec.Abridged = b.abridged
	if b.runtimeMin > 0 {
		rec.RuntimeMin = b.runtimeMin
	}
	if rd := b.str("release_date"); datePattern.MatchString(rd) {
		rec.ReleaseDate = rd
	}
	if pub := b.str("publisher"); pub != "" {
		rec.Publisher = pub
	}
	if img := b.str("image_url"); strings.HasPrefix(img, "https://") {
		rec.CoverURL = img
	}
	if asin != "" {
		if region, ok := mapRegion(b.str("region")); ok {
			rec.ASIN = []OutASIN{{Region: region, ASIN: asin}}
		} else {
			warn("region %q is not a known marketplace; ASIN not recorded", b.str("region"))
		}
	}
	if chs := buildChapters(b.raw, warn); chs != nil {
		rec.Chapters = chs
	}

	ws.recs[slug] = &recInfo{narrators: ToSet(narratorSlugs)}
	p.emit(filepath.Join("works", model.Shard(ws.slug), ws.slug, "recordings", slug+".json"), rec)
	p.summary.NewRecordings++
}

// addToSeries places work at position pos in the named series, creating the
// series when new. Duplicate memberships and position clashes warn and leave the
// existing entry.
func (p *planner) addToSeries(name, work, pos string, warn func(string, ...any)) {
	// Defense in depth: the parsers uphold the non-empty-name invariant, but a
	// future source (or a direct caller) must never mint a nameless series.
	if name == "" {
		warn("empty series name; not placed in series")
		return
	}
	ss := p.getOrCreateSeries(name, warn)
	if existing, ok := ss.members[work]; ok {
		if existing != pos {
			warn("series %q already lists work %q at position %q; not re-adding at %q", name, work, existing, pos)
		}
		return
	}
	if other, ok := ss.positions[pos]; ok && other != work {
		warn("series %q position %q already taken by %q; %q not added", name, pos, other, work)
		return
	}
	ss.members[work] = pos
	ss.positions[pos] = work
	ss.dirty = true
	if ss.isNew {
		ss.out.Works = append(ss.out.Works, OutSeriesWork{Work: work, Position: pos})
	} else {
		p.loadSeriesRaw(ss)
		works, _ := ss.raw["works"].([]any)
		ss.raw["works"] = append(works, map[string]any{"work": work, "position": pos})
	}
}

// getOrCreateSeries returns the series for name, creating an in-memory record
// when new. Numeric suffixes resolve a collision with a differently-named series.
func (p *planner) getOrCreateSeries(name string, warn func(string, ...any)) *seriesState {
	base := Slugify(name)
	if base == "" {
		base = "series"
		warn("series name %q produced an empty slug; using %q", name, base)
	}
	for i := 0; ; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		ss, exists := p.series[slug]
		if !exists {
			if slug != base {
				warn("series slug %q taken by a different series; using %q for %q", base, slug, name)
			}
			ss = &seriesState{
				slug:      slug,
				name:      name,
				path:      filepath.Join("series", model.Shard(slug), slug+".json"),
				isNew:     true,
				out:       &OutSeries{ID: slug, Name: name, License: licenseCC0, Sources: []OutSource{p.curSource}},
				members:   map[string]string{},
				positions: map[string]string{},
			}
			p.series[slug] = ss
			p.summary.NewSeries++
			return ss
		}
		if strings.EqualFold(ss.name, name) {
			return ss
		}
	}
}

// loadSeriesRaw reads an existing series file into ss.raw the first time it is
// extended, so its non-managed fields (authors, xref, existing sources) survive.
func (p *planner) loadSeriesRaw(ss *seriesState) {
	if ss.raw != nil || p.fatal != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(p.dataDir, ss.path))
	if err != nil {
		p.fatal = fmt.Errorf("read series %s: %w", ss.path, err)
		return
	}
	if err := json.Unmarshal(data, &ss.raw); err != nil {
		p.fatal = fmt.Errorf("parse series %s: %w", ss.path, err)
	}
}

// finalizeSeries queues the JSON for every new or extended series.
func (p *planner) finalizeSeries() {
	for _, ss := range p.series {
		if !ss.dirty {
			continue
		}
		if ss.isNew {
			p.emit(ss.path, ss.out)
		} else {
			p.emitRaw(ss.path, ss.raw)
		}
	}
}

// emit canonicalizes v and queues it for writing at rel (a data-relative path).
func (p *planner) emit(rel string, v any) {
	if p.fatal != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		p.fatal = fmt.Errorf("marshal %s: %w", rel, err)
		return
	}
	p.emitRaw(rel, json.RawMessage(data))
}

func (p *planner) emitRaw(rel string, v any) {
	if p.fatal != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		p.fatal = fmt.Errorf("marshal %s: %w", rel, err)
		return
	}
	formatted, err := canonical.Format(data)
	if err != nil {
		p.fatal = fmt.Errorf("canonicalize %s: %w", rel, err)
		return
	}
	p.writes[filepath.ToSlash(rel)] = formatted
}

// flush writes every queued file to disk under the data dir, creating parent
// directories. Paths are written in sorted order for a deterministic run.
func (p *planner) flush() error {
	rels := make([]string, 0, len(p.writes))
	for rel := range p.writes {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		full := filepath.Join(p.dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", rel, err)
		}
		if err := os.WriteFile(full, p.writes[rel], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// buildChapters maps a book's chapters, trimming titles and enforcing the same
// monotonic-from-zero rule metacheck applies. On any structural violation it
// warns and returns nil (the recording is emitted without chapters).
func buildChapters(b rawBook, warn func(string, ...any)) []outChapter {
	raw := b.chapters()
	if len(raw) == 0 {
		return nil
	}
	out := make([]outChapter, 0, len(raw))
	for i, rc := range raw {
		start, sOK := rc.intVal("start_offset_ms")
		length, lOK := rc.intVal("length_ms")
		if !sOK || !lOK || length <= 0 || start < 0 {
			warn("chapter %d has invalid offsets; chapters omitted", i+1)
			return nil
		}
		title := strings.TrimSpace(rc.str("title"))
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		out = append(out, outChapter{Title: title, StartMS: start, LengthMS: length})
	}
	if out[0].StartMS != 0 {
		warn("chapters do not start at 0; chapters omitted")
		return nil
	}
	for i := 1; i < len(out); i++ {
		if out[i].StartMS <= out[i-1].StartMS {
			warn("chapter offsets are not strictly increasing; chapters omitted")
			return nil
		}
	}
	return out
}

// uniqueRecSlug returns a free recording slug under ws for base, and whether an
// identical recording (same narrator set) already exists there.
func uniqueRecSlug(ws *workState, base string, narrators map[string]bool) (slug string, present bool) {
	for i := 0; ; i++ {
		slug = base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		existing, ok := ws.recs[slug]
		if !ok {
			return slug, false
		}
		if SameSet(existing.narrators, narrators) {
			return slug, true
		}
	}
}

// workCandidates yields the ordered slug candidates for a work: the bare title
// slug, then the title plus first-author slug, then numeric suffixes on that.
func workCandidates(base, firstAuthor string) []string {
	withAuthor := base + "-" + firstAuthor
	out := []string{base, withAuthor}
	for i := 2; i <= 50; i++ {
		out = append(out, fmt.Sprintf("%s-%d", withAuthor, i))
	}
	return out
}

func NormalizeASIN(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if asinPattern.MatchString(s) {
		return s
	}
	return ""
}

func bookLabel(b sourceBook) string {
	if a := strings.TrimSpace(b.str("asin")); a != "" {
		return a
	}
	if t := firstNonEmpty(b.str("title_short"), b.str("title")); t != "" {
		return t
	}
	return "(unknown book)"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ToSet builds a set from a string slice. Shared with internal/issueform so a
// form submission dedupes narrator/author sets exactly like a bulk import.
func ToSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// SameSet reports whether two string sets have identical membership.
func SameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func problemLines(ps []check.Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}
