package audit

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// SER-DUP subclasses.
const (
	serDupName   = "normalized-name" // the names differ only by case, diacritics, articles or a decoration suffix
	serDupSaga   = "suffix-saga"     // ...and the only difference is a trailing " Saga"
	serParenSolo = "decorated-only"  // a parenthetical-decorated name with no undecorated sibling
	serParenPair = "decorated-pair"  // ...with one, which may well be a deliberate second ordering
)

// seriesDecorSuffixes are the trailing words a retailer's catalogue appends to a
// series name that the name itself does not carry: Audible lists "Dragon Heart
// Series" and "Richard Sharpe Novels" where the series is called "Dragon Heart"
// and "Richard Sharpe".
//
// " Saga" is deliberately NOT here. It is part of the real name far more often
// than it is decoration ("Vorkosigan Saga", "The Saga of Seven Suns"), so it gets
// its own looser key and its own subclass, which proposes nothing.
var seriesDecorSuffixes = []string{
	" series", " novels", " novel", " books", " book", " trilogy",
	" audiobooks", " audiobook", " collection", " box set", " boxed set",
}

// sagaSuffix is the one suffix held back from seriesDecorSuffixes.
const sagaSuffix = " saga"

// seriesKey is a series name's comparison identity: parentheticals removed, a
// leading article dropped, a trailing catalogue decoration dropped, then folded
// through the project's own slug rules with the hyphens removed - so case,
// diacritics, punctuation and spacing are not identity.
func seriesKey(name string) string { return seriesKeyWith(name, false) }

// seriesSagaKey is seriesKey with a trailing " Saga" dropped too.
func seriesSagaKey(name string) string { return seriesKeyWith(name, true) }

func seriesKeyWith(name string, dropSaga bool) string {
	t := strings.ToLower(strings.TrimSpace(stripParenGroups(name)))
	t = strings.TrimSpace(dropLeadingArticle(t))
	// Peel repeatedly: "The Dragon Heart Series Collection" carries two.
	for i := 0; i < 3; i++ {
		trimmed := t
		for _, suf := range seriesDecorSuffixes {
			if len(trimmed) > len(suf) && strings.HasSuffix(trimmed, suf) {
				trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, suf))
				break
			}
		}
		if dropSaga && len(trimmed) > len(sagaSuffix) && strings.HasSuffix(trimmed, sagaSuffix) {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, sagaSuffix))
		}
		if trimmed == t {
			break
		}
		t = trimmed
	}
	return foldKey(t)
}

// detectSeriesDup groups series whose names are the same name spelled two ways.
func detectSeriesDup(ix *index) *findings {
	f := &findings{class: ClassSeriesDup}

	byKey, keyOrder := groupSeries(ix.cat.Series, seriesKey)
	tight := map[string]bool{}
	for _, key := range keyOrder {
		group := byKey[key]
		if len(group) < 2 {
			continue
		}
		tight[key] = true
		f.add(seriesDupFinding(ix, serDupName, key, group,
			"review as one series: fold the members onto the canonical name, then delete the empty one"))
	}

	// The saga key is strictly looser, so it only ever adds groups the tight key
	// did not already report - and it proposes nothing, because a trailing "Saga"
	// is usually part of the name.
	bySaga, sagaOrder := groupSeries(ix.cat.Series, seriesSagaKey)
	for _, key := range sagaOrder {
		group := bySaga[key]
		if len(group) < 2 {
			continue
		}
		if allShareTightKey(group) {
			continue // already reported under serDupName
		}
		f.add(seriesDupFinding(ix, serDupSaga, key, group,
			`review by hand: the names differ only by a trailing "Saga", which is part of a real series name at least as often as it is catalogue decoration - no merge is proposed`))
	}
	return f
}

// allShareTightKey reports whether every member of a saga-key group already meets
// under the tight key too.
func allShareTightKey(group []*model.Series) bool {
	first := seriesKey(group[0].Name)
	for _, s := range group[1:] {
		if seriesKey(s.Name) != first {
			return false
		}
	}
	return true
}

func groupSeries(all []*model.Series, key func(string) string) (map[string][]*model.Series, []string) {
	by := map[string][]*model.Series{}
	var order []string
	for _, s := range all {
		k := key(s.Name)
		if k == "" {
			continue
		}
		if _, seen := by[k]; !seen {
			order = append(order, k)
		}
		by[k] = append(by[k], s)
	}
	sort.Strings(order)
	for k := range by {
		g := by[k]
		sort.Slice(g, func(i, j int) bool { return g[i].ID < g[j].ID })
		by[k] = g
	}
	return by, order
}

func seriesDupFinding(ix *index, sub, key string, group []*model.Series, action string) Finding {
	fd := Finding{Subclass: sub, Key: key, Action: action}
	var names []string
	for _, s := range group {
		fd.Series = append(fd.Series, ix.seriesRef(s))
		names = append(names, s.Name)
	}
	// The canonical proposal is the series holding the most works, then the
	// lowest id: a fold moves the fewest memberships that way.
	best := group[0]
	for _, s := range group[1:] {
		if len(s.Works) > len(best.Works) || (len(s.Works) == len(best.Works) && s.ID < best.ID) {
			best = s
		}
	}
	fd.Canonical = best.ID
	fd.Notes = []string{"spellings: " + truncateList(sortedUnique(names), 8)}
	return fd
}

// detectSeriesParen reports series names carrying a parenthetical decoration. It
// proposes nothing: "Vorkosigan Saga (chronological)" beside "Vorkosigan Saga" is
// very likely a DELIBERATE second ordering of one series, which the data model has
// no other way to express, so the only honest output is "a human should look".
func detectSeriesParen(ix *index) *findings {
	f := &findings{class: ClassSeriesParen}
	// The undecorated keys, so a decorated name can say whether a plain sibling
	// exists.
	plain := map[string][]string{}
	for _, s := range ix.cat.Series {
		if strings.ContainsAny(s.Name, "([") {
			continue
		}
		k := seriesKey(s.Name)
		plain[k] = append(plain[k], s.ID)
	}
	for _, s := range ix.cat.Series {
		if !strings.ContainsAny(s.Name, "([") {
			continue
		}
		siblings := sortedUnique(plain[seriesKey(s.Name)])
		fd := Finding{
			Key:    s.ID,
			Series: []SeriesRef{ix.seriesRef(s)},
			Field:  "name",
			Have:   s.Name,
			Want:   tidyTitle(stripParenGroups(s.Name)),
		}
		if len(siblings) > 0 {
			fd.Subclass = serParenPair
			fd.Action = "review by hand: the parenthetical may be a deliberate alternative ordering of the sibling series - never merged automatically"
			fd.Notes = []string{"undecorated sibling: " + truncateList(siblings, 8)}
			for _, id := range siblings {
				if sib := ix.seriesByID[id]; sib != nil {
					fd.Series = append(fd.Series, ix.seriesRef(sib))
				}
			}
		} else {
			fd.Subclass = serParenSolo
			fd.Action = "review by hand: the parenthetical is decoration with nothing to fold onto, so the name itself may want cleaning"
		}
		f.add(fd)
	}
	return f
}
