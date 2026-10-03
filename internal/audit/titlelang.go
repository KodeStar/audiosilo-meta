package audit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// titlelang.go is L-MIX's TITLE-LANGUAGE subclass: the set-work-language proposals a
// work's own TITLE supports, the second source beside narrator evidence
// (narration-contradicts). Without it a mis-tag no narrator had recorded twice
// elsewhere could not be proposed at all, and a language is only corrected by a
// reviewer accepting a proposal. Every proposal is ADVISORY: a title is a statement
// about a book's NAME, and a name is not its language often enough ("Dave Pelzer
// [Spanish Edition]" holds an English book, "Steelheart [German Edition]" keeps its
// English title in German). Two signals, each named in the reason with its evidence:
//
//   - EDITION STATEMENT, over EVERY work whatever its tag: the title or subtitle states
//     an own-language edition (titlerule.EditionLanguage) of a language other than the
//     work's tag - "The Gambler [Persian Edition]" tagged en, or "(English Edition)" on
//     a work tagged fr. The decoration names one language outright, so no tag is
//     exempt. Measured over the tree
//     at landing, the same shape is otherwise a language COURSE ("Learn German: By
//     Reading Fantasy (German Edition)", "101 Conversations in Simple Spanish (Spanish
//     Edition)"), whose decoration names the language taught; a title naming a
//     language outside its decoration (titlerule.NamesALanguage) is withheld.
//   - SERIES MINORITY TITLE, over EN-TAGGED members only: an en-tagged member of a
//     mixed-language series whose
//     language L is not English - the series' derived language, or for a tie the one
//     language every OTHER member states - and whose title reads as not English in
//     either of two narrow ways, both titlerule's: a BRACKET GLOSS (titlerule.GlossOf:
//     "La Odisea [The Odyssey]", the US marketplace's English translation printed
//     after a foreign title), or FUNCTION WORDS of L (titlerule.TitleFunctionWords: at
//     least two distinct ones, and not one English function word - "Il Cuore
//     Spezzato Di Arelium" in an Italian series).
//
// The series signal reads an en tag only. The two shapes run one way: an English
// gloss says the title it glosses is NOT English, and an English-language edition
// rarely keeps a foreign title - while a translation INTO another language keeps its
// original's English title all the time ("A Dance of Lies", French, translator
// credited, in a tied English/French series), so English words in a non-English
// record's title are no evidence against its tag. The series context is the
// load-bearing half: a gloss alone is not (M. Robinson's English "El Diablo [The
// Devil]"), so neither signal of the second kind is read outside a mixed series.
//
// Narrator evidence (check.NarrationProfile) only WITHHOLDS here as everywhere: a
// contradiction is named in the reason, and the proposal is advisory either way. A
// work the narration-contradicts subclass already proposes keeps that one finding,
// with the title evidence folded into its notes.

// noteTitleLanguage records title evidence that w is in lang.
func (m *langMix) noteTitleLanguage(w *model.Work, lang, why string) {
	c := m.languageCandidate(w)
	if c.titled == nil {
		c.titled = map[string][]string{}
	}
	if !slices.Contains(c.titled[lang], why) {
		c.titled[lang] = append(c.titled[lang], why)
	}
}

// editionStatements is the EDITION-STATEMENT signal over the whole catalogue.
func (m *langMix) editionStatements() {
	for _, w := range m.ix.cat.Works {
		lang, ok := titlerule.EditionLanguage(w.Title, w.Subtitle)
		if !ok || lang == model.PrimarySubtag(w.Language) {
			continue
		}
		if titlerule.NamesALanguage(w.Title) || titlerule.NamesALanguage(w.Subtitle) {
			m.st.TitleCourse++
			continue
		}
		m.st.TitleEdition++
		m.noteTitleLanguage(w, lang, fmt.Sprintf("the title %q states the %s edition", editionText(w), lang))
	}
}

