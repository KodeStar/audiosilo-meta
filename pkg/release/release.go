// Package release finds AudioSilo Meta's DATA releases on GitHub and downloads
// their artifact, verified. It is the ONE implementation of the release asset
// contract - the asset names and digests, "the newest non-draft,
// non-prerelease release carrying meta.sqlite.gz", the allowlisted download
// hosts, the stall watchdog and the gunzip + sha256 + temp-then-rename install -
// shared by metaserve's poller (internal/serve, which adds the binary-delta and
// cache-volume paths on top) and by mirror-mode AudioSilo servers, which
// download the same artifact once a day.
//
// The asset contract itself is release.yml's: meta.sqlite.gz plus
// meta.sqlite.gz.sha256 (the universal anchor every download is verified
// against) and meta.sqlite.sha256 (the digest of the decompressed file, which
// verifies a file that was NOT just downloaded through the anchor - one
// reconstructed from a delta, or already on disk), every one verified by the
// workflow before the release is published. The repository also cuts code/image releases (v*) with no data
// assets, so GitHub's "latest" release is not the data release: selection is by
// asset presence at the maximum published_at.
package release

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/ghhost"
)

// The repository and the data release's asset names.
const (
	// DefaultRepo is the repository the data releases are published from.
	DefaultRepo = "KodeStar/audiosilo-meta"
	// DataAsset is the release asset that makes a release a DATA release: the
	// gzipped SQLite artifact. Release selection keys on its presence.
	DataAsset = "meta.sqlite.gz"
	// DataDigestAsset is DataAsset's `sha256sum`-format checksum, which every
	// download is verified against while it streams.
	DataDigestAsset = DataAsset + ".sha256"
	// RawDigestAsset is the checksum of the DECOMPRESSED artifact. No raw
	// artifact is published - only its digest, which verifies a decompressed,
	// reconstructed or already-present file.
	RawDigestAsset = "meta.sqlite.sha256"
)

// Asset is one release asset as the release metadata declares it.
type Asset struct {
	Name string
	// URL is the asset's browser_download_url.
	URL string
	// Size is the asset's size as the release metadata declares it (0 when it
	// declares none). For a compressed asset it is the COMPRESSED size - the one
	// thing a plausible expansion can be measured against (see DecompressBound).
	Size int64
}

// Release is one data release.
type Release struct {
	Tag         string
	PublishedAt time.Time
	Assets      []Asset
}

// Asset returns the named asset of r. It hands back the WHOLE asset rather than
// its URL because the declared Size is what bounds a download.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// The request deadlines. They are deliberately NOT one http.Client.Timeout:
// that is a deadline on the whole request, body included, so a single number
// has to cover both a tiny JSON document and an artifact download that is
// hundreds of MB. Sized for the JSON it aborts healthy long downloads - and a
// server whose first artifact never arrives never becomes ready. Sized for the
// artifact it lets a hung metadata request sit for the same long time. So the
// two are split, and the artifact download is policed by PROGRESS rather than
// by total duration.
const (
	// metadataTimeout bounds the release-metadata request end to end. It is a
	// small JSON document: a slow one is a broken one.
	metadataTimeout = 30 * time.Second

	// responseHeaderTimeout bounds the wait for response HEADERS on every
	// request, asset downloads included. It is what catches a connection that is
	// accepted and then never answered, which is otherwise the one failure the
	// stall watchdog below cannot see (no body has started).
	responseHeaderTimeout = 30 * time.Second

	// assetDeadline is the absolute ceiling on ONE asset download. It is not a
	// service-level expectation but a runaway guard: a slow-but-healthy link may
	// legitimately need many minutes for the artifact, and aborting that is worse
	// than waiting for it.
	assetDeadline = 60 * time.Minute

	// assetStallTimeout is how long a download may go without delivering a
	// single byte before it is abandoned. This - not a whole-request deadline -
	// is what tells a dead transfer from a slow one, so a healthy download of any
	// size finishes while a stalled one fails in a minute.
	assetStallTimeout = 60 * time.Second
)

