package serve

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// servedPaths is the API surface the embedded spec must describe, EXACTLY - read
// off the SAME route table buildMux registers from, so the guard observes the
// server instead of a hand-written third copy of the list that could quietly
// agree with a wrong mux.
//
// The server is constructed WITH a webhook secret so the release hook is in the
// table: it is registered only on a configured deployment, but the spec
// describes the whole surface, so the guard has to see it.
func servedPaths() []string {
	srv := &Server{cfg: Config{WebhookSecret: strings.Repeat("s", minWebhookSecretBytes)}}
	rs := srv.routes()
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.specPath())
	}
	sort.Strings(out)
	return out
}

// openAPIDoc is the slice of the spec the drift guard reads.
type openAPIDoc struct {
	OpenAPI string                                `json:"openapi"`
	Info    struct{ Title string }                `json:"info"`
	Paths   map[string]map[string]json.RawMessage `json:"paths"`
}

func parseSpec(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	if err := json.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatalf("openapi.json does not parse: %v", err)
	}
	return doc
}

// TestOpenAPICoversEveryRoute is the drift guard. It diffs the spec's path set
// against the server's own route table, so adding a route without describing it
// (or describing one that does not exist) fails here, naming the path - the spec
// cannot quietly become fiction.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	doc := parseSpec(t)
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi = %q, want a 3.1.x document", doc.OpenAPI)
	}
	if doc.Info.Title != "AudioSilo Meta API" {
		t.Errorf("info.title = %q", doc.Info.Title)
	}

	got := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		got = append(got, p)
	}
	sort.Strings(got)

	want := map[string]bool{}
	for _, p := range servedPaths() {
		want[p] = true
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("openapi.json describes %s, which Server.routes does not register", p)
		}
		delete(want, p)
	}
	for p := range want {
		t.Errorf("route %s is served but missing from openapi.json", p)
	}
}

// TestOpenAPIOperationsAreComplete keeps the spec renderable: the site's
// /docs/api page groups operations by tag and prints their summary, so an
// operation with neither is a blank section there.
func TestOpenAPIOperationsAreComplete(t *testing.T) {
	doc := parseSpec(t)
	for path, methods := range doc.Paths {
		for method, raw := range methods {
			var op struct {
				OperationID string   `json:"operationId"`
				Summary     string   `json:"summary"`
				Tags        []string `json:"tags"`
				Responses   map[string]json.RawMessage
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			if op.OperationID == "" || op.Summary == "" || len(op.Tags) == 0 {
				t.Errorf("%s %s: needs operationId, summary and at least one tag", strings.ToUpper(method), path)
			}
			if len(op.Responses) == 0 {
				t.Errorf("%s %s: no responses documented", strings.ToUpper(method), path)
			}
		}
	}
}

// TestOpenAPIProseStaysInTheRenderedSubset pins the prose to what the site can
// actually render. OpenAPI descriptions are CommonMark, but the docs page
// renders exactly ONE construct - the inline code span (see
// site/src/components/docs/Inline.astro); everything else is interpolated as
// text, so a link, a bold run or a bullet list would appear on the page as its
// literal markdown. That is a rendering bug nobody sees until they look, so it
// fails here in the blocking CI job instead.
//
// Widening the prose is fine - it just has to widen the renderer first, and then
// this guard, together.
func TestOpenAPIProseStaysInTheRenderedSubset(t *testing.T) {
	var doc any
	if err := json.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatal(err)
	}
	for _, f := range collectProse(doc, "#") {
		switch {
		case strings.Contains(f.text, "]("):
			t.Errorf("%s: markdown link, which the page renders as literal text: %q", f.where, f.text)
		case strings.Contains(f.text, "**"):
			t.Errorf("%s: markdown bold, which the page renders as literal text: %q", f.where, f.text)
		}
		for _, line := range strings.Split(f.text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "- ") {
				t.Errorf("%s: markdown bullet, which the page renders as literal text: %q", f.where, line)
			}
		}
	}
}

// proseField is one human-readable string in the spec, with the JSON pointer it
// sits at so a failure names the field to fix.
type proseField struct {
	where string
	text  string
}

// collectProse walks the document for every description/summary string.
func collectProse(v any, at string) []proseField {
	switch t := v.(type) {
	case map[string]any:
		var out []proseField
		for k, child := range t {
			if s, ok := child.(string); ok && (k == "description" || k == "summary") {
				out = append(out, proseField{where: at + "/" + k, text: s})
				continue
			}
			out = append(out, collectProse(child, at+"/"+k)...)
		}
		return out
	case []any:
		var out []proseField
		for i, child := range t {
			out = append(out, collectProse(child, at+"/"+strconv.Itoa(i))...)
		}
		return out
	}
	return nil
}

