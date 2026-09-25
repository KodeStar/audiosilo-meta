// Command genrepaths DERIVES the by_path half of
// internal/importer/audiblegenres.json, and the verification file the importer's
// drift guard reads, from Audible's per-marketplace category taxonomies (as
// libex serves them at /categories?region=<marketplace>) and the table's own
// hand-authored by_asin/by_name halves. See scripts/README.md, "genrepaths".
//
// The rule it implements is the whole contract of by_path: a source that states
// a category as a LADDER of names (OpenAudible's genre field) must resolve to
// exactly what a source stating that category's NODE id resolves to, in the same
// marketplace. The importer's path lookup is
//
//	by_path[region][path]  else  by_path["us"][path]  else  by_name[leaf]
//
// so for every path of every marketplace this writes an entry exactly where that
// chain would otherwise give a different answer from the node - holding the
// node's answer, or "" when the node maps to nothing (a suppression). US is
// derived first because every other marketplace falls back to it.
//
// It is deterministic: the taxonomy files are walked in their own order,
// marketplaces in a fixed order, and the JSON is written with sorted keys.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// regions is the fixed derivation order: US first (every other marketplace
// falls back to it), then the schema's region vocabulary.
var regions = []string{"us", "uk", "ca", "au", "in", "de", "fr", "es", "it", "jp", "br"}

type category struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Children []category `json:"children"`
}

type table struct {
	ByASIN map[string]string            `json:"by_asin"`
	ByName map[string]string            `json:"by_name"`
	ByPath map[string]map[string]string `json:"by_path"`
}

type pathNode struct {
	key  string // genrePathKey form
	leaf string // lowercased, trimmed leaf name
	root string // lowercased, trimmed root name
	node string
}

func main() {
	cats := flag.String("categories", "", "directory holding <region>.json taxonomy files (libex /categories)")
	fetch := flag.Bool("fetch", false, "download the taxonomy files into -categories first (one request per marketplace, paced)")
	base := flag.String("libex", "https://libexdb.com", "libex base URL for -fetch")
	tablePath := flag.String("table", "internal/importer/audiblegenres.json", "the genre table to rewrite (by_path only)")
	verifyPath := flag.String("verify", "internal/importer/testdata/genrepaths.json", "the verification file to write")
	flag.Parse()
	if *cats == "" {
		fmt.Fprintln(os.Stderr, "genrepaths: -categories is required")
		os.Exit(2)
	}
	if err := run(*cats, *fetch, *base, *tablePath, *verifyPath); err != nil {
		fmt.Fprintln(os.Stderr, "genrepaths:", err)
		os.Exit(1)
	}
}

func run(catDir string, fetch bool, base, tablePath, verifyPath string) error {
	if fetch {
		if err := fetchAll(catDir, base); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(tablePath)
	if err != nil {
		return err
	}
	var t table
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return fmt.Errorf("%s: %w", tablePath, err)
	}

	taxonomies := map[string][]pathNode{}
	for _, r := range regions {
		data, err := os.ReadFile(filepath.Join(catDir, r+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue // a marketplace with no taxonomy simply gets no entries
		}
		if err != nil {
			return err
		}
		var roots []category
		if err := json.Unmarshal(data, &roots); err != nil {
			return fmt.Errorf("%s.json: %w", r, err)
		}
		var out []pathNode
		walk(roots, nil, &out)
		taxonomies[r] = out
	}
	if len(taxonomies["us"]) == 0 {
		return errors.New("no us.json taxonomy: every marketplace falls back to US, so it is required")
	}

	nodeAnswer := func(p pathNode) string {
		if g, ok := t.ByASIN[p.node]; ok {
			return g
		}
		return t.ByName[p.leaf]
	}

	byPath := map[string]map[string]string{}
	conflicts := 0
	for _, r := range regions {
		entries := map[string]string{}
		decided := map[string]string{}
		for _, p := range taxonomies[r] {
			want := nodeAnswer(p)
			if prev, seen := decided[p.key]; seen {
				if prev != want {
					conflicts++
					fmt.Fprintf(os.Stderr, "conflict: %s %q: %q kept, %q (node %s) dropped\n", r, p.key, prev, want, p.node)
				}
				continue
			}
			decided[p.key] = want
			fallback := t.ByName[p.leaf]
			if r != "us" {
				if g, ok := byPath["us"][p.key]; ok {
					fallback = g
				}
			}
			if want != fallback {
				entries[p.key] = want
			}
		}
		if len(entries) > 0 {
			byPath[r] = entries
		}
	}
	t.ByPath = byPath

	// The verification file: for each marketplace, the node each checked path
	// names. Checked = every path whose node the table pins by id, every path
	// some marketplace's by_path carries, every root, and every path under a
	// root the table answers "childrens" (the children's rule is checked over
	// all of it).
	anyEntry := map[string]bool{}
	for _, m := range byPath {
		for k := range m {
			anyEntry[k] = true
		}
	}
	verify := map[string]map[string]string{}
	for _, r := range regions {
		childrensRoot := map[string]bool{}
		for _, p := range taxonomies[r] {
			if !strings.Contains(p.key, ":") && nodeAnswer(p) == "childrens" {
				childrensRoot[p.root] = true
			}
		}
		m := map[string]string{}
		for _, p := range taxonomies[r] {
			if _, done := m[p.key]; done {
				continue
			}
			_, pinned := t.ByASIN[p.node]
			if pinned || anyEntry[p.key] || !strings.Contains(p.key, ":") || childrensRoot[p.root] {
				m[p.key] = p.node
			}
		}
		if len(m) > 0 {
			verify[r] = m
		}
	}

	if err := writeJSON(tablePath, t); err != nil {
		return err
	}
	if err := writeJSON(verifyPath, verify); err != nil {
		return err
	}
	n := 0
	for _, m := range byPath {
		n += len(m)
	}
	fmt.Printf("by_path: %d entries over %d marketplaces; %d same-path conflicts inside a marketplace (first kept)\n",
		n, len(byPath), conflicts)
	return nil
}

func walk(nodes []category, prefix []string, out *[]pathNode) {
	for _, n := range nodes {
		p := append(append([]string(nil), prefix...), n.Name)
		key := pathKey(p)
		if key != "" {
			*out = append(*out, pathNode{
				key:  key,
				leaf: strings.ToLower(strings.TrimSpace(n.Name)),
				root: strings.ToLower(strings.TrimSpace(p[0])),
				node: strings.TrimSpace(n.ID),
			})
		}
		walk(n.Children, p, out)
	}
}

// pathKey is internal/importer's genrePathKey over a slice of segments: each
// trimmed and lowercased, empties dropped, joined with ":". A segment holding a
// ":" of its own would be split by a colon-joined source, so it is split here
// too.
func pathKey(segs []string) string {
	var out []string
	for _, s := range segs {
		for _, part := range strings.Split(s, ":") {
			if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
				out = append(out, part)
			}
		}
	}
	return strings.Join(out, ":")
}

// writeJSON writes v with 2-space indentation, sorted keys (maps), literal
// non-ASCII and no HTML escaping - the form the table has always been kept in.
func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func fetchAll(dir, base string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	for i, r := range regions {
		if i > 0 {
			time.Sleep(2 * time.Second) // a free public service: one request at a time
		}
		resp, err := client.Get(strings.TrimRight(base, "/") + "/categories?region=" + r)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", r, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("fetch %s: %w", r, err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fetch %s: HTTP %d", r, resp.StatusCode)
		}
		if err := os.WriteFile(filepath.Join(dir, r+".json"), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
