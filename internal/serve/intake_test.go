package serve

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
	"github.com/kodestar/audiosilo-meta/pkg/release"
	"github.com/kodestar/audiosilo-meta/pkg/release/releasetest"
)

const (
	intakeTestToken = "0123456789abcdef0123456789abcdef-token"
	unknownA        = "B0ABCDEFGH"
	unknownB        = "B0XXXXXXXX"
	knownASIN       = artifacttest.HailMaryASIN
	// lateASIN is one only intakeLateCatalog's artifact knows.
	lateASIN = "B0LATEASIN"
)

// intakeLateCatalog is the fixture plus a recording carrying lateASIN: the newer
// release a queued ASIN turns out to be in.
func intakeLateCatalog() *model.Catalog {
	cat := artifacttest.Fixture()
	cat.Works = append(cat.Works, &model.Work{
		ID: "the-martian", Title: "The Martian", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "the-martian-r", Work: "the-martian", Language: "en", License: "CC0-1.0",
			Narrators: []string{"ray-porter"},
			ASIN:      []model.ASIN{{Region: "uk", ASIN: lateASIN}},
		}},
	})
	return cat
}

// safeClock is fakeClock for a test where a background goroutine reads it too.
type safeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newSafeClock() *safeClock {
	return &safeClock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
}

func (c *safeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *safeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// logCapture is a log destination a test can read while housekeeping writes to it.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func newLoggerCapture() (*log.Logger, *logCapture) {
	c := &logCapture{}
	return log.New(c, "", 0), c
}

// intakeServer builds a server with the queue on. cfg's unset essentials are
// filled in: an artifact (unless the test polls for one), a queue file, the token
// and a quiet logger.
func intakeServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.DBPath == "" && !cfg.Poll {
		cfg.DBPath = artifacttest.Build(t, artifacttest.Fixture())
	}
	if cfg.IntakeDB == "" {
		cfg.IntakeDB = filepath.Join(t.TempDir(), "intake", "queue.sqlite")
	}
	if cfg.IntakeToken == "" {
		cfg.IntakeToken = intakeTestToken
	}
	if cfg.Logger == nil {
		cfg.Logger = artifacttest.QuietLogger()
	}
	if cfg.swapGrace == 0 {
		cfg.swapGrace = time.Minute
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.closeIntake)
	if srv.intake == nil {
		t.Fatal("the intake queue did not come up")
	}
	return srv
}

// intakeReq is one request to the intake path, with what a test varies.
type intakeReq struct {
	method, ctype, body, remote, auth, ua string
	xff                                   []string
}

