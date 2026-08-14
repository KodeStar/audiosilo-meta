package audit

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// SER-DUP / SER-PAREN subclasses.
const (
	serDupName   = "normalized-name" // the names differ only by case, diacritics, articles or a decoration suffix
	serDupSaga   = "suffix-saga"     // ...and the only difference is a trailing " Saga"
	serParenSolo = "decorated-only"  // a parenthetical-decorated name with no undecorated sibling
	serParenPair = "decorated-pair"  // ...with one, which may well be a deliberate second ordering
)

// seriesKeys is one series' two comparison keys plus whether its name carries a
// parenthetical. Both detectors read it, and it is computed ONCE per series: the
// keys fold through model.Slugify (NFD normalization plus a builder) and were being
// computed four times over 45k series across two detectors and their tie-breaks.
type seriesKeys struct {
	series *model.Series
	tight  string
	saga   string
	paren  bool
}

// seriesKeyIndex is every series' keys, in catalogue order.
func seriesKeyIndex(all []*model.Series) []seriesKeys {
	out := make([]seriesKeys, 0, len(all))
	for _, s := range all {
		out = append(out, seriesKeys{
			series: s,
			tight:  titlerule.SeriesKey(s.Name),
			saga:   titlerule.SeriesSagaKey(s.Name),
			paren:  strings.ContainsAny(s.Name, "(["),
		})
	}
	return out
}

// detectSeriesDup groups series whose names are the same name spelled two ways.
func detectSeriesDup(ix *index, keys []seriesKeys) *findings {
	f := &findings{class: ClassSeriesDup}

	byTight, tightOrder := groupBy(keys, func(k seriesKeys) string { return k.tight })
	for _, key := range tightOrder {
		if group := byTight[key]; len(group) >= 2 {
			f.add(seriesDupFinding(ix, serDupName, key, group,
				"fold the members onto the canonical name, then delete the empty spelling"))
		}
	}

	// The saga key is strictly looser, so it only ever adds groups the tight key
	// did not already report - and it proposes nothing, because a trailing "Saga"
	// is usually part of the name.
	bySaga, sagaOrder := groupBy(keys, func(k seriesKeys) string { return k.saga })
	for _, key := range sagaOrder {
		group := bySaga[key]
		if len(group) < 2 || allShareKey(group, func(k seriesKeys) string { return k.tight }) {
			continue
		}
		f.add(seriesDupFinding(ix, serDupSaga, key, group,
			`the names differ only by a trailing "Saga", which is part of a real series name at least as often as it is `+
				"catalogue decoration - no merge is proposed"))
	}
	return f
}

func seriesDupFinding(ix *index, sub, key string, group []seriesKeys, reason string) Finding {
	fd := Finding{Subclass: sub, Key: key}
	var names, ids []string
	best, bestRank := group[0].series, titlerule.SeriesRank{}
	for i, k := range group {
		fd.Series = append(fd.Series, ix.seriesRef(k.series))
		names = append(names, k.series.Name)
		ids = append(ids, k.series.ID)
		// Through titlerule's ladder, so the report and a repair pass agree on
		// which spelling survives.
		r := titlerule.SeriesRank{Works: len(k.series.Works), ID: k.series.ID}
		if i == 0 || r.Better(bestRank) {
			best, bestRank = k.series, r
		}
	}
	others := make([]string, 0, len(ids))
	for _, id := range sortedUnique(ids) {
		if id != best.ID {
			others = append(others, id)
		}
	}
	fd.Propose = Proposal{
		Op:       OpMergeSeries,
		Target:   best.ID,
		Others:   others,
		Advisory: sub == serDupSaga,
		Reason:   reason,
	}
	if sub == serDupSaga {
		fd.Propose.Op = OpReview
	}
	fd.Notes = []string{"spellings: " + truncateList(sortedUnique(names), 8)}
	return fd
}

// detectSeriesParen reports series names carrying a parenthetical decoration. It
// proposes nothing: "Vorkosigan Saga (chronological)" beside "Vorkosigan Saga" is
// very likely a DELIBERATE second ordering of one series, which the data model has
// no other way to express, so the only honest output is "a human should look".
//
// It reads the SAME key index detectSeriesDup does, and in ONE pass: the plain
// siblings are collected as the loop goes rather than in a first loop of their own,
// which is what a decorated name later in catalogue order needs anyway - so the
// sibling lookup is completed after the walk, not during it.
func detectSeriesParen(ix *index, keys []seriesKeys) *findings {
	f := &findings{class: ClassSeriesParen}
	plain := map[string][]string{}
	var decorated []seriesKeys
	for _, k := range keys {
		if k.paren {
			decorated = append(decorated, k)
			continue
		}
		plain[k.tight] = append(plain[k.tight], k.series.ID)
	}
	for _, k := range decorated {
		s := k.series
		siblings := sortedUnique(plain[k.tight])
		fd := Finding{
			Key:    s.ID,
			Series: []SeriesRef{ix.seriesRef(s)},
		}
		fd.Propose = Proposal{
			Op:       OpReview,
			Target:   s.ID,
			Field:    "name",
			From:     s.Name,
			To:       titlerule.TidyTitle(titlerule.StripParenGroups(s.Name)),
			Advisory: true,
		}
		if len(siblings) > 0 {
			fd.Subclass = serParenPair
			fd.Propose.Others = siblings
			fd.Propose.Reason = "the parenthetical may be a deliberate alternative ordering of the sibling series - never merged automatically"
			fd.Notes = []string{"undecorated sibling: " + truncateList(siblings, 8)}
			for _, id := range siblings {
				if sib := ix.seriesByID[id]; sib != nil {
					fd.Series = append(fd.Series, ix.seriesRef(sib))
				}
			}
		} else {
			fd.Subclass = serParenSolo
			fd.Propose.Reason = "the parenthetical is decoration with nothing to fold onto, so the name itself may want cleaning"
		}
		f.add(fd)
	}
	return f
}