// Timeouts are a Client's request deadlines (see the constants above for why
// they are three). A zero field means the default.
type Timeouts struct {
	// Metadata is the whole-request deadline on the release list (30s).
	Metadata time.Duration
	// Asset is the ceiling on one asset download (60m) - a runaway guard.
	Asset time.Duration
	// Stall is the no-progress watchdog on an asset download (60s): the
	// download is abandoned once it delivers nothing for this long.
	Stall time.Duration
}

// Client talks to the GitHub Releases API of one repository.
type Client struct {
	base      string // API base, "https://api.github.com" (overridable for tests)
	repo      string // "owner/name"
	token     string // optional
	userAgent string // "" = net/http's own
	http      *http.Client
	log       *log.Logger

	// allowOrigins are the `scheme://host` origins this client may talk to
	// BESIDE the public GitHub rule (see checkAssetTarget) - and, as a redirect
	// hop, beside ghhost.HopPolicy. It is EMPTY in production, where the policy
	// is exactly github.com / api.github.com / *.githubusercontent.com over
	// https with no port; its one filler is test setup pointing the whole client
	// at an httptest server, which is plain HTTP on a loopback IP with a port -
	// three things the production rule refuses, and rightly.
	allowOrigins []string

	// baseSize reports the size of the artifact the caller currently holds (0
	// for none) - the arm of the expansion bound that tracks a growing catalogue
	// (see DecompressBound).
	baseSize func() int64

	timeouts Timeouts
}

// Option configures a Client.
type Option func(*Client)

// WithAPIBase points the client at another API base URL than
// https://api.github.com, and admits that origin as a download origin (see
// WithAllowedOrigin). TEST SETUP: production always talks to api.github.com.
func WithAPIBase(base string) Option {
	return func(c *Client) {
		c.base = strings.TrimRight(base, "/")
		allowOrigin(c, base)
	}
}

// WithAllowedOrigin admits raw's `scheme://host[:port]` origin as a download
// origin and a redirect hop BESIDE the production rule (https, no explicit port,
// github.com / api.github.com / *.githubusercontent.com). TEST SETUP: an
// httptest server is plain HTTP on a loopback IP with a port, three things the
// production rule refuses - so the exemption is an explicit piece of test setup
// matching a WHOLE origin, not a clause inside the rule.
func WithAllowedOrigin(raw string) Option {
	return func(c *Client) { allowOrigin(c, raw) }
}

// WithHTTPClient makes the client send its requests through hc (a copy of it).
// Its CheckRedirect is replaced by this package's hop policy, and its Timeout
// should be zero: downloads are policed by progress (Timeouts), and a
// whole-request deadline would abort a healthy slow one.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		cp := *hc
		c.http = &cp
	}
}

// WithUserAgent sets the User-Agent every request carries (GitHub asks API
// clients to name themselves). Empty keeps net/http's default.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithLogger routes the client's notices (an asset that declares no size) to
// l. Nil means log.Default().
func WithLogger(l *log.Logger) Option { return func(c *Client) { c.log = l } }

// WithBaseSize tells the client how big the artifact the caller currently holds
// is (0 for none). The decompression bound never falls below twice it, so the
// bound tracks a growing catalogue instead of needing a constant raised by
// hand (see DecompressBound).
func WithBaseSize(size func() int64) Option { return func(c *Client) { c.baseSize = size } }

// WithTimeouts overrides the request deadlines; a zero field keeps its default.
// A test scales them down; production has no reason to.
func WithTimeouts(t Timeouts) Option {
	return func(c *Client) {
		if t.Metadata > 0 {
			c.timeouts.Metadata = t.Metadata
		}
		if t.Asset > 0 {
			c.timeouts.Asset = t.Asset
		}
		if t.Stall > 0 {
			c.timeouts.Stall = t.Stall
		}
	}
}

