package pack

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// survey.go answers the question everything else depends on: which files under a
// family root are AUTHORITATIVE bounds, and which are merely content sitting in
// the wrong place.
//
// The distinction is the architectural fix for a whole class of bug. Bound math,
// splitting and directory planning all assume the file names they are handed are
// real bounds in a well-formed order. Handing them a misfiled, misnamed or
// too-deep file makes them compute nonsense - two packs planned onto one path,
// a bound raised instead of lowered, entries overwritten. So nothing reaches
// that machinery until it has been classified here: a file that is not a valid
// bound in a valid place is never treated as one. Its entries are content to be
// relocated, and the file goes away.
//
// This is also what keeps metafmt's model of "wrong" equal to metacheck's. Every
// structural invariant checkPackBounds enforces has an answer here, so a tree
// metacheck rejects is never a tree metafmt calls clean.

// familySurvey is a family's files, classified.
type familySurvey struct {
	def FamilyDef
	// tree holds the authoritative packs, in bound order. It is what placement,
	// caps, splitting and relocation are all computed against.
	tree *Tree
	// files holds the parsed contents of every authoritative pack, by path.
	files map[string]*File
	// salvage holds the files that must not exist where or as they are.
	salvage []Salvage
	// unreadable holds the files nothing here can interpret.
	unreadable []Unreadable
}

// surveyed is one candidate file during classification.
type surveyed struct {
	rel   string
	dir   string
	bound string
	depth int // 0 directly under the family root, 1 one directory deep, 2+ deeper
	file  *File
}

// survey classifies every JSON file under family f's root.
func (s *Store) survey(def FamilyDef) (*familySurvey, error) {
	root := def.Family.Root()
	// A FRESH scan of the family root, never the walk the store was opened on.
	// A survey is a statement about the files that are there NOW: it decides what
	// is salvaged, relocated and deleted, and the store's walk is a snapshot that
	// anything - another process, a partly-applied Flush - may have moved on
	// from. Reading a stale one would make a file that appeared since invisible
	// (silently left behind, or overwritten by a plan that did not know about
	// it), and one deleted since would be surveyed as an empty pack. The parse
	// cache is still shared, so a pack the store has read is not read again.
	rels, err := jsonFilesUnder(s.dir, root)
	if err != nil {
		return nil, err
	}

	fs := &familySurvey{def: def, files: map[string]*File{}}
	var cands []surveyed
	for _, rel := range rels {
		parts := strings.Split(strings.TrimPrefix(rel, root+"/"), "/")
		c := surveyed{rel: rel, depth: len(parts) - 1}
		switch c.depth {
		case 0:
			c.bound, _ = packBound(parts[0])
		case 1:
			c.dir, c.bound = parts[0], ""
			c.bound, _ = packBound(parts[1])
		}
		file, perr := s.loadPath(rel)
		if perr != nil {
			fs.unreadable = append(fs.unreadable, Unreadable{
				Family: def.Family, Path: rel, Err: collapseErr(perr),
			})
			continue
		}
		c.file = file
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		fs.tree = NewTree(def.Family, nil)
		return fs, nil
	}

	// The family's SHAPE is decided by the majority of well-placed files, not by
	// any single one: one stray file in a subdirectory must not convert a whole
	// flat family into a directory-level one (and vice versa). Being over the
	// per-directory pack cap is deliberately NOT part of this: a flat family
	// that has outgrown the cap is still flat until Flush performs the
	// transition, and its packs are where they belong until then.
	flat, deep := 0, 0
	for _, c := range cands {
		switch c.depth {
		case 0:
			flat++
		case 1:
			deep++
		}
	}
	usesDirs := def.Dirs || deep > flat

	keep := cands[:0:0]
	for _, c := range cands {
		if reason := fs.reject(c, usesDirs); reason != "" {
			fs.addSalvage(c, reason)
			continue
		}
		keep = append(keep, c)
	}
	keep = fs.rejectOutOfDirRange(keep, usesDirs)
	keep = fs.rejectDuplicateBounds(keep)

	refs := make([]PackRef, 0, len(keep))
	for _, c := range keep {
		refs = append(refs, PackRef{Family: def.Family, Dir: c.dir, Bound: c.bound})
		fs.files[c.rel] = c.file
	}
	fs.tree = NewTree(def.Family, refs)
	sort.Slice(fs.salvage, func(i, j int) bool { return fs.salvage[i].Path < fs.salvage[j].Path })
	return fs, nil
}

