package pack

import (
	"encoding/json"
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
// Invariants the walker enforces: the entry key equals Work.ID, each map key
// equals its recording's ID, and every recording's Work equals the entry key.
type WorkEntry struct {
	Work       *model.Work
	Recordings map[string]*model.Recording
}

// MarshalJSON renders the composite: the work's own fields at the top level,
// with the recordings map spliced in under "recordings" (omitted when empty).
func (e WorkEntry) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(e.Work)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	if len(e.Recordings) > 0 {
		recs, err := json.Marshal(e.Recordings)
		if err != nil {
			return nil, err
		}
		obj["recordings"] = recs
	} else {
		delete(obj, "recordings")
	}
	return json.Marshal(obj)
}

// UnmarshalJSON reads the composite. The work is decoded from the same object
// the recordings map is spliced into: "recordings" is not a model.Work field,
// so it is ignored by that pass and read separately here.
func (e *WorkEntry) UnmarshalJSON(b []byte) error {
	var w model.Work
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	var aux struct {
		Recordings map[string]*model.Recording `json:"recordings"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	e.Work = &w
	e.Recordings = aux.Recordings
	return nil
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

// CommunityEntry is a works-community pack entry, keyed by work slug: the two
// CC BY-SA sidecars for that work. Either member may be absent; an entry with
// neither is invalid. Both keep their own work backref and share-alike license,
// so the schema lock ($defs/license_content) still applies per record.
type CommunityEntry struct {
	Characters *model.Characters `json:"characters,omitempty"`
	Recaps     *model.Recaps     `json:"recaps,omitempty"`
}