func (q intakeReq) do(srv *Server) *httptest.ResponseRecorder {
	method := q.method
	if method == "" {
		method = http.MethodPost
	}
	r := httptest.NewRequest(method, intakePath, strings.NewReader(q.body))
	if q.remote != "" {
		r.RemoteAddr = q.remote
	}
	if method == http.MethodPost {
		ctype := q.ctype
		if ctype == "" {
			ctype = "application/json"
		}
		if ctype != "none" {
			r.Header.Set("Content-Type", ctype)
		}
	}
	if q.auth != "" {
		r.Header.Set("Authorization", q.auth)
	}
	if q.ua != "" {
		r.Header.Set("User-Agent", q.ua)
	}
	for _, v := range q.xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

// asinsBody is a report naming asins.
func asinsBody(asins ...string) string {
	b, err := json.Marshal(map[string][]string{"asins": asins})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// report posts a report from a remote of its own and wants 202.
func report(t *testing.T, srv *Server, asins ...string) map[string]int {
	t.Helper()
	rec := intakeReq{body: asinsBody(asins...), remote: nextRemote()}.do(srv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("report = %d %s, want 202", rec.Code, rec.Body)
	}
	var got map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

var remoteSeq atomic.Int64

// nextRemote is a client address no other request of this test binary has used, so
// a test about the queue never trips over the limiter. 198.18/15 is the benchmark
// range; nothing else here uses it.
func nextRemote() string {
	n := remoteSeq.Add(1)
	return fmt.Sprintf("198.18.%d.%d:4000", n/250, n%250)
}

// queueRows reads the queue through the read route.
func queueRows(t *testing.T, srv *Server) []intakeRow {
	t.Helper()
	rec := intakeReq{method: http.MethodGet, auth: "Bearer " + intakeTestToken, remote: nextRemote()}.do(srv)
	if rec.Code != http.StatusOK {
		t.Fatalf("read = %d %s, want 200", rec.Code, rec.Body)
	}
	var doc struct {
		Total int         `json:"total"`
		ASINs []intakeRow `json:"asins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Total != len(doc.ASINs) {
		t.Errorf("total = %d with %d rows", doc.Total, len(doc.ASINs))
	}
	return doc.ASINs
}

// queueASINs is the queue's ASINs, sorted.
func queueASINs(t *testing.T, srv *Server) []string {
	t.Helper()
	out := []string{}
	for _, r := range queueRows(t, srv) {
		out = append(out, r.ASIN)
	}
	slices.Sort(out)
	return out
}

// seedQueue writes rows directly, for states a report cannot reach in a test.
func seedQueue(t *testing.T, q *intakeQueue, rows ...intakeRow) {
	t.Helper()
	for _, r := range rows {
		if _, err := q.db.Exec(`INSERT INTO intake_asins(asin, count, first_day, last_day) VALUES(?,?,?,?)`,
			r.ASIN, r.Count, r.FirstDay, r.LastDay); err != nil {
			t.Fatal(err)
		}
	}
}

// seedFile leaves rows in a queue file, the way a previous container would.
func seedFile(t *testing.T, path string, asins ...string) {
	t.Helper()
	q, err := openIntakeQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.close() }()
	for _, a := range asins {
		seedQueue(t, q, intakeRow{ASIN: a, Count: 1, FirstDay: "2026-11-01", LastDay: "2026-11-01"})
	}
}

// eventually polls cond, for what housekeeping does on its own goroutine.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		runtime.Gosched()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// wantQueue waits for the queue to settle on exactly want.
func wantQueue(t *testing.T, srv *Server, want ...string) {
	t.Helper()
	slices.Sort(want)
	if want == nil {
		want = []string{}
	}
	var got []string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if got = queueASINs(t, srv); slices.Equal(got, want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("queue = %v, want %v", got, want)
}

// ---- env unset ------------------------------------------------------------

// TestIntakeOffIsNoChange is the acceptance line: with both variables unset there
// is no route, no spec entry, no state and no file, and the API answers as ever.
func TestIntakeOffIsNoChange(t *testing.T) {
	seed := artifacttest.Build(t, artifacttest.Fixture())
	site := writeSiteFixture(t)
	logger, logged := newLoggerCapture()
	cwd := t.TempDir()
	t.Chdir(cwd)

	for name, cfg := range map[string]Config{
		"with a site": {DBPath: seed, Site: site, swapGrace: time.Minute, Logger: logger},
		"api only":    {DBPath: seed, swapGrace: time.Minute, Logger: logger},
	} {
		t.Run(name, func(t *testing.T) {
			srv, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if srv.intake != nil {
				t.Fatal("intake state exists with the env unset, and only it starts a goroutine")
			}
			srv.kickIntakePurge() // no queue to reach: must not panic
			srv.closeIntake()

			for _, r := range srv.routes() {
				if strings.Contains(r.pattern, "/intake") {
					t.Errorf("route %q registered with the env unset", r.pattern)
				}
			}
			for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodOptions, http.MethodDelete} {
				rec := intakeReq{method: method, body: asinsBody(unknownA), auth: "Bearer " + intakeTestToken}.do(srv)
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s = %d, want 404", method, rec.Code)
				}
				if rec.Header().Get("Cache-Control") != "" {
					t.Errorf("%s carries Cache-Control %q: the intake stack ran", method, rec.Header().Get("Cache-Control"))
				}
			}

			spec := servedDoc(t, srv)
			paths := spec["paths"].(map[string]any)
			if _, ok := paths[intakePath]; ok {
				t.Error("the served spec describes the intake path")
			}
			components := spec["components"].(map[string]any)
			if _, ok := components["securitySchemes"]; ok {
				t.Error("the served spec keeps the read route's security scheme")
			}
			for name := range components["schemas"].(map[string]any) {
				if strings.HasPrefix(name, "Intake") {
					t.Errorf("the served spec keeps schema %s", name)
				}
			}
			if _, ok := paths[githubReleaseWebhookPath]; !ok {
				t.Error("the release hook went missing from the spec, which has always described it")
			}

			// The rest of the API wears the stack it always did.
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			resp := get(t, ts.URL+"/api/v1/stats", nil)
			if resp.StatusCode != http.StatusOK || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
				t.Errorf("stats = %d, ACAO %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
			}
		})
	}
	if out := logged.String(); strings.Contains(out, "intake") {
		t.Errorf("the env unset logged about intake: %q", out)
	}
	if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 0 {
		t.Errorf("files appeared with the env unset: %v %v", entries, err)
	}
}

func TestIntakeTokenWithoutDBStaysOff(t *testing.T) {
	logger, logged := newLoggerCapture()
	srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), IntakeToken: intakeTestToken,
		swapGrace: time.Minute, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if srv.intake != nil {
		t.Fatal("a token alone brought the queue up")
	}
	if rec := (intakeReq{method: http.MethodGet, auth: "Bearer " + intakeTestToken}).do(srv); rec.Code != http.StatusNotFound {
		t.Errorf("read = %d, want 404", rec.Code)
	}
	if out := logged.String(); !strings.Contains(out, "without METASERVE_INTAKE_DB") || strings.Contains(out, intakeTestToken) {
		t.Errorf("log = %q: want the misconfiguration named and the value absent", out)
	}
}

// TestIntakeQueueFailureLeavesTheAPIUp: a queue that will not open is logged and
// leaves the routes off, whichever way it fails.
func TestIntakeQueueFailureLeavesTheAPIUp(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(dir, "garbage.sqlite")
	if err := os.WriteFile(garbage, bytes.Repeat([]byte("not a database "), 200), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign.sqlite")
	db, err := sql.Open("sqlite", foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE intake_asins(something TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	cases := map[string]string{
		"parent is a file":      filepath.Join(notADir, "queue.sqlite"),
		"path is a directory":   dir,
		"not a database":        garbage,
		"another table's shape": foreign,
	}
	if os.Geteuid() != 0 { // root writes through any mode
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o700); err != nil {
			t.Fatal(err)
		}
		lockedQueue := filepath.Join(locked, "queue.sqlite")
		seedFile(t, lockedQueue, unknownA)
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		// The file itself opens read-write; only the journal it needs cannot be made.
		cases["directory read-only, so no journal"] = lockedQueue
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			logger, logged := newLoggerCapture()
			srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), IntakeDB: path,
				IntakeToken: intakeTestToken, swapGrace: time.Minute, Logger: logger})
			if err != nil {
				t.Fatalf("a bad queue must not fail the boot: %v", err)
			}
			if srv.intake != nil {
				t.Fatal("the queue came up")
			}
			if !strings.Contains(logged.String(), "intake queue unavailable") {
				t.Errorf("log = %q, want the failure", logged.String())
			}
			if rec := (intakeReq{body: asinsBody(unknownA)}).do(srv); rec.Code != http.StatusNotFound {
				t.Errorf("POST = %d, want 404", rec.Code)
			}
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			if resp := get(t, ts.URL+"/api/v1/stats", nil); resp.StatusCode != http.StatusOK {
				t.Errorf("stats = %d, want 200", resp.StatusCode)
			}
		})
	}
}

// ---- the report ------------------------------------------------------------

func TestIntakeReportRefusals(t *testing.T) {
	srv := intakeServer(t, Config{})
	over := `{"asins":[],"pad":"` + strings.Repeat("x", maxIntakeBodyBytes) + `"}`
	items := make([]string, maxIntakeItems+1)
	for i := range items {
		items[i] = unknownA
	}
	for _, tc := range []struct {
		name, ctype, body string
		want              int
	}{
		{"over 8 KiB", "", over, http.StatusRequestEntityTooLarge},
		{"over the item count", "", asinsBody(items...), http.StatusBadRequest},
		{"text/plain", "text/plain", asinsBody(unknownA), http.StatusUnsupportedMediaType},
		{"form", "application/x-www-form-urlencoded", asinsBody(unknownA), http.StatusUnsupportedMediaType},
		{"no content type", "none", asinsBody(unknownA), http.StatusUnsupportedMediaType},
		{"a json lookalike", "application/jsonp", asinsBody(unknownA), http.StatusUnsupportedMediaType},
		{"a broken media type", "application/json; =", asinsBody(unknownA), http.StatusUnsupportedMediaType},
		{"malformed json", "", `{"asins":[`, http.StatusBadRequest},
		{"trailing data", "", asinsBody(unknownA) + " {}", http.StatusBadRequest},
		{"empty body", "", ``, http.StatusBadRequest},
		{"asins missing", "", `{"other":[]}`, http.StatusBadRequest},
		{"asins another case", "", `{"ASINS":["` + unknownA + `"]}`, http.StatusBadRequest},
		{"asins null", "", `{"asins":null}`, http.StatusBadRequest},
		{"asins a string", "", `{"asins":"` + unknownA + `"}`, http.StatusBadRequest},
		{"asins an object", "", `{"asins":{}}`, http.StatusBadRequest},
		{"a bare array", "", `["` + unknownA + `"]`, http.StatusBadRequest},
		{"null", "", `null`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := intakeReq{ctype: tc.ctype, body: tc.body, remote: nextRemote()}.do(srv)
			if rec.Code != tc.want {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body, tc.want)
			}
			var doc map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc["error"] == "" {
				t.Errorf("body = %q, want the error envelope", rec.Body)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q", cc)
			}
		})
	}
	if rows := queueRows(t, srv); len(rows) != 0 {
		t.Errorf("a refused report left rows: %v", rows)
	}
}

// TestIntakeReportAtItsLimits: the bounds admit what they name.
func TestIntakeReportAtItsLimits(t *testing.T) {
	srv := intakeServer(t, Config{})
	envelope := `{"asins":[],"pad":""}`
	exact := `{"asins":[],"pad":"` + strings.Repeat("x", maxIntakeBodyBytes-len(envelope)) + `"}`
	if len(exact) != maxIntakeBodyBytes {
		t.Fatalf("test body is %d bytes", len(exact))
	}
	if rec := (intakeReq{body: exact, remote: nextRemote()}).do(srv); rec.Code != http.StatusAccepted {
		t.Errorf("exactly 8 KiB = %d, want 202", rec.Code)
	}
	if rec := (intakeReq{body: exact[:len(exact)-2] + `x"}`, remote: nextRemote()}).do(srv); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("one byte over = %d, want 413", rec.Code)
	}

	items := make([]string, maxIntakeItems)
	for i := range items {
		items[i] = fmt.Sprintf("B0%08d", i)
	}
	if got := report(t, srv, items...); got["accepted"] != maxIntakeItems {
		t.Errorf("200 items: %v", got)
	}

	for _, ctype := range []string{"application/json; charset=utf-8", "Application/JSON", "application/json;charset=UTF-8"} {
		rec := intakeReq{ctype: ctype, body: `{"asins":[]}`, remote: nextRemote()}.do(srv)
		if rec.Code != http.StatusAccepted {
			t.Errorf("%q = %d, want 202", ctype, rec.Code)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"accepted":0,"invalid":0,"known":0}` {
			t.Errorf("an empty report answered %s", got)
		}
	}
}

// TestIntakeItemsAreCounted is the three numbers: dedupe, known-drop and the
// invalid items that do not sink a report.
func TestIntakeItemsAreCounted(t *testing.T) {
	srv := intakeServer(t, Config{})
	body := `{"asins":["` + unknownA + `","` + unknownA + `","` + knownASIN + `","` + knownASIN + `",` +
		`"b0abcdefgh","B0ABC","B0ABCDEFGHI","B0ABCDEF-H","B0ABCDEFGH\n",12,null,{"a":1},["x"],true,""],` +
		`"server":"a-server-name"}`
	rec := intakeReq{body: body, remote: nextRemote()}.do(srv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var got map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"accepted": 1, "known": 1, "invalid": 11}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer = %v, want %v", got, want)
	}
	rows := queueRows(t, srv)
	if len(rows) != 1 || rows[0].ASIN != unknownA || rows[0].Count != 1 {
		t.Errorf("rows = %+v, want %s once: repeated in one body counts once, a known one is dropped", rows, unknownA)
	}
	raw, err := os.ReadFile(srv.cfg.IntakeDB)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("a-server-name")) {
		t.Error("a report's unknown member reached the file")
	}
}

// TestIntakeCountsAndDays: one per POST, first day kept, last day moved, and the
// day is the UTC one whatever zone the clock reads in.
func TestIntakeCountsAndDays(t *testing.T) {
	clock := newSafeClock()
	clock.t = time.Date(2026, 11, 5, 23, 30, 0, 0, time.FixedZone("EST", -5*3600)) // 04:30 on the 6th, UTC
	srv := intakeServer(t, Config{now: clock.now})

	report(t, srv, unknownA)
	report(t, srv, unknownA, unknownB)
	clock.advance(72 * time.Hour)
	report(t, srv, unknownA)

	got := queueRows(t, srv)
	want := []intakeRow{
		{ASIN: unknownA, Count: 3, FirstDay: "2026-11-06", LastDay: "2026-11-09"},
		{ASIN: unknownB, Count: 1, FirstDay: "2026-11-06", LastDay: "2026-11-06"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %+v, want %+v", got, want)
	}
}

func TestIntakeReadOrder(t *testing.T) {
	srv := intakeServer(t, Config{})
	seedQueue(t, srv.intake.queue,
		intakeRow{ASIN: "B000000005", Count: 1, FirstDay: "2026-11-01", LastDay: "2026-11-01"},
		intakeRow{ASIN: "B000000004", Count: 1, FirstDay: "2026-10-01", LastDay: "2026-11-01"},
		intakeRow{ASIN: "B000000003", Count: 1, FirstDay: "2026-10-01", LastDay: "2026-11-01"},
		intakeRow{ASIN: "B000000002", Count: 2, FirstDay: "2026-11-02", LastDay: "2026-11-02"},
		intakeRow{ASIN: "B000000001", Count: 9, FirstDay: "2026-11-03", LastDay: "2026-11-03"},
	)
	var got []string
	for _, r := range queueRows(t, srv) {
		got = append(got, r.ASIN)
	}
	want := []string{"B000000001", "B000000002", "B000000003", "B000000004", "B000000005"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want count desc, first_day, asin: %v", got, want)
	}
}

func TestIntakeEmptyReadIsAnEmptyArray(t *testing.T) {
	srv := intakeServer(t, Config{})
	rec := intakeReq{method: http.MethodGet, auth: "Bearer " + intakeTestToken}.do(srv)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"total":0,"asins":[]}` {
		t.Errorf("body = %s", got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

// ---- eviction and expiry ---------------------------------------------------

func TestIntakeEvictionTakesTheLowestRow(t *testing.T) {
	ctx := context.Background()
	today := "2026-11-05"
	row := func(asin string, count int, last string) intakeRow {
		return intakeRow{ASIN: asin, Count: count, FirstDay: "2026-01-01", LastDay: last}
	}
	for _, tc := range []struct {
		name   string
		seed   []intakeRow
		report []string
		want   []string
	}{
		{
			name:   "the lowest count goes",
			seed:   []intakeRow{row("A000000001", 5, "2026-01-01"), row("A000000002", 1, "2026-11-01"), row("A000000003", 2, "2026-01-01")},
			report: []string{"N000000001"},
			want:   []string{"A000000001", "A000000003", "N000000001"},
		},
		{
			name:   "then the oldest last day",
			seed:   []intakeRow{row("A000000001", 1, "2026-11-01"), row("A000000002", 1, "2026-02-01"), row("A000000003", 1, "2026-03-01")},
			report: []string{"N000000001"},
			want:   []string{"A000000001", "A000000003", "N000000001"},
		},
		{
			name:   "the ASIN settles a tie",
			seed:   []intakeRow{row("A000000002", 1, "2026-02-01"), row("A000000001", 1, "2026-02-01"), row("A000000003", 1, "2026-02-01")},
			report: []string{"N000000001"},
			want:   []string{"A000000002", "A000000003", "N000000001"},
		},
		{
			name:   "an ASIN already held evicts nothing",
			seed:   []intakeRow{row("A000000001", 5, "2026-01-01"), row("A000000002", 1, "2026-11-01"), row("A000000003", 2, "2026-01-01")},
			report: []string{"A000000002"},
			want:   []string{"A000000001", "A000000002", "A000000003"},
		},
		{
			name:   "a report's own rows are exempt",
			seed:   []intakeRow{row("A000000001", 5, "2026-01-01"), row("A000000002", 1, "2026-01-01"), row("A000000003", 2, "2026-01-01")},
			report: []string{"A000000002", "N000000001"},
			want:   []string{"A000000001", "A000000002", "N000000001"},
		},
		{
			name:   "a batch evicts one per new ASIN",
			seed:   []intakeRow{row("A000000001", 5, "2026-01-01"), row("A000000002", 1, "2026-01-01"), row("A000000003", 2, "2026-01-01")},
			report: []string{"N000000001", "N000000002"},
			want:   []string{"A000000001", "N000000001", "N000000002"},
		},
		{
			name:   "under the cap nothing goes",
			seed:   []intakeRow{row("A000000001", 1, "2026-01-01")},
			report: []string{"N000000001"},
			want:   []string{"A000000001", "N000000001"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := openIntakeQueue(filepath.Join(t.TempDir(), "queue.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = q.close() }()
			q.maxRows = 3
			seedQueue(t, q, tc.seed...)
			if err := q.record(ctx, tc.report, today); err != nil {
				t.Fatal(err)
			}
			got, err := q.asins(ctx)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("queue = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIntakeHoldsTheRealCap runs the shipped cap through the route.
func TestIntakeHoldsTheRealCap(t *testing.T) {
	srv := intakeServer(t, Config{})
	q := srv.intake.queue
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxIntakeRows {
		// All count 2 but one, so which row goes is not in doubt.
		count := 2
		if i == 7777 {
			count = 1
		}
		if _, err := tx.Exec(`INSERT INTO intake_asins VALUES(?,?,?,?)`, fmt.Sprintf("C%09d", i), count, "2026-11-01", "2026-11-01"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	report(t, srv, unknownA)
	rows := queueRows(t, srv)
	if len(rows) != maxIntakeRows {
		t.Errorf("rows = %d, want the cap of %d", len(rows), maxIntakeRows)
	}
	asins := make(map[string]bool, len(rows))
	for _, r := range rows {
		asins[r.ASIN] = true
	}
	if !asins[unknownA] || asins["C000007777"] {
		t.Errorf("new row present = %v, lowest row present = %v; want true, false", asins[unknownA], asins["C000007777"])
	}
}

// TestIntakeExpiryRunsOnTheClock: a row goes after 90 days unseen with no request
// arriving to prompt it, and the 90th day itself is still inside.
func TestIntakeExpiryRunsOnTheClock(t *testing.T) {
	clock := newSafeClock() // 2026-11-05
	srv := intakeServer(t, Config{now: clock.now, intakeTick: 5 * time.Millisecond})
	seedQueue(t, srv.intake.queue,
		intakeRow{ASIN: "E000000001", Count: 50, FirstDay: "2026-01-01", LastDay: "2026-08-07"}, // 90 days before the 5th
		intakeRow{ASIN: "E000000002", Count: 50, FirstDay: "2026-01-01", LastDay: "2026-08-06"}, // 91
		intakeRow{ASIN: "E000000003", Count: 1, FirstDay: "2026-11-05", LastDay: "2026-11-05"},
	)
	// The clock crosses midnight: nothing but the ticker can act now.
	clock.advance(13 * time.Hour) // 2026-11-06 01:00: E1 is now 91 days old, E2 92
	wantQueue(t, srv, "E000000003")
}

func TestIntakeExpiryBoundary(t *testing.T) {
	clock := newSafeClock() // 2026-11-05
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	q, err := openIntakeQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	seedQueue(t, q,
		intakeRow{ASIN: "E000000001", Count: 1, FirstDay: "2026-01-01", LastDay: "2026-08-07"}, // exactly 90 days
		intakeRow{ASIN: "E000000002", Count: 1, FirstDay: "2026-01-01", LastDay: "2026-08-06"}, // 91 days
	)
	_ = q.close()
	// The boot's own pass, so a server that was down over the day still expires.
	srv := intakeServer(t, Config{now: clock.now, IntakeDB: path})
	wantQueue(t, srv, "E000000001")
}

// ---- the purge -------------------------------------------------------------

// TestIntakePurgeOnEveryAdoptPath is trap 5 from the plan: each way a snapshot gets
// installed filters the queue, and each subtest proves it took the path it names.
func TestIntakePurgeOnEveryAdoptPath(t *testing.T) {
	v1Path := artifacttest.Build(t, artifacttest.Fixture())
	v1 := readDB(t, v1Path)
	latePath := artifacttest.Build(t, intakeLateCatalog())
	late := readDB(t, latePath)

	// A server on the old artifact, its queue holding the late ASIN.
	seeded := func(t *testing.T, f *releasetest.GitHub) *Server {
		t.Helper()
		srv := intakeServer(t, Config{DBPath: v1Path, Repo: releasetest.Repo, CacheDir: t.TempDir()})
		srv.gh = srv.newGHClient(releasetest.Repo, f.URL)
		seedQueue(t, srv.intake.queue, intakeRow{ASIN: lateASIN, Count: 3, FirstDay: "2026-11-01", LastDay: "2026-11-01"},
			intakeRow{ASIN: unknownA, Count: 1, FirstDay: "2026-11-01", LastDay: "2026-11-01"})
		return srv
	}

	t.Run("full download", func(t *testing.T) {
		f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
		srv := seeded(t, f)
		if err := srv.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.Hits(release.DataAsset) != 1 {
			t.Fatalf("artifact downloaded %d times: not the full path", f.Hits(release.DataAsset))
		}
		wantQueue(t, srv, unknownA)
	})

	t.Run("patched", func(t *testing.T) {
		f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, v1, "", nil)})
		srv := seeded(t, f)
		if err := srv.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantQueue(t, srv, lateASIN, unknownA) // R1 does not know it
		f.Publish(tagR2, makeAssets(t, late, tagR1, v1))
		if err := srv.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.Hits(patchAssetName(tagR1)) != 1 {
			t.Fatalf("patch fetched %d times: not the patch path", f.Hits(patchAssetName(tagR1)))
		}
		wantQueue(t, srv, unknownA)
	})

	t.Run("cached artifact", func(t *testing.T) {
		f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR2, Assets: makeAssets(t, late, "", nil)})
		srv := seeded(t, f)
		if err := os.WriteFile(srv.dbCachePath(tagR2), late, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := srv.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.Hits(release.DataAsset) != 0 {
			t.Fatalf("artifact downloaded %d times: not the cached path", f.Hits(release.DataAsset))
		}
		wantQueue(t, srv, unknownA)
	})

	t.Run("stale cache at a degraded boot", func(t *testing.T) {
		cache := t.TempDir()
		f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
		f.SetDown(true)
		if err := os.WriteFile((&Server{cfg: Config{CacheDir: cache}}).dbCachePath(tagR1), late, 0o600); err != nil {
			t.Fatal(err)
		}
		queue := filepath.Join(t.TempDir(), "queue.sqlite")
		seedFile(t, queue, lateASIN, unknownA)
		srv := intakeServer(t, Config{Poll: true, Repo: "owner/name", CacheDir: cache, apiBase: f.URL,
			bootRetry: time.Hour, IntakeDB: queue})
		if srv.current() == nil || srv.loaded != "" {
			t.Fatalf("not the stale path: snapshot %v, loaded %q", srv.current(), srv.loaded)
		}
		wantQueue(t, srv, unknownA)
	})

	t.Run("first adopt at a boot that fetched", func(t *testing.T) {
		f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
		queue := filepath.Join(t.TempDir(), "queue.sqlite")
		seedFile(t, queue, lateASIN, unknownA)
		srv := intakeServer(t, Config{Poll: true, Repo: "owner/name", CacheDir: t.TempDir(), apiBase: f.URL,
			bootRetry: time.Hour, IntakeDB: queue})
		if srv.loaded != tagR1 {
			t.Fatalf("loaded = %q", srv.loaded)
		}
		wantQueue(t, srv, unknownA)
	})

	t.Run("first adopt after a boot with no snapshot", func(t *testing.T) {
		f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
		f.SetDown(true)
		srv := intakeServer(t, Config{Poll: true, Repo: "owner/name", CacheDir: t.TempDir(), apiBase: f.URL, bootRetry: time.Hour})
		if srv.current() != nil {
			t.Fatal("expected no snapshot")
		}
		// With no snapshot everything is taken, even what any release would know.
		if got := report(t, srv, lateASIN, knownASIN, unknownA); got["accepted"] != 3 || got["known"] != 0 {
			t.Fatalf("report with no snapshot = %v, want 3 accepted, 0 known", got)
		}
		wantQueue(t, srv, lateASIN, knownASIN, unknownA)
		f.SetDown(false)
		if err := srv.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		wantQueue(t, srv, unknownA)
	})

	t.Run("a local artifact at boot", func(t *testing.T) {
		queue := filepath.Join(t.TempDir(), "queue.sqlite")
		seedFile(t, queue, lateASIN, unknownA)
		srv := intakeServer(t, Config{DBPath: latePath, IntakeDB: queue})
		wantQueue(t, srv, unknownA)
	})
}

// TestIntakePurgeNeverHoldsUpASwap: the queue's one connection is held for the
// whole refresh, so a purge on the request path or under s.mu would hang here. The
// kicks that pile up meanwhile coalesce, and the purge then runs against the newest.
func TestIntakePurgeNeverHoldsUpASwap(t *testing.T) {
	v1Path := artifacttest.Build(t, artifacttest.Fixture())
	late := readDB(t, artifacttest.Build(t, intakeLateCatalog()))
	f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
	srv := intakeServer(t, Config{DBPath: v1Path, Repo: releasetest.Repo, CacheDir: t.TempDir()})
	srv.gh = srv.newGHClient(releasetest.Repo, f.URL)
	seedQueue(t, srv.intake.queue, intakeRow{ASIN: lateASIN, Count: 1, FirstDay: "2026-11-01", LastDay: "2026-11-01"})

	tx, err := srv.intake.queue.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- srv.refresh(context.Background()) }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the refresh waited on the queue")
	}
	if srv.loaded != tagR1 {
		t.Fatalf("loaded = %q", srv.loaded)
	}
	if !srv.mu.TryLock() {
		t.Fatal("s.mu is held with the refresh finished")
	}
	srv.mu.Unlock()
	for range 50 {
		srv.kickIntakePurge()
	}
	if n := len(srv.intake.purge); n > 1 {
		t.Errorf("%d purges pending, want them coalesced to at most one", n)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, srv)
}

// TestIntakePurgeFailureIsOnlyLogged: with the queue gone, the swap stands.
func TestIntakePurgeFailureIsOnlyLogged(t *testing.T) {
	v1Path := artifacttest.Build(t, artifacttest.Fixture())
	late := readDB(t, artifacttest.Build(t, intakeLateCatalog()))
	f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
	logger, logged := newLoggerCapture()
	srv := intakeServer(t, Config{DBPath: v1Path, Repo: releasetest.Repo, CacheDir: t.TempDir(), Logger: logger})
	srv.gh = srv.newGHClient(releasetest.Repo, f.URL)

	if err := srv.intake.queue.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("a purge failure reached the refresh: %v", err)
	}
	if srv.loaded != tagR1 || srv.current().Stats().Works != 5 {
		t.Errorf("loaded = %q with %d works: the swap did not stand", srv.loaded, srv.current().Stats().Works)
	}
	eventually(t, "the failure in the log", func() bool {
		return strings.Contains(logged.String(), "intake queue purge failed")
	})
}

// TestIntakeSwapKeepsTheQueue: a swap and its prune leave the queue alone, even
// with the file sitting in the cache directory itself.
func TestIntakeSwapKeepsTheQueue(t *testing.T) {
	v1Path := artifacttest.Build(t, artifacttest.Fixture())
	late := readDB(t, artifacttest.Build(t, intakeLateCatalog()))
	f := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, late, "", nil)})
	cache := t.TempDir()
	queue := filepath.Join(cache, "queue.sqlite")
	srv := intakeServer(t, Config{DBPath: v1Path, Repo: releasetest.Repo, CacheDir: cache, IntakeDB: queue})
	srv.gh = srv.newGHClient(releasetest.Repo, f.URL)
	report(t, srv, unknownA)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.pruneCacheLocked()
	srv.mu.Unlock()
	wantQueue(t, srv, unknownA)
}

// TestIntakeQueueSurvivesARestart: the file is the state.
func TestIntakeQueueSurvivesARestart(t *testing.T) {
	queue := filepath.Join(t.TempDir(), "queue.sqlite")
	first := intakeServer(t, Config{IntakeDB: queue})
	report(t, first, unknownA)
	first.closeIntake()
	second := intakeServer(t, Config{IntakeDB: queue})
	wantQueue(t, second, unknownA)
}

// TestIntakeReadsSurviveSwaps: a report loads its snapshot once, so swapping under
// a stream of them never closes one mid-read while it is inside its grace (and the
// race detector sees the handoff).
func TestIntakeReadsSurviveSwaps(t *testing.T) {
	srv := intakeServer(t, Config{})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad atomic.Int32
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := intakeReq{body: asinsBody(knownASIN, unknownA), remote: nextRemote()}.do(srv)
				if rec.Code != http.StatusAccepted {
					bad.Add(1)
					return
				}
			}
		}()
	}
	for range 5 {
		next, err := query.Open(artifacttest.Build(t, artifacttest.Fixture()), "")
		if err != nil {
			t.Fatal(err)
		}
		srv.mu.Lock()
		srv.swap(next)
		srv.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d reports failed across swaps", bad.Load())
	}
}

// ---- the read route --------------------------------------------------------

func TestIntakeTokenRefusal(t *testing.T) {
	srv := intakeServer(t, Config{})
	report(t, srv, unknownA)
	for name, auth := range map[string]string{
		"none":           "",
		"wrong":          "Bearer " + strings.Repeat("x", len(intakeTestToken)),
		"a prefix":       "Bearer " + intakeTestToken[:len(intakeTestToken)-1],
		"an extension":   "Bearer " + intakeTestToken + "x",
		"the wrong case": "Bearer " + strings.ToUpper(intakeTestToken),
		"basic":          "Basic " + intakeTestToken,
		"no scheme":      intakeTestToken,
		"scheme only":    "Bearer",
		"empty":          "Bearer ",
	} {
		t.Run(name, func(t *testing.T) {
			rec := intakeReq{method: http.MethodGet, auth: auth}.do(srv)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q", got)
			}
			if strings.Contains(rec.Body.String(), unknownA) {
				t.Error("a refused read carried the queue")
			}
		})
	}
	// Denied: the media routes' query token has no meaning here.
	r := httptest.NewRequest(http.MethodGet, intakePath+"?token="+intakeTestToken, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("?token= = %d, want 401", rec.Code)
	}
	// Allowed, the scheme read as HTTP says it is: case-insensitively.
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		rec := intakeReq{method: http.MethodGet, auth: scheme + " " + intakeTestToken}.do(srv)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), unknownA) {
			t.Errorf("%s = %d %s, want 200 with the queue", scheme, rec.Code, rec.Body)
		}
	}
}

// TestIntakeShortTokenLeavesOnlyTheReadRouteOff: too short is logged without the
// value, and the POST route, which never needed it, is unaffected.
func TestIntakeShortTokenLeavesOnlyTheReadRouteOff(t *testing.T) {
	const short = "this-token-is-too-short"
	for name, token := range map[string]string{"short": short, "unset": ""} {
		t.Run(name, func(t *testing.T) {
			logger, logged := newLoggerCapture()
			srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), Site: writeSiteFixture(t),
				IntakeDB: filepath.Join(t.TempDir(), "queue.sqlite"), IntakeToken: token, swapGrace: time.Minute, Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.closeIntake)
			if rec := (intakeReq{body: asinsBody(unknownA), remote: nextRemote()}).do(srv); rec.Code != http.StatusAccepted {
				t.Errorf("POST = %d, want 202", rec.Code)
			}
			// The static site takes what the route table does not claim, as it does
			// for any path; the token cannot be tried at all.
			rec := intakeReq{method: http.MethodGet, auth: "Bearer " + token}.do(srv)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), unknownA) {
				t.Errorf("GET = %d %q, want the site's 404", rec.Code, rec.Body)
			}
			out := logged.String()
			if strings.Contains(out, short) {
				t.Errorf("the log holds the token: %q", out)
			}
			if token != "" && !strings.Contains(out, "shorter than 32 bytes") {
				t.Errorf("log = %q, want the short token named", out)
			}
		})
	}

	// With no site, the mux answers for a path it knows under another method.
	srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()),
		IntakeDB: filepath.Join(t.TempDir(), "queue.sqlite"), swapGrace: time.Minute, Logger: artifacttest.QuietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.closeIntake)
	rec := intakeReq{method: http.MethodGet}.do(srv)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET with no site = %d, Allow %q; want 405, POST", rec.Code, rec.Header().Get("Allow"))
	}
}

