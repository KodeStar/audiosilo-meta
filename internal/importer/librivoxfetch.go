package importer

// librivoxfetch.go is the POLITE fetcher behind `metaimport librivox fetch`
// (LICENSING.md's public-domain exception; LIBRIVOX-PLAN.md decision 8). It
// pages LibriVox's audiobooks feed into a snapshot directory that
// ReadLibrivoxSnapshot reads, and it is built around what LibriVox asked of
// its clients when it capped the API (2026-09): small pages, several seconds
// between requests, an identifiable client, and backing off when told to.
//
//   - limit=100 per page (the feed's cap is 500; 100 keeps each request cheap),
//     extended=1 (sections, genres, translators, url_iarchive) and coverart=1;
//   - at least Spacing (5 s) between the START of any two requests, retries
//     included;
//   - a User-Agent naming meta.audiosilo.app and a contact address;
//   - 429 and 5xx back off: Retry-After when the server sends one (seconds or
//     an HTTP date), else an exponential wait from BackoffBase capped at
//     MaxBackoff, for at most MaxRetries retries (MaxRetries+1 requests per
//     page) before the run stops - resumably;
//   - RESUMABLE: every page is written atomically before the manifest records
//     it, so a killed run resumes at the first page it had not finished.
//
// The end of the catalogue is a short page (fewer than limit books) or the
// feed's "no results" answer (a 404 carrying {"error": ...}). Pages are offset
// windows over the feed's default order, ascending project id, so a project
// catalogued during a long fetch lands at the end rather than shifting a window
// already read.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/atomicfile"
)

// LibrivoxFeedURL is LibriVox's audiobooks feed.
const LibrivoxFeedURL = "https://librivox.org/api/feed/audiobooks"

// LibrivoxUserAgent identifies the fetcher to LibriVox: who we are and how to
// reach us, which is what a volunteer reading their access log needs.
const LibrivoxUserAgent = "audiosilo-meta-importer/1.0 (+https://meta.audiosilo.app; kode@audiosilo.app)"

// The polite defaults (LIBRIVOX-PLAN.md decision 8).
const (
	librivoxPageLimit   = 100
	librivoxSpacing     = 5 * time.Second
	librivoxMaxRetries  = 6
	librivoxBackoffBase = 30 * time.Second
	librivoxMaxBackoff  = 10 * time.Minute
	// librivoxMaxBody bounds one response read: a 100-project extended page is
	// well under a megabyte, so anything near this is not a feed page.
	librivoxMaxBody = 64 << 20
)

// librivoxQuery is the fixed part of every page request; limit and offset are
// added per page. It is recorded in the manifest so a resume can refuse a
// snapshot fetched with other parameters.
const librivoxQuery = "coverart=1&extended=1&format=json"

// LibrivoxFetcher fetches a snapshot. The zero value is not usable; start from
// NewLibrivoxFetcher, which sets the polite defaults, and override only in tests.
type LibrivoxFetcher struct {
	FeedURL     string
	UserAgent   string
	HTTP        *http.Client
	Limit       int
	Spacing     time.Duration
	MaxRetries  int
	BackoffBase time.Duration
	MaxBackoff  time.Duration
	// Sleep waits d or until ctx ends; Now is the clock. Both are injectable so
	// a test runs offline at full speed and can see every wait asked for.
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
	// Log receives one progress line per page and per retry; nil is silent.
	Log io.Writer

	lastRequest time.Time
}

// NewLibrivoxFetcher returns a fetcher with the polite defaults.
func NewLibrivoxFetcher() *LibrivoxFetcher {
	return &LibrivoxFetcher{
		FeedURL:     LibrivoxFeedURL,
		UserAgent:   LibrivoxUserAgent,
		HTTP:        &http.Client{Timeout: 2 * time.Minute},
		Limit:       librivoxPageLimit,
		Spacing:     librivoxSpacing,
		MaxRetries:  librivoxMaxRetries,
		BackoffBase: librivoxBackoffBase,
		MaxBackoff:  librivoxMaxBackoff,
		Sleep:       sleepCtx,
		Now:         time.Now,
	}
}

