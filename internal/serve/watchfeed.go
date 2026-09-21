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
	etag := watchFeedETag(snap, s.cfg.SiteURL, r.URL.Path, rawSeries, rawWindow)
	if matchesETag(r.Header.Get("If-None-Match"), etag) || anyValidator(r.Header.Get("If-None-Match")) {
		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Cache-Control", watchFeedMaxAge)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	now := s.cfg.now().UTC()
	feed, err := snap.watchFeed(series, window, now, s.cfg.SiteURL, s.watchFeedSelfURL(r), rawSeries, rawWindow)
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

func (s *Server) watchFeedSelfURL(r *http.Request) string {
	return s.cfg.SiteURL + r.URL.RequestURI()
}

func watchFeedETag(snap *snapshot, siteURL, path, rawSeries, rawWindow string) string {
	version := snap.tag
	if version == "" {
		version = snap.stats.BuiltAt
	}
	identity := pageIdentity(path, rawSeries, rawWindow, version+"/"+siteURL)
	return `W/"` + identity + `"`
}

func (s *snapshot) watchFeed(
	requested []string,
	window int,
	now time.Time,
	siteURL string,
	selfURL string,
	rawSeries string,
	rawWindow string,
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

	updated := now
	if len(items) > 0 {
		updated = items[0].updated
	}
	subtitle := strings.Join(names, ", ")
	if len(unknown) > 0 {
		missing := fmt.Sprintf("%d slugs not found: %s", len(unknown), strings.Join(unknown, ", "))
		if subtitle == "" {
			subtitle = missing
		} else {
			subtitle += ". " + missing
		}
	}
	feedID := "tag:meta.audiosilo.app,2026:watch/" + pageIdentity("watch", rawSeries, rawWindow, "")
	return watchFeed{
		title:    watchFeedTitle,
		subtitle: subtitle,
		id:       feedID,
		self:     selfURL,
		updated:  updated,
		items:    items,
	}, nil
}

func watchItem(seriesName string, entry seriesEntry, window int, now time.Time, siteURL string) (watchFeedItem, bool) {
	card := entry.Work
	if card == nil {
		return watchFeedItem{}, false
	}
	state := "date unknown"
	stateID := "released"
	var updated time.Time
	if card.ReleaseDate != "" {
		release, ok := parseReleaseDate(card.ReleaseDate)
		if !ok {
			return watchFeedItem{}, false
		}
		updated = release
		if release.After(now) {
			state = "preorder"
			stateID = "preorder"
		} else {
			cutoff := dayStart(now).AddDate(0, 0, -window)
			if release.Before(cutoff) {
				return watchFeedItem{}, false
			}
			state = "released"
		}
	} else {
		if card.AddedAt == nil {
			return watchFeedItem{}, false
		}
		added, ok := parseAddedAt(*card.AddedAt)
		cutoff := now.Add(-time.Duration(window) * 24 * time.Hour)
		if len(*card.AddedAt) == len(time.DateOnly) {
			cutoff = dayStart(now).AddDate(0, 0, -window)
		}
		if !ok || added.Before(cutoff) {
			return watchFeedItem{}, false
		}
		updated = added
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
	summary := "Added to the catalogue, release date unknown"
	if card.ReleaseDate != "" {
		formatted := formatFeedDate(updated)
		if state == "preorder" {
			summary = "Preorder - due " + formatted
		} else {
			summary = "Released " + formatted
		}
	}
	return watchFeedItem{
		id:       "tag:meta.audiosilo.app,2026:work/" + card.ID + "/" + stateID,
		title:    title,
		state:    state,
		link:     strings.TrimRight(siteURL, "/") + "/works/" + url.PathEscape(card.ID),
		authors:  authors,
		summary:  summary,
		updated:  updated.UTC(),
		series:   seriesName,
		position: entry.Position,
	}, true
}

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

func formatFeedDate(t time.Time) string {
	return strconv.Itoa(t.Day()) + " " + t.Format("Jan 2006")
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
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
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