// TestIntakeHasNoCORS: nothing a browser could use, from any answer on the path.
func TestIntakeHasNoCORS(t *testing.T) {
	for name, site := range map[string]string{"with a site": writeSiteFixture(t), "api only": ""} {
		t.Run(name, func(t *testing.T) {
			srv := intakeServer(t, Config{Site: site})
			do := func(method string, hdr map[string]string, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, intakePath, strings.NewReader(body))
				r.RemoteAddr = "192.0.2.200:1000"
				r.Header.Set("Origin", "https://evil.example")
				for k, v := range hdr {
					r.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, r)
				return rec
			}
			jsonBody := map[string]string{"Content-Type": "application/json"}
			preflight := map[string]string{"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"}
			answers := map[string]*httptest.ResponseRecorder{
				"preflight": do(http.MethodOptions, preflight, ""),
				"post":      do(http.MethodPost, jsonBody, asinsBody(unknownA)),
				"post 415":  do(http.MethodPost, nil, asinsBody(unknownA)),
				"read":      do(http.MethodGet, map[string]string{"Authorization": "Bearer " + intakeTestToken}, ""),
				"read 401":  do(http.MethodGet, nil, ""),
			}
			for range intakeReportsPerDay {
				answers["post 429"] = do(http.MethodPost, jsonBody, asinsBody(unknownA))
			}
			if answers["post 429"].Code != http.StatusTooManyRequests {
				t.Fatalf("expected the last POST refused, got %d", answers["post 429"].Code)
			}
			if code := answers["preflight"].Code; code >= 200 && code < 300 {
				t.Errorf("preflight = %d: it must fail", code)
			}
			for name, rec := range answers {
				for header := range rec.Header() {
					if strings.HasPrefix(http.CanonicalHeaderKey(header), "Access-Control-") {
						t.Errorf("%s carries %s", name, header)
					}
				}
			}
		})
	}
}

