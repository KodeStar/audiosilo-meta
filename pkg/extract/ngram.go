package extract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
// string field on each element of the kind's top-level array (itemKey), plus any
// top-level string fields.
//
// itemKey is EMPTY for a kind whose record carries no array - the description
// sidecar is one flat document - and such a kind is recognized by its top-level
// fields instead. See discriminators.
type sidecarFields struct {
	itemKey   string
	itemField string
	topLevel  []string
}

// expressiveFields is the SOURCE OF TRUTH for which sidecar fields the ngram
// check scans, keyed by the works-community MEMBER NAME the sidecar occupies
// ("characters", "recaps", "description") - which is also the stem of its schema
// file. These are exactly the own-words, length-capped (maxLength) string fields
// of the sidecar schemas; the drift-guard test (TestCheckedFieldsMatchSchemas)
// walks the embedded schemas and fails when a capped field appears there that is
// not listed here.
var expressiveFields = map[string]sidecarFields{
	"characters": {itemKey: "characters", itemField: "description"},
	"recaps":     {itemKey: "recaps", itemField: "text", topLevel: []string{"in_short", "ending"}},
	// The description sidecar is one paragraph and nothing else: no array, so it
	// is recognized (and scanned) by its own `text`. Spoiler-free is a contract
	// about what it may SAY; it is own-words prose like every other member here,
	// so the no-verbatim gate applies to it identically.
	"description": {topLevel: []string{"text"}},
}

// discriminators are the top-level keys whose presence says a bare record is of
// this kind: the array for a kind that has one, its top-level prose fields
// otherwise. A characters or recaps record carries no top-level "text", and a
// description record carries neither array, so the three are unambiguous.
func (f sidecarFields) discriminators() []string {
	if f.itemKey != "" {
		return []string{f.itemKey}
	}
	return f.topLevel
}

// matchesRecord reports whether m reads as a record of this kind.
func (f sidecarFields) matchesRecord(m map[string]any) bool {
	for _, k := range f.discriminators() {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
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

// sidecarDiscriminators returns every kind's discriminating keys, sorted and
// deduplicated - what the "wrong file" errors below name, since for a flat kind
// the member name is not a key inside its own record.
func sidecarDiscriminators() []string {
	var keys []string
	for _, kind := range sidecarKinds() {
		keys = append(keys, expressiveFields[kind].discriminators()...)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// collectExprs reads a sidecar and returns its expressive strings, driven by
// expressiveFields (characters contribute every characters[].description; recaps
// every recaps[].text plus in_short and ending when present). It parses
// generically (map[string]any) so schema growth does not break the tool.
//
// It accepts either shape the community layer takes: a works-community PACK
// file, where each entry holds a work's sidecars keyed by work slug, or a bare
// sidecar record. The pack form is what the tree actually holds, so pointing the
// check at the pack a work lives in is the normal usage; the bare form keeps a
// record extracted for review checkable on its own. Anything that is neither is
// an error, never a silent zero findings.
func collectExprs(path string) ([]expr, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if entries, ok := m["entries"].(map[string]any); ok {
		slugs := make([]string, 0, len(entries))
		for slug := range entries {
			slugs = append(slugs, slug)
		}
		slices.Sort(slugs)
		var out []expr
		sidecars := 0
		for _, slug := range slugs {
			entry, ok := entries[slug].(map[string]any)
			if !ok {
				continue
			}
			// A works-community entry nests each sidecar under its own member.
			for _, kind := range sidecarKinds() {
				member, ok := entry[kind].(map[string]any)
				if !ok {
					continue
				}
				sidecars++
				out = append(out, collectRecord(member, slug+"."+kind+".")...)
			}
		}
		// Same distinction as for a bare record: a pack whose sidecars happen to
		// carry no prose is nothing to check, while a pack holding no sidecars at
		// all is the wrong file - a works pack, say - and reporting zero findings
		// for it would pass the no-verbatim gate for text nobody looked at.
		if sidecars == 0 {
			return nil, fmt.Errorf("%s: a pack file holding no %s entries",
				path, strings.Join(sidecarKinds(), " or "))
		}
		return out, nil
	}

	if out := collectRecord(m, ""); len(out) > 0 || hasSidecarKey(m) {
		return out, nil
	}
	keys := sidecarDiscriminators()
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = strconv.Quote(k)
	}
	return nil, fmt.Errorf("%s: not a %s sidecar (no %s key)",
		path, strings.Join(sidecarKinds(), " or "), strings.Join(quoted, " or "))
}

// hasSidecarKey reports whether m is a sidecar record at all, so one that is
// simply empty of prose reads as "nothing to check" rather than "wrong file".
func hasSidecarKey(m map[string]any) bool {
	for _, kind := range sidecarKinds() {
		if expressiveFields[kind].matchesRecord(m) {
			return true
		}
	}
	return false
}

// collectRecord returns one sidecar record's expressive strings, each locus
// prefixed by prefix (empty for a bare record, "<slug>.<kind>." inside a pack).
func collectRecord(m map[string]any, prefix string) []expr {
	var out []expr
	for _, kind := range sidecarKinds() {
		fields := expressiveFields[kind]
		if !fields.matchesRecord(m) {
			continue
		}
		if fields.itemKey != "" {
			for i, el := range asSlice(m[fields.itemKey]) {
				if s := stringField(el, fields.itemField); s != "" {
					out = append(out, expr{fmt.Sprintf("%s%s[%d].%s", prefix, fields.itemKey, i, fields.itemField), s})
				}
			}
		}
		for _, tl := range fields.topLevel {
			if s, ok := m[tl].(string); ok && s != "" {
				out = append(out, expr{prefix + tl, s})
			}
		}
	}
	return out
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