// New returns a client for repo ("owner/name"; "" means DefaultRepo). token is
// optional: it raises the API rate limit and is sent only to the allowlisted
// GitHub hosts.
func New(repo, token string, opts ...Option) *Client {
	if repo == "" {
		repo = DefaultRepo
	}
	c := &Client{
		base:  "https://api.github.com",
		repo:  repo,
		token: token,
		timeouts: Timeouts{
			Metadata: metadataTimeout,
			Asset:    assetDeadline,
			Stall:    assetStallTimeout,
		},
	}
	for _, o := range opts {
		o(c)
	}
	if c.http == nil {
		// The client carries NO Timeout (see the deadline constants); the
		// per-request contexts do that job. The transport still bounds the
		// pre-body phase.
		tr, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			tr = &http.Transport{}
		}
		tr = tr.Clone()
		tr.ResponseHeaderTimeout = responseHeaderTimeout
		c.http = &http.Client{Transport: tr}
	}
	if c.log == nil {
		c.log = log.Default()
	}
	if c.baseSize == nil {
		c.baseSize = func() int64 { return 0 }
	}
	// A browser_download_url ALWAYS 302s to a CDN host, so a redirect is the
	// normal path here rather than an edge case. The hop is judged by
	// ghhost.HopPolicy - https, no IP literal, bounded - and deliberately NOT by
	// the asset allowlist: GitHub picks the CDN and has moved it before, and
	// net/http strips the Authorization header on a cross-host hop anyway. See
	// ghhost's package doc, which is where that trade is argued.
	c.http.CheckRedirect = ghhost.CheckRedirect(0, c.allowsOrigin) // 0: ghhost.DefaultMaxRedirects
	return c
}

// Timeouts reports the client's request deadlines.
func (c *Client) Timeouts() Timeouts { return c.timeouts }

func allowOrigin(c *Client, raw string) {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		c.allowOrigins = append(c.allowOrigins, u.Scheme+"://"+u.Host)
	}
}

