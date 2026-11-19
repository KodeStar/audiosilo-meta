package query

import (
	"net/http"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func (s *handler) handleSeriesListeningGuide(w http.ResponseWriter, r *http.Request, snap *DB) {
	q := r.URL.Query()
	d, err := snap.SeriesListeningGuide(r.Context(), r.PathValue(idWildcard), clampLimit(q.Get("limit"), 50, CataloguePageMax), clampOffset(q.Get("offset")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if d == nil {
		if s.redirected(w, r, snap) {
			return
		}
		httpx.WriteErr(w, http.StatusNotFound, "series not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (s *handler) handlePersonRecordings(w http.ResponseWriter, r *http.Request, snap *DB) {
	q := r.URL.Query()
	seriesID := q.Get("series")
	if seriesID != "" && !model.ValidSlug(seriesID) {
		httpx.WriteErr(w, http.StatusBadRequest, "series: invalid series slug")
		return
	}
	language := strings.Join(q["lang"], ",")
	if _, err := parseLangFilter(language); err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := snap.PersonRecordings(r.Context(), r.PathValue(idWildcard), clampLimit(q.Get("limit"), 50, CataloguePageMax), clampOffset(q.Get("offset")), seriesID, language)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if d == nil {
		if s.redirected(w, r, snap) {
			return
		}
		httpx.WriteErr(w, http.StatusNotFound, "person not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}
