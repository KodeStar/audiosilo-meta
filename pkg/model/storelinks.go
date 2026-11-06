package model

import (
	"iter"
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

// storeDef is one store: its key, the hosts it serves its product pages from,
// and the RecordingLinks field that holds its page.
type storeDef struct {
	key   string
	hosts []string
	field func(*RecordingLinks) *string
}

// stores is the vocabulary, in its fixed order (the order the API's purchase
// links and the artifact's columns use). The hosts are as the stores serve
// their product pages (audiobookguild.com/products/<handle>, a Shopify
// storefront answering on the bare domain; www.graphicaudio.net/<handle>.html).
// Both spellings of each are accepted, since a store states either and
// redirects the other.
var stores = []storeDef{
	{StoreAudiobookGuild, []string{"audiobookguild.com", "www.audiobookguild.com"}, func(l *RecordingLinks) *string { return &l.AudiobookGuild }},
	{StoreGraphicAudio, []string{"graphicaudio.net", "www.graphicaudio.net"}, func(l *RecordingLinks) *string { return &l.GraphicAudio }},
}

func storeOf(key string) (storeDef, bool) {
	for _, s := range stores {
		if s.key == key {
			return s, true
		}
	}
	return storeDef{}, false
}

// StoreKeys lists the store keys in their fixed order.
func StoreKeys() []string {
	keys := make([]string, len(stores))
	for i, s := range stores {
		keys[i] = s.key
	}
	return keys
}

// KnownStore reports whether key names a store this vocabulary holds.
func KnownStore(key string) bool {
	_, ok := storeOf(key)
	return ok
}

// StoreLinkOK reports whether raw is a product page on one of store's hosts:
// an https URL (the host case-insensitive, no port, no credentials) with a
// path past the host and no query string or fragment. The scheme is the
// literal "https://" the schema's https_url requires and StoreLinkKey cuts:
// url.Parse lowercases a scheme, so it alone would pass "HTTPS://...", which
// the schema refuses. A query or fragment is refused because StoreLinkKey is
// the URL itself: a tracking or variant parameter would make one page two
// keys, so the dedupe and pkg/check's uniqueness would miss it. A path of
// nothing but slashes (StoreLinkKey drops trailing ones) is the store's
// homepage, never a book. An unknown store answers false.
func StoreLinkOK(store, raw string) bool {
	s, ok := storeOf(store)
	if !ok || !strings.HasPrefix(raw, "https://") || strings.ContainsAny(raw, "?#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Port() != "" || strings.Trim(u.Path, "/") == "" {
		return false
	}
	return slices.Contains(s.hosts, strings.ToLower(u.Hostname()))
}

// StoreLinkRule names what StoreLinkOK requires, for a refusal that quotes it.
func StoreLinkRule(store string) string {
	return "an https page on " + strings.Join(StoreHosts(store), " or ") + " (a path past the host, no query string or fragment)"
}

// StoreHosts returns a copy of store's hosts (StoreLinkRule names them).
func StoreHosts(store string) []string {
	s, _ := storeOf(store)
	return slices.Clone(s.hosts)
}

// StoreLinks yields each store page the recording states, as (store key,
// link), in StoreKeys order. A nil links object yields nothing.
func (l *RecordingLinks) StoreLinks() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		if l == nil {
			return
		}
		for _, s := range stores {
			if v := *s.field(l); v != "" && !yield(s.key, v) {
				return
			}
		}
	}
}

// SetStore sets the link for store, reporting false for an unknown key.
func (l *RecordingLinks) SetStore(store, link string) bool {
	s, ok := storeOf(store)
	if ok {
		*s.field(l) = link
	}
	return ok
}

// StoreLinkKey is the identity of a store link: the URL past its scheme, with
// the host lowercased and a leading "www." dropped (each store answers on both
// spellings, StoreLinkOK), the path as the store states it less any trailing
// "/" (one page either way). StoreLinkOK refuses a query string or fragment,
// so the path is the whole page.
// pkg/check holds it unique across the dataset and the store-listing import
// deduplicates on it, so the two agree on what "the same page" is. A link that
// is not an absolute https URL keys as itself.
func StoreLinkKey(link string) string {
	rest, ok := strings.CutPrefix(link, "https://")
	if !ok {
		return link
	}
	host, path, _ := strings.Cut(rest, "/")
	return strings.TrimPrefix(strings.ToLower(host), "www.") + "/" + strings.TrimRight(path, "/")
}
