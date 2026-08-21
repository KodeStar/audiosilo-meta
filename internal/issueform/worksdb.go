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
	"net/url"
	"path/filepath"
	"sort"

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

// worksDBDSN builds the read-only `file:` URI for an artifact path.
//
// The path is URI-ENCODED rather than spliced, which is the whole reason this is
// a function. `--works-db` is an operator-supplied path and a file URI is not a
// string with a path in it: a '?' anywhere in the path starts the QUERY, so
// `/tmp/a?b/meta.sqlite` names the file `/tmp/a` with the parameter `b/meta.sqlite`
// and DROPS mode=ro (a read-only guarantee lost silently, on a path the operator
// believes they named); a '#' starts a fragment and truncates just as quietly; and
// a literal '%2F' in a directory name is DECODED to '/' and opens a different file
// entirely. SQLite's URI parser does all three - they are its documented syntax,
// not a driver quirk - so the escaping has to happen before the string is handed
// over. url.URL.EscapedPath applies exactly the encodePath rule those three
// characters need.
//
// The path is made ABSOLUTE first, and that is load-bearing rather than tidy: a
// file URI with an empty authority needs a rooted path, and url.URL.String()
// renders a relative one as `file://meta.sqlite`, where the parser reads
// `meta.sqlite` as the HOST and finds no file at all.
func worksDBDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	return u.String(), nil
}

