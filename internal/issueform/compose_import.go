package issueform

import (
	"os"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// Field labels for import-library.yml.
const (
	fImportType       = "Export type"
	fImportAttachment = "Additional notes"
)

// importLibrary ingests an attached OpenAudible/Libation/Audiobookshelf export
// via the bulk importer (which writes and validates the tree itself).
// Folder-scan and unknown exports are left to a human. This path writes directly
// to disk and sets handled, so Process returns its Result verbatim.
func (c *composer) importLibrary(s sections) {
	c.handled = true

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

	raw, ok := c.attachmentBytes(s.get(fImportAttachment))
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

	sum, err := run(tmpName, importer.Options{DataDir: c.dataDir, ImportDate: c.date})
	if err != nil {
		c.fail(StatusInvalid, "import failed: %v", err)
		return
	}
	newCount := sum.NewWorks + sum.NewRecordings + sum.NewPeople + sum.NewSeries + sum.MergedASINs
	if newCount == 0 {
		if sum.Skipped > 0 {
			// Genuinely nothing new: every book deduped against the catalog.
			c.fail(StatusDuplicate, "nothing new to import: every book in the export is already in the catalog (%d skipped)", sum.Skipped)
			return
		}
		// Nothing was produced AND nothing deduped. Distinguish two honest cases:
		// entries that fell out (warnings) likely mean the file does not match the
		// selected export type; an export with NO warnings simply had no importable
		// books, and claiming a type mismatch there would be false.
		if len(sum.Warnings) > 0 {
			c.fail(StatusNeedsHuman, "no importable books were found in the export - the file may not match the selected export type")
			for _, w := range sum.Warnings[:min(5, len(sum.Warnings))] {
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
	for _, w := range sum.Warnings {
		c.note("import warning: %s", w)
	}
	// The importer wrote and validated the tree; the intake workflow diffs it
	// to build the pull request, so an explicit file list is not needed here.
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
