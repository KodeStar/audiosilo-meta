// Command metascan walks a directory of audiobooks and emits a JSON document
// the meta.audiosilo.app import page accepts - the low-friction way to contribute
// a library when you have only files, no OpenAudible or Libation export.
//
// It reads embedded tags, the folder structure, and file names locally and sends
// nothing anywhere. If ffprobe is on PATH it also records runtime and chapter
// counts; without it the scan still works.
//
// Usage:
//
//	metascan <dir> [-o scan.json] [-ffprobe ffprobe]
//
// The JSON goes to stdout (or -o); a human-readable summary goes to stderr.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/scan"
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("metascan", flag.ContinueOnError)
	out := fs.String("o", "", "write the JSON scan to this file (default: stdout)")
	ffprobe := fs.String("ffprobe", "ffprobe", "ffprobe binary for runtime/chapter enrichment; \"\" disables it")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: metascan <dir> [-o scan.json] [-ffprobe ffprobe]")
		fs.PrintDefaults()
	}
	// Accept the positional <dir> either before or after the flags, so both
	// "metascan ./books -o scan.json" and "metascan -o scan.json ./books" work.
	dir, flagArgs := splitPositional(os.Args[1:])
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if dir == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	result, stats, err := scan.Scan(dir, scan.Options{FFprobePath: *ffprobe})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metascan:", err)
		return 1
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metascan:", err)
		return 1
	}
	data = append(data, '\n')

	if *out == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, "metascan:", err)
			return 1
		}
	} else if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "metascan:", err)
		return 1
	}

	printSummary(stats, *out)
	return 0
}

// splitPositional pulls the first non-flag argument out as the directory,
// leaving the rest for the flag parser.
func splitPositional(args []string) (positional string, rest []string) {
	for _, a := range args {
		if positional == "" && a != "" && a[0] != '-' {
			positional = a
			continue
		}
		rest = append(rest, a)
	}
	return positional, rest
}

func printSummary(s scan.Stats, out string) {
	fmt.Fprintf(os.Stderr, "metascan: %d book(s) found; %d with an ASIN; %d with series data; %d tag-read failure(s)\n",
		s.Books, s.WithASIN, s.WithSeries, s.TagFailures)
	if out != "" {
		fmt.Fprintf(os.Stderr, "metascan: wrote %s - drop it onto meta.audiosilo.app/import\n", out)
	}
}
