// Package check loads the data/ tree, validates every record against its JSON
// Schema, and enforces the cross-record rules (location, id/shard agreement,
// referential integrity, global uniqueness, chapter ordering, series
// positions). It returns both the discovered problems and, best-effort, the
// loaded Catalog so metabuild can reuse the same load.
package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/model"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Problem is one validation failure, tied to the file it came from.
type Problem struct {
	Path string // data-relative, slash-separated path
	Msg  string
}

func (p Problem) String() string { return p.Path + ": " + p.Msg }

// Result is the outcome of a load: any problems plus the loaded catalog.
type Result struct {
	Problems []Problem
	Catalog  *model.Catalog
}

// OK reports whether the load found no problems.
func (r Result) OK() bool { return len(r.Problems) == 0 }

// addFunc accumulates a formatted problem for a path.
type addFunc func(path, format string, args ...any)

// recordWithPath carries a recording alongside its parent-work slug and file
// path, so it can be attached to its work after all works are read.
type recordWithPath struct {
	rec      *model.Recording
	workSlug string
	path     string
}

// pathIndex remembers where each entity was loaded from, for later problem
// reporting during cross-record checks.
type pathIndex struct {
	work   map[*model.Work]string
	rec    map[*model.Recording]string
	person map[*model.Person]string
	series map[*model.Series]string
}

// Load walks dir, validates it, and returns the result. dir is the data root.
func Load(dir string) Result {
	var probs []Problem
	add := func(path, format string, args ...any) {
		probs = append(probs, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
	}

	schemas, err := compileSchemas()
	if err != nil {
		return Result{Problems: []Problem{{Path: "schema", Msg: err.Error()}}}
	}

	cat := &model.Catalog{}
	idx := &pathIndex{
		work:   map[*model.Work]string{},
		rec:    map[*model.Recording]string{},
		person: map[*model.Person]string{},
		series: map[*model.Series]string{},
	}
	var pendingRecs []recordWithPath

	files, err := jsonFiles(dir)
	if err != nil {
		return Result{Problems: []Problem{{Path: dir, Msg: err.Error()}}}
	}

	for _, path := range files {
		rel := relSlash(dir, path)
		loc, ok := model.ParseLocation(rel)
		if !ok {
			add(rel, "unrecognized location (not a work, recording, person, or series file)")
			continue
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			add(rel, "read: %v", err)
			continue
		}

		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			add(rel, "invalid JSON: %s", collapse(err.Error()))
			continue
		}
		for _, m := range schemas.validate(loc.Kind, inst) {
			add(rel, "%s", m)
		}

		checkStructure(rel, loc, raw, add)

		switch loc.Kind {
		case model.KindWork:
			var w model.Work
			if json.Unmarshal(raw, &w) == nil {
				cat.Works = append(cat.Works, &w)
				idx.work[&w] = rel
			}
		case model.KindRecording:
			var r model.Recording
			if json.Unmarshal(raw, &r) == nil {
				pendingRecs = append(pendingRecs, recordWithPath{rec: &r, workSlug: loc.WorkSlug, path: rel})
				idx.rec[&r] = rel
			}
		case model.KindPerson:
			var p model.Person
			if json.Unmarshal(raw, &p) == nil {
				cat.People = append(cat.People, &p)
				idx.person[&p] = rel
			}
		case model.KindSeries:
			var s model.Series
			if json.Unmarshal(raw, &s) == nil {
				cat.Series = append(cat.Series, &s)
				idx.series[&s] = rel
			}
		}
	}

	// Attach recordings to their parent works (integrity flags orphans below).
	workByID := map[string]*model.Work{}
	for _, w := range cat.Works {
		if _, dup := workByID[w.ID]; !dup {
			workByID[w.ID] = w
		}
	}
	for _, pr := range pendingRecs {
		if w := workByID[pr.workSlug]; w != nil {
			w.Recordings = append(w.Recordings, pr.rec)
		}
	}

	checkIntegrity(cat, workByID, pendingRecs, idx, add)
	checkUniqueness(cat, pendingRecs, idx, add)
	checkChapters(pendingRecs, add)
	checkSeriesPositions(cat, idx, add)

	sort.Slice(probs, func(i, j int) bool {
		if probs[i].Path != probs[j].Path {
			return probs[i].Path < probs[j].Path
		}
		return probs[i].Msg < probs[j].Msg
	})

	return Result{Problems: probs, Catalog: cat}
}

// checkStructure verifies id == slug and shard == first-two-chars, using the raw
// JSON so it works even when the typed struct would zero a bad field.
func checkStructure(rel string, loc model.Location, raw []byte, add addFunc) {
	var head struct {
		ID   string `json:"id"`
		Work string `json:"work"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return // JSON errors already reported elsewhere.
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
