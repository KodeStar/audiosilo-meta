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

	exportType := strings.ToLower(s.get(fImportType))
	var run func(string, importer.Options) (importer.Summary, error)
	switch {
	case strings.Contains(exportType, "openaudible"):
		run = importer.Run
	case strings.Contains(exportType, "libation"):
		run = importer.RunLibation
	case strings.Contains(exportType, "audiobookshelf"):
		// The site's /import page builds the audiosilo-books envelope from an
		// Audiobookshelf export (a bare ABS item export is not attachable here).
		run = importer.RunAudiosiloBooks
	default:
		c.fail(StatusNeedsHuman, "%q exports are ingested by a maintainer running metascan/metaimport, not the intake bot", s.get(fImportType))
		return
	}

	raw, ok := c.attachmentBytes(s.get(fImportAttachment))
	if !ok {
		return
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
	newCount := sum.NewWorks + sum.NewRecordings + sum.NewPeople + sum.NewSeries
	if newCount == 0 {
		c.fail(StatusDuplicate, "nothing new to import: every book in the export is already in the catalog (%d skipped)", sum.Skipped)
		return
	}

	c.status = StatusOK
	c.note("imported %d works, %d recordings, %d people, %d series (%d already present)",
		sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.NewSeries, sum.Skipped)
	for _, w := range sum.Warnings {
		c.note("import warning: %s", w)
	}
	// The importer wrote and validated the tree; the intake workflow diffs it
	// to build the pull request, so an explicit file list is not needed here.
}
