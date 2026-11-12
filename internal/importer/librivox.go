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
//     repeat markers, a trailing "(version 2)", "(ver 2)", "(ver. 2)",
//     "(Dramatic Reading)" or a bare ", Version 2" (stacked or combined, and
//     "version" in its Italian, Spanish and Portuguese spellings too,
//     librivoxTitle), are stripped first: they name LibriVox projects of one
//     text, not works, so such a row is another recording of the plain-titled
//     work. A non-Latin title (it slugs to nothing, or most of its letters are
//     not Latin script) never walks a slug chain: its new work is created at
//     `librivox-<project id>`.
//   - Its PEOPLE are keyed by LibriVox id. A reader id the catalogue already
//     holds is its person whatever the feed now calls them (librivoxReaderSlug),
//     and every author id's person is decided once before planning
//     (resolveLibrivoxAuthors), so a person renamed on LibriVox, or reread
//     from a later snapshot, never mints a second record;
//     after planning, the ids of the rows that landed are stamped onto the
//     people they resolved to (stampLibrivoxPeople). A READER id is a person of
//     its own even when its name is not: a reader whose name's slug another
//     person already holds under a different reader id is minted at
//     model.LibrivoxReaderSlug (`martina-librivox-634`) instead of being folded
//     onto them (librivoxReaderSlug), so two LibriVox accounts called "Martina"
//     are two people and the first holder keeps the plain slug; a reader whose
//     name slugs to nothing (Greek, Cyrillic, CJK) is minted at
//     model.LibrivoxUnslugReaderSlug (`librivox-reader-11294`) rather than on
//     the shared catch-all.
//   - Sections are chapters: start_ms cumulative from each section's playtime,
//     the section's archive.org file as the chapter's audio_url. One section
//     without a playtime makes the timeline unknowable past it, so the whole
//     chapter list is omitted (buildChapters' all-or-nothing rule).
//   - A project still IN PROGRESS (no runtime and no archive.org item,
//     librivoxInProgress) is set aside before planning and listed on the run's
//     summary, never written; a rerun after LibriVox completes it imports it.
//   - Facts only (LICENSING.md): no description is read at all.

