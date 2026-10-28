package importer

// librivox.go is the LibriVox adapter: a snapshot of LibriVox's audiobooks feed
// (librivoxfetch.go writes one) becomes sourceBooks for the shared runBooks
// planner, under LICENSING.md's public-domain exception.
//
// What a LibriVox row is, against the planner's other sources:
//
//   - Its identity is the PROJECT ID (recording.librivox_id), never its title and
//     never an ASIN (it has none). A row whose id the catalogue already holds is
//     a skip (dedupeByLibrivox), and a row is always a recording of its own: two
//     projects are two productions, so addRecording never merges one into a
//     same-narrator sibling.
//   - Its WORK is found by librivoxmatch.go's rule (routeLibrivox): the
//     planner's ordinary identity walk (title chain, identity author set,
//     language) first, then the one work by the same authors whose title differs
//     only by a leading article or punctuation; an ambiguous match is a skip on
//     the run's worklist, and otherwise the row creates a work. LibriVox's own
//     repeat marker, a trailing "(version 2)", is stripped first: it numbers
//     LibriVox projects of one text, not works. A non-Latin title (it slugs to
//     nothing, or most of its letters are not Latin script) never walks a slug
//     chain: its new work is created at `librivox-<project id>`.
//   - Its PEOPLE are keyed by LibriVox id. Before any census reads a name, a
//     reader or author id the catalogue already holds is spelled the way that
//     person is catalogued (keyLibrivoxCredits), so a reader renamed on
//     LibriVox, or reread from a later snapshot, never mints a second record;
//     after planning, the ids of the rows that landed are stamped onto the
//     people they resolved to (stampLibrivoxPeople). A READER id is a person of
//     its own even when its name is not: a reader whose name's slug another
//     person already holds under a different reader id is minted at
//     model.LibrivoxReaderSlug (`martina-librivox-634`) instead of being folded
//     onto them (librivoxReaderSlug), so two LibriVox accounts called "Martina"
//     are two people and the first holder keeps the plain slug.
//   - Sections are chapters: start_ms cumulative from each section's playtime,
//     the section's archive.org file as the chapter's audio_url. One section
//     without a playtime makes the timeline unknowable past it, so the whole
//     chapter list is omitted (buildChapters' all-or-nothing rule).
//   - Facts only (LICENSING.md): no description is read at all.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// LibrivoxPublisher is the publisher of record every LibriVox recording carries:
// LibriVox produces and releases the recording, which is a fact about every
// project in its catalogue.
const LibrivoxPublisher = "LibriVox"

// lvInt is an integer LibriVox states either as a JSON number or as a string of
// digits (its feed does both: "id":"527" beside "totaltimesecs":9265). Anything
// else reads as 0, which every caller treats as "not stated".
type lvInt int

func (n *lvInt) UnmarshalJSON(b []byte) error {
	v, err := strconv.Atoi(strings.TrimSpace(strings.Trim(strings.TrimSpace(string(b)), `"`)))
	if err != nil {
		v = 0
	}
	*n = lvInt(v)
	return nil
}

// lvText is a free-text field LibriVox may state as a JSON string or, as it
// does with the integers lvInt reads, as a bare number ("copyright_year": 1915):
// either reads as its text, and null or any other value as "" (not stated), so
// one oddly typed year never fails the decode of a whole page.
type lvText string

func (t *lvText) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = lvText(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*t = lvText(n.String())
		return nil
	}
	*t = ""
	return nil
}

// LibrivoxPerson is an author or translator as LibriVox's feed states one. DOB
// and DOD are free text there ("1883", "alive", ""); librivoxYear reads the ones
// that are years.
type LibrivoxPerson struct {
	ID        lvInt  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	DOB       lvText `json:"dob"`
	DOD       lvText `json:"dod"`
}

// name is the person's display name: first and last joined, trimmed (the
// pseudo-author "Various" has an empty first name).
func (a LibrivoxPerson) name() string {
	return strings.TrimSpace(strings.TrimSpace(a.FirstName) + " " + strings.TrimSpace(a.LastName))
}

// LibrivoxReader is one reader of a section.
type LibrivoxReader struct {
	ReaderID    lvInt  `json:"reader_id"`
	DisplayName string `json:"display_name"`
}

// LibrivoxSection is one audio file of a project.
type LibrivoxSection struct {
	SectionNumber lvInt            `json:"section_number"`
	Title         string           `json:"title"`
	ListenURL     string           `json:"listen_url"`
	Playtime      lvInt            `json:"playtime"`
	Readers       []LibrivoxReader `json:"readers"`
}

// LibrivoxGenre is one genre LibriVox files a project under.
type LibrivoxGenre struct {
	Name string `json:"name"`
}

// LibrivoxProject is one entry of LibriVox's audiobooks feed (extended=1,
// coverart=1), the fields this importer reads and nothing else: in particular
// not `description`, which LICENSING.md keeps out.
type LibrivoxProject struct {
	ID            lvInt             `json:"id"`
	Title         string            `json:"title"`
	URLTextSource string            `json:"url_text_source"`
	Language      string            `json:"language"`
	CopyrightYear lvText            `json:"copyright_year"`
	URLRSS        string            `json:"url_rss"`
	URLZipFile    string            `json:"url_zip_file"`
	URLLibrivox   string            `json:"url_librivox"`
	URLIArchive   string            `json:"url_iarchive"`
	TotalTimeSecs lvInt             `json:"totaltimesecs"`
	CoverArtJPG   string            `json:"coverart_jpg"`
	Authors       []LibrivoxPerson  `json:"authors"`
	Translators   []LibrivoxPerson  `json:"translators"`
	Genres        []LibrivoxGenre   `json:"genres"`
	Sections      []LibrivoxSection `json:"sections"`
}