// sleepCtx waits d, returning early with ctx's error when it ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// librivoxManifest is a snapshot's resume state (<snapshot>/manifest.json).
type librivoxManifest struct {
	Feed       string `json:"feed"`
	Query      string `json:"query"`
	Limit      int    `json:"limit"`
	NextOffset int    `json:"next_offset"`
	Pages      int    `json:"pages"`
	Projects   int    `json:"projects"`
	Complete   bool   `json:"complete"`
	StartedAt  string `json:"started_at"`
	UpdatedAt  string `json:"updated_at"`
}

// LibrivoxFetchResult is what one Fetch call did.
type LibrivoxFetchResult struct {
	PagesFetched int
	Projects     int // projects in the pages this call fetched
	Requests     int // HTTP requests made, retries included
	NextOffset   int
	Complete     bool
	// TotalProjects is the snapshot's whole count, this call's pages included.
	TotalProjects int
}

// Fetch pages the feed into dir, resuming from dir's manifest when there is one,
// until the catalogue ends or maxPages pages were fetched (0 = no cap). A
// snapshot that is already complete makes no request.
func (f *LibrivoxFetcher) Fetch(ctx context.Context, dir string, maxPages int) (LibrivoxFetchResult, error) {
	if f.Limit < 1 || f.Limit > 500 {
		return LibrivoxFetchResult{}, fmt.Errorf("librivox fetch: limit %d is outside 1-500 (the feed's cap)", f.Limit)
	}
	m, err := f.openManifest(dir)
	if err != nil {
		return LibrivoxFetchResult{}, err
	}
	res := LibrivoxFetchResult{NextOffset: m.NextOffset, Complete: m.Complete, TotalProjects: m.Projects}
	for !m.Complete && (maxPages == 0 || res.PagesFetched < maxPages) {
		body, end, requests, err := f.page(ctx, m.NextOffset)
		res.Requests += requests
		if err != nil {
			return res, err
		}
		n := 0
		if body != nil {
			books, err := parseLibrivoxPage(body)
			if err != nil {
				return res, fmt.Errorf("librivox fetch: offset %d: %w", m.NextOffset, err)
			}
			n = len(books)
		}
		if n > 0 {
			if err := writeAtomic(filepath.Join(dir, librivoxPagesDir, fmt.Sprintf("offset-%07d.json", m.NextOffset)), body); err != nil {
				return res, err
			}
			m.Pages++
			m.Projects += n
			res.PagesFetched++
			res.Projects += n
			m.NextOffset += n
		}
		m.Complete = end || n < f.Limit
		m.UpdatedAt = f.Now().UTC().Format(time.RFC3339)
		if err := writeManifest(dir, m); err != nil {
			return res, err
		}
		// Kept in step with the manifest just committed, so a run that stops on a
		// later page (an interrupt, a give-up) still reports where the snapshot
		// stands rather than where this call started.
		res.NextOffset, res.Complete, res.TotalProjects = m.NextOffset, m.Complete, m.Projects
		done := ""
		if m.Complete {
			done = "; catalogue complete"
		}
		f.logf("librivox fetch: offset %d: %d projects (%d in snapshot)%s", m.NextOffset-n, n, m.Projects, done)
	}
	return res, nil
}

// openManifest reads dir's manifest, or starts one. A manifest written with
// other request parameters is refused: its offsets count other windows.
func (f *LibrivoxFetcher) openManifest(dir string) (librivoxManifest, error) {
	path := filepath.Join(dir, librivoxManifestFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		now := f.Now().UTC().Format(time.RFC3339)
		return librivoxManifest{Feed: f.FeedURL, Query: librivoxQuery, Limit: f.Limit, StartedAt: now, UpdatedAt: now}, nil
	}
	if err != nil {
		return librivoxManifest{}, err
	}
	var m librivoxManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return librivoxManifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if m.Feed != f.FeedURL || m.Query != librivoxQuery || m.Limit != f.Limit {
		return librivoxManifest{}, fmt.Errorf("%s was fetched from %s?%s with limit %d; resume it with the same parameters or start a new snapshot directory",
			path, m.Feed, m.Query, m.Limit)
	}
	return m, nil
}

