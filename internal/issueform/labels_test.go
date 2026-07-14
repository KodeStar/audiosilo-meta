package issueform

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The f* field-label constants in the compose_*.go files mirror the `label:`
// strings in .github/ISSUE_TEMPLATE/*.yml with nothing enforcing it: rename a
// field label in a template and the composer silently reads "" forever. This
// test pins each template to the exact set of label constants the composer reads
// for it, so a drift fails CI here rather than in production.
func TestFieldLabelsExistInTemplates(t *testing.T) {
	cases := []struct {
		file   string
		labels []string
	}{
		{"add-work.yml", []string{
			fWorkTitle, fWorkSubtitle, fWorkAuthors, fWorkLanguage, fWorkFirstPublished,
			fWorkSeriesName, fWorkSeriesPosition, fWorkISBN, fWorkWikidata, fWorkOpenLibrary,
			fRecNarrators, fRecAbridged, fRecRuntime, fRecRelease, fRecPublisher,
			fRecASINs, fRecISBNs, fRecCoverURL, fSources, fCC0,
		}},
		{"add-recording.yml", []string{
			fWorkRef, fRecNarrators, fRecAbridged, fRecRuntime, fRecRelease, fRecPublisher,
			fRecASINs, fRecISBNs, fRecCoverURL, fSources, fCC0,
		}},
		{"correct-data.yml", []string{
			fCorrectRecord, fCorrectField, fCorrectCorrected, fCorrectEvidence, fCC0,
		}},
		{"add-characters.yml", []string{
			fWorkRef, fSidecarCharactersFile, fSidecarLicense,
		}},
		{"add-recaps.yml", []string{
			fWorkRef, fSidecarRecapsFile, fSidecarLicense,
		}},
		{"import-library.yml", []string{
			fImportType, fImportAttachment,
		}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			have := templateLabels(t, c.file)
			for _, want := range c.labels {
				if !have[want] {
					t.Errorf("composer reads label %q for %s, but no such field label exists in the template (labels present: %v)",
						want, c.file, sortedKeys(have))
				}
			}
		})
	}
}

// templateLabels parses a GitHub issue-form template and returns the set of its
// field-level `label:` values (the `### <label>` headings parseBody keys on). It
// intentionally ignores the nested checkbox `- label:` option strings, which are
// not headings. The templates are small and this only reads `label:` lines, so a
// full YAML parser is unnecessary.
func templateLabels(t *testing.T, name string) map[string]bool {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "ISSUE_TEMPLATE", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Field labels are `label: X`; checkbox option labels are `- label: X`
		// (list items), which are not form-field headings.
		rest, ok := strings.CutPrefix(line, "label:")
		if !ok {
			continue
		}
		out[unquoteYAML(strings.TrimSpace(rest))] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

// unquoteYAML strips a single pair of matching surrounding quotes from a scalar.
func unquoteYAML(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestTemplateFromLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   string
	}{
		{"add-work", []string{"data", "data:add-work"}, "add-work"},
		{"add-recording", []string{"data:add-recording"}, "add-recording"},
		{"correction", []string{"data", "data:correction"}, "correction"},
		{"characters", []string{"data:characters"}, "characters"},
		{"recaps", []string{"data:recaps"}, "recaps"},
		{"import", []string{"data:import"}, "import"},
		{"first routing label wins", []string{"data:correction", "data:add-work"}, "correction"},
		// Outcome labels a re-run adds must never be mistaken for a template.
		{"outcome labels ignored", []string{"data", "data:duplicate", "data:needs-human", "data:invalid"}, ""},
		{"bare data only", []string{"data"}, ""},
		{"outcome then real template", []string{"data:invalid", "data:recaps"}, "recaps"},
		{"non-data labels ignored", []string{"bug", "triage", "data:import"}, "import"},
		{"unknown data suffix", []string{"data:frobnicate"}, ""},
		{"case-insensitive suffix", []string{"data:Add-Work"}, "add-work"},
		{"whitespace tolerated", []string{" data:import "}, "import"},
		// Legacy issues opened before the data: label rename carry the bare name.
		{"legacy bare add-work", []string{"add-work"}, "add-work"},
		{"legacy bare correction", []string{"correction"}, "correction"},
		{"legacy bare characters", []string{"characters"}, "characters"},
		{"legacy bare import", []string{"import"}, "import"},
		{"legacy bare case-insensitive", []string{"Add-Work"}, "add-work"},
		{"legacy bare non-template ignored", []string{"triage", "bug"}, ""},
		{"legacy bare recaps first wins", []string{"recaps", "data:add-work"}, "recaps"},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TemplateFromLabels(c.labels); got != c.want {
				t.Errorf("TemplateFromLabels(%v) = %q, want %q", c.labels, got, c.want)
			}
		})
	}
}