// TestOpenAPIRefsResolve pins every local $ref to a component that exists. The
// site renders the spec by following these, so a dangling one is a blank
// response shape on the docs page rather than a loud failure.
func TestOpenAPIRefsResolve(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatal(err)
	}
	for _, ref := range collectRefs(doc) {
		if !strings.HasPrefix(ref, "#/") {
			t.Errorf("non-local $ref %q: the spec must stand alone", ref)
			continue
		}
		if resolvePointer(doc, strings.Split(strings.TrimPrefix(ref, "#/"), "/")) == nil {
			t.Errorf("$ref %q resolves to nothing", ref)
		}
	}
}

func collectRefs(v any) []string {
	switch t := v.(type) {
	case map[string]any:
		var out []string
		for k, child := range t {
			if k == "$ref" {
				if s, ok := child.(string); ok {
					out = append(out, s)
				}
				continue
			}
			out = append(out, collectRefs(child)...)
		}
		return out
	case []any:
		var out []string
		for _, child := range t {
			out = append(out, collectRefs(child)...)
		}
		return out
	}
	return nil
}

func resolvePointer(doc any, parts []string) any {
	cur := doc
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = m[p]; !ok {
			return nil
		}
	}
	return cur
}

// TestOpenAPIServedWithoutASnapshot pins the one route that must answer before
// any data has loaded: a client discovering the API on a cold boot gets the
// contract, not the 503 every data route is answering.
func TestOpenAPIServedWithoutASnapshot(t *testing.T) {
	srv := &Server{cfg: Config{}, log: log.Default(), retired: map[string]int{}}
	srv.mux = srv.buildMux()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if srv.current() != nil {
		t.Fatal("test server unexpectedly has a snapshot")
	}
	if code, _ := getJSON(t, ts.URL, "/api/v1/stats"); code != http.StatusServiceUnavailable {
		t.Fatalf("stats = %d, want 503 (the gate this route opts out of)", code)
	}

	resp, err := http.Get(ts.URL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.json = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if origin := resp.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", origin)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var served map[string]any
	if err := json.Unmarshal(body, &served); err != nil {
		t.Fatalf("served spec does not parse: %v", err)
	}
	if served["openapi"] == nil {
		t.Error("served document has no openapi version")
	}
}

// TestOpenAPIRevalidates pins the spec route's caching. The document is embedded
// and constant for the life of the binary, so a client that already has it
// should be told so rather than sent hundreds of kilobytes again.
func TestOpenAPIRevalidates(t *testing.T) {
	srv := &Server{cfg: Config{}, log: log.Default(), retired: map[string]int{}}
	srv.mux = srv.buildMux()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	first, err := http.Get(ts.URL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Body.Close() }()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first GET = %d, want 200", first.StatusCode)
	}
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("first GET carries no ETag")
	}
	if cc := first.Header.Get("Cache-Control"); cc != specMaxAge {
		t.Errorf("Cache-Control = %q, want %q", cc, specMaxAge)
	}
	if _, err := io.Copy(io.Discard, first.Body); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	// Accept-Encoding is set EXPLICITLY so the transport neither adds it nor
	// transparently strips the Content-Encoding off the answer: this assertion is
	// about the header the gzip middleware puts on the wire.
	req.Header.Set("Accept-Encoding", "gzip")
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", second.StatusCode)
	}
	if got := second.Header.Get("ETag"); got != etag {
		t.Errorf("304 ETag = %q, want %q", got, etag)
	}
	// A 304 carries no body, so it must not claim one is encoded: the client
	// applies these headers to the copy it ALREADY has, which it holds in
	// whatever encoding it was served in. Announcing gzip here tells a client
	// with an identity-cached copy to gunzip plain JSON (RFC 9110 15.4.5).
	if enc := second.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("304 Content-Encoding = %q, want none: a bodyless response encodes nothing", enc)
	}
	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Errorf("304 carried a %d-byte body", len(body))
	}
}

// TestMatchesETag covers the If-None-Match forms RFC 9110 allows, since a wrong
// answer either serves a 304 for a document the client does not have or never
// serves one at all.
func TestMatchesETag(t *testing.T) {
	const tag = `"abc"`
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{tag, true},
		{`W/"abc"`, true},
		{`"other", "abc"`, true},
		{`"other"`, false},
		{"*", true},
		{`"ab"`, false},
	}
	for _, tc := range cases {
		if got := matchesETag(tc.header, tag); got != tc.want {
			t.Errorf("matchesETag(%q, %q) = %v, want %v", tc.header, tag, got, tc.want)
		}
	}
}
