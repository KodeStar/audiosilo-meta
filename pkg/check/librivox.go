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
//     nothing reads and nothing can verify;
//   - a person's death_year is not before their birth_year (both signed years,
//     negative BC; the schema refuses 0). LibriVox is the first source to state
//     either, and an inverted pair is a transcription slip the schema cannot see;
//   - librivox_title, librivox_genres and a chapter's section_number require the
//     recording's librivox_id, as audio_url does: each is a LibriVox project's
//     own statement, which no other recording has;
//   - a chapter's section_number differs from its 1-based position: absence
//     already says the position is the number, so one fact has one spelling;
//   - a chapter's readers are among the recording's narrators: a chapter is read
//     by the people who read the recording, so a reader the narrators omit is a
//     credit lost from every reader of the recording list (or a stale slug left
//     by an edit of the narrators).
func checkLibrivox(cat *model.Catalog, recs []recordWithPath, idx *pathIndex, add addFunc) {
	recSeen := map[int]string{}
	for _, pr := range recs {
		r := pr.rec
		checkChapterReaders(pr, add)
		if r.LibrivoxID != 0 {
			if prev, ok := recSeen[r.LibrivoxID]; ok {
				add(pr.path, "duplicate librivox_id %d also in %s", r.LibrivoxID, prev)
			} else {
				recSeen[r.LibrivoxID] = pr.path
			}
			for i, ch := range r.Chapters {
				if ch.SectionNumber != nil && *ch.SectionNumber == i+1 {
					add(pr.path, "chapter %d states section_number %d, its own position; omit it (absence means the position is the number)", i, i+1)
				}
			}
			continue
		}
		if r.LibrivoxTitle != "" {
			add(pr.path, "librivox_title without a librivox_id; it is a LibriVox project's own title")
		}
		if len(r.LibrivoxGenres) > 0 {
			add(pr.path, "librivox_genres without a librivox_id; they are a LibriVox project's own genres")
		}
		for i, ch := range r.Chapters {
			if ch.AudioURL != "" {
				add(pr.path, "chapter %d has an audio_url but the recording has no librivox_id; audio_url is a LibriVox section's file", i)
				break
			}
		}
		for i, ch := range r.Chapters {
			if ch.SectionNumber != nil {
				add(pr.path, "chapter %d has a section_number but the recording has no librivox_id; section_number is a LibriVox section's number", i)
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
		if p.BirthYear != 0 && p.DeathYear != 0 && p.DeathYear < p.BirthYear {
			add(rel, "death_year %d is before birth_year %d", p.DeathYear, p.BirthYear)
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

// checkChapterReaders holds every chapter's readers to the recording's
// narrators, reporting each stray reader once per recording.
func checkChapterReaders(pr recordWithPath, add addFunc) {
	var narrators map[string]bool
	reported := map[string]bool{}
	for i, ch := range pr.rec.Chapters {
		for _, rd := range ch.Readers {
			if narrators == nil {
				narrators = make(map[string]bool, len(pr.rec.Narrators))
				for _, n := range pr.rec.Narrators {
					narrators[n] = true
				}
			}
			if !narrators[rd] && !reported[rd] {
				reported[rd] = true
				add(pr.path, "chapter %d reader %q is not one of the recording's narrators", i, rd)
			}
		}
	}
}