// librivoxPage is one response body of the feed: {"books": [...]}. Books is a
// pointer so one decode tells a missing key from an empty list.
type librivoxPage struct {
	Books *[]LibrivoxProject `json:"books"`
}

// parseLibrivoxPage decodes one response body. A body with no "books" key is an
// error: the feed answers "no results" with {"error": ...} and a 404, which the
// fetcher reads as the end of the catalogue and never stores as a page.
func parseLibrivoxPage(body []byte) ([]LibrivoxProject, error) {
	var page librivoxPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("not a LibriVox feed page: %w", err)
	}
	if page.Books == nil {
		return nil, fmt.Errorf("not a LibriVox feed page: no \"books\" key")
	}
	return *page.Books, nil
}

// The snapshot layout (written by LibrivoxFetcher.Fetch, read here):
//
//	<snapshot>/manifest.json            the fetch's resume state (librivoxManifest)
//	<snapshot>/pages/<name>.json        one feed response body each, verbatim
//
// The reader needs only pages/: every *.json file under it is a {"books": [...]}
// body, read in name order. A project id seen twice keeps its LAST copy (a
// later page is a later reading), which never happens in a fetch that ran to
// completion but makes a hand-assembled snapshot unambiguous.
const (
	librivoxPagesDir     = "pages"
	librivoxManifestFile = "manifest.json"
)

// ReadLibrivoxSnapshot reads every project a snapshot holds, sorted by id.
func ReadLibrivoxSnapshot(dir string) ([]LibrivoxProject, error) {
	return readLibrivoxSnapshot(dir, nil)
}