// ---- the limiter -----------------------------------------------------------

// TestIntakeLimit runs the limiter through the route: six a day, the same client
// reading as the global limiter does it, and a refusal that says so honestly.
func TestIntakeLimit(t *testing.T) {
	proxies, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	clock := newSafeClock()
	// The global limit is off here, and the POST limit must be on regardless.
	srv := intakeServer(t, Config{now: clock.now, TrustedProxies: proxies})
	const proxy = "10.1.2.3:5000"
	post := func(remote string, xff ...string) *httptest.ResponseRecorder {
		return intakeReq{body: asinsBody(unknownA), remote: remote, xff: xff}.do(srv)
	}

	for i := range intakeReportsPerDay {
		if rec := post(proxy, "203.0.113.7"); rec.Code != http.StatusAccepted {
			t.Fatalf("report %d of the burst = %d", i+1, rec.Code)
		}
	}
	refused := post(proxy, "203.0.113.7")
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the seventh = %d, want 429", refused.Code)
	}
	secs, err := strconv.Atoi(refused.Header().Get("Retry-After"))
	if err != nil || secs < 4*3600 || secs > 4*3600+1 {
		t.Errorf("Retry-After = %q, want about four hours", refused.Header().Get("Retry-After"))
	}
	if cc := refused.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var doc map[string]string
	if err := json.Unmarshal(refused.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc["error"], "at most 6 reports a day") {
		t.Errorf("message = %q, want the limit in its own units", doc["error"])
	}
	if strings.Contains(doc["error"], "e-05") || strings.Contains(doc["error"], "a second") {
		t.Errorf("message = %q: the global limiter's wording leaked", doc["error"])
	}

	// Another client behind the same proxy has its own bucket.
	if rec := post(proxy, "203.0.113.8"); rec.Code != http.StatusAccepted {
		t.Errorf("a second client through the proxy = %d, want 202", rec.Code)
	}
	// Denied: a forged leftmost hop through the proxy is not a new client.
	if rec := post(proxy, "198.51.100.1, 203.0.113.7"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a forged hop through the proxy = %d, want 429", rec.Code)
	}
	// Denied: from an untrusted address the header is not read at all.
	const direct = "192.0.2.50:6000"
	for range intakeReportsPerDay {
		if rec := post(direct, "198.51.100.2"); rec.Code != http.StatusAccepted {
			t.Fatalf("direct client's burst = %d", rec.Code)
		}
	}
	if rec := post(direct, "198.51.100.3"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a direct client naming a fresh address = %d, want 429", rec.Code)
	}
	// Denied: a /64 is one client.
	for i := range intakeReportsPerDay {
		if rec := post(fmt.Sprintf("[2001:db8:1:2::%x]:7000", i+1)); rec.Code != http.StatusAccepted {
			t.Fatalf("ipv6 burst %d = %d", i+1, rec.Code)
		}
	}
	if rec := post("[2001:db8:1:2::ff]:7000"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("another address of one /64 = %d, want 429", rec.Code)
	}

	// Four hours buys one report back, and only one.
	clock.advance(4*time.Hour + time.Second)
	if rec := post(proxy, "203.0.113.7"); rec.Code != http.StatusAccepted {
		t.Errorf("after four hours = %d, want 202", rec.Code)
	}
	if rec := post(proxy, "203.0.113.7"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("straight after = %d, want 429", rec.Code)
	}
}

