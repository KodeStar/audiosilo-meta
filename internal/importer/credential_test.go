package importer

import (
	"strings"
	"testing"
)

// TestCredentialVocabulary is the canonicalization self-test: a key that is not
// in its own comparison form can never match. It also pins the one entry that
// must NEVER appear here - the generational suffix, which is part of a person's
// identity and whose fold would merge a man with his father.
func TestCredentialVocabulary(t *testing.T) {
	for cred := range academicCredentials {
		if got := foldCredit(cred); got != cred {
			t.Errorf("academicCredentials key %q is not canonical (foldCredit gives %q); it can never match", cred, got)
		}
		if n := len(strings.Fields(cred)); n < 1 || n > maxCredentialWords {
			t.Errorf("academicCredentials key %q has %d words; credentialTail probes at most %d, so it could never match", cred, n, maxCredentialWords)
		}
		if words := strings.Fields(cred); len(words) > 1 && !credentialSecondWords[words[len(words)-1]] {
			t.Errorf("academicCredentials key %q is multi-word but its final word is not in credentialSecondWords, which gates the two-token probe", cred)
		}
	}
	for _, generational := range []string{"jr", "jr.", "sr", "sr.", "ii", "iii"} {
		if academicCredentials[generational] {
			t.Errorf("academicCredentials holds the generational suffix %q; it is part of the name, not a credential", generational)
		}
	}
	// The licensure tier is measured (see credential.go's header) and
	// deliberately left out, two of its entries being initials-shaped.
	for _, licensure := range []string{"mba", "rn", "jd", "lcsw", "lmft", "esq.", "mph", "ma", "ms", "ba"} {
		if academicCredentials[licensure] {
			t.Errorf("academicCredentials holds %q; the licensure tier is a separate, measured maintainer decision", licensure)
		}
	}
}