// readLibrivoxSnapshot is ReadLibrivoxSnapshot keeping only the projects keep
// selects (every project when keep is nil), so a tranche import holds only its
// own projects rather than the whole catalogue.
func readLibrivoxSnapshot(dir string, keep func(id int) bool) ([]LibrivoxProject, error) {
	pagesDir := filepath.Join(dir, librivoxPagesDir)
	entries, err := os.ReadDir(pagesDir)
	if err != nil {
		return nil, fmt.Errorf("read LibriVox snapshot %s: %w", dir, err)
	}
	byID := map[int]LibrivoxProject{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(pagesDir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		books, err := parseLibrivoxPage(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, b := range books {
			if keep == nil || keep(int(b.ID)) {
				byID[int(b.ID)] = b
			}
		}
	}
	out := make([]LibrivoxProject, 0, len(byID))
	for _, b := range byID {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LibrivoxIDs is a set of project-id ranges, the `--ids` selection an import
// takes: "527", "1-2000", "5000-" (open-ended), or a comma-separated union.
type LibrivoxIDs struct {
	spans [][2]int // inclusive; hi 0 = open-ended
}

// ParseLibrivoxIDs parses an --ids value.
func ParseLibrivoxIDs(s string) (LibrivoxIDs, error) {
	var ids LibrivoxIDs
	if strings.TrimSpace(s) == "" {
		return ids, fmt.Errorf("--ids is empty; name a project id or range (527, 1-2000, 5000-)")
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		l, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil || l < 1 {
			return LibrivoxIDs{}, fmt.Errorf("--ids %q: %q is not a project id or range", s, part)
		}
		h := l
		if isRange {
			if strings.TrimSpace(hi) == "" {
				h = 0
			} else if h, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil || h < l {
				return LibrivoxIDs{}, fmt.Errorf("--ids %q: %q is not a project id or range", s, part)
			}
		}
		ids.spans = append(ids.spans, [2]int{l, h})
	}
	return ids, nil
}

// Has reports whether id is selected.
func (s LibrivoxIDs) Has(id int) bool {
	for _, sp := range s.spans {
		if id >= sp[0] && (sp[1] == 0 || id <= sp[1]) {
			return true
		}
	}
	return false
}

// RunLibrivox imports the projects of the snapshot at snapshotDir whose ids are
// selected into opts.DataDir, through the shared planner. Only the create mode
// exists for this source.
func RunLibrivox(snapshotDir string, ids LibrivoxIDs, opts Options) (Summary, error) {
	if opts.Mode != ModeCreate {
		return Summary{}, fmt.Errorf("a LibriVox import creates only; it has no %s mode", opts.Mode.runName())
	}
	// ParseLibrivoxIDs admits no id below 1, so every selected project has one.
	projects, err := readLibrivoxSnapshot(snapshotDir, ids.Has)
	if err != nil {
		return Summary{}, err
	}
	books := make([]sourceBook, 0, len(projects))
	for _, pr := range projects {
		books = append(books, librivoxBook(pr))
	}
	sum, err := runBooks(books, sourceLibrivox, opts, nil, func(p *planner) {
		p.genres = librivoxGenreTable()
	})
	sum.LibrivoxProjects = len(books)
	return sum, err
}

// librivoxVersionRE is LibriVox's repeat-recording marker: a trailing
// "(version 2)" numbering the second project of one text. It names a project,
// not a work, so it never reaches the work identity.
var librivoxVersionRE = regexp.MustCompile(`(?i)\s*\(\s*version\s+\d+\s*\)\s*$`)

// librivoxTitle is a project's work title: LibriVox's own, its version marker
// stripped.
func librivoxTitle(t string) string {
	return strings.TrimSpace(librivoxVersionRE.ReplaceAllString(strings.TrimSpace(t), ""))
}

// librivoxBook converts one selected project (its id is at least 1) into the
// planner's sourceBook. Every refusal - no reader, no author, an unknown
// language - is the planner's own, so it is worded and counted like every other
// source's.
func librivoxBook(pr LibrivoxProject) sourceBook {
	facts := &librivoxFacts{id: int(pr.ID), archiveID: archiveIDOf(pr.URLIArchive)}
	facts.copyrightYear, _ = librivoxYear(string(pr.CopyrightYear))
	links := model.RecordingLinks{
		Librivox: httpsOrEmpty(pr.URLLibrivox),
		Zip:      httpsOrEmpty(pr.URLZipFile),
		RSS:      httpsOrEmpty(pr.URLRSS),
		Text:     httpsOrEmpty(pr.URLTextSource),
	}
	if links != (model.RecordingLinks{}) {
		facts.links = &links
	}

	raw := rawBook{
		"title":     librivoxTitle(pr.Title),
		"language":  strings.TrimSpace(pr.Language),
		"publisher": LibrivoxPublisher,
	}
	if cover := httpsOrEmpty(pr.CoverArtJPG); cover != "" {
		raw["image_url"] = cover
	}
	b := sourceBook{raw: raw, lv: facts}
	if code, ok := librivoxLanguage(pr.Language); ok {
		b.lang = code
	}
	if secs := int(pr.TotalTimeSecs); secs > 0 {
		b.runtimeMin = max(1, (secs+30)/60)
	}

	// Authors first, then translators: the translator rides the author list with
	// the role qualifier the shared credit parser reads, which is how every
	// source states a role (a translator credit, never part of the work's
	// identity author set).
	for _, a := range pr.Authors {
		if name := a.name(); name != "" {
			facts.credits = append(facts.credits, librivoxCredit{kind: librivoxAuthor, id: int(a.ID), name: name, index: len(b.authors), life: lifeYearsOf(a)})
			b.authors = append(b.authors, name)
		}
	}
	for _, t := range pr.Translators {
		if name := t.name(); name != "" {
			facts.credits = append(facts.credits, librivoxCredit{kind: librivoxAuthor, id: int(t.ID), name: name, index: len(b.authors), suffix: librivoxTranslatorSuffix, life: lifeYearsOf(t)})
			b.authors = append(b.authors, name+librivoxTranslatorSuffix)
		}
	}

	sections := append([]LibrivoxSection(nil), pr.Sections...)
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].SectionNumber < sections[j].SectionNumber })
	seenReader := map[string]bool{}
	var start int64
	chapters := make([]rawChapter, 0, len(sections))
	for _, s := range sections {
		for _, r := range s.Readers {
			name := strings.TrimSpace(r.DisplayName)
			key := strconv.Itoa(int(r.ReaderID)) + "\x00" + name
			if name == "" || seenReader[key] {
				continue
			}
			seenReader[key] = true
			facts.credits = append(facts.credits, librivoxCredit{kind: librivoxReader, id: int(r.ReaderID), name: name, index: len(b.narrators)})
			b.narrators = append(b.narrators, name)
		}
		length := int64(s.Playtime) * 1000
		ch := rawChapter{
			"title":           strings.TrimSpace(s.Title),
			"start_offset_ms": json.Number(strconv.FormatInt(start, 10)),
			"length_ms":       json.Number(strconv.FormatInt(length, 10)),
		}
		facts.audioURLs = append(facts.audioURLs, httpsOrEmpty(s.ListenURL))
		chapters = append(chapters, ch)
		start += length
	}
	if len(chapters) > 0 {
		b.chapters = chapters
	}
	for _, g := range pr.Genres {
		if name := strings.TrimSpace(g.Name); name != "" {
			b.genres = append(b.genres, genreClaim{name: name})
		}
	}
	return b
}

// librivoxTranslatorSuffix is the role qualifier a translator's name carries in
// the row's author list (mapping.go's role vocabulary reads it).
const librivoxTranslatorSuffix = " (Translator)"

// httpsOrEmpty is the URL as stated when it is an https URL the schema accepts,
// else "": every URL field the schema gives a LibriVox recording is https only
// and format-checked as a URI (`$defs/https_url`), so a value url.Parse refuses
// (a stray "%" escape, a control character) is dropped here rather than written
// and then failing the post-import validation of the whole tranche.
func httpsOrEmpty(u string) string {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "https://") {
		return ""
	}
	if parsed, err := url.Parse(u); err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return ""
	}
	return u
}

// librivoxYearRE is a year as LibriVox's free-text year fields state one: a
// signed number ("1883", "-428"), or a number marked BC ("428 BC", "428 BCE").
var librivoxYearRE = regexp.MustCompile(`(?i)^(-?\d{1,4})(?:\s*(B\.?C\.?(?:E\.?)?))?$`)

// librivoxYear reads one of LibriVox's year fields (copyright_year, an author's
// dob or dod) as a signed year, negative BC. Anything that is not plainly a year
// - "", "alive", "c. 1500", "0" (LibriVox's "not stated"), a negative number
// that also says BC - reads as ok=false and is never written: a year is a fact
// or it is absent, never a guess.
func librivoxYear(s string) (int, bool) {
	m := librivoxYearRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	y, err := strconv.Atoi(m[1])
	if err != nil || y == 0 {
		return 0, false
	}
	if m[2] != "" {
		if y < 0 {
			return 0, false
		}
		y = -y
	}
	return y, true
}

// lifeYears is a person's stated birth and death years (0 = not stated).
type lifeYears struct{ birth, death int }