// TestIntakeLimitersAreSeparate: neither limiter's budget or wording reaches the
// other, and the read route does not spend reports.
func TestIntakeLimitersAreSeparate(t *testing.T) {
	srv := intakeServer(t, Config{RateLimit: 0.001, RateBurst: 2})
	const client = "192.0.2.60:6000"
	stats := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
		r.RemoteAddr = client
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec
	}
	// Six reports leave the global bucket full.
	for range intakeReportsPerDay {
		if rec := (intakeReq{body: asinsBody(unknownA), remote: client}).do(srv); rec.Code != http.StatusAccepted {
			t.Fatalf("report = %d", rec.Code)
		}
	}
	if len(srv.limiter.buckets) != 0 {
		t.Error("a report's address reached the global limiter's buckets, which the intake sweep does not cover")
	}
	for i := range 2 {
		if rec := stats(); rec.Code != http.StatusOK {
			t.Fatalf("stats %d = %d", i+1, rec.Code)
		}
	}
	rec := stats()
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "requests a second") {
		t.Errorf("past the global limit = %d %s, want its own wording", rec.Code, rec.Body)
	}

	// The read route is on the global budget, which this client has spent, and
	// spends no reports.
	const reader = "192.0.2.61:6000"
	for range 2 {
		if rec := (intakeReq{method: http.MethodGet, auth: "Bearer " + intakeTestToken, remote: reader}).do(srv); rec.Code != http.StatusOK {
			t.Fatalf("read = %d", rec.Code)
		}
	}
	if rec := (intakeReq{method: http.MethodGet, auth: "wrong", remote: reader}).do(srv); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a guess past the global limit = %d, want 429", rec.Code)
	}
	if rec := (intakeReq{body: asinsBody(unknownA), remote: reader}).do(srv); rec.Code != http.StatusAccepted {
		t.Errorf("a report after reads = %d, want 202", rec.Code)
	}
}

