package serve

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The two COMMUNITY GUIDE pages: /works/{id}/recap and /works/{id}/characters.
//
// They exist because the sidecar layer - the most expensive data in the repo -
// was invisible to a search engine. The work page renders none of it (the words
// "recap" and "characters" appear nowhere in its markup), the island renders it
// client-side inside accordions whose bodies are not in the DOM until they are
// clicked, and no URL anywhere targeted "<title> recap" or "<title> characters".
// A dedicated page per sidecar is the surface those queries can land on: a real
// title, a real H1, the text in the markup, and a sitemap entry pointing at it.
//
// Everything about them falls out of the machinery Phase A already built. They
// are two more rows in htmlEntityRoutes, so they inherit the shell injection,
// the ETag, the retired-slug 301 and the coverage guards without a special case
// anywhere. The page BODY costs no new SQL: the compose funcs call
// snapshot.workDetail, the same read GET /works/{id} makes, and embed its
// marshal as the page payload - so the island hydrates without a second fetch.
// The one query they add is the PRESENCE PROBE below, which is what keeps the
// 404 path from paying for a page that will not be served.
//
// What is deliberately NOT here: the sidecar TEXT on the work page. These pages
// own it. Duplicating it would split the crawl weight for the very queries this
// exists to win, and the work page instead links here (see the Guides section of
// the work fact sheet).

// The guide pages' trailing path literals, composed from the segments
// pkg/model/reserved.go declares - the one home for a route literal two packages
// must agree on. internal/issueform builds the pattern that reads such a URL
// back into a work slug from the same two values, so the page this server
// publishes and the URL the intake bot accepts cannot drift apart.
//
// Everything on THIS side then composes its path from these: the route pattern,
// the canonical URL, the work page's cross-links, the sitemap families and the
// sibling links.
//
// They FOLLOW the {id} wildcard, which is what keeps them out of the reserved
// slug set: a literal after the wildcard shadows no record, exactly as the
// chapters route's literal does.
const (
	recapSuffix      = "/" + model.GuideSegmentRecap
	charactersSuffix = "/" + model.GuideSegmentCharacters
)

// ccBySAURL is the deed the community layer is licensed under, and ccBySALabel
// the human name the pages print for it. Both guide pages carry the pair as a
// rel="license" link and in their JSON-LD, because the CC BY-SA layer's terms
// travel with the text wherever it is republished - see LICENSING.md. The names
// are version-neutral and the label rides the view beside the URL, so a future
// license bump edits these two lines and nothing else.
const (
	ccBySAURL   = "https://creativecommons.org/licenses/by-sa/4.0/"
	ccBySALabel = "CC BY-SA 4.0"
)

// workGuidePath composes one guide page's path. Written once so the canonical
// URL, the work page's link, the sibling link and the sitemap loc cannot spell
// it three ways.
func workGuidePath(id, suffix string) string { return workPath + id + suffix }

// hasRecapGuide and hasCharacterGuide are the ONE definition of "this work has
// a guide page". Three places ask: the compose func (which returns no page
// without it), the work fact sheet (which links a guide only when it exists) and
// the sibling cross-link. A second spelling would produce a link to a 404.
//
// A recap page is worth serving for a whole-book summary alone: a work with no
// chaptered recaps but an "in short" and an "ending" still answers "<title>
// recap" and "<title> summary". The summary counts only when it STATES one of
// its two fields - the same rule the site's hasGuide reads off storyRows, so the
// two spellings of presence cannot diverge on a summary row carrying neither
// (internal/build writes no such row today, but nothing here should rest on
// that: an all-empty summary would otherwise compose a zero-row 200 page).
func hasRecapGuide(d *workDetail) bool {
	return len(d.Recaps) > 0 ||
		(d.RecapSummary != nil && (d.RecapSummary.InShort != "" || d.RecapSummary.Ending != ""))
}
func hasCharacterGuide(d *workDetail) bool { return len(d.Characters) > 0 }

