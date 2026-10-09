// Package httpx holds the response-writing and cache-validator helpers that
// pkg/query (the JSON API metaserve and mirror-mode AudioSilo servers both
// serve) and internal/serve (metaserve's pages, sitemaps and operational
// routes) share. It is a LEAF - no dependency on anything else here - because
// the two packages answer on one server, and a second spelling of an error
// envelope or of the If-None-Match comparison is a difference a client could
// read as a difference.
package httpx

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// WriteJSON writes v as the JSON body of a response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

// WriteErr writes the one error envelope every route answers with:
// {"error": msg}.
func WriteErr(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// NoArtifactMsg is the one wording of the no-artifact 503, shared by the API
// gate (pkg/query's requireDB) and metaserve's sitemap routes (which are outside
// that gate because they are not API). Two spellings of one condition is a
// difference a client could read as a difference.
const NoArtifactMsg = "no data loaded yet: the server is fetching the latest release"

// InternalErrMsg is the body of EVERY 500 this server writes. An internal
// error's own text describes the failure's internals - a SQL statement, a file
// path on the cache volume, a driver message - and every API route here is
// public and CORS-open, so that text is reflected to anyone who can provoke it.
// The 4xx messages are deliberately untouched: those are about the REQUEST, which
// the caller sent and is the only thing they can act on.
const InternalErrMsg = "internal error"

// Fail answers with the fixed 500 body and logs what actually went wrong, so the
// detail is kept where an operator reads it rather than where a stranger does.
// ONE helper rather than a fixed string at each site: a handler that spells its
// own 500 is a handler that can quietly go back to reflecting err.Error().
//
// The method and path are QUOTED. r.URL.Path is the DECODED path, so a request
// for `/works/x%0A2026-01-01 serve: 500 ...` puts a newline in the middle of
// this line and the rest of it reads as a log entry of its own - a caller
// forging whatever an operator or a log pipeline then believes. %q keeps it on
// one line, with the control characters visible as escapes.
//
// A request whose client has gone is not logged: the search and lookup reads
// run under the request's context, so a client that abandons a query (the
// site's search box does, on every keystroke) fails it with the context's
// error, which is no fault of the server's and nobody reads the 500.
func Fail(w http.ResponseWriter, r *http.Request, logger *log.Logger, err error) {
	if r.Context().Err() == nil {
		logger.Printf("serve: 500 %q %q: %v", r.Method, r.URL.Path, err)
	}
	WriteErr(w, http.StatusInternalServerError, InternalErrMsg)
}

// MatchesETag reports whether an If-None-Match header NAMES etag. It handles the
// comma-separated list, and compares with the WEAK comparison function the spec
// mandates for If-None-Match: a "W/" prefix is ignored on BOTH sides, so a weak
// validator (the entity pages issue one - see entityETag) matches the header it
// was itself sent in, and a strong one (the spec route's content hash) still
// matches a weakened echo of it.
//
// The "*" wildcard is deliberately NOT a match here. Per RFC 9110 13.1.2 it
// matches only when a current representation EXISTS, which is a question about
// the resource and not about the header - so the callers answer it themselves
// (see AnyValidator) and this function stays a comparison.
func MatchesETag(header, etag string) bool {
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

// AnyValidator reports whether an If-None-Match header carries the "*" wildcard.
// It says only that the client asked "304 if this resource has any current
// representation" - whether it HAS one is the caller's to establish, which is
// what keeps an unknown or retired id off a 304 it would otherwise get for free.
func AnyValidator(header string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(candidate) == "*" {
			return true
		}
	}
	return false
}

// Identity folds any number of components, each length-prefixed, into one short
// hex digest - the body of a cache validator. The entity pages (pageIdentity,
// over the shell and the revision) and the watch feeds (over the artifact and
// the request's own parameters) both call it, each with the components its
// representation is a function of.
//
// fnv-64a is enough: this is a cache key, not a signature - a client that
// fabricates a validator is only ever handed a 304 for a page it invented, so
// nothing here has to resist collisions chosen by an attacker. Components are
// length-prefixed so no two of them can be run together into the same digest
// input.
func Identity(parts ...string) string {
	h := fnv.New64a()
	for _, part := range parts {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(part)))
		_, _ = h.Write(n[:])
		_, _ = io.WriteString(h, part)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// RenderXML marshals an XML document, declaration included - the sitemaps and
// the watch feed's Atom representation. It is deterministic: struct field order
// is the element order, the entries are in the order their query returned them
// and nothing here reads a clock, so two renders of one snapshot are
// byte-identical.
func RenderXML(doc any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}
