package importer

import (
	"strings"
	"testing"
)

// aiSystemRows is the fixture for the authorship half of the AI exclusion. Both
// rows are lifted verbatim from the libex dump.
//
// The first is the row that motivated the rule: "CLAUDE.AI" credited as an AUTHOR
// (B0H6JDL8R5, a German novel), which the narrator-only gate let through in a
// seed wave and which minted "claude-ai" as a person of record. It also credits a
// human co-author, which is the ANY rule made visible - refusing the row costs
// that book, admitting it puts a language model in the people table, and the
// second failure is the one that cannot be undone by hand.
//
// The second is the negative control the rule has to survive: a real author
// writing ABOUT ChatGPT, narrated by a real person. The SUBJECT of a book is not
// evidence about who wrote it, and only the credits are ever judged - so this row
// imports normally even though its title carries a token from the vocabulary.
func aiSystemRows() []string {
	return []string{
		`{"asin":"B0H6JDL8R5","title":"Circle of Life - Was, wenn alles, was Du kennst, nur Code ist?",` +
			`"region":"de","publisher":"Piet Henry Records","language":"german","bookFormat":"unabridged",` +
			`"lengthMinutes":637,"authors":[{"name":"CLAUDE.AI"},{"name":"SILVIA de COUËT"}],` +
			`"narrators":[{"name":"Oliver Hiller"}]}`,
		`{"asin":"B0BZK3RWBS","title":"ChatGPT Unleashed","region":"us","language":"english",` +
			`"bookFormat":"unabridged","lengthMinutes":61,"authors":[{"name":"Morris Mann"}],` +
			`"narrators":[{"name":"Jason Wright"}]}`,
	}
}

// TestLibexRefusesAIAuthoredRows is the authorship rule's acceptance test: a row
// crediting a generative-AI system as its AUTHOR is refused by the PARSE layer,
// so no person record is ever minted for a language model - while the control
// row, a human-written book merely ABOUT one, imports normally.
func TestLibexRefusesAIAuthoredRows(t *testing.T) {
	sum, dataDir := runLibex(t, strings.Join(aiSystemRows(), "\n")+"\n", false)

	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1 (the AI-authored row)", sum.SkippedRows)
	}
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.NewPeople != 2 {
		t.Errorf("summary = %d works / %d recordings / %d people, want 1/1/2",
			sum.NewWorks, sum.NewRecordings, sum.NewPeople)
	}
	if !entryExists(t, dataDir, "works/ch/chatgpt-unleashed/work.json") {
		t.Error("the human-authored book ABOUT ChatGPT was refused; only the credits are judged")
	}
	for _, rel := range []string{"people/mo/morris-mann.json", "people/ja/jason-wright.json"} {
		if !entryExists(t, dataDir, rel) {
			t.Errorf("the control row's real person %s is missing", rel)
		}
	}
	// Nothing from the AI-authored row reached the tree - not the model, and not
	// the human credits that rode along on it.
	for _, rel := range []string{
		"people/cl/claude-ai.json",
		"people/si/silvia-de-couet.json",
		"people/ol/oliver-hiller.json",
		"works/ci/circle-of-life/work.json",
	} {
		if entryExists(t, dataDir, rel) {
			t.Errorf("the AI-authored row created %s", rel)
		}
	}
}

