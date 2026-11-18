package repair

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// recordings.go applies merge-recordings: recording From of work Target folds into
// its recording To, two records of one production (a reissue the importer attached
// under a second ASIN as a recording of its own). No detector proposes it; a
// reviewed assertion sources it.
//
// The assertion says the two are one production, and the fold still asks
// rawentry.SameProduction - the question merge-works asks of two colliding
// recordings - and refuses where it says no: a LibriVox project is a production of
// its own, two narrator sets or an abridgement are two productions, and a runtime
// more than 10% apart contradicts the claim. SameProduction leaves the language to
// its caller, so the fold asks that too: two recordings stating different languages
// are two productions (the importer offers a moved recording only its same-language
// siblings). Those are facts on the records, which a
// reviewer fixes first if they are wrong. What folds is rawentry.MergeRecordings'
// rule: identifiers and provenance unioned, the keeper's scalars kept, the mover's
// filling only gaps, and every value chosen away named in the notes.
//
// A recording id has no tombstone namespace, so From is simply gone afterwards; the
// work, which is what a client keys on, is unchanged.

// mergeRecordings plans one recording fold into t.
func (rn *runner) mergeRecordings(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.From == "" || p.To == "" || p.From == p.To {
		return refusef(CatMalformed, "merge-recordings names recording %q into %q: it needs two different recording ids", p.From, p.To)
	}
	e, err := rn.liveWork(t, p.Target)
	if err != nil {
		return err
	}
	next := e.Clone()
	recs, err := next.Recordings()
	if err != nil {
		return fmt.Errorf("work %q: %w", p.Target, err)
	}
	keep, ok := recs[p.To]
	if !ok {
		return refusef(CatMissing, "work %q holds no recording %q to fold into", p.Target, p.To)
	}
	mover, ok := recs[p.From]
	if !ok {
		return refusef(CatMissing, "work %q holds no recording %q to fold", p.Target, p.From)
	}
	if kl, ml := keep.Str("language"), mover.Str("language"); kl != "" && ml != "" && !model.SameLanguage(kl, ml) {
		return refusef(CatProduction, "recordings %s and %s of %s are not one production on the records: languages %s and %s differ",
			p.To, p.From, p.Target, kl, ml)
	}
	why, same := rawentry.SameProduction(keep, mover)
	if !same {
		return refusef(CatProduction, "recordings %s and %s of %s are not one production on the records: %s",
			p.To, p.From, p.Target, why)
	}
	merged, lost := rawentry.MergeRecordings(keep, mover)
	recs[p.To] = merged
	delete(recs, p.From)
	if err := next.SetRecordings(recs); err != nil {
		return fmt.Errorf("work %q: %w", p.Target, err)
	}
	t.works.put(p.Target, next)
	t.noteRecordingMerged(p.Target+"/"+p.From, p.Target+"/"+p.To, why, lost)
	return nil
}
