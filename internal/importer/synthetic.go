package importer

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// ---------------------------------------------------------------------------
// Synthetic narration: one record, not one per persona
//
// A book a person actually owns is a book this catalogue wants, and some of
// them are AI-narrated. The rule that used to refuse every such row everywhere
// was answering a different question: the problem was never the BOOK, it was
// that admitting the credit minted a PERSON record for a text-to-speech engine
// (the full-dump recordings-only run really did create eleven "ai-voice-*"
// people before the vocabulary existed).
//
// So the narration credit folds instead of refusing. Every narrator credit the
// AI-voice vocabulary matches - the whole-name labels in any language, the "AI
// Voice <persona>" prefix family, the "<narrator>'s voice replica" suffix
// family and the trailing parenthetical markers - names ONE canonical record,
// "Virtual Voice", kind `synthetic`. It is exactly the fold collective.go makes
// for "full cast" and "autori vari", for exactly the same reason: the statement
// is "a synthetic voice read this", and a statement deserves one address rather
// than one address per spelling.
//
// The persona and the cloned narrator's name are DELIBERATELY NOT PRESERVED.
// They are free text - 236 distinct "AI Voice <persona>" spellings and 81
// distinct "<name>'s voice replica" spellings in the 1.13M-row dump - so
// keeping them would put a record per TTS persona in the people family, which
// is the failure the refusal existed to prevent. "AI Voice Nina" and "Steve
// Stewart's Voice Replica" are both the same fact about the production.
//
// WHERE THE FOLD APPLIES, and where it does not:
//
//   - the three USER-LIBRARY sources (openaudible, libation, audiosilo-books -
//     pkg/model's trust tiers rank them TierUserLibrary) and the intake bot's
//     issue forms, which are a hand submission and the same tier. These are
//     somebody's own books.
//   - NOT the libex bulk mirror, which keeps refusing exactly as it did, at its
//     own parse layer and in scripts/libex-export-rows.sql. 145,558 rows of the
//     dump credit an AI voice; seeding them would make Virtual Voice the
//     single most prolific narrator in the catalogue, off nobody's attestation.
//     That filter and that SQL are unchanged.
//   - NOT the AUTHOR side, anywhere. An AI credited as the author still refuses
//     the whole row (refuseAIBooks, and firstAICredit's author arm): the
//     decision the maintainer made is about NARRATION, and "Sparky From
//     ChatGPT" is a generative system credited with writing a book, which is a
//     claim about authorship rather than a production fact.
//   - NOT a generative SYSTEM credited as the narrator either ("ChatGPT" in the
//     narrated_by column). That is the dump's untidiness about which column its
//     non-people land in, not a narration credit, and folding a language model
//     onto the voice record would state something the row never said. It keeps
//     refusing, on both sides.
//
// THE SLUG IS RESERVED. Nothing else can mint `virtual-voice` for a real
// credit, and it is the AI vocabulary itself that reserves it rather than a
// second rule: "virtual voice" is an aiNarratorNames entry, so a credit spelled
// that way is either folded here (narrator side, user library) or refuses its
// row (author side, or any libex row). The issue forms close the same loop -
// their narrator credits fold through SyntheticNarratorName and an AI author
// credit is refused - so no door writes that address for anybody real. This is
// collective.go's canonical-reservation shape: the canonical is a key of its
// own vocabulary, so every spelling of the statement lands on it.

// syntheticVoiceName is the NAME the canonical record is minted under, and
// syntheticVoiceSlug is the id it lands at. They are kept in step by
// TestSyntheticCanonicalSlugsToItsID, the twin of the collective fold's guard:
// the fold is only a fold onto ONE address if the name slugs to that address.
const (
	syntheticVoiceName = "Virtual Voice"
	syntheticVoiceSlug = "virtual-voice"
)

// SyntheticNarratorName folds a NARRATOR credit onto the canonical synthetic
// record, reporting whether it did. It is the exported door for the intake
// forms (internal/issueform), which compose a submission one typed name at a
// time and have no census to clean against; the bulk importer folds inside the
// credit pipeline instead (creditWithRolesSided), so both roads reach one
// record from one vocabulary.
//
// Author credits must never be passed here. The caller decides the side; this
// function only knows the vocabulary.
func SyntheticNarratorName(name string) (string, bool) {
	if !namesSyntheticVoice(name) {
		return name, false
	}
	return syntheticVoiceName, true
}

// namesSyntheticVoice reports whether a credit name is one of the AI-VOICE
// shapes, judged on the name as the source spelled it AND on the cleaned form -
// the same pair firstAICredit judges, because a role qualifier can hide a
// trailing marker ("Elise (AI) - narrator") and the cleaned name is the one
// that would become the person record.
//
// Deliberately NOT namesAISystem: see the header. A synthetic voice is a
// production fact; a language model in a credit column is not.
func namesSyntheticVoice(name string) bool {
	return syntheticVoiceCredit(name, CleanCreditName(name))
}

// syntheticVoiceCredit is namesSyntheticVoice for a caller that has ALREADY
// cleaned the name - the credit pipeline, whose cleaning consults the run's
// census and so can differ (very slightly) from CleanCreditName's census-free
// pass. Both arms are asked either way, so the gate that admits the row and the
// fold that rewrites the credit judge the same vocabulary on the same two
// spellings and cannot disagree about what the row credits.
func syntheticVoiceCredit(raw, cleaned string) bool {
	return isAINarratorName(raw) || (cleaned != raw && isAINarratorName(cleaned))
}

// PersonKindFor is the kind a NEWLY minted person record carries. It is the one
// place a writer sets the field at all: every other kind is a human
// classification made through the correct-data form, and an absent kind means
// "an individual, or unclassified" (schema/person.schema.json). The canonical
// synthetic record is the exception, because its kind is not a judgment about
// the record - it is what the credit said.
//
// Exported for internal/issueform, which mints people of its own: a record
// created by a form and one created by an import have to be the same record.
func PersonKindFor(name string) string {
	if name == syntheticVoiceName {
		return model.KindEntitySynthetic
	}
	return ""
}

// syntheticNarrations is what a run ADMITTED under the fold: every row whose
// narration was credited to a synthetic voice, counted, with the first few kept
// for the aggregated note. It is aiRefusals' mirror image - same shape, same
// example cap (built at collection time, so a library that AI-narrates in bulk
// costs one int per row) - and deliberately a NOTE rather than a warning:
// nothing went wrong, and the run's warning list is where a reader looks for
// things that did.
type syntheticNarrations struct {
	n        int
	examples []string
}

// add records one folded narration.
func (s *syntheticNarrations) add(book, name string) {
	s.n++
	if len(s.examples) >= maxWarnExamples {
		return
	}
	s.examples = append(s.examples, fmt.Sprintf("%q (narrator %q)", book, name))
}

// note is the ONE aggregated line the fold reports, in the form every
// aggregated importer line takes. One line rather than one per book for the
// reason aiRefusals.warning gives: the news is "these books are AI-narrated and
// were admitted under one record", not which spelling each of them used.
func (s syntheticNarrations) note() (string, bool) {
	if s.n == 0 {
		return "", false
	}
	return withExamples(
		fmt.Sprintf("%d recordings credited to %s (synthetic narration)", s.n, syntheticVoiceName),
		s.examples), true
}
