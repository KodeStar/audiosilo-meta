package pack

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// PackRef identifies one pack file: its family, the directory bound it sits
// under (empty when the family is flat), and its own bound.
type PackRef struct {
	Family Family
	Dir    string
	Bound  string
}

// Path returns the pack's data-relative, slash-separated path.
func (p PackRef) Path() string {
	if p.Dir == "" {
		return path.Join(p.Family.Root(), p.Bound+".json")
	}
	return path.Join(p.Family.Root(), p.Dir, p.Bound+".json")
}

func (p PackRef) String() string { return p.Path() }

// Tree is a family's pack listing in bound order: the index. In a well-formed
// family the bounds are strictly increasing, the lowest is MinBound, and each
// directory's bound equals its first pack's bound; Tree itself asserts none of
// that, so pkg/check can report a tree that violates it.
type Tree struct {
	packs []PackRef
}

// NewTree builds a tree from a pack listing. The listing is copied and sorted
// by bound; ties keep the input order.
func NewTree(f Family, packs []PackRef) *Tree {
	cp := make([]PackRef, len(packs))
	copy(cp, packs)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Bound < cp[j].Bound })
	return &Tree{packs: cp}
}

// Packs returns the packs in bound order.
func (t *Tree) Packs() []PackRef { return t.packs }

// Len returns the number of packs.
func (t *Tree) Len() int { return len(t.packs) }

// Index returns the position of the pack covering slug: the last pack whose
// bound is <= slug. It returns -1 for an empty tree, and 0 for a slug below the
// lowest bound (which a well-formed family cannot produce, since its lowest
// bound is MinBound).
func (t *Tree) Index(slug string) int {
	if len(t.packs) == 0 {
		return -1
	}
	// First pack whose bound is > slug; the one before it covers slug.
	i := sort.Search(len(t.packs), func(i int) bool { return t.packs[i].Bound > slug })
	if i == 0 {
		return 0
	}
	return i - 1
}

// Lookup returns the pack covering slug. ok is false only for an empty tree.
func (t *Tree) Lookup(slug string) (ref PackRef, ok bool) {
	i := t.Index(slug)
	if i < 0 {
		return PackRef{}, false
	}
	return t.packs[i], true
}

// Range returns the half-open slug range [lo, hi) that the pack at position i
// covers. An empty hi means unbounded above.
func (t *Tree) Range(i int) (lo, hi string, ok bool) {
	if i < 0 || i >= len(t.packs) {
		return "", "", false
	}
	lo = t.packs[i].Bound
	if i+1 < len(t.packs) {
		hi = t.packs[i+1].Bound
	}
	return lo, hi, true
}

// Dirs returns the directory bounds in order, each appearing once. A flat
// family yields a single empty bound.
func (t *Tree) Dirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range t.packs {
		if !seen[p.Dir] {
			seen[p.Dir] = true
			out = append(out, p.Dir)
		}
	}
	return out
}

// DirPacks returns the packs sitting under directory bound dir, in order.
func (t *Tree) DirPacks(dir string) []PackRef {
	var out []PackRef
	for _, p := range t.packs {
		if p.Dir == dir {
			out = append(out, p)
		}
	}
	return out
}

// Covers reports whether slug falls in the half-open range [lo, hi). An empty
// hi means unbounded above.
func Covers(lo, hi, slug string) bool {
	if slug < lo {
		return false
	}
	return hi == "" || slug < hi
}

// ReadTree lists family f's packs under the data root. It assumes the family is
// in pack layout (use Detect first when that is in doubt): every *.json file
// directly under the family root, or one directory deeper, is taken to be a
// pack, whatever its name. A missing root yields an empty tree.
//
// It is Listing.Tree over a scan of one family root rather than a walk of the
// whole tree - the same classification (packRefs), so the two can never
// disagree about what a pack is.
func ReadTree(dataDir string, f Family) (*Tree, error) {
	rels, err := jsonFilesUnder(dataDir, f.Root())
	if err != nil {
		return nil, err
	}
	return NewTree(f, packRefs(f, rels)), nil
}

// jsonExt is the ONLY extension a pack file may carry. The match is
// case-sensitive on purpose: on a case-sensitive filesystem, accepting "X.JSON"
// would list a pack with bound "X" whose every subsequent read and write then
// targeted an "X.json" that does not exist.
const jsonExt = ".json"

// isJSONFile reports whether name is a JSON file by this project's spelling.
func isJSONFile(name string) bool { return strings.HasSuffix(name, jsonExt) }

// packBound strips a pack file name's .json extension.
func packBound(name string) (string, bool) {
	if !isJSONFile(name) {
		return "", false
	}
	base := name[:len(name)-len(jsonExt)]
	if base == "" {
		return "", false
	}
	return base, true
}

// jsonFilesUnder returns every *.json file under root, data-relative and
// slash-separated, sorted. A missing root yields nothing.
func jsonFilesUnder(dataDir, root string) ([]string, error) {
	var out []string
	base := filepath.Join(dataDir, root)
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == base {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || !isJSONFile(p) {
			return nil
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
