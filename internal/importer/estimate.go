package importer

import (
	"encoding/json"
	"fmt"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// estimate.go is the PREORDER-ESTIMATE rule: a runtime the bulk mirror stated
// BEFORE the production was released is an estimate, not a measurement.
//
// libex lists a book as soon as it is up for preorder, with the retailer's
// ESTIMATED length (often a round 600 or 800), and corrects it after release.
// A Bird Among Wolves was catalogued as a preorder at 840 minutes and released
// at 970 - over a chapter table already totalling 970.7 minutes. Two importer
// rules read a runtime as identity evidence, and both misfired on it: the ASIN
// merge (addRecording) refused the corrected regional rows of the same
// production (970 is not within 10% of 840), so the create path minted a second
// recording beside the first; and enrichment (applyToRecording) refused the
// post-release row as a contradiction, so the stale 840 could never be corrected.
//
// The rule has two halves:
//
//  1. COMPARISON: an estimated runtime is weaker evidence than a measured one.
//     Each side is resolved ONCE into a runtimeEvidence - a recording's
//     recInfo.known (knownRuntime, at every site that builds or refreshes a
//     recInfo) and a row's sourceBook.evidence (setEvidenceRuntime, at the row
//     doors). For an estimate the CHAPTER TIMELINE's total stands in (the table
//     is the audio itself); with none, the estimate is kept and marked, and the
//     guards that refuse a row - the ASIN merge, the contradiction guard, the
//     estimate correction - compare it through ONE predicate, sameRuntime, at
//     estimateTolerancePct (50%: a released runtime between half and double the
//     estimate) instead of rawentry.RuntimesCompatible's 10%, which is
//     unchanged. The title corroboration is the exception: it is positive
//     evidence, so it reads an estimate's minutes at the 10% rule
//     (rowProduction.sameProductionAs). The abridged guard, the series-position
//     guard and the narrator set still decide as before.
//  2. CORRECTION: a released row stating a runtime REPLACES a recorded estimate,
//     on the ASIN merge and on --enrich (correctEstimate), unless the chapter
//     timeline - or, with none, the estimate bound - refutes the stated value.
//
// WHICH runtimes are estimates is decided by provenance and dates, never by the
// value itself:
//
//   - a RECORDED runtime is an estimate iff the recording is BULK-MIRROR-ONLY
//     (nothing is ever relaxed for a record a user attested) and its
//     release_date is LATER than every dated statement the mirror made about
//     it: each sources[].imported_at (added_at only where no source is dated).
//     A post-release stamp - an ASIN merge or enrichment by a released row -
//     ENDS the state (stampSource writes it even where its ref is already
//     recorded, see endsEstimate);
//   - a ROW's runtime is an estimate iff the run is a bulk-mirror run and the
//     row's release date is later than the day the row was CAPTURED (rowDay): a
//     libex row's own updatedAt, else --rows-as-of, else the run's date. A
//     preorder row captured before the run is dated that day in the provenance
//     it writes (statementDay), so it neither ends a recorded estimate nor
//     creates a recording that reads as measured.
//
// "Later" is compared at the release date's own PRECISION (releasedAfter), the
// rule internal/serve's releaseIsFuture and the site's isFutureRelease apply, so
// a year-only release date makes an estimate only of a statement made in an
// EARLIER year ("2027" against 2026-12-31). A run with no import date makes
// nothing an estimate.
//
// Measured over the 297,422-recording tree on 2026-10-07: 1,877 bulk-mirror-only
// recordings carry an estimate (540 since released, 1,337 still preorders), and
// 1,352 of them have no chapter list. 515 carry a chapter list and a runtime:
// 407 timelines agree with the recorded runtime and 108 contradict it. Reading
// the statements alone (added_at only as a fallback) changes the state of no
// recording in the tree.
//
// The BOUND is measured against libex itself: the live record of every one of
// the 526 released estimate recordings carrying an ASIN, fetched on 2026-10-07.
// 149 kept the estimate and 377 were corrected; the released length sits within
// 10% of the estimate for 310 (59%), within 30% for 474, 40% for 498, 50% for 507
// (96.4%; 190 of the 194 with no chapter list, 97.9%) and up to 75% beyond (the
// Mr Gum titles, listed at about 400 minutes and released at about 100). Where
// the recording carries chapters the timeline matches the released length
// within 2% for 201 of 204 corrected ones, which is why a timeline replaces the
// estimate outright and is the one fact that can withhold a replacement (The
// Knave and the Moon: 720 recorded over 739 minutes of chapters, a twin minted
// at a round 600). Over the tree's 111 same-work, same-narrator pairs of an
// estimate and a released twin the spread is 15-65% (median 20%), 107 within
// 50%. Distinct productions are not separable by length alone - 553 of the
// 1,580 same-work, same-narrator pairs of recordings catalogued after release
// differ by more than 10%, 159 of them by more than 50% - so the bound is the
// widest the corrections need and no wider: a runtime under half or over double
// an estimate (the 300-minute production beside an 840-minute estimate) stays a
// different production, and the abridged, position and narrator guards decide
// the rest. 28 of the 526 live records were last written before their release
// date: libex had not yet corrected them, which is what the capture-day rule
// reads.
//
// The title corroboration was measured by replaying each of the 2,233
// recordings of the works holding an estimate or a preorder as a row against its
// own work, and each recording against the other volumes of its series by the
// same narrators (11,917 pairs with an estimate on a side). Before this rule the
// first corroborated 2,165 rows and the second 5,089 cross-volume pairs;
// reading an estimate with no timeline as unknown corroborated 765 and 1,303;
// at the 50% bound it would be 2,059 and 9,391 - nearly double the cross-volume
// matches, the Witch Myth shape (TestPreorderVolumesStayApart) - and at the 10%
// rule, as shipped, 2,059 and 5,172. The 106 replay rows lost against the old
// rule are recordings whose own timeline contradicts their runtime, replayed at
// the uncorrected value a real released row would not state.

// releasedAfter reports whether a stated release date is strictly LATER than
// day (YYYY-MM-DD, or an RFC 3339 timestamp read by its UTC day), compared at
// the release date's own precision. An empty or unparseable side is never
// later.
func releasedAfter(release, day string) bool {
	if len(day) > 10 {
		day = model.TimeKey(day)[:10]
	}
	if release == "" || len(day) != 10 || len(release) > len(day) || release <= day[:len(release)] {
		return false
	}
	return datePattern.MatchString(release)
}

// estimateTolerancePct is the bound a same-production comparison holds an
// estimate WITH NO TIMELINE to (see the header's measurement): two runtimes
// are compatible when they are within this percentage of the larger, so a
// released runtime between half and double the estimate.
const estimateTolerancePct = 50

// runtimeEvidence is a runtime as a same-production comparison reads it: whole
// minutes (0 = unknown) and whether those minutes are a preorder ESTIMATE that
// no chapter timeline stood in for. Resolved once per side - a recording's
// recInfo.known (knownRuntime) and a row's sourceBook.evidence
// (setEvidenceRuntime) - and compared only through sameRuntime, or by the title
// corroboration's stricter reading (rowProduction.sameProductionAs).
type runtimeEvidence struct {
	min      int
	estimate bool
}

// estimateRuntimesCompatible is rawentry.RuntimesCompatible's rule at
// estimateTolerancePct: an unknown side is compatible with anything.
func estimateRuntimesCompatible(a, b int) bool {
	if a <= 0 || b <= 0 {
		return true
	}
	lo, hi := min(a, b), max(a, b)
	return (hi-lo)*100 <= hi*estimateTolerancePct
}

// sameRuntime is THE runtime test of every same-production guard that refuses
// a row (the ASIN merge, the contradiction guard, the estimate correction): the
// ordinary 10% rule (runtimesCompatible, unchanged), or estimateTolerancePct
// where either side is an estimate with no timeline.
func sameRuntime(a, b runtimeEvidence) bool {
	if a.estimate || b.estimate {
		return estimateRuntimesCompatible(a.min, b.min)
	}
	return runtimesCompatible(a.min, b.min)
}

// runtimeEstimated is THE test for a recorded runtime: the recording is
// bulk-mirror-only and every dated mirror statement about it - each
// sources[].imported_at - predates its release. added_at stands in only for a
// recording whose sources carry no date (over the tree the two readings agree
// on every recording); it is not consulted otherwise, because a recording
// created after release from a row captured before it is dated by that row's
// capture day (setSource) while its added_at is the run's. A recording with no
// dated statement at all is not judged an estimate.
func runtimeEstimated(releaseDate, addedAt string, sources []model.Source) bool {
	if releaseDate == "" {
		return false
	}
	dated := false
	for _, s := range sources {
		if s.ImportedAt == "" {
			continue
		}
		if !releasedAfter(releaseDate, s.ImportedAt) {
			return false
		}
		dated = true
	}
	if !dated && !releasedAfter(releaseDate, addedAt) {
		return false
	}
	return model.BulkMirrorOnly(sources)
}

// rawRuntimeEstimated is runtimeEstimated over a recording held as decoded JSON.
func rawRuntimeEstimated(raw map[string]any) bool {
	release := coerceStr(raw["release_date"])
	if release == "" {
		return false
	}
	return runtimeEstimated(release, coerceStr(raw["added_at"]), rawSources(raw))
}

// knownRuntime is THE comparable runtime of a recording - recInfo.known: for an
// estimate, its chapter timeline's total when it has one, else the recorded
// runtime marked as an estimate; for any other recording the recorded runtime.
// timeline is asked only for an estimate.
func knownRuntime(runtime int, release, addedAt string, sources []model.Source, timeline func() int) runtimeEvidence {
	if !runtimeEstimated(release, addedAt, sources) {
		return runtimeEvidence{min: runtime}
	}
	if tl := timeline(); tl > 0 {
		return runtimeEvidence{min: tl}
	}
	return runtimeEvidence{min: runtime, estimate: true}
}

// rawKnownRuntime is knownRuntime over a recording held as decoded JSON.
func rawKnownRuntime(raw map[string]any) runtimeEvidence {
	runtime, _ := coerceInt(raw["runtime_min"])
	return knownRuntime(int(runtime), coerceStr(raw["release_date"]), coerceStr(raw["added_at"]), rawSources(raw),
		func() int { tl, _ := rawChapterMinutes(raw["chapters"]); return tl })
}

// syncRecInfo refreshes the run's view of a recording after a write to it (a
// replaced estimate, a filled runtime or chapter list, a post-release stamp
// ending the estimate state), so a later row of the run is compared with what
// the record now says.
func syncRecInfo(ri *recInfo, raw map[string]any) {
	if ri != nil {
		ri.known = rawKnownRuntime(raw)
	}
}

// endsEstimate reports whether this run's stamp on raw is the post-release
// statement that ends its estimate state: the recording is still an estimate and
// the stamp itself is dated on or after the release (a row captured before
// release is dated its capture day, setSource, so it ends nothing). stampSource
// then writes the stamp even where an entry of the same type and ref is already
// recorded. Once written, the recording is no estimate, so a later stamp of the
// run - or a re-run - is deduplicated as usual and sources[] never grows by
// repetition.
func (p *planner) endsEstimate(raw map[string]any) bool {
	day := p.curSource.ImportedAt
	return len(day) == 10 && rawRuntimeEstimated(raw) && !releasedAfter(coerceStr(raw["release_date"]), day)
}

// rowDay is the day a row's statement was CAPTURED, which is what decides
// whether the row knew the release: a libex row's own updatedAt (the day libex
// last wrote it - a row not written since before its release still holds the
// preorder listing, however late it is read), else the genre regeneration's
// --rows-as-of, else the run's date.
func (p *planner) rowDay(b sourceBook) string {
	switch {
	case b.capturedAt != "":
		return b.capturedAt
	case p.rowsAsOf != "":
		return p.rowsAsOf
	}
	return p.importDate
}

// rowEstimate reports whether the row's runtime is a preorder estimate: a
// bulk-mirror row captured before its own release date.
func (p *planner) rowEstimate(b sourceBook) bool {
	return p != nil && p.mirrorTier && releasedAfter(b.str("release_date"), p.rowDay(b))
}

// statementDay is the imported_at a row's provenance carries: the run's date,
// except that a preorder row captured before the run is dated its capture day -
// the statement it records was made before the release, and dating it after
// would end the estimate state of the recording it is stamped on (endsEstimate,
// runtimeEstimated) on a value nobody has corrected.
func (p *planner) statementDay(b sourceBook) string {
	if day := p.rowDay(b); day < p.importDate && p.rowEstimate(b) {
		return day
	}
	return p.importDate
}

// setEvidenceRuntime is the ROW half, resolved once at a row door (planner.run,
// AttestAt, libex-select's libexBook): the runtime a same-production comparison
// may read. For a preorder row (rowEstimate) that is its chapter list's total
// when it states one that builds, else its runtime marked as an estimate. The
// read is evidence only, so a malformed table stays silent here; the paths that
// write chapters warn. A nil planner - a selection that could load no catalogue
// - has no run date, so nothing is an estimate.
func (p *planner) setEvidenceRuntime(b *sourceBook) {
	b.evidence = runtimeEvidence{min: b.runtimeMin}
	if p.rowEstimate(*b) {
		if tl := chapterMinutes(buildChapters(b.chapterRows(), func(string, ...any) {})); tl > 0 {
			b.evidence = runtimeEvidence{min: tl}
		} else {
			b.evidence.estimate = true
		}
	}
}

// rowReleased reports whether the row's own release date had passed when the
// row was captured (rowDay) - the condition for its runtime to replace a
// recorded estimate. A row stating no date, or with no capture day, has not
// shown that it has.
func (p *planner) rowReleased(b sourceBook) bool {
	rd, day := b.str("release_date"), p.rowDay(b)
	return rd != "" && datePattern.MatchString(rd) && len(day) == 10 && !releasedAfter(rd, day)
}

// correctEstimate is the CORRECTION half: a released row stating a runtime
// replaces a recorded estimate (asked of raw before anything this run stamps on
// it). It reports whether raw changed. The replacement is withheld when the
// stated runtime is not the same production's by sameRuntime against the best
// evidence there is:
//
//   - the row carries a clean chapter list: it is the post-release timeline. If
//     it agrees (within 10%) with the stated runtime both replace the recorded
//     ones; if it contradicts its own row's runtime nothing is replaced;
//   - the row carries none: a recorded chapter list that agrees with the stated
//     runtime stays; one that contradicts it keeps the recorded runtime too;
//   - neither carries one: the stated runtime replaces the estimate when it is
//     within estimateTolerancePct of it, and is withheld otherwise.
//
// The evidence is the header's measurement: a preorder listing usually already
// carries the real chapter table, and dropping a correct timeline for a runtime
// it refutes would replace a fact with a guess. A replacement is reported in the
// run's aggregated note, a withheld one in its own; neither is a contradiction,
// so neither reaches the conflict worklist.
func (p *planner) correctEstimate(raw map[string]any, ref RecRef, b sourceBook) bool {
	stated := b.runtimeMin
	if stated <= 0 || !p.rowReleased(b) {
		return false
	}
	// No recorded runtime is no estimate to replace: enrichment fills an absent
	// one itself, and a merged regional row never fills one.
	recorded, _ := coerceInt(raw["runtime_min"])
	if recorded <= 0 || int(recorded) == stated || !rawRuntimeEstimated(raw) {
		return false
	}
	// Evidence only, like setEvidenceRuntime: the fill and create paths warn
	// about a malformed table, so this read stays silent.
	rowChapters := buildChapters(b.chapterRows(), func(string, ...any) {})
	against := rawKnownRuntime(raw) // the recorded timeline, else the estimate itself
	if len(rowChapters) > 0 {
		against = runtimeEvidence{min: chapterMinutes(rowChapters)}
	}
	if !sameRuntime(against, runtimeEvidence{min: stated}) {
		p.estimates.kept++
		if len(p.estimates.keptExamples) < maxWarnExamples {
			evidence := fmt.Sprintf("chapters %d", against.min)
			if against.estimate {
				evidence = "no chapters"
			}
			p.estimates.keptExamples = append(p.estimates.keptExamples,
				fmt.Sprintf("%s/%s kept %d (stated %d, %s)", ref.Work, ref.Rec, recorded, stated, evidence))
		}
		return false
	}
	setDecoded(raw, "runtime_min", stated)
	if len(rowChapters) > 0 {
		setDecoded(raw, "chapters", rowChapters)
	}
	p.summary.EstimatesReplaced++
	if len(p.estimates.replacedExamples) < maxWarnExamples {
		p.estimates.replacedExamples = append(p.estimates.replacedExamples,
			fmt.Sprintf("%s/%s %d -> %d", ref.Work, ref.Rec, recorded, stated))
	}
	return true
}

// setDecoded stores v under key in a decoded record in the form a fresh decode
// of the written entry gives (numbers float64, objects map[string]any, arrays
// []any), so a reader later in the same edit - enrichment's "chapters absent?"
// test, syncRecInfo's chapter total, coerceInt - sees exactly what the next
// read of the entry will. Its values are the importer's own (a runtime, a
// chapter list), which always marshal, so a failure is a programming error.
func setDecoded(raw map[string]any, key string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("setDecoded %s: %v", key, err))
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		panic(fmt.Sprintf("setDecoded %s: %v", key, err))
	}
	raw[key] = decoded
}