// allowsOrigin reports whether u is one of the extra origins above. Both the
// scheme and the host (port included) must match: an exemption that ignored
// either would be a hole in the rule it is an exception to.
func (c *Client) allowsOrigin(u *url.URL) bool {
	origin := u.Scheme + "://" + u.Host
	for _, o := range c.allowOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

func (c *Client) setCommonHeaders(req *http.Request) {
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// ---- finding the data release ------------------------------------------------

// releaseListPageSize is how many recent releases LatestData scans for a data
// release. The repo also cuts code/image releases (v*, no data assets) between
// data releases, so a window of the list is searched and the newest by
// published_at selected (the list order is not publish-chronological - see
// LatestData).
//
// The window is a BOOT-SURVIVAL concern, not just a freshness one: a server
// that holds no artifact yet finds nothing to load if enough code/image releases
// ever pushed every data release out of these 15, and every retry scans the
// same exhausted window. A wider window (or pagination) is the fix if the
// cadence of code releases ever approaches this. release.yml's prev-release
// selection scans the same window (TestWorkflowMatchesTheSelection).
const releaseListPageSize = 15

// apiAsset and apiRelease are the GitHub releases API's JSON, as much of it as
// selection and download read.
type apiAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

type apiRelease struct {
	TagName     string     `json:"tag_name"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt time.Time  `json:"published_at"`
	Assets      []apiAsset `json:"assets"`
}

// LatestData fetches the newest releases and returns the non-draft,
// non-prerelease release carrying a DataAsset with the MAXIMUM published_at.
// GitHub's "latest" release can be a code/image release (v*) with no data
// assets, so selection is by asset presence, not recency alone. The list
// endpoint's order is NOT publish-chronological: it was observed live to be
// created_at date descending, then reverse-lexicographic tag order within a day
// - so with several same-day data releases the first-listed one is not the
// newest.
//
// The request is CONDITIONAL: etag is the ETag of the previous answer ("" for
// none), and a 304 comes back as notModified with rel nil. newETag is the
// validator to send next time - the response's ETag, or etag unchanged when the
// response carried none or the request failed - and the caller stores it even
// alongside an error: a 200 listing no data release stores its ETag, so the
// next poll is a cheap 304 until the list changes (retrying an unchanged list
// cannot find a data release either). A caller whose download of rel then
// fails should drop the stored ETag, or the next poll 304s and never retries it.
func (c *Client) LatestData(ctx context.Context, etag string) (rel *Release, newETag string, notModified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeouts.Metadata)
	defer cancel()
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", c.base, c.repo, releaseListPageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, etag, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	c.setCommonHeaders(req)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, etag, false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, etag, false, fmt.Errorf("releases: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var list []apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, etag, false, err
	}
	if et := resp.Header.Get("ETag"); et != "" {
		etag = et
	}
	var best *apiRelease
	for i := range list {
		r := &list[i]
		if r.Draft || r.Prerelease || !hasAsset(r, DataAsset) {
			continue
		}
		// Greater-or-equal keeps the LATER-in-list entry on equal timestamps,
		// matching release.yml's jq (max_by is a stable sort, so it returns the
		// last of a tied pair) - the two selectors must agree even on a
		// same-second tie, or the workflow could base its patch on a release no
		// running server has loaded.
		if best == nil || !r.PublishedAt.Before(best.PublishedAt) {
			best = r
		}
	}
	if best == nil {
		return nil, etag, false, fmt.Errorf("no data release with a %s asset among the latest %d", DataAsset, len(list))
	}
	out := &Release{Tag: best.TagName, PublishedAt: best.PublishedAt}
	for _, a := range best.Assets {
		out.Assets = append(out.Assets, Asset{Name: a.Name, URL: a.DownloadURL, Size: a.Size})
	}
	return out, etag, false, nil
}

func hasAsset(r *apiRelease, name string) bool {
	for _, a := range r.Assets {
		if a.Name == name {
			return true
		}
	}
	return false
}

// ---- downloading -------------------------------------------------------------

// get issues the authenticated asset GET. The caller owns (and must close) the
// response body - closing it is also what releases the request context, so a
// caller that forgets leaks a context until the asset deadline.
//
// The download is bounded by the asset deadline (a runaway guard) and, far more
// tightly, by a no-progress watchdog: see stallGuard.
func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	// The URL comes out of the release JSON, and the request below attaches this
	// client's token to whatever host it names - so the host is checked FIRST.
	if err := c.checkAssetURL(url); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeouts.Asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	c.setCommonHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	resp.Body = newStallGuard(resp.Body, c.timeouts.Stall, cancel)
	return resp, nil
}

// stallGuard wraps a download body with a no-progress watchdog and owns the
// request context's cancel func (released on Close, so the guard is also what
// makes the deadline collectable).
//
// It is the piece that lets the artifact download have a generous deadline
// without also tolerating a dead connection for that long: every byte delivered
// resets the timer, so a slow-but-steady transfer of any size runs to
// completion, while one that goes quiet for the stall timeout is cancelled. The
// cancellation surfaces to the reader as a context error, which says nothing
// about why, so Read rewrites it into the real reason.
type stallGuard struct {
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	stalled atomic.Bool
}

func newStallGuard(body io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) *stallGuard {
	g := &stallGuard{body: body, timeout: timeout, cancel: cancel}
	g.timer = time.AfterFunc(timeout, func() {
		g.stalled.Store(true)
		cancel() // kills the in-flight Read
	})
	return g
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.body.Read(p)
	if n > 0 {
		g.timer.Reset(g.timeout)
	}
	if err != nil && g.stalled.Load() {
		return n, fmt.Errorf("download stalled: no data for %s", g.timeout)
	}
	return n, err
}

func (g *stallGuard) Close() error {
	g.timer.Stop()
	err := g.body.Close()
	g.cancel()
	return err
}

// checkAssetURL refuses an asset URL this client may not send its token to.
//
// The URLs are read out of the release JSON, which is data from a remote
// service, and get() attaches `Authorization: Bearer <token>` to whatever they
// name. A release whose asset URL pointed at another host - a compromised or
// mistaken one, or simply a repository somebody else can publish to - would
// therefore hand that host a credential. The allowlist is applied BEFORE the
// request is built, so a refused URL is never even dialed.
func (c *Client) checkAssetURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("download %s: %w", raw, err)
	}
	return c.checkAssetTarget(u)
}

// checkAssetTarget is the rule itself, over a parsed URL: the INITIAL URL the
// release JSON named, which is the one this client attaches its token to. A
// redirect hop is judged by ghhost.HopPolicy instead (see New).
//
// Production policy is exactly: https, no explicit port, and one of github.com,
// api.github.com or *.githubusercontent.com. `api.github.com` is this client's
// own addition to the shared GitHub host rule - it is the API's asset route,
// which is no part of what issueform fetches.
//
// THE PORT IS PART OF THE RULE. The allowlist reads u.Hostname(), which strips
// the port, so `https://objects.githubusercontent.com:8443/x` matched the host
// arm and was dialled with the bearer token attached - a URL the release JSON
// chooses, pointing at whatever is listening on that port of a host whose NAME
// resolves wherever the resolver says. A real asset URL never carries one.
//
// The one exception is an origin WithAllowedOrigin/WithAPIBase was handed,
// which is test setup alone and is why it is checked as a whole origin rather
// than as a relaxation of any single arm.
func (c *Client) checkAssetTarget(u *url.URL) error {
	if c.allowsOrigin(u) {
		return nil
	}
	switch {
	case u.Scheme != "https":
		// A token is not sent in clear.
		return fmt.Errorf("refusing to download %s: scheme %q is not https", u.Redacted(), u.Scheme)
	case u.Port() != "":
		return fmt.Errorf("refusing to download %s: %q names an explicit port", u.Redacted(), u.Host)
	case !ghhost.Allowed(u.Hostname(), "api.github.com"):
		return fmt.Errorf("refusing to download %s: %q is not a GitHub release-asset host", u.Redacted(), u.Host)
	}
	return nil
}

// maxSmallAssetBytes bounds the assets that are read into memory. Only the
// `sha256sum`-format checksum files take that path (about 80 bytes each); the
// artifact and a patch are streamed to disk, so nothing unbounded is ever
// buffered.
const maxSmallAssetBytes = 4 << 10

// ReadAsset reads the named asset of rel into memory - checksum files only: it
// refuses anything larger than 4 KiB.
func (c *Client) ReadAsset(ctx context.Context, rel *Release, name string) ([]byte, error) {
	asset, ok := rel.Asset(name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s asset", rel.Tag, name)
	}
	resp, err := c.get(ctx, asset.URL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSmallAssetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSmallAssetBytes {
		return nil, fmt.Errorf("download %s: larger than %d bytes, expected a checksum file", asset.URL, maxSmallAssetBytes)
	}
	return body, nil
}

// readDigest reads one of rel's checksum assets and parses it.
func (c *Client) readDigest(ctx context.Context, rel *Release, name string) (string, error) {
	sum, err := c.ReadAsset(ctx, rel, name)
	if err != nil {
		return "", err
	}
	return ExpectedDigest(sum)
}

// DownloadAsset streams the named asset of rel straight into dstPath (a temp
// file in dstPath's folder, then a rename - never through memory) and, when
// wantHexDigest is non-empty, refuses to install it unless the streamed bytes
// hash to it. It returns the bytes written.
//
// The bound on what lands on disk is the asset's DECLARED size: nothing is
// decompressed here, so the release states exactly how many bytes this is, and a
// body that runs past its own declaration is either a corrupt transfer or a host
// answering with something else. A release that declares no size falls back to
// the expansion bound (a ceiling rather than a size, but still a bound).
func (c *Client) DownloadAsset(ctx context.Context, rel *Release, name, dstPath, wantHexDigest string) (int64, error) {
	asset, ok := rel.Asset(name)
	if !ok {
		return 0, fmt.Errorf("release %s has no %s asset", rel.Tag, name)
	}
	bound := asset.Size
	if bound <= 0 {
		bound = c.expansionBound(name, 0)
	}
	resp, err := c.get(ctx, asset.URL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return InstallVerified(resp.Body, dstPath, wantHexDigest, bound)
}

// Result describes an installed artifact.
type Result struct {
	// Bytes is the size of the decompressed artifact.
	Bytes int64
	// SHA256 is its lowercase hex digest, taken while it was written - the value
	// the release's RawDigestAsset states for it, and what VerifyFile checks a
	// copy against later.
	SHA256 string
}

// DownloadData downloads rel's artifact into dstPath in ONE streaming pass: the
// DataAsset body is hashed as it arrives and decompressed straight into a temp
// file in dstPath's folder, so no .gz is ever staged and neither form of the
// artifact is held in memory. Before the temp file is renamed onto dstPath the
// COMPRESSED bytes must match DataDigestAsset; any failure - the digest, the
// bound, a stall, a cancelled context - removes the temp file and never creates
// dstPath.
//
// The gz digest is the ANCHOR, deliberately the only gate: it pins the exact
// compressed bytes, and gzip's own CRC then pins what they decompress to, so a
// RawDigestAsset check here would add a failure mode (a broken checksum file
// failing a download that is provably right) without adding a guarantee - and
// metaserve's full download is the fallback that must still work when the raw
// checksum, which gates its patch path, is the broken asset. The decompressed
// file's digest comes back in Result for a caller that records or compares it.
//
// The decompressed size is bounded (see DecompressBound: the declared size, a
// floor and the caller's current artifact), the download is stall-guarded and
// only ever made to an allowlisted GitHub host. progress, when non-nil, is
// called as compressed bytes arrive, with the bytes so far and the size the
// release declares (0 when it declares none).
func (c *Client) DownloadData(ctx context.Context, rel *Release, dstPath string, progress func(done, total int64)) (Result, error) {
	// The checksum is fetched FIRST, so the download can be gated on it without
	// ever materializing the compressed asset.
	wantGz, err := c.readDigest(ctx, rel, DataDigestAsset)
	if err != nil {
		return Result{}, err
	}
	asset, ok := rel.Asset(DataAsset)
	if !ok {
		return Result{}, fmt.Errorf("release %s has no %s asset", rel.Tag, DataAsset)
	}
	resp, err := c.get(ctx, asset.URL)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body io.Reader = resp.Body
	if progress != nil {
		body = &progressReader{r: body, total: asset.Size, report: progress}
	}
	return gunzipStreamTo(body, dstPath, wantGz, c.expansionBound(DataAsset, asset.Size))
}

// progressReader reports the bytes read through it.
type progressReader struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.report(p.done, p.total)
	}
	return n, err
}

// ---- installing --------------------------------------------------------------

// ExpectedDigest parses a `sha256sum`-format checksum file, returning its first
// whitespace-separated field as the expected lowercase hex digest.
func ExpectedDigest(checksumFile []byte) (string, error) {
	fields := strings.Fields(string(checksumFile))
	if len(fields) == 0 {
		return "", fmt.Errorf("checksum file is empty")
	}
	return strings.ToLower(fields[0]), nil
}

// tempPattern names every temp file an install writes beside its destination
// (see TempFile).
const tempPattern = ".meta-*.tmp"

// TempFile reports whether name (a base name) is one of the temp files an
// install writes beside its destination. A process that died mid-download
// leaves one behind; a caller that owns the folder deletes them at start.
func TempFile(name string) bool {
	return strings.HasPrefix(name, ".meta-") && strings.HasSuffix(name, ".tmp")
}

// installStream streams src into dstPath atomically (temp file + fsync + rename
// in dstPath's directory). When verify is non-nil it runs AFTER the copy and
// BEFORE the rename: an error from it means nothing is installed. Any failure
// removes the temp file and never creates dstPath. Returns the number of bytes
// written.
//
// The gate is a callback rather than a digest because the bytes that must be
// verified are not always the bytes being written: DownloadData decompresses
// while it downloads, so what it can check against the published checksum is
// the COMPRESSED input as well as the artifact landing on disk.
//
// maxBytes is the bound on what is written (0 means unbounded, which no caller
// passes: every artifact-sized writer carries a bound). The check lives here
// because this is where the copy's byte count already is, and it is applied
// exactly as a failed verification is: the temp file goes and dstPath is never
// created. src is read at most one byte past the bound, which is all it takes to
// prove it was crossed.
func installStream(src io.Reader, dstPath string, maxBytes int64, verify func() error) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), tempPattern)
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if maxBytes > 0 {
		src = io.LimitReader(src, maxBytes+1)
	}
	n, err := io.Copy(tmp, src) //nolint:gosec // bounded above and gated by verify below, so unverified bytes never become dstPath
	if err == nil {
		// Durable before it is visible: a crash after the rename must not leave
		// a name pointing at bytes the disk never received.
		err = tmp.Sync()
	}
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if maxBytes > 0 && n > maxBytes {
		return 0, fmt.Errorf("the asset expands past the %d-byte bound: refusing it as unverifiable", maxBytes)
	}
	if verify != nil {
		if err := verify(); err != nil {
			return 0, err
		}
	}
	if err := os.Rename(tmpName, dstPath); err != nil {
		return 0, err
	}
	return n, nil
}

