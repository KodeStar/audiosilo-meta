package serve

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

const cataloguePageSize = 50
const chapterPageSize = 100

// readPage bounds the multiplication used for every HTML window. Repeated and
// empty parameters are invalid; a valid alternate spelling gets a canonical
// redirect after existence has been established.
func readPage(r *http.Request) (int, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, false
	}
	values, ok := query["page"]
	if !ok {
		return 1, true
	}
	if len(values) != 1 || values[0] == "" {
		return 0, false
	}
	n, err := strconv.Atoi(values[0])
	return n, err == nil && n > 0 && n <= 1000000
}

func pageQuery(page int) string {
	if page <= 1 {
		return ""
	}
	return "page=" + strconv.Itoa(page)
}
func querySuffix(page int) string {
	if query := pageQuery(page); query != "" {
		return "?" + query
	}
	return ""
}
func pageInRange(page, total, size int) bool { return page == 1 || (page-1)*size < total }

func writeRedirectPageLocation(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.Header().Set("Cache-Control", query.RedirectMaxAge)
	writeRedirectPage(w, location, "")
}

type paginationView struct {
	Previous, Next string
	Page, Pages    int
}

func newPagination(path string, page, total, size int) paginationView {
	v := paginationView{Page: page, Pages: max(1, (total+size-1)/size)}
	if page > 1 {
		v.Previous = path + querySuffix(page-1)
	}
	if page < v.Pages {
		v.Next = path + querySuffix(page+1)
	}
	return v
}

type directoryFamily struct {
	Slug, Title, Description string
	Family                   query.PageFamily
	Prefix, Suffix           string
}

var directoryFamilies = []directoryFamily{
	{"works", "Audiobooks", "Browse the works in the catalogue and compare their recordings.", query.WorkPages, model.WorksPath, ""},
	{"series", "Series", "Find series order and the recordings catalogued for each work.", query.SeriesPages, model.SeriesPath, ""},
	{"people", "Authors and narrators", "Explore writing and recording credits.", query.PersonPages, model.PeoplePath, ""},
	{"recaps", "Recap guides", "Catch up with existing community-written story summaries.", query.RecapPages, model.WorksPath, recapSuffix},
	{"characters", "Character guides", "Explore existing community-written character guides with spoilers closed.", query.CharacterPages, model.WorksPath, charactersSuffix},
}

func (s *Server) browseRoutes() []route {
	rs := []route{{"GET /browse", s.html(s.browseHandler(nil))}}
	for i := range directoryFamilies {
		family := directoryFamilies[i]
		rs = append(rs, route{"GET /browse/" + family.Slug, s.html(s.browseHandler(&family))})
	}
	return rs
}

type directoryLink struct{ URL, Title, Detail string }
type browseView struct {
	Title, Description, Caption string
	CurrentURL                  string
	Directories                 []directoryLink
	Entries                     []directoryLink
	Pagination                  paginationView
	Index                       bool
}

func (s *Server) browseHandler(family *directoryFamily) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sh := s.shells.get("browse/index.html")
		if sh == nil || !sh.inject {
			s.pageError(w, http.StatusInternalServerError, "This page is unavailable because its site template could not be loaded.")
			return
		}
		snap := s.current()
		if snap == nil {
			s.pageUnavailable(w)
			return
		}
		page, valid := readPage(r)
		if !valid {
			s.pageError(w, http.StatusBadRequest, "Invalid page number.")
			return
		}
		path := "/browse"
		view := browseView{Title: "Browse the audiobook catalogue", Description: "Find audiobooks, series, authors, narrators and community guides through the catalogue directories.", Index: family == nil}
		for _, f := range directoryFamilies {
			view.Directories = append(view.Directories, directoryLink{URL: path + "/" + f.Slug, Title: f.Title, Detail: f.Description})
		}
		if family == nil {
			if page != 1 {
				s.site.notFound(w, r)
				return
			}
			view.Entries = view.Directories
		} else {
			path += "/" + family.Slug
			view.CurrentURL = path
			result, err := snap.Browse(r.Context(), family.Family, cataloguePageSize, (page-1)*cataloguePageSize)
			if err != nil {
				s.log.Printf("serve: rendering %q failed: %v", r.URL.Path, err)
				s.pageUnavailable(w)
				return
			}
			if result == nil || !pageInRange(page, result.Total, cataloguePageSize) {
				s.site.notFound(w, r)
				return
			}
			view.Title, view.Description = family.Title, family.Description
			view.Caption = countCaption(len(result.Entries), result.Total, "entry", "entries")
			view.Pagination = newPagination(path, page, result.Total, cataloguePageSize)
			for _, entry := range result.Entries {
				view.Entries = append(view.Entries, directoryLink{URL: family.Prefix + url.PathEscape(entry.ID) + family.Suffix, Title: entry.Title, Detail: entry.Language})
			}
		}
		if r.URL.RawQuery != pageQuery(page) {
			writeRedirectPageLocation(w, path+querySuffix(page))
			return
		}
		sheet, err := renderTemplate("browse", view)
		if err != nil {
			s.pageUnavailable(w)
			return
		}
		canonical := s.cfg.SiteURL + path + querySuffix(page)
		doc := &entityPage{title: view.Title + " - " + siteName, description: view.Description, canonical: canonical, ogType: "website", image: ogImage(s.cfg.SiteURL, ""), jsonLD: catalogueJSONLD(view.Title, s.cfg.SiteURL, canonical, ""), factSheet: sheet}
		etag := entityETag(snap, sh, path+querySuffix(page))
		if httpx.MatchesETag(r.Header.Get("If-None-Match"), etag) {
			s.writePage(w, r, sh, nil, etag)
			return
		}
		s.writePage(w, r, sh, doc, etag)
	}
}