// page fetches one window, retrying 429/5xx/transport failures politely. It
// returns the body of a 200, end=true for the feed's "no results" 404, and the
// number of requests it made.
func (f *LibrivoxFetcher) page(ctx context.Context, offset int) (body []byte, end bool, requests int, err error) {
	u := fmt.Sprintf("%s?%s&limit=%d&offset=%d", f.FeedURL, librivoxQuery, f.Limit, offset)
	for attempt := 0; ; attempt++ {
		if err := f.waitTurn(ctx); err != nil {
			return nil, false, requests, err
		}
		requests++
		status, retryAfter, b, rerr := f.get(ctx, u)
		switch {
		case rerr == nil && status == http.StatusOK:
			return b, false, requests, nil
		case rerr == nil && status == http.StatusNotFound && librivoxNoResults(b):
			return nil, true, requests, nil
		case rerr == nil && status != http.StatusTooManyRequests && status < 500:
			return nil, false, requests, fmt.Errorf("librivox fetch: %s: HTTP %d: %s", u, status, snippet(b))
		}
		if attempt >= f.MaxRetries {
			if rerr != nil {
				return nil, false, requests, fmt.Errorf("librivox fetch: %s: giving up after %d tries: %w (rerun to resume)", u, requests, rerr)
			}
			return nil, false, requests, fmt.Errorf("librivox fetch: %s: giving up after %d tries: HTTP %d (rerun to resume)", u, requests, status)
		}
		wait := f.backoff(attempt, retryAfter)
		why := fmt.Sprintf("HTTP %d", status)
		if rerr != nil {
			why = rerr.Error()
		}
		f.logf("librivox fetch: offset %d: %s; waiting %s before retry %d of %d", offset, why, wait, attempt+1, f.MaxRetries)
		if err := f.Sleep(ctx, wait); err != nil {
			return nil, false, requests, err
		}
	}
}

// waitTurn holds the next request until Spacing has passed since the last one
// began. The first request of a fetcher's life goes straight out.
func (f *LibrivoxFetcher) waitTurn(ctx context.Context) error {
	if !f.lastRequest.IsZero() {
		if d := f.Spacing - f.Now().Sub(f.lastRequest); d > 0 {
			if err := f.Sleep(ctx, d); err != nil {
				return err
			}
		}
	}
	f.lastRequest = f.Now()
	return nil
}

// get makes one request. retryAfter is the server's Retry-After, 0 when absent
// or unreadable.
func (f *LibrivoxFetcher) get(ctx context.Context, u string) (status int, retryAfter time.Duration, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return 0, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(resp.Body, librivoxMaxBody))
	if err != nil {
		return 0, 0, nil, err
	}
	return resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After"), f.Now()), body, nil
}

// backoff is the wait before retry attempt+1: the server's Retry-After when it
// sent one (never less than Spacing), else BackoffBase doubled per attempt,
// capped at MaxBackoff.
func (f *LibrivoxFetcher) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return max(retryAfter, f.Spacing)
	}
	d := f.BackoffBase
	for i := 0; i < attempt && d < f.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, f.MaxBackoff)
}

// parseRetryAfter reads a Retry-After header: delay-seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// librivoxNoResults reports whether a 404 body is the feed's "no results"
// answer ({"error": "Audiobooks could not be found"}) rather than a real 404.
func librivoxNoResults(body []byte) bool {
	var e struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(body, &e) == nil && strings.Contains(strings.ToLower(e.Error), "could not be found")
}

// snippet is the head of a body, for an error message.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func (f *LibrivoxFetcher) logf(format string, args ...any) {
	if f.Log != nil {
		_, _ = fmt.Fprintf(f.Log, format+"\n", args...)
	}
}

// writeAtomic replaces path with b in one rename.
func writeAtomic(path string, b []byte) error {
	file, err := atomicfile.Stage(path)
	if err != nil {
		return err
	}
	file.Write(b)
	return atomicfile.CommitInOrder(file)
}

// writeManifest commits the manifest, after the page it records.
func writeManifest(dir string, m librivoxManifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, librivoxManifestFile), append(b, '\n'))
}
