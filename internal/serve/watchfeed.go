package serve

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

const (
	defaultWatchWindow = 90
	maxWatchWindow     = 365
	maxWatchFeedItems  = 200
	watchFeedMaxAge    = "public, max-age=3600"
	watchFeedTitle     = "AudioSilo Meta: new in your series"
)

type watchFeedItem struct {
	id       string
	title    string
	state    string
	link     string
	authors  []string
	summary  string
	updated  time.Time
	series   string
	position string
}

type watchFeed struct {
	title    string
	subtitle string
	id       string
	self     string
	updated  time.Time
	items    []watchFeedItem
}

func (s *Server) handleWatchAtom(w http.ResponseWriter, r *http.Request) {
	s.handleWatchFeed(w, r, "application/atom+xml; charset=utf-8", renderAtomFeed)
}

func (s *Server) handleWatchJSON(w http.ResponseWriter, r *http.Request) {
	s.handleWatchFeed(w, r, "application/feed+json; charset=utf-8", renderJSONFeed)
}

func (s *Server) handleWatchFeed(
	w http.ResponseWriter,
	r *http.Request,
	contentType string,
	render func(watchFeed) ([]byte, error),
) {
	rawSeries := r.URL.Query().Get("s")
	series, err := decodeSeriesParam(rawSeries)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rawWindow := r.URL.Query().Get("window")
	window, err := watchWindow(rawWindow)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	snap := s.current()
	now := s.cfg.now().UTC()
	etag := watchFeedETag(snap, s.cfg.SiteURL, r.URL.Path, rawSeries, rawWindow, now)
	inm := r.Header.Get("If-None-Match")
	if matchesETag(inm, etag) || anyValidator(inm) {
		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Cache-Control", watchFeedMaxAge)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	feed, err := snap.watchFeed(series, window, now, s.cfg.SiteURL,
		s.watchFeedSelfURL(r.URL.Path, rawSeries, rawWindow), watchFeedID(rawSeries, rawWindow))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := render(feed)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("ETag", etag)
	h.Set("Cache-Control", watchFeedMaxAge)
	_, _ = w.Write(body)
}

