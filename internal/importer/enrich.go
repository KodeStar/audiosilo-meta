package importer

import "strings"

// enrich.go implements the ASIN-matched ENRICHMENT mode (ModeEnrich), the
// second of the three import shapes LICENSING.md's import posture accepts:
// "filling absent facts on catalogued works/recordings, matched by identifier,
// from a source whose operator permits it".
//
// It is the exact complement of the create mode in importer.go, and the two are
// deliberately disjoint:
//
//   - create: a row whose ASIN is already catalogued is SKIPPED; an unknown ASIN
//     creates a work/recording/people/series.
//   - enrich: a row whose ASIN is UNKNOWN is counted (Summary.NotInCatalog) and
//     ignored; a KNOWN ASIN fills only facts the catalogued records do not have.
//
// Enrichment NEVER creates a work, a recording, a person or a series, and never
// overwrites a fact already recorded - the existing value always wins. That is
// what bounds a run by THIS catalogue rather than by the source's: only the rows
// matching records already here do anything at all. The bound is on what a run
// WRITES, though, not on what it reads - a source's parse layer still reads its
// whole input into memory before any of this runs (see libex.go), so a
// pre-filtered row set is still the recommended input.
//
// The field-application core below (applyToRecording / applyToWork) is shared
// with the trust-tier attestation the create and ASIN-merge paths perform, so
// "the existing value always wins" has exactly one documented exception, in one
// place: a USER-library run meeting a record that is still bulk-mirror-only
// overwrites it (attest.go, LICENSING.md). Enrichment itself is a libex mode, so
// it never takes that branch - p.userTier is false for the whole run.
//
// Every changed record gets one provenance stamp (appendSourceUnique, so a
// second pass never double-stamps), and a record whose facts were all already
// present is not rewritten - so an unchanged input queues zero writes and a
// second identical run is a full no-op.

// RecRef locates a recording by its work and recording slugs (the pack entry it
// sits in is derived from the pair on demand, never stored).
//
// It is exported so internal/issueform can share it rather than redeclare the
// same pair: both writers address a recording the same way, because a recording
// has no identity of its own - it is a key inside its work's composite entry.
type RecRef struct {
	Work string
	Rec  string
}

// planEnrich is the enrichment planning pass: each row is matched to a
// catalogued recording by ASIN and fills absent facts on it, on its work, and
// on the series it claims, stamped with the planner's run provenance.
func (p *planner) planEnrich(books []sourceBook) {
	for _, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		p.setSource(asin)
		p.enrichBook(b, asin)
		if p.fatal != nil {
			return
		}
	}
}

// enrichBook fills absent facts from one row onto the records its ASIN already
// matches. A row whose ASIN is not in the catalogue (including a row with no
// well-formed ASIN at all, which can never match) is counted and ignored.
//
// A row the matched recording CONTRADICTS is dropped whole (see
// recordingContradicts): the contradiction is the code's own evidence that this
// ASIN sits on a different production than the row describes, so the row's
// genres and series claim are no more trustworthy than its runtime.
func (p *planner) enrichBook(b sourceBook, asin string) {
	ref, matched := p.asinLoc[asin]
	if !matched {
		p.summary.NotInCatalog++
		return
	}
	p.summary.Matched++
	warn := p.bookWarn(b)
	if !p.applyToRecording(b, ref, warn, scopeFill) {
		return
	}
	p.applyToWork(b, ref.Work, scopeFill)
	p.enrichSeries(b, ref.Work, warn)
}

// applyScope is how much authority a row has over the record it matched, on top
// of the trust-tier decision. It exists because three call paths share one
// core for applying fields while differing in what they may do when the tier
// does NOT grant overwrite - and each of those three answers is deliberate.
type applyScope int

const (
	// scopeFill is the ENRICHMENT pass: fill facts the record does not carry
	// (and overwrite when the tier grants it). It is the only scope that writes
	// to a record the run may not overwrite.
	scopeFill applyScope = iota
	// scopeAttestExact is an EXACT-ASIN attestation from the create/
	// recordings-only dedup skip. It overwrites when the tier grants it and
	// otherwise writes nothing at all - a skip stays a skip, so a re-import of an
	// already-attested library is unchanged. The contradiction guard still runs,
	// because two people stating different facts about the SAME edition is the
	// case the policy asks to flag for review.
	scopeAttestExact
	// scopeAttestMerged is the ASIN-MERGE path, where the record was matched by
	// work and narrator set rather than by identifier and the row is a different
	// (usually regional) edition. It overwrites when the tier grants it and is
	// otherwise completely silent: a re-release's own release date differing from
	// the recorded one is expected, not a disagreement to flag.
	scopeAttestMerged
)

