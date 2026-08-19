package serve

import (
	"encoding/json"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The HTML entity pages. metaserve does not own a parallel page design: the
// Astro build stays the source of the header, footer and design system, and this
// file injects per-entity content into the BUILT shell at request time (see
// shell.go for the marker contract). What it adds is what a crawler and a no-JS
// reader need and a client-rendered island cannot give them - a real title, a
// real description, a fact sheet in the markup, and JSON-LD - plus the entity's
// own API payload embedded in the page so hydration costs no second fetch.
//
// Everything here reads the LIVE snapshot through the same methods the JSON
// handlers call and adds NO SQL of its own, so TestServeLookupsAreIndexed covers
// these routes by construction and a page's query budget is exactly the API
// route the site already hits for it.

// The path routes every internal link, canonical URL and JSON-LD url is built
// from. They are the prefixes of the three entity patterns, written once.
const (
	workPath   = "/works/"
	personPath = "/people/"
	seriesPath = "/series/"
)

// siteName is the suffix every page title carries and the og:site_name value.
const siteName = "AudioSilo Meta"

// defaultSiteURL is the public origin metaserve assumes when --site-url is not
// given. The server cannot otherwise know its own origin, and a canonical link
// or an og:url has to be absolute.
const defaultSiteURL = "https://meta.audiosilo.app"

// defaultOGImage is the site icon used when the entity has no cover of its own
// (every person and series page, and a work whose recordings carry no cover).
const defaultOGImage = "/android-chrome-512x512.png"

// entityMaxAge is how long a rendered entity page may be reused without
// revalidating. The content changes exactly when the artifact does, which the
// ETag captures, so five minutes only bounds how long a client can miss a swap
// while sparing a crawler's budget the full page on every hit.
const entityMaxAge = "public, max-age=300"

// descriptionMax is the meta description's budget. Search engines truncate
// around here, and a description cut mid-sentence by somebody else's renderer
// reads worse than one this package ended on a word.
const descriptionMax = 160

// composeFunc renders one entity page from the live snapshot, or returns
// (nil, nil) when the id names no record - which is the caller's cue to try the
// redirect table and then the site's 404.
type composeFunc func(siteURL string, snap *snapshot, id string) (*entityPage, error)

// htmlEntityRoute is one page, as DATA. Every consumer reads this one table
// rather than a list of its own: buildMux registers the pattern and its legacy
// twin, redirectNamespaces learns the id namespace from it, the redirect writer
// learns which patterns answer in HTML from it, and loadShells learns which
// built shells to split. A fourth family - or a further page hanging off an
// existing one - is one row.
type htmlEntityRoute struct {
	pattern string // the ServeMux pattern of the path route
	// legacy is the ServeMux pattern of the ?id= route this page replaced, and is
	// EMPTY for a page that never had one (the guide pages, which are new URLs
	// rather than a second spelling of an old one). htmlRoutes registers nothing
	// for an empty legacy - a redirect from a URL that was never served would be
	// a route inventing its own history.
	legacy    string
	shell     string             // the built shell to inject into, relative to the site dir
	prefix    string             // the family's path prefix, e.g. "/works/"
	namespace model.RedirectKind // which tombstone namespace a retired id resolves in
	compose   composeFunc
}

var htmlEntityRoutes = []htmlEntityRoute{
	{pattern: "GET /works/{id}", legacy: "GET /work", shell: "work/index.html", prefix: workPath, namespace: model.RedirectWorks, compose: composeWorkPage},
	{pattern: "GET /people/{id}", legacy: "GET /person", shell: "person/index.html", prefix: personPath, namespace: model.RedirectPeople, compose: composePersonPage},
	{pattern: "GET /series/{id}", legacy: "GET /series", shell: "series/index.html", prefix: seriesPath, namespace: model.RedirectSeries, compose: composeSeriesPage},
	// The community guide pages (see guides.go). They address a WORK by the same
	// slug the work page does - hence the same namespace, which is what makes a
	// retired slug 301 here too - and their literal FOLLOWS the wildcard, so they
	// shadow no record and the reserved-slug set does not grow.
	{pattern: "GET /works/{id}" + recapSuffix, shell: "recap/index.html", prefix: workPath, namespace: model.RedirectWorks, compose: composeRecapPage},
	{pattern: "GET /works/{id}" + charactersSuffix, shell: "characters/index.html", prefix: workPath, namespace: model.RedirectWorks, compose: composeCharactersPage},
}

// htmlEntityRouteByPattern is the table above indexed by ServeMux pattern - the
// ONE derivation every pattern-keyed consumer reads. Membership answers "is this
// a page route", which is what decides that a retired slug's 301 carries the
// page body rather than the API's JSON envelope; the row answers which namespace
// that slug resolves in (redirectNamespaces folds it in). Both questions are the
// same knowledge, so they are one index built by one iteration, and a new page
// route is a row that answers both.
var htmlEntityRouteByPattern = func() map[string]htmlEntityRoute {
	out := make(map[string]htmlEntityRoute, len(htmlEntityRoutes))
	for _, e := range htmlEntityRoutes {
		out[e.pattern] = e
	}
	return out
}()

// writeRedirectPage is the entity pages' 301 body: the smallest valid HTML
// document that says where the record went and links there. A page's client is a
// browser or a crawler, and both are better served by a followable link than by
// the API's {"redirect": ...} envelope if they ignore the Location header.
func writeRedirectPage(w http.ResponseWriter, location string) {
	escaped := html.EscapeString(location)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusMovedPermanently)
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Moved permanently</title>`+
		`<p>This record has moved to <a href="`+escaped+`">`+escaped+"</a>.</p>\n")
}

// htmlRoutes is the PAGE surface, in registration order: every page in the table
// above, then the legacy query-param routes the three entity pages replaced. It
// is deliberately a second table beside routes() rather than more rows in it -
// openapi.json describes the API, and these are pages, so
// TestOpenAPICoversEveryRoute must not see them
// (TestHTMLRoutesAreDisjointFromTheAPI pins the separation).
//
// buildMux registers it AFTER the API table and BEFORE the "/" static fallback,
// and only when a site directory is configured: with no dist there is no shell
// to inject into, and an API-only deployment serves no pages at all.
func (s *Server) htmlRoutes() []route {
	rs := make([]route, 0, 2*len(htmlEntityRoutes))
	for _, e := range htmlEntityRoutes {
		rs = append(rs, route{e.pattern, s.html(s.entityHandler(e))})
	}
	for _, e := range htmlEntityRoutes {
		if e.legacy == "" {
			continue // a page with no query-param predecessor - see htmlEntityRoute.legacy
		}
		rs = append(rs, route{e.legacy, s.html(s.legacyHandler(e))})
	}
	return rs
}

// html is the middleware stack a page wears: gzip, and deliberately NOT CORS.
// A page is navigated to, not fetched cross-origin by a script; the API keeps
// the open headers, and the pages do not widen that surface.
func (s *Server) html(h http.HandlerFunc) http.Handler { return gzipMW(h) }

// entityHandler serves one family's page. Every failure mode DEGRADES rather
// than erroring - no artifact loaded yet, a dist whose shell carries no markers,
// or a read that failed all serve the shell exactly as it was built, and a shell
// that is not in the dist at all falls through to the static site handler as the
// path did before these routes existed. The page is still a page (the island
// fetches its own data and reports its own error), and answering 503 or 500 to a
// crawler for a condition that resolves itself in seconds costs the URL its
// ranking.
//
// A reserved slug (search/latest) needs no special case here: they are refused
// as record ids across the whole project (pkg/model/reserved.go), so no record
// can hold one and they 404 like any other unknown id.
func (s *Server) entityHandler(e htmlEntityRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sh := s.shells.get(e.shell)
		if sh == nil {
			s.site.ServeHTTP(w, r)
			return
		}
		snap := s.current()
		if !sh.inject || snap == nil {
			sh.serveRaw(w)
			return
		}
		id := r.PathValue(idWildcard)

		// The validator depends on the snapshot's identity, the page's identity and
		// the id only, so it is computed BEFORE the page and a match costs ZERO
		// snapshot queries - which is the request shape a crawler's traffic settles
		// into once a page is indexed, against a compose that is a 6+4N-query
		// cascade plus two JSON marshals and a template execute.
		//
		// The match needs no existence check to be honest. The ETag embeds the
		// snapshot's release tag (or its build timestamp) and a snapshot is
		// immutable, so the only client that can hold a matching validator is one
		// we served a 200 from THIS snapshot - which is proof the id resolved under
		// it. That is also why the 301 below needs no exemption from this ordering:
		// a retired slug never 200s under the current snapshot, so no honest
		// matching validator for one can exist and every honest request for a
		// retired slug reaches the redirect.
		// A FABRICATED match gets a bodyless 304 for a page we never served, which
		// deceives only its fabricator; the alternative spends the whole compose on
		// every honest revalidation, which is the traffic this route exists for.
		//
		// "*" is the one validator that cannot take this path. It names no
		// representation, so it carries no proof of anything, and RFC 9110 13.1.2
		// makes it match only when a current representation EXISTS - which here is
		// exactly the question the compose answers. So a wildcard skips the fast
		// path, runs the whole cascade, and is answered 304 only where a page was
		// composed; an unknown slug still 404s and a retired one still 301s.
		inm := r.Header.Get("If-None-Match")
		etag := entityETag(snap, sh, id)
		wildcard := anyValidator(inm)
		if !wildcard && matchesETag(inm, etag) {
			h := w.Header()
			h.Set("ETag", etag)
			h.Set("Cache-Control", entityMaxAge)
			// The gzip middleware passes a bodyless status through uncompressed and
			// announces no encoding on it - see gzipResponseWriter.ensureHeader.
			w.WriteHeader(http.StatusNotModified)
			return
		}

		page, err := e.compose(s.cfg.SiteURL, snap, id)
		if err != nil {
			// The REQUEST's path, not the family prefix plus the id: several pages
			// now hang off one prefix (see the guide routes), so a notice built from
			// the prefix would name the work page for a failure on its recap.
			snap.logf("serve: rendering %s failed, serving the shell untouched: %v", r.URL.Path, err)
			sh.serveRaw(w)
			return
		}
		if page == nil {
			if redirected(w, r, snap) {
				return
			}
			s.site.notFound(w, r)
			return
		}

		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Cache-Control", entityMaxAge)
		if wildcard {
			// The representation exists, which is all "*" asked. The composed page
			// is dropped rather than written - the cost of composing it is what the
			// wildcard buys by naming nothing - and the response carries exactly the
			// headers the fast path's 304 carries.
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, sh.render(page.renderHead(), page.renderBody()))
	}
}

// legacyHandler answers the old query-param URL: /work?id=X becomes a 301 to
// /works/X, with the rest of the query preserved and id dropped. It reads NO
// snapshot - the mapping is between two spellings of a URL, not a claim that the
// record exists - so a retired slug simply takes a second hop at the path route,
// and an unknown one gets that route's 404.
//
// Without an id it is not a legacy link at all but the family's landing page, so
// it falls through to the static shell exactly as before these routes existed.
func (s *Server) legacyHandler(e htmlEntityRoute) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		id := strings.TrimSpace(q.Get("id"))
		if id == "" {
			s.site.ServeHTTP(w, r)
			return
		}
		q.Del("id")
		// url.Values.Encode sorts by key, so one request always produces one
		// Location - the pages are deterministic and so is this.
		location := e.prefix + url.PathEscape(id)
		if rest := q.Encode(); rest != "" {
			location += "?" + rest
		}
		http.Redirect(w, r, location, http.StatusMovedPermanently)
	}
}

// entityETag is the page's validator, in three parts: the artifact's identity,
// the PAGE's identity, and the slug. The release tag is the artifact's identity
// when the server is polling; a local --db artifact has no tag, so the build
// timestamp stands in. The page's identity is the shell, the origin and the
// composer (see pageIdentity) - the body is shell plus composed markup, so a
// validator over the data alone would keep a client on a page whose dist has
// been replaced under it.
//
// Weak, because the comparison is about "is this the same content", not about
// byte equality of two transfer encodings. A quote in any part would end the tag
// early, so all of them are stripped of them - every id in the artifact is a
// slug and the other two parts are a tag and a hex digest, which makes that
// unreachable rather than merely unlikely.
func entityETag(snap *snapshot, sh *shell, id string) string {
	version := snap.tag
	if version == "" {
		version = snap.stats.BuiltAt
	}
	return `W/"` + strings.NewReplacer(`"`, "", `\`, "").Replace(version+"/"+sh.identity+"/"+id) + `"`
}

