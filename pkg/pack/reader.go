package pack

import (
	"fmt"
	"os"
	"path/filepath"
)

// Reader is a read-through cache of parsed pack files under one data root: a
// path it has read is parsed once and served from memory afterwards.
//
// It is a WRITER's cache. A Store reads a pack to compose an entry into, reads
// it again while planning, and renders it at Flush; without the cache each of
// those parsed the file afresh. It is shareable so that a second reader of the
// same tree - pkg/check, validating what the writer is about to write into - can
// answer from a pack the store has already parsed instead of parsing it again
// (see check.LoadStore).
//
// What it deliberately does NOT do is accumulate the packs a validating pass
// reads. Everything a Reader holds stays resident until Drop, and a whole tree
// held that way is several times the memory of a pass that releases each pack as
// it finishes with it - on the seed tree, hundreds of megabytes to save the
// re-read of the handful of packs a run actually writes to. So a caller that
// reads a pack the store never asked for keeps it to itself; the store re-reads
// that pack if it ever needs it.
//
// A Reader is not safe for concurrent use with anything that FILLS or empties it
// - Read and Drop are the two, and a Store calls both. Concurrent LOOKUPS
// (Cached, peek) are safe, because they only read the two maps and write to
// neither; check.LoadStore relies on exactly that, validating a tree on a pool of
// goroutines that ask the cache and never fill it, while the store's own
// goroutine is blocked in the call.
type Reader struct {
	dir   string
	cache map[string]*File
	// standIn marks the paths whose cached pack is the empty stand-in Read makes
	// for a file that is not there, rather than a parse of anything. Cached
	// refuses those: to a caller that knows the file was supposed to exist, a
	// missing file is a failure to report, not an empty pack to validate.
	standIn map[string]bool
}

// NewReader returns an empty reader over dataDir.
func NewReader(dataDir string) *Reader {
	return &Reader{dir: dataDir, cache: map[string]*File{}, standIn: map[string]bool{}}
}

// Read returns the parsed pack at rel, a data-relative path, reading and parsing
// it only the first time.
//
// A file that does not exist reads as an EMPTY pack, so a first write into a
// family that has none works without ceremony. That stand-in is remembered as
// one: a caller that must tell "empty" from "absent" apart - pkg/check, which
// reports an unreadable pack rather than silently validating nothing - reads the
// file itself, and Cached will not hand it a stand-in.
func (r *Reader) Read(rel string) (*File, error) {
	if f, ok := r.cache[rel]; ok {
		return f, nil
	}
	raw, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			f := NewFile()
			r.cache[rel] = f
			r.standIn[rel] = true
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

// Cached returns the pack the reader parsed for rel. ok is false for a path
// nothing has read yet, and for one whose cached pack is the empty stand-in for
// a file that was not there.
//
// It is what lets a caller that does its own reading still share the parse: a
// caller reporting a read failure and a parse failure differently cannot use
// Read, so it asks Cached first and falls back to reading the file itself.
//
// What comes back is what the file held when it was read, which is not
// necessarily what it holds now - see the as-of-Open semantics documented on
// check.LoadStore.
func (r *Reader) Cached(rel string) (*File, bool) {
	if r.standIn[rel] {
		return nil, false
	}
	return r.peek(rel)
}

// peek is Cached without the stand-in refusal: everything the reader holds,
// however it came to hold it.
//
// It is what the store itself asks, and the difference matters. A stand-in is
// nothing to a validator, but to the store it is the pack a queued CREATE is
// being composed into - the file that does not exist yet, entries already Set
// into it, waiting to be rendered by the flush that will create it.
func (r *Reader) peek(rel string) (*File, bool) {
	f, ok := r.cache[rel]
	return f, ok
}

// Drop empties the cache. Anything that changes the files on disk has to call
// it, or a later read would return what the file used to hold.
func (r *Reader) Drop() {
	r.cache = map[string]*File{}
	r.standIn = map[string]bool{}
}
