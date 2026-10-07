package importer

import (
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
//  1. COMPARISON: an estimated runtime is not evidence to any same-production
//     comparison the importer makes. Each side is resolved ONCE - a recording's
//     recInfo.knownMin (knownMinutes, at every site that builds or refreshes a
//     recInfo) and a row's sourceBook.evidenceRuntime (setEvidenceRuntime, at
//     the row doors) - and the ASIN merge, the contradiction guard, the title
//     corroboration and the genre regeneration read only those. For an estimate
//     the CHAPTER TIMELINE's total stands in (the table is the audio itself),
//     and with none the runtime is unknown, which is what an absent runtime
//     already reads as - so a timeline agreeing with its estimate keeps the guard
//     exactly as it was, and the abridged guard, the series-position guard and
//     the narrator set still decide.
//  2. CORRECTION: a released row stating a runtime REPLACES a recorded estimate,
//     on the ASIN merge and on --enrich (correctEstimate), unless a chapter
//     timeline contradicts the stated value.
//
// WHICH runtimes are estimates is decided by provenance and dates, never by the
// value itself:
//
//   - a RECORDED runtime is an estimate iff the recording is BULK-MIRROR-ONLY
//     (nothing is ever relaxed for a record a user attested) and its
//     release_date is LATER than the day of every statement the mirror made
//     about it: its added_at and every sources[].imported_at. A post-release
//     stamp - an ASIN merge or enrichment after release - ENDS the state
//     (stampSource writes it even where its ref is already recorded, see
//     endsEstimate);
//   - a ROW's runtime is an estimate iff the run is a bulk-mirror run and the
//     row's release date is later than the run's import date (the same test for
//     a record that does not exist yet: a recording created from such a row is
//     an estimate by the first test).
//
// "Later" is compared at the release date's own PRECISION (releasedAfter), the
// rule internal/serve's releaseIsFuture and the site's isFutureRelease apply, so
// a year-only release date makes an estimate only of a statement made in an
// EARLIER year ("2027" against 2026-12-31). The run's "now" is
// Options.ImportDate, the stamp every record it writes carries; "" makes nothing
// an estimate.
//
// Measured over the 297,422-recording tree on 2026-10-07: 2,282 bulk-mirror-only
// recordings were catalogued before their release date (945 of them since
// released); 1,877 still carry no post-release statement (540 released, 1,337
// still preorders). 515 of those carry a chapter list and a runtime: 407
// timelines agree with the recorded runtime and 108 contradict it. Of the 713
// same-work, same-narrator recording pairs whose runtimes the 10% guard calls
// different productions, 199 have an estimate on one side and 197 now compare as
// one production. Of the 49 estimate recordings with a twin minted beside them
// that carry chapters, 34 timelines agree with the TWIN's released runtime and
// 12 with the recorded one, where the recorded value is the real one and the
// twin holds a round estimate (The Knave and the Moon: 720 recorded over 739
// minutes of chapters, the twin's 600 over 738) - which is why correctEstimate
// lets a timeline withhold a replacement.

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

// runtimeEstimated is THE test for a recorded runtime: every mirror statement
// about the recording - its added_at and each source's imported_at - predates
// its release, and the recording is bulk-mirror-only. A recording with no dated
// statement at all is not judged an estimate. The order is the cost order: the
// added_at comparison settles nearly every recording.
func runtimeEstimated(releaseDate, addedAt string, sources []model.Source) bool {
	dated := addedAt != ""
	if dated && !releasedAfter(releaseDate, addedAt) {
		return false
	}
	for _, s := range sources {
		if s.ImportedAt == "" {
			continue
		}
		if !releasedAfter(releaseDate, s.ImportedAt) {
			return false
		}
		dated = true
	}
	return dated && model.BulkMirrorOnly(sources)
}

// rawRuntimeEstimated is runtimeEstimated over a recording held as decoded JSON.
func rawRuntimeEstimated(raw map[string]any) bool {
	release, addedAt := coerceStr(raw["release_date"]), coerceStr(raw["added_at"])
	// The added_at half first, so the common recording reads no sources at all.
	if release == "" || (addedAt != "" && !releasedAfter(release, addedAt)) {
		return false
	}
	return runtimeEstimated(release, addedAt, rawSources(raw))
}

// knownMinutes is THE comparable runtime of a recording - recInfo.knownMin, the
// one value a same-production comparison reads: for an estimate, its chapter
// timeline's total (0, unknown, when it has none), else the recorded runtime.
// timeline is asked only for an estimate.
func knownMinutes(runtime int, release, addedAt string, sources []model.Source, timeline func() int) int {
	if runtimeEstimated(release, addedAt, sources) {
		return timeline()
	}
	return runtime
}

// rawKnownMinutes is knownMinutes over a recording held as decoded JSON.
func rawKnownMinutes(raw map[string]any) int {
	if rawRuntimeEstimated(raw) {
		timeline, _ := rawChapterMinutes(raw["chapters"])
		return timeline
	}
	runtime, _ := coerceInt(raw["runtime_min"])
	return int(runtime)
}

// syncRecInfo refreshes the run's view of a recording after a write to it (a
// replaced estimate, a filled runtime or chapter list, a post-release stamp
// ending the estimate state), so a later row of the run is compared with what
// the record now says.
func syncRecInfo(ri *recInfo, raw map[string]any) {
	if ri != nil {
		ri.knownMin = rawKnownMinutes(raw)
	}
}

// endsEstimate reports whether this run's stamp on raw is the post-release
// statement that ends its estimate state: the recording is still an estimate and
// its release date has passed as of the run. stampSource then writes the stamp
// even where an entry of the same type and ref is already recorded. Once
// written, the recording is no estimate, so a later stamp of the run - or a
// re-run - is deduplicated as usual and sources[] never grows by repetition.
func (p *planner) endsEstimate(raw map[string]any) bool {
	return len(p.importDate) == 10 && rawRuntimeEstimated(raw) &&
		!releasedAfter(coerceStr(raw["release_date"]), p.importDate)
}

// setEvidenceRuntime is the ROW half, resolved once at a row door (planner.run,
// AttestAt, libex-select's libexBook): the runtime a same-production comparison
// may read, which for a bulk-mirror row listing a production its own release
// date says is not out yet is its chapter list's total (0, unknown, when it
// states none or one that does not build). The read is evidence only, so a
// malformed table stays silent here; the paths that write chapters warn. A nil
// planner - a selection that could load no catalogue - has no run date, so
// nothing is an estimate.
func (p *planner) setEvidenceRuntime(b *sourceBook) {
	b.evidenceRuntime = b.runtimeMin
	if p != nil && p.mirrorTier && releasedAfter(b.str("release_date"), p.importDate) {
		b.evidenceRuntime = chapterMinutes(buildChapters(b.chapterRows(), func(string, ...any) {}))
	}
}

// rowReleased reports whether the row's own release date has passed as of the
// run - the condition for its runtime to replace a recorded estimate. A row
// stating no date, or a run with no import date, has not shown that it has.
func (p *planner) rowReleased(b sourceBook) bool {
	rd := b.str("release_date")
	return rd != "" && datePattern.MatchString(rd) && len(p.importDate) == 10 && !releasedAfter(rd, p.importDate)
}

// correctEstimate is the CORRECTION half: a released row stating a runtime
// replaces a recorded estimate (asked of raw before anything this run stamps on
// it). It reports whether raw changed. The chapter timeline is the one fact
// that can withhold the replacement:
//
//   - the row carries a clean chapter list: it is the post-release timeline. If
//     it agrees (within 10%) with the stated runtime both replace the recorded
//     ones; if it contradicts its own row's runtime nothing is replaced;
//   - the row carries none: a recorded chapter list that agrees with the stated
//     runtime stays; one that contradicts it keeps the recorded runtime too;
//   - neither carries one: the stated runtime replaces the estimate.
//
// The evidence is the header's 34-of-49 / 12-of-49 measurement: a preorder
// listing usually already carries the real chapter table, and dropping a correct
// timeline for a runtime it refutes would replace a fact with a guess. A
// replacement is reported in the run's aggregated note, a withheld one in its
// own; neither is a contradiction, so neither reaches the conflict worklist.
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
	timeline, timed := rawChapterMinutes(raw["chapters"])
	if len(rowChapters) > 0 {
		timeline, timed = chapterMinutes(rowChapters), true
	}
	if timed && !runtimesCompatible(timeline, stated) {
		p.estimates.kept++
		if len(p.estimates.keptExamples) < maxWarnExamples {
			p.estimates.keptExamples = append(p.estimates.keptExamples,
				fmt.Sprintf("%s/%s kept %d (stated %d, chapters %d)", ref.Work, ref.Rec, recorded, stated, timeline))
		}
		return false
	}
	raw["runtime_min"] = stated
	if len(rowChapters) > 0 {
		raw["chapters"] = rowChapters
	}
	p.summary.EstimatesReplaced++
	if len(p.estimates.replacedExamples) < maxWarnExamples {
		p.estimates.replacedExamples = append(p.estimates.replacedExamples,
			fmt.Sprintf("%s/%s %d -> %d", ref.Work, ref.Rec, recorded, stated))
	}
	return true
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
			"kept %d preorder-estimate %s a released row restated, because the chapter timeline contradicts the stated value",
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
// member (which a write this run may have set to the importer's own list); ok is
// false when it carries none.
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
		return msToMinutes(ms), len(chs) > 0
	}
	return 0, false
}