// watchWindow reads the `window` parameter. It REJECTS a value it cannot use,
// where the list endpoints' clampLimit/clampOffset silently clamp one: those
// serve a page a caller can see is not what it asked for, while a feed URL is
// pasted once into a reader and then polled unattended for months. A typo that
// quietly became 90 days would never be noticed.
func watchWindow(raw string) (int, error) {
	if raw == "" {
		return defaultWatchWindow, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > maxWatchWindow {
		return 0, fmt.Errorf("window must be an integer from 1 to %d", maxWatchWindow)
	}
	return days, nil
}

// watchFeedSelfURL is the absolute URL the feed names as itself. It is composed
// from the two parameters this handler READ rather than echoed from the request
// line, so the document is a function of exactly what the validator below
// covers - a stray third parameter can neither reach the body nor give one URL
// the cached body of another.
func (s *Server) watchFeedSelfURL(path, rawSeries, rawWindow string) string {
	q := url.Values{"s": []string{rawSeries}}
	if rawWindow != "" {
		q.Set("window", rawWindow)
	}
	return s.cfg.SiteURL + path + "?" + q.Encode()
}

// watchFeedID is the feed's own stable id: one subscription URL is one feed,
// for the life of that URL. Deliberately free of the artifact and the clock -
// an id that moved with the data would make every poll look like a new feed.
func watchFeedID(rawSeries, rawWindow string) string {
	return "tag:meta.audiosilo.app,2026:watch/" + identity(rawSeries, rawWindow)
}

// watchFeedETag is the feed's cache validator. It covers the artifact, the
// representation (the path: .atom and .json are different documents), the two
// parameters that shape the feed - and the DAY, which the other cached surfaces
// have no need of: this body is the only one on the server whose content moves
// with the clock. The window is a rolling cutoff and a preorder becomes a
// release on a date, so a validator naming only the artifact would let a
// conditional GET 304-renew a stale feed for every hour between data releases -
// which is exactly the news the feed exists to carry.
func watchFeedETag(snap *snapshot, siteURL, path, rawSeries, rawWindow string, now time.Time) string {
	day := dayStart(now).Format(time.DateOnly)
	return `W/"` + identity(path, rawSeries, rawWindow, day, snap.version()+"/"+siteURL) + `"`
}

func (s *snapshot) watchFeed(
	requested []string,
	window int,
	now time.Time,
	siteURL string,
	selfURL string,
	feedID string,
) (watchFeed, error) {
	var items []watchFeedItem
	var names, unknown []string
	seenRequest := map[string]bool{}
	seenSeries := map[string]bool{}
	for _, requestedSlug := range requested {
		if seenRequest[requestedSlug] {
			continue
		}
		seenRequest[requestedSlug] = true

		detail, err := s.series(requestedSlug, 0, 0)
		if err != nil {
			return watchFeed{}, err
		}
		if detail == nil {
			resolved, err := s.redirectTarget(model.RedirectSeries, requestedSlug)
			if err != nil {
				return watchFeed{}, err
			}
			if resolved != "" {
				detail, err = s.series(resolved, 0, 0)
				if err != nil {
					return watchFeed{}, err
				}
			}
		}
		if detail == nil {
			unknown = append(unknown, requestedSlug)
			continue
		}
		if seenSeries[detail.ID] {
			continue
		}
		seenSeries[detail.ID] = true
		names = append(names, detail.Name)
		for _, entry := range detail.Works {
			if item, ok := watchItem(detail.Name, entry, window, now, siteURL); ok {
				items = append(items, item)
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].updated.Equal(items[j].updated) {
			return items[i].updated.After(items[j].updated)
		}
		if items[i].series != items[j].series {
			return items[i].series < items[j].series
		}
		pi, pj := positionStart(items[i].position), positionStart(items[j].position)
		if pi != pj {
			return pi < pj
		}
		if items[i].position != items[j].position {
			return items[i].position < items[j].position
		}
		return items[i].id < items[j].id
	})
	if len(items) > maxWatchFeedItems {
		items = items[:maxWatchFeedItems]
	}

	// An empty feed has no item to date itself by, so it uses the DAY rather
	// than the instant: the ETag is day-granular, and a body that moved with
	// the second would make two responses under one validator differ - which is
	// the one thing an ETag promises cannot happen.
	updated := dayStart(now)
	if len(items) > 0 {
		updated = items[0].updated
	}
	subtitle := strings.Join(names, ", ")
	if len(unknown) > 0 {
		noun := "slugs"
		if len(unknown) == 1 {
			noun = "slug"
		}
		missing := fmt.Sprintf("%d %s not found: %s", len(unknown), noun, strings.Join(unknown, ", "))
		if subtitle == "" {
			subtitle = missing
		} else {
			subtitle += ". " + missing
		}
	}
	return watchFeed{
		title:    watchFeedTitle,
		subtitle: subtitle,
		id:       feedID,
		self:     selfURL,
		updated:  updated,
		items:    items,
	}, nil
}

// watchItem turns one series entry into a feed item, or reports that it is not
// news. Three kinds of item, each settling its state, its id suffix and its
// summary together rather than leaving them to be re-derived further down:
//
//   - a release still AHEAD of today is a preorder, whatever the window says -
//     an announced date is the news, and it may be a year out;
//   - a release BEHIND today is news while it is inside the window;
//   - a work stating no USABLE date - none at all, or one the calendar rejects
//     (`date_flex` is a pattern, so `2026-13` and `2026-02-30` are schema-valid
//     and land in the artifact) - is news while its added_at is inside the
//     window: the catalogue learning of it is the only event there is.
//
// That last case falls THROUGH rather than dropping the work, because a value
// the calendar rejects is a data-quality problem and deleting the book from a
// reader's feed over it is the one outcome nobody wants. The feed then says
// "date unknown" where the site still renders the stated string; the two agree
// about every value the catalogue is supposed to hold, and the remedy for one
// it is not is to repair the record.
//
// The id suffix is "preorder" or "released" and NOT the state's wording, so a
// preorder becoming a release is a NEW item in the reader's feed - the one
// transition worth telling them about twice - while a date arriving on a work
// already listed as undated is not.
func watchItem(seriesName string, entry seriesEntry, window int, now time.Time, siteURL string) (watchFeedItem, bool) {
	card := entry.Work
	if card == nil {
		return watchFeedItem{}, false
	}
	var state, stateID, summary string
	var updated time.Time
	var release time.Time
	var dated bool
	if card.ReleaseDate != "" {
		release, dated = parseReleaseDate(card.ReleaseDate)
	}
	switch {
	case dated:
		updated = release
		switch {
		case releaseIsFuture(card.ReleaseDate, now):
			state, stateID = "preorder", "preorder"
			summary = "Preorder - due " + formatReleaseDate(card.ReleaseDate, release)
		case release.Before(dayStart(now).AddDate(0, 0, -window)):
			return watchFeedItem{}, false
		default:
			state, stateID = "released", "released"
			summary = "Released " + formatReleaseDate(card.ReleaseDate, release)
		}
	case card.AddedAt != nil:
		added, ok := parseAddedAt(*card.AddedAt)
		// A bare `YYYY-MM-DD` added_at states no time of day, so it is measured
		// against the start of the cutoff DAY; a full timestamp is measured
		// against the same instant of day it carries.
		cutoff := now.Add(-time.Duration(window) * 24 * time.Hour)
		if len(*card.AddedAt) == len(time.DateOnly) {
			cutoff = dayStart(now).AddDate(0, 0, -window)
		}
		if !ok || added.Before(cutoff) {
			return watchFeedItem{}, false
		}
		state, stateID = "date unknown", "released"
		summary = "Added to the catalogue, release date unknown"
		updated = added
	default:
		return watchFeedItem{}, false
	}

	title := seriesName
	if entry.Position != "" {
		title += " #" + entry.Position
	}
	title += ": " + card.Title
	authors := make([]string, len(card.Authors))
	for i, author := range card.Authors {
		authors[i] = author.Name
	}
	return watchFeedItem{
		id:    "tag:meta.audiosilo.app,2026:work/" + card.ID + "/" + stateID,
		title: title,
		state: state,
		// Config.SiteURL is stripped of its trailing slash once, in New, and
		// workPath is the one spelling of the work prefix - as on every other
		// absolute work URL this package builds.
		link:     siteURL + workPath + url.PathEscape(card.ID),
		authors:  authors,
		summary:  summary,
		updated:  updated.UTC(),
		series:   seriesName,
		position: entry.Position,
	}, true
}

// The three release-date helpers below are a HAND-MIRRORED TWIN of
// site/src/lib/dates.ts (`RELEASE_DATE`, `isFutureRelease`, `formatReleaseDate`)
// - the site renders the same three catalogue values (`YYYY`, `YYYY-MM`,
// `YYYY-MM-DD`, schema/recording.schema.json) on the work and watching pages,
// and a reader comparing their /watching list against this feed must not be
// told two different things about one book. The cases each side pins are the
// same cases: see TestReleaseDatePrecisionMirrorsTheSite here and
// site/src/lib/dates.test.ts there. Change one side, change both.
//
// parseReleaseDate reads a stated value as the FIRST instant it can name - the
// one thing the string form cannot do, and what Atom's <updated> and the feed's
// ordering need. Every judgement about the value (is it ahead of today, how
// does it read) is made on the STRING, at the precision it states, exactly as
// the site does.
func parseReleaseDate(value string) (time.Time, bool) {
	var layout string
	switch len(value) {
	case 4:
		layout = "2006"
	case 7:
		layout = "2006-01"
	case 10:
		layout = time.DateOnly
	default:
		return time.Time{}, false
	}
	t, err := time.Parse(layout, value)
	return t.UTC(), err == nil
}

// releaseIsFuture reports whether a stated release date is still ahead of now,
// compared at the value's OWN precision: a bare `2026` against this year,
// `2026-10` against this month. Strictly after, so a book released today is
// released. Twin of dates.ts isFutureRelease - widening `2026` to 1 January
// would call every book published earlier this year a preorder.
func releaseIsFuture(value string, now time.Time) bool {
	today := dayStart(now).Format(time.DateOnly)
	if len(value) > len(today) {
		return false
	}
	return value > today[:len(value)]
}

// formatReleaseDate renders a stated value as a person reads it, at the
// precision the data states: "20 Oct 2026", "Oct 2026", "2026". Twin of
// dates.ts formatReleaseDate. `t` is the value as parseReleaseDate read it, so
// the two can never disagree about which month a value names.
func formatReleaseDate(value string, t time.Time) string {
	switch len(value) {
	case 4:
		return t.Format("2006")
	case 7:
		return t.Format("Jan 2006")
	default:
		return strconv.Itoa(t.Day()) + " " + t.Format("Jan 2006")
	}
}

func parseAddedAt(value string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), true
	}
	t, err := time.Parse(time.DateOnly, value)
	return t.UTC(), err == nil
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