// lifeYearsOf reads an author's dob and dod. A pair whose death precedes its
// birth is dropped whole: one of the two is wrong and nothing says which.
func lifeYearsOf(p LibrivoxPerson) lifeYears {
	b, _ := librivoxYear(string(p.DOB))
	d, _ := librivoxYear(string(p.DOD))
	if b != 0 && d != 0 && d < b {
		return lifeYears{}
	}
	return lifeYears{birth: b, death: d}
}

// archiveIDRE is the schema's archive_id pattern.
var archiveIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// archiveIDOf reads the Internet Archive item identifier off a project's
// url_iarchive ("https://www.archive.org/details/<id>"), "" when it states none.
func archiveIDOf(u string) string {
	_, rest, ok := strings.Cut(strings.TrimSpace(u), "archive.org/details/")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	id, _, _ = strings.Cut(id, "?")
	if !archiveIDRE.MatchString(id) {
		return ""
	}
	return id
}

// librivoxKind is which LibriVox id namespace a credit's id is in.
type librivoxKind int

const (
	librivoxReader librivoxKind = iota
	librivoxAuthor
)

// field is the person-record field the namespace's ids are stored in.
func (k librivoxKind) field() string {
	if k == librivoxReader {
		return "librivox_reader_id"
	}
	return "librivox_author_id"
}

// librivoxCredit is one person a LibriVox row credits: the namespace and id,
// the name the row spells them by, and where that name sits in the row's
// narrator (reader) or author list, with the role qualifier that rides it there.
type librivoxCredit struct {
	kind   librivoxKind
	id     int
	name   string
	index  int
	suffix string
	// life is an author's or translator's stated birth and death years (a
	// reader states none), written with the id (stampLibrivoxID).
	life lifeYears
}

// librivoxFacts is what a LibriVox row carries beyond the shared sourceBook
// fields: its identity, its publication facts, and its credits' ids.
type librivoxFacts struct {
	id            int
	archiveID     string
	links         *model.RecordingLinks
	copyrightYear int
	credits       []librivoxCredit
	// audioURLs is each section's archive.org file in chapter order ("" when it
	// states none). Only applyLibrivoxRecording puts them on chapters, so no
	// other source's chapter rows can ever carry one.
	audioURLs []string
}

// librivoxState is a LibriVox run's planner state. The zero value is every
// other run's, and nothing in it is read unless a row carries librivoxFacts.
type librivoxState struct {
	// ids is the project ids the catalogue holds, plus those this run landed.
	ids map[int]bool
	// held is where the catalogue holds each project's recording.
	held map[int]RecRef
	// people maps a (namespace, id) the catalogue holds to the person slug
	// carrying it.
	people map[librivoxKey]string
	// readers is the person slug each reader id resolved to in this run
	// (librivoxReaderSlug), and readerOf the reader id each person slug is
	// HELD by - the one its record carries, or the one this run resolved onto
	// it (a landed row's, which the stamp then writes, or a held recording's
	// credit, splitHeldLibrivoxReaders). Together they are what decides that a
	// second reader id meeting a held name is someone else.
	readers  map[int]string
	readerOf map[string]int
	// authorOf is the author id each person slug carries: catalogued, or
	// stamped (stampLibrivoxPeople runs after planning, so while rows are
	// planned it is the catalogue's alone).
	authorOf map[string]int
	// landed is the rows whose recording this run created, in row order, with
	// what the stamp checks a resolved person against.
	landed []librivoxLanding
	// titles is the spelling-variant index (librivoxmatch.go), built on first
	// use; variantExamples names a few rows it attached, for the run's note.
	titles          map[string][]string
	variantExamples []string
	// authorVariantExamples names a few rows librivoxauthors.go's rule 1
	// attached, for the run's note.
	authorVariantExamples []string
}

// librivoxKey is one LibriVox id in its namespace.
type librivoxKey struct {
	kind librivoxKind
	id   int
}

// librivoxLanding is a row that landed: its facts, the work it landed on and the
// recording's narrator slugs.
type librivoxLanding struct {
	facts     *librivoxFacts
	work      string
	narrators map[string]bool
}

// newLibrivoxState is the empty LibriVox planner state every run starts with.
func newLibrivoxState() librivoxState {
	return librivoxState{
		ids:      map[int]bool{},
		held:     map[int]RecRef{},
		people:   map[librivoxKey]string{},
		readers:  map[int]string{},
		readerOf: map[string]int{},
		authorOf: map[string]int{},
	}
}

// loadLibrivoxPerson indexes a catalogued person's LibriVox ids.
func (p *planner) loadLibrivoxPerson(person *model.Person) {
	for _, k := range []librivoxKey{{librivoxReader, person.LibrivoxReaderID}, {librivoxAuthor, person.LibrivoxAuthorID}} {
		if k.id != 0 {
			p.lv.people[k] = person.ID
		}
	}
	if person.LibrivoxReaderID != 0 {
		p.lv.readerOf[person.ID] = person.LibrivoxReaderID
	}
	if person.LibrivoxAuthorID != 0 {
		p.lv.authorOf[person.ID] = person.LibrivoxAuthorID
	}
}

// rowNarratorSlugs is creditSlugs over a row's cleaned narrator names, with a
// LibriVox row's readers resolved by READER ID first (librivoxReaderSlug). Every
// other row, and a LibriVox row whose cleaned list is not one name per reader
// credit, is creditSlugs unchanged.
//
// The cleaned list lines up with the row's reader credits by index: librivoxBook
// appends exactly one non-empty narrator per credit and sourceCredits maps a
// typed list one name to one credit, so names[c.index] IS that credit's name.
// The length test is the guard on that, not a case that arises.
func (p *planner) rowNarratorSlugs(b sourceBook, names []string, warn func(string, ...any)) []string {
	if b.lv == nil || len(names) != len(b.narrators) {
		return p.creditSlugs(names, warn)
	}
	byIndex := make(map[int]librivoxCredit, len(names))
	for _, c := range b.lv.credits {
		if c.kind == librivoxReader {
			byIndex[c.index] = c
		}
	}
	return dedupeSlugs(names, func(i int, name string) string {
		if c, ok := byIndex[i]; ok {
			if slug := p.librivoxReaderSlug(c, name, b.lv.id, warn); slug != "" {
				return slug
			}
		}
		return p.getOrCreatePerson(name, warn)
	})
}

