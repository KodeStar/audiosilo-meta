package serve

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The sitemap surface: the index at /sitemap-index.xml and the entity shards at
// /sitemaps/<family>-<n>.xml. It is what makes the Phase A entity pages
// DISCOVERABLE - a crawler that is never told about 280k URLs finds them at the
// rate the site links to them, which is a handful.
//
// The index route deliberately SHADOWS the Astro build's static sitemap-index.xml
// (a specific pattern beats the "/" static fallback), because that file describes
// the dozen static pages only. Its own sitemap, /sitemap-0.xml, keeps being
// served from the dist and is listed here as the first entry, so nothing the site
// build knows about is lost.
//
// Everything is rendered from the LIVE snapshot plus the configured origin: no
// artifact change, no SchemaVersion question, and no shell - so unlike the entity
// pages these routes are registered whether or not a site directory is
// configured (an API-only deployment simply has no static entry to list). The
// dist reaches ONE bit of one document, the index's static entry, which is why
// that bit rides in the index's validator and in no other.

const (
	// sitemapIndexPath is the document robots.txt points crawlers at.
	sitemapIndexPath = "/sitemap-index.xml"
	// sitemapShardPrefix is the entity shards' directory. It is a TOP-LEVEL
	// literal on purpose: a shard route under a family ("/works/sitemap-0.xml")
	// would be a new literal inside that family's id space, and the reserved-slug
	// set (pkg/model/reserved.go) would have to grow to keep a record from
	// shadowing it.
	sitemapShardPrefix = "/sitemaps/"
	// staticSitemapFile is the Astro build's own sitemap of the static pages,
	// relative to the site directory. It is listed in the index when the dist
	// carries it and omitted when it does not.
	staticSitemapFile = "sitemap-0.xml"
	// sitemapNS is the sitemap protocol's namespace, on both document kinds.
	sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"
	// sitemapMaxAge bounds how long a sitemap may be reused without revalidating.
	// The content changes exactly when the artifact does, which the ETag captures;
	// an hour only bounds how long a crawler can miss a data release, and a
	// revalidation costs a 304 rather than a re-render.
	sitemapMaxAge = "public, max-age=3600"
)

// sitemapShardURLs is the protocol's per-file URL cap (50,000). It is a var
// rather than a const for ONE reason: a test lowers it to drive a multi-shard
// partition through the mux, which 50,000 fixture records cannot. Nothing in
// production writes it, and the package's tests do not run in parallel.
var sitemapShardURLs = 50000

// sitemapFamily is one shardable family, as DATA - the ONE table the index's
// entries, the shard route's file names and the per-family query are all read
// from, so a fourth family is a row rather than four edits.
//
// The ORDER of the table is the order the index lists them in, and it is the
// crawl-budget decision the campaign rests on: series and people first (few
// files, high query value - series reading order and narrator-name queries),
// works last. A young domain gets its six-figure page count indexed slowly, so
// what the early crawl budget is spent on is the part we control.
type sitemapFamily struct {
	name   string          // the shard file's prefix, e.g. "works"
	prefix string          // the page path prefix these ids are addressed at
	count  func(Stats) int // how many records the family holds
	query  string          // id (+ lastmod) rows, ordered by id, one shard's worth
}

var sitemapFamilies = []sitemapFamily{
	{name: "series", prefix: seriesPath, count: func(st Stats) int { return st.Series }, query: seriesSitemapSQL},
	{name: "people", prefix: personPath, count: func(st Stats) int { return st.People }, query: peopleSitemapSQL},
	{name: "works", prefix: workPath, count: func(st Stats) int { return st.Works }, query: worksSitemapSQL},
}

// The per-family shard queries. Each walks its table's PRIMARY KEY index in id
// order - the same order the OFFSET pages through - so a shard is a windowed
// index walk rather than a scan (TestServeLookupsAreIndexed runs these very
// constants through EXPLAIN QUERY PLAN).
//
// Only works carries an added_at column, so the other two select a literal NULL
// for it: one row shape, one scan loop, and no per-family branch that could
// disagree with the query it belongs to. A family with no date simply emits no
// <lastmod>, which is the protocol's own "unknown" - nothing is fabricated, and
// nothing about the artifact changes to serve them.
const (
	worksSitemapSQL  = `SELECT id, added_at FROM works ORDER BY id LIMIT ? OFFSET ?`
	peopleSitemapSQL = `SELECT id, NULL FROM people ORDER BY id LIMIT ? OFFSET ?`
	seriesSitemapSQL = `SELECT id, NULL FROM series ORDER BY id LIMIT ? OFFSET ?`
)

