package importer

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// estimate.go is the PREORDER-ESTIMATE rule: a runtime the bulk mirror stated
// BEFORE the production was released is an estimate, not a measurement.
//
// libex lists a book as soon as it is up for preorder, with the retailer's
// ESTIMATED length (often a round 600 or 800). After release the length is
// corrected - A Bird Among Wolves was listed at 840 minutes and released at 970.
// Two importer rules read a runtime as identity evidence, and both misfired on
// an estimate:
//
//   - the ASIN merge (addRecording) refused the corrected regional rows of the
//     same production (970 is not within 10% of 840) and the create path minted
//     a second recording of it beside the first;
//   - enrichment (applyToRecording) reads a runtime disagreement as evidence the
//     row is about another production, so the post-release row was refused as a
//     contradiction and the stale 840 could never be corrected.
//
// The rule, in two halves, each asked through ONE helper here:
//
//  1. COMPARISON: an estimated runtime is not evidence to any same-production
//     comparison the importer makes (recInfo.knownRuntime and
//     planner.statedRuntime, read by the ASIN merge, the contradiction guard,
//     the title-corroboration test and the genre regeneration's contradiction
//     test). Where the same listing carried a CHAPTER LIST, its total stands in
//     for the runtime - the table is the audio itself, and a preorder listing
//     already carries the real one beside an estimated length (A Bird Among
//     Wolves: 840 minutes over 970.7 minutes of chapters) - and with none the
//     runtime is UNKNOWN, which is what an absent runtime already reads as. So a
//     timeline that agrees with its estimate keeps the guard exactly as it was
//     (World's Worst Time Machine: 446 minutes over 446 of chapters stays apart
//     from the 210- and 236-minute productions beside it), and the abridged
//     guard, the series-position guard and the narrator set still decide.
//  2. CORRECTION: a released row stating a runtime REPLACES a recorded estimate,
//     on the ASIN merge and on --enrich (correctEstimate), unless a chapter
//     timeline contradicts the stated value.
//
// WHICH runtimes are estimates is decided by provenance and dates, never by the
// value itself:
//
//   - a RECORDED runtime is an estimate iff the recording is BULK-MIRROR-ONLY
//     (model.BulkMirrorOnly - nothing is ever relaxed for a record a user
//     attested) and its release_date is LATER than the day of every statement
//     the mirror made about it: its added_at and every sources[].imported_at.
//     The first is the brief's "catalogued from a preorder listing"; the second
//     is what ENDS the state - an ASIN merge or enrichment after release stamps a
//     post-release source, and from then on the runtime is a post-release
//     statement and fully guarded again;
//   - a ROW's runtime is an estimate iff the run is a bulk-mirror run and the
//     row's release date is later than the run's import date (the same test
//     over the same two facts, for a record that does not exist yet - a
//     recording created from such a row is an estimate by the first test).
//
// "Later" is compared at the release date's own PRECISION (releasedAfter), the
// rule internal/serve's releaseIsFuture and the site's isFutureRelease apply:
// a bare "2026" is not later than any day of 2026, so a year-only release date
// never makes an estimate. The run's "now" is Options.ImportDate, the stamp
// every record it writes carries, so a run is deterministic in its input and an
// import date of "" (no date) makes nothing an estimate.
//
// Measured over the 297,422-recording tree on 2026-10-07: 2,282 bulk-mirror-only
// recordings were catalogued before their release date (945 of them since
// released); 1,877 still carry no post-release statement (540 of them
// released, 1,337 still preorders). 515 of those carry a chapter list and a
// runtime: 407 timelines agree with the recorded runtime and 108 contradict it.
// Of the 713 same-work, same-narrator recording pairs whose runtimes the 10%
// guard calls different productions, 199 have an estimate on one side.

// releasedAfter reports whether a stated release date is strictly LATER than
// day (YYYY-MM-DD, or a longer timestamp whose first ten characters are the
// day), compared at the release date's own precision. An empty or unparseable
// side is never later.
func releasedAfter(release, day string) bool {
	if len(day) > 10 {
		day = day[:10]
	}
	if release == "" || !datePattern.MatchString(release) || len(day) != 10 || len(release) > len(day) {
		return false
	}
	return release > day[:len(release)]
}