// librivoxReaderSlug resolves one reader credit (name its cleaned name) by its
// reader id, returning the person slug it is, or "" to let getOrCreatePerson
// resolve the name exactly as it always has. In order:
//
//   - an id this run already resolved, or one the catalogue holds, is the person
//     carrying it ("" when that is the name's own resolution anyway, so every
//     side effect of the ordinary path - a tombstone ride's note - is kept);
//   - a name that slugs away to nothing is "": the catch-all is no one's, so no
//     reader is disambiguated off it (librivoxCreditSlug stamps no id there);
//     nor is one that folds onto a shared record (librivoxSharedPeople), which
//     is a statement rather than somebody's name;
//   - a name whose resolved person is free, or held by this same reader id,
//     claims it for this id and is "": the first holder keeps the plain slug;
//   - otherwise the name is held by ANOTHER reader id, and this reader is a
//     person of their own at model.LibrivoxReaderSlug, created here carrying the
//     id (so the record is never written without the id its slug states). An
//     occupant there that is not this reader (a person whose own name slugs to
//     it), or a tombstone, is never written over: the run warns and falls back
//     to the name.
//
// Decisions are taken in row order as the planner meets the rows, so a run is
// deterministic, and a rerun finds every reader through the catalogued id
// before it reads a name, so it never mints a second record.
func (p *planner) librivoxReaderSlug(c librivoxCredit, name string, project int, warn func(string, ...any)) string {
	if c.id <= 0 {
		return ""
	}
	r := p.resolvePerson(name)
	if slug, ok := p.lv.readers[c.id]; ok {
		return p.librivoxReaderResolved(slug, r)
	}
	if owner, ok := p.lv.people[librivoxKey{librivoxReader, c.id}]; ok {
		if live, ok := p.livePerson(owner); ok {
			p.lv.readers[c.id] = live
			return p.librivoxReaderResolved(live, r)
		}
	}
	if r.fellBack || librivoxSharedPeople[r.slug] {
		return ""
	}
	if holder := p.lv.readerOf[r.slug]; holder == 0 || holder == c.id {
		p.lv.readers[c.id] = r.slug
		p.lv.readerOf[r.slug] = c.id
		return ""
	}
	slug, ok := model.LibrivoxReaderSlug(name, c.id)
	if !ok {
		return ""
	}
	_, taken := p.people[slug]
	if _, retired := p.redirects.Survivor(model.RedirectPeople, slug); taken || retired {
		warn("reader %d (%q): %q is another person's id or a retired one; credited to %q", c.id, name, slug, r.slug)
		return ""
	}
	p.createPerson(slug, name)
	p.stampLibrivoxID(slug, c, project)
	p.lv.readers[c.id] = slug
	p.lv.readerOf[slug] = c.id
	p.summary.LibrivoxReadersSplit++
	return slug
}

// librivoxSharedPeople is the records a credit FOLDS onto rather than names: the
// collective statements (CollectiveIDs - Full Cast, Various, Anonymous, ...) and
// the synthetic voice. Like the catch-all they stand for no one person, so a
// second reader id meeting one is never minted a "various-librivox-<id>" record
// beside it (a person named "Various" that every collective rule would miss).
var librivoxSharedPeople = func() map[string]bool {
	ids := CollectiveIDs()
	ids[syntheticVoiceSlug] = true
	return ids
}()

// librivoxReaderResolved is librivoxReaderSlug's answer for a reader id already
// tied to slug: "" when the name resolves there anyway, else slug.
func (p *planner) librivoxReaderResolved(slug string, r personResolution) string {
	if !r.create && r.slug == slug {
		return ""
	}
	return slug
}

// dedupeByLibrivox is the LibriVox twin of dedupeByASIN: a row whose project id
// the catalogue (or this run) already holds is a skip. false for any other row.
func (p *planner) dedupeByLibrivox(b sourceBook, warn func(string, ...any)) bool {
	if b.lv == nil || !p.lv.ids[b.lv.id] {
		return false
	}
	p.splitHeldLibrivoxReaders(b, warn)
	p.summary.Skipped++
	return true
}

