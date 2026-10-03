package audit

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// vetolang.go holds the merge veto a record's OWN LANGUAGE STATEMENT earns.
//
// identityClusters already keeps two STATED languages out of one cluster, and that
// is enough while the language tags are right. It is not enough when a tag is wrong:
// "The Gambler [Persian Edition]" and "White Nights (Persian Edition)" are both
// tagged `en` in the tree, so they clustered with Dostoevsky's English editions of the
// same books and the merge was MECHANICAL - and a merge deletes the record, so the
// one Persian translation of each book in the catalogue would have been folded away.
// Their narrators read in Persian; the title states it; only the tag says English.

// vetoEditionLanguageDiffers: a member STATES an own-language edition, and another
// member's language - its own statement, or else its tag - is known and a different
// primary subtag. A different language is a translation, and a translation is a
// different work (T-LINK links it; nothing merges it).
//
// It fires only on a STATEMENT, so two members whose tags merely differ stay
// identityClusters' business, and a member of unknown language is never judged.
// "X [English Edition]" beside an English "X" agrees and merges as before.
//
// It is deliberately AUDIT-ONLY. The writer guards and metacheck's census
// (check.WorkIdentity) still judge the language TAGS: a writer's refusal is
// recoverable where a merge is not, and moving "a stated edition language beats the
// tag" into the identity predicate would change census and writer outcomes - a
// separate change with its own measurement.
//
// Measured over the 282,052-work tree at c7ef08466 it fires on exactly two W-DUP
// clusters, the two Persian ones above, and moves both out of the mechanical set. It
// reads "Persian Edition" because titlerule's edition vocabulary learned the word in
// the same change (editionOnlyWords); T-LINK, which reads that vocabulary too, was
// byte-identical.
func vetoEditionLanguageDiffers(members []dupMember) (string, bool) {
	// Each member's EFFECTIVE language, read once rather than per pair: the language
	// its own title or subtitle states through an own-language edition decoration
	// (titlerule.EditionLanguage, the vocabulary T-LINK reads), else its tag's primary
	// subtag, else "" - unknown, which is never judged. The STATEMENT beats the tag
	// because it is the record speaking about itself; the tag is somebody's
	// classification and is the thing found wrong here.
	stated := make([]bool, len(members))
	effective := make([]string, len(members))
	anyStated := false
	for i, m := range members {
		if l, ok := titlerule.EditionLanguage(m.work.Title, m.work.Subtitle); ok {
			stated[i], effective[i], anyStated = true, l, true
		} else {
			effective[i] = model.PrimarySubtag(m.work.Language)
		}
	}
	if !anyStated {
		return "", false
	}
	for i, m := range members {
		if !stated[i] {
			continue
		}
		lang := effective[i]
		for j, other := range members {
			if other.work.ID == m.work.ID {
				continue
			}
			olang := effective[j]
			if languagesCompatible(lang, olang) {
				continue
			}
			return fmt.Sprintf("%s states a %s edition in its own title and %s is %s: a translation is a different work "+
				"(a mis-tagged language does not make it the same book)", m.work.ID, lang, other.work.ID, olang), true
		}
	}
	return "", false
}