// TestIntakeLimiterForgetsAnAddressWithNoTraffic is trap 1 from the plan. Nothing
// calls the limiter after the one report, so only the server's own ticker can drop
// the address: it is still held short of the 24 hours and gone just past them.
func TestIntakeLimiterForgetsAnAddressWithNoTraffic(t *testing.T) {
	clock := newSafeClock()
	srv := intakeServer(t, Config{now: clock.now, intakeTick: 5 * time.Millisecond})
	addr := netip.MustParseAddr("203.0.113.99")
	held := func() bool {
		l := srv.intake.limiter
		l.mu.Lock()
		defer l.mu.Unlock()
		_, ok := l.buckets[addr]
		return ok
	}
	if rec := (intakeReq{body: asinsBody(unknownA), remote: "203.0.113.99:5000"}).do(srv); rec.Code != http.StatusAccepted {
		t.Fatalf("report = %d", rec.Code)
	}
	if !held() {
		t.Fatal("the report made no bucket")
	}

	// Short of the day it stays: the sweep is what is on the clock, not the promise.
	clock.advance(23*time.Hour + 58*time.Minute)
	srv.intake.limiter.sweepIdle()
	time.Sleep(30 * time.Millisecond) // several ticks
	if !held() {
		t.Fatal("the address went before its bucket had refilled, which would hand back the allowance")
	}

	// Past it, with no request of any kind, the ticker drops it.
	clock.advance(2*time.Minute + 2*sweepEvery)
	eventually(t, "the address to leave the limiter", func() bool { return !held() })
}

