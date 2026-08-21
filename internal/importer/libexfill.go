package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// libexfill.go is the AUTOMATIC half of the libex enrichment story.
//
// `metaimport libex --enrich` fills a catalogue from rows an operator already
// has. That is the right shape for a dump-driven wave, and the wrong shape for
// the case this file exists for: a personal library export has just landed, it
// states no cover and no chapter list, and the handful of ASINs it needs are
// known exactly. Exporting a million rows to fill twelve is not a workflow
// anyone runs, so in practice nobody ran one - every audiosilo-books import in
// the tree sat with placeholder covers because filling them was a manual step.
//
// So this selects the gaps FROM THE CATALOGUE, fetches only those ASINs from
// the live libex service, and hands the rows to the existing enrichment pass.
// Nothing about the enrichment itself is new: same planner, same fill-absent
// rule, same runtime/date contradiction guard, same libex-import provenance, so
// a fetched row can do nothing an operator's row could not.
//
// The live service is the source rather than a dump because a dump is a
// point-in-time mirror of libex's own database and the recent ASINs are exactly
// the ones missing from it - all three ASINs our dump lacked for the #1405
// import were served fine by the live endpoint, which fetches from Audible on
// demand.

// LibexBase is the public libex service the fill pass reads.
const LibexBase = "https://libexdb.com"

// libexFillRowKeys are the fields scripts/libex-export-rows.sql emits, and
// therefore the shape internal/importer/libex.go parses. A live record is a
// superset of them, so projecting it down to exactly these keys yields a row
// indistinguishable from a dump row - which is what lets the fetched rows go
// through the ordinary enrichment path with no special case anywhere in it.
var libexFillRowKeys = []string{
	"asin", "authors", "bookFormat", "chapters", "genres", "imageUrl",
	"language", "lengthMinutes", "narrators", "publisher", "region",
	"releaseDate", "series", "subtitle", "title",
}

// FillTarget is one recording the catalogue can have filled: an ASIN, and what
// it is missing.
type FillTarget struct {
	ASIN         string
	Work         string
	Recording    string
	NeedCover    bool
	NeedChapters bool
}

