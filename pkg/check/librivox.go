package check

import "github.com/kodestar/audiosilo-meta/pkg/model"

// checkLibrivox enforces the LibriVox identity rules the schema cannot see
// (LICENSING.md's public-domain exception; internal/importer/librivox.go is the
// writer):
//
//   - a recording's librivox_id is unique across the dataset: it is the identity
//     a LibriVox import deduplicates on, so two recordings claiming one project
//     would make the next import's answer depend on which it met first;
//   - a person's librivox_reader_id, and separately their librivox_author_id, is
//     unique across the dataset for the same reason (the import keys people on
//     them). The two are different LibriVox namespaces, so reader 94 and author
//     94 are not a collision;
//   - a chapter's audio_url requires the recording's librivox_id: the field
//     exists for a recording published one file per section, which today only a
//     LibriVox project is, and a chapter URL on any other recording is a field
//     nothing reads and nothing can verify.
func checkLibrivox(cat *model.Catalog, recs []recordWithPath, idx *pathIndex, add addFunc) {
	recSeen := map[int]string{}
	for _, pr := range recs {
		r := pr.rec
		if r.LibrivoxID != 0 {
			if prev, ok := recSeen[r.LibrivoxID]; ok {
				add(pr.path, "duplicate librivox_id %d also in %s", r.LibrivoxID, prev)
			} else {
				recSeen[r.LibrivoxID] = pr.path
			}
			continue
		}
		for i, ch := range r.Chapters {
			if ch.AudioURL != "" {
				add(pr.path, "chapter %d has an audio_url but the recording has no librivox_id; audio_url is a LibriVox section's file", i)
				break
			}
		}
	}

	readerSeen := map[int]string{}
	authorSeen := map[int]string{}
	for _, p := range cat.People {
		rel := idx.person[p]
		if id := p.LibrivoxReaderID; id != 0 {
			if prev, ok := readerSeen[id]; ok {
				add(rel, "duplicate librivox_reader_id %d also in %s", id, prev)
			} else {
				readerSeen[id] = rel
			}
		}
		if id := p.LibrivoxAuthorID; id != 0 {
			if prev, ok := authorSeen[id]; ok {
				add(rel, "duplicate librivox_author_id %d also in %s", id, prev)
			} else {
				authorSeen[id] = rel
			}
		}
	}
}
