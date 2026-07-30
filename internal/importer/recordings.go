package importer

import (
	"fmt"
	"regexp"
	"strings"
)

// recordings.go implements the RECORDINGS-ONLY planning mode
// (ModeRecordingsOnly): adding an alternate NARRATION to a work the catalogue
// already holds.
//
// It is the third planning mode, and it fills a gap the other two left open.
// The catalogue models one work with many recordings, but no tooling path could
// add a new one to an existing work:
//
//   - create (importer.go): a row whose ASIN is unknown mints a work, so a
//     second narration of a catalogued book arrives as a duplicate work rather
//     than a sibling recording.
//   - enrich (enrich.go): matches by ASIN, so a narration the catalogue has
//     never seen matches nothing and is ignored.
//   - libex-select (libexselect.go): keeps only rows that fill a FREE series
//     position, so a second narration of book 2 is excluded by construction.
//
// The mode is deliberately narrow, and its bound is the same one enrichment
// relies on (LICENSING.md's import posture): it is scoped by THIS catalogue, not
// by the source's. It never creates a work and never touches a series file - a
// row whose work is not already here is counted (Summary.SkippedNoWork) and
// dropped. What it may create is a recording under an existing work, and the
// person records its narrators need.
//
// Everything else is the shared pipeline: ASIN dedup first, the same
// omit-never-guess field mapping, the same same-narrator ASIN merge (with its
// runtime and abridged guards) so a regional re-release folds into the recording
// that already exists, and the same libex-import provenance stamp.

// planRecordings is the recordings-only planning pass: every row is resolved to
// a work the catalogue ALREADY holds and, when it resolves, becomes a new
// recording under it (or merges its ASIN into a matching sibling).
func (p *planner) planRecordings(books []sourceBook) {
	normalizeEditionMarkers(books)
	for _, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		p.setSource(asin)
		p.addRecordingToExistingWork(b, asin)
		if p.fatal != nil {
			return
		}
	}
	p.reportUnmatchedWorks()
}

// addRecordingToExistingWork plans one row as a recording under a catalogued
// work. asin is the row's normalized ASIN (computed once by the caller). It
// returns quietly (recording a skip or a warning) whenever the row cannot be
// placed - crucially including the case where its work is not in the catalogue,
// which must never fall through to work creation.
//
// The step ORDER is the mode's own, and deliberately not the create path's:
// work resolution comes BEFORE the language and narrator validation, so a row
// for a book the catalogue does not hold produces no per-row warning and no
// person record. Its natural input is an unfiltered export in which nearly
// every row is about a book we do not have; warning about each one's Finnish
// language or missing narrator would bury the run's real output, and would
// scold the operator for rows they never asked to import.
func (p *planner) addRecordingToExistingWork(b sourceBook, asin string) {
	// Dedup first, exactly as the create mode does: an already-present ASIN is a
	// skip, not a warning.
	if p.dedupeByASIN(asin) {
		return
	}

	ws := p.resolveExistingWork(b)
	if ws == nil {
		p.summary.SkippedNoWork++
		if len(p.noWorkExamples) < maxWarnExamples {
			p.noWorkExamples = append(p.noWorkExamples, bookLabel(b))
		}
		return
	}

	warn := p.bookWarn(b)
	lang, narratorNames, ok := admitRecordingFacts(b, warn)
	if !ok {
		return
	}
	narratorSlugs := p.creditSlugs(narratorNames, warn)
	if p.addRecording(ws, b, asin, lang, narratorSlugs, warn) && asin != "" {
		// Single owner of the global ASIN registry, as in addBook: claim the ASIN
		// only once it actually landed on a recording.
		p.asins[asin] = true
	}
}