// ---- page composition -------------------------------------------------------

// entityPage is one rendered page's parts, before they are assembled into the
// shell. Head composition and body composition each happen ONCE, here, for all
// three families - only the facts differ.
type entityPage struct {
	title       string
	description string
	canonical   string
	ogType      string
	image       string
	jsonLD      []byte
	factSheet   string // already-escaped HTML, rendered through html/template
	payload     []byte // the entity JSON, byte-equivalent to its API route's body
}

// renderHead composes the block that replaces the shell's marked head section.
// Every interpolated value goes through html.EscapeString - nothing is formatted
// into an attribute raw - and the JSON-LD is written verbatim because
// encoding/json escapes "<" by default, which is what makes a "</script>" inside
// a title impossible rather than merely unlikely.
func (p *entityPage) renderHead() string {
	var b strings.Builder
	b.WriteString("\n<title>" + html.EscapeString(p.title) + "</title>\n")
	b.WriteString(metaTag("name", "description", p.description))
	b.WriteString(`<link rel="canonical" href="` + html.EscapeString(p.canonical) + "\">\n")
	b.WriteString(metaTag("property", "og:type", p.ogType))
	b.WriteString(metaTag("property", "og:site_name", siteName))
	b.WriteString(metaTag("property", "og:title", p.title))
	b.WriteString(metaTag("property", "og:description", p.description))
	b.WriteString(metaTag("property", "og:url", p.canonical))
	b.WriteString(metaTag("property", "og:image", p.image))
	b.WriteString(metaTag("name", "twitter:card", "summary_large_image"))
	b.WriteString(metaTag("name", "twitter:title", p.title))
	b.WriteString(metaTag("name", "twitter:description", p.description))
	b.WriteString(metaTag("name", "twitter:image", p.image))
	b.WriteString(`<script type="application/ld+json">`)
	b.Write(p.jsonLD)
	b.WriteString("</script>\n")
	return b.String()
}

