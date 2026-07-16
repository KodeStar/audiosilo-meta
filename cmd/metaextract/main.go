// Command metaextract supports the Phase 3 epub -> characters/recaps extraction
// pipeline. It has two subcommands:
//
//	metaextract split --epub <book.epub> -o <outdir>
//	    Split an epub into one plain-text file per spine document (001.txt, ...)
//	    plus a manifest.json describing the chapter list. Exits 0 on success,
//	    2 on a usage/IO/parse error.
//
//	metaextract ngram --source <dir-or-file> [--n 8] <sidecar.json> [more.json...]
//	    Check authored characters/recaps sidecars for near-verbatim overlap with
//	    the source text (the no-verbatim copyright rule in AUTHORING.md). Exits 1
//	    when any overlap is found, 0 when clean, 2 on a usage/IO error.
//
// Logic lives in pkg/extract; this command is transport-only.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/pkg/extract"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "split":
		os.Exit(runSplit(os.Args[2:]))
	case "ngram":
		os.Exit(runNGram(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "metaextract: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runSplit(args []string) int {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	epub := fs.String("epub", "", "path to the .epub file")
	out := fs.String("o", "", "output directory for the chapter text + manifest.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *epub == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "metaextract split: --epub and -o are required")
		return 2
	}

	man, err := extract.Split(*epub, *out)
	if err != nil {
		// 2, not 1: across metaextract, 1 is reserved for "findings" (ngram
		// overlaps) and 2 for usage/IO errors; split has no findings state.
		fmt.Fprintln(os.Stderr, "metaextract:", err)
		return 2
	}
	fmt.Printf("split %q: %d document(s) written to %s\n", man.Title, len(man.Docs), *out)
	for _, w := range man.Warnings {
		fmt.Println("  warning:", w)
	}
	return 0
}

func runNGram(args []string) int {
	fs := flag.NewFlagSet("ngram", flag.ContinueOnError)
	source := fs.String("source", "", "source text: a .txt file or a directory of .txt files")
	n := fs.Int("n", 8, "shingle size in words (minimum 4)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *source == "" {
		fmt.Fprintln(os.Stderr, "metaextract ngram: --source is required")
		return 2
	}
	sidecars := fs.Args()
	if len(sidecars) == 0 {
		fmt.Fprintln(os.Stderr, "metaextract ngram: at least one sidecar JSON path is required")
		return 2
	}

	findings, err := extract.NGram(*source, sidecars, *n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaextract:", err)
		return 2
	}
	for _, f := range findings {
		fmt.Printf("%s: %s\n  %d-word overlap: %q\n\n", f.File, f.Locus, f.Words, f.Text)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "%d near-verbatim overlap(s) found\n", len(findings))
		return 1
	}
	fmt.Fprintln(os.Stderr, "ok: no near-verbatim overlap found")
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  metaextract split --epub <book.epub> -o <outdir>")
	fmt.Fprintln(os.Stderr, "  metaextract ngram --source <dir-or-file> [--n 8] <sidecar.json> [more.json...]")
}