// checkDigest compares an accumulated hash against an expected lowercase hex
// digest.
func checkDigest(h hash.Hash, wantHexDigest string) error {
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHexDigest {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, wantHexDigest)
	}
	return nil
}

// InstallVerified streams src into dstPath atomically (a temp file in dstPath's
// folder, fsynced, then renamed), requiring the streamed bytes to hash to
// wantHexDigest (empty = no digest check) before anything is installed. maxBytes
// bounds what is written (0 = unbounded); crossing it fails exactly as a digest
// mismatch does. It returns the bytes written.
func InstallVerified(src io.Reader, dstPath, wantHexDigest string, maxBytes int64) (int64, error) {
	if wantHexDigest == "" {
		return installStream(src, dstPath, maxBytes, nil)
	}
	h := sha256.New()
	return installStream(io.TeeReader(src, h), dstPath, maxBytes, func() error {
		return checkDigest(h, wantHexDigest)
	})
}

// downloadBufferBytes is the read buffer for the on-disk paths - large enough
// that a multi-hundred-MB artifact is not read in 4KB syscalls, small enough to
// be irrelevant to the process's footprint.
const downloadBufferBytes = 1 << 20

// gunzipStreamTo decompresses src into dstPath in ONE pass, verifying the
// COMPRESSED bytes against wantGzDigest before the result is installed and
// hashing the decompressed ones for the Result. Neither the compressed nor the
// decompressed bytes are ever held in memory.
//
// The gz digest is taken at the tee, i.e. over everything read from src, and the
// reader is drained before the check so trailing bytes cannot be skipped by the
// gzip reader stopping at its trailer. installStream runs the check before the
// rename, so a corrupted download is discarded with the temp file.
//
// maxBytes is the computed bound on what is written (DecompressBound). The
// checksum gate is the real defence, but it only fires at the END, and a gzip
// stream declares no output size - so without a bound, an asset that
// decompresses without limit fills the volume before anything gets to reject
// it. Hitting the bound is treated exactly as a failed verification.
func gunzipStreamTo(src io.Reader, dstPath, wantGzDigest string, maxBytes int64) (Result, error) {
	gzHash := sha256.New()
	tee := io.TeeReader(src, gzHash)
	zr, err := gzip.NewReader(bufio.NewReaderSize(tee, downloadBufferBytes))
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = zr.Close() }()
	rawHash := sha256.New()
	n, err := installStream(io.TeeReader(zr, rawHash), dstPath, maxBytes, func() error {
		if _, err := io.Copy(io.Discard, tee); err != nil {
			return err
		}
		return checkDigest(gzHash, wantGzDigest)
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Bytes: n, SHA256: hex.EncodeToString(rawHash.Sum(nil))}, nil
}

