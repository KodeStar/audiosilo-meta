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
	// The wildcard is answered here rather than deferred the way the entity pages
	// defer it: this representation is embedded in the binary, so "a current
	// representation exists" is true by construction and costs nothing to know.
	inm := r.Header.Get("If-None-Match")
	if matchesETag(inm, etag) || anyValidator(inm) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(openAPISpec)
}

// matchesETag reports whether an If-None-Match header NAMES etag. It handles the
// comma-separated list, and compares with the WEAK comparison function the spec
// mandates for If-None-Match: a "W/" prefix is ignored on BOTH sides, so a weak
// validator (the entity pages issue one - see entityETag) matches the header it
// was itself sent in, and a strong one (the spec route's content hash) still
// matches a weakened echo of it.
//
// The "*" wildcard is deliberately NOT a match here. Per RFC 9110 13.1.2 it
// matches only when a current representation EXISTS, which is a question about
// the resource and not about the header - so the callers answer it themselves
// (see anyValidator) and this function stays a comparison.
func matchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == want {
			return true
		}
	}
	return false
}

// anyValidator reports whether an If-None-Match header carries the "*" wildcard.
// It says only that the client asked "304 if this resource has any current
// representation" - whether it HAS one is the caller's to establish, which is
// what keeps an unknown or retired id off a 304 it would otherwise get for free.
func anyValidator(header string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(candidate) == "*" {
			return true
		}
	}
	return false
}
