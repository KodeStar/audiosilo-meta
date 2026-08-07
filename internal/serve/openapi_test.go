package serve

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// specPaths is the API surface the embedded spec must describe, EXACTLY: every
// pattern buildMux registers, plus the release hook (registered only when a
// webhook secret is configured, so no test server carries it) and the spec route
// itself. It is written out by hand rather than read off the mux, because a
// derived list would agree with a wrong mux; this one is the independent
// statement of what is public, and either side moving without the other is the
// failure the test exists to catch.
var specPaths = []string{
	"/abs/search",
	"/api/v1/coverage",
	"/api/v1/coverage/series-gaps",
	"/api/v1/coverage/works",
	"/api/v1/lookup",
	"/api/v1/openapi.json",
	"/api/v1/people/search",
	"/api/v1/people/{id}",
	"/api/v1/search",
	"/api/v1/series/search",
	"/api/v1/series/{id}",
	"/api/v1/stats",
	"/api/v1/works/latest",
	"/api/v1/works/search",
	"/api/v1/works/{id}",
	"/api/v1/works/{id}/recordings/{rid}/chapters",
	"/healthz",
	"/hooks/github/release",
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

// TestOpenAPICoversEveryRoute is the drift guard. Adding a route without
// describing it (or describing one that does not exist) fails here, naming the
// path - so the spec cannot quietly become fiction.
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
	for _, p := range specPaths {
		want[p] = true
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("openapi.json describes %s, which buildMux does not register", p)
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