// SelectFillTargets returns the recordings that carry an ASIN but lack a cover,
// a chapter list, or both.
//
// Sorted by ASIN so a run's fetch order - and therefore its output, its rate
// limiting and its --limit truncation - does not depend on map iteration.
func SelectFillTargets(cat *model.Catalog) []FillTarget {
	var out []FillTarget
	for _, w := range cat.Works {
		for _, r := range w.Recordings {
			needCover := r.CoverURL == ""
			needChapters := len(r.Chapters) == 0
			if !needCover && !needChapters {
				continue
			}
			for _, a := range r.ASIN {
				if a.ASIN == "" {
					continue
				}
				out = append(out, FillTarget{
					ASIN: a.ASIN, Work: w.ID, Recording: r.ID,
					NeedCover: needCover, NeedChapters: needChapters,
				})
				// One ASIN is enough to identify the recording; a regional
				// sibling would fetch the same production twice.
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ASIN < out[j].ASIN })
	return out
}

// UserLibraryOnly restricts targets to recordings carrying a USER-LIBRARY
// source - an Audiobookshelf/audiosilo-books projection, a Libation export, an
// OpenAudible export, or a hand submission.
//
// This is the bound the automatic pass runs under, and it is the population the
// gap actually lives in. Measured over the tree: audiosilo-books records are 32%
// without a cover and 100% without chapters, because a personal library export
// states neither. libex-seeded records are 0.1% without a cover - the mirror
// carries them - so filling covers across the whole catalogue would be ~136,000
// lookups against a free public service to fix about 130 records.
//
// Chapters are a different story (79% of libex records have none), but that gap
// is a bulk backfill with its own batching, not something an intake run should
// start.
func UserLibraryOnly(targets []FillTarget, cat *model.Catalog) []FillTarget {
	tier := map[string]bool{}
	for _, w := range cat.Works {
		for _, r := range w.Recordings {
			for _, s := range r.Sources {
				if model.TierOfSource(s.Type) == model.TierUserLibrary {
					tier[w.ID+"\x00"+r.ID] = true
					break
				}
			}
		}
	}
	out := targets[:0:0]
	for _, t := range targets {
		if tier[t.Work+"\x00"+t.Recording] {
			out = append(out, t)
		}
	}
	return out
}

// SelectFillTargetsIn is SelectFillTargets restricted to a set of work ids -
// what the intake path uses, so a run triggered by one import fills that
// import's records rather than walking the whole catalogue.
func SelectFillTargetsIn(cat *model.Catalog, works map[string]bool) []FillTarget {
	if len(works) == 0 {
		return nil
	}
	all := SelectFillTargets(cat)
	out := all[:0:0]
	for _, t := range all {
		if works[t.Work] {
			out = append(out, t)
		}
	}
	return out
}

// LibexClient fetches single records from the libex service.
type LibexClient struct {
	BaseURL string
	HTTP    *http.Client
	// Pause is slept between fetches. libex is a free public service run by one
	// person and the live path proxies to Audible, so a fill run is deliberately
	// polite rather than fast.
	Pause time.Duration
}

// NewLibexClient returns a client with the defaults a fill run uses.
func NewLibexClient() *LibexClient {
	return &LibexClient{
		BaseURL: LibexBase,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Pause:   250 * time.Millisecond,
	}
}

// errLibexNotFound is returned when libex has no record for an ASIN. It is an
// ordinary outcome - plenty of a personal library is not on Audible at all - so
// the caller counts it rather than failing the run.
var errLibexNotFound = errors.New("libex: not found")

// fetchOne returns the raw record for one ASIN, trying libex's own mirrored copy
// first and its live Audible fetch second. Same order the site's client uses:
// the mirror is cheap and covers most of the catalogue, and the live path is
// what serves the recent releases the mirror has not caught up with.
func (c *LibexClient) fetchOne(ctx context.Context, asin string) (map[string]any, error) {
	var lastErr error
	for _, path := range []string{"/db/book/", "/book/"} {
		rec, err := c.get(ctx, path+url.PathEscape(asin))
		if err == nil {
			return rec, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchChapters returns the chapters payload for one ASIN, in the same
// mirror-then-live order fetchOne uses.
//
// The book record does NOT embed a chapter list - libex serves chapters from
// their own endpoint - so a fill run that only fetched the record filled almost
// no chapter gaps at all: measured against the live service, 19 of 13,387
// chapter-gap recordings. The batch pass that fetched 127k chapter lists used
// exactly these two paths.
func (c *LibexClient) fetchChapters(ctx context.Context, asin string) (map[string]any, error) {
	var lastErr error
	for _, path := range []string{"/db/book/", "/book/"} {
		doc, err := c.getDoc(ctx, path+url.PathEscape(asin)+"/chapters")
		if err == nil {
			return doc, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// get returns the RECORD at path. A record self-identifies with its ASIN, so a
// body without one is a miss whatever status it arrived under.
func (c *LibexClient) get(ctx context.Context, path string) (map[string]any, error) {
	doc, err := c.getDoc(ctx, path)
	if err != nil {
		return nil, err
	}
	if s, _ := doc["asin"].(string); s == "" {
		return nil, errLibexNotFound
	}
	return doc, nil
}

// getDoc is get without the record identity check: the chapters payload is a
// document ABOUT a book rather than a book, and does not restate its ASIN. The
// not-found and error-body semantics are the same, because they are properties
// of the service rather than of the shape it answered with.
func (c *LibexClient) getDoc(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errLibexNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("libex: %s: %s", path, resp.Status)
	}
	// A record is small; the cap is there so a wrong URL answering with
	// something enormous cannot be read into memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var rec map[string]any
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("libex: %s: %w", path, err)
	}
	// libex reports a miss as a 200 with an error body on some paths.
	if _, bad := rec["error"]; bad {
		return nil, errLibexNotFound
	}
	return rec, nil
}

// chapterListOf returns a document's usable chapter list, or nil. Absent, not an
// array, and empty are one outcome: there is nothing to attach.
func chapterListOf(doc map[string]any) []any {
	arr, _ := doc["chapters"].([]any)
	if len(arr) == 0 {
		return nil
	}
	return arr
}

// acceptChapters judges a fetched chapters payload against the record it belongs
// to, and returns the list to attach or nil. Two guards, both carried over from
// the batch pass that fetched 127k of these:
//
//   - libex states `isAccurate` on the payload. An explicit false is the service
//     saying the offsets do not describe this production, and nothing else in
//     the row can repair that.
//   - the payload's own `runtimeLengthMs` has to agree with the record's stated
//     runtime, or the chapters describe a different edition.
//
// The runtime tolerance is 10% OR 1.5 minutes, whichever is LARGER. The absolute
// floor is deliberate: the catalogue stores runtime as floored integer minutes,
// so on a short book the rounding is bigger than 10% of the value - a
// relative-only guard falsely refused about 1,300 short children's books.
func acceptChapters(rec, payload map[string]any) ([]any, bool) {
	list := chapterListOf(payload)
	if list == nil {
		return nil, false
	}
	if acc, ok := payload["isAccurate"].(bool); ok && !acc {
		return nil, false
	}
	minutes, okMin := coerceInt(rec["lengthMinutes"])
	ms, okMS := coerceInt(payload["runtimeLengthMs"])
	if okMin && okMS && minutes > 0 && ms > 0 {
		stated := float64(minutes)
		got := float64(ms) / 60000.0
		tol := 0.10 * stated
		if tol < 1.5 {
			tol = 1.5
		}
		if math.Abs(got-stated) > tol {
			return nil, false
		}
	}
	return list, true
}

// libexFillRow projects a live record down to the export row shape.
func libexFillRow(rec map[string]any) map[string]any {
	row := make(map[string]any, len(libexFillRowKeys))
	for _, k := range libexFillRowKeys {
		if v, ok := rec[k]; ok {
			row[k] = v
		}
	}
	// The credit lists arrive with fields the export does not emit; keep the
	// name shape so the parse layer sees exactly what a dump row gives it.
	for _, k := range []string{"authors", "narrators"} {
		if list, ok := rec[k].([]any); ok {
			names := make([]any, 0, len(list))
			for _, e := range list {
				if m, ok := e.(map[string]any); ok {
					if n, _ := m["name"].(string); n != "" {
						names = append(names, map[string]any{"name": n})
					}
				}
			}
			row[k] = names
		}
	}
	if _, ok := row["chapters"]; !ok {
		row["chapters"] = []any{}
	}
	return row
}

// FillReport is what a fetch pass produced.
type FillReport struct {
	Requested int
	Fetched   int
	NotFound  int
	Failed    int
	// ChaptersFetched counts the rows a chapter list was attached to.
	ChaptersFetched int
	// ChaptersRejected counts the payloads that arrived and were NOT attached -
	// the accuracy flag said so, the runtime disagreed, or the payload held no
	// chapters. A payload libex does not have at all is not counted here: nothing
	// was judged.
	ChaptersRejected int
	// Errors carries one line per failure, for the caller to report. A fill run
	// never fails as a whole on a per-ASIN error: the service is external and
	// best-effort, and a partial fill is strictly better than none.
	Errors []string
}

// FetchRows fetches the given targets and writes their rows as NDJSON to w.
//
// Errors are collected, never fatal: this is a best-effort pass over an
// external service, and the enrichment that follows is bounded by the
// catalogue anyway, so a row that did not arrive is simply a gap left for the
// next run.
func (c *LibexClient) FetchRows(ctx context.Context, targets []FillTarget, w io.Writer) (FillReport, error) {
	rep := FillReport{Requested: len(targets)}
	enc := json.NewEncoder(w)
	// The pause is a property of the SERVICE, not of the target list: it is slept
	// between requests, so a target that needs a second one for its chapters
	// paces exactly as the next target would.
	first := true
	pace := func() error {
		if first {
			first = false
			return nil
		}
		if c.Pause <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.Pause):
		}
		return nil
	}
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if err := pace(); err != nil {
			return rep, err
		}
		rec, err := c.fetchOne(ctx, t.ASIN)
		switch {
		case errors.Is(err, errLibexNotFound):
			rep.NotFound++
			continue
		case err != nil:
			rep.Failed++
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", t.ASIN, err))
			continue
		}
		row := libexFillRow(rec)
		if t.NeedChapters && chapterListOf(rec) == nil {
			if err := pace(); err != nil {
				return rep, err
			}
			c.fillChapters(ctx, t.ASIN, rec, row, &rep)
		}
		if err := enc.Encode(row); err != nil {
			return rep, err
		}
		rep.Fetched++
	}
	return rep, nil
}

// fillChapters fetches the chapters payload for one target and attaches it to
// the row if it passes acceptChapters.
//
// Chapters are best-effort WITHIN a row that is otherwise fine: the cover and
// every other fact still fill, so a payload that did not arrive, that libex does
// not have, or that the guards refused only means the row goes out without
// chapters. None of it is a failure for the run.
func (c *LibexClient) fillChapters(ctx context.Context, asin string, rec, row map[string]any, rep *FillReport) {
	payload, err := c.fetchChapters(ctx, asin)
	if err != nil {
		if !errors.Is(err, errLibexNotFound) {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: chapters: %v", asin, err))
		}
		return
	}
	list, ok := acceptChapters(rec, payload)
	if !ok {
		rep.ChaptersRejected++
		return
	}
	// The payload's entries are exactly the dump row's chapter shape
	// (title/lengthMs/startOffsetMs/startOffsetSec), so they go through as they
	// arrived - the parse layer reads the same keys either way.
	row["chapters"] = list
	rep.ChaptersFetched++
}

// LoadCatalogForFill loads the data tree and returns its catalogue.
//
// It goes through pkg/check rather than reading packs directly so a fill run
// refuses a tree that does not validate, for the same reason every writer does:
// enriching on top of a broken tree writes correct-looking records into a
// layout the next reader will not find them in.
// PROFILE: bare dataDir = ProfileAll by Options.Profile's own default rule
// (types.go carries the full statement; adding a --profile flag to this CLI
// means threading it here too).
func LoadCatalogForFill(dataDir string) (*model.Catalog, error) {
	res := check.Load(dataDir)
	if !res.OK() {
		return nil, fmt.Errorf("data tree does not validate (%d problems); run metacheck", len(res.Problems))
	}
	return res.Catalog, nil
}
