package check

import (
	"maps"
	"slices"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

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
//   - a person's librivox_reader_aliases and librivox_author_aliases are sorted,
//     and each is unique against every id and every other alias of its namespace,
//     the record's own included: an alias is an id the import resolves to this
//     record, so it answers one question exactly as the record's own id does (the
//     schema requires that own id beside it);
//   - a chapter's audio_url requires the recording's librivox_id: the field
//     exists for a recording published one file per section, which today only a
//     LibriVox project is, and a chapter URL on any other recording is a field
//     nothing reads and nothing can verify;
//   - a person's death_year is not before their birth_year (both signed years,
//     negative BC; the schema refuses 0). LibriVox is the first source to state
//     either, and an inverted pair is a transcription slip the schema cannot see;
//   - librivox_title, librivox_genres, the recording's own credits, and a
//     chapter's section_number and readers require the recording's
//     librivox_id, as audio_url does: each is a LibriVox project's own
//     statement, which no other recording has (so the readers rules below only
//     ever judge a LibriVox recording);
//   - a chapter's section_number differs from its 1-based position: absence
//     already says the position is the number, so one fact has one spelling;
//   - a recording with ONE narrator does not list readers on every chapter:
//     absence already says that narrator reads them all, so one fact has one
//     spelling (a chapter without readers beside others with them still says
//     that section states none);
//   - a chapter's readers are among the recording's narrators: a chapter is read
//     by the people who read the recording, so a reader the narrators omit is a
//     credit lost from every reader of the recording list (or a stale slug left
//     by an edit of the narrators).
func checkLibrivox(cat *model.Catalog, recs []recordWithPath, idx *pathIndex, add addFunc) {
	recSeen := map[int]string{}
	for _, pr := range recs {
		r := pr.rec
		lv := r.LibrivoxID != 0
		if lv {
			if prev, ok := recSeen[r.LibrivoxID]; ok {
				add(pr.path, "duplicate librivox_id %d also in %s", r.LibrivoxID, prev)
			} else {
				recSeen[r.LibrivoxID] = pr.path
			}
		} else {
			if r.LibrivoxTitle != "" {
				add(pr.path, "librivox_title without a librivox_id; it is a LibriVox project's own title")
			}
			if len(r.LibrivoxGenres) > 0 {
				add(pr.path, "librivox_genres without a librivox_id; they are a LibriVox project's own genres")
			}
			if len(r.Credits) > 0 {
				add(pr.path, "credits without a librivox_id; a recording's own credits are a LibriVox project's translators")
			}
		}
		// One walk of the chapters; a non-LibriVox recording's audio_url and
		// section_number are each reported once, a stray reader once per reader.
		var audioReported, numberReported, readersReported bool
		var readerReported map[string]bool
		everyChapterRead := len(r.Chapters) > 0
		for i := range r.Chapters {
			ch := &r.Chapters[i]
			everyChapterRead = everyChapterRead && len(ch.Readers) > 0
			switch {
			case lv && ch.SectionNumber != nil && *ch.SectionNumber == i+1:
				add(pr.path, "chapter %d states section_number %d, its own position; omit it (absence means the position is the number)", i, i+1)
			case !lv && ch.SectionNumber != nil && !numberReported:
				numberReported = true
				add(pr.path, "chapter %d has a section_number but the recording has no librivox_id; section_number is a LibriVox section's number", i)
			}
			if !lv && ch.AudioURL != "" && !audioReported {
				audioReported = true
				add(pr.path, "chapter %d has an audio_url but the recording has no librivox_id; audio_url is a LibriVox section's file", i)
			}
			if !lv && len(ch.Readers) > 0 && !readersReported {
				readersReported = true
				add(pr.path, "chapter %d has readers but the recording has no librivox_id; readers are a LibriVox section's", i)
			}
			for _, rd := range ch.Readers {
				if !slices.Contains(r.Narrators, rd) && !readerReported[rd] {
					if readerReported == nil {
						readerReported = map[string]bool{}
					}
					readerReported[rd] = true
					add(pr.path, "chapter %d reader %q is not one of the recording's narrators", i, rd)
				}
			}
		}
		if everyChapterRead && len(r.Narrators) == 1 {
			add(pr.path, "every chapter lists readers on a recording with one narrator; omit them (no readers on a one-narrator recording already says they read every chapter)")
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
	// A second pass, so an alias meets every record's own id whichever record
	// the load reached first.
	for _, p := range cat.People {
		if len(p.LibrivoxReaderAliases)+len(p.LibrivoxAuthorAliases) == 0 {
			continue
		}
		rel := idx.person[p]
		checkAliases(rel, "librivox_reader_aliases", "reader", p.LibrivoxReaderAliases, readerSeen, add)
		checkAliases(rel, "librivox_author_aliases", "author", p.LibrivoxAuthorAliases, authorSeen, add)
	}
}

// checkAliases is one record's alias list (field) against the ids of its
// namespace seen so far, which it joins.
func checkAliases(rel, field, namespace string, aliases []int, seen map[int]string, add addFunc) {
	if !slices.IsSorted(aliases) {
		add(rel, "%s is not sorted ascending: %v", field, aliases)
	}
	for _, id := range aliases {
		if prev, ok := seen[id]; ok {
			add(rel, "%s names %d, already a LibriVox %s id in %s", field, id, namespace, prev)
		} else {
			seen[id] = rel
		}
	}
}

// checkLibrivoxTombstones holds a retired person slug of a LibriVox id form
// (model.LibrivoxIDOfSlug) to the record it points at: that record answers to the
// id, as its own or an alias, so a fold cannot lose the id and the next import
// mint the twin again. A target that is no record is checkRedirects' problem.
func checkLibrivoxTombstones(cat *model.Catalog, add addFunc) {
	table := cat.Redirects[model.RedirectPeople]
	if len(table) == 0 {
		return
	}
	byID := make(map[string]*model.Person, len(cat.People))
	for _, p := range cat.People {
		byID[p.ID] = p
	}
	for _, from := range slices.Sorted(maps.Keys(table)) {
		reader, author := model.LibrivoxIDOfSlug(from)
		p := byID[table[from]]
		if p == nil {
			continue
		}
		if reader != 0 && reader != p.LibrivoxReaderID && !slices.Contains(p.LibrivoxReaderAliases, reader) {
			add(redirectPath(model.RedirectPeople, from), "retired slug %q is LibriVox reader %d's form, and its target %q does not answer to that id: "+
				"record it as the target's librivox_reader_id or in its librivox_reader_aliases", from, reader, p.ID)
		}
		if author != 0 && author != p.LibrivoxAuthorID && !slices.Contains(p.LibrivoxAuthorAliases, author) {
			add(redirectPath(model.RedirectPeople, from), "retired slug %q is LibriVox author %d's form, and its target %q does not answer to that id: "+
				"record it as the target's librivox_author_id or in its librivox_author_aliases", from, author, p.ID)
		}
	}
}
