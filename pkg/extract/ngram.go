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

// sidecarFields describes one works-community member kind: where its own-words
// strings live, and what a bare record has to carry to BE one.
//
// The per-item array is keyed by the KIND itself (characters[].description,
// recaps[].text), so there is no field naming it: itemField names the string
// inside an element, and its absence says the kind has no array at all - the
// description member is one flat document.
//
// required is the kind's schema-level `required` list, and it is the ONLY
// discriminator a bare record is judged by. One suggestive key is not enough:
// a whisper transcript is `{"text": [...]}`, which under a one-key rule read as
// a description whose prose happened to be absent - so the no-verbatim gate
// passed a file nobody had scanned, which is exactly the silence collectExprs
// promises never to produce. TestRequiredKeysMatchSchemas pins the list against
// the embedded schema and TestSidecarDiscriminatorsAreDisjoint pins that no one
// record can satisfy two kinds.
type sidecarFields struct {
	itemField string
	topLevel  []string
	required  []string
}

// expressiveFields is the SOURCE OF TRUTH for which sidecar fields the ngram
// check scans, keyed by the works-community MEMBER NAME the sidecar occupies
// ("characters", "recaps", "description") - which is also the stem of its schema
// file, and the key of its per-item array where it has one. These are exactly
// the own-words, length-capped (maxLength) string fields of the sidecar schemas;
// the drift-guard test (TestCheckedFieldsMatchSchemas) walks the embedded
// schemas and fails when a capped field appears there that is not listed here.
var expressiveFields = map[string]sidecarFields{
	"characters": {
		itemField: "description",
		required:  []string{"work", "characters", "license", "sources"},
	},
	"recaps": {
		itemField: "text",
		topLevel:  []string{"in_short", "ending"},
		required:  []string{"work", "recaps", "license", "sources"},
	},
	// The description sidecar is one paragraph and nothing else: no array, so its
	// own `text` is the prose. Spoiler-free is a contract about what it may SAY;
	// it is own-words prose like every other member here, so the no-verbatim gate
	// applies to it identically.
	"description": {
		topLevel: []string{"text"},
		required: []string{"work", "text", "license", "sources"},
	},
}

// matchesRecord reports whether m reads as a bare record of this kind: it
// carries every key the kind's schema requires. A kind declaring no required
// keys matches nothing, so a half-filled table can never make every file a
// sidecar.
func (f sidecarFields) matchesRecord(m map[string]any) bool {
	if len(f.required) == 0 {
		return false
	}
	for _, k := range f.required {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// collect returns the expressive strings of a record KNOWN to be of this kind -
// which the pack path knows from the member's key and the bare path from
// matchKind, so the shape is never re-guessed once it has been decided.
//
// A declared field present under the WRONG JSON type is an ERROR, not a skip:
// collectExprs' contract is that a file it accepts had its prose read, and a
// record whose `text` is an array (or whose `characters` is not one) would
// otherwise contribute nothing and report clean.
func (f sidecarFields) collect(kind string, m map[string]any, prefix string) ([]expr, error) {
	var out []expr
	if f.itemField != "" {
		if raw, ok := m[kind]; ok {
			items, ok := raw.([]any)
			if !ok {
				return nil, fmt.Errorf("%s%s: expected an array of %s entries, got %T",
					prefix, kind, kind, raw)
			}
			for i, el := range items {
				s, err := stringField(el, f.itemField)
				if err != nil {
					return nil, fmt.Errorf("%s%s[%d].%s: %w", prefix, kind, i, f.itemField, err)
				}
				if s != "" {
					out = append(out, expr{fmt.Sprintf("%s%s[%d].%s", prefix, kind, i, f.itemField), s})
				}
			}
		}
	}
	for _, tl := range f.topLevel {
		raw, ok := m[tl]
		if !ok {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%s%s: expected a string, got %T", prefix, tl, raw)
		}
		if s != "" {
			out = append(out, expr{prefix + tl, s})
		}
	}
	return out, nil
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

// matchKind names the kind a BARE record reads as, or "" for a file that is no
// sidecar record at all. The kinds are disjoint by their required keys, so the
// iteration order decides nothing (TestSidecarDiscriminatorsAreDisjoint).
func matchKind(m map[string]any) string {
	for _, kind := range sidecarKinds() {
		if expressiveFields[kind].matchesRecord(m) {
			return kind
		}
	}
	return ""
}

// requiredKeySpecs renders each kind's required keys for the "wrong file" error,
// so an operator is told what the tool was actually looking for rather than only
// that it did not find it.
func requiredKeySpecs() []string {
	specs := make([]string, 0, len(expressiveFields))
	for _, kind := range sidecarKinds() {
		quoted := make([]string, 0, len(expressiveFields[kind].required))
		for _, k := range expressiveFields[kind].required {
			quoted = append(quoted, strconv.Quote(k))
		}
		specs = append(specs, kind+" ("+strings.Join(quoted, ", ")+")")
	}
	return specs
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
			// A works-community entry nests each sidecar under its own member, so
			// the KEY names the kind. Nothing is re-discriminated by shape here: a
			// member sitting under "description" is scanned as a description, and a
			// malformed one is an error rather than a kind that failed to match.
			for _, kind := range sidecarKinds() {
				member, ok := entry[kind].(map[string]any)
				if !ok {
					continue
				}
				sidecars++
				got, err := expressiveFields[kind].collect(kind, member, slug+"."+kind+".")
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				out = append(out, got...)
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

	// A BARE record, judged by its kind's required keys and then scanned as that
	// kind. A record that matches but holds no prose is "nothing to check"; one
	// that matches nothing is the wrong file, and says which keys would have made
	// it the right one.
	if kind := matchKind(m); kind != "" {
		out, err := expressiveFields[kind].collect(kind, m, "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: not a %s sidecar record: a bare record must carry every required key of one member kind - %s",
		path, strings.Join(sidecarKinds(), ", "), strings.Join(requiredKeySpecs(), "; "))
}

// stringField reads one string field off an array element. A non-object element,
// or a field of the wrong type, is an ERROR (see sidecarFields.collect); an
// ABSENT field is simply empty, since every per-item prose field is optional.
func stringField(el any, key string) (string, error) {
	obj, ok := el.(map[string]any)
	if !ok {
		return "", fmt.Errorf("expected an object, got %T", el)
	}
	raw, ok := obj[key]
	if !ok {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("expected a string, got %T", raw)
	}
	return s, nil
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