// reportEstimates appends the run's aggregated notes: the estimates a released
// row corrected, and those a chapter timeline kept.
func (p *planner) reportEstimates() {
	if n := p.summary.EstimatesReplaced; n > 0 {
		p.summary.Notes = append(p.summary.Notes, withExamples(fmt.Sprintf(
			"replaced %d preorder-estimate %s with the released runtime",
			n, plural(n, "runtime")), p.estimates.replacedExamples))
	}
	if n := p.estimates.kept; n > 0 {
		p.summary.Notes = append(p.summary.Notes, withExamples(fmt.Sprintf(
			"kept %d preorder-estimate %s a released row restated, because the chapter timeline or the estimate bound refutes the stated value",
			n, plural(n, "runtime")), p.estimates.keptExamples))
	}
}

// msToMinutes rounds a length in milliseconds to whole minutes.
func msToMinutes(ms int64) int { return int((ms + 30000) / 60000) }

// chapterMinutes is a chapter list's total length in whole minutes, over the
// importer's own chapters or a loaded recording's (the two have one shape).
func chapterMinutes[C outChapter | model.Chapter](chs []C) int {
	var ms int64
	for _, c := range chs {
		ms += model.Chapter(c).LengthMS
	}
	return msToMinutes(ms)
}

// rawChapterMinutes is chapterMinutes over a recording's decoded chapters
// member (every write to it goes through setDecoded, so it is always the decoded
// form); ok is false when it carries none.
func rawChapterMinutes(v any) (int, bool) {
	chs, _ := v.([]any)
	var ms int64
	for _, c := range chs {
		m, _ := c.(map[string]any)
		n, _ := coerceInt(m["length_ms"])
		ms += n
	}
	return msToMinutes(ms), len(chs) > 0
}
