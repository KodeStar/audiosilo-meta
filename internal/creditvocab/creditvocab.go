// Package creditvocab holds the two tables of credits that name no individual,
// the data both internal/importer (which folds and refuses on them) and
// pkg/model's IsGenericCredit (which the server's matcher asks) read. It is a
// leaf with no imports, so pkg/model can reach it without a cycle and the
// tables stay out of the public API. The rules for what belongs in each table
// live beside their importer users: collective.go's header for Collective,
// placeholderCreditNames' doc in libex.go for Placeholders.
package creditvocab

// Collective maps the folded form (internal/importer's foldCredit: lowercased,
// diacritics removed, whitespace collapsed) of every measured variant onto the
// canonical credit NAME it states. Folding the diacritics is why the Spanish
// "anónimo" and the Italian "anonimo" share one key - they are the same string
// once the accent is gone, and they state the same thing anyway.
//
// The canonicals are keys of their own, mapping to themselves. That is not
// redundancy: it is what makes a source's "FULL CAST" or "various" mint the
// record under the canonical spelling rather than under whichever casing arrived
// first, and it keeps each bucket's canonical visible at the head of its group.
//
// Counts are credits over the full dump. Read-only: callers must not write to it.
var Collective = map[string]string{
	// ------------------------------------------------------------------
	// Bucket 1: collective statements -> Full Cast.
	// An ensemble performed this. The credit's own words say it is more than
	// one person, so no individual record could ever be right.
	"full cast":               "Full Cast", // 5,657 - the canonical
	"a full cast":             "Full Cast", // 65
	"ensemble cast":           "Full Cast", // 20
	"cast":                    "Full Cast", // 11 - measured: ensemble theatre recordings
	"full cast dramatization": "Full Cast", // 7
	"elenco":                  "Full Cast", // 7 - Italian/Spanish/Portuguese "cast"
	"distribution complete":   "Full Cast", // 4 - French "distribution complète"
	"full supporting cast":    "Full Cast", // 3
	"full cast ensemble":      "Full Cast", // 2
	"an ensemble cast":        "Full Cast", // 2
	"additional cast":         "Full Cast", // 1
	"an all-star cast":        "Full Cast", // 1
	"full cast recording":     "Full Cast", // 1
	"assorted cast":           "Full Cast", // 1

	// ------------------------------------------------------------------
	// Bucket 1: collective statements -> Various.
	// Several contributors, none of them named. The anthology convention.
	"various":             "Various", // 2,064 - the canonical
	"div.":                "Various", // 2,096 - German/Scandinavian "diverse", abbreviated; see collective.go's header
	"div":                 "Various", // 5 - the same abbreviation missing its final period
	"various authors":     "Various", // 228
	"autori vari":         "Various", // 196 - Italian
	"diverse":             "Various", // 191 - German
	"divers auteurs":      "Various", // 98 - French
	"various narrators":   "Various", // 96
	"varios narradores":   "Various", // 89 - Spanish
	"divers narrateurs":   "Various", // 64 - French
	"diverse sprecher":    "Various", // 17 - German "various speakers", the exact twin of the four above
	"narratori vari":      "Various", // 56 - Italian
	"various artists":     "Various", // 48
	"varios autores":      "Various", // 38 - Spanish
	"diversos narradores": "Various", // 18 - Spanish
	"various performers":  "Various", // 8
	"diverse diverse":     "Various", // 3

	// ------------------------------------------------------------------
	// Bucket 1: collective statements -> Anonymous.
	// Anonymity as authorship: the party is deliberately unnamed. Distinct from
	// Unknown below, which is a statement about knowledge rather than intent.
	"anonymous":   "Anonymous", // 2,279 - the canonical
	"anonimo":     "Anonymous", // 79 - Spanish "anónimo" (53) + Italian "anonimo" (26), one folded key
	"anonymus":    "Anonymous", // 16 - the Latin/German spelling; see collective.go's header
	"anonyme":     "Anonymous", // 14 - French
	"i anonymous": "Anonymous", // 2

	// ------------------------------------------------------------------
	// Bucket 1: collective statements -> Uncredited.
	// The production credits nobody, on purpose. No twins measured; the
	// canonical is here so the bucket is stated rather than implied.
	"uncredited": "Uncredited", // 2,188 - the canonical

	// ------------------------------------------------------------------
	// Bucket 2: unknown-identity statements -> Unknown.
	// Nobody knows who this was. "N.N." is nomen nescio, the publishing
	// convention, and the dump spells it three ways (766 credits between them).
	"unknown":               "Unknown", // 77 - the canonical
	"n.n.":                  "Unknown", // 700
	"n. n.":                 "Unknown", // 62
	"n.n":                   "Unknown", // 4 - the same abbreviation missing its final period
	"auteur inconnu":        "Unknown", // 164 - French
	"narratore sconosciuto": "Unknown", // 55 - Italian
	"autore sconosciuto":    "Unknown", // 14 - Italian
	"autor desconocido":     "Unknown", // 8 - Spanish
	"narrateur inconnu":     "Unknown", // 7 - French
}

// Placeholders are the whole-name booking placeholders (in foldCredit form) the
// libex import refuses a row for: the TBD family, an administrative state rather
// than a credit. Counts are books over the full dump. Read-only: callers must
// not write to it.
var Placeholders = map[string]bool{
	// The TBD family: an administrative state, not a credit.
	"to be announced": true, // 251
	"to be confirmed": true, // 55
	"tbd":             true, // 20
	"tba":             true, // 10
	"tbc":             true, // 4
	"n/a":             true, // 1
	// A retail export's empty field printed into the credit: three Caedmon
	// Shakespeare recordings, each with its named lead beside it.
	"(null) cast": true, // 3, in the catalogue rather than the dump
}