// runtimeEstimated is THE test for a recorded runtime: the recording is
// bulk-mirror-only and every mirror statement about it - its added_at and each
// source's imported_at - predates its release. A recording with no dated
// statement at all is not judged an estimate.
func runtimeEstimated(releaseDate, addedAt string, sources []model.Source) bool {
	if !model.BulkMirrorOnly(sources) {
		return false
	}
	dated := false
	for _, day := range append([]string{addedAt}, sourceDays(sources)...) {
		if day == "" {
			continue
		}
		if !releasedAfter(releaseDate, day) {
			return false
		}
		dated = true
	}
	return dated
}

func sourceDays(sources []model.Source) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.ImportedAt)
	}
	return out
}

// rawRuntimeEstimated is runtimeEstimated over a recording held as decoded JSON.
func rawRuntimeEstimated(raw map[string]any) bool {
	arr, _ := raw["sources"].([]any)
	sources := make([]model.Source, 0, len(arr))
	for _, s := range arr {
		m, _ := s.(map[string]any)
		sources = append(sources, model.Source{Type: coerceStr(m["type"]), ImportedAt: coerceStr(m["imported_at"])})
	}
	return runtimeEstimated(coerceStr(raw["release_date"]), coerceStr(raw["added_at"]), sources)
}

// knownRuntime is the recording's runtime as a same-production comparison may
// read it: for an estimate, its chapter timeline's total (0, unknown, when it
// has none).
func (ri *recInfo) knownRuntime() int {
	if ri.estimate {
		return ri.timeline
	}
	return ri.runtimeMin
}

// knownRecordedRuntime is knownRuntime over a recording held as decoded JSON.
func knownRecordedRuntime(raw map[string]any) int {
	if rawRuntimeEstimated(raw) {
		timeline, _ := rawChapterMinutes(raw["chapters"])
		return timeline
	}
	runtime, _ := coerceInt(raw["runtime_min"])
	return int(runtime)
}

// mirrorRun reports whether this run's source is the bulk mirror.
func (p *planner) mirrorRun() bool {
	return model.TierOfSource(p.sourceType) == model.TierBulkMirror
}

// rowRuntimeEstimated is the ROW half of the rule: a bulk-mirror row listing a
// production its own release date says is not out yet.
func (p *planner) rowRuntimeEstimated(b sourceBook) bool {
	return p.mirrorRun() && releasedAfter(b.str("release_date"), p.importDate)
}

// statedRuntime is the row's runtime as a same-production comparison may read
// it: for an estimate, its own chapter list's total (0, unknown, when it states
// none or one that does not build).
func (p *planner) statedRuntime(b sourceBook) int {
	if p.rowRuntimeEstimated(b) {
		return chapterMinutes(buildChapters(b.chapterRows(), func(string, ...any) {}))
	}
	return b.runtimeMin
}

// rowReleased reports whether the row's own release date has passed as of the
// run - the condition for its runtime to replace a recorded estimate. A row
// stating no date, or a run with no import date, has not shown that it has.
func (p *planner) rowReleased(b sourceBook) bool {
	rd := b.str("release_date")
	return rd != "" && datePattern.MatchString(rd) && len(p.importDate) == 10 && !releasedAfter(rd, p.importDate)
}