// splitHeldLibrivoxReaders is the one edit a skipped LibriVox row may make: on a
// project the catalogue already holds, a reader credit whose reader id is not
// the one the credited person carries is moved onto that reader's own person
// (librivoxReaderSlug, minting `<name>-librivox-<id>` when the reader has none
// yet). It is how a project imported before readers were keyed apart - when a
// second "Martina" was folded onto the first and the id dropped with a warning -
// is put right by re-importing its id; on a project imported since, every reader
// already resolves to the person the recording credits, so it changes nothing,
// and a second rerun is a no-op by the same test.
//
// Only a narrator credit the recording really holds is moved, and only off a
// person held by ANOTHER reader id, or off any person when this reader's id is
// catalogued on someone else: a person carrying no reader id is otherwise left
// as it is (nothing says it is not this reader). A reader the recording already
// credits under their own person moves nothing (what else it credits is not a
// fold this can prove). When the row also credits the holder under the same name
// (two readers called "Martina" on one project) the holder's credit stays and
// this reader's is added after it. Every decision reads the recording as the
// catalogue holds it, so two readers folded onto one person (neither of them the
// holder) are both moved, in row order, by ONE rewrite.
func (p *planner) splitHeldLibrivoxReaders(b sourceBook, warn func(string, ...any)) {
	ref, ok := p.lv.held[b.lv.id]
	if !ok {
		return
	}
	ws := p.works[ref.Work]
	if ws == nil {
		return
	}
	ri := ws.recs[ref.Rec]
	if ri == nil {
		return
	}
	names := p.rowNarratorNames(b)
	if len(names) != len(b.narrators) {
		return
	}
	moves := map[string]*heldMove{}
	moved := 0
	for _, c := range b.lv.credits {
		if c.kind != librivoxReader || c.id <= 0 {
			continue
		}
		name := names[c.index]
		r := p.resolvePerson(name)
		if r.fellBack || r.create || !ri.narrators[r.slug] {
			continue
		}
		if _, known := p.lv.people[librivoxKey{librivoxReader, c.id}]; !known {
			if holder := p.lv.readerOf[r.slug]; holder == 0 || holder == c.id {
				// The held credit IS this reader as far as anything says, so it
				// holds the name for the run: a new row's other reader of it is
				// then split at once, rather than taking the name and this
				// credit being split off it by the next rerun.
				if _, resolved := p.lv.readers[c.id]; !resolved {
					p.lv.readers[c.id] = r.slug
					p.lv.readerOf[r.slug] = c.id
				}
				continue
			}
		}
		slug := p.librivoxReaderSlug(c, name, b.lv.id, warn)
		if slug == "" || ri.narrators[slug] {
			continue // already credited: a rerun of a split project
		}
		m := moves[r.slug]
		if m == nil {
			m = &heldMove{}
			moves[r.slug] = m
		}
		if slices.Contains(m.to, slug) {
			continue
		}
		m.to = append(m.to, slug)
		m.keep = m.keep || p.rowCreditsHolder(b, names, c, r.slug)
		moved++
	}
	if moved == 0 {
		return
	}
	p.moveHeldNarrators(ref, moves)
	if p.fatal != nil {
		return
	}
	for from, m := range moves {
		if !m.keep {
			delete(ri.narrators, from)
		}
		for _, to := range m.to {
			ri.narrators[to] = true
			p.narratorPeople[to] = true
		}
	}
	p.summary.LibrivoxReadersMoved += moved
}

// heldMove is what splitHeldLibrivoxReaders moves off one credited person: the
// readers' own people, in row order, and whether the holder's own credit stays.
type heldMove struct {
	to   []string
	keep bool
}

// rowCreditsHolder reports whether the row also credits, under another reader
// credit, the reader id that holds slug - the holder's own credit on this
// recording, which a move must keep.
func (p *planner) rowCreditsHolder(b sourceBook, names []string, moved librivoxCredit, slug string) bool {
	holder := p.lv.readerOf[slug]
	for _, c := range b.lv.credits {
		if c.kind == librivoxReader && c.index != moved.index && c.id == holder && p.resolvePerson(names[c.index]).slug == slug {
			return true
		}
	}
	return false
}

// moveHeldNarrators rewrites one held recording's narrator list once: each moved
// person is replaced in place by the readers moved off them, or, with keep, those
// readers are inserted right after them. A reader the list already names is not
// added twice.
func (p *planner) moveHeldNarrators(ref RecRef, moves map[string]*heldMove) {
	entry, rec := p.recordingRaw(ref.Work, ref.Rec)
	if rec == nil {
		return
	}
	list, _ := rec["narrators"].([]any)
	out := make([]any, 0, len(list)+len(moves))
	for _, v := range list {
		from, _ := v.(string)
		m := moves[from]
		if m == nil || m.keep {
			out = append(out, v)
		}
		if m == nil {
			continue
		}
		for _, to := range m.to {
			if !slices.Contains(list, any(to)) && !slices.Contains(out, any(to)) {
				out = append(out, to)
			}
		}
	}
	rec["narrators"] = out
	p.putWorkEntry(ref.Work, entry)
}

// keyLibrivoxCredits spells every LibriVox credit whose id is already known the
// way it is known, BEFORE the censuses and the planner read a name: a catalogued
// id takes its person's catalogued name, and an id this batch states twice takes
// the name its first row gave it. A person slug is derived from the name, so
// this is what makes the id - not the spelling - the person's key.
//
// It runs after runBooks' entity decode, so the names it compares and the ones
// it writes are both decoded.
func (p *planner) keyLibrivoxCredits(books []sourceBook) {
	batch := map[librivoxKey]string{}
	for i := range books {
		b := &books[i]
		if b.lv == nil {
			continue
		}
		for j := range b.lv.credits {
			c := &b.lv.credits[j]
			c.name = decodeField(c.name)
			if c.id <= 0 {
				continue
			}
			k := librivoxKey{c.kind, c.id}
			name := c.name
			if slug, ok := p.lv.people[k]; ok && p.people[slug] != "" {
				name = p.people[slug]
			} else if first, ok := batch[k]; ok {
				name = first
			} else {
				batch[k] = name
			}
			c.name = name
			list := b.authors
			if c.kind == librivoxReader {
				list = b.narrators
			}
			list[c.index] = name + c.suffix
		}
	}
}