import (
	"encoding/json"
	"fmt"
	"net/url"
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

// lvNumber is an lvInt where 0 is a real value - LibriVox numbers some
// projects' sections from 0 - so it also says whether the feed stated a number:
// a missing, null or non-numeric one is not stated, never 0.
type lvNumber struct {
	n      int
	stated bool
}

func (v *lvNumber) UnmarshalJSON(b []byte) error {
	n, err := strconv.Atoi(strings.TrimSpace(strings.Trim(strings.TrimSpace(string(b)), `"`)))
	*v = lvNumber{n: n, stated: err == nil}
	return nil
}

// MarshalJSON writes the number as LibriVox states one (a string of digits),
// null when none was stated, so a snapshot round-trips.
func (v lvNumber) MarshalJSON() ([]byte, error) {
	if !v.stated {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(strconv.Itoa(v.n))), nil
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
	SectionNumber lvNumber         `json:"section_number"`
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

// overlaps reports whether any selected id lies in [lo, hi]; hi 0 is
// open-ended.
func (s LibrivoxIDs) overlaps(lo, hi int) bool {
	for _, sp := range s.spans {
		if (hi == 0 || sp[0] <= hi) && (sp[1] == 0 || lo <= sp[1]) {
			return true
		}
	}
	return false
}

// RunLibrivox imports the projects of the snapshot at snapshotDir whose ids are
// selected into opts.DataDir, through the shared planner. Two modes exist for
// this source: the create mode, and the genre regeneration (regenerate.go),
// which re-derives the genres of the works holding the selected projects under
// today's table (model.LibrivoxGenres) - how a table change reaches the works a
// tranche already wrote.
func RunLibrivox(snapshotDir string, ids LibrivoxIDs, opts Options) (Summary, error) {
	if opts.Mode != ModeCreate && opts.Mode != ModeRegenerateGenres {
		return Summary{}, fmt.Errorf("a LibriVox import creates or regenerates genres; it has no %s mode", opts.Mode.runName())
	}
	// ParseLibrivoxIDs admits no id below 1, so every selected project has one.
	projects, snapshotWarnings, err := readLibrivoxSnapshot(snapshotDir, &ids)
	if err != nil {
		return Summary{}, err
	}
	books := make([]sourceBook, 0, len(projects))
	var inProgress, retired []int
	for _, pr := range projects {
		switch {
		case librivoxRetiredNumber(pr):
			retired = append(retired, int(pr.ID))
		case librivoxInProgress(pr):
			inProgress = append(inProgress, int(pr.ID))
		default:
			books = append(books, librivoxBook(pr))
		}
	}
	sum, err := runBooks(books, sourceLibrivox, opts, nil, func(p *planner) {
		p.genres = librivoxGenreTable()
		p.lv.inProgress = inProgress
	})
	sum.LibrivoxProjects = len(projects)
	sum.LibrivoxRetired = retired
	// A snapshot row no --ids can select is a run-level line, ahead of the rows'.
	sum.Warnings = slices.Concat(snapshotWarnings, sum.Warnings)
	sum.RunLevelWarnings += len(snapshotWarnings)
	return sum, err
}

// settleLibrivoxInProgress sorts the in-progress projects RunLibrivox set
// aside once the catalogue is loaded: one the catalogue already holds is
// skipped as already present, as its project id would be (a project landed
// before it read as in progress - its sections since withdrawn - is not
// news), and the rest are Summary.LibrivoxInProgress.
func (p *planner) settleLibrivoxInProgress() {
	for _, id := range p.lv.inProgress {
		if p.lv.ids[id] {
			p.summary.Skipped++
			continue
		}
		p.summary.LibrivoxInProgress = append(p.summary.LibrivoxInProgress, id)
	}
	p.lv.inProgress = nil
}

// librivoxRetiredTitleRE is the title LibriVox gives a project number it
// retired or holds for reuse: "DO NOT RE-USE PROJECT NUMBER" (8030, 18134),
// "REUSE project number - 03 sections" (22249).
var librivoxRetiredTitleRE = regexp.MustCompile(`(?i)^\s*(?:do\s+not\s+)?re-?use\s+project\s+number\b`)

// librivoxRetiredNumber reports whether pr is a retired or reusable project
// number rather than a project: such a title and no author. It is no book,
// never will be one under that title, and is neither landed nor listed as in
// progress (meta#2778: the in-progress rule listed every one of them on every
// run); only their count is reported (Summary.LibrivoxRetired).
func librivoxRetiredNumber(pr LibrivoxProject) bool {
	return len(pr.Authors) == 0 && librivoxRetiredTitleRE.MatchString(pr.Title)
}

// librivoxInProgress reports whether pr is a project LibriVox has not finished
// recording: it states no total runtime (0, missing or unreadable, lvInt) AND
// no archive.org item (archiveIDOf, the rule the recording's archive_id is
// read by). The feed has no status field, and a project still being
// read is listed with its sections so far, so landing one would publish a
// recording without most of its audio (LIBRIVOX-PLAN.md keeps in-progress
// projects a non-goal). Such a row is set aside before planning and writes
// nothing - no work, person or id stamp - and a rerun once LibriVox completes
// it (a runtime or an archive.org item appears) imports it like any other.
// A zero runtime alone is not enough: a finished project can state none while
// naming its archive.org item (id 66, Grimm's Märchen index).
func librivoxInProgress(pr LibrivoxProject) bool {
	return pr.TotalTimeSecs <= 0 && archiveIDOf(pr.URLIArchive) == ""
}

// librivoxVersionRE is one trailing parenthetical of LibriVox's
// repeat-recording markers: "(version 2)", "(ver 2)" or "(ver. 2)" numbering
// another project of one text, and "(Dramatic Reading)" naming a cast
// production of it. One parenthetical may hold several, bare or separated by
// librivoxSep ("(version 3, Dramatic Reading)", "(version 2 dramatic
// reading)", "(Version 4 - Dramatic Reading)"), and a separator standing just
// before it goes with it ("Time Traders, (Version 2)" is "Time Traders"). Each
// names a project, not a work: a dramatic reading or a numbered version is
// another recording of the same text, so the marker never reaches the work
// identity.
var librivoxVersionRE = regexp.MustCompile(`(?i)(?:\s*` + librivoxSep + `)?\s*\(\s*` + librivoxMarker +
	`(?:\s*` + librivoxSep + `?\s*` + librivoxMarker + `)*\s*\)\s*$`)

// librivoxMarker is one of librivoxVersionRE's markers. A version number has
// one or two digits, as in the bare form (librivoxBareVersionRE), so a year in
// a parenthetical ("(Version 1611)") is never read as one.
const librivoxMarker = `(?:(?:` + librivoxVersionWord + `|ver\.?)\s*` + librivoxVersionNumber + `|dramatic\s+reading)`

// librivoxVersionNumber is a repeat marker's version number: one or two
// digits, in every marker form, so a year ("Version 1611") is never one.
// Measured over the whole 2026-10-27 snapshot, no version is above 14.
const librivoxVersionNumber = `\d{1,2}`

// librivoxSharedVersionRE is a version marker that ENDS a parenthetical
// holding something else: "Spring (Barber version 2)", "Sir Gawain and the
// Green Knight (Weston Translation Version 2)", "Alice's Adventures in
// Wonderland (abridged, version 2)" (meta#2778). Only the marker goes - the
// full word with a one- or two-digit number after whitespace or a separator -
// so the parenthetical keeps what names the edition ("(Barber)", "(Weston
// Translation)") and an abridgement stays stated ("(abridged)", which the
// shared title rules then read as they read project 1122's). Group 1 is the
// parenthetical's own content and group 2 the version word. A translation
// whose NAME ends in "Version" ("King James Version", "Revised Standard
// Version", librivoxVersionNamedTranslation) keeps the word: in "(King James
// Version 2)" only the number is LibriVox's repeat marker, so the title is
// "(King James Version)", never "(King James)".
var librivoxSharedVersionRE = regexp.MustCompile(`(?i)\(\s*([^()]*?[^()\s,;:\x{2013}\x{2014}-])\s*(?:` + librivoxSep + `\s*|\s+)` +
	`(` + librivoxVersionWord + `)\s*` + librivoxVersionNumber + `\s*\)\s*$`)

// librivoxVersionTranslationWords are the last words of the Bible translation
// names that end in "Version" (King James, Authorized, Revised, Revised
// Standard, American Standard, Douay-Rheims): before them "Version" is part
// of the name, not a repeat marker.
var librivoxVersionTranslationWords = map[string]bool{
	"james": true, "authorized": true, "authorised": true, "revised": true,
	"standard": true, "rheims": true, "douay": true,
}

// librivoxVersionNamedTranslation reports whether content (a parenthetical's
// words before "Version N") ends in a translation name that takes "Version".
func librivoxVersionNamedTranslation(content string) bool {
	words := strings.FieldsFunc(strings.ToLower(content), func(r rune) bool { return r == ' ' || r == '-' })
	return len(words) > 0 && librivoxVersionTranslationWords[words[len(words)-1]]
}

// librivoxVersionWord is the word "version" as LibriVox's projects spell it:
// English and French "version", Italian "versione", Spanish "versión",
// Portuguese "versão" ("Canti (Versione 2)", "Rimas Versión 2", "Relíquia
// (Versão 2)").
const librivoxVersionWord = `(?:versione?|versión|versão)`

// librivoxSep is a separator between a title and its marker, or between two
// markers: a comma, semicolon, colon, hyphen, en dash or em dash.
const librivoxSep = `[,;:\x{2013}\x{2014}-]`

// librivoxBareVersionRE is the same version marker written without
// parentheses: "Walden, Version 2", "Sea Wolf - Version 2", "Daddy-Long-Legs
// Version 2". Only the full word (librivoxVersionWord) with a number of one or
// two digits counts here, after whitespace or librivoxSep, so a title that
// merely ends in a number, or in a year ("... Version 1611"), is never read as
// a marker. Measured over the whole 2026-10-27 snapshot (22,449 projects, no
// version above 14) it changes 58 titles, every one a repeat project.
var librivoxBareVersionRE = regexp.MustCompile(`(?i)(?:\s*` + librivoxSep + `\s*|\s+)` + librivoxVersionWord + `\s*` + librivoxVersionNumber + `\s*$`)

// librivoxTitle is a project's work title: LibriVox's own, its trailing
// markers stripped, and a version marker ending a parenthetical that holds
// something else removed from it (librivoxSharedVersionRE). The strip is end-anchored and repeated, so stacked markers
// go in any order ("(version 3) (Dramatic Reading)", "Emma (Dramatic Reading),
// Version 2"), while a marker followed by anything else stays ("Metamorphosis
// (Dramatic Reading) (1915)" keeps its title and meets the qualified-twin rule).
// A title is never emptied: one that is nothing but markers keeps its first
// ("(Dramatic Reading) (version 2)" is "(Dramatic Reading)").
func librivoxTitle(t string) string {
	t = strings.TrimSpace(t)
	for {
		loc := librivoxVersionRE.FindStringIndex(t)
		if loc == nil {
			loc = librivoxBareVersionRE.FindStringIndex(t)
		}
		if loc == nil {
			if m := librivoxSharedVersionRE.FindStringSubmatchIndex(t); m != nil && m[0] > 0 {
				content := t[m[2]:m[3]]
				if librivoxVersionNamedTranslation(content) {
					content += " " + t[m[4]:m[5]]
				}
				t = strings.TrimSpace(t[:m[0]]) + " (" + content + ")"
				continue
			}
		}
		if loc == nil || loc[0] == 0 {
			return t
		}
		t = strings.TrimSpace(t[:loc[0]])
	}
}

// librivoxBook converts one selected project (its id is at least 1) into the
// planner's sourceBook. Every refusal - no reader, no author, an unknown
// language - is the planner's own, so it is worded and counted like every other
// source's.
func librivoxBook(pr LibrivoxProject) sourceBook {
	// The project's own title and genre names are free text like the work's
	// title, so they are read decoded (entities.go) - the work's title is
	// decoded from the same string by runBooks' pass, which never sees these.
	facts := &librivoxFacts{id: int(pr.ID), archiveID: archiveIDOf(pr.URLIArchive), title: decodeField(strings.TrimSpace(pr.Title))}
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
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].SectionNumber.n < sections[j].SectionNumber.n })
	readerCredit := map[string]int{} // a reader's key -> their credit's index in facts.credits
	var start int64
	chapters := make([]sourceChapter, 0, len(sections))
	for _, s := range sections {
		sec := librivoxSection{number: -1}
		if s.SectionNumber.stated {
			sec.number = s.SectionNumber.n
		}
		for _, r := range s.Readers {
			name := strings.TrimSpace(r.DisplayName)
			if name == "" {
				continue
			}
			key := strconv.Itoa(int(r.ReaderID)) + "\x00" + name
			ci, seen := readerCredit[key]
			if !seen {
				ci = len(facts.credits)
				readerCredit[key] = ci
				facts.credits = append(facts.credits, librivoxCredit{kind: librivoxReader, id: int(r.ReaderID), name: name, index: len(b.narrators)})
				b.narrators = append(b.narrators, name)
			}
			if !slices.Contains(sec.readers, ci) {
				sec.readers = append(sec.readers, ci)
			}
		}
		facts.sections = append(facts.sections, sec)
		length := int64(s.Playtime) * 1000
		ch := sourceChapter{title: strings.TrimSpace(s.Title), startMS: start, lengthMS: length, hasStart: true, hasLength: true}
		facts.audioURLs = append(facts.audioURLs, httpsOrEmpty(s.ListenURL))
		chapters = append(chapters, ch)
		start += length
	}
	if len(chapters) > 0 {
		b.chapters = chapters
	}
	for _, g := range pr.Genres {
		if name := decodeField(strings.TrimSpace(g.Name)); name != "" {
			b.genres = append(b.genres, genreClaim{name: name})
			if !slices.Contains(facts.genres, name) {
				facts.genres = append(facts.genres, name)
			}
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

// lifeYears is a person's stated birth and death years (0 = not stated), and
// the year LibriVox says an author with no dates FLOURISHED ("fl. 1916"), which
// is never written as a birth or death year: it only dates the person for the
// same-name rule (librivoxpeople.go), and names a record minted for them.
type lifeYears struct{ birth, death, fl int }

// stated reports whether any year is stated.
func (l lifeYears) stated() bool { return l.birth != 0 || l.death != 0 || l.fl != 0 }

// lifeYearsOf reads an author's dob and dod. A pair whose death precedes its
// birth is dropped whole: one of the two is wrong and nothing says which. A
// dod (or dob) of "fl.1916" / "fl. 1925" is the flourished year.
func lifeYearsOf(p LibrivoxPerson) lifeYears {
	b, _ := librivoxYear(string(p.DOB))
	d, _ := librivoxYear(string(p.DOD))
	if b != 0 && d != 0 && d < b {
		return lifeYears{}
	}
	if b != 0 || d != 0 {
		return lifeYears{birth: b, death: d}
	}
	fl := librivoxFlourished(string(p.DOD))
	if fl == 0 {
		fl = librivoxFlourished(string(p.DOB))
	}
	return lifeYears{fl: fl}
}

// librivoxFlourishedRE is LibriVox's "flourished" year: "fl.1916", "fl. 1925".
var librivoxFlourishedRE = regexp.MustCompile(`(?i)^fl\.?\s*(\d{3,4})$`)

// librivoxFlourished reads a flourished year, 0 when s states none.
func librivoxFlourished(s string) int {
	m := librivoxFlourishedRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	y, _ := strconv.Atoi(m[1])
	return y
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
	// person is an author or translator credit's person, decided for its id
	// before planning (resolveLibrivoxAuthors); nil for a reader, a credit
	// with no id, and one the person rules held or left to its name.
	person *creditPerson
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
	// title is the project's own title, its repeat markers kept, and genres its
	// own genre names, verbatim, each once, in LibriVox's order; sections is
	// each section's number and readers in chapter order. The recording pass
	// (librivoxrecording.go) writes them onto the recording.
	title    string
	genres   []string
	sections []librivoxSection
}

// librivoxState is a LibriVox run's planner state. The zero value is every
// other run's, and nothing in it is read unless a row carries librivoxFacts.
type librivoxState struct {
	// reviewed is the run's reviewed decisions (reviewedLibrivoxState).
	reviewed map[int]LibrivoxReview
	// ownWorks are the works holding an own-work project, catalogued or created by
	// this run: what the duplicate-identity probe sets apart (librivoxreviewed.go).
	ownWorks map[string]bool
	// ids is the project ids the catalogue holds, plus those this run landed.
	ids map[int]bool
	// held is where the catalogue holds each project's recording.
	held map[int]RecRef
	// people maps a (namespace, id) the catalogue holds to the person slug
	// carrying it.
	people map[librivoxKey]string
	// readers is the person slug each reader id of the run was decided to be
	// before planning (decideLibrivoxReaders), and readerOf the reader id each
	// person slug is HELD by - the one its record carries, or the one this run
	// decided onto it (which the stamp then writes when the row lands).
	// Together they are what decides that a second reader id meeting a held
	// name is someone else.
	readers  map[int]string
	readerOf map[string]int
	// authorOf is the author id each person slug is HELD by - the one its
	// record carries, or the one a row already planned in this run claimed
	// for it (claimLibrivoxAuthors), which the stamp then writes - and
	// claimedAuthor the ids so claimed. The stamp runs after planning, so the
	// claims are what lets a later row of the run see an id an earlier one
	// will stamp, as readerOf does for readers.
	authorOf      map[string]int
	claimedAuthor map[int]bool
	// landed is the rows whose recording this run created, in row order, with
	// what the stamp checks a resolved person against.
	landed []librivoxLanding
	// heldRows is the rows whose project the catalogue already held
	// (dedupeByLibrivox), in row order, for the recording pass to restate
	// LibriVox's facts on (librivoxrecording.go).
	heldRows []librivoxHeld
	// titles is the spelling-variant index (librivoxmatch.go), built on first
	// use; variantExamples names a few rows it attached, for the run's note.
	titles          map[string][]string
	variantExamples []string
	// inProgress is the project ids RunLibrivox set aside as in progress,
	// sorted once the catalogue is loaded (settleLibrivoxInProgress).
	inProgress []int
	// scriptWorks is rule 4's catalogued `librivox-<id>` works by their
	// own-script title key, for resolveWork's merge-only step
	// (librivoxScriptWalk), built on first use and kept current.
	scriptWorks map[string][]string
	// authorVariantExamples names a few rows librivoxauthors.go's rule 1
	// attached, for the run's note; keyedVariants is the example of each
	// project keyLibrivoxAuthorVariants moved credits on, counted once that
	// project lands (noteLibrivoxLanded).
	authorVariantExamples []string
	keyedVariants         map[int]string
	// life is the birth and death years each catalogued person states.
	life map[string]lifeYears
	// authorPins is the person each author id of the rows to be planned (and
	// each translator id of the held rows, heldRowSkips) was resolved to
	// before planning (resolveLibrivoxAuthors, then
	// keyLibrivoxAuthorVariants): the catalogued holder of a known id, else
	// the person rules' decision. Every credit of the id carries it
	// (librivoxCredit.person, credit.person).
	authorPins map[int]*creditPerson
	// runAuthors is the person slugs an author id of the run whose years end
	// before LibriVox began lands on (authorPins, settleLibrivoxPeople):
	// author records for rule 3 whatever order the run meets their rows and a
	// same-name reader's in (librivoxAuthorRecord).
	runAuthors map[string]bool
	// sameName is the author ids whose rows go to the worklist as
	// same-name-author (librivoxpeople.go rule 2), and pendingHolds the
	// worklist rows among them whose candidates are named once the run has
	// planned every row (finishLibrivoxHolds).
	sameName     map[int]lvHold
	pendingHolds []lvPending
	// readerMints is the reader ids decideLibrivoxReaders made people of
	// their own, created when a row of theirs lands (librivoxReaderSlug), and
	// readerNames the spelling a reader id that claimed a person not yet a
	// record was decided under (its lowest project's), which a row spelling
	// the reader otherwise creates them by.
	readerMints map[int]*lvReaderMint
	readerNames map[int]string
	// pairs is the titled and bare author ids of the batch rule 1b kept
	// apart (Summary.LibrivoxAuthorPairs), and splits the ids rule 4 filed
	// at their author-id form beside a same-name person
	// (Summary.LibrivoxAuthorSplits).
	pairs  [][2]*lvAuthorClaim
	splits []lvAuthorSplit
	// slotTaken is the author ids whose name slugs to nothing and whose
	// librivox-author-<id> slot another person or a tombstone holds, by that
	// slot: their credits are not recorded (librivoxSlotTaken).
	slotTaken map[int]string
	// works is each catalogued person's works (librivoxpeople.go), built on
	// first use (workDates the same works by slug); workKeys each catalogued
	// work's title keys, memoized;
	// titledTwins the catalogued titled names by their bare slug.
	works       map[string][]lvPersonWork
	workDates   map[string]lvPersonWork
	workKeys    map[string]map[string]bool
	titledTwins map[string][]string
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
	work, rec string
	narrators map[string]bool
}

// newLibrivoxState is the empty LibriVox planner state every run starts with. The
// decisions are every source's: its duplicate guard asks where they put a work.
func newLibrivoxState() librivoxState {
	return librivoxState{
		ownWorks:      map[string]bool{},
		ids:           map[int]bool{},
		held:          map[int]RecRef{},
		people:        map[librivoxKey]string{},
		readers:       map[int]string{},
		readerOf:      map[string]int{},
		authorOf:      map[string]int{},
		claimedAuthor: map[int]bool{},
		life:          map[string]lifeYears{},
		authorPins:    map[int]*creditPerson{},
		runAuthors:    map[string]bool{},
		sameName:      map[int]lvHold{},
		readerMints:   map[int]*lvReaderMint{},
		readerNames:   map[int]string{},
		slotTaken:     map[int]string{},
	}
}

// loadLibrivoxPerson indexes a catalogued person's LibriVox ids, their own and
// their aliases (model.Person.LibrivoxReaderAliases): an alias resolves as the
// record's own id does. readerOf and authorOf stay the record's own, and a test
// comparing ids asks answersTo.
func (p *planner) loadLibrivoxPerson(person *model.Person) {
	index := func(kind librivoxKind, own int, aliases []int) {
		if own != 0 {
			p.lv.people[librivoxKey{kind, own}] = person.ID
		}
		for _, id := range aliases {
			p.lv.people[librivoxKey{kind, id}] = person.ID
		}
	}
	index(librivoxReader, person.LibrivoxReaderID, person.LibrivoxReaderAliases)
	index(librivoxAuthor, person.LibrivoxAuthorID, person.LibrivoxAuthorAliases)
	if person.LibrivoxReaderID != 0 {
		p.lv.readerOf[person.ID] = person.LibrivoxReaderID
	}
	if person.LibrivoxAuthorID != 0 {
		p.lv.authorOf[person.ID] = person.LibrivoxAuthorID
	}
	if person.BirthYear != 0 || person.DeathYear != 0 {
		p.lv.life[person.ID] = lifeYears{birth: person.BirthYear, death: person.DeathYear}
	}
}

// rowNarratorSlugs is creditSlugs over a row's cleaned narrator names, with a
// LibriVox row's readers resolved by READER ID first (librivoxReaderSlug). Every
// other row, and a LibriVox row whose cleaned list is not one name per reader
// credit, is creditSlugs unchanged.
//
// The cleaned list lines up with the row's reader credits by index: librivoxBook
// appends exactly one non-empty narrator per credit and rawNarratorNamesIn maps
// its typed list one name to one credit (cleanedCredits: the list rule, which
// can drop a name, is skipped there), so names[c.index] IS that credit's name.
// The length test is the guard on that, not a case that arises.
func (p *planner) rowNarratorSlugs(b sourceBook, names []string, warn func(string, ...any)) []string {
	byIndex := librivoxReaderCreditsByIndex(b, names)
	if byIndex == nil {
		return p.creditSlugs(names, warn)
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

// librivoxReaderCreditsByIndex is a LibriVox row's reader credits by their
// index in its cleaned narrator list (names), nil for every other row and for
// one whose cleaned list is not one name per reader credit.
func librivoxReaderCreditsByIndex(b sourceBook, names []string) map[int]librivoxCredit {
	if b.lv == nil || len(names) != len(b.narrators) {
		return nil
	}
	byIndex := make(map[int]librivoxCredit, len(names))
	for _, c := range b.lv.credits {
		if c.kind == librivoxReader {
			byIndex[c.index] = c
		}
	}
	return byIndex
}

// setLibrivoxPerson puts pin on the row's j-th LibriVox credit. The row's
// cached author credits (batchClaimsIn) are built from these pins, so a cache
// built before is rebuilt here, at the one place a pin is set ahead of
// planning: a credit's pin and the row's cached credit can never disagree,
// whichever pass filled the cache first.
func (p *planner) setLibrivoxPerson(b *sourceBook, j int, pin *creditPerson) {
	b.lv.credits[j].person = pin
	if b.creditsCached {
		cacheAuthorCredits(p.credits, b)
	}
}

// librivoxAuthorCredits is rowAuthorCreditsIn for a LibriVox row: its author
// list cleaned as cleanedCredits cleans it (one credit per author credit,
// pinned or not: the list rule that drops a doubled name is a retailer's
// rule), except that a credit whose person
// was decided for its author id (librivoxCredit.person, resolveLibrivoxAuthors)
// carries that person and is spelled by the person's name, its roles read
// through a quiet census: the id, not the cleaning, says who it is, so no fold
// the cleaning would make is a merge to report. The cleaned list lines up with
// the row's credits by index, as rowNarratorSlugs' does (librivoxBook appends
// one non-empty name per credit); the length test guards that.
func librivoxAuthorCredits(ctx creditContext, b sourceBook) []credit {
	names := sourceNames(b.authors, "")
	if len(names) != len(b.authors) {
		return cleanedCredits(b.authors, b.str("author"), ctx.author)
	}
	pins := make([]*creditPerson, len(names))
	pinned := false
	for _, c := range b.lv.credits {
		if c.kind == librivoxAuthor && c.person != nil && c.index < len(pins) {
			pins[c.index], pinned = c.person, true
		}
	}
	if !pinned {
		return cleanedCredits(b.authors, b.str("author"), ctx.author)
	}
	out := make([]credit, 0, len(names))
	for i, name := range names {
		if pin := pins[i]; pin != nil {
			_, roles := creditWithRolesSided(name, ctx.author.quiet())
			out = append(out, credit{name: pin.spelled, roles: roles, person: pin})
			continue
		}
		cleaned, roles := creditWithRolesSided(name, ctx.author)
		out = append(out, credit{name: cleaned, roles: roles})
	}
	return out
}

// librivoxReaderSlug is a reader credit's person at planning (name its
// cleaned name), as decideLibrivoxReader decided it for the reader id before
// any row was planned, or "" to let getOrCreatePerson resolve the name exactly
// as it always has: the reader's own name's person (its claim), an id with no
// decision, or a split record whose slot was taken. A reader id decided by
// another project's spelling (readerNames) is created under that spelling by
// the first row of theirs that lands, whatever this row calls them. A split
// reader's record (lvReaderMint) is created here, carrying the id, the first time a row of
// theirs lands - never written over an occupant or a tombstone at the
// composed id: the run warns and falls back to the name. The unsluggable form
// never reaches that fallback: a row with such a reader is refused before
// planning (librivoxUnfiledReader), since its name would fall back to the
// catch-all.
func (p *planner) librivoxReaderSlug(c librivoxCredit, name string, project int, warn func(string, ...any)) string {
	d := p.librivoxReaderDecision(c, name)
	switch {
	case d.blocked:
		warn("reader %d (%q): %q is another person's id or a retired one; credited to %q", c.id, name, p.lv.readerMints[c.id].slug, p.resolvePerson(name).slug)
	case d.mint != nil:
		if m := d.mint; !m.made {
			m.made = true
			p.createPerson(m.slug, m.name)
			p.stampLibrivoxID(m.slug, c, project)
			if m.unslug {
				p.summary.LibrivoxReadersUnslugged++
			} else {
				p.summary.LibrivoxReadersSplit++
			}
		}
	case d.slug != "":
		if first, ok := p.lv.readerNames[c.id]; ok {
			// The id was decided by another project's spelling of the reader,
			// whose row has not created them - not yet, or not at all (a
			// worklist row): this row creates the person, under that spelling.
			p.getOrCreatePerson(first, warn)
		}
	}
	return d.slug
}

// lvReaderDecision is what the decision taken for a reader id before planning
// (decideLibrivoxReader) says of one credit of it: the person it is filed at
// (slug; "" lets the name decide - no id, no decision, a reader the name
// resolves to anyway, or blocked), the split record to file it at (mint),
// and whether that split's slot is another person's or a tombstone's
// (blocked).
type lvReaderDecision struct {
	slug    string
	mint    *lvReaderMint
	blocked bool
}

// librivoxReaderDecision is the reader id's decision for a credit (name its
// cleaned name) with none of its effects: librivoxReaderSlug acts on it, and
// the read-only resolvers (rowProductionOf) read it, so the two never
// disagree.
func (p *planner) librivoxReaderDecision(c librivoxCredit, name string) lvReaderDecision {
	if c.id <= 0 {
		return lvReaderDecision{}
	}
	if m, ok := p.lv.readerMints[c.id]; ok {
		if _, taken := p.people[m.slug]; m.blocked || (taken && !m.made) {
			return lvReaderDecision{blocked: true}
		}
		return lvReaderDecision{slug: m.slug, mint: m}
	}
	if slug, ok := p.lv.readers[c.id]; ok {
		return lvReaderDecision{slug: p.librivoxReaderResolved(slug, p.resolvePerson(name))}
	}
	return lvReaderDecision{}
}

// librivoxUnfiledReader reports the first of a LibriVox row's narrator names
// (names, its cleaned list) that slugs to nothing and that its reader id's
// decision files nowhere (librivoxReaderDecision, the rule librivoxReaderSlug
// acts on): a credit with no reader id, or one whose slot another person or a
// tombstone holds - since before the run, or since a row of this run created a
// person there. Such a name would fall back to the
// shared catch-all, which pkg/check refuses for a LibriVox recording
// (checkUnslugFallback) only after every pack is written, so the row is
// refused at planning instead and stays re-importable. false for every other
// row.
func (p *planner) librivoxUnfiledReader(b sourceBook, names []string) (string, bool) {
	if b.lv == nil {
		return "", false
	}
	byIndex := librivoxReaderCreditsByIndex(b, names)
	for i, name := range names {
		if _, fellBack := personSlug(name); !fellBack {
			continue
		}
		// The one rule librivoxReaderSlug files the reader by: a decided
		// person, never a split whose slot is blocked - by the catalogue, or
		// by a person this run created there since the decision.
		if c, ok := byIndex[i]; ok && p.librivoxReaderDecision(c, name).slug != "" {
			continue
		}
		return name, true
	}
	return "", false
}

// lvReaderMint is a reader decideLibrivoxReader made a person of their own: the
// composed slot (model.LibrivoxReaderSlug, or model.LibrivoxUnslugReaderSlug
// for a name that slugs to nothing: unslug) and the spelling it was composed
// from, which the record is created under whichever of the reader's rows
// lands first (its id names the slot, so the two always agree), blocked when
// another person or a tombstone already holds it, and made once a row of
// theirs has created it.
type lvReaderMint struct {
	slug, name            string
	unslug, blocked, made bool
}

// decideLibrivoxReaders decides, before any row is planned and after the
// author ids (whose dead authors are author records to a reader, runAuthors),
// the person every reader id of the batch is, in the order of the lowest
// project crediting it, held projects included - the order a run meets
// readers in, whatever order the rows arrive in. A held project decides only
// what splitHeldLibrivoxReaders reads: a credit naming one of the held
// recording's narrators that no other reader id holds is that reader, holding
// the name for the run; every other credit of a held project, and every
// credit of a project to be planned, is decideLibrivoxReader's. A project with
// no language the import knows never lands, so it decides nothing.
func (p *planner) decideLibrivoxReaders(books []sourceBook) {
	for _, i := range p.librivoxRows(books, lvAllRows) {
		b := &books[i]
		names := rowNarratorNamesIn(p.credits, *b)
		if len(names) != len(b.narrators) {
			continue
		}
		var held *recInfo
		if ref, ok := p.lv.held[b.lv.id]; ok && p.lv.ids[b.lv.id] {
			if ws := p.works[ref.Work]; ws != nil {
				held = ws.recs[ref.Rec]
			}
			if held == nil {
				continue
			}
		} else if _, ok := b.language(); !ok {
			continue
		}
		for _, c := range b.lv.credits {
			if c.kind != librivoxReader || c.id <= 0 {
				continue
			}
			name := names[c.index]
			if held != nil {
				r := p.resolvePerson(name)
				if r.fellBack || r.create || !held.narrators[r.slug] {
					continue
				}
				if _, known := p.lv.people[librivoxKey{librivoxReader, c.id}]; !known {
					if holder := p.lv.readerOf[r.slug]; holder == 0 || holder == c.id {
						if _, resolved := p.lv.readers[c.id]; !resolved {
							p.lv.readers[c.id] = r.slug
							p.lv.readerOf[r.slug] = c.id
						}
						continue
					}
				}
			}
			p.decideLibrivoxReader(c, name)
		}
	}
}

// decideLibrivoxReader decides one reader id by its credit (name its cleaned
// name), once: an id the catalogue holds is the person carrying it; a name
// that slugs away to nothing is a person of their own at
// model.LibrivoxUnslugReaderSlug, never the shared catch-all (which pkg/check's
// checkUnslugFallback refuses for a LibriVox-stated record); a name that folds
// onto a shared record (librivoxSharedPeople) is a statement rather than
// somebody's name, so no reader is disambiguated off it and the name decides;
// a name whose person is free, or held by this same reader id, is this reader
// (the first holder keeps the plain slug), except an AUTHOR record
// (librivoxAuthorRecord, librivoxpeople.go rule 3), which no reader is stamped
// onto; otherwise the name is held by ANOTHER reader id or an author, and the
// reader is a person of their own at model.LibrivoxReaderSlug (lvReaderMint),
// created when a row of theirs lands. A rerun finds every reader through the
// catalogued id before it reads a name, so it never mints a second record.
func (p *planner) decideLibrivoxReader(c librivoxCredit, name string) {
	if _, ok := p.lv.readers[c.id]; ok {
		return
	}
	if _, ok := p.lv.readerMints[c.id]; ok {
		return
	}
	if owner, ok := p.lv.people[librivoxKey{librivoxReader, c.id}]; ok {
		if live, ok := p.livePerson(owner); ok {
			p.lv.readers[c.id] = live
			return
		}
	}
	r := p.resolvePerson(name)
	mint, unslug := model.LibrivoxReaderSlug, false
	switch holder := p.lv.readerOf[r.slug]; {
	case r.fellBack:
		mint, unslug = model.LibrivoxUnslugReaderSlug, true
	case librivoxSharedPeople[r.slug]:
		return
	case holder == c.id, holder == 0 && !p.librivoxAuthorRecord(r.slug):
		p.lv.readers[c.id] = r.slug
		p.lv.readerOf[r.slug] = c.id
		if r.create {
			p.lv.readerNames[c.id] = name
		}
		return
	}
	slug, ok := mint(name, c.id)
	if !ok {
		return
	}
	m := &lvReaderMint{slug: slug, name: name, unslug: unslug}
	_, taken := p.people[slug]
	if _, retired := p.redirects.Survivor(model.RedirectPeople, slug); taken || retired {
		m.blocked = true
	} else {
		p.lv.readers[c.id] = slug
		p.lv.readerOf[slug] = c.id
	}
	p.lv.readerMints[c.id] = m
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

// librivoxReaderResolved is librivoxReaderSlug's answer for a reader id tied to
// slug: "" when the name resolves there anyway (live, or the record the name
// creates: the reader's own claim), else slug.
func (p *planner) librivoxReaderResolved(slug string, r personResolution) string {
	if r.slug == slug {
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
	if ref, ok := p.lv.held[b.lv.id]; ok {
		p.lv.heldRows = append(p.lv.heldRows, librivoxHeld{facts: b.lv, ref: ref})
	}
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
// fold this can prove). When the row also credits the holder (two readers called
// "Martina" on one project, or the holder's id under any spelling:
// rowCreditsHolder) the holder's credit stays and this reader's is added after
// it. Every decision reads the recording as the
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
				// The held credit IS this reader as far as anything says, and
				// holds the name for the run (decideLibrivoxReaders): a new
				// row's other reader of it is split at once, rather than
				// taking the name and this credit being split off it by the
				// next rerun.
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
// credit, the person holding slug - the holder's own credit on this recording,
// which a move must keep. A credit carrying an id they answer to is theirs
// whatever it spells (the id, not the name, is the person); with no id on
// either side, only the name can say so.
func (p *planner) rowCreditsHolder(b sourceBook, names []string, moved librivoxCredit, slug string) bool {
	for _, c := range b.lv.credits {
		if c.kind != librivoxReader || c.index == moved.index || !p.answersTo(librivoxReader, slug, c.id) {
			continue
		}
		if c.id > 0 || p.resolvePerson(names[c.index]).slug == slug {
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

// decodeLibrivoxCredits decodes every LibriVox credit's name as runBooks' entity
// decode has decoded the row's own lists, before anything reads one. A credit's
// PERSON is not its spelling: an author credit's is decided once for the batch
// (resolveLibrivoxAuthors, librivoxpeople.go) and a reader's by reader id
// (librivoxReaderSlug), so the names stay as LibriVox spells them.
func (p *planner) decodeLibrivoxCredits(books []sourceBook) {
	for i := range books {
		if b := &books[i]; b.lv != nil {
			for j := range b.lv.credits {
				b.lv.credits[j].name = decodeField(b.lv.credits[j].name)
			}
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
	if ws := p.works[rec.Work]; ws != nil {
		ws.noteVolumeTotals(rec.ID, b.lv.title)
	}
	p.lv.landed = append(p.lv.landed, librivoxLanding{facts: b.lv, work: rec.Work, rec: rec.ID, narrators: ToSet(rec.Narrators)})
}

// noteHeldLibrivoxTitles reads, before any row is routed, the project title of
// every row whose project the catalogue already holds as that recording's
// volume totals (workState.volumeTotals): the recording pass restates it as
// librivox_title after planning (restateLibrivoxRecordings), so a catalogue
// imported before that field existed - or one whose stored title LibriVox has
// since changed - is read in this run as the next run will read it.
func (p *planner) noteHeldLibrivoxTitles(books []sourceBook) {
	for _, b := range books {
		if b.lv == nil || b.lv.title == "" {
			continue
		}
		ref, ok := p.lv.held[b.lv.id]
		if !ok {
			continue
		}
		if ws := p.works[ref.Work]; ws != nil && ws.recs[ref.Rec] != nil {
			ws.noteVolumeTotals(ref.Rec, b.lv.title)
		}
	}
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

// librivoxAuthorIDOf is the librivox_author_id of the row's author or
// translator credit cr (r its resolution), or 0: the credit naming the person r
// names (librivoxCreditPerson: its decided person, else its name's), or one
// with no decided person whose name cleans to cr's (librivoxCreditNames, the
// cleaning librivoxCreditSlug uses). A name that slugs away to nothing matches
// by name alone - every such name resolves to the one catch-all record.
func (p *planner) librivoxAuthorIDOf(b sourceBook, cr credit, r personResolution) int {
	for _, c := range b.lv.credits {
		if c.kind != librivoxAuthor {
			continue
		}
		if c.person == nil && p.soleCreditName(c) == cr.name {
			return c.id
		}
		if !r.fellBack && p.librivoxCreditPerson(c) == r.slug {
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
// slugs away to nothing: such a name resolves to the shared catch-all person
// (model.UnslugPersonID), and one LibriVox id stamped there would claim that every
// unsluggable credit is that one reader or author. An unsluggable READER the run
// minted at model.LibrivoxUnslugReaderSlug never gets that far (the first branch
// returns their record); one it could not mint (an occupied or retired slot, no
// reader id) still reaches the fellBack refusal below.
func (p *planner) librivoxCreditSlug(c librivoxCredit) (string, bool) {
	// A reader id the run resolved is that person, whatever the name slugs to
	// (librivoxReaderSlug), and so is an author credit's decided person.
	if c.kind == librivoxReader {
		if slug, ok := p.lv.readers[c.id]; ok {
			return p.livePerson(slug)
		}
	} else if c.person != nil {
		return p.livePerson(c.person.slug)
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
		return rawNarratorNamesIn(p.credits, sourceBook{narrators: []string{c.name}})
	}
	return creditNamesOf(rawAuthorCreditsIn(p.credits, sourceBook{authors: []string{c.name + c.suffix}}))
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

// claimLibrivoxAuthors records, for a row that landed on work, the author ids
// stampLibrivoxPeople will write once planning is done, by the same tests in
// the same (row) order: a credit resolving to a person the work credits, whose
// id no person carries or claimed, onto a person holding none (authorOf).
func (p *planner) claimLibrivoxAuthors(b sourceBook, work string) {
	for _, c := range b.lv.credits {
		if c.kind != librivoxAuthor || c.id <= 0 {
			continue
		}
		if _, held := p.lv.people[librivoxKey{librivoxAuthor, c.id}]; held || p.lv.claimedAuthor[c.id] {
			continue
		}
		slug, ok := p.librivoxCreditSlug(c)
		if !ok || !p.workCreditsPerson(work, slug) || p.lv.authorOf[slug] != 0 {
			continue
		}
		p.lv.authorOf[slug] = c.id
		p.lv.claimedAuthor[c.id] = true
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