// shardFilePattern is the strict spelling of a shard's file name. Strict on both
// counts: the family is matched against the table above, and a shard number has
// exactly ONE spelling (no leading zeros, at most six digits), because
// "works-0.xml" and "works-00.xml" would otherwise be two URLs serving one
// document - precisely the duplicate content a sitemap exists to avoid. Anything
// else is a 404, including every traversal spelling.
var shardFilePattern = regexp.MustCompile(`^([a-z]+)-(0|[1-9][0-9]{0,5})\.xml$`)

// sitemapRoutes is the sitemap surface, a THIRD route table beside routes() and
// htmlRoutes(). It is not API (no openapi.json entry - these are crawler
// documents, not a client contract) and not a page (no shell, no dist), so it is
// its own table and buildMux registers it unconditionally.
func (s *Server) sitemapRoutes() []route {
	return []route{
		{"GET " + sitemapIndexPath, s.html(s.handleSitemapIndex)},
		{"GET " + sitemapShardPrefix + "{" + sitemapFileWildcard + "}", s.html(s.handleSitemapShard)},
	}
}

// sitemapFileWildcard is what the shard route spells its file wildcard. It is
// NOT a record id - see redirectExemptRoutes, which says so out loud.
const sitemapFileWildcard = "file"

// handleSitemapIndex serves the index: the static-pages sitemap (when the dist
// carries one), then every family's shards in sitemapFamilies order.
//
// With no artifact loaded it DEGRADES to the dist's own static index rather than
// erroring - that is exactly the document this route shadows, and a crawler that
// arrives during a cold boot is better served the static pages than a 503. Only a
// deployment with neither a dist nor an artifact has nothing at all to say, and
// that is the API's own 503 (with the poll loop's real Retry-After).
func (s *Server) handleSitemapIndex(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	if snap == nil {
		if p := s.staticSitePath(strings.TrimPrefix(sitemapIndexPath, "/")); p != "" {
			// The dist's file is days or weeks old, and with no Cache-Control
			// ServeFile leaves a heuristic cache free to keep this static-pages
			// stand-in for a tenth of that age - hours after the real index came
			// live. The fallback describes THIS MOMENT, not the site, so it is
			// never stored.
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, p)
			return
		}
		s.sitemapUnavailable(w)
		return
	}
	// The static entry is settled ONCE per request: it decides both the document's
	// first entry and the validator that stands for it.
	static := s.staticSitePath(staticSitemapFile) != ""
	etag, done := s.sitemapNotModified(w, r, snap, sitemapIndexDocName(static))
	if done {
		return
	}
	writeSitemap(w, etag, s.sitemapIndex(snap, static))
}

// sitemapIndexDocName is the index's identity inside its validator. The static
// entry's PRESENCE is part of it because it is part of the body: a UI-only
// redeploy that adds or drops the dist's sitemap under an unchanged data release
// changes the document, and without this marker it would 304-renew the old one
// for the life of the release.
func sitemapIndexDocName(static bool) string {
	if static {
		return sitemapIndexPath + "/static"
	}
	return sitemapIndexPath
}

// handleSitemapShard serves one family's shard of entity URLs.
//
// EXISTENCE is settled before anything is read: the file name must parse, and the
// shard number must be inside the family's shard count - which comes from the
// stats the snapshot computed at load, so an out-of-range shard (and every URL of
// an empty family) is a 404 that costs no query. That is also why the conditional
// request is answered in full here, wildcard included, where the entity pages
// have to defer "*" until the page is composed: here the question "does this
// representation exist" is already answered.
func (s *Server) handleSitemapShard(w http.ResponseWriter, r *http.Request) {
	fam, shard, ok := parseShardFile(r.PathValue(sitemapFileWildcard))
	if !ok {
		http.NotFound(w, r)
		return
	}
	snap := s.current()
	if snap == nil {
		s.sitemapUnavailable(w)
		return
	}
	if shard >= shardCount(fam.count(snap.stats)) {
		http.NotFound(w, r)
		return
	}
	etag, done := s.sitemapNotModified(w, r, snap, sitemapShardPrefix+shardFile(fam, shard))
	if done {
		return
	}
	doc, err := s.sitemapShard(snap, fam, shard)
	if err != nil {
		// No validator and no cache policy on this path: a hot swap closing the db
		// mid-request is a 500 that must never be stored, let alone 304-renewed
		// under the document's own ETag for the life of the release.
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeSitemap(w, etag, doc)
}

// sitemapUnavailable is the no-artifact answer, the API routes' own 503 with the
// poll loop's real wait (see requireSnapshot/retryAfter). The body is JSON where
// the document would have been XML: a 503 carries no sitemap either way, and one
// error shape across the server beats a second one invented for two routes.
func (s *Server) sitemapUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", s.retryAfter())
	writeErr(w, http.StatusServiceUnavailable, noArtifactMsg)
}

