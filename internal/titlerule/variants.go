package titlerule

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// variants.go holds the one SPELLING-VARIANT rule: a closed table of British and
// American spellings of one word, so "The Armour of Light" and "The Armor of Light" -
// Ken Follett's novel under its UK and US titles, one John Lee production - are one
// comparison key to a reader that asks for it.
//
// It is NOT part of the comparison key (CompareKey) or the identity key
// (IdentityTitleKey), and that is deliberate: those keys are what pkg/check's census and
// the two writers' duplicate guards refuse a CREATE on, and a spelling table is a
// widening nobody has measured there. internal/audit's W-DUP reads it as an EXTRA
// clustering key only, where every cluster it adds is still judged by the merge vetoes.
//
// THE TABLE IS CLOSED, and every entry is a whole word in its slug form, mapped onto
// the American spelling. There are no suffix rules ("-our" -> "-or", "-ise" -> "-ize"):
// "hour", "four", "your", "rise", "wise" and "promise" are why. Inflections are listed
// one by one for the same reason. Left out on purpose because the variant is also an
// unrelated common word: metre/meter, tyre/tire, cheque/check, storey/story,
// draught/draft, kerb/curb, axe/ax, gaol/jail (a proper name in "Reading Gaol").
//
// Measured over the 282k-work tree when it landed: W-DUP gained 24 clusters, 23 of them
// mechanical and every one hand-reviewed as the UK and US edition of one book (Sharpe's
// Honour, The Labours of Hercules, The Sisterhood of the Travelling Pants, Valour, ...);
// one mechanical merge went advisory because its cluster grew to hold a 114-minute
// adaptation of Journey to the Centre of the Earth.
var spellingVariants = map[string]string{
	"aluminium": "aluminum",
	"analogue":  "analog",
	"ageing":    "aging",
	"armour":    "armor", "armoured": "armored", "armoury": "armory",
	"behaviour": "behavior",
	"calibre":   "caliber",
	"catalogue": "catalog",
	"centre":    "center", "centres": "centers",
	"clamour": "clamor",
	"colour":  "color", "colours": "colors", "coloured": "colored",
	"defence":   "defense",
	"endeavour": "endeavor",
	"favour":    "favor", "favours": "favors", "favourite": "favorite",
	"fibre":   "fiber",
	"flavour": "flavor",
	"grey":    "gray", "greys": "grays",
	"harbour": "harbor",
	"honour":  "honor", "honours": "honors", "honoured": "honored", "honourable": "honorable",
	"humour":    "humor",
	"jewellery": "jewelry",
	"labour":    "labor", "labours": "labors",
	"licence":   "license",
	"manoeuvre": "maneuver", "manoeuvres": "maneuvers",
	"mould":     "mold",
	"moustache": "mustache",
	"neighbour": "neighbor", "neighbours": "neighbors", "neighbourhood": "neighborhood",
	"offence":   "offense",
	"parlour":   "parlor",
	"plough":    "plow",
	"pretence":  "pretense",
	"programme": "program",
	"pyjamas":   "pajamas",
	"rancour":   "rancor",
	"rumour":    "rumor", "rumours": "rumors",
	"sabre":   "saber",
	"saviour": "savior",
	"sceptic": "skeptic", "sceptical": "skeptical",
	"sceptre":   "scepter",
	"sombre":    "somber",
	"spectre":   "specter",
	"splendour": "splendor",
	"theatre":   "theater",
	"traveller": "traveler", "travellers": "travelers", "travelling": "traveling",
	"valour": "valor",
	"vapour": "vapor", "vapours": "vapors",
}

// SpellingVariantKey is CompareKey(cleaned) with every word the closed table holds
// spelled the American way - or "" when no word of the title is in the table, so a
// caller adds a key only for the titles the rule actually changes.
func SpellingVariantKey(cleaned string) string {
	key, changed := spellingKey(cleaned)
	if !changed {
		return ""
	}
	return key
}

// spellingKey is CompareKey spelled through the table, and whether any word changed.
// It folds exactly as CompareKey does (model.Slugify of the article-less text, hyphens
// removed), only word by word, so a title with no table word keys identically.
func spellingKey(cleaned string) (string, bool) {
	words := strings.Split(model.Slugify(dropLeadingArticle(cleaned)), "-")
	changed := false
	for i, w := range words {
		if us, ok := spellingVariants[w]; ok {
			words[i], changed = us, true
		}
	}
	return strings.Join(words, ""), changed
}

// SameVariantTitleUnderCommonSeries is SameTitleUnderCommonSeries for a pair that met on
// a SpellingVariantKey: the two titles, each cleaned against the same series name, agree
// once both are spelled through the table. It is the soundness condition the variant key
// needs for the same reason the identity key does - the meeting must not depend on each
// side having shed a different series name - asked in the variant key's own terms,
// since under the plain key "armour" and "armor" never agree at all.
func SameVariantTitleUnderCommonSeries(titleA, seriesA, titleB, seriesB string) bool {
	for _, s := range [2]string{seriesA, seriesB} {
		a, _ := spellingKey(Clean(titleA, s))
		b, _ := spellingKey(Clean(titleB, s))
		if a != "" && a == b {
			return true
		}
	}
	return false
}
