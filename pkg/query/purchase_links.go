package query

import (
	"strconv"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// PurchaseLink is a derived, non-affiliate route to a retailer. It deliberately
// makes no availability claim: ASINs and recording ISBNs are durable catalogue
// facts, while whether a retailer currently sells the recording is volatile.
type PurchaseLink struct {
	Retailer     string `json:"retailer"`
	ID           string `json:"id"`
	Region       string `json:"region,omitempty"`
	URL          string `json:"url"`
	Availability string `json:"availability"`
	// Free marks a link to a recording that is free to listen to and download
	// (LibriVox: public domain). Omitted, never false, on every other link.
	Free bool `json:"free,omitempty"`
}

// librivoxPurchaseLinks is a LibriVox recording's one link: its catalogue page,
// free (LibriVox recordings are public domain). It leads the list - the one
// route that costs nothing - and is absent for a recording with no LibriVox id
// or no catalogue page.
func librivoxPurchaseLinks(r RecordingDetail) []PurchaseLink {
	if r.LibrivoxID == 0 || r.Links == nil || r.Links.Librivox == "" {
		return nil
	}
	return []PurchaseLink{{
		Retailer: "librivox", ID: strconv.Itoa(r.LibrivoxID), URL: r.Links.Librivox,
		Availability: "unknown", Free: true,
	}}
}

// audibleMarketplaceHosts keys on common.schema.json #/$defs/region, the ONE
// vocabulary every region-scoped field reaches (like importer.marketplaces and
// issueform.regionAliases). TestAudibleMarketplaceHostsCoverTheRegionEnum keeps
// the key set in step with the enum in both directions; the host values have no
// second source of truth.
var audibleMarketplaceHosts = map[string]string{
	"us": "www.audible.com",
	"uk": "www.audible.co.uk",
	"ca": "www.audible.ca",
	"au": "www.audible.com.au",
	"de": "www.audible.de",
	"fr": "www.audible.fr",
	"es": "www.audible.es",
	"it": "www.audible.it",
	"jp": "www.audible.co.jp",
	"in": "www.audible.in",
	"br": "www.audible.com.br",
}

func derivedPurchaseLinks(asins []ASINRef, isbns []string) []PurchaseLink {
	links := make([]PurchaseLink, 0, len(asins)+len(isbns))
	for _, asin := range asins {
		host, ok := audibleMarketplaceHosts[asin.Region]
		if !ok {
			continue
		}
		links = append(links, PurchaseLink{
			Retailer: "audible", ID: asin.ASIN, Region: asin.Region,
			URL: "https://" + host + "/pd/" + asin.ASIN, Availability: "unknown",
		})
	}
	for _, isbn := range isbns {
		// Libro.fm URLs are ISBN-13 paths, and the catalogue's ISBN shape is
		// checksum-free (importer.NormalizeISBN records what a source states),
		// so a typo'd identifier would otherwise mint a link to nothing.
		if !model.ValidISBN13(isbn) {
			continue
		}
		links = append(links, PurchaseLink{
			Retailer: "libro-fm", ID: isbn,
			URL: "https://libro.fm/audiobooks/" + isbn, Availability: "unknown",
		})
	}
	return links
}
