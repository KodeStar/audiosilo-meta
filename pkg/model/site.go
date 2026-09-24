package model

// SiteURL is the public origin of the database's site and API. It is the one
// spelling of it: metaserve's --site-url default (canonical links, JSON-LD,
// sitemaps) and the intake bot's verdicts (the page a submitter is sent to)
// both read it, so a move is one edit.
const SiteURL = "https://meta.audiosilo.app"