// seriesTitles is the SERIES-MINORITY-TITLE signal over one mixed series: each en-tagged
// member read against the series' language - the derived one where the series has a
// majority, else (a tie) the one language every other member states.
func (m *langMix) seriesTitles(s *model.Series, byLang map[string][]model.SeriesWork, keeper, how string) {
	members := byLang["en"]
	if len(members) == 0 {
		return
	}
	for _, sw := range sortedMembers(members) {
		w := m.ix.workByID[sw.Work]
		lang := keeper
		if how != keepMajority {
			lang = otherMembersLanguage(byLang, w.ID)
		}
		if lang == "" || lang == "en" || titlerule.NamesALanguage(w.Title) || titlerule.NamesALanguage(w.Subtitle) {
			continue
		}
		var evidence []string
		if head, gloss, ok := titlerule.GlossOf(w.Title, recordingPublishers(w), m.seriesNames(w)); ok {
			evidence = append(evidence, fmt.Sprintf("its title %q is glossed [%s], the English translation of a title that is not English", head, gloss))
		}
		if words := titlerule.TitleFunctionWords(lang, w.Title, w.Subtitle); len(words) > 0 {
			evidence = append(evidence, fmt.Sprintf("its title %q carries %s function words (%s) and no English ones", editionText(w), lang, strings.Join(words, ", ")))
		}
		if len(evidence) == 0 {
			continue
		}
		m.titleSeriesWorks[w.ID] = true
		m.noteTitleLanguage(w, lang, fmt.Sprintf("a member of %s, whose other members are %s: %s", s.ID, lang, strings.Join(evidence, "; ")))
	}
}

// otherMembersLanguage is the one primary language every member of the series other
// than work states, or "" when they state several.
func otherMembersLanguage(byLang map[string][]model.SeriesWork, work string) string {
	found := ""
	for _, lang := range sortedKeys(byLang) {
		if slices.ContainsFunc(byLang[lang], func(sw model.SeriesWork) bool { return sw.Work != work }) {
			if found != "" {
				return ""
			}
			found = lang
		}
	}
	return found
}

// seriesNames is the names of every series a work is a member of.
func (m *langMix) seriesNames(w *model.Work) []string {
	var out []string
	for _, ms := range m.ix.memberships[w.ID] {
		if s := m.ix.seriesByID[ms.series]; s != nil {
			out = append(out, s.Name)
		}
	}
	return out
}

// recordingPublishers is the publishers of record of a work's recordings.
func recordingPublishers(w *model.Work) []string {
	out := make([]string, 0, len(w.Recordings))
	for _, r := range w.Recordings {
		out = append(out, r.Publisher)
	}
	return out
}

// titleLanguageFinding is the title-language record of a work no narration finding
// names: one set-work-language proposal when the evidence names one language, a review
// when it names several. Advisory either way, with the narrators' verdict in the reason.
func (m *langMix) titleLanguageFinding(w *model.Work, c *languageCandidate) Finding {
	fd := Finding{Subclass: lMixTitle, Key: w.ID, Works: []WorkRef{m.ix.workRef(w, "")}, Notes: titleNotes(c)}
	if c.evidence.Total > 0 {
		fd.Notes = append(fd.Notes, "the narrators' recordings of other works: "+evidenceText(c.evidence))
	}
	if cs := c.contested; len(cs) > 0 {
		fd.Notes = append(fd.Notes, "a minority member of a series whose keeper language is contested: "+strings.Join(sortedUnique(cs), "; "))
	}
	fd.Propose = languageProposal(w, sortedKeys(c.titled), "title evidence", func(to string) string {
		verdict := "the narrators' other recordings settle nothing"
		switch d := c.evidence.Dominant(); {
		case c.evidence.Contradicts(to):
			verdict = fmt.Sprintf("its narrators record in %s (%s), which contradicts %s", d, evidenceText(c.evidence), to)
		case d != "":
			verdict = fmt.Sprintf("its narrators record in %s too (%s)", d, evidenceText(c.evidence))
		}
		return fmt.Sprintf("the title says %s: %s; %s. A title is not a language, so a reviewed decision is what applies this",
			to, strings.Join(c.titled[to], "; "), verdict)
	})
	return fd
}

// titleNotes renders a candidate's title evidence, one note per language.
func titleNotes(c *languageCandidate) []string {
	var out []string
	for _, to := range sortedKeys(c.titled) {
		out = append(out, "-> "+to+" (title): "+strings.Join(c.titled[to], "; "))
	}
	return out
}

// languageReview is the advisory review of a work's language a set-work-language
// proposal becomes when the evidence does not settle on one language.
func languageReview(w *model.Work, reason string) Proposal {
	return Proposal{Op: OpReview, Target: w.ID, Field: fieldLanguage, From: w.Language, Advisory: true, Reason: reason}
}

// languageProposal is the set-work-language proposal for one language, always advisory,
// or the review of several - evidence names what was read ("evidence", "title
// evidence"), and reason renders the proposal's reason for its one language.
func languageProposal(w *model.Work, tos []string, evidence string, reason func(to string) string) Proposal {
	if len(tos) != 1 {
		return languageReview(w, "the "+evidence+" names several languages this work could be ("+strings.Join(tos, ", ")+"); a human decides")
	}
	return Proposal{Op: OpSetWorkLanguage, Target: w.ID, Field: fieldLanguage, From: w.Language, To: tos[0], Advisory: true,
		Reason: reason(tos[0])}
}
