package importer

// ---------------------------------------------------------------------------
// Collective and placeholder credits
//
// A credit that names no individual is one of exactly four things, and which one
// is decided by what the credit STATES, never by the language it states it in:
//
//  1. a COLLECTIVE statement - "a full cast performed this", "several
//     contributors wrote this". That is a fact about the production, and it is
//     the same fact in English, French, German, Italian and Spanish.
//  2. an UNKNOWN-IDENTITY statement - "who wrote this is not known": the
//     bibliographic "N.N." (nomen nescio) and its translations.
//  3. a BOOKING PLACEHOLDER - the retailer's "to be announced", standing in for
//     a cast that had not been booked when the listing went up.
//  4. a BRANDED ENSEMBLE - a named troupe (The Colonial Radio Players, Museum
//     Audiobooks cast, Das Schauspiel-Ensemble vom Landestheater Detmold,
//     Linguistics Team). A real group with a real name.
//
// Only the third names nobody at all, and it is the only one still REFUSED
// (placeholderCreditNames, libex.go).
//
// The two tables are NOT interchangeable, and a maintainer adding an entry has
// to know which layer they are adding it to: placeholderCreditNames compares the
// RAW credit name at the libex parse layer, before any cleaning, and refuses the
// whole row; this table compares the CLEANED name at the tail of the cleaning
// fixpoint, in every importer. So "To Be Announced - narrator" is not refused
// today - the qualifier hides the placeholder from a raw compare. That is a
// pre-existing gap rather than something this change introduced, and closing it
// means giving the refusal a look at the cleaned name too (the shape
// firstAICredit already uses), not moving entries between the tables.
//
// The first two name a real party that
// simply is not one identifiable human, and a work needs at least one author, so
// the books carrying them need a record to point at. What they must not need is
// one record per language: the catalogue had minted "autori vari", "divers
// auteurs", "narratore sconosciuto", "N. N." and a dozen more, each a separate
// person of record stating the same thing as an English canonical already on
// disk. This table folds every measured spelling of one statement onto ONE
// canonical record - full-cast, various, anonymous, uncredited, unknown - so the
// statement has one address instead of one address per translator.
//
// The fourth is a person-side record like any other and this table never touches
// it. That is what the matching rule is: the comparison is the WHOLE cleaned
// credit name, exactly, never a pattern and never a substring. "Museum
// Audiobooks cast", "the Full Cast Family" and "ZacharyWebber & full cast" all
// contain the words a pattern rule would fire on, and every one of them is a
// branded ensemble that keeps its own record. So do the people: P.C. Cast,
// Kristin Cast, Jayne Castle, Rainer Castor, Devon Sorvari.
//
// The vocabulary is evidence-driven in the same way as the AI, junk and
// placeholder lists. The count on each entry is credits over the full 1.13M-book
// libex dump, counted across BOTH credit lists - an anthology is as likely to
// state "Various" as its author as a dramatization is to state "Full Cast" as
// its narrator, and the dump does both.
//
// Deliberately NOT folded, each a judgment call worth restating:
//
//   - "div." (2,096 credits), the German bibliographic abbreviation. It is the
//     largest neighbour of this list and it very probably belongs in it, but it
//     is an abbreviation of "diverse" rather than the statement itself, and a
//     2,096-credit fold is a maintainer decision of its own rather than a
//     rounding of this one. Adding it later is one line here plus one data
//     migration.
//   - "anonymus" (16), "n.n. n.n." (7), "unbekannt" (1), "desconocido" (1), and
//     the QUALIFIED forms ("unknown author" 4, "anonymous guest" 5). The first
//     four are further spellings of statements this list already holds and were
//     not in the measured proposal; the qualified forms are a different claim -
//     "an anonymous guest" is a person the publisher declined to name on one
//     book, not the anonymous-authorship convention. Both groups are one-line
//     additions if a maintainer wants them.
//   - the German "anonym", which the approved proposal listed but the dump does
//     not carry at all (0 credits). Every entry below is a form the dump really
//     contains, and a zero-count entry would invert exactly the evidence bar the
//     neighbouring vocabularies state - the rule earns its lines by measurement,
//     not by completing a paradigm.
//
// Which CANONICAL a variant folds onto is decided by the statement, never by the
// variant's language. That is why the German "diverse Sprecher" sits with
// Various rather than with Full Cast: it is the word-for-word twin of "various
// narrators", "varios narradores", "narratori vari" and "divers narrateurs", and
// letting one of five translations land somewhere else is the very failure this
// table exists to end.
//
// ORDERING CONSTRAINT, until the twin-migration data PR lands. This change folds
// new credits onto the canonicals; it migrates nothing, so the catalogue still
// holds the variant records the folding replaced (measured on the live tree:
// ~198 work-author references and ~72 narrator references across n-n,
// autori-vari, divers-auteurs, diverse and friends). A work recorded under a
// variant AUTHOR is now addressed by a different author set than an incoming row
// for the same book states, so any pass that CREATES - the plain create path,
// --recordings-only, and the user-library importers - can FORK it into a second
// work rather than matching the one on disk. --enrich is unaffected: it matches
// by ASIN and creates nothing. So the migration must land BEFORE the next import
// wave, not merely eventually.
//
// The canonical NAME each variant maps onto is the spelling a NEW record would
// be minted under. Three of the five canonicals already exist in the catalogue
// spelled lowercase ("anonymous", "uncredited", "unknown"); a record that exists
// keeps its own name, so the canonical spelling here only ever decides how an
// absent one is created. Every canonical name slugs to its canonical id under
// model.PersonSlug, which is what makes this a fold onto an existing address
// rather than a rename (pinned by TestCollectiveCanonicalsSlugToTheirIDs).

// collectiveCredits maps the folded form (foldCredit: lowercased, diacritics
// removed, whitespace collapsed) of every measured variant onto the canonical
// credit NAME it states. Folding the diacritics is why the Spanish "anónimo" and
// the Italian "anonimo" share one key - they are the same string once the accent
// is gone, and they state the same thing anyway.
//
// The canonicals are keys of their own, mapping to themselves. That is not
// redundancy: it is what makes a source's "FULL CAST" or "various" mint the
// record under the canonical spelling rather than under whichever casing arrived
// first, and it keeps each bucket's canonical visible at the head of its group.
var collectiveCredits = map[string]string{
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

// canonicalCreditName folds a CLEANED credit name onto the canonical record for
// the statement it makes, and returns every other name exactly as it was given.
//
// It is applied once, at the tail of creditWithRolesSided, which is the single
// point every cleaned credit name in the importer passes through: the import
// pipeline (sourceCredits), the batch credit census (creditCensusesOf) and the
// initials pre-pass (decideInitialsOf) all read their names through it, so
// authors, narrators and role credits are folded by one call in one place.
//
// Folding THERE rather than at getOrCreatePerson is what keeps a variant from
// counting as evidence of its own spelling: the initials census never sees
// "N. N.", so it never decides a surviving spelling for a group no record will
// be created in, and the credit census records the canonical slug the import is
// actually going to write.
//
// It runs AFTER cleaning, never before, so a variant that arrives with a role
// qualifier or doubled ("Autori Vari - traduttore", "Full Cast Full Cast") is
// folded on the name the cleaning rules produced. The roles that qualifier
// stated are untouched: what the fold changes is WHO is credited, not what the
// source said they did.
func canonicalCreditName(name string) string {
	if canonical, ok := collectiveCredits[foldCredit(name)]; ok {
		return canonical
	}
	return name
}