// TestIntakeSweepEndsWithTheServer: the ticker is tied to the server's lifetime.
func TestIntakeSweepEndsWithTheServer(t *testing.T) {
	srv := intakeServer(t, Config{intakeTick: time.Millisecond})
	srv.closeIntake()
	select {
	case <-srv.intake.done:
	default:
		t.Error("closeIntake returned with the loop still running")
	}
	srv.closeIntake() // twice is fine
}

// TestRunClosesTheQueue: Run owns the shutdown, after the requests have drained.
func TestRunClosesTheQueue(t *testing.T) {
	srv := intakeServer(t, Config{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- srv.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return")
	}
	if err := srv.intake.queue.db.Ping(); err == nil {
		t.Error("the queue is still open after Run returned")
	}
}

// ---- what is kept, what is not ---------------------------------------------

// TestIntakeSchemaHoldsNoClientData pins the table to the plan's one statement, so
// a client column, a timestamp or a rowid cannot arrive without this failing.
func TestIntakeSchemaHoldsNoClientData(t *testing.T) {
	srv := intakeServer(t, Config{})
	rec := intakeReq{body: asinsBody(unknownA), remote: "203.0.113.42:5000", ua: "audiosilo-server/9.9-marker"}.do(srv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("report = %d", rec.Code)
	}
	db := srv.intake.queue.db

	rows, err := db.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var objects []string
	for rows.Next() {
		var typ, name, stmt string
		if err := rows.Scan(&typ, &name, &stmt); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, typ+" "+name)
		if name == "intake_asins" && stmt != intakeSchema {
			t.Errorf("the table is %q, want %q", stmt, intakeSchema)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if !slices.Equal(objects, []string{"table intake_asins"}) {
		t.Errorf("schema objects = %v, want the one table", objects)
	}
	want := "CREATE TABLE intake_asins(asin TEXT PRIMARY KEY, count INTEGER NOT NULL, first_day TEXT NOT NULL, last_day TEXT NOT NULL) WITHOUT ROWID"
	if intakeSchema != want {
		t.Errorf("schema = %q", intakeSchema)
	}
	if _, err := db.Exec(`SELECT rowid FROM intake_asins`); err == nil {
		t.Error("the table has a rowid")
	}

	day := queueRows(t, srv)[0].FirstDay
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		t.Errorf("first_day = %q, want a bare date", day)
	}
	raw, err := os.ReadFile(srv.cfg.IntakeDB)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"203.0.113.42", "audiosilo-server/9.9-marker"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("the file holds %q", leak)
		}
	}
}

// TestIntakeLogsNoClientData is trap 3: every kind of answer, the failures included,
// with the log read afterwards.
func TestIntakeLogsNoClientData(t *testing.T) {
	proxies, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	logger, logged := newLoggerCapture()
	srv := intakeServer(t, Config{Logger: logger, TrustedProxies: proxies})
	const (
		address = "203.0.113.77"
		agent   = "audiosilo-server/log-marker"
	)
	send := func(method, body, auth string) {
		intakeReq{method: method, body: body, auth: auth, ua: agent, remote: "10.0.0.1:5000", xff: []string{address}}.do(srv)
	}
	send(http.MethodPost, asinsBody(unknownA, knownASIN, "b0lowercase"), "")
	send(http.MethodPost, `{"asins":[`, "")
	send(http.MethodPost, asinsBody(unknownB), "")
	send(http.MethodGet, "", "Bearer "+intakeTestToken)
	send(http.MethodGet, "", "Bearer wrong")
	// A row the purge will take, and it logging what it did.
	seedQueue(t, srv.intake.queue, intakeRow{ASIN: knownASIN, Count: 1, FirstDay: "2026-11-01", LastDay: "2026-11-01"})
	srv.kickIntakePurge()
	eventually(t, "the purge", func() bool { return strings.Contains(logged.String(), "now known") })
	// A burst past the limit.
	for range intakeReportsPerDay {
		send(http.MethodPost, asinsBody(unknownA), "")
	}
	// Storage failing on both routes, with a fresh limiter's worth left for it.
	if err := srv.intake.queue.db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, remote := range []string{"192.0.2.70:1", "192.0.2.71:1"} {
		rec := intakeReq{body: asinsBody(unknownA, unknownB), remote: remote, ua: agent}.do(srv)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("POST over a closed queue = %d, want 503", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "database") || strings.Contains(rec.Body.String(), "sql") {
			t.Errorf("the 503 reflects the error: %q", rec.Body)
		}
	}
	rec := intakeReq{method: http.MethodGet, auth: "Bearer " + intakeTestToken, remote: "192.0.2.72:1", ua: agent}.do(srv)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET over a closed queue = %d, want 503", rec.Code)
	}

	out := logged.String()
	if !strings.Contains(out, "intake queue:") {
		t.Fatalf("the failures were not logged at all: %q", out)
	}
	for _, leak := range []string{address, "192.0.2.7", "10.0.0.1", agent, unknownA, unknownB, knownASIN, "b0lowercase", intakeTestToken} {
		if strings.Contains(out, leak) {
			t.Errorf("the log holds %q:\n%s", leak, out)
		}
	}
}

