package check

// The file-per-entity reader, preserved verbatim for the migration window: the
// live data/ tree is still in the legacy layout while the pack tooling lands,
// and CI validates that tree on every PR. Load routes a family here only when
// pack.DetectLayout says it is legacy.
//
// TEMPORARY. The migration PR converts data/ to the pack layout and deletes
// this file together with model.Shard and model.ParseLocation. Do not build new
// behaviour on it; new rules belong in packcheck.go, which runs on the layout
// everything is moving to.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// loadLegacy reads the given data-relative files as one-entity-per-file
// records. It returns the recordings it read, unattached: their parent works
// may not have been read yet, so Load attaches them once every family is in.
func (l *loader) loadLegacy(dir string, rels []string) []recordWithPath {
	var pendingRecs []recordWithPath

	for _, rel := range rels {
		loc, ok := model.ParseLocation(rel)
		if !ok {
			l.add(rel, "unrecognized location (not a work, recording, person, or series file)")
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			l.add(rel, "read: %v", err)
			continue
		}

		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			l.add(rel, "invalid JSON: %s", collapse(err.Error()))
			continue
		}
		for _, m := range l.schemas.validate(loc.Kind, inst) {
			l.add(rel, "%s", m)
		}

		checkStructure(rel, loc, raw, l.add)

		switch loc.Kind {
		case model.KindWork:
			var w model.Work
			if json.Unmarshal(raw, &w) == nil {
				l.cat.Works = append(l.cat.Works, &w)
				l.idx.work[&w] = rel
			}
		case model.KindRecording:
			var r model.Recording
			if json.Unmarshal(raw, &r) == nil {
				pendingRecs = append(pendingRecs, recordWithPath{rec: &r, workSlug: loc.WorkSlug, path: rel})
				l.idx.rec[&r] = rel
			}
		case model.KindPerson:
			var p model.Person
			if json.Unmarshal(raw, &p) == nil {
				l.cat.People = append(l.cat.People, &p)
				l.idx.person[&p] = rel
			}
		case model.KindSeries:
			var s model.Series
			if json.Unmarshal(raw, &s) == nil {
				l.cat.Series = append(l.cat.Series, &s)
				l.idx.series[&s] = rel
			}
		case model.KindCharacters:
			var c model.Characters
			if json.Unmarshal(raw, &c) == nil {
				l.cat.Characters = append(l.cat.Characters, &c)
				l.idx.characters[&c] = rel
			}
		case model.KindRecaps:
			var rc model.Recaps
			if json.Unmarshal(raw, &rc) == nil {
				l.cat.Recaps = append(l.cat.Recaps, &rc)
				l.idx.recaps[&rc] = rel
			}
		}
	}

	return pendingRecs
}

// checkStructure verifies id == slug and shard == first-two-chars, using the raw
// JSON so it works even when the typed struct would zero a bad field. The pack
// layout's replacement is checkPackKeys, which agrees an entry key with its
// entity id instead of a path with a filename.
func checkStructure(rel string, loc model.Location, raw []byte, add addFunc) {
	var head struct {
		ID   string `json:"id"`
		Work string `json:"work"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return // JSON errors already reported elsewhere.
	}
	// Per-work sidecars (characters/recaps) have no own id; they are identified
	// by their parent work dir and its shard, and carry a work backref instead.
	if loc.Kind == model.KindCharacters || loc.Kind == model.KindRecaps {
		if !model.ValidSlug(loc.WorkSlug) {
			add(rel, "slug %q is not a valid slug", loc.WorkSlug)
		}
		if want := model.Shard(loc.WorkSlug); loc.Shard != want {
			add(rel, "shard dir %q must be %q (first two chars of work slug %q)", loc.Shard, want, loc.WorkSlug)
		}
		if head.Work != loc.WorkSlug {
			add(rel, "work %q must equal the parent work dir id %q", head.Work, loc.WorkSlug)
		}
		return
	}
	if head.ID != loc.Slug {
		add(rel, "id %q does not match its file/dir slug %q", head.ID, loc.Slug)
	}
	if !model.ValidSlug(loc.Slug) {
		add(rel, "slug %q is not a valid slug", loc.Slug)
	}
	// For a recording the shard directory belongs to its parent work, not the
	// recording's own slug.
	shardSlug := loc.Slug
	if loc.Kind == model.KindRecording {
		shardSlug = loc.WorkSlug
	}
	if want := model.Shard(shardSlug); loc.Shard != want {
		add(rel, "shard dir %q must be %q (first two chars of slug %q)", loc.Shard, want, shardSlug)
	}
	if loc.Kind == model.KindRecording && head.Work != loc.WorkSlug {
		add(rel, "recording work %q must equal the parent work dir id %q", head.Work, loc.WorkSlug)
	}
}

// jsonFiles lists every .json file under dir, sorted. It serves both layouts:
// the legacy reader consumes it directly, and the pack walker uses it to spot
// files a family's pack listing does not account for.
func jsonFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// relSlash returns path relative to dir with forward slashes.
func relSlash(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel)
}