// applyToRecording applies one row's stated facts to the recording its ASIN
// matched, reporting whether the row was usable at all - false means nothing
// was applied from it here OR anywhere else, because it contradicted the record
// (or the record would not load, which has already set p.fatal).
//
// Which values win is the TRUST TIER's decision, made once for this record (see
// attest.go):
//
//   - the ordinary posture: only facts the record does not carry are filled;
//     a recorded value always wins.
//   - the overwrite posture (a user-library run against a bulk-mirror-only
//     record): a fact the row STATES replaces the recorded one, and the record
//     is written even when no field changed, because the run's source entry is
//     itself the attestation that ends the record's bulk-mirror-only status.
//
// scope narrows what a caller that is NOT enriching may do; see applyScope.
//
// Deliberately never touched in either posture: abridged (a tri-state whose
// absence the model cannot distinguish from a stated false, so a write could
// invent a fact), narrators (a differing credit list means a different
// production, not a fact to reconcile), asin[] (the row's own ASIN is by
// definition already recorded - that is how we found this record) and added_at
// (a creation stamp, not a fact an export states).
func (p *planner) applyToRecording(b sourceBook, ref RecRef, warn func(string, ...any), scope applyScope) (usable bool) {
	entry, raw := p.recordingRaw(ref.Work, ref.Rec)
	if raw == nil {
		return false
	}
	overwrite := p.overwrites(raw)
	// A merged-in row is a DIFFERENT edition of the same production, so on a
	// record it may not overwrite it has nothing to say: its regional release
	// date legitimately differs, and reporting that as a conflict would flag an
	// expected fact as a disagreement.
	if scope == scopeAttestMerged && !overwrite {
		return true
	}
	if p.recordingContradicts(b, raw, warn) {
		return false
	}
	// An exact-ASIN row about a record it may not overwrite has already had its
	// say: a disagreement was flagged just above, and its agreeing facts are
	// what the record states. The create path's skip stays a skip.
	if scope == scopeAttestExact && !overwrite {
		return true
	}
	changed := false

	// Runtime and release date reach here only in agreement (recordingContradicts
	// refused the row otherwise), so an overwrite is a precision improvement -
	// 601 minutes for a recorded 600, or a full date for a recorded year - never
	// a re-statement of a different production.
	if b.runtimeMin > 0 {
		cur, known := coerceInt(raw["runtime_min"])
		if !known || cur <= 0 || (overwrite && int(cur) != b.runtimeMin) {
			raw["runtime_min"] = b.runtimeMin
			changed = true
		}
	}

	if rd := b.str("release_date"); datePattern.MatchString(rd) && fillStr(raw, "release_date", rd, overwrite) {
		changed = true
	}

	// Publisher names are spelled a dozen ways across marketplaces and imprints,
	// so a differing one is noise rather than a conflict worth reporting - in the
	// ordinary posture the recorded spelling simply wins, silently.
	if fillStr(raw, "publisher", b.str("publisher"), overwrite) {
		changed = true
	}

	if img := b.str("image_url"); strings.HasPrefix(img, "https://") && fillStr(raw, "cover_url", img, overwrite) {
		changed = true
	}

	// Chapters are all-or-nothing: a recording's chapter list is one coherent
	// timeline, so it is written only when the record has none (or the tier
	// grants overwrite) AND the row's own chapter rows build cleanly. Merging two
	// sources' chapter tables would produce a timeline neither source states.
	if existing, _ := raw["chapters"].([]any); len(existing) == 0 || overwrite {
		if chs := buildChapters(b.chapterRows(), warn); len(chs) > 0 && !sameChapters(existing, chs) {
			raw["chapters"] = chs
			changed = true
		}
	}

	// ISBNs are only ever APPENDED, in every tier: they are identifiers, and
	// checkUniqueness makes them globally unique, so an overwrite would be a
	// retraction of someone else's identifier rather than a better value.
	if p.enrichISBNs(raw, b.isbns, warn) {
		changed = true
	}

	if !changed && !overwrite {
		return true
	}
	p.stampSource(raw)
	p.putWorkEntry(ref.Work, entry)
	if overwrite {
		p.summary.AttestedRecordings++
	} else {
		p.summary.EnrichedRecordings++
	}
	return true
}

