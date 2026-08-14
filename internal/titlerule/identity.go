package titlerule

import "strings"

// identity.go holds the NORMALIZED WORK IDENTITY key: the one answer to "do these
// two records name the same book, as far as their titles can say".
//
// It is the rule three separate defences read, which is the whole reason it is one
// function here rather than a formula spelled at each of them:
//
//   - internal/issueform's intake gate, so a submitted title that normalizes onto a
//     catalogued work is a duplicate verdict naming that work instead of a new
//     record;
//   - internal/importer's create guard, so a bulk row whose identity a work already
//     holds is skipped and reported instead of minting a sibling;
//   - pkg/check's advisory census, so the collisions already in the tree are counted
//     in every metacheck run and a repair wave's progress is a number.
//
// The three would otherwise be three thresholds, and a defect class that one of
// them called a duplicate while another minted it is exactly how the 4,596
// near-duplicate clusters accumulated in the first place: the bulk importer's own
// identity (a title SLUG plus an author set) cannot see through a retailer's
// decoration, so "Hammered" and "Hammered: The Iron Druid Chronicles, Book 3"
// are two works to it and one book to a reader.

// IdentityTitleKey is a work title's normalized identity: the title cleaned of
// retailer decoration (Clean) and reduced to its comparison form (CompareKey), so
// case, spacing, punctuation, diacritics, a leading article, edition markers,
// volume markers, genre-subtitle fluff and the series name are all NOT identity.
//
// series is the series name the title is read against, or "" - see SeriesNameFor
// for which of a work's memberships that is. It returns "" for a title that
// normalizes to nothing, which every caller reads as "no key, compare nothing":
// two titles that reduce to nothing are not thereby the same book.
//
// It is deliberately COARSER than the importer's own work identity and must stay
// that way. The importer's identity decides where a record is STORED (its slug) and
// may never widen without moving records; this decides whether to REFUSE a new
// record, which is reversible - a refused row is re-importable, a wrong merge is
// not. So a collision here is a duplicate CANDIDATE, and every caller pairs it with
// the author-set and language rules before it acts.
func IdentityTitleKey(title, series string) string {
	return CompareKey(Clean(title, series))
}

// SeriesNameFor picks the series name a work's title is read against, out of the
// names of the series it belongs to: the one the title actually SPELLS OUT if there
// is one, else the first of the list.
//
// Both halves are load-bearing. A work in two series must clean the same way
// whoever asks (so the choice cannot depend on map order - the caller passes the
// names in a deterministic order, by series id), and a title that names one of its
// series should be cleaned against THAT one rather than against a sibling series
// whose name it does not carry.
//
// It takes NAMES rather than a catalogue on purpose: this package never reads a
// tree (see the package doc), and every caller already has the memberships in hand.
func SeriesNameFor(title string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	lower := strings.ToLower(title)
	for _, name := range names {
		if _, ok := SeriesRefIn(lower, name); ok {
			return name
		}
	}
	return names[0]
}

// StatedVolume is the volume number a title spells out against a series name, and
// whether it states one at all. It is BareSeq under the name the duplicate gates
// ask it by: "does this title say which volume it is", the question that separates
// two records of one book from two volumes of one serial.
func StatedVolume(title, series string) (float64, bool) { return BareSeq(title, series) }

// SameStatedVolume reports whether two titles state DIFFERENT volume numbers
// against their series names - the one piece of evidence that turns a normalized
// identity collision from a duplicate into a pair of siblings.
//
// A title stating no number is not a disagreement: "Hammered" beside "Hammered: The
// Iron Druid Chronicles, Book 3" is precisely the duplicate the gates exist to
// catch, and only two titles that BOTH state a number, differently, are volumes.
func SameStatedVolume(titleA, seriesA, titleB, seriesB string) bool {
	a, okA := StatedVolume(titleA, seriesA)
	b, okB := StatedVolume(titleB, seriesB)
	return !okA || !okB || a == b
}