// parseShardFile reads a shard file name as (family, shard number). Everything
// that is not exactly a known family and a canonical number is rejected, which is
// what makes a traversal attempt ("..%2Ffoo", "../../etc/passwd") a plain 404
// rather than something the file system ever sees: nothing here is a path.
func parseShardFile(file string) (sitemapFamily, int, bool) {
	m := shardFilePattern.FindStringSubmatch(file)
	if m == nil {
		return sitemapFamily{}, 0, false
	}
	// The pattern already bounded the number to at most six digits, so the parse
	// cannot fail and cannot overflow an int - there is no error case to handle.
	n, _ := strconv.Atoi(m[2])
	for _, fam := range sitemapFamilies {
		if fam.name == m[1] {
			return fam, n, true
		}
	}
	return sitemapFamily{}, 0, false
}

func shardFile(fam sitemapFamily, shard int) string {
	return fam.name + "-" + strconv.Itoa(shard) + ".xml"
}

// shardCount is how many files a family of n records needs: ceil(n / 50,000),
// and zero for an empty family (which therefore contributes no index entry and
// 404s at every shard).
//
// The count itself is NOT a per-request COUNT(*): it is the snapshot's stats,
// computed once at load beside the redirect gate (see loadStats), and a snapshot
// is immutable - so the shard arithmetic is memoized by construction rather than
// by a second cache that could disagree with /api/v1/stats about how big the
// catalogue is.
func shardCount(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + sitemapShardURLs - 1) / sitemapShardURLs
}

