package serve

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// openAPISpec is the machine-readable description of this API. It is hand-
// authored rather than generated: the handlers return map[string]any envelopes
// around hand-written structs, so nothing could generate it faithfully, and a
// spec that drifts is worse than none. What keeps it honest is
// TestOpenAPICoversEveryRoute, which diffs its path set against the very route
// table buildMux registers (see Server.routes), so a route added on one side
// only fails the build. The response SHAPES are a matter of authoring
// discipline: nothing checks them against the structs, so a field added to a
// response struct has to be added here in the same change.
//
// It has a second consumer: the site's /docs/api page imports this very file at
// build time and renders it in the site's own design system (see
// site/src/pages/docs/api.astro), so the human docs and the served spec are the
// same document.
//
//go:embed openapi.json
var openAPISpec []byte

// specETag is the spec's content hash, computed once. The document is embedded
// and therefore constant for the life of the binary, so a conditional request
// can be answered from the hash without touching the bytes.
var specETag = sync.OnceValue(func() string {
	sum := sha256.Sum256(openAPISpec)
	return `"` + hex.EncodeToString(sum[:]) + `"`
})

// specMaxAge is how long a client may reuse the spec without revalidating. It
// changes only when a new binary is deployed, so an hour costs a stale contract
// at most that long and spares the docs tooling a fetch per page load.
const specMaxAge = "public, max-age=3600"

// handleOpenAPI serves the embedded spec. It reads no snapshot and touches no
// database, so it is registered outside the loaded-artifact gate: a client
// discovering the API must be able to fetch the contract even on a boot that is
// still downloading its first release.
//
// The bytes are written RAW, not re-encoded: the file is pretty-printed for the
// human who curls it, and routing it through an encoder would compact it.
// Compression is the gzip middleware's job (see Server.public), so nothing here
// negotiates content encoding.
func handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	etag := specETag()
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("ETag", etag)
	h.Set("Cache-Control", specMaxAge)
	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(openAPISpec)
}

// matchesETag reports whether an If-None-Match header names etag. It handles the
// comma-separated list and the "*" wildcard RFC 9110 allows, and compares
// weakly (a "W/" prefix is ignored), which is what a cache revalidating a body
// this handler never varies needs.
func matchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
