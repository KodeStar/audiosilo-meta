package issueform

// worksdb.go answers the ONE question a data root without the works family
// cannot ask of itself: does this sidecar's entry key name a live CORE work
// slug?
//
// The community repository holds works-community alone. Its entry keys are core
// work slugs, so "the parent work exists" is not a rule that tree can carry -
// pkg/check's integrity rule stands down under ProfileCommunity, and the release
// build over both checkouts is what asks authoritatively (check.LoadComposed).
// Between those two sits the intake bot, which must NOT compose a pull request
// that CI will then reject: the stand-in it reads is the same one the community
// repository's own key check reads (scripts/key-check.sh there), the newest data
// release's meta.sqlite - the catalogue as the world currently sees it.
//
// Three verdicts, deliberately the key check's own:
//
//	live      works.id holds the slug                            compose under it
//	retired   redirects(kind='works') points it at a live work   compose under the SURVIVOR
//	unknown   neither                                            refuse, naming the slug
//
// The retired case DIVERGES from key-check.sh on purpose, and only in direction:
// the CI check FAILS a retired key because it is already written down, and the
// fix is to re-key it; the bot is composing the key in the first place, so it
// writes the live one and says so. Writing a tombstoned key instead would open a
// pull request whose own CI refuses it.
//
// STALE IN ONE DIRECTION, deliberately: a work added to core since the newest
// release has no row yet and reads as unknown. That is the intended refusal -
// the alternative is composing a dangling sidecar - and it resolves by cutting a
// release, exactly as it does for the CI check.
//
// READ-ONLY, and never trusted beyond what it can prove: the file is opened
// mode=ro, three point queries are all it is asked, and a shape it does not
// recognize is an error naming the file rather than a guess.

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // the pure-Go driver the artifact is built and served with

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// workVerdict is what the artifact says about one submitted work slug.
type workVerdict int

const (
	// workUnknown: no live work, and no tombstone naming one.
	workUnknown workVerdict = iota
	// workLive: the catalogue holds the slug.
	workLive
	// workRetired: a core merge retired the slug; the survivor is returned.
	workRetired
)

// worksDB is a read-only handle on a built meta.sqlite release artifact.
type worksDB struct {
	path string
	db   *sql.DB
	// hasRedirects records whether the artifact carries the slug tombstone table
	// at all. It arrived at artifact schema_version 5, so an older release simply
	// has none - asked ONCE at open, exactly as internal/serve asks it, rather
	// than per lookup.
	//
	// The probe is the TABLE's presence rather than the version number, which is
	// the safe way round for this consumer: an artifact that claims 5 without the
	// table then reads as "no tombstones recorded", so a retired key reports as
	// unknown and the submission is REFUSED. metaserve treats the same
	// combination as a corrupt file because it must not silently stop redirecting
	// live traffic; here the conservative reading and the loud one agree.
	hasRedirects bool
}

// The three lookups this file is allowed to make. The two catalogue ones read a
// PRIMARY KEY (works.id, and redirects' (kind, old_slug)), so neither can scan;
// the third is the table probe, asked twice at open and never again.
const (
	workLiveSQL     = `SELECT EXISTS(SELECT 1 FROM works WHERE id = ?)`
	workRedirectSQL = `SELECT new_slug FROM redirects WHERE kind = ? AND old_slug = ?`
	worksTableSQL   = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
)

// openWorksDB opens a built artifact read-only and confirms it is one. The error
// is contributor-visible through a needs-human verdict, so it names the file and
// what was wrong with it rather than a driver message alone.
func openWorksDB(path string) (*worksDB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open works artifact %s: %w", path, err)
	}
	w := &worksDB{path: path, db: db}
	works, err := w.hasTable("works")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read works artifact %s: %w", path, err)
	}
	if !works {
		_ = db.Close()
		return nil, fmt.Errorf("%s carries no works table - it is not a meta.sqlite release artifact", path)
	}
	if w.hasRedirects, err = w.hasTable("redirects"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read works artifact %s: %w", path, err)
	}
	return w, nil
}