// staticSitePath returns the absolute path of a file in the configured site
// directory, or "" when there is no site directory or no such file. name is a
// package constant, never request input.
func (s *Server) staticSitePath(name string) string {
	if s.cfg.Site == "" {
		return ""
	}
	p := filepath.Join(s.cfg.Site, name)
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// ---- documents --------------------------------------------------------------

// The two protocol documents. They are structs rather than hand-written strings
// so encoding/xml owns the escaping: every slug in the artifact matches
// ^[a-z0-9-]+$, which makes an escape unreachable there, but the configured
// origin is not a slug (an "&" in it would produce a document no parser accepts)
// and "unreachable" is not a reason to hand-roll it.
type sitemapIndexDoc struct {
	XMLName  xml.Name          `xml:"sitemapindex"`
	NS       string            `xml:"xmlns,attr"`
	Sitemaps []sitemapEntryDoc `xml:"sitemap"`
}

type sitemapEntryDoc struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type urlSetDoc struct {
	XMLName xml.Name      `xml:"urlset"`
	NS      string        `xml:"xmlns,attr"`
	URLs    []urlEntryDoc `xml:"url"`
}

// urlEntryDoc carries loc and lastmod ONLY. changefreq and priority are
// deliberately absent: the major crawlers have said for years that they ignore
// both, and a per-URL guess at how often a record changes would be exactly the
// fabricated fact this project does not state.
type urlEntryDoc struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// sitemapIndex composes the index document from the live snapshot and whether
// the dist carries its own static sitemap (the caller settles that once, because
// the validator stands for it too - see sitemapIndexDocName).
//
// The static entry carries NO lastmod: that file belongs to the dist, and the
// artifact's build time is not a fact about it. Every shard entry carries the
// artifact's built_at, which IS its date - the shard is rendered from that
// artifact and from nothing else.
func (s *Server) sitemapIndex(snap *snapshot, static bool) *sitemapIndexDoc {
	doc := &sitemapIndexDoc{NS: sitemapNS}
	if static {
		doc.Sitemaps = append(doc.Sitemaps, sitemapEntryDoc{Loc: s.cfg.SiteURL + "/" + staticSitemapFile})
	}
	built := w3cDate(snap.stats.BuiltAt)
	for _, fam := range sitemapFamilies {
		for shard := range shardCount(fam.count(snap.stats)) {
			doc.Sitemaps = append(doc.Sitemaps, sitemapEntryDoc{
				Loc:     s.cfg.SiteURL + sitemapShardPrefix + shardFile(fam, shard),
				LastMod: built,
			})
		}
	}
	return doc
}

// sitemapShard composes one shard: up to sitemapShardURLs entity page URLs, in
// id order, each with the record's own added_at as its lastmod where the family
// has one.
func (s *Server) sitemapShard(snap *snapshot, fam sitemapFamily, shard int) (*urlSetDoc, error) {
	rows, err := snap.db.Query(fam.query, sitemapShardURLs, shard*sitemapShardURLs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	doc := &urlSetDoc{NS: sitemapNS}
	for rows.Next() {
		var id string
		var added *string
		if err := rows.Scan(&id, &added); err != nil {
			return nil, err
		}
		entry := urlEntryDoc{Loc: s.cfg.SiteURL + fam.prefix + id}
		if added != nil {
			entry.LastMod = w3cDate(*added)
		}
		doc.URLs = append(doc.URLs, entry)
	}
	return doc, rows.Err()
}

// w3cDate normalizes a stored date to the W3C Datetime forms the sitemap
// protocol accepts. The artifact carries added_at in the two spellings the data
// model allows (see the added_at note in CLAUDE.md): a bare YYYY-MM-DD, which is
// already a valid lastmod and is passed through, and a full RFC 3339 timestamp,
// which is normalized to UTC so one instant has one spelling in every document.
//
// Anything else returns "" and the caller omits the element. A lastmod a crawler
// cannot parse is worse than none, and inventing a date for a record that states
// no usable one would be stating a fact nobody gave us.
func w3cDate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if _, err := time.Parse(time.DateOnly, v); err == nil {
		return v
	}
	return ""
}

// ---- transport --------------------------------------------------------------

// sitemapNotModified answers a conditional request for a sitemap, returning the
// document's validator and whether the request is already finished. It is named
// for its surface because it hard-codes sitemapMaxAge - openapi.json and the
// entity pages each spell this in their own terms and with their own max-age.
//
// The validator and the cache policy are set on the 304 ONLY; the 200 path sets
// them itself once the document has been composed (writeSitemap), which is the
// entity handler's ordering. An error between here and there - a hot swap
// closing the db mid-request - must not ship an error body under an hour of
// public caching and this document's own ETag, or a shared cache would
// 304-renew that error for the life of the release.
//
// The validator is the ARTIFACT's identity, the configured origin and the
// document's own name. For a SHARD that is everything it is rendered from: the
// dist's identity deliberately does not appear (it does for an entity page - see
// pageIdentity), because not one byte of the built shell reaches a shard. The
// INDEX is the exception, and its name carries the difference - it lists the
// dist's static sitemap when there is one, so the presence bit rides in the
// document name (sitemapIndexDocName).
//
// Unlike the entity pages this honours the "*" wildcard: existence was
// established before this is called (the shard route checks the family's shard
// count first; the index always exists once an artifact is loaded), so RFC 9110
// 13.1.2's condition is already answered.
func (s *Server) sitemapNotModified(w http.ResponseWriter, r *http.Request, snap *snapshot, doc string) (string, bool) {
	etag := sitemapETag(snap, s.cfg.SiteURL, doc)
	inm := r.Header.Get("If-None-Match")
	if !matchesETag(inm, etag) && !anyValidator(inm) {
		return etag, false
	}
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", sitemapMaxAge)
	// The gzip middleware passes a bodyless status through uncompressed and
	// announces no encoding on it - see gzipResponseWriter.ensureHeader.
	w.WriteHeader(http.StatusNotModified)
	return etag, true
}

// sitemapETag builds that validator. The release tag is the artifact's identity
// when the server is polling; a local --db artifact has no tag, so the build
// timestamp stands in - entityETag's rule, because it is the same question.
//
// The ORIGIN is in it because every loc is built from it: a --site-url change
// against an unchanged data release would otherwise renew a 304 forever for a
// document naming the wrong host. Weak, and stripped of quotes and backslashes
// for the same reason entityETag is - a quote in any component would end the tag
// early.
func sitemapETag(snap *snapshot, siteURL, doc string) string {
	version := snap.tag
	if version == "" {
		version = snap.stats.BuiltAt
	}
	return `W/"` + strings.NewReplacer(`"`, "", `\`, "").Replace(version+"/"+siteURL+doc) + `"`
}

// writeSitemap renders a document and writes it under the validator the
// conditional check computed. Rendering happens into a buffer first so a marshal
// failure is still a 500 rather than a truncated document behind a 200 already
// on the wire - the reason the JSON handlers marshal before they write - and
// SUCCESS is what puts the ETag and the cache policy on the response, so no
// error path can inherit them.
func writeSitemap(w http.ResponseWriter, etag string, doc any) {
	body, err := renderSitemap(doc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", sitemapMaxAge)
	h.Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(body)
}

// renderSitemap marshals a sitemap document, XML declaration included. It is
// deterministic: struct field order is the element order, the entries are in the
// order their query returned them (ORDER BY id) and nothing here reads a clock,
// so two renders of one snapshot are byte-identical.
func renderSitemap(doc any) ([]byte, error) {
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
