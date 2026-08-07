package serve

import (
	_ "embed"
	"net/http"
)

// openAPISpec is the machine-readable description of this API. It is hand-
// authored rather than generated: the handlers return map[string]any envelopes
// around hand-written structs, so nothing could generate it faithfully, and a
// spec that drifts is worse than none. Two things keep it honest - the response
// schemas are derived from the structs in this package, and
// TestOpenAPICoversEveryRoute pins its path set to buildMux's registrations, so
// a route added on one side only fails the build.
//
// It has a second consumer: the site's /docs/api page imports this very file at
// build time and renders it in the site's own design system (see
// site/src/pages/docs/api.astro), so the human docs and the served spec are the
// same document.
//
//go:embed openapi.json
var openAPISpec []byte

// handleOpenAPI serves the embedded spec. It reads no snapshot and touches no
// database, so it is registered outside s.api's loaded-artifact gate: a client
// discovering the API must be able to fetch the contract even on a boot that is
// still downloading its first release.
func handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPISpec)
}