// openWorksDB opens a built artifact read-only and confirms it is one. The error
// is contributor-visible through a needs-human verdict, so it names the file and
// what was wrong with it rather than a driver message alone.
func openWorksDB(path string) (*worksDB, error) {
	dsn, err := worksDBDSN(path)
	if err != nil {
		return nil, fmt.Errorf("open works artifact %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", dsn)
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

// aliases returns every retired slug the table points AT slug, sorted.
//
// It is the redirect table read backwards, so it walks rather than seeks: the
// primary key is (kind, old_slug) and new_slug carries no index. That is a
// deliberate non-issue rather than an oversight - the table is the count of
// merges the project has ever applied (1,936 rows in the release this landed
// against, against 277k works), it is read ONCE per submission, and an index on
// new_slug would be a schema_version question in the builder for a query only the
// intake bot makes. If a repair campaign ever makes the table large enough to
// matter, the fix is that index, not a cache here.
const workAliasesSQL = `SELECT old_slug FROM redirects WHERE kind = ? AND new_slug = ? ORDER BY old_slug`

func (w *worksDB) aliases(slug string) ([]string, error) {
	if !w.hasRedirects {
		return nil, nil
	}
	rows, err := w.db.Query(workAliasesSQL, string(model.RedirectWorks), slug)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var old string
		if err := rows.Scan(&old); err != nil {
			return nil, err
		}
		if old != slug {
			out = append(out, old)
		}
	}
	return out, rows.Err()
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
//     answers from the catalogue load that seeded the dedup maps, and from that
//     load's TOMBSTONE TABLE where the works map has no answer;
//   - a root that does not (pack.ProfileCommunity) asks the release artifact the
//     same two questions - works, then redirects - and REFUSES to compose without
//     one. Never composing an unverified key is the whole point: an entry keyed by
//     a work nothing holds is a dangling sidecar the release build would stop on,
//     and the CC BY-SA layer is the most expensive data in the project to lose.
//
// The two branches are deliberately SYMMETRIC. A submitter who pastes a work-page
// URL for a retired slug gets a 301 from metaserve and never learns the slug
// changed, so the reference they type is the tombstoned one either way - and
// answering "not found" while the tree's own data/redirects.json names the
// survivor would be the bot refusing to read a table it is holding. Both branches
// therefore compose under the survivor and say so.
//
// ok is false when a terminal verdict has been set.
//
// The key it returns is not the only place a record for this work may SIT - see
// retiredKeysFor, which the sidecar path must consult before it concludes there
// is nothing there.
func (c *composer) resolveWorkKey(slug string) (key string, ok bool) {
	if c.profile.Has(pack.FamilyWorks) {
		if _, exists := c.works[slug]; exists {
			return slug, true
		}
		// The tree's own tombstone table, read exactly as the artifact branch reads
		// the artifact's: one lookup, and the target must be a live work (pkg/check
		// enforces that, so a table that fails it is a red tree rather than
		// something to resolve against).
		if to := c.redirects[model.RedirectWorks][slug]; to != "" && to != slug {
			if _, live := c.works[to]; live {
				c.noteRekey(slug, to)
				return to, true
			}
		}
		c.fail(StatusNeedsHuman, "work %q was not found; the sidecar's work must already be in the database", slug)
		return "", false
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
		c.noteRekey(slug, survivor)
		return survivor, true
	default:
		// NEEDS-HUMAN, not invalid, and the message says why: this answer is stale
		// in ONE direction by design (a work added to core since the newest release
		// has no row yet), so "nothing holds this slug" can be the BOT's gap rather
		// than the submitter's mistake. StatusInvalid is the contributor-fault
		// verdict and would tell a submitter their form was wrong when a re-run
		// after the next release may simply compose it. It is also what the
		// works-holding branch answers for the same state, and one state should not
		// change verdict class with the repository it was submitted to.
		c.fail(StatusNeedsHuman, "work %q names no work in the core catalogue (checked against the newest data release); "+
			"either the book is not in the core repository yet and has to be added there first (%s), "+
			"or it was added since that release was cut - in which case a maintainer can re-run this once a newer one is out",
			slug, coreRepoIssues)
		return "", false
	}
}

// retiredKeysFor returns every slug a core merge has RETIRED onto key - the other
// addresses a record for this work may still be sitting at - sorted, so a run is
// deterministic.
//
// It exists because "does this work already have a characters sidecar" cannot be
// answered by looking under the live slug alone. A merge retires a work slug in
// the core repository; the community entry keeps its OLD key until the re-key
// sweep lands in the other repository, and the two land at different times BY
// DESIGN (that window is the whole reason metabuild resolves community keys
// through the redirect table at compose time). Inside it, a probe under the
// survivor sees nothing and the bot composes a COMPETING member of the same kind
// for the same book. The release build then refuses the pair, and which of the
// two describes the work is a question only a human can answer.
//
// Both DIRECTIONS are covered by asking it this way round rather than by
// remembering what the submitter typed: whether the form named the retired slug
// (resolveWorkKey moved it to the survivor) or the live one (a submitter reading
// the current page), the set of addresses to check is the same set.
//
// ok is false when a terminal verdict has been set. An artifact lookup failure is
// a refusal rather than an empty list: "no aliases" and "could not ask" are the
// same answer to a caller and only one of them is safe to compose on.
func (c *composer) retiredKeysFor(key string) ([]string, bool) {
	if c.profile.Has(pack.FamilyWorks) {
		var out []string
		for old, to := range c.redirects[model.RedirectWorks] {
			if to == key && old != key {
				out = append(out, old)
			}
		}
		sort.Strings(out)
		return out, true
	}
	if c.worksDB == nil {
		// Unreachable: resolveWorkKey has already refused without an artifact.
		return nil, true
	}
	out, err := c.worksDB.aliases(key)
	if err != nil {
		c.fail(StatusNeedsHuman, "could not check %q for retired spellings against the release artifact %s: %v",
			key, c.worksDB.path, err)
		return nil, false
	}
	return out, true
}

// noteRekey reports that the submission named a slug a core merge has retired and
// that the record is being composed under the surviving one. It is a NOTE rather
// than a refusal - the bot is CHOOSING the key here, and choosing the tombstoned
// spelling would produce a pull request the community repository's own key check
// rejects with "re-key to ..." - but it is never silent: a maintainer reading the
// pull request has to be able to see that the key is not the one the form said.
func (c *composer) noteRekey(from, to string) {
	c.note("work %q has been retired by a core merge and now resolves to %q; the sidecar is composed under the surviving slug",
		from, to)
}
