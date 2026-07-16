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
//	metascan [flags] <dir>
//
// The JSON goes to stdout (or -o); a human-readable summary goes to stderr.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/pkg/scan"
)

func main() {
	os.Exit(run())
}

// cliArgs is the parsed command line.
type cliArgs struct {
	dir     string
	out     string
	ffprobe string
}

// parseArgs parses the command line with stock flag semantics (flags first,
// then the positional <dir>), plus one trivial extra pass so trailing flags
// ("metascan <dir> -o scan.json") also work. Flag VALUES are handled by the
// flag package itself, so "-o scan.json ./books" can never misread scan.json
// as the directory.
func parseArgs(args []string) (cliArgs, error) {
	fs := flag.NewFlagSet("metascan", flag.ContinueOnError)
	out := fs.String("o", "", "write the JSON scan to this file (default: stdout)")
	ffprobe := fs.String("ffprobe", "ffprobe", "ffprobe binary for runtime/chapter enrichment; \"\" disables it")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: metascan [flags] <dir>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return cliArgs{}, err // the flag package already printed the error (or usage, for -h)
	}
	dir := fs.Arg(0)
	// Trailing-flag support: re-parse whatever followed the positional.
	if fs.NArg() > 1 {
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return cliArgs{}, err
		}
		if fs.NArg() != 0 {
			err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
			fmt.Fprintln(os.Stderr, "metascan:", err)
			fs.Usage()
			return cliArgs{}, err
		}
	}
	if dir == "" {
		err := errors.New("missing <dir>")
		fmt.Fprintln(os.Stderr, "metascan:", err)
		fs.Usage()
		return cliArgs{}, err
	}
	return cliArgs{dir: dir, out: *out, ffprobe: *ffprobe}, nil
}

func run() int {
	args, err := parseArgs(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return 0 // -h/--help is a successful outcome, not a usage error
	}
	if err != nil {
		return 2
	}

	result, stats, err := scan.Scan(args.dir, scan.Options{FFprobePath: args.ffprobe})
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

	if args.out == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, "metascan:", err)
			return 1
		}
	} else if err := os.WriteFile(args.out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "metascan:", err)
		return 1
	}

	printSummary(stats, args.out)
	return 0
}

func printSummary(s scan.Stats, out string) {
	fmt.Fprintf(os.Stderr, "metascan: %d book(s) found; %d with an ASIN; %d with series data; %d tag-read failure(s)\n",
		s.Books, s.WithASIN, s.WithSeries, s.TagFailures)
	if s.AmbiguousDirs > 0 {
		fmt.Fprintf(os.Stderr, "metascan: %d folder(s) kept as one book without tag evidence - check for collections\n",
			s.AmbiguousDirs)
	}
	if s.UnreadableDirs > 0 {
		fmt.Fprintf(os.Stderr, "metascan: %d director(y/ies) unreadable or unresolvable - skipped\n", s.UnreadableDirs)
	}
	if s.ProbeFailures > 0 {
		fmt.Fprintf(os.Stderr, "metascan: ffprobe failed on %d file(s) - runtime/chapters omitted for the affected book(s)\n",
			s.ProbeFailures)
	}
	if out != "" {
		fmt.Fprintf(os.Stderr, "metascan: wrote %s - drop it onto meta.audiosilo.app/import\n", out)
	}
}