// resolveExistingWork returns the catalogued work a row belongs to, or nil. The
// identity is the standard one - a title slug plus an exact author set - tried
// over the row's title candidates in most-specific-first order
// (workTitleCandidates), so a retailer's volume/production decoration cannot
// hide a work we already have.
//
// Each title candidate walks the SAME slug-candidate chain getOrCreateWork
// walks (workCandidates), the way findSeries mirrors getOrCreateSeries: a work
// whose bare title slug was taken by a different author's book is stored under
// "<title>-<author>", and probing only the bare slug would make that work - and
// every alternate narration of it - invisible. The walk stops at the first slug
// that does not exist, because getOrCreateWork would have claimed exactly that
// slug, so nothing beyond it can be in the catalogue.
//
// The author set (and the CleanCreditName pass behind it) is built LAZILY, only
// once a title candidate's bare slug actually hits: on an unfiltered dump the
// overwhelming majority of rows are about books we do not have, and for those
// the single map lookup per candidate is the whole cost. A work can only sit on
// a suffixed slug if the bare one is taken too, so the bare-slug hit is a sound
// gate for entering the chain.
//
// It only ever READS p.works. A recordings-only run creates no work, so the map
// is exactly the catalogue on disk, and a nil result means "not in the
// catalogue" rather than "not created yet".
func (p *planner) resolveExistingWork(b sourceBook) *workState {
	var want map[string]bool
	var firstAuthor string
	for _, title := range workTitleCandidates(b) {
		base := Slugify(title)
		if base == "" {
			continue
		}
		if _, taken := p.works[base]; !taken {
			continue
		}
		if want == nil {
			authorSlugs := slugCredits(rowAuthorNames(b))
			if len(authorSlugs) == 0 {
				return nil // no author: nothing to identify a work by
			}
			want, firstAuthor = ToSet(authorSlugs), authorSlugs[0]
		}
		for _, slug := range workCandidates(base, firstAuthor) {
			ws, exists := p.works[slug]
			if !exists {
				break
			}
			if SameSet(ws.authors, want) {
				return ws
			}
		}
	}
	return nil
}

// reportUnmatchedWorks appends one run-level warning naming how many rows
// matched no catalogued work, with a few examples. It is aggregated rather than
// per-row because "this book is not in the catalogue" is the EXPECTED outcome
// for most of a row set - the same reasoning enrichment's aggregate parse
// warnings use - while the examples are what an operator checks the title
// matching against.
func (p *planner) reportUnmatchedWorks() {
	if p.summary.SkippedNoWork == 0 {
		return
	}
	p.summary.Warnings = append(p.summary.Warnings, withExamples(
		fmt.Sprintf("%d rows matched no catalogued work; no recording added", p.summary.SkippedNoWork),
		p.noWorkExamples))
}

// slugCredits maps a credit list to person slugs WITHOUT creating any person
// record, deduplicating by slug in first-seen order. It is getOrCreatePerson's
// read-only twin: both derive the identity through personSlug, so a name
// compares against a catalogued work's author list exactly as it would be
// written if the work were being created.
func slugCredits(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		slug, _ := personSlug(name)
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out
}

// workTitleCandidates returns the ordered work-title candidates for a row: the
// title as the source states it first, then progressively de-decorated forms.
// Order is load-bearing - the undecorated title is only ever a FALLBACK, so a
// catalogued work whose real title carries the decoration ("The Jungle Book 2")
// is matched by its own title before the stripped form is ever tried.
//
// Both title fields seed the chain (title_short first, then the fuller
// "Title: Subtitle"), because a source states the work title in whichever of
// the two it has.
func workTitleCandidates(b sourceBook) []string {
	var out []string
	seen := map[string]bool{}
	for _, key := range []string{"title_short", "title"} {
		base := strings.TrimSpace(b.str(key))
		if base == "" {
			continue
		}
		for _, cand := range titleStripChain(base) {
			if seen[cand] {
				continue
			}
			seen[cand] = true
			out = append(out, cand)
		}
	}
	return out
}

// titleStripChain returns title followed by each successively de-decorated form
// of it (a trailing volume marker, a trailing production qualifier, or both).
// The chain stops as soon as a pass changes nothing.
//
// Termination is structural, so the loop needs no iteration cap: both strip
// rules either return their input unchanged or return a strictly shorter
// string, and the fixpoint test ends the loop the first time neither fires. A
// future rule that could GROW a title would break that and needs a cap added
// with it.
func titleStripChain(title string) []string {
	out := []string{title}
	cur := title
	for {
		next := strings.TrimSpace(stripProductionQualifier(stripVolumeMarker(cur)))
		if next == "" || next == cur {
			return out
		}
		cur = next
		out = append(out, cur)
	}
}

