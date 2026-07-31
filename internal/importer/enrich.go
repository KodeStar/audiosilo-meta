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
	if !p.enrichRecording(b, ref, warn) {
		return
	}
	p.enrichWork(b, ref.Work)
	p.enrichSeries(b, ref.Work, warn)
}

// enrichRecording fills the recording facts the matched record does not carry,
// reporting whether the row was usable at all - false means nothing was filled
// from it here OR anywhere else, because it contradicted the record (or the
// record would not load, which has already set p.fatal). A present value is
// never replaced.
//
// Deliberately never touched: abridged (a tri-state whose absence the model
// cannot distinguish from a stated false, so a fill could invent a fact),
// narrators (a differing credit list means a different production, not a missing
// fact), and asin[] (the row's own ASIN is by definition already recorded -
// that is how we found this file).
func (p *planner) enrichRecording(b sourceBook, ref RecRef, warn func(string, ...any)) (usable bool) {
	entry, raw := p.recordingRaw(ref.Work, ref.Rec)
	if raw == nil {
		return false
	}
	if p.recordingContradicts(b, raw, warn) {
		return false
	}
	changed := false

	if b.runtimeMin > 0 {
		if cur, known := coerceInt(raw["runtime_min"]); !known || cur <= 0 {
			raw["runtime_min"] = b.runtimeMin
			changed = true
		}
	}

	if rd := b.str("release_date"); datePattern.MatchString(rd) && fillStr(raw, "release_date", rd) {
		changed = true
	}

	// Publisher names are spelled a dozen ways across marketplaces and imprints,
	// so a differing one is noise rather than a conflict worth reporting - the
	// recorded spelling simply wins.
	if fillStr(raw, "publisher", b.str("publisher")) {
		changed = true
	}

	if img := b.str("image_url"); strings.HasPrefix(img, "https://") && fillStr(raw, "cover_url", img) {
		changed = true
	}

	// Chapters are all-or-nothing: a recording's chapter list is one coherent
	// timeline, so it is filled only when the record has none AND the row's own
	// chapter rows build cleanly. Merging two sources' chapter tables would
	// produce a timeline neither source states.
	if existing, _ := raw["chapters"].([]any); len(existing) == 0 {
		if chs := buildChapters(b.chapterRows(), warn); len(chs) > 0 {
			raw["chapters"] = chs
			changed = true
		}
	}

	if p.enrichISBNs(raw, b.isbns, warn) {
		changed = true
	}

	if !changed {
		return true
	}
	p.stampSource(raw)
	p.putWorkEntry(ref.Work, entry)
	p.summary.EnrichedRecordings++
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
func (p *planner) recordingContradicts(b sourceBook, raw map[string]any, warn func(string, ...any)) bool {
	if b.runtimeMin > 0 {
		if cur, known := coerceInt(raw["runtime_min"]); known && cur > 0 && !runtimesCompatible(int(cur), b.runtimeMin) {
			warn("runtime %d min conflicts with the recorded %d min; the row was not used for enrichment", b.runtimeMin, cur)
			return true
		}
	}
	if rd := b.str("release_date"); datePattern.MatchString(rd) {
		if cur := coerceStr(raw["release_date"]); cur != "" && datesConflict(cur, rd) {
			warn("release date %s conflicts with the recorded %s; the row was not used for enrichment", rd, cur)
			return true
		}
	}
	return false
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

// enrichWork fills the work facts the matched work does not carry - today only
// genres, mapped from the source's own strings onto this project's vocabulary
// (LICENSING.md forbids storing a retailer's taxonomy verbatim). Genres are
// set only when the work has none, so the result is crisp and rerun-deterministic
// rather than an accreting union.
//
// Deliberately never touched: title and authors (identity - a change is a
// correction, not an enrichment), first_published and xref (facts a retailer row
// does not state), description (community-written, never imported), and subtitle
// (an Audible subtitle is marketing or series copy, not the edition's own
// subtitle - see libexToBook).
func (p *planner) enrichWork(b sourceBook, workSlug string) {
	if len(b.genres) == 0 {
		return
	}
	raw := p.workEntryRaw(workSlug)
	if raw == nil {
		return
	}
	if existing, _ := raw["genres"].([]any); len(existing) > 0 {
		return
	}
	// Mapped only on the branch that can store the result, so a row whose genres
	// could never be recorded adds nothing to the unmapped-genre report.
	mapped := p.genres.mapGenres(b.genres, p.unmappedGenres)
	if len(mapped) == 0 {
		return
	}
	raw["genres"] = mapped
	p.stampSource(raw)
	// The read-modify-write is on the whole composite, so the work's recordings
	// ride along untouched; added_at is left as found (enrichment never creates).
	p.putWorkEntry(workSlug, raw)
	p.summary.EnrichedWorks++
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
