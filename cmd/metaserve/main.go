// Command metaserve is the read-only HTTP API over the compiled metadata
// artifact. It serves the SQLite database built by metabuild and can optionally
// refresh from GitHub Releases on signed webhooks and fallback polls. All logic
// lives in internal/serve; this is only flag wiring and signal handling.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/serve"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/release"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	db := flag.String("db", "", "path to a local meta.sqlite artifact (dev)")
	site := flag.String("site", "", "optional static site directory served at /")
	siteURL := flag.String("site-url", model.SiteURL,
		"public origin of this deployment, used for canonical, og: and JSON-LD URLs on the entity pages")
	poll := flag.Bool("poll", false, "fetch and refresh the artifact from GitHub Releases")
	repo := flag.String("repo", release.DefaultRepo, "GitHub owner/name to poll")
	interval := flag.Duration("interval", time.Hour, "poll interval")
	cache := flag.String("cache", "./cache", "directory for downloaded artifacts")
	rateLimit := flag.Float64("rate-limit", envFloat("METASERVE_RATE_LIMIT", serve.DefaultRateLimit),
		"API requests a second per client (a token bucket); 0 turns the limit off, a negative value is refused (env METASERVE_RATE_LIMIT)")
	rateBurst := flag.Int("rate-burst", envInt("METASERVE_RATE_BURST", serve.DefaultRateBurst),
		"the bucket's size: how many requests a client may make at once; at least 1 while the limit is on, "+
			"0 is refused rather than defaulted (env METASERVE_RATE_BURST)")
	trustedProxies := flag.String("trusted-proxy", os.Getenv("METASERVE_TRUSTED_PROXIES"),
		"comma-separated proxy addresses or CIDR prefixes whose X-Forwarded-For names the client; "+
			"behind a reverse proxy, name it, or every request counts as one client (env METASERVE_TRUSTED_PROXIES)")
	flag.Parse()

	proxies, err := serve.ParseTrustedProxies(*trustedProxies)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaserve:", err)
		os.Exit(2)
	}

	cfg := serve.Config{
		Addr:           *addr,
		DBPath:         *db,
		Site:           *site,
		SiteURL:        *siteURL,
		Poll:           *poll,
		Repo:           *repo,
		Interval:       *interval,
		CacheDir:       *cache,
		Token:          os.Getenv("GITHUB_TOKEN"),
		WebhookSecret:  os.Getenv("METASERVE_WEBHOOK_SECRET"),
		RateLimit:      *rateLimit,
		RateBurst:      *rateBurst,
		TrustedProxies: proxies,
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

// envInt is envFloat for a whole number: a value that is not an integer
// (including "2.5") is reported and falls back to def.
func envInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaserve: %s=%q is not a whole number; using %d\n", name, raw, def)
		return def
	}
	return v
}

// envFloat is a flag's default read from the environment: the variable's value
// when it parses as a number, else def. An unparsable value is reported, not
// ignored, so a typo in a deployment's environment is visible at boot.
func envFloat(name string, def float64) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaserve: %s=%q is not a number; using %g\n", name, raw, def)
		return def
	}
	return v
}
