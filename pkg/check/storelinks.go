package check

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// checkStoreLinks enforces what the schema cannot see about a recording's
// store links (links.audiobookguild, links.graphicaudio; pkg/model's
// storelinks.go is the vocabulary, the store-listing import the writer):
//
//   - each is on its OWN store's host (model.StoreLinkHostOK). The schema
//     already requires https; a page on another host under a store's key would
//     send a reader somewhere the label does not name;
//   - each is unique across the dataset: a store page is one product, and the
//     store-listing import deduplicates on it, so two recordings carrying one
//     would make the next import's answer depend on which it met first. The
//     comparison is model.StoreLinkKey's (the host's case and a leading www.
//     folded, since a store answers on both).
func checkStoreLinks(recs []recordWithPath, add addFunc) {
	seen := map[string]string{}
	for _, pr := range recs {
		links := pr.rec.Links
		if links == nil {
			continue
		}
		for _, store := range model.StoreKeys() {
			link := links.Store(store)
			if link == "" {
				continue
			}
			if !model.StoreLinkHostOK(store, link) {
				add(pr.path, "links.%s %q is not an https page on %s", store, link, strings.Join(model.StoreHosts(store), " or "))
				continue
			}
			key := model.StoreLinkKey(link)
			if prev, ok := seen[key]; ok {
				add(pr.path, "duplicate links.%s %q also in %s", store, link, prev)
			} else {
				seen[key] = pr.path
			}
		}
	}
}