// renderBody composes the block that replaces the shell's body marker: the fact
// sheet the island removes on mount, and the entity payload it hydrates from.
//
// The payload is the API route's own bytes, so the island needs no second fetch
// and cannot be handed a shape the API would not have returned. It is written
// raw for the same reason the JSON-LD is: encoding/json's HTML escaping is on,
// so "<" is < and the script element cannot be closed from inside.
func (p *entityPage) renderBody() string {
	var b strings.Builder
	b.WriteString(`<div id="ssr-entity">`)
	b.WriteString(p.factSheet)
	b.WriteString(`</div><script type="application/json" id="entity-data">`)
	b.Write(p.payload)
	b.WriteString(`</script>`)
	return b.String()
}

func metaTag(kind, name, content string) string {
	if content == "" {
		return ""
	}
	return `<meta ` + kind + `="` + html.EscapeString(name) + `" content="` + html.EscapeString(content) + "\">\n"
}

// ---- work -------------------------------------------------------------------

func composeWorkPage(siteURL string, snap *snapshot, id string) (*entityPage, error) {
	d, err := snap.workDetail(id)
	if err != nil || d == nil {
		return nil, err
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	canonical := siteURL + workPath + d.ID
	// The view carries the cover the og image wants and the author names both the
	// title and the description read, so each is derived once for the page.
	view := newWorkView(d)
	authors := joinNames(personNames(d.Authors))
	sheet, err := renderTemplate("work", view)
	if err != nil {
		return nil, err
	}
	return &entityPage{
		title:       workTitle(d, authors),
		description: workDescription(d, authors),
		canonical:   canonical,
		ogType:      "book",
		image:       ogImage(siteURL, view.CoverURL),
		jsonLD:      workJSONLD(d, siteURL, canonical),
		factSheet:   sheet,
		payload:     payload,
	}, nil
}

// workTitle is "<Title> by <Authors> (audiobook) - AudioSilo Meta". authors is
// the work's already-joined credit line (composeWorkPage derives it once for the
// two places that read it); the clause is dropped rather than left empty when
// the work credits nobody.
func workTitle(d *workDetail, authors string) string {
	var b strings.Builder
	b.WriteString(d.Title)
	if authors != "" {
		b.WriteString(" by " + authors)
	}
	b.WriteString(" (audiobook) - " + siteName)
	return b.String()
}

// workDescription composes the meta description from the facts the work states,
// in a FIXED order, so one snapshot always produces one description: authors,
// narrators (deduped across recordings, in credit order), the series and
// position, how many recordings there are, and a runtime.
func workDescription(d *workDetail, authors string) string {
	parts := []string{d.Title}
	if authors != "" {
		parts = append(parts, "by "+authors)
	}
	if names := personNames(dedupeNarrators(d)); len(names) > 0 {
		parts = append(parts, "narrated by "+joinNames(names))
	}
	if len(d.Series) > 0 {
		sr := d.Series[0]
		if sr.Position != "" {
			parts = append(parts, "book "+sr.Position+" of "+sr.Name)
		} else {
			parts = append(parts, "part of "+sr.Name)
		}
	}
	if n := len(d.Recordings); n > 1 {
		parts = append(parts, strconv.Itoa(n)+" recordings")
	}
	if rt := firstRuntime(d); rt != "" {
		parts = append(parts, rt)
	}
	return truncateDescription(strings.Join(parts, ", ") + ".")
}

// dedupeNarrators returns the work's narrators across all its recordings, in
// first-credited order and without repeats: two recordings sharing a narrator
// name it once.
func dedupeNarrators(d *workDetail) []personRef {
	var out []personRef
	seen := map[string]bool{}
	for _, rec := range d.Recordings {
		for _, n := range rec.Narrators {
			if seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}

// firstRuntime is the runtime of the first recording that states one. A work's
// recordings are separate productions of different lengths, so summing them
// would state a duration nothing has.
func firstRuntime(d *workDetail) string {
	for _, rec := range d.Recordings {
		if rec.RuntimeMin > 0 {
			return formatRuntime(rec.RuntimeMin)
		}
	}
	return ""
}

func firstCover(d *workDetail) string {
	for _, rec := range d.Recordings {
		if rec.CoverURL != "" {
			return rec.CoverURL
		}
	}
	return ""
}

// ---- person -----------------------------------------------------------------

func composePersonPage(siteURL string, snap *snapshot, id string) (*entityPage, error) {
	// The MAXIMUM window, not the JSON route's default. The embedded payload
	// replaces the fetch the island would otherwise make, and the site asks for
	// personPageMax outright (site/src/lib/api.ts PERSON_PAGE_MAX), so composing a
	// smaller page would hand the island fewer credits than it asked for - and
	// there is no pagination UI on the page to reach the rest with. The window
	// still bounds a corporate credit that narrates thousands of works, and
	// windowCaption says so on the fact sheet.
	d, err := snap.person(id, personPageMax, 0)
	if err != nil || d == nil {
		return nil, err
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	canonical := siteURL + personPath + d.ID
	sheet, err := renderTemplate("person", newPersonView(d))
	if err != nil {
		return nil, err
	}
	return &entityPage{
		title:       d.Name + " - audiobooks and narrations - " + siteName,
		description: personDescription(d),
		canonical:   canonical,
		ogType:      "profile",
		image:       ogImage(siteURL, ""),
		jsonLD:      personJSONLD(d, canonical),
		factSheet:   sheet,
		payload:     payload,
	}, nil
}

func personDescription(d *personDetail) string {
	var parts []string
	if d.AuthoredTotal > 0 {
		parts = append(parts, "author of "+plural(d.AuthoredTotal, "audiobook", "audiobooks"))
	}
	if d.NarratedTotal > 0 {
		parts = append(parts, "narrator of "+plural(d.NarratedTotal, "recording", "recordings"))
	}
	if len(parts) == 0 {
		return truncateDescription(d.Name + " in the open " + siteName + " audiobook database.")
	}
	return truncateDescription(d.Name + ": " + strings.Join(parts, ", ") + ", in the open " + siteName + " audiobook database.")
}

// ---- series -----------------------------------------------------------------

func composeSeriesPage(siteURL string, snap *snapshot, id string) (*entityPage, error) {
	// The SAME call the JSON route makes with no ?limit: the whole series (see
	// snapshot.series - membership is bounded by what a series is).
	d, err := snap.series(id, 0, 0)
	if err != nil || d == nil {
		return nil, err
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	canonical := siteURL + seriesPath + d.ID
	sheet, err := renderTemplate("series", newSeriesView(d))
	if err != nil {
		return nil, err
	}
	return &entityPage{
		title:       d.Name + " (audiobook series) - " + siteName,
		description: seriesDescription(d),
		canonical:   canonical,
		ogType:      "website",
		image:       ogImage(siteURL, ""),
		jsonLD:      seriesJSONLD(d, siteURL, canonical),
		factSheet:   sheet,
		payload:     payload,
	}, nil
}

func seriesDescription(d *seriesDetail) string {
	parts := []string{d.Name}
	if names := personNames(d.Authors); len(names) > 0 {
		parts = append(parts, "by "+joinNames(names))
	}
	if d.WorksTotal > 0 {
		parts = append(parts, plural(d.WorksTotal, "audiobook", "audiobooks")+" in series order")
	}
	return truncateDescription(strings.Join(parts, ", ") + ".")
}

// ---- shared value helpers ---------------------------------------------------

// ogImage prefers the entity's own cover and falls back to the site icon. Only
// an https cover is used: the pages are served over https and a social crawler
// drops (or a browser blocks) mixed content, so an http URL would leave the card
// imageless rather than merely insecure.
func ogImage(siteURL, cover string) string {
	if strings.HasPrefix(cover, "https://") {
		return cover
	}
	return siteURL + defaultOGImage
}

// joinNames renders a credit list for prose. personNames (abs.go) is the one
// projection of person refs to display names; there is no second copy of it
// here.
func joinNames(names []string) string { return strings.Join(names, ", ") }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// formatRuntime renders minutes for a human: "16 h 10 min", "45 min".
func formatRuntime(min int) string {
	if min <= 0 {
		return ""
	}
	h, m := min/60, min%60
	switch {
	case h == 0:
		return strconv.Itoa(m) + " min"
	case m == 0:
		return strconv.Itoa(h) + " h"
	default:
		return strconv.Itoa(h) + " h " + strconv.Itoa(m) + " min"
	}
}

// truncateDescription cuts a composed description to descriptionMax at a WORD
// boundary and leaves no dangling separator behind, so a truncated description
// still reads as a phrase rather than as a fragment ending in a comma. The cut
// is re-validated as UTF-8, because the byte budget can land inside a rune when
// the string holds no space to cut at.
func truncateDescription(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= descriptionMax {
		return s
	}
	cut := s[:descriptionMax]
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(strings.ToValidUTF8(cut, ""), " ,.;:-")
}

// ---- fact sheets ------------------------------------------------------------

// The server-rendered fact sheet: the crawler's and the no-JS reader's version
// of the page, removed by the island on mount. It is deliberately SIMPLER than
// the React detail view - semantic markup carrying the facts, wearing a few of
// the site's design tokens (container, text-hi, text-dim, border-edge) so it
// inherits the shell's stylesheet. Pixel-matching the React components in Go
// templates would be a drift contract nobody could keep.
//
// html/template does the escaping, and every internal link is a PATH route.
const factSheetTemplates = `
{{define "people"}}{{range $i, $p := .}}{{if $i}}, {{end}}<a href="/people/{{$p.ID}}">{{$p.Name}}</a>{{end}}{{end}}

{{define "linklist"}}<ul>
{{- range .}}
<li><a href="{{.URL}}">{{.Text}}</a></li>
{{- end}}
</ul>{{end}}

{{define "work"}}<section class="container">
{{- if .CoverURL}}
<img src="{{.CoverURL}}" alt="Cover art for {{.Title}}" width="300" height="300" class="border-edge">
{{- end}}
<h1 class="text-hi">{{.Title}}</h1>
{{- if .Subtitle}}
<p class="text-dim">{{.Subtitle}}</p>
{{- end}}
{{- if .Authors}}
<p>By {{template "people" .Authors}}</p>
{{- end}}
{{- if .FirstPublished}}
<p class="text-dim">First published {{.FirstPublished}}</p>
{{- end}}
{{- if .Series}}
<h2>Series</h2>
<ul>
{{- range .Series}}
<li><a href="/series/{{.ID}}">{{.Name}}</a>{{if .Position}} - book {{.Position}}{{end}}</li>
{{- end}}
</ul>
{{- end}}
{{- if .Guides}}
<h2>Guides</h2>
{{template "linklist" .Guides}}
{{- end}}
{{- if .Genres}}
<p class="text-dim">Genres: {{range $i, $g := .Genres}}{{if $i}}, {{end}}{{$g}}{{end}}</p>
{{- end}}
{{- if .Recordings}}
<h2>Recordings</h2>
{{- range .Recordings}}
<div class="border-edge">
<h3>{{if .Narrators}}Narrated by {{template "people" .Narrators}}{{else}}Recording {{.ID}}{{end}}</h3>
<ul>
{{- if .Runtime}}
<li>Runtime: {{.Runtime}}</li>
{{- end}}
{{- if .Abridged}}
<li>Abridged</li>
{{- end}}
{{- if .Publisher}}
<li>Publisher: {{.Publisher}}</li>
{{- end}}
{{- if .ReleaseYear}}
<li>Released: {{.ReleaseYear}}</li>
{{- end}}
{{- range .Codes}}
<li>{{.Label}}: {{.Value}}</li>
{{- end}}
{{- if .Chapters}}
<li>{{.Chapters}} chapters</li>
{{- end}}
</ul>
</div>
{{- end}}
{{- end}}
</section>{{end}}

{{define "person"}}<section class="container">
<h1 class="text-hi">{{.Name}}</h1>
{{- if .Authored}}
<h2>Audiobooks written</h2>
<p class="text-dim">{{.AuthoredCaption}}</p>
<ul>
{{- range .Authored}}
<li><a href="/works/{{.ID}}">{{.Title}}</a>{{if .Byline}} <span class="text-dim">{{.Byline}}</span>{{end}}</li>
{{- end}}
</ul>
{{- end}}
{{- if .Narrated}}
<h2>Audiobooks narrated</h2>
<p class="text-dim">{{.NarratedCaption}}</p>
<ul>
{{- range .Narrated}}
<li><a href="/works/{{.ID}}">{{.Title}}</a>{{if .Byline}} <span class="text-dim">{{.Byline}}</span>{{end}}</li>
{{- end}}
</ul>
{{- end}}
</section>{{end}}

{{define "series"}}<section class="container">
<h1 class="text-hi">{{.Name}}</h1>
{{- if .Authors}}
<p>By {{template "people" .Authors}}</p>
{{- end}}
<p class="text-dim">{{.Caption}}</p>
{{- if .Volumes}}
<ol>
{{- range .Volumes}}
<li>{{if .Position}}<span class="text-dim">{{.Position}}.</span> {{end}}<a href="/works/{{.ID}}">{{.Title}}</a></li>
{{- end}}
</ol>
{{- end}}
</section>{{end}}
`

// factSheets is the parsed template set, parsed ONCE: the templates are
// constants, so a parse failure would be a build-time mistake that this would
// surface on the first request. A panic is the honest answer to it - the process
// cannot serve pages at all - and it cannot depend on data.
//
// The guide pages' sheet (guides.go) is parsed into the SAME set rather than one
// of its own, so it can call the shared "people" define and a future shared
// fragment is available to every page automatically.
var factSheets = sync.OnceValue(func() *template.Template {
	return template.Must(template.Must(template.New("factsheets").Parse(factSheetTemplates)).Parse(guideTemplates))
})

func renderTemplate(name string, data any) (string, error) {
	var b strings.Builder
	if err := factSheets().ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// codeView is one identifier line on a recording block.
type codeView struct{ Label, Value string }

type recordingView struct {
	ID          string
	Narrators   []personRef
	Runtime     string
	Abridged    bool
	Publisher   string
	ReleaseYear string
	Codes       []codeView
	Chapters    int
}

type workView struct {
	Title          string
	Subtitle       string
	CoverURL       string
	FirstPublished string
	Authors        []personRef
	Series         []seriesRef
	Genres         []string
	Guides         []guideLink
	Recordings     []recordingView
}

func newWorkView(d *workDetail) workView {
	v := workView{
		Title: d.Title, Subtitle: d.Subtitle, CoverURL: firstCover(d),
		FirstPublished: d.FirstPublished, Authors: d.Authors, Series: d.Series, Genres: d.Genres,
		Guides: workGuideLinks(d),
	}
	for _, rec := range d.Recordings {
		rv := recordingView{
			ID: rec.ID, Narrators: rec.Narrators, Runtime: formatRuntime(rec.RuntimeMin),
			Abridged: rec.Abridged, Publisher: rec.Publisher,
			// publishedYear (abs.go) is the package's one date-to-year rule, so the
			// fact sheet and the ABS facade cannot read one date two ways: a value
			// it does not recognize as a year renders as stated rather than being
			// silently truncated to four characters.
			ReleaseYear: publishedYear(rec.ReleaseDate), Chapters: rec.ChapterCount,
		}
		for _, a := range rec.ASIN {
			rv.Codes = append(rv.Codes, codeView{Label: "ASIN (" + a.Region + ")", Value: a.ASIN})
		}
		for _, isbn := range rec.ISBN {
			rv.Codes = append(rv.Codes, codeView{Label: "ISBN", Value: isbn})
		}
		v.Recordings = append(v.Recordings, rv)
	}
	return v
}

// workLink is one work in a credit or membership list.
type workLink struct {
	ID     string
	Title  string
	Byline string
}

type personView struct {
	Name            string
	Authored        []workLink
	Narrated        []workLink
	AuthoredCaption string
	NarratedCaption string
}

func newPersonView(d *personDetail) personView {
	v := personView{
		Name:            d.Name,
		AuthoredCaption: windowCaption(len(d.Authored), d.AuthoredTotal),
		NarratedCaption: windowCaption(len(d.Narrated), d.NarratedTotal),
	}
	for _, card := range d.Authored {
		v.Authored = append(v.Authored, cardLink(card))
	}
	for _, entry := range d.Narrated {
		v.Narrated = append(v.Narrated, cardLink(entry.Work))
	}
	return v
}

// cardLink renders one work card as a link plus its byline (its authors, which
// is what tells two same-titled works apart in a long list).
func cardLink(card *workCard) workLink {
	if card == nil {
		return workLink{}
	}
	return workLink{ID: card.ID, Title: card.Title, Byline: byline(card.Authors)}
}

func byline(authors []personRef) string {
	if names := personNames(authors); len(names) > 0 {
		return "by " + joinNames(names)
	}
	return ""
}

// windowCaption states the page and, when the list is windowed, the unpaged
// total - so a crawler and a reader are both told that what they see is a slice
// rather than the whole credit list.
func windowCaption(shown, total int) string {
	if total > shown {
		return "Showing " + strconv.Itoa(shown) + " of " + strconv.Itoa(total) + " titles"
	}
	return plural(total, "title", "titles")
}

type volumeView struct {
	Position string
	ID       string
	Title    string
}

type seriesView struct {
	Name    string
	Authors []personRef
	Volumes []volumeView
	Caption string
}

func newSeriesView(d *seriesDetail) seriesView {
	v := seriesView{Name: d.Name, Authors: d.Authors, Caption: windowCaption(len(d.Works), d.WorksTotal)}
	for _, entry := range d.Works {
		if entry.Work == nil {
			continue
		}
		v.Volumes = append(v.Volumes, volumeView{Position: entry.Position, ID: entry.Work.ID, Title: entry.Work.Title})
	}
	return v
}