// volumeMarkerRE matches the trailing volume marker retailers append to a series
// volume's title ("Harry Potter and the Chamber of Secrets, Book 2", "The Lost
// Cartographer - Vol. 3"). The marker names the volume's place in its series,
// not the work, so the work it decorates is the same work without it.
//
// "part" is deliberately NOT in the list: "Part 1" is routinely a real half of a
// split release, and stripping it would map that half onto the whole work.
//
// pkg/scan/derive.go's trailingVol is the sibling pattern on the folder-scanning
// side, and it DOES accept "part" - correctly, because there the marker is being
// read to DERIVE a series position from a folder name, not to decide that two
// titles name the same work. The two lists diverge on purpose; keep them in step
// on everything else.
var volumeMarkerRE = regexp.MustCompile(`(?i)\s*[,:;-]?\s*(?:book|vol\.?|volume)\s*\d+(?:\.\d+)?\s*$`)

// stripVolumeMarker drops one trailing volume marker, or returns the title
// unchanged when there is none (or when stripping would leave nothing).
func stripVolumeMarker(title string) string {
	stripped := strings.TrimSpace(volumeMarkerRE.ReplaceAllString(title, ""))
	if stripped == "" {
		return title
	}
	return stripped
}

// productionQualifiers are the trailing parenthetical qualifiers that describe
// the PRODUCTION rather than the work - the same category as the
// (Unabridged)/(Abridged) markers cleanWorkTitle already strips, so the work
// they decorate is the one without them and an alternate production of it
// belongs under that work as another recording.
//
// The list is deliberately tiny and evidence-driven, and it is used ONLY as a
// fallback candidate when matching against works already in the catalogue - it
// never renames anything and never creates anything. Qualifiers that mean the
// row is NOT the work ("(Excerpt)", a trivia or companion title) must never be
// added: those rows are meant to fall into the no-work bucket.
//
// A stripped qualifier carries no abridged signal - only abridgedFromMarker
// reads a title for the abridged tri-state, and it looks for the abridged
// markers alone - so removing one here cannot invent an edition fact.
//
// Keys are in normQualifier's canonical form: lowercase, single-spaced, and
// HYPHEN-FREE. Folding hyphens into spaces is what makes "Full-Cast Edition" and
// "Full Cast Edition" one entry rather than two, so a punctuation variant of a
// future qualifier cannot silently fail to match the entry meant to cover it.
// TestProductionQualifiersAreCanonical pins the form.
var productionQualifiers = map[string]bool{
	"full cast edition": true,
}

// trailingParenRE captures the content of a title's trailing parenthetical (or
// bracketed) qualifier, with its surrounding whitespace.
var trailingParenRE = regexp.MustCompile(`\s*[([]([^()\[\]]*)[)\]]\s*$`)

// qualifierPunct folds the punctuation a qualifier is spelled with into the
// spaces normQualifier then collapses. The en dash is listed because retailer
// titles really do print one; it is DATA being folded here, not prose, so it
// does not breach the hyphens-only writing rule.
var qualifierPunct = strings.NewReplacer("-", " ", "–", " ", "_", " ")

// normQualifier is the canonical form of a production qualifier: lowercase,
// punctuation folded to spaces, runs of whitespace collapsed to one. Both the
// table's keys and a title's captured qualifier go through it, so the lookup
// cannot depend on how the release happened to punctuate the phrase.
func normQualifier(s string) string {
	return strings.Join(strings.Fields(qualifierPunct.Replace(strings.ToLower(s))), " ")
}

// stripProductionQualifier drops one trailing parenthetical qualifier when it is
// a listed production qualifier (compared in normQualifier's canonical form).
// Anything else - and anything that would strip the title away entirely - is
// left exactly as it is.
func stripProductionQualifier(title string) string {
	m := trailingParenRE.FindStringSubmatchIndex(title)
	if m == nil {
		return title
	}
	if !productionQualifiers[normQualifier(title[m[2]:m[3]])] {
		return title
	}
	stripped := strings.TrimSpace(title[:m[0]])
	if stripped == "" {
		return title
	}
	return stripped
}