// TestCredentialFoldsOntoTheBareName walks every spelling family the dump
// carries, including the two shapes that defeat a naive one-token strip: the
// comma the source punctuates the list with, and stacked or repeated
// credentials ("DANIEL K. ASIEDU MD PhD", "Frank Lipman MD MD").
func TestCredentialFoldsOntoTheBareName(t *testing.T) {
	seen := seenAs("Philip Zimbardo", "Emily Nagoski", "John M. Gottman", "Frank Lipman", "Robert J. McMahon")
	cases := []struct{ in, want string }{
		{"Philip Zimbardo PhD", "Philip Zimbardo"},
		{"Philip Zimbardo Ph.D.", "Philip Zimbardo"},
		{"Philip Zimbardo Ph.D", "Philip Zimbardo"},
		{"Philip Zimbardo Ph. D.", "Philip Zimbardo"},
		{"Philip Zimbardo PhD.", "Philip Zimbardo"},
		{"Philip Zimbardo, Ph.D.", "Philip Zimbardo"},
		{"Emily Nagoski PhD", "Emily Nagoski"},
		{"John M. Gottman Ph.D.", "John M. Gottman"},
		{"Robert J. McMahon MD", "Robert J. McMahon"},
		{"Robert J. McMahon M.D.", "Robert J. McMahon"},
		{"Robert J. McMahon PsyD", "Robert J. McMahon"},
		{"Robert J. McMahon Ed.D.", "Robert J. McMahon"},
		{"Frank Lipman MD MD", "Frank Lipman"},                     // the doubled spelling
		{"Frank Lipman MD PhD", "Frank Lipman"},                    // stacked, two families
		{"Frank Lipman, MD, PhD", "Frank Lipman"},                  // stacked and punctuated
		{"Philip Zimbardo", "Philip Zimbardo"},                     // nothing to strip
		{"Philip Zimbardo Professor", "Philip Zimbardo Professor"}, // not a listed credential
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := stripCredential(c.in, seen); got != c.want {
				t.Errorf("stripCredential(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestCredentialWithoutABareTwinSurvives is the rule's first guard, and the
// reason it consults the census at all: 2,649 of the dump's 3,703 credentialed
// names have no bare twin anywhere, and every one of them is somebody's name of
// record.
func TestCredentialWithoutABareTwinSurvives(t *testing.T) {
	seen := seenAs("Somebody Else")
	for _, name := range []string{"Philip Zimbardo PhD", "Deepak Chopra M.D."} {
		if got := stripCredential(name, seen); got != name {
			t.Errorf("stripCredential(%q) = %q, want it untouched: nothing attests the bare spelling", name, got)
		}
	}
}

// TestCredentialNeedsAFullNameBehindIt is the second guard: the census answers
// "does this string exist as a credit", not "is it the same person", and a
// one-word remainder is where those two questions come apart.
func TestCredentialNeedsAFullNameBehindIt(t *testing.T) {
	seen := func(string) bool { return true } // the most permissive census there is
	for _, name := range []string{"Rahim MD", "Rahim, MD", "Zimbardo PhD"} {
		if got := stripCredential(name, seen); got != name {
			t.Errorf("stripCredential(%q) = %q: a one-word remainder must never merge", name, got)
		}
	}
}

// TestCredentialNeverStripsAGenerationalSuffix is the boundary between this fold
// and the trim in mapping.go: a credential says what somebody studied, "Jr."
// says which of two men with one name they are.
func TestCredentialNeverStripsAGenerationalSuffix(t *testing.T) {
	seen := func(string) bool { return true }
	for _, name := range []string{"Theodore C. Van Alst Jr.", "Martin Luther King Jr", "Jane Roe LCSW", "John Doe MBA"} {
		if got := stripCredential(name, seen); got != name {
			t.Errorf("stripCredential(%q) = %q, want it untouched", name, got)
		}
	}
}

// TestCredentialIsSilentWithoutACensus keeps the public door honest: pkg/scan
// reading an ID3 tag and internal/issueform reading a typed name have no
// catalogue in hand, so the rule must not fire for them at all.
func TestCredentialIsSilentWithoutACensus(t *testing.T) {
	const name = "Philip Zimbardo Ph.D."
	if got := stripCredential(name, nil); got != name {
		t.Errorf("stripCredential(%q, nil) = %q", name, got)
	}
	if got := CleanCreditName(name); got != name {
		t.Errorf("CleanCreditName(%q) = %q, want the name unchanged", name, got)
	}
	if got := SplitNames(name); len(got) != 1 || got[0] != name {
		t.Errorf("SplitNames(%q) = %v, want the name unchanged", name, got)
	}
}

// TestCredentialMergeIsReported is the run-level half: a merge of two person
// records is the least reversible thing an import does, so the wave lists them
// (Summary.CredentialMerges) rather than leaving them to be found in a diff.
func TestCredentialMergeIsReported(t *testing.T) {
	sum, dataDir := runLibex(t, rows(
		libexRow{asin: "B0CREDENT1", title: "Come as You Are", authors: `{"name":"Emily Nagoski"}`},
		libexRow{asin: "B0CREDENT2", title: "Burnout", authors: `{"name":"Emily Nagoski PhD"}`},
	), false)

	if sum.NewPeople != 2 { // the one author plus the shared narrator
		t.Fatalf("NewPeople = %d, want 2: the credential forked the author", sum.NewPeople)
	}
	if entryExists(t, dataDir, personAddr("emily-nagoski-phd")) {
		t.Error("the credentialed person record was created")
	}
	if !entryExists(t, dataDir, personAddr("emily-nagoski")) {
		t.Error("the bare person record is missing")
	}
	if want := "Emily Nagoski PhD -> Emily Nagoski"; !containsLine(sum.CredentialMerges, want) {
		t.Errorf("CredentialMerges = %v, want it to report %q", sum.CredentialMerges, want)
	}
	if len(sum.HonorificMerges) != 0 {
		t.Errorf("HonorificMerges = %v, want none: the two reports are separate decisions", sum.HonorificMerges)
	}
}

// TestCredentialMergesAreEmptyWhenTheRuleNeverFires keeps the report honest.
func TestCredentialMergesAreEmptyWhenTheRuleNeverFires(t *testing.T) {
	sum, _ := runLibex(t, rows(
		libexRow{asin: "B0CREDENT3", title: "A Book", authors: `{"name":"Ada One"}`},
	), false)
	if len(sum.CredentialMerges) != 0 {
		t.Errorf("CredentialMerges = %v, want none", sum.CredentialMerges)
	}
}
