package pack

import (
	"fmt"
	"os"
	"path/filepath"
)

// Reader is a read-through cache of parsed pack files under one data root: a
// path it has read is parsed once and served from memory afterwards.
//
// It exists so the two halves of a writer's run can share ONE read and ONE parse
// of each pack. pkg/check parses every pack in the tree to validate it, and a
// Store parses the packs the writer reads and rewrites; run back to back on the
// same tree - which is what every importer and intake run does - that was the
// whole catalogue parsed twice. Handing both the same Reader makes the second
// pass free (see check.LoadStore).
//
// The trade is memory: a pack stays resident until Drop, where an unshared read
// could be collected as soon as the caller finished with it. A Store drops the
// cache at Flush, because what is on disk is then no longer what was parsed.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	dir   string
	cache map[string]*File
}

// NewReader returns an empty reader over dataDir.
func NewReader(dataDir string) *Reader {
	return &Reader{dir: dataDir, cache: map[string]*File{}}
}

// Read returns the parsed pack at rel, a data-relative path, reading and parsing
// it only the first time.
//
// A file that does not exist reads as an EMPTY pack, so a first write into a
// family that has none works without ceremony. A caller that must tell "empty"
// from "absent" apart - pkg/check, which reports an unreadable pack rather than
// silently validating nothing - reads the file itself and Holds the result.
func (r *Reader) Read(rel string) (*File, error) {
	if f, ok := r.cache[rel]; ok {
		return f, nil
	}
	raw, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			f := NewFile()
			r.cache[rel] = f
			return f, nil
		}
		return nil, err
	}
	f, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	r.cache[rel] = f
	return f, nil
}

// Cached returns the pack the reader holds for rel. ok is false for a path
// nothing has read yet.
//
// It is what lets a caller that does its own reading still share the parse: a
// caller reporting a read failure and a parse failure differently cannot use
// Read, so it asks Cached first and Holds what it had to read itself. A cached
// pack may be the empty stand-in Read makes for a file that does not exist, so
// only ask for paths a walk actually found.
func (r *Reader) Cached(rel string) (*File, bool) {
	f, ok := r.cache[rel]
	return f, ok
}

// Hold caches file as the parsed contents of rel, so a later Read or Cached is
// served from it. The file is NOT copied: the caller is handing it over.
func (r *Reader) Hold(rel string, file *File) { r.cache[rel] = file }

// Drop empties the cache. Anything that changes the files on disk has to call
// it, or a later read would return what the file used to hold.
func (r *Reader) Drop() { r.cache = map[string]*File{} }