// Close releases the handle.
func (w *worksDB) Close() error { return w.db.Close() }

func (w *worksDB) hasTable(name string) (bool, error) {
	var n int
	if err := w.db.QueryRow(worksTableSQL, name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// resolve answers the three-verdict question for one submitted work slug. On
// workRetired, survivor is the live slug the sidecar must be keyed by; it is
// empty otherwise.
//
// A tombstone whose TARGET the catalogue does not hold reads as unknown, not as
// retired: it resolves to nothing, so there is no slug to compose under. That is
// key-check.sh's own reading of the same row.
func (w *worksDB) resolve(slug string) (survivor string, v workVerdict, err error) {
	live, err := w.live(slug)
	if err != nil {
		return "", workUnknown, err
	}
	if live {
		return "", workLive, nil
	}
	if !w.hasRedirects {
		return "", workUnknown, nil
	}
	var to string
	switch err := w.db.QueryRow(workRedirectSQL, string(model.RedirectWorks), slug).Scan(&to); {
	case errors.Is(err, sql.ErrNoRows):
		return "", workUnknown, nil
	case err != nil:
		return "", workUnknown, err
	}
	// A self-redirect is a row pkg/check refuses; reading it as "live" here would
	// be a lie about a slug the works table has already said it does not hold.
	if to == "" || to == slug {
		return "", workUnknown, nil
	}
	target, err := w.live(to)
	if err != nil {
		return "", workUnknown, err
	}
	if !target {
		return "", workUnknown, nil
	}
	return to, workRetired, nil
}

func (w *worksDB) live(slug string) (bool, error) {
	var ok bool
	if err := w.db.QueryRow(workLiveSQL, slug).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// resolveWorkKey verifies that slug names a live core work and returns the slug
// the composed entry must be KEYED by. It is the one place the work-existence
// question is asked, and WHERE it is asked depends on the tree profile:
//
//   - a root that HOLDS the works family (pack.ProfileAll, this repository today)
//     answers from the catalogue load that seeded the dedup maps - unchanged, byte
//     for byte, from before the profile existed;
//   - a root that does not (pack.ProfileCommunity) answers from the release
//     artifact, and REFUSES to compose without one. Never composing an unverified
//     key is the whole point: an entry keyed by a work nothing holds is a dangling
//     sidecar the release build would stop on, and the CC BY-SA layer is the most
//     expensive data in the project to lose.
//
// ok is false when a terminal verdict has been set.
func (c *composer) resolveWorkKey(slug string) (string, bool) {
	if c.profile.Has(pack.FamilyWorks) {
		if _, exists := c.works[slug]; !exists {
			c.fail(StatusNeedsHuman, "work %q was not found; the sidecar's work must already be in the database", slug)
			return "", false
		}
		return slug, true
	}
	if c.worksDB == nil {
		c.fail(StatusNeedsHuman, "this data root holds no works family, so the bot cannot check that %q names a live work; "+
			"it needs the newest data release artifact (metaissue --works-db meta.sqlite) and will not compose a sidecar it could not verify", slug)
		return "", false
	}
	survivor, verdict, err := c.worksDB.resolve(slug)
	if err != nil {
		c.fail(StatusNeedsHuman, "could not check %q against the release artifact %s: %v", slug, c.worksDB.path, err)
		return "", false
	}
	switch verdict {
	case workLive:
		return slug, true
	case workRetired:
		c.note("work %q has been retired by a core merge and now resolves to %q; the sidecar is composed under the surviving slug",
			slug, survivor)
		return survivor, true
	default:
		c.fail(StatusInvalid, "work %q names no work in the core catalogue (checked against the newest data release); "+
			"the book has to be added to the core repository first (%s) - or, if it was added there since that release was cut, "+
			"a maintainer can re-run this once a newer release is out",
			slug, coreRepoIssues)
		return "", false
	}
}
