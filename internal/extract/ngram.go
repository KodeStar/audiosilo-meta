package extract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// minShingle is the smallest n allowed. Below it, ordinary phrasing collides
// with the source by chance and the check becomes noise.
const minShingle = 4

// Finding is one near-verbatim overlap between a sidecar string and the source.
type Finding struct {
	File  string // sidecar file the string came from
	Locus string // JSON locus, e.g. characters[3].description, recaps[2].text, in_short
	Text  string // the matched run (normalized: lowercased, punctuation stripped)
	Words int    // length of the run in words
}

// NGram checks each sidecar's expressive strings for near-verbatim overlap with
// the source text using n-word shingles. source is a .txt file or a directory
// of .txt files. It returns every overlap found (it does not stop at the
// first); an empty result means clean.
func NGram(source string, sidecars []string, n int) ([]Finding, error) {
	if n < minShingle {
		return nil, fmt.Errorf("n must be at least %d, got %d", minShingle, n)
	}
	shingles, err := sourceShingles(source, n)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, sc := range sidecars {
		fs, err := scanSidecar(sc, shingles, n)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fs...)
	}
	return findings, nil
}

// sourceShingles builds the set of all n-word shingles of the source. Shingles
// are built per source file (never spanning a file boundary), so concatenating
// adjacent chapters cannot fabricate a cross-boundary match.
func sourceShingles(source string, n int) (map[string]struct{}, error) {
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	var texts []string
	if info.IsDir() {
		entries, err := os.ReadDir(source)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(source, e.Name()))
			if err != nil {
				return nil, err
			}
			texts = append(texts, string(data))
		}
		if len(texts) == 0 {
			return nil, fmt.Errorf("no .txt files in %q", source)
		}
	} else {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		texts = append(texts, string(data))
	}

	set := map[string]struct{}{}
	for _, t := range texts {
		toks := tokenize(t)
		for i := 0; i+n <= len(toks); i++ {
			set[strings.Join(toks[i:i+n], " ")] = struct{}{}
		}
	}
	return set, nil
}

// scanSidecar collects a sidecar's expressive strings and reports every
// near-verbatim run. Within a string, a match is extended greedily to the
// longest run of consecutive words the source also contains; scanning then
// resumes past the run, so a run is never reported twice.
func scanSidecar(path string, shingles map[string]struct{}, n int) ([]Finding, error) {
	exprs, err := collectExprs(path)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, e := range exprs {
		toks := tokenize(e.text)
		i := 0
		for i+n <= len(toks) {
			if _, ok := shingles[strings.Join(toks[i:i+n], " ")]; !ok {
				i++
				continue
			}
			end := i + n
			for end < len(toks) {
				if _, ok := shingles[strings.Join(toks[end-n+1:end+1], " ")]; !ok {
					break
				}
				end++
			}
			findings = append(findings, Finding{
				File:  path,
				Locus: e.locus,
				Text:  strings.Join(toks[i:end], " "),
				Words: end - i,
			})
			i = end
		}
	}
	return findings, nil
}

// expr is one expressive string with its JSON locus.
type expr struct {
	locus string
	text  string
}

// sidecarFields describes where one sidecar kind's own-words strings live: a
// string field on each element of the kind's discriminating top-level array,
// plus any top-level string fields.
type sidecarFields struct {
	itemField string
	topLevel  []string
}

// expressiveFields is the SOURCE OF TRUTH for which sidecar fields the ngram
// check scans, keyed by the discriminating top-level array key ("characters"
// or "recaps"). These are exactly the own-words, length-capped (maxLength)
// string fields of the sidecar schemas; the drift-guard test
// (TestCheckedFieldsMatchSchemas) walks the embedded schemas and fails when a
// capped field appears there that is not listed here.
var expressiveFields = map[string]sidecarFields{
	"characters": {itemField: "description"},
	"recaps":     {itemField: "text", topLevel: []string{"in_short", "ending"}},
}

// sidecarKinds returns expressiveFields' keys in deterministic (sorted) order.
func sidecarKinds() []string {
	kinds := make([]string, 0, len(expressiveFields))
	for k := range expressiveFields {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	return kinds
}

// collectExprs reads a sidecar and returns its expressive strings, driven by
// expressiveFields (a characters file contributes every
// characters[].description; a recaps file every recaps[].text plus in_short and
// ending when present). It parses generically (map[string]any) so schema growth
// does not break the tool. A file with no known discriminating key is an error.
func collectExprs(path string) ([]expr, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	for _, kind := range sidecarKinds() {
		if _, ok := m[kind]; !ok {
			continue
		}
		fields := expressiveFields[kind]
		var out []expr
		for i, el := range asSlice(m[kind]) {
			if s := stringField(el, fields.itemField); s != "" {
				out = append(out, expr{fmt.Sprintf("%s[%d].%s", kind, i, fields.itemField), s})
			}
		}
		for _, tl := range fields.topLevel {
			if s, ok := m[tl].(string); ok && s != "" {
				out = append(out, expr{tl, s})
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: not a characters or recaps sidecar (no %q or %q key)", path, "characters", "recaps")
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func stringField(el any, key string) string {
	obj, ok := el.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := obj[key].(string)
	return s
}

// tokenize lowercases and splits text into word tokens, treating every rune
// that is neither a letter nor a digit as a separator. This normalizes case,
// punctuation, curly quotes, and hyphenation so the comparison is on words
// alone.
func tokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			cur.WriteRune(unicode.ToLower(r))
			continue
		}
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}
