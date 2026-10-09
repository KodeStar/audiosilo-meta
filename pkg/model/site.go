package model

// SiteURL is the public origin of the database's site and API. It is the one
// spelling of it: metaserve's --site-url default (canonical links, JSON-LD,
// sitemaps) and the intake bot's verdicts (the page a submitter is sent to)
// both read it, so a move is one edit.
const SiteURL = "https://meta.audiosilo.app"

// The entity pages' path prefixes on SiteURL: a work is at WorksPath + its slug,
// and so on. Two packages build those URLs and must agree letter for letter -
// internal/serve, which SERVES the pages (and their canonical, og:, JSON-LD and
// sitemap URLs), and pkg/query, whose watch feeds LINK to them - so they are
// spelled once, here, beside the other route literals the model owns
// (reserved.go).
const (
	WorksPath  = "/works/"
	PeoplePath = "/people/"
	SeriesPath = "/series/"
)