type atomFeed struct {
	XMLName  xml.Name    `xml:"feed"`
	XMLNS    string      `xml:"xmlns,attr"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomEntry struct {
	ID       string       `xml:"id"`
	Title    string       `xml:"title"`
	Updated  string       `xml:"updated"`
	Link     atomLink     `xml:"link"`
	Authors  []atomAuthor `xml:"author"`
	Category atomCategory `xml:"category"`
	Summary  atomText     `xml:"summary"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

type atomText struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

func renderAtomFeed(feed watchFeed) ([]byte, error) {
	doc := atomFeed{
		XMLNS:    "http://www.w3.org/2005/Atom",
		Title:    feed.title,
		Subtitle: feed.subtitle,
		ID:       feed.id,
		Updated:  feed.updated.UTC().Format(time.RFC3339),
		Links:    []atomLink{{Rel: "self", Type: "application/atom+xml", Href: feed.self}},
	}
	for _, item := range feed.items {
		authors := make([]atomAuthor, len(item.authors))
		for i, name := range item.authors {
			authors[i] = atomAuthor{Name: name}
		}
		doc.Entries = append(doc.Entries, atomEntry{
			ID:       item.id,
			Title:    item.title,
			Updated:  item.updated.UTC().Format(time.RFC3339),
			Link:     atomLink{Rel: "alternate", Href: item.link},
			Authors:  authors,
			Category: atomCategory{Term: item.state},
			Summary:  atomText{Type: "text", Text: item.summary},
		})
	}
	// The sitemaps' renderer: same declaration, same indent, same trailing
	// newline, and deterministic for the same reason.
	return renderXML(doc)
}

type jsonFeed struct {
	Version     string         `json:"version"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	FeedURL     string         `json:"feed_url"`
	Items       []jsonFeedItem `json:"items"`
}

type jsonFeedItem struct {
	ID            string           `json:"id"`
	URL           string           `json:"url"`
	Title         string           `json:"title"`
	ContentText   string           `json:"content_text"`
	DatePublished string           `json:"date_published"`
	DateModified  string           `json:"date_modified"`
	Authors       []jsonFeedAuthor `json:"authors,omitempty"`
	Tags          []string         `json:"tags"`
}

type jsonFeedAuthor struct {
	Name string `json:"name"`
}

func renderJSONFeed(feed watchFeed) ([]byte, error) {
	doc := jsonFeed{
		Version:     "https://jsonfeed.org/version/1.1",
		Title:       feed.title,
		Description: feed.subtitle,
		FeedURL:     feed.self,
		Items:       []jsonFeedItem{},
	}
	for _, item := range feed.items {
		authors := make([]jsonFeedAuthor, len(item.authors))
		for i, name := range item.authors {
			authors[i] = jsonFeedAuthor{Name: name}
		}
		updated := item.updated.UTC().Format(time.RFC3339)
		doc.Items = append(doc.Items, jsonFeedItem{
			ID:            item.id,
			URL:           item.link,
			Title:         item.title,
			ContentText:   item.summary,
			DatePublished: updated,
			DateModified:  updated,
			Authors:       authors,
			Tags:          []string{item.state},
		})
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}
