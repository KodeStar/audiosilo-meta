package pack

import (
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// WorkEntry is a works-family pack entry, keyed by work slug: every field of a
// standalone work record plus the work's recordings keyed by recording slug.
//
// The composite is assembled here rather than in pkg/model on purpose.
// model.Work's wire shape stays exactly as it is - its Recordings slice remains
// json:"-", populated by a loader - so the legacy per-file reader, metabuild,
// and the artifact are untouched by the pack layout. Recording records keep
// their own id, work backref, license and sources, so a recording's bytes are
// the same whether it sits in its own file or in a pack; the map key is simply
// the filename's replacement.
//
// It is deliberately a plain struct with no JSON methods. Marshalling a work
// back out through the typed struct would emit every zero-valued required field
// - an "abridged": false on a recording whose source never stated one - and
// stating a fact nobody gave us is the one thing this project does not do.
// Readers decode the parts they need (pkg/check), and writers edit the decoded
// map (DecodeEntry and SetRecording), so bytes the tooling does not model are
// carried through rather than regenerated.
//
// Invariants the walker enforces: the entry key equals Work.ID, each map key
// equals its recording's ID, and every recording's Work equals the entry key.
type WorkEntry struct {
	Work       *model.Work
	Recordings map[string]*model.Recording
}

// Compose returns the entry's work with its Recordings slice populated in
// recording-slug order, the shape pkg/check's Catalog and metabuild consume.
func (e WorkEntry) Compose() *model.Work {
	if e.Work == nil {
		return nil
	}
	w := e.Work
	w.Recordings = nil
	keys := make([]string, 0, len(e.Recordings))
	for k := range e.Recordings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		w.Recordings = append(w.Recordings, e.Recordings[k])
	}
	return w
}