// applyLibrivoxRecording puts a LibriVox row's identity and publication facts on
// the recording addRecording is creating (each section's audio file on its
// chapter, when buildChapters kept the list), and remembers the landing for the
// people stamp. A no-op for every other row.
func (p *planner) applyLibrivoxRecording(rec *outRecording, b sourceBook) {
	if b.lv == nil {
		return
	}
	rec.LibrivoxID = b.lv.id
	rec.ArchiveID = b.lv.archiveID
	rec.Links = b.lv.links
	rec.CopyrightYear = b.lv.copyrightYear
	if len(rec.Chapters) == len(b.lv.audioURLs) {
		for i := range rec.Chapters {
			rec.Chapters[i].AudioURL = b.lv.audioURLs[i]
		}
	}
	p.lv.ids[b.lv.id] = true
	p.lv.landed = append(p.lv.landed, librivoxLanding{facts: b.lv, work: rec.Work, narrators: ToSet(rec.Narrators)})
}

// stampLibrivoxPeople writes each landed row's LibriVox ids onto the people its
// credits resolved to: a reader's onto one of the recording's narrators, an
// author's or translator's onto one of the work's credited people (its authors,
// a catalogued work's stored credits, or a role credit this run added to a work
// it created - a second translation's translator). A credit that resolves to
// nobody the row's records name (a translator of a work the row only attached
// to, which records no new credits) is left unstamped. A person that already
// carries a DIFFERENT id in that namespace keeps it and the run warns naming
// both: for an author that is two LibriVox ids whose names are one person here;
// a reader reaches it only when librivoxReaderSlug could not split them (the
// composed id was taken), since a second reader id is otherwise a person of
// their own.
func (p *planner) stampLibrivoxPeople() {
	for _, l := range p.lv.landed {
		for _, c := range l.facts.credits {
			if c.id <= 0 {
				continue
			}
			slug, ok := p.librivoxCreditSlug(c)
			if !ok {
				continue
			}
			if c.kind == librivoxReader && !l.narrators[slug] {
				continue
			}
			if c.kind == librivoxAuthor && !p.workCreditsPerson(l.work, slug) {
				continue
			}
			p.stampLibrivoxID(slug, c, l.facts.id)
			if p.fatal != nil {
				return
			}
		}
	}
}

// LibrivoxUnrecordedCredit is one author or translator credit a landed LibriVox
// row stated that its work does not record (Summary.LibrivoxUnrecordedCredits).
type LibrivoxUnrecordedCredit struct {
	Project int `json:"librivox_id"`
	// AuthorID is the person's librivox_author_id, 0 when the row states none.
	AuthorID int      `json:"librivox_author_id,omitempty"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles,omitempty"`
	// Work is the work the row's recording landed on.
	Work string `json:"work"`
	// PersonExists says the person record exists (the catalogue or the run
	// already held it); false means the run withheld it rather than mint an
	// orphan.
	PersonExists bool `json:"person_exists"`
	// Reason is "unsluggable" for a name that slugs to nothing, which no record
	// can hold (librivoxauthors.go rule 2); "" for a credit the attached work
	// does not name.
	Reason string `json:"reason,omitempty"`
}

// String is the credit as the import report prints it, one line:
//
//	2027 "Amy Coulter" (translator, librivox_author_id 9876) -> siddhartha, person not created
//	2607 "Йордан Йовков" (author, librivox_author_id 6294) -> staroplaninski-legendi, unsluggable
func (c LibrivoxUnrecordedCredit) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %q (", c.Project, c.Name)
	if len(c.Roles) > 0 {
		b.WriteString(strings.Join(c.Roles, ", "))
	} else {
		b.WriteString("author")
	}
	if c.AuthorID > 0 {
		fmt.Fprintf(&b, ", librivox_author_id %d", c.AuthorID)
	}
	fmt.Fprintf(&b, ") -> %s", c.Work)
	switch {
	case c.Reason != "":
		b.WriteString(", " + c.Reason)
	case !c.PersonExists:
		b.WriteString(", person not created")
	}
	return b.String()
}

// librivoxRowAuthors creates the people of a LibriVox row that ATTACHES to ws
// (a work the walk found) - but only those the run will credit there: a person
// ws already names (in its authors or its stored credits), or a role-credited
// person (a translator) when ws is a work THIS run created, onto which
// mergeCreatedWorkFacts adds the row's role credits. Anyone else would be a
// record credited nowhere: a create run never edits a catalogued work's credit
// list, and the row's recording credits only its readers. Each such credit is
// returned for the run's report (and its person, when it would be new, is not
// created; PersonExists is settled once the run has planned every row,
// settleLibrivoxUnrecorded). Identity authors always qualify (matchWork admits
// only a work whose credit list holds them); the per-person test covers every
// attach route alike - the ordinary walk, a spelling variant and a work created
// earlier in the run.
func (p *planner) librivoxRowAuthors(b sourceBook, ws *workState, credits []credit, warn func(string, ...any)) []LibrivoxUnrecordedCredit {
	_, runCreated := p.runCredits[ws.slug]
	var out []LibrivoxUnrecordedCredit
	for _, c := range credits {
		r := p.resolvePerson(c.name)
		if ws.names(r.slug) || (runCreated && len(c.roles) > 0 && !r.fellBack) {
			p.getOrCreatePerson(c.name, warn)
			continue
		}
		out = append(out, LibrivoxUnrecordedCredit{
			Project:  b.lv.id,
			AuthorID: p.librivoxAuthorIDOf(b, c.name, r),
			Name:     c.name,
			Roles:    slices.Clone(c.roles),
			Work:     ws.slug,
		})
	}
	return out
}

