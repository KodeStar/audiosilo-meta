package query

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// librivox.go is the API's side of the LibriVox layer (librivoxSchemaVersion):
// the ?source= filter on the list surfaces, lookup?librivox_id=, and the load
// gate proving what a version 8 artifact claims. The LibriVox-shaped feed is
// librivoxfeed.go.

// librivoxShapeSQL proves the columns and tables a version 8 artifact claims:
// each statement names them and reads no row (LIMIT 0), so a missing one fails
// the prepare - the whole check, as for seriesShapeSQL.
var librivoxShapeSQL = []string{
	`SELECT librivox_id, archive_id, link_librivox, link_zip, link_rss, link_text, copyright_year, added_at FROM recordings LIMIT 0`,
	`SELECT audio_url FROM chapters LIMIT 0`,
	`SELECT librivox_reader_id, librivox_author_id, birth_year, death_year FROM people LIMIT 0`,
	`SELECT work_id, person_id, role, ord FROM work_credits LIMIT 0`,
	`SELECT work_id, recording_id, type FROM recording_sources LIMIT 0`,
}

// ---- the source filter --------------------------------------------------------

// The SOURCE FILTER (?source=<type>): a reader who wants only LibriVox's free
// recordings asks for `source=librivox`. It narrows the work lists - the combined
// and work searches (both boosts included) and works/latest - to works with at
// least one recording whose sources[] names that type, through the
// recording_sources table (idx_recording_sources_type). Like ?lang= it narrows
// LISTS only; a record page or a lookup names its record outright.
//
// The value is one source type the schema's enum names (model.KnownSourceType);
// anything else is a 400, because a typo that silently filtered to nothing would
// read as "the catalogue holds no such books". On the combined search it scopes
// the page to works: a person or a series has no recording to judge. An artifact
// older than librivoxSchemaVersion has no recording_sources table, so a filtered
// request there answers an empty list - never the unfiltered one, which would
// claim every work came from that source.

// worksWithSourceArm is the predicate a filtered list adds over a works id
// column: the works that have a recording of the source type in its one argument.
const worksWithSourceArm = `id IN (SELECT work_id FROM recording_sources WHERE type = ?)`

// sourceParam reads a request's ?source= value, answering the 400 itself when it
// names no known source type. "" is no filter.
func sourceParam(w http.ResponseWriter, q url.Values) (string, bool) {
	src := strings.TrimSpace(q.Get("source"))
	if src == "" || model.KnownSourceType(src) {
		return src, true
	}
	httpx.WriteErr(w, http.StatusBadRequest, fmt.Sprintf("source: %q is not a source type (for example: librivox)", src))
	return "", false
}

// hasLibrivoxLayer reports whether this artifact carries the LibriVox layer
// (librivoxSchemaVersion): the source filter, the LibriVox columns and the feed.
func (s *DB) hasLibrivoxLayer() bool { return s.schemaVersion >= librivoxSchemaVersion }

// ---- lookup?librivox_id= ------------------------------------------------------

// librivoxLookupSQL resolves a LibriVox project id to its recording through
// idx_recordings_librivox. pkg/check holds the id unique across the dataset; the
// ORDER BY only makes a corrupt artifact's answer deterministic.
const librivoxLookupSQL = `SELECT work_id, id FROM recordings WHERE librivox_id = ? ORDER BY work_id, id LIMIT 1`

// parseLibrivoxID reads a project id parameter: a positive integer.
func parseLibrivoxID(raw string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id < 1 {
		return 0, fmt.Errorf("librivox_id: %q is not a LibriVox project id", raw)
	}
	return id, nil
}

// lookupLibrivox finds the recording a LibriVox project id names, as lookup's
// result shape, or nil when the artifact holds none (an artifact older than
// librivoxSchemaVersion holds none by construction).
func (s *DB) lookupLibrivox(ctx context.Context, id int) (*lookupResult, error) {
	if !s.hasLibrivoxLayer() {
		return nil, nil
	}
	var workID, rid string
	if err := s.db.QueryRowContext(ctx, librivoxLookupSQL, id).Scan(&workID, &rid); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	card, err := s.workCard(ctx, workID)
	if err != nil || card == nil {
		return nil, err
	}
	return &lookupResult{Work: card, RecordingID: rid}, nil
}