// recordingContradicts reports whether the row disagrees with the recording its
// ASIN matched on a fact that is identity-adjacent: the runtime or the release
// date. Those two are how this code recognizes a DIFFERENT production, so a
// disagreement is not a value to reconcile - it is evidence the row is about
// another edition and must not be used to fill anything.
//
// Filling anyway is worse than it looks, because two of the fields are not
// repairable: chapters are all-or-nothing (a later, correct row finds them
// present and leaves them) and an ISBN is claimed GLOBALLY (a later row's
// identical ISBN is refused as a duplicate). Existing-value-wins means a wrong
// fill is permanent, so a contradicted row buys nothing and risks everything.
//
// It warns at most once - the first contradiction found is enough to disqualify
// the row, and a second line about the same row would only repeat that verdict.
//
// This guard holds in EVERY trust tier, which is what makes it the policy's
// "first wins, flag for review" rule as well as its safety net: when a second
// user's export disagrees with what a record already states, the recorded value
// stands, the row is refused whole, and the disagreement is counted
// (Summary.Conflicts) and warned about for a human to adjudicate. It is never
// resolved by letting the later writer win.
func (p *planner) recordingContradicts(b sourceBook, raw map[string]any, warn func(string, ...any)) bool {
	contradiction := func(format string, args ...any) bool {
		warn(format, args...)
		// Counted only for a user-library run: a bulk mirror disagreeing with the
		// catalogue is an expected data-quality artefact of the source, while a
		// person's own library disagreeing is the case a maintainer should look
		// at (and the intake bot reports).
		if p.userTier {
			p.summary.Conflicts++
		}
		return true
	}
	if b.runtimeMin > 0 {
		if cur, known := coerceInt(raw["runtime_min"]); known && cur > 0 && !runtimesCompatible(int(cur), b.runtimeMin) {
			return contradiction("runtime %d min conflicts with the recorded %d min; the row was not used for enrichment", b.runtimeMin, cur)
		}
	}
	if rd := b.str("release_date"); datePattern.MatchString(rd) {
		if cur := coerceStr(raw["release_date"]); cur != "" && datesConflict(cur, rd) {
			return contradiction("release date %s conflicts with the recorded %s; the row was not used for enrichment", rd, cur)
		}
	}
	return false
}

// sameChapters reports whether a recording's already-recorded chapter table is
// the one the row would write. It exists for the overwrite posture: replacing a
// table with an identical one would stamp and re-queue a record nothing changed
// on, which is exactly what makes a second identical run stop being a no-op.
func sameChapters(existing []any, incoming []outChapter) bool {
	if len(existing) != len(incoming) {
		return false
	}
	for i, e := range existing {
		m, _ := e.(map[string]any)
		start, sOK := coerceInt(m["start_ms"])
		length, lOK := coerceInt(m["length_ms"])
		if !sOK || !lOK || start != incoming[i].StartMS || length != incoming[i].LengthMS {
			return false
		}
		if coerceStr(m["title"]) != incoming[i].Title {
			return false
		}
	}
	return true
}

// datesConflict reports whether two release dates genuinely disagree. A release
// date is recorded at whatever precision its source stated ("2015", "2015-03",
// "2015-03-24"), so a value that is a PREFIX of the other is the same date at a
// different precision, not a contradiction - the recorded one still wins (a
// re-dating is a correction, not an enrichment), but it is not worth a warning.
// Without this the year-only dates already in the catalogue would each produce a
// warning per matched row, drowning the real disagreements.
func datesConflict(recorded, incoming string) bool {
	return !strings.HasPrefix(incoming, recorded) && !strings.HasPrefix(recorded, incoming)
}

// enrichISBNs appends the row's ISBNs that this recording does not already
// carry, reporting whether it changed the record. An ISBN already on THIS
// record is a silent no-op (it is the same edition's identifier, not a
// collision); one recorded on ANOTHER recording is refused with a warning by
// claimISBNs, since checkUniqueness requires ISBNs to be globally unique.
func (p *planner) enrichISBNs(raw map[string]any, isbns []string, warn func(string, ...any)) bool {
	if len(isbns) == 0 {
		return false
	}
	existing, _ := raw["isbn"].([]any)
	have := make(map[string]bool, len(existing))
	for _, v := range existing {
		if s := coerceStr(v); s != "" {
			have[strings.ToUpper(s)] = true
		}
	}
	var candidates []string
	for _, isbn := range isbns {
		if !have[strings.ToUpper(isbn)] {
			candidates = append(candidates, isbn)
		}
	}
	fresh := p.claimISBNs(candidates, warn)
	if len(fresh) == 0 {
		return false
	}
	appendISBNs(raw, fresh)
	return true
}

