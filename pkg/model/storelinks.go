package model

import (
	"net/url"
	"slices"
	"strings"
)

// storelinks.go is the STORE vocabulary: the stores a recording's links may
// name a product page in (RecordingLinks.AudiobookGuild / .GraphicAudio), each
// with the hosts it serves its pages from. They are stores that sell recordings
// the identifier-derived retailers (an ASIN's Audible marketplace, an ISBN's
// Libro.fm) do not reach, and that have no durable identifier a URL could be
// derived from - so the store's own URL is the fact, held as stated, and a dead
// link is a data edit rather than a derivation to fix.
//
// The one table both pkg/check (which holds a link to its store's host) and the
// writers (internal/importer's store-listing import) read, so the two can never
// disagree about which page belongs to which store.

// The store keys: the RecordingLinks JSON keys, the PurchaseLink retailer
// names and the store-listing import's `store` values, one spelling each.
const (
	StoreAudiobookGuild = "audiobookguild"
	StoreGraphicAudio   = "graphicaudio"
)

// storeHosts names each store's hosts, as the stores serve their product pages
// (audiobookguild.com/products/<handle>, a Shopify storefront answering on the
// bare domain; www.graphicaudio.net/<handle>.html). Both spellings of each are
// accepted, since a store states either and redirects the other.
var storeHosts = map[string][]string{
	StoreAudiobookGuild: {"audiobookguild.com", "www.audiobookguild.com"},
	StoreGraphicAudio:   {"graphicaudio.net", "www.graphicaudio.net"},
}

// StoreKeys lists the store keys in their fixed order (the order the API's
// purchase links and the artifact's columns use).
func StoreKeys() []string { return []string{StoreAudiobookGuild, StoreGraphicAudio} }

// KnownStore reports whether key names a store this vocabulary holds.
func KnownStore(key string) bool {
	_, ok := storeHosts[key]
	return ok
}

// StoreLinkHostOK reports whether raw is an https URL on one of store's hosts
// (case-insensitive, no port, no credentials). An unknown store answers false.
func StoreLinkHostOK(store, raw string) bool {
	hosts, ok := storeHosts[store]
	if !ok {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	return slices.Contains(hosts, strings.ToLower(u.Hostname()))
}

// StoreHosts returns a copy of store's hosts, for a refusal that names them.
func StoreHosts(store string) []string { return slices.Clone(storeHosts[store]) }

// Store returns the link the recording states for store ("" when none, or for
// an unknown key).
func (l *RecordingLinks) Store(store string) string {
	if l == nil {
		return ""
	}
	switch store {
	case StoreAudiobookGuild:
		return l.AudiobookGuild
	case StoreGraphicAudio:
		return l.GraphicAudio
	}
	return ""
}

// SetStore sets the link for store, reporting false for an unknown key.
func (l *RecordingLinks) SetStore(store, link string) bool {
	switch store {
	case StoreAudiobookGuild:
		l.AudiobookGuild = link
	case StoreGraphicAudio:
		l.GraphicAudio = link
	default:
		return false
	}
	return true
}

// StoreLinkKey is the identity of a store link: the URL past its scheme, with
// the host lowercased and a leading "www." dropped (each store answers on both
// spellings, StoreLinkHostOK), the path and query as the store states them.
// pkg/check holds it unique across the dataset and the store-listing import
// deduplicates on it, so the two agree on what "the same page" is. A link that
// is not an absolute https URL keys as itself.
func StoreLinkKey(link string) string {
	rest, ok := strings.CutPrefix(link, "https://")
	if !ok {
		return link
	}
	host, path, _ := strings.Cut(rest, "/")
	return strings.TrimPrefix(strings.ToLower(host), "www.") + "/" + path
}