// correctEstimate is the CORRECTION half, on a recording whose runtime the
// caller has established IS an estimate (asked before anything this run stamps
// on it): a released row stating a runtime replaces it. It reports whether raw
// changed.
//
// The chapter list is the one fact that can veto the replacement, because a
// chapter timeline is the production's own audio measured where a runtime is a
// listing's number. Measured over the tree, of the 49 estimate recordings with
// a twin minted beside them that carry chapters, 34 timelines agree with the
// TWIN's (post-release) runtime and not the recorded one - the preorder listing
// already carried the real chapter table beside an estimated length, which is
// how A Bird Among Wolves holds 840 minutes over 970.7 minutes of chapters - and
// 12 agree with the recorded runtime, where on inspection the recorded value is
// the real one and the twin holds a round estimate (The Knave and the Moon: 720
// recorded over 739 minutes of chapters, the twin's 600 over 738). So:
//
//   - the row carries a clean chapter list: it is the post-release timeline. If
//     it agrees (within 10%) with the stated runtime both replace the recorded
//     ones; if it contradicts its own row's runtime the row is the inconsistent
//     side and nothing is replaced;
//   - the row carries none: a recorded chapter list that agrees with the stated
//     runtime stays (the common case above); one that contradicts it keeps the
//     recorded runtime too, since the timeline corroborates the recorded value
//     and dropping a correct timeline for a runtime it refutes would replace a
//     fact with a guess;
//   - neither carries one: the stated runtime replaces the estimate.
//
// A recorded timeline that contradicts the stated value rarely reaches here at
// all: the comparison reads that timeline in the estimate's place, so the ASIN
// merge keeps such a row apart and enrichment refuses it as the contradiction
// it is. The rule above is what holds when one does.
//
// A replacement is reported in the run's aggregated note, a withheld one in its
// own; neither is a contradiction, so neither reaches the conflict worklist.
func (p *planner) correctEstimate(raw map[string]any, ref RecRef, b sourceBook) bool {
	stated := b.runtimeMin
	if stated <= 0 || !p.rowReleased(b) {
		return false
	}
	recorded, _ := coerceInt(raw["runtime_min"])
	if int(recorded) == stated {
		return false
	}
	// The fill and create paths warn about a malformed chapter table; this read
	// is evidence only, so it stays silent rather than warning twice.
	rowChapters := buildChapters(b.chapterRows(), func(string, ...any) {})
	timeline, timed := rawChapterMinutes(raw["chapters"])
	if len(rowChapters) > 0 {
		timeline, timed = chapterMinutes(rowChapters), true
	}
	if timed && !runtimesCompatible(timeline, stated) {
		p.estimates.kept++
		p.estimates.keptExamples = appendExample(p.estimates.keptExamples,
			fmt.Sprintf("%s/%s kept %d (stated %d, chapters %d)", ref.Work, ref.Rec, recorded, stated, timeline))
		return false
	}
	raw["runtime_min"] = stated
	if len(rowChapters) > 0 {
		raw["chapters"] = rowChapters
	}
	p.estimates.replaced++
	p.estimates.replacedExamples = appendExample(p.estimates.replacedExamples,
		fmt.Sprintf("%s/%s %d -> %d", ref.Work, ref.Rec, recorded, stated))
	p.summary.EstimatesReplaced++
	return true
}

// estimateReport is the run's tally for the aggregated notes.
type estimateReport struct {
	replaced, kept                 int
	replacedExamples, keptExamples []string
}

func appendExample(list []string, s string) []string {
	if len(list) < maxWarnExamples {
		list = append(list, s)
	}
	return list
}

// chapterMinutes is a chapter list's total length in whole minutes (rounded).
func chapterMinutes(chs []outChapter) int {
	var ms int64
	for _, c := range chs {
		ms += c.LengthMS
	}
	return int((ms + 30000) / 60000)
}

// rawChapterMinutes is chapterMinutes over a recording's decoded chapters
// member; ok is false when it carries none.
func rawChapterMinutes(v any) (int, bool) {
	switch chs := v.(type) {
	case []outChapter:
		return chapterMinutes(chs), len(chs) > 0
	case []any:
		var ms int64
		for _, c := range chs {
			m, _ := c.(map[string]any)
			n, _ := coerceInt(m["length_ms"])
			ms += n
		}
		return int((ms + 30000) / 60000), len(chs) > 0
	}
	return 0, false
}

// reportEstimates appends the run's aggregated notes: the estimates a released
// row corrected, and those a chapter timeline kept.
func (p *planner) reportEstimates() {
	if p.estimates.replaced > 0 {
		p.summary.Notes = append(p.summary.Notes, withExamples(fmt.Sprintf(
			"replaced %d preorder-estimate %s with the released runtime",
			p.estimates.replaced, plural(p.estimates.replaced, "runtime")), p.estimates.replacedExamples))
	}
	if p.estimates.kept > 0 {
		p.summary.Notes = append(p.summary.Notes, withExamples(fmt.Sprintf(
			"kept %d preorder-estimate %s a released row restated, because the chapter timeline contradicts the stated value",
			p.estimates.kept, plural(p.estimates.kept, "runtime")), p.estimates.keptExamples))
	}
}

// syncRecInfo refreshes the run's view of a recording after a write to it: its
// runtime (a replaced estimate) and whether that runtime is still an estimate (a
// post-release stamp ends the state), so a later row of the run is compared
// with what the record now says.
func (p *planner) syncRecInfo(ri *recInfo, raw map[string]any) {
	if ri == nil {
		return
	}
	if runtime, ok := coerceInt(raw["runtime_min"]); ok {
		ri.runtimeMin = int(runtime)
	}
	ri.estimate = rawRuntimeEstimated(raw)
	ri.timeline, _ = rawChapterMinutes(raw["chapters"])
}

// modelChapterMinutes is chapterMinutes over a loaded recording's chapters.
func modelChapterMinutes(chs []model.Chapter) int {
	var ms int64
	for _, c := range chs {
		ms += c.LengthMS
	}
	return int((ms + 30000) / 60000)
}