// applyToWork applies a row's stated facts to the work the matched recording
// belongs to - today only genres, mapped from the source's own strings onto this
// project's vocabulary (LICENSING.md forbids storing a retailer's taxonomy
// verbatim). Genres are a SET, never an accreting union: in the ordinary posture
// they are written only when the work has none, and in the overwrite posture the
// row's mapped set replaces the recorded one wholesale, so the result stays
// crisp and rerun-deterministic either way. A row whose genres map to nothing
// never clears a recorded set - silence is not an assertion.
//
// A user-library run attests a bulk-mirror-only work even when it changes no
// field: the source entry is the attestation. That is also why the early return
// for a genre-less row is conditional on the run's tier - a libex enrichment run
// still pays no read at all for a row with no genres.
//
// Deliberately never touched: title and authors (identity - a change is a
// correction, not an import), first_published and xref (facts a retailer row
// does not state), description (community-written, never imported), subtitle (an
// Audible subtitle is marketing or series copy, not the edition's own subtitle -
// see libexToBook), and added_at (a creation stamp).
func (p *planner) applyToWork(b sourceBook, workSlug string, scope applyScope) {
	if len(b.genres) == 0 && !p.userTier {
		return
	}
	raw := p.workEntryRaw(workSlug)
	if raw == nil {
		return
	}
	overwrite := p.overwrites(raw)
	if scope != scopeFill && !overwrite {
		return
	}
	changed := false
	existing, _ := raw["genres"].([]any)
	if len(b.genres) > 0 && (len(existing) == 0 || overwrite) {
		// Mapped only on the branch that can store the result, so a row whose
		// genres could never be recorded adds nothing to the unmapped-genre report.
		if mapped := p.genres.mapGenres(b.genres, p.unmappedGenres); len(mapped) > 0 && !sameStrings(existing, mapped) {
			raw["genres"] = mapped
			changed = true
		}
	}
	if !changed && !overwrite {
		return
	}
	p.stampSource(raw)
	// The read-modify-write is on the whole composite, so the work's recordings
	// ride along untouched; added_at is left as found (nothing here creates).
	p.putWorkEntry(workSlug, raw)
	if overwrite {
		p.summary.AttestedWorks++
	} else {
		p.summary.EnrichedWorks++
	}
}

// sameStrings reports whether a recorded string array is the one a run would
// write. Like sameChapters, it keeps the overwrite posture from re-queueing a
// record whose value is already what the row states.
func sameStrings(existing []any, incoming []string) bool {
	if len(existing) != len(incoming) {
		return false
	}
	for i, e := range existing {
		if coerceStr(e) != incoming[i] {
			return false
		}
	}
	return true
}

// enrichSeries places the matched work into the series it claims - but only
// into series the catalogue ALREADY holds (enrichment never creates one) and
// only when the work is not a member yet. A membership that already exists is
// left exactly as it is, at whatever position it records: re-positioning a
// catalogued work is a correction, not an enrichment.
func (p *planner) enrichSeries(b sourceBook, workSlug string, warn func(string, ...any)) {
	for _, r := range b.series {
		ss := p.findSeries(r.name)
		if ss == nil {
			continue
		}
		if _, member := ss.members[workSlug]; member {
			continue
		}
		if !r.seqOK {
			warn("series %q: missing or invalid position %q; not placed in series", r.name, r.rawSeq)
			continue
		}
		before := len(ss.members)
		p.addToSeries(r.name, workSlug, r.seq, warn)
		if len(ss.members) == before {
			continue // a position clash; addToSeries already warned
		}
		p.summary.SeriesPlacements++
		// finalizeSeries emits the record; stamp provenance on the raw form
		// addToSeries loaded (a series created earlier this run carries its
		// stamp already, but enrichment never creates one).
		if !ss.isNew && ss.raw != nil {
			p.stampSource(ss.raw)
		}
	}
}