// recordingPath is shared by comparisons, detailed cards and recording credits.
func recordingPath(workID, recordingID string) string {
	return model.WorksPath + url.PathEscape(workID) + "#recording-" + url.PathEscape(recordingID)
}
func chaptersPath(workID, recordingID string) string {
	return model.WorksPath + url.PathEscape(workID) + "/recordings/" + url.PathEscape(recordingID) + "/chapters"
}

type chapterView struct {
	Number                 int
	Title, Start, Duration string
}
type chaptersView struct {
	Title, WorkURL, RecordingURL, RecordingID, Narrators, Caption string
	Chapters                                                      []chapterView
	Pagination                                                    paginationView
}

func composeChaptersPage(ctx context.Context, siteURL string, snap *query.DB, workID, recordingID string, page int) (*entityPage, error) {
	result, err := snap.RecordingChapters(ctx, workID, recordingID, chapterPageSize, (page-1)*chapterPageSize)
	if err != nil || result == nil {
		return nil, err
	}
	if !pageInRange(page, result.Total, chapterPageSize) {
		return nil, nil
	}
	path := chaptersPath(workID, recordingID)
	v := chaptersView{Title: result.Work.Title, WorkURL: model.WorksPath + url.PathEscape(workID), RecordingURL: recordingPath(workID, recordingID), RecordingID: recordingID, Narrators: joinNames(query.PersonNames(result.Recording.Narrators)), Caption: countCaption(len(result.Chapters), result.Total, "chapter", "chapters"), Pagination: newPagination(path, page, result.Total, chapterPageSize)}
	for i, ch := range result.Chapters {
		v.Chapters = append(v.Chapters, chapterView{Number: (page-1)*chapterPageSize + i + 1, Title: ch.Title, Start: formatChapterTime(ch.StartMS), Duration: formatChapterTime(ch.LengthMS)})
	}
	sheet, err := renderTemplate("chapters", v)
	if err != nil {
		return nil, err
	}
	canonical := siteURL + path + querySuffix(page)
	return &entityPage{title: v.Title + " chapters - " + siteName, description: "Chapter titles, start times and durations for the " + recordingID + " recording of " + v.Title + ".", canonical: canonical, ogType: "article", image: ogImage(siteURL, ""), jsonLD: catalogueJSONLD(v.Title+" chapters", siteURL, canonical, siteURL+v.WorkURL), factSheet: sheet}, nil
}