// hasChapteredRecaps is whether the sidecar carries a recap for a real chapter -
// the thing "chapter summaries" in a title, or "chapter-by-chapter" in an
// anchor, is a claim about. A chapter-0 recap ("previously, in earlier books" /
// front matter) is a recap the page serves, but it summarizes no chapter, so a
// sidecar carrying only those must not be promised as chaptered.
func hasChapteredRecaps(d *workDetail) bool {
	for _, r := range d.Recaps {
		if r.Through.Chapter > 0 {
			return true
		}
	}
	return false
}

// ---- the presence probe -----------------------------------------------------

// The guide pages' presence probes: does this work carry the sidecar the page is
// about? ONE indexed point query each (every table below is keyed on work_id -
// see internal/build's DDL), asked BEFORE the page is composed.
//
// It is what keeps the 404 path cheap. Almost every work in the catalogue
// carries no sidecar at all, so almost every request to these routes - a stale
// link, a crawler walking a guessed URL, a bot probing the shape - ends in a
// 404, and without a probe each one first paid workDetail's 6+4N-query cascade
// to learn there was nothing to render.
//
// The recap probe reuses ONE bound parameter across its two EXISTS clauses
// (`?1`), so both version variants take exactly one argument and the caller
// needs no branch of its own.
const (
	recapGuideExistsSQL = `SELECT EXISTS(SELECT 1 FROM recaps WHERE work_id=?1) ` +
		`OR EXISTS(SELECT 1 FROM recap_summaries WHERE work_id=?1)`
	// recapGuideExistsOnlySQL is that same question on an artifact that predates
	// recap_summaries (schema_version 2), where naming the second table would not
	// parse - the sitemap family's own pair of spellings, one work at a time.
	recapGuideExistsOnlySQL = `SELECT EXISTS(SELECT 1 FROM recaps WHERE work_id=?1)`
	characterGuideExistsSQL = `SELECT EXISTS(SELECT 1 FROM characters WHERE work_id=?1)`
)

// recapGuideProbeSQL picks the recap probe's spelling from the artifact's
// schema_version, in the same style (and for the same reason) as the sitemap
// family's recapSitemapSQL: a pure function of the version, so the probe and the
// listing agree about which tables a recap page can be built from.
func recapGuideProbeSQL(schemaVersion int) string {
	if schemaVersion >= summarySchemaVersion {
		return recapGuideExistsSQL
	}
	return recapGuideExistsOnlySQL
}

// hasRecapPage reports whether a recap page exists for this work id. False - with
// no query at all - below the sidecar tables' version, where the answer is "no
// page" for every work in the artifact.
//
// A work id the catalogue does not hold answers false too, since nothing can
// reference it: the probe therefore covers "no such work" as well, which is why
// it can stand in front of the workDetail cascade rather than beside it.
func (s *snapshot) hasRecapPage(workID string) (bool, error) {
	return s.sidecarExists(recapGuideProbeSQL(s.schemaVersion), workID)
}

// hasCharacterPage is its sibling for the character guide.
func (s *snapshot) hasCharacterPage(workID string) (bool, error) {
	return s.sidecarExists(characterGuideExistsSQL, workID)
}

