package issueform

import (
	"fmt"
	"os"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// Field labels for import-library.yml.
const (
	fImportType       = "Export type"
	fImportAttachment = "Export file"
	// fImportAttachmentLegacy is the attachment field's label before it was
	// renamed to say what goes in it. GitHub renders the label an issue was
	// OPENED with, so an issue filed under the old template keeps this heading -
	// including every edit that re-runs the bot on it - and it is still read.
	fImportAttachmentLegacy = "Additional notes"
)

// importLibrary ingests an attached OpenAudible/Libation/Audiobookshelf export
// via the bulk importer (which writes and validates the tree itself).
// Folder-scan and unknown exports are left to a human. The importer writes to
// disk itself, so Process dispatches this path ahead of the generic flush.
func (c *composer) importLibrary(s sections) {
	// Check the dropdown FIRST, with a pure predicate and no fetch: an export
	// type the intake bot cannot ingest (folder scan or anything unknown) is a
	// maintainer's job, so fail needs-human before spending a fetch. Only the
	// three supported types proceed to fetch + envelope sniff, where a self-
	// identifying audiosilo-books envelope is still trusted over the dropdown so a
	// mis-click AMONG the supported three imports correctly.
	exportType := s.get(fImportType)
	if !supportedExportType(exportType) {
		c.fail(StatusNeedsHuman, "%q exports are ingested by a maintainer running metascan/metaimport, not the intake bot", exportType)
		return
	}

	raw, ok := c.attachmentBytes(s.firstRaw(fImportAttachment, fImportAttachmentLegacy), maxImportAttachmentBytes)
	if !ok {
		return
	}

	run := c.selectImporter(exportType, raw)
	if run == nil {
		return // c.fail already set (needs-human)
	}

	tmp, err := os.CreateTemp("", "metaissue-import-*.json")
	if err != nil {
		c.fail(StatusInvalid, "create temp file: %v", err)
		return
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		c.fail(StatusInvalid, "write temp file: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		c.fail(StatusInvalid, "close temp file: %v", err)
		return
	}

	// The PROFILE travels with the root. This path is the one template that
	// hands the tree to another writer, so without it a scoped root would be
	// opened and validated as ProfileAll - the one reading under which a leftover
	// family in the wrong repository looks clean.
	// The SERIES LOOKUP travels the same way, and only from here: a library
	// export is exactly the source that states a series with no part number, and
	// the lookup is what places those works instead of warning about them
	// (internal/importer/seriespos.go). nil unless the caller asked for it.
	sum, err := run(tmpName, importer.Options{
		DataDir:           c.dataDir,
		Profile:           c.profile,
		ImportDate:        c.date,
		SeriesLookup:      c.seriesLookup,
		SeriesLookupLimit: c.seriesLookupLimit,
		Distinct:          audit.ReviewedDistinct(),
	})
	if err != nil {
		c.fail(StatusInvalid, "import failed: %v", err)
		return
	}
	// Summary.Produced is what "the run changed something" means, defined next to
	// the counters it reads. Notably it counts ATTESTATIONS: a library export
	// whose every book is already catalogued still changes the tree when those
	// records were libex mirror seeds, because the submitter's data replaced
	// theirs (LICENSING.md's trust tiers). Reporting that as a duplicate would
	// open no pull request and silently discard the takeover.
	if sum.Produced() == 0 {
		// A run that produced nothing but DISAGREED with the catalogue is not a
		// duplicate, and closing it as one would throw away the one thing it did
		// say. A submitter whose export contradicts a recorded runtime or release
		// date has stated a fact about a book they own; the importer refused the
		// row (first writer wins) and only a maintainer can adjudicate it, so the
		// verdict is needs-human with the same warnings the ok path emits.
		if sum.Conflicts > 0 {
			c.fail(StatusNeedsHuman, "nothing new to import, but %d row(s) of the export disagree with the catalog", sum.Conflicts)
			c.noteConflicts(sum)
			c.noteImportWarnings(sum)
			return
		}
		// A run whose books were refused by the DUPLICATE-IDENTITY guard is not a
		// duplicate either, for the same reason a conflict is not: the export named
		// books the catalogue holds under differently-spelled titles, which is a pair
		// only a maintainer can resolve (internal/importer/dupidentity.go skips rather
		// than merging, on purpose). Checked before the Skipped branch because it is
		// the actionable half of a run that did both.
		if sum.SkippedDuplicateIdentity > 0 {
			c.fail(StatusNeedsHuman, "nothing new to import: %d book(s) of the export are already in the catalog under "+
				"differently-spelled titles, which a maintainer resolves rather than the bot", sum.SkippedDuplicateIdentity)
			c.noteImportWarnings(sum)
			return
		}
		// Books skipped because their names cannot become ids are the same kind
		// of answer: the file is fine (claiming a type mismatch would be false),
		// the books are not duplicates, and only a maintainer can place them.
		// Checked before the Skipped branch, which would otherwise call a run that
		// deduped some books and skipped the rest "already in the catalog".
		if sum.SkippedUnsluggable > 0 {
			c.fail(StatusNeedsHuman, "nothing new to import: %s", unsluggableSkips(sum.SkippedUnsluggable))
			c.noteImportWarnings(sum)
			return
		}
		if sum.Skipped > 0 {
			// Genuinely nothing new: every book deduped against the catalog.
			c.fail(StatusDuplicate, "nothing new to import: every book in the export is already in the catalog (%d skipped)", sum.Skipped)
			if sum.ISBNsMirrorOnly > 0 {
				c.note("%s", isbnsMirrorOnly(sum.ISBNsMirrorOnly))
			}
			return
		}
		// Books the catalogue holds under a mirror seed, whose only news was an ISBN
		// nothing identifies them by: the file is fine, so this is not the
		// export-type question below, and the answer is the submitter's to act on.
		if sum.ISBNsMirrorOnly > 0 {
			c.fail(StatusDuplicate, "nothing new to import: %s", isbnsMirrorOnly(sum.ISBNsMirrorOnly))
			c.noteImportWarnings(sum)
			return
		}
		// Nothing was produced AND nothing deduped. Distinguish two honest cases:
		// entries that fell out (warnings) likely mean the file does not match the
		// selected export type; an export with NO warnings simply had no importable
		// books, and claiming a type mismatch there would be false.
		if len(sum.Warnings) > 0 {
			c.fail(StatusNeedsHuman, "no importable books were found in the export - the file may not match the selected export type")
			for _, w := range falloutExamples(sum) {
				c.note("import warning: %s", w)
			}
		} else {
			c.fail(StatusNeedsHuman, "the export contains no importable books")
		}
		return
	}

	c.status = StatusOK
	c.note("imported %d works, %d recordings, %d people, %d series (%d already present)",
		sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.NewSeries, sum.Skipped)
	if sum.MergedASINs > 0 {
		c.note("merged %d re-release ASIN(s) into existing recordings", sum.MergedASINs)
	}
	if sum.SkippedDuplicateIdentity > 0 {
		// Reported on the ok path too: the run landed records AND declined some, and
		// the declined ones are a review queue a maintainer should see in the pull
		// request rather than only in the run's warnings.
		c.note("%d book(s) were not imported because the catalog already holds them under differently-spelled titles - "+
			"see the warnings below; a maintainer resolves those pairs", sum.SkippedDuplicateIdentity)
	}
	if sum.SkippedUnsluggable > 0 {
		c.note("%s - see the warnings below", unsluggableSkips(sum.SkippedUnsluggable))
	}
	if sum.AttestedWorks+sum.AttestedRecordings > 0 {
		// The summary counts the attested records without their sources, so the
		// note names the tier rather than a mirror it cannot see.
		c.note("attested %d work(s) and %d recording(s) that were previously seeded from %s - your library's values replaced the mirror's",
			sum.AttestedWorks, sum.AttestedRecordings, seededFrom(nil))
	}
	if sum.GenreWorks > 0 {
		c.note("added your library's genres to %d work(s) another library had already described", sum.GenreWorks)
	}
	if sum.ISBNRecordings > 0 {
		c.note("added your library's ISBNs to %d recording(s) your export had nothing else to add to", sum.ISBNRecordings)
	}
	if sum.ISBNsMirrorOnly > 0 {
		c.note("%s", isbnsMirrorOnly(sum.ISBNsMirrorOnly))
	}
	c.noteConflicts(sum)
	c.noteImportWarnings(sum)
	// The importer wrote and validated the tree itself, so the file list is the
	// one ITS flush reports, in the field the compose path's own flush fills.
	c.wrote = sum.Files
}

// isbnsMirrorOnly is the verdict's account of Summary.ISBNsMirrorOnly, on either
// outcome: an export with other news and one with none.
func isbnsMirrorOnly(n int) string {
	return fmt.Sprintf("isbn-not-attached: mirror-only: %d book(s) with no ASIN matched a recording only %s has described, so their ISBNs were not added; "+
		"a row with the ASIN carries the ISBN", n, seededFrom(nil))
}

// unsluggableSkips is the verdict's plain account of the books skipped because
// every author or narrator name slugs to nothing (Summary.SkippedUnsluggable).
func unsluggableSkips(n int) string {
	return fmt.Sprintf("%d book(s) were skipped: their author or narrator names could not be turned into ids "+
		"(written in a non-Latin script, which the catalogue cannot address yet); those books need a maintainer", n)
}

// falloutExamples is the evidence behind "the file may not match the selected
// export type": the first few PER-ROW warnings, which say why individual entries
// fell out (no narrator, no ASIN, ...) - the signature of a mismatched file. The
// importer ranks its run-level lines first, so the head of Warnings would show
// summaries instead; a run with no row lines at all falls back to them.
func falloutExamples(sum importer.Summary) []string {
	lines := sum.RowWarnings()
	if len(lines) == 0 {
		lines = sum.Warnings
	}
	return lines[:min(5, len(lines))]
}

// noteConflicts reports the rows the importer refused because they disagreed
// with a recorded value. It is "flag for review", not a rejection: each row was
// refused individually (the recorded value stands) and a maintainer adjudicates
// from this note during the review the bot never bypasses. Shared by the two
// outcomes that can carry conflicts - a run that also produced records (ok) and
// one whose only effect was the disagreement (needs-human) - so the submitter
// reads the same sentence either way.
func (c *composer) noteConflicts(sum importer.Summary) {
	if sum.Conflicts == 0 {
		return
	}
	c.note("%d row(s) disagreed with a value already recorded and were not applied - the recorded value stands; see the warnings below and adjudicate before merging",
		sum.Conflicts)
}

// noteImportWarnings echoes the importer's per-row warnings, which are what name
// the records a conflict note refers to, and the run's NOTES - what it decided
// rather than what went wrong. The AI-narration fold is the note that matters
// here: a submitter whose library is AI-narrated should read in the pull request
// that those books were admitted under one synthetic record, not wonder why
// their narrator is not in the catalogue.
func (c *composer) noteImportWarnings(sum importer.Summary) {
	for _, n := range sum.Notes {
		c.note("import note: %s", n)
	}
	c.noteUnrecordedCredits(sum.UnrecordedCredits)
	for _, w := range sum.Warnings {
		c.note("import warning: %s", w)
	}
}

// maxUnrecordedNotes caps the per-credit lines noteUnrecordedCredits writes, so
// a large library whose books mostly join catalogued works cannot bury the rest
// of the verdict; the count line always states the whole number.
const maxUnrecordedNotes = 20

// noteUnrecordedCredits reports the credits the import withheld
// (importer.Summary.UnrecordedCredits), for one of two reasons the per-credit
// line states. A book of the export attached to a work that does not name an
// author, translator or other contributor it states, and a library import never
// edits a catalogued work's credits, so the credit was not recorded and a person
// not already catalogued was not created ("person not created"). Or the name -
// an author's, a contributor's or a narrator's - slugs to nothing ("unsluggable",
// meta#2778): no record can hold it, the shared catch-all person is not them, so
// the book was imported with that credit left off. They are notes, not warnings
// - the book landed - but a maintainer should see them in the pull request,
// since adding the credit is a correction only a person makes.
func (c *composer) noteUnrecordedCredits(credits []importer.UnrecordedCredit) {
	if len(credits) == 0 {
		return
	}
	c.note("import note: %d credit(s) were not recorded - the book attached to a work that does not name them, "+
		"whose credits a library import never edits, so a person not already catalogued was not created "+
		"(kept for recording-level credits, meta#2752); or the name slugs to nothing (unsluggable), "+
		"so the book was imported with that credit left off rather than credited to a shared catch-all", len(credits))
	for _, u := range credits[:min(maxUnrecordedNotes, len(credits))] {
		c.note("credit not recorded: %s", u)
	}
	if extra := len(credits) - maxUnrecordedNotes; extra > 0 {
		c.note("credit not recorded: ... and %d more", extra)
	}
}

// supportedExportType reports whether an import export-type dropdown selection
// is one the intake bot can ingest (OpenAudible, Libation, or Audiobookshelf).
// Folder-scan and any unknown selection are a maintainer's job. It is a pure
// predicate so importLibrary can reject an unsupported type before fetching the
// attachment.
func supportedExportType(exportType string) bool {
	lower := strings.ToLower(exportType)
	return strings.Contains(lower, "openaudible") ||
		strings.Contains(lower, "libation") ||
		strings.Contains(lower, "audiobookshelf")
}

// selectImporter chooses the importer entrypoint for an attachment. A self-
// identifying audiosilo-books envelope routes to RunAudiosiloBooks regardless of
// the dropdown (the file names its own format); otherwise the dropdown selection
// routes. Callers reach this only for a supportedExportType selection, so the
// default is a defensive fallback.
func (c *composer) selectImporter(exportType string, raw []byte) func(string, importer.Options) (importer.Summary, error) {
	if importer.IsAudiosiloBooksEnvelope(raw) {
		return importer.RunAudiosiloBooks
	}
	switch lower := strings.ToLower(exportType); {
	case strings.Contains(lower, "openaudible"):
		return importer.Run
	case strings.Contains(lower, "libation"):
		return importer.RunLibation
	case strings.Contains(lower, "audiobookshelf"):
		// The site's /import page builds the audiosilo-books envelope from an
		// Audiobookshelf export (a bare ABS item export is not attachable here).
		return importer.RunAudiosiloBooks
	default:
		c.fail(StatusNeedsHuman, "%q exports are ingested by a maintainer running metascan/metaimport, not the intake bot", exportType)
		return nil
	}
}
