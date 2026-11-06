package check

import "github.com/kodestar/audiosilo-meta/pkg/model"

// checkStoreLinks enforces what the schema cannot see about a recording's
// store links (links.audiobookguild, links.graphicaudio; pkg/model's
// storelinks.go is the vocabulary, the store-listing import the writer):
//
//   - each is a page on its OWN store's host (model.StoreLinkOK). The schema
//     already requires https; a page on another host under a store's key would
//     send a reader somewhere the label does not name, and a query string,
//     fragment or bare homepage would make the uniqueness below miss a page;
//   - each is unique across the dataset: a store page is one product, and the
//     store-listing import deduplicates on it, so two recordings carrying one
//     would make the next import's answer depend on which it met first. The
//     comparison is model.StoreLinkKey's (a trailing "/", the host's case and
//     a leading www. folded, since a store serves one page under each).
func checkStoreLinks(recs []recordWithPath, add addFunc) {
	seen := map[string]string{}
	for _, pr := range recs {
		for store, link := range pr.rec.Links.StoreLinks() {
			if !model.StoreLinkOK(store, link) {
				add(pr.path, "links.%s %q is not %s", store, link, model.StoreLinkRule(store))
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