func (s *snapshot) sidecarExists(query, workID string) (bool, error) {
	if s.schemaVersion < sidecarSchemaVersion {
		return false, nil
	}
	var exists bool
	if err := s.db.QueryRow(query, workID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// ---- composition ------------------------------------------------------------

// composeRecapPage renders /works/{id}/recap, or (nil, nil) when there is no
// page here - which covers BOTH "no such work" and "this work carries no
// recaps". Both fall into the entity handler's nil path, so a retired slug still
// 301s to the survivor's recap page and everything else is the site's 404. That
// is deliberate: a URL that renders nothing must not answer 200, or a crawler
// indexes an empty page for the query this whole feature targets.
//
// The probe settles that question first, so the expensive read only ever runs
// for a page that will be served. hasRecapGuide is then asked of the composed
// document anyway: it is the definition the LINKS and the sitemap are built
// from, and having the page's own existence rest on it means the two can never
// disagree about which works have a page - the probe is an optimization of that
// answer, not a second definition of it.
func composeRecapPage(siteURL string, snap *snapshot, id string) (*entityPage, error) {
	has, err := snap.hasRecapPage(id)
	if err != nil || !has {
		return nil, err
	}
	d, err := snap.workDetail(id)
	if err != nil || d == nil || !hasRecapGuide(d) {
		return nil, err
	}
	authors := joinNames(personNames(d.Authors))
	return guidePage(siteURL, d, recapSuffix, recapTitle(d), recapDescription(d, authors), newRecapView(d))
}

// composeCharactersPage renders /works/{id}/characters, on the same terms.
func composeCharactersPage(siteURL string, snap *snapshot, id string) (*entityPage, error) {
	has, err := snap.hasCharacterPage(id)
	if err != nil || !has {
		return nil, err
	}
	d, err := snap.workDetail(id)
	if err != nil || d == nil || !hasCharacterGuide(d) {
		return nil, err
	}
	authors := joinNames(personNames(d.Authors))
	return guidePage(siteURL, d, charactersSuffix, charactersTitle(d), charactersDescription(d, authors), newCharactersView(d))
}

// guidePage assembles everything the two pages share: the payload, the
// canonical, the og image and the JSON-LD. Only the head text and the rows
// differ between them, so only those are parameters.
//
// The payload is the WORK's detail document, byte-identical to what
// GET /api/v1/works/{id} returns - not a subset. The island that hydrates these
// pages renders the sidecar out of a work document and checks the id in it, so
// handing it anything else would make the embedded payload unusable and cost the
// page a second fetch.
func guidePage(siteURL string, d *workDetail, suffix, title, description string, view guideView) (*entityPage, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	sheet, err := renderTemplate("guide", view)
	if err != nil {
		return nil, err
	}
	canonical := siteURL + workGuidePath(d.ID, suffix)
	return &entityPage{
		title:       title,
		description: description,
		canonical:   canonical,
		// A guide page is a document ABOUT a book, not the book's own page: the
		// work page keeps og:type "book" and these are articles.
		ogType:    "article",
		image:     ogImage(siteURL, firstCover(d)),
		jsonLD:    guideJSONLD(view.Heading, siteURL, canonical, siteURL+workPath+d.ID),
		factSheet: sheet,
		payload:   payload,
	}, nil
}

// ---- head text --------------------------------------------------------------

// recapTitle leads with "<Title> recap" - the query the page exists to answer -
// works in "summary", and ends on the site name every page title ends on. The
// middle clause is chosen by the FACTS: a work whose sidecar carries only a
// whole-book summary has no chapter summaries to promise, and a title promising
// them would be the fabricated claim this project does not make.
func recapTitle(d *workDetail) string {
	clause := " - story summary"
	if hasChapteredRecaps(d) {
		clause = " - story so far and chapter summaries"
	}
	return d.Title + " recap" + clause + " - " + siteName
}

func charactersTitle(d *workDetail) string {
	return d.Title + " characters - a spoiler-safe character guide - " + siteName
}

// recapDescription composes the meta description from FACTS ONLY, in a fixed
// order, so one snapshot always produces one description: the work, its authors,
// how many story-so-far checkpoints there are and through which chapter, and
// which whole-book sections exist.
//
// Not one word of recap TEXT reaches it. A meta description is quoted verbatim
// into a search result, which is precisely where a spoiler must never appear -
// the same reason the text itself sits behind data-nosnippet on the page.
func recapDescription(d *workDetail, authors string) string {
	parts := []string{"Spoiler-safe recaps of " + d.Title}
	if authors != "" {
		parts = append(parts, "by "+authors)
	}
	if n := len(d.Recaps); n > 0 {
		p := plural(n, "story-so-far summary", "story-so-far summaries")
		// The recaps arrive ordered by through_chapter (snapshot.recapsOf), so the
		// last one names the furthest chapter the sidecar covers.
		if through := d.Recaps[n-1].Through.Chapter; through > 0 {
			p += " through chapter " + strconv.Itoa(through)
		}
		parts = append(parts, p)
	}
	if s := d.RecapSummary; s != nil {
		if s.InShort != "" {
			parts = append(parts, "a whole-book summary")
		}
		if s.Ending != "" {
			parts = append(parts, "how the story ends")
		}
	}
	// The spoiler posture is stated in the FIRST clause rather than the last:
	// this is the longest of the two descriptions, and a trailing clause is the
	// one truncateDescription cuts off a long title.
	return truncateDescription(strings.Join(parts, ", ") + ".")
}

// charactersDescription is the same discipline for the character guide: how many
// characters, that they are gated by the chapter they appear in, and nothing a
// character entry actually SAYS.
func charactersDescription(d *workDetail, authors string) string {
	parts := []string{"Character guide to " + d.Title}
	if authors != "" {
		parts = append(parts, "by "+authors)
	}
	parts = append(parts, plural(len(d.Characters), "character", "characters"))
	parts = append(parts, "each hidden behind the chapter they first appear in")
	return truncateDescription(strings.Join(parts, ", ") + ".")
}

// ---- the fact sheet ---------------------------------------------------------

// guideLink is one entry in a guide page's crawl mesh: an internal link with
// DESCRIPTIVE anchor text (the title, not "here"), which is what makes it worth
// anything to the page it points at.
type guideLink struct {
	URL  string
	Text string
}

// guideRow is one collapsible entry: a spoiler-SAFE summary line, an optional
// short badge on it, and the spoiler-bearing body.
//
// Facts and Text are separated only so the body reads as prose (the short
// statements first, the paragraph after). BOTH sit inside the data-nosnippet
// block - a character's alias is as much a spoiler as their description
// ("also known as the Dark Lord"), so the split is typographic, not a boundary
// between safe and unsafe.
type guideRow struct {
	Label string
	Badge string
	Facts []string
	Text  string
}

// guideView is one guide page's fact sheet. Both pages render the SAME template
// from it: the frame (heading, byline, spoiler notice, cross-links, licence and
// contribute footer) is the same page, and only the rows differ.
type guideView struct {
	Heading        string
	Authors        []personRef
	Notice         string
	Rows           []guideRow
	Links          []guideLink
	LicenseURL     string
	LicenseLabel   string
	ContributeURL  string
	ContributeText string
}

func newRecapView(d *workDetail) guideView {
	v := guideView{
		// The H1 mirrors the <title>'s leading phrase: one page, one heading, and
		// the query words in the element a reader and a crawler both weigh most.
		Heading: d.Title + " recap",
		Authors: d.Authors,
		Notice: "Every recap below is hidden until you open it. Each one is safe to read " +
			"once you have finished the chapter it names.",
		LicenseURL:     ccBySAURL,
		LicenseLabel:   ccBySALabel,
		ContributeURL:  contributeURL(d.ID, "recaps"),
		ContributeText: "Improve this recap",
	}
	// READING ORDER, which is not the order any one table holds: the whole-book
	// opener first, the chaptered recaps in position order, and the ending last -
	// the same order the site's storyRows composes for the island, so the two
	// renderings of one sidecar do not disagree about what comes first.
	if s := d.RecapSummary; s != nil && s.InShort != "" {
		v.Rows = append(v.Rows, guideRow{Label: "In short", Badge: "whole book", Text: s.InShort})
	}
	for _, r := range d.Recaps {
		v.Rows = append(v.Rows, guideRow{Label: recapRowLabel(r), Badge: recapScopeLabel(r.Scope), Text: r.Text})
	}
	if s := d.RecapSummary; s != nil && s.Ending != "" {
		v.Rows = append(v.Rows, guideRow{Label: "How does it end?", Badge: "ending", Text: s.Ending})
	}
	v.Links = guideLinks(d, recapSuffix)
	return v
}

func newCharactersView(d *workDetail) guideView {
	v := guideView{
		Heading: d.Title + " characters",
		Authors: d.Authors,
		Notice: "Every entry below is hidden until you open it, and each names the chapter " +
			"the character first appears in.",
		LicenseURL:     ccBySAURL,
		LicenseLabel:   ccBySALabel,
		ContributeURL:  contributeURL(d.ID, "characters"),
		ContributeText: "Improve this character guide",
	}
	// Authored order (snapshot.charactersOf reads ORDER BY ord), so the sidecar's
	// own presentation order survives to the page.
	for _, c := range d.Characters {
		row := guideRow{Label: c.Name, Badge: characterRoleLabel(c.Role), Text: c.Description}
		if len(c.Aliases) > 0 {
			row.Facts = append(row.Facts, "Also known as "+joinNames(c.Aliases))
		}
		row.Facts = append(row.Facts, revealLabel(c.Reveal.Chapter))
		v.Rows = append(v.Rows, row)
	}
	v.Links = guideLinks(d, charactersSuffix)
	return v
}

// guideLinks is the crawl mesh a guide page publishes: back to the work, across
// to the sibling guide when that page exists, and out to the first series. self
// is the page composing them, so a page never links itself.
//
// The FIRST series only, matching the card rule (snapshot.firstSeriesByWork) and
// the work page's own JSON-LD: a work in several series presents one of them,
// and presenting a different one here would say two things about the same book.
func guideLinks(d *workDetail, self string) []guideLink {
	links := []guideLink{{URL: workPath + d.ID, Text: "Full details for " + d.Title}}
	if self != recapSuffix && hasRecapGuide(d) {
		links = append(links, guideLink{URL: workGuidePath(d.ID, recapSuffix), Text: d.Title + " recap"})
	}
	if self != charactersSuffix && hasCharacterGuide(d) {
		links = append(links, guideLink{URL: workGuidePath(d.ID, charactersSuffix), Text: d.Title + " character guide"})
	}
	if len(d.Series) > 0 {
		sr := d.Series[0]
		links = append(links, guideLink{URL: seriesPath + sr.ID, Text: "More books in " + sr.Name})
	}
	return links
}

// workGuideLinks is the "Guides" section of the WORK page's fact sheet: the
// internal discovery path to the guide pages, and the one place the work page
// itself carries the words "recap" and "characters" (which is what a branded
// query - "audiosilo recap <title>" - has to land on).
//
// The full sidecar TEXT deliberately stays off the work page: the guide pages
// own it, so the crawl weight for those queries is not split between two URLs
// carrying the same paragraphs.
//
// The recap anchor is chosen by the FACTS for the same reason recapTitle's
// clause is: promising a chapter-by-chapter recap for a work that carries only a
// whole-book summary would be a claim the data does not support.
func workGuideLinks(d *workDetail) []guideLink {
	var out []guideLink
	if hasRecapGuide(d) {
		text := "Story summary of " + d.Title
		if hasChapteredRecaps(d) {
			text = "Chapter-by-chapter recap of " + d.Title
		}
		out = append(out, guideLink{URL: workGuidePath(d.ID, recapSuffix), Text: text})
	}
	if hasCharacterGuide(d) {
		out = append(out, guideLink{URL: workGuidePath(d.ID, charactersSuffix), Text: d.Title + " character guide"})
	}
	return out
}

// contributeURL is the site's own contribute deep link (site/src/lib/api.ts,
// href.build), so a reader who spots a gap lands on the form already scoped to
// this work and this sidecar kind rather than on a blank one.
func contributeURL(workID, kind string) string {
	return "/build?work=" + url.QueryEscape(workID) + "&kind=" + kind
}

// recapRowLabel is a recap's spoiler-SAFE summary line: it names the position
// the recap is safe at and nothing about what happens there.
//
// It deliberately reads differently from the island's recapLabel ("Up to chapter
// N"): this string is a <summary> element on a crawlable page, so it is also the
// phrase the page is trying to be found by, and "story so far" is what a reader
// types. The chapter-0 forms match the site's semantics exactly, because those
// two rows mean something specific (see the position model in CLAUDE.md - 0 is
// front matter or prior-book knowledge).
func recapRowLabel(r recapOut) string {
	switch ch := r.Through.Chapter; {
	case ch == 0 && r.Scope == "series":
		return "Previously, in earlier books"
	case ch == 0:
		return "Before this book"
	default:
		return "Story so far - through chapter " + strconv.Itoa(ch)
	}
}

// recapScopeLabel is the site's scopeLabel: a short tag saying how far a recap
// reaches. An absent scope states nothing rather than guessing "this book".
func recapScopeLabel(scope string) string {
	switch scope {
	case "series":
		return "earlier books"
	case "book":
		return "this book"
	default:
		return ""
	}
}

// characterRoleLabel renders the schema's role enum for a reader, and returns ""
// for a role the sidecar did not state - the badge is then simply absent, which
// is the honest rendering of "unstated".
func characterRoleLabel(role string) string {
	switch role {
	case "protagonist":
		return "Protagonist"
	case "antagonist":
		return "Antagonist"
	case "supporting":
		return "Supporting"
	case "minor":
		return "Minor"
	default:
		return ""
	}
}

// revealLabel phrases a character's reveal position, mirroring the site's own
// revealLabel: chapter 0 and chapter 1 are both "from the start" (0 is front
// matter, 1 is the first chapter - neither is a reveal to gate), and anything
// later names its chapter.
func revealLabel(chapter int) string {
	if chapter <= 1 {
		return "Appears from the start"
	}
	return "First appears in chapter " + strconv.Itoa(chapter)
}

// guideTemplates is the guide pages' fact sheet, parsed into the same set as the
// three entity fact sheets (see factSheets in html.go) so "people" and any other
// shared define is available to it.
//
// The <details>/<summary> pair is doing three jobs at once, which is why it is
// the shape rather than a JS accordion: the text is IN the markup (a crawler
// reads it), it is CLOSED until a reader opens it (a spoiler is never shown to
// somebody who did not ask), and it works with no JavaScript at all. The body
// sits inside <div data-nosnippet> so that the text, while indexable, is never
// quoted into a search result snippet - which would put the spoiler exactly
// where the reader cannot avoid it.
const guideTemplates = `
{{define "guide"}}<section class="container">
<h1 class="text-hi">{{.Heading}}</h1>
{{- if .Authors}}
<p>By {{template "people" .Authors}}</p>
{{- end}}
<p class="text-dim">{{.Notice}}</p>
{{- range .Rows}}
<details>
<summary>{{.Label}}{{if .Badge}} <span class="text-dim">({{.Badge}})</span>{{end}}</summary>
<div data-nosnippet>
{{- range .Facts}}
<p class="text-dim">{{.}}</p>
{{- end}}
{{- if .Text}}
<p>{{.Text}}</p>
{{- end}}
</div>
</details>
{{- end}}
{{- if .Links}}
<h2>More about this book</h2>
{{template "linklist" .Links}}
{{- end}}
<p class="text-dim">Community-written - <a rel="license" href="{{.LicenseURL}}">{{.LicenseLabel}}</a>. <a href="{{.ContributeURL}}">{{.ContributeText}}</a></p>
</section>{{end}}
`