// VerifyFile hashes an existing file and compares it with an expected lowercase
// hex digest. The download paths verify bytes as they stream; this is for bytes
// already on disk (an artifact a previous process left behind, adopted only once
// it matches the release's RawDigestAsset).
func VerifyFile(path, wantHexDigest string) error {
	f, err := os.Open(path) //nolint:gosec // the caller's own artifact path, not request input
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, downloadBufferBytes)); err != nil {
		return err
	}
	return checkDigest(h, wantHexDigest)
}

// ---- the expansion bound -----------------------------------------------------

// The EXPANSION BOUND. A decompressed download - and metaserve's binary-delta
// reconstruction - writes a file whose size is decided by the asset's CONTENT
// rather than by anything either end declared: a gzip stream states no output
// size, and a zstd --patch-from frame's window is the whole base artifact. The
// sha256 gate rejects a wrong result, but only once the bytes are on disk, so
// the bound is what keeps a runaway expansion from filling the volume first.
//
// IT IS A CEILING, NOT AN EXPECTATION, and the failure mode it must not have is
// being too LOW. A bound the real artifact exceeds is not a caught attack, it is
// every refresh failing forever - re-downloading the whole asset each poll,
// writing maxBytes+1 bytes to the volume each time, and serving stale data in
// between. That is worse than the disk-filling it guards against, so every arm
// below is sized generously and the three are combined with max():
//
//   - the DECLARED compressed size times decompressRatio. The real artifact's
//     gzip ratio measured about 3.8x, so 16x is margin enough for a catalogue
//     that compresses far worse than today's and is still a bound.
//   - decompressFloor, which covers a small or UNDECLARED size. It has to clear
//     the real artifact on its own, because that is what an asset declaring no
//     size falls back to: at ~1.7 GB today, a 1 GiB floor was below the file it
//     was bounding.
//   - twice the artifact the caller CURRENTLY HOLDS, when there is one. The
//     catalogue only grows, and the next release is the neighbour of the one
//     being served, so this is the arm that keeps the bound tracking the data
//     instead of needing a constant raised by hand every year.
const (
	decompressRatio = 16
	decompressFloor = 4 << 30 // 4 GiB
	// currentArtifactRatio is the multiple of the current artifact's on-disk
	// size the bound may never fall below. Two: a release that DOUBLED the
	// catalogue between two polls is not something to refuse.
	currentArtifactRatio = 2
)

// DecompressBound is the arithmetic above: the maximum of the floor, the ratio
// over a declared compressed size (0 or negative = nothing declared) and
// currentArtifactRatio over the current artifact's size (0 = none). Each
// multiplication is guarded against an absurd input overflowing into a small
// number, which would turn the ceiling into a trap.
func DecompressBound(declared, currentBytes int64) int64 {
	bound := int64(decompressFloor)
	if currentBytes > 0 && currentBytes <= (1<<62)/currentArtifactRatio {
		bound = max(bound, currentBytes*currentArtifactRatio)
	}
	if declared > 0 && declared <= (1<<62)/decompressRatio {
		bound = max(bound, declared*decompressRatio)
	}
	return bound
}

// expansionBound is DecompressBound over this client's base size, and the one
// place an UNDECLARED asset size is reported: a release that stops declaring
// sizes is a silent move onto the floor, and an operator should read that in the
// log rather than infer it from a refresh that started failing.
func (c *Client) expansionBound(asset string, declared int64) int64 {
	bound := DecompressBound(declared, c.baseSize())
	if declared <= 0 {
		c.log.Printf("release: asset %s declares no size; bounding its expansion at %d bytes", asset, bound)
	}
	return bound
}