func formatChapterTime(ms int64) string {
	if ms < 0 {
		return "Unknown"
	}
	seconds := ms / 1000
	value := strconv.FormatInt(seconds/3600, 10) + ":" + twoDigits(seconds/60%60) + ":" + twoDigits(seconds%60)
	if ms%1000 != 0 {
		fraction := strconv.FormatInt(1000+ms%1000, 10)
		value += "." + fraction[1:]
	}
	return value
}
func twoDigits(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

// summaryView prints every unknown explicitly so two partial records cannot
// accidentally look like the same production.
type summaryView struct{ ID, URL, Narrators, Language, Abridgement, Runtime, Release, Publisher, Chapters string }

func newSummaryView(workID string, rec query.RecordingSummary) summaryView {
	return summaryView{ID: rec.ID, URL: recordingPath(workID, rec.ID), Narrators: known(joinNames(query.PersonNames(rec.Narrators))), Language: known(rec.Language), Abridgement: abridgementLabel(rec.Abridgement), Runtime: known(formatRuntime(rec.RuntimeMin)), Release: known(rec.ReleaseDate), Publisher: known(rec.Publisher), Chapters: chapterCountLabel(rec.ChapterCount)}
}
func known(value string) string {
	if value == "" {
		return "Unknown"
	}
	return value
}
func abridgementLabel(value string) string {
	switch value {
	case "abridged":
		return "Abridged"
	case "unabridged":
		return "Unabridged"
	default:
		return "Unknown"
	}
}
func chapterCountLabel(n int) string {
	if n == 0 {
		return "Unknown"
	}
	return strconv.Itoa(n)
}

type listeningEntryView struct {
	Work         workLink
	Position     string
	Recordings   []summaryView
	Total, Limit int
}

func listeningEntries(guide *query.SeriesListeningGuide) []listeningEntryView {
	out := make([]listeningEntryView, 0, len(guide.Entries))
	for _, entry := range guide.Entries {
		v := listeningEntryView{Work: cardLink(entry.Work), Position: entry.Position, Total: entry.RecordingsTotal, Limit: entry.RecordingsLimit}
		for _, rec := range entry.Recordings {
			v.Recordings = append(v.Recordings, newSummaryView(entry.Work.ID, rec))
		}
		out = append(out, v)
	}
	return out
}

type creditView struct {
	Work      workLink
	Recording summaryView
}
type recordingGroup struct {
	Title, SeriesURL, Language string
	Credits                    []creditView
}

func personRecordingGroups(credits *query.PersonRecordingsPage) []recordingGroup {
	var groups []recordingGroup
	byKey := map[string]int{}
	for _, row := range credits.Recordings {
		seriesID, title := "", "Standalone works"
		if len(row.Series) > 0 {
			seriesID, title = row.Series[0].ID, row.Series[0].Name
		}
		language := row.Recording.Language
		if language == "" {
			language = "Language unknown"
		}
		key := seriesID + "\x00" + language
		i, ok := byKey[key]
		if !ok {
			i = len(groups)
			byKey[key] = i
			group := recordingGroup{Title: title, Language: language}
			if seriesID != "" {
				group.SeriesURL = model.SeriesPath + url.PathEscape(seriesID)
			}
			groups = append(groups, group)
		}
		groups[i].Credits = append(groups[i].Credits, creditView{Work: cardLink(row.Work), Recording: newSummaryView(row.Work.ID, row.Recording)})
	}
	return groups
}

type continuityView struct {
	Person query.PersonRef
	Works  int
}

func seriesNarratorContinuity(guide *query.SeriesListeningGuide) []continuityView {
	people := map[string]query.PersonRef{}
	works := map[string]map[string]bool{}
	for _, entry := range guide.Entries {
		for _, rec := range entry.Recordings {
			for _, person := range rec.Narrators {
				people[person.ID] = person
				if works[person.ID] == nil {
					works[person.ID] = map[string]bool{}
				}
				works[person.ID][entry.Work.ID] = true
			}
		}
	}
	var out []continuityView
	for id, seen := range works {
		if len(seen) > 1 {
			out = append(out, continuityView{people[id], len(seen)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Works != out[j].Works {
			return out[i].Works > out[j].Works
		}
		if out[i].Person.Name != out[j].Person.Name {
			return out[i].Person.Name < out[j].Person.Name
		}
		return out[i].Person.ID < out[j].Person.ID
	})
	return out
}

const catalogueTemplates = `
{{define "pagination"}}{{if gt .Pages 1}}<nav aria-label="Pagination"><p>Page {{.Page}} of {{.Pages}}</p>{{if .Previous}}<a rel="prev" href="{{.Previous}}">Previous page</a>{{end}} {{if .Next}}<a rel="next" href="{{.Next}}">Next page</a>{{end}}</nav>{{end}}{{end}}
{{define "browse"}}<section class="container catalogue-directory" aria-labelledby="directory-title">
<header class="directory-header"><p class="directory-eyebrow">Explore the catalogue</p><h1 id="directory-title">{{.Title}}</h1><p class="directory-description">{{.Description}}</p></header>
<nav class="directory-nav" aria-label="Catalogue directories"><a href="/browse"{{if .Index}} aria-current="page"{{end}}>All directories</a>{{range .Directories}}<a href="{{.URL}}"{{if eq .URL $.CurrentURL}} aria-current="page"{{end}}>{{.Title}}</a>{{end}}</nav>
<div class="directory-results"><h2>{{if .Index}}Choose a directory{{else}}In this directory{{end}}</h2><p>{{if .Index}}5 directories{{else}}{{.Caption}}{{end}}</p></div>
{{if .Entries}}<ul class="directory-grid{{if .Index}} directory-grid-index{{end}}">{{range .Entries}}<li><a class="directory-card" href="{{.URL}}"><span><span class="directory-card-title">{{.Title}}</span>{{if .Detail}}<span class="directory-card-detail">{{.Detail}}</span>{{end}}</span><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6"/></svg></a></li>{{end}}</ul>{{else}}<p class="directory-empty">No entries are catalogued in this directory yet.</p>{{end}}
<div class="directory-pagination">{{template "pagination" .Pagination}}</div></section>{{end}}
{{define "chapters"}}<section class="container"><h1>{{.Title}} chapters</h1><p><a href="{{.WorkURL}}">About this audiobook</a> · <a href="{{.RecordingURL}}">Recording details</a></p><p>Recording {{.RecordingID}}{{if .Narrators}} · Narrated by {{.Narrators}}{{end}}</p><p>{{.Caption}}</p>{{if .Chapters}}<table><caption>Chapter titles and times</caption><thead><tr><th scope="col">Chapter</th><th scope="col">Title</th><th scope="col">Start</th><th scope="col">Duration</th></tr></thead><tbody>{{range .Chapters}}<tr><td>{{.Number}}</td><th scope="row">{{.Title}}</th><td>{{.Start}}</td><td>{{.Duration}}</td></tr>{{end}}</tbody></table>{{else}}<p>No chapter navigation is catalogued for this recording.</p>{{end}}{{template "pagination" .Pagination}}</section>{{end}}
{{define "summary"}}<a href="{{.URL}}">Recording {{.ID}}</a><dl><dt>Narrators</dt><dd>{{.Narrators}}</dd><dt>Language</dt><dd>{{.Language}}</dd><dt>Abridgement</dt><dd>{{.Abridgement}}</dd><dt>Runtime</dt><dd>{{.Runtime}}</dd><dt>Released</dt><dd>{{.Release}}</dd><dt>Publisher</dt><dd>{{.Publisher}}</dd><dt>Chapters</dt><dd>{{.Chapters}}</dd></dl>{{end}}
{{define "listening-guide"}}<section id="listening-guide"><h2>Series listening guide</h2><p>Compare the catalogued recordings in series order. Choose a recording for each work to plan your listening time. Unknown runtimes and unselected works leave the plan partial. Retailer links do not state availability.</p><h3>Narrator continuity</h3>{{if .Continuity}}<ul>{{range .Continuity}}<li><a href="/people/{{.Person.ID}}">{{.Person.Name}}</a> is credited on {{.Works}} works among the choices on this page.</li>{{end}}</ul>{{else}}<p>No recurring narrator credit among the choices on this page.</p>{{end}}{{range .ListeningEntries}}<article><h3>{{if .Position}}{{.Position}}. {{end}}<a href="/works/{{.Work.ID}}">{{.Work.Title}}</a></h3>{{if .Recordings}}{{range .Recordings}}<div>{{template "summary" .}}</div>{{end}}{{if gt .Total .Limit}}<p>Showing {{.Limit}} of {{.Total}} recordings. <a href="/works/{{.Work.ID}}">Compare every recording</a>.</p>{{end}}{{else}}<p>No recording catalogued.</p>{{end}}</article>{{else}}<p>No works have been added to this series yet.</p>{{end}}{{template "pagination" .Pagination}}</section>{{end}}
{{define "recording-credits"}}<h2>Recording credits</h2><p>{{.RecordingsCaption}} Grouped by series and language on this page. Separate recordings of one work retain separate credits.</p>{{range .RecordingGroups}}<section><h3>{{if .SeriesURL}}<a href="{{.SeriesURL}}">{{.Title}}</a>{{else}}{{.Title}}{{end}} · {{.Language}}</h3>{{range .Credits}}<article><h4><a href="/works/{{.Work.ID}}">{{.Work.Title}}</a></h4>{{template "summary" .Recording}}</article>{{end}}</section>{{else}}<p>No narration recordings are catalogued.</p>{{end}}{{template "pagination" .Pagination}}{{end}}
`

func countCaption(shown, total int, one, many string) string {
	if total > shown {
		return "Showing " + strconv.Itoa(shown) + " of " + strconv.Itoa(total) + " " + many
	}
	return plural(total, one, many)
}
