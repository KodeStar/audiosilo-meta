package serve

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
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

// specDoc is the document one server serves and its content hash, computed once
// (see Server.servedSpec). It is constant for the life of the server, so a
// conditional request can be answered from the hash without touching the bytes.
type specDoc struct {
	body []byte
	etag string
}

// servedSpec is the embedded document as this deployment answers it. The file
// describes the whole surface, and that is what TestOpenAPICoversEveryRoute and
// the site's docs page read. A deployment that does not offer the intake queue
// must not advertise it, though: with the env unset, its spec has no route it
// does not serve, and a server that offers the POST but no read token has no GET.
//
// (The release webhook is described by the file on every deployment, as ever. Its
// description says it is production only.)
func (s *Server) servedSpec() specDoc {
	body := openAPISpec
	for _, member := range s.unofferedSpecMembers() {
		cut, err := cutJSONMember(body, member...)
		if err != nil {
			s.log.Printf("serve: openapi: %v", err)
			continue
		}
		body = cut
	}
	sum := sha256.Sum256(body)
	return specDoc{body: body, etag: `"` + hex.EncodeToString(sum[:]) + `"`}
}

// cutJSONMember returns doc without the object member at path (each element a key,
// one level deeper), its neighbours' bytes untouched. The spec is pretty-printed
// for the human who curls it, so a decode and re-encode would compact or reorder
// it; the decoder here is only used to find the member's offsets. The member's
// comma goes with it.
func cutJSONMember(doc []byte, path ...string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	var start int
	for _, key := range path {
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			return nil, fmt.Errorf("%v: %q is not inside an object", path, key)
		}
		for {
			if !dec.More() {
				return nil, fmt.Errorf("%v: no member %q", path, key)
			}
			before := int(dec.InputOffset())
			tok, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("%v: %w", path, err)
			}
			if tok == key {
				start = before + bytes.IndexByte(doc[before:], '"')
				break
			}
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, fmt.Errorf("%v: %w", path, err)
			}
		}
	}
	var value json.RawMessage
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("%v: %w", path, err)
	}
	end := int(dec.InputOffset())

	const space = " \t\r\n"
	if rest := bytes.TrimLeft(doc[end:], space); len(rest) > 0 && rest[0] == ',' {
		// Followed by a sibling: take the comma and the gap, so the sibling lands
		// where this member began, indentation included.
		end = len(doc) - len(bytes.TrimLeft(rest[1:], space))
	} else if head := bytes.TrimRight(doc[:start], space); len(head) > 0 && head[len(head)-1] == ',' {
		// The last member: its comma is the one before it.
		start = len(head) - 1
	}
	return slices.Concat(doc[:start], doc[end:]), nil
}

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
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	etag := s.spec.etag
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("ETag", etag)
	h.Set("Cache-Control", specMaxAge)
	// The wildcard is answered here rather than deferred the way the entity pages
	// defer it: this representation is embedded in the binary, so "a current
	// representation exists" is true by construction and costs nothing to know.
	inm := r.Header.Get("If-None-Match")
	if httpx.MatchesETag(inm, etag) || httpx.AnyValidator(inm) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(s.spec.body)
}