// TestIntakeLeavesNoSidecar is trap 4: no write-ahead log to hold batches in arrival
// order, no leftover journal, and a deleted row's bytes actually gone.
func TestIntakeLeavesNoSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.sqlite")
	srv := intakeServer(t, Config{IntakeDB: path})
	db := srv.intake.queue.db

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "delete") {
		t.Errorf("journal_mode = %q %v, want delete", mode, err)
	}
	var secure int
	if err := db.QueryRow(`PRAGMA secure_delete`).Scan(&secure); err != nil || secure != 1 {
		t.Errorf("secure_delete = %d %v, want 1", secure, err)
	}

	report(t, srv, unknownA)
	report(t, srv, unknownB)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "queue.sqlite" {
			t.Errorf("sidecar %s left behind", e.Name())
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: the queue is a token-only read", perm)
	}

	if err := srv.intake.queue.remove(context.Background(), []string{unknownA}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(unknownA)) {
		t.Error("a deleted row's ASIN is still in the file")
	}
	if !bytes.Contains(raw, []byte(unknownB)) {
		t.Error("the row left behind is not in the file, so this test proves nothing")
	}
}

// TestIntakeASINIsTheSchemasPattern: the route's format is the catalogue's.
func TestIntakeASINIsTheSchemasPattern(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "common.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Defs["asin"].Pattern; got != intakeASIN.String() {
		t.Errorf("schema pattern %q, route pattern %q", got, intakeASIN)
	}
}

// ---- the spec --------------------------------------------------------------

// servedDoc fetches the spec through the server's own route.
func servedDoc(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("openapi.json = %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("served spec does not parse: %v", err)
	}
	return doc
}

// TestIntakeSpecFollowsTheRouteTable: in each of the three deployments the served
// document's intake operations are exactly the registered routes, every $ref in it
// still lands, and nothing else in it moved.
func TestIntakeSpecFollowsTheRouteTable(t *testing.T) {
	seed := artifacttest.Build(t, artifacttest.Fixture())
	queue := func() string { return filepath.Join(t.TempDir(), "queue.sqlite") }
	var full map[string]any
	if err := json.Unmarshal(openAPISpec, &full); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cfg  Config
		want []string
	}{
		{"off", Config{DBPath: seed}, nil},
		{"post only", Config{DBPath: seed, IntakeDB: queue()}, []string{"post"}},
		{"short token", Config{DBPath: seed, IntakeDB: queue(), IntakeToken: "short"}, []string{"post"}},
		{"both", Config{DBPath: seed, IntakeDB: queue(), IntakeToken: intakeTestToken}, []string{"get", "post"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.swapGrace = time.Minute
			tc.cfg.Logger = artifacttest.QuietLogger()
			srv, err := New(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.closeIntake)

			var registered []string
			for _, r := range srv.routes() {
				if r.specPath() == intakePath {
					method, _, _ := strings.Cut(r.pattern, " ")
					registered = append(registered, strings.ToLower(method))
				}
			}
			slices.Sort(registered)
			if !slices.Equal(registered, tc.want) {
				t.Errorf("registered = %v, want %v", registered, tc.want)
			}

			doc := servedDoc(t, srv)
			var described []string
			if item, ok := doc["paths"].(map[string]any)[intakePath].(map[string]any); ok {
				for method := range item {
					described = append(described, method)
				}
			}
			slices.Sort(described)
			if !slices.Equal(described, tc.want) {
				t.Errorf("described = %v, want %v", described, tc.want)
			}
			for _, ref := range collectRefs(doc) {
				if resolvePointer(doc, strings.Split(strings.TrimPrefix(ref, "#/"), "/")) == nil {
					t.Errorf("$ref %q resolves to nothing in this deployment's document", ref)
				}
			}
			for path, item := range full["paths"].(map[string]any) {
				if path != intakePath && !reflect.DeepEqual(doc["paths"].(map[string]any)[path], item) {
					t.Errorf("path %s changed", path)
				}
			}
			if !reflect.DeepEqual(doc["info"], full["info"]) || !reflect.DeepEqual(doc["tags"], full["tags"]) {
				t.Error("the document's info or tags changed")
			}
			if len(tc.want) == 2 && !bytes.Equal(srv.spec.body, openAPISpec) {
				t.Error("with everything on the document must be the file, byte for byte")
			}
			if srv.spec.etag == "" {
				t.Error("no ETag")
			}
		})
	}
}

// TestIntakeSpecETagsDiffer: a cache must not be handed the other deployment's
// document, and a conditional request still revalidates against this one's.
func TestIntakeSpecETagsDiffer(t *testing.T) {
	seed := artifacttest.Build(t, artifacttest.Fixture())
	off, err := New(Config{DBPath: seed, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	on := intakeServer(t, Config{DBPath: seed})
	if off.spec.etag == on.spec.etag {
		t.Error("two different documents share an ETag")
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	r.Header.Set("If-None-Match", off.spec.etag)
	rec := httptest.NewRecorder()
	off.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", rec.Code)
	}
}

func TestCutJSONMember(t *testing.T) {
	const doc = `{
  "a": 1,
  "b": { "x": [1, {"y": 2}], "y": "}", "z": {"deep": true} },
  "c": "last"
}
`
	for _, tc := range []struct {
		name string
		path []string
		want string
	}{
		{"first", []string{"a"}, "{\n  \"b\": { \"x\": [1, {\"y\": 2}], \"y\": \"}\", \"z\": {\"deep\": true} },\n  \"c\": \"last\"\n}\n"},
		{"middle", []string{"b"}, "{\n  \"a\": 1,\n  \"c\": \"last\"\n}\n"},
		{"last", []string{"c"}, "{\n  \"a\": 1,\n  \"b\": { \"x\": [1, {\"y\": 2}], \"y\": \"}\", \"z\": {\"deep\": true} }\n}\n"},
		{"nested first", []string{"b", "x"}, "{\n  \"a\": 1,\n  \"b\": { \"y\": \"}\", \"z\": {\"deep\": true} },\n  \"c\": \"last\"\n}\n"},
		{"nested last", []string{"b", "z"}, "{\n  \"a\": 1,\n  \"b\": { \"x\": [1, {\"y\": 2}], \"y\": \"}\" },\n  \"c\": \"last\"\n}\n"},
		{"the only member", []string{"b", "z", "deep"}, "{\n  \"a\": 1,\n  \"b\": { \"x\": [1, {\"y\": 2}], \"y\": \"}\", \"z\": {} },\n  \"c\": \"last\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cutJSONMember([]byte(doc), tc.path...)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			if !json.Valid(got) {
				t.Error("the result is not JSON")
			}
		})
	}
	for name, path := range map[string][]string{
		"no such member":      {"nope"},
		"no such nested":      {"b", "nope"},
		"inside a non-object": {"a", "x"},
		// A member of the same name deeper down is not this path's.
		"name only deeper": {"deep"},
	} {
		if _, err := cutJSONMember([]byte(doc), path...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestIntakeSpecMembersExist keeps the cut list and the file in step: a rename in
// the file would otherwise only log at boot and leave an entry advertised.
func TestIntakeSpecMembersExist(t *testing.T) {
	for _, in := range []*intakeState{nil, {}, {token: intakeTestToken}} {
		s := &Server{intake: in, log: artifacttest.QuietLogger()}
		body := openAPISpec
		for _, member := range s.unofferedSpecMembers() {
			cut, err := cutJSONMember(body, member...)
			if err != nil {
				t.Fatalf("%v: %v", member, err)
			}
			body = cut
		}
		if !json.Valid(body) {
			t.Error("the cut document is not JSON")
		}
	}
}