// TestAIAuthorWarningLine pins how an AI AUTHOR reads in the per-row report: the
// line names the credit LIST the offending name came from, so an operator is not
// left looking for a narrator that is not there.
func TestAIAuthorWarningLine(t *testing.T) {
	parsed, err := parseLibex([]byte(strings.Join(aiSystemRows(), "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(parsed.warningLines(false), "\n")
	const want = `B0H6JDL8R5: author "CLAUDE.AI" is an AI system, not a person; row skipped`
	if !strings.Contains(lines, want) {
		t.Errorf("per-row lines missing %q:\n%s", want, lines)
	}
}

// TestNamesAISystem is the vocabulary's passing/violating table. The REFUSALS in
// the second half are the load-bearing ones: every name there is a real credit in
// the dump that a lazier rule (a bare "ai", "claude" or "gpt" token) would
// wrongly exclude.
func TestNamesAISystem(t *testing.T) {
	systems := []string{
		// Every spelling the dump carries, welded to whatever human text
		// surrounds it.
		"ChatGPT", "ChatGPT ChatGPT", "ChatGPT-5", "ChatGPT 4.0 and 4o",
		"A. ChatGPT", "Sparky From ChatGPT", "Collaboration ChatGPT",
		"Chat GPT", "Chat GPT Generated",
		"GPT-5", "GPT-4 GPT-4", "GPT-4 OpenAI",
		"ChatGPT OpenAi", "ChatGPT Open AI",
		"CLAUDE.AI", "claude.ai",
		"Copilot AI", "Copilot Artifical Intelligence",
		"Grok Ai",
		"Jim Mitchell and Google NotebookLM",
		"NotebookLM Gemini Claude Grok ChatGPT Stephan Dijkstra",
	}
	for _, name := range systems {
		if !namesAISystem(name) {
			t.Errorf("namesAISystem(%q) = false, want true", name)
		}
	}

	humans := []string{
		// Real people a bare "claude" token would eat - the given name is
		// ordinary, and only the dotted product spelling is unambiguous.
		"Claude Gilbert", "Claude Lalumière", "Jean-Claude Van Damme",
		// Real credits a bare "ai" token would eat.
		"Ai-jen Poo", "Sage Ia", "Ai Voicu", "Blair Hodgkinson",
		// Person-shaped pen names a bare "gpt" token would eat, all in the dump.
		"Daddy GPT", "Mommy GPT", "Parents GPT",
		// A token that only appears INSIDE a longer word is not a token.
		"Grokker Ltd", "Chatgptsson", "Marc Openaire",
		// Ordinary credits.
		"Stephen Fry", "Full Cast", "Audible Studios", "",
	}
	for _, name := range humans {
		if namesAISystem(name) {
			t.Errorf("namesAISystem(%q) = true, want false", name)
		}
	}
}

// TestAISystemVocabularyIsCanonical pins the token table's shape. Lookups run
// against foldCredit output (lowercased, diacritics folded, single-spaced), so a
// token that is not already in that form is unreachable by construction. Mirrors
// TestAINarratorVocabularyIsCanonical.
func TestAISystemVocabularyIsCanonical(t *testing.T) {
	for _, token := range aiSystemTokens {
		if token == "" {
			t.Error("aiSystemTokens contains an empty token")
			continue
		}
		if got := foldCredit(token); got != token {
			t.Errorf("aiSystemTokens entry %q is not canonical (foldCredit gives %q); it can never match", token, got)
		}
	}
}

// TestFirstAICreditJudgesBothLists pins the widening this rule exists for: the
// two vocabularies are applied to BOTH credit lists, not one each. A synthetic
// voice credited as the author is no more a person than one credited as the
// narrator, and the dump is not tidy about which column its non-people land in.
func TestFirstAICreditJudgesBothLists(t *testing.T) {
	cases := []struct {
		name      string
		authors   []string
		narrators []string
		wantRole  string
		wantName  string
		wantWhy   string
	}{
		{"ai system as author", []string{"CLAUDE.AI"}, []string{"Oliver Hiller"},
			"author", "CLAUDE.AI", "an AI system, not a person"},
		{"ai voice as author", []string{"Virtual Voice"}, []string{"Oliver Hiller"},
			"author", "Virtual Voice", "an AI voice"},
		{"ai voice as narrator", []string{"H.G. Wells"}, []string{"AI Voice Arthur Lane"},
			"narrator", "AI Voice Arthur Lane", "an AI voice"},
		{"ai system as narrator", []string{"H.G. Wells"}, []string{"ChatGPT"},
			"narrator", "ChatGPT", "an AI system, not a person"},
		// The defensive second look, on the author side: a trailing role
		// qualifier hides the marker from the parenthetical test, and the CLEANED
		// name is the one that would become the person record.
		{"role-qualified ai author", []string{"Elise (AI) - editor"}, []string{"Oliver Hiller"},
			"author", "Elise (AI) - editor", "an AI voice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role, name, why, isAI := firstAICredit(tc.authors, tc.narrators)
			if !isAI {
				t.Fatalf("firstAICredit(%v, %v) admitted the row", tc.authors, tc.narrators)
			}
			if role != tc.wantRole || name != tc.wantName || why != tc.wantWhy {
				t.Errorf("= %q/%q/%q, want %q/%q/%q", role, name, why, tc.wantRole, tc.wantName, tc.wantWhy)
			}
		})
	}

	if _, _, _, isAI := firstAICredit([]string{"Morris Mann"}, []string{"Jason Wright"}); isAI {
		t.Error("an all-human row was refused")
	}
	if _, _, _, isAI := firstAICredit(nil, nil); isAI {
		t.Error("an empty row was refused")
	}
}
