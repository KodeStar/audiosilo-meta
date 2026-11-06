package query

import "github.com/kodestar/audiosilo-meta/pkg/model"

// storelinks.go is the API's side of the STORE LINKS (storeLinksSchemaVersion):
// a recording's product page in a store that sells it outside the
// identifier-derived retailers (pkg/model's storelinks.go names the stores),
// returned on the recording's `links` and as a purchase link.

// storeLinkColumns are the recordings columns a version 11 artifact carries,
// in model.StoreKeys order.
const storeLinkColumns = `link_audiobookguild, link_graphicaudio`

// storeLinksShapeSQL proves what a version 11 artifact claims, as
// librivoxShapeSQL does for version 8: it names the columns and reads no row.
const storeLinksShapeSQL = `SELECT ` + storeLinkColumns + ` FROM recordings LIMIT 0`

// hasStoreLinks reports whether this artifact carries the store link columns.
func (s *DB) hasStoreLinks() bool { return s.schemaVersion >= storeLinksSchemaVersion }

// storePurchaseLinks is one purchase link per store page the recording states,
// in model.StoreKeys order. Unlike the derived links, the URL is the store's
// own statement (there is no identifier to derive one from), so the link's id
// IS that URL; its availability is unknown, as every link's is. They follow
// the free LibriVox link and lead the derived ones: a stated page before a
// constructed route.
func storePurchaseLinks(links *model.RecordingLinks) []PurchaseLink {
	var out []PurchaseLink
	for store, u := range links.StoreLinks() {
		out = append(out, PurchaseLink{Retailer: store, ID: u, URL: u, Availability: "unknown"})
	}
	return out
}
