// Command metaserve is the read-only HTTP API over the compiled metadata
// artifact. It serves the SQLite database built by metabuild and can optionally
// poll GitHub Releases to hot-swap a newer artifact. All logic lives in
// internal/serve; this is only flag wiring and signal handling.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/serve"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	db := flag.String("db", "", "path to a local meta.sqlite artifact (dev)")
	site := flag.String("site", "", "optional static site directory served at /")
	poll := flag.Bool("poll", false, "fetch and refresh the artifact from GitHub Releases")
	repo := flag.String("repo", "KodeStar/audiosilo-meta", "GitHub owner/name to poll")
	interval := flag.Duration("interval", time.Hour, "poll interval")
	cache := flag.String("cache", "./cache", "directory for downloaded artifacts")
	flag.Parse()

	cfg := serve.Config{
		Addr:     *addr,
		DBPath:   *db,
		Site:     *site,
		Poll:     *poll,
		Repo:     *repo,
		Interval: *interval,
		CacheDir: *cache,
		Token:    os.Getenv("GITHUB_TOKEN"),
	}

	srv, err := serve.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaserve:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "metaserve: listening on %s\n", *addr)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "metaserve:", err)
		os.Exit(1)
	}
}
