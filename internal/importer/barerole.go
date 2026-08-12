package importer

import (
	"strings"
	"unicode"
)

// barerole.go strips a role qualifier a source welded onto a name with NO
// separator at all: "Andrew Lang Editor" (B002V8L32I), "Kate Reading Narrator",
// "Martin McLaughlin translator". stripRoleQualifier cannot see these - it needs
// the dash - and without a rule they mint andrew-lang-editor beside the editor's
// own record (issue #1800).
//
// It is the census-backed sibling of studiotail.go's tier 3, with a ROLE tail in
// place of a studio tail, and it needs that tier's evidence for the same reason:
// the shape carries none of its own. Measured over the full 1.13M-book dump, the
// naive form - "a 3+ token name whose final token is a listed role word" -
// matches 55 names, and 41 of them are NOT a person with a qualifier welded on:
//
//	entity names whose own name ends in the word  "Luz da Serra Editora", "On
//	    Line Editora", "Public Play Editora" (Portuguese publishing houses),
//	    "Amazon Music", "Meditative Minds Music", "Luna Bright Illustrations",
//	    "New Living Translation", "Los Andes Grupo Editor"
//	surnames that ARE the word  "Augustin Eugène Scribe" (the French dramatist),
//	    "A. P. Scribe", "J. U. Scribe"
//	stage names built ON the word  "Cricket the Narrator", "Erikka the Narrator",
//	    "KM the Narrator", "Emeka the Director", "Mercury the Scribe", "West End
//	    Producer"
//	collective statements  "Full Cast Dramatization"
//
// Three things separate those from the real targets, and all three are needed:
//
//	the vocabulary  bareRoleTails is its OWN narrow list, not roleQualifiers.
//	    Every word above that turned out to be an entity word, a surname or the
//	    tail of a stage name is excluded from it - which is why "scribe",
//	    "editora", "music", "translation", "producer", "director",
//	    "illustrations", "compilation" and "dramatization" strip after a DASH
//	    (mapping.go) and never on their own.
//	the census  the head must be independently a credit somewhere (the anySide
//	    universe, as studiotail.go's tier 3 uses: a person credited on either
//	    side is a person, and the two names this gains - "Alexander Scourby
//	    Narrator" credited as an author, "David Coward Translator" - are both
//	    correct merges).
//	the head guards  2+ tokens (minPersonTokens), a final token that is neither
//	    an entity word nor a boundary token (personHalfOK), not an article (the
//	    stage-name shape, 6 dump names), and word-like rather than a stray
//	    separator.
//
// With all three the rule fires on 11 dump names and every one is a person
// carrying a welded qualifier: Andrew Lang Editor, Arthur B. Reeve Editor,
// Murray Stein editor, Basil Hall Chamberlain translator, Frank Wynne
// Translator, Lionel Giles translator, Martin McLaughlin translator, David
// Coward Translator, Kate Reading Narrator, Alexander Scourby Narrator, James
// Martin SJ Foreword. Zero false positives.
//
// Like every census-backed rule it is silent for a caller with no catalogue in
// hand (CleanCreditName, SplitNames over an ID3 tag, a typed issue-form name):
// the shape alone is not evidence, and the worst an unmeasured population can do
// here is fail to strip.

// bareRoleTails is the closed vocabulary of FINAL tokens this rule reads. It is
// a SET, not a second role table: what each word states is looked up in
// roleQualifiers, so the two spellings of one qualifier (welded and dashed)
// can never state different things. TestBareRoleVocabulary pins the subset
// relationship and the foldCredit form of the keys.
//
// Adding a word means measuring it the way the file header measures these four:
// every dump credit name whose final token is that word, with the entity names,
// the surnames and the stage names counted rather than assumed away.
var bareRoleTails = map[string]bool{
	"editor":     true, // 5 dump names, 4 of them real targets ("Los Andes Grupo Editor" is the entity, and its head is unattested)
	"translator": true, // 7 names, every one a translator credited as an author
	"narrator":   true, // 8 names, 2 of them real targets (6 are the "<X> the Narrator" stage-name shape)
	"foreword":   true, // 1 name, "James Martin SJ Foreword"
}

// bareRoleHeadStop are the words a head may not END on, beyond the boundary
// tokens every studio tier already refuses. An article there means the string is
// a stage name built on the role word ("Cricket the Narrator", "Eliakim The
// Scribe") rather than a name with a qualifier after it - 6 of the dump's 55
// candidate names, and the only systematic shape the census does not already
// refuse.
var bareRoleHeadStop = map[string]bool{"the": true, "a": true, "an": true}

// stripBareRoleTail removes a separator-less trailing role qualifier and reports
// the schema roles it stated. It returns the name unchanged, and no roles, when
// any of the three evidence gates fails.
func stripBareRoleTail(name string, seen creditSeenFunc) (string, []string) {
	if seen == nil {
		return name, nil
	}
	tokens := strings.Fields(name)
	if len(tokens) <= minPersonTokens {
		return name, nil
	}
	if !bareRoleTails[foldCredit(tokens[len(tokens)-1])] {
		return name, nil
	}
	head := tokens[:len(tokens)-1]
	last := foldCredit(head[len(head)-1])
	if !personHalfOK(head, minPersonTokens) || bareRoleHeadStop[last] || !wordLike(last) {
		return name, nil
	}
	headText := strings.Join(head, " ")
	if !seen.seen(headText) {
		return name, nil
	}
	return headText, roleQualifiers[foldCredit(tokens[len(tokens)-1])]
}

// wordLike reports whether a folded token carries a letter or a digit. A head
// ending in a stray separator ("Jane Doe |") is a cut in the wrong place: the
// census would still recognize it (Slugify drops the punctuation) and the name
// this rule returned would keep the separator.
func wordLike(folded string) bool {
	for _, r := range folded {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