// reject reports why a file cannot be an authoritative bound, or "" when it can.
func (fs *familySurvey) reject(c surveyed, usesDirs bool) string {
	root := fs.def.Family.Root()
	switch {
	case c.depth > 1:
		return "the file is nested deeper than a pack can sit (" + root + "/<dir-bound>/<bound>.json at the deepest)"
	case usesDirs && c.depth == 0:
		return "the file sits directly under " + root + "/, but this family's packs sit one directory deep"
	case !usesDirs && c.depth == 1:
		return "the file sits in a subdirectory, but this family's packs sit directly under " + root + "/"
	case c.bound == "":
		return "the file has no name before its .json extension, so it names no bound"
	case !model.ValidSlug(c.bound):
		return "the file name is not a valid slug bound"
	case usesDirs && !model.ValidSlug(c.dir):
		return "the directory name is not a valid slug bound"
	case c.file.Len() == 0:
		return "the pack holds no entries, so it covers no range"
	}
	return ""
}

// rejectOutOfDirRange drops packs that sit in a directory whose range does not
// contain them. It runs to a fixpoint because dropping the last pack of a
// directory removes that directory, which widens its neighbour's range.
func (fs *familySurvey) rejectOutOfDirRange(cands []surveyed, usesDirs bool) []surveyed {
	if !usesDirs {
		return cands
	}
	for {
		sort.Slice(cands, func(i, j int) bool { return cands[i].bound < cands[j].bound })
		dirs := distinctDirs(cands)
		next := map[string]string{}
		for i, d := range dirs {
			if i+1 < len(dirs) {
				next[d] = dirs[i+1]
			}
		}
		keep := cands[:0:0]
		var dropped []surveyed
		for _, c := range cands {
			if Covers(c.dir, next[c.dir], c.bound) {
				keep = append(keep, c)
				continue
			}
			dropped = append(dropped, c)
		}
		if len(dropped) == 0 {
			return keep
		}
		for _, c := range dropped {
			fs.addSalvage(c, "the pack is outside the range its directory "+
				rangeLabel(c.dir, next[c.dir])+" covers")
		}
		cands = keep
	}
}

// rejectDuplicateBounds is a backstop: two packs claiming one bound would plan
// onto one path. The directory-range rule already makes it unreachable in a
// directory-level family, and a filename is unique within a directory, so this
// only fires if one of those assumptions is ever broken.
func (fs *familySurvey) rejectDuplicateBounds(cands []surveyed) []surveyed {
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].bound != cands[j].bound {
			return cands[i].bound < cands[j].bound
		}
		return cands[i].rel < cands[j].rel
	})
	keep := cands[:0:0]
	for i, c := range cands {
		if i > 0 && c.bound == cands[i-1].bound {
			fs.addSalvage(c, "another pack already claims the bound "+c.bound)
			continue
		}
		keep = append(keep, c)
	}
	return keep
}

func (fs *familySurvey) addSalvage(c surveyed, reason string) {
	n := 0
	if c.file != nil {
		n = c.file.Len()
	}
	fs.salvage = append(fs.salvage, Salvage{
		Family: fs.def.Family, Path: c.rel, Reason: reason, Entries: n,
	})
}

// distinctDirs returns the directory bounds present in cands, sorted. cands must
// already be in bound order, which makes the directories come out in bound order
// too for any family the range rule has not yet rejected.
func distinctDirs(cands []surveyed) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		if !seen[c.dir] {
			seen[c.dir] = true
			out = append(out, c.dir)
		}
	}
	sort.Strings(out)
	return out
}

// rangeLabel renders a half-open bound range for a message.
func rangeLabel(lo, hi string) string {
	if hi == "" {
		return "[" + lo + ", end)"
	}
	return "[" + lo + ", " + hi + ")"
}

// collapseErr flattens an error to one line.
func collapseErr(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}