// settleLibrivoxUnrecorded sets each unrecorded credit's PersonExists from the
// planner as the run left it: the row's own reader, or a later row's new work,
// can create the person after the credit was withheld. A name that slugs away
// to nothing never has a record of its own (the catch-all is not it).
func (p *planner) settleLibrivoxUnrecorded() {
	for i := range p.summary.LibrivoxUnrecordedCredits {
		c := &p.summary.LibrivoxUnrecordedCredits[i]
		r := p.resolvePerson(c.Name)
		c.PersonExists = !r.create && !r.fellBack
	}
}

// librivoxAuthorIDOf is the librivox_author_id of the row's author or
// translator credit named name (r its resolution), or 0: the credit whose name
// cleans to it (librivoxCreditNames, the cleaning librivoxCreditSlug uses) or
// resolves to the same person. A name that slugs away to nothing matches by
// name alone - every such name resolves to the one catch-all record.
func (p *planner) librivoxAuthorIDOf(b sourceBook, name string, r personResolution) int {
	for _, c := range b.lv.credits {
		if c.kind != librivoxAuthor {
			continue
		}
		got := p.soleCreditName(c)
		if got == "" {
			continue
		}
		if got == name || (!r.fellBack && p.resolvePerson(got).slug == r.slug) {
			return c.id
		}
	}
	return 0
}

// workCreditsPerson reports whether work names person: in its authors, in a
// catalogued work's stored credits, or in a role credit this run recorded on it
// (mergeCreatedWorkFacts adds a later row's credits to a work the run created
// without touching its authors).
func (p *planner) workCreditsPerson(work, person string) bool {
	if ws := p.works[work]; ws != nil && ws.names(person) {
		return true
	}
	for c := range p.runCredits[work] {
		if c.Person == person {
			return true
		}
	}
	return false
}

// librivoxCreditSlug is the person a credit's name resolves to, through the same
// cleaning and resolution the planner used for the row, or ok=false when it
// cleans to nothing or to more than one name, resolves to no live record, or
// slugs away to nothing: such a name lands on the shared catch-all person
// (model.UnslugPersonID), and one LibriVox id stamped there would claim that every
// unsluggable credit is that one reader or author.
func (p *planner) librivoxCreditSlug(c librivoxCredit) (string, bool) {
	// A reader id the run resolved is that person, whatever the name slugs to
	// (librivoxReaderSlug).
	if c.kind == librivoxReader {
		if slug, ok := p.lv.readers[c.id]; ok {
			return p.livePerson(slug)
		}
	}
	name := p.soleCreditName(c)
	if name == "" {
		return "", false
	}
	r := p.resolvePerson(name)
	if r.fellBack {
		return "", false
	}
	return p.livePerson(r.slug)
}

// librivoxCreditNames is the names one LibriVox credit cleans to, through the
// same per-credit cleaning the planner applied to the row: a reader's as a
// narrator credit, an author's or translator's (its role suffix included) as an
// author credit.
func (p *planner) librivoxCreditNames(c librivoxCredit) []string {
	if c.kind == librivoxReader {
		return rowNarratorNamesIn(p.credits, sourceBook{narrators: []string{c.name}})
	}
	return creditNamesOf(rowAuthorCreditsIn(p.credits, sourceBook{authors: []string{c.name + c.suffix}}))
}

// names reports whether the work's own record names person: in its authors or
// in a catalogued work's stored credits[].
func (ws *workState) names(person string) bool {
	return ws.all[person] || slices.ContainsFunc(ws.storedCredits, func(c model.Credit) bool { return c.Person == person })
}

// stampLifeYears writes an author's stated birth and death years onto the person
// being stamped, only when the record states NEITHER: a year another source (or
// a hand edit) wrote is never overwritten, and adding one half beside the other
// half's existing value could pair a death with a birth it precedes.
func stampLifeYears(raw map[string]any, life lifeYears) {
	if _, has := raw["birth_year"]; has {
		return
	}
	if _, has := raw["death_year"]; has {
		return
	}
	if life.birth != 0 {
		raw["birth_year"] = life.birth
	}
	if life.death != 0 {
		raw["death_year"] = life.death
	}
}

// stampLibrivoxID records one id on one person, appending the row's provenance
// when it does so. An id the record already carries is a no-op. Its warnings are
// about one row, so they join the per-row tier (labelled as bookLabel labels a
// LibriVox row), not the run-level lines Summary.RowWarnings leaves out.
func (p *planner) stampLibrivoxID(slug string, c librivoxCredit, project int) {
	k := librivoxKey{c.kind, c.id}
	if owner, ok := p.lv.people[k]; ok {
		if owner != slug {
			p.rowWarnings = append(p.rowWarnings, fmt.Sprintf(
				"librivox %d: %s %d (%q) is already on person %q, not on %q; not recorded twice",
				project, c.kind.field(), c.id, c.name, owner, slug))
		}
		return
	}
	raw := p.entryRaw(pack.FamilyPeople, slug)
	if raw == nil {
		return
	}
	if have, ok := raw[c.kind.field()]; ok {
		p.rowWarnings = append(p.rowWarnings, fmt.Sprintf(
			"librivox %d: person %q already carries %s %v; %s %d (%q) not recorded",
			project, slug, c.kind.field(), have, c.kind.field(), c.id, c.name))
		return
	}
	raw[c.kind.field()] = c.id
	stampLifeYears(raw, c.life)
	srcArr, _ := raw["sources"].([]any)
	raw["sources"] = appendSourceUnique(srcArr, OutSource{Type: p.sourceType, Ref: strconv.Itoa(project), ImportedAt: p.importDate})
	p.putEntry(pack.FamilyPeople, slug, raw)
	p.lv.people[k] = slug
	if c.kind == librivoxAuthor {
		p.lv.authorOf[slug] = c.id
	}
	p.summary.LibrivoxIDsStamped++
}
