package revision

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kodestar/audiosilo-meta/internal/sqlitedsn"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// Families is every page family a revision is recorded for, in the order the
// rows are written.
var Families = []query.PageFamily{
	query.CharacterPages, query.PersonPages, query.RecapPages, query.SeriesPages, query.WorkPages,
}

// Options is what Stamp needs besides the artifact it stamps.
type Options struct {
	// Previous is the previous data release's artifact, which the new one's dates
	// are carried from. It must open: a release whose predecessor cannot be read
	// is refused rather than stamped as though it had none, which would restart
	// every date in the catalogue.
	Previous string
	// Base is the previous release's tag, recorded as meta(revisions_base) so the
	// chain can be audited. Optional.
	Base string
	// At is when a changed or new page changed: the release's own stamp, which
	// release.yml takes as the newer of the two composed commits. It is clamped to
	// the previous release's meta(revisions_at), so dates never go backwards along
	// the chain, and truncated to the second.
	At time.Time
	// Workers is how many pages are digested at once (at least one).
	Workers int
}

// Summary is what one Stamp did.
type Summary struct {
	// At is the stamp changed pages were given, after the clamp.
	At string
	// Pages is how many pages the new artifact holds; Changed how many of them
	// were stamped At (changed or new), Carried how many kept a date from the
	// previous release and Unknown how many kept none.
	Pages, Changed, Carried, Unknown int
}

// Stamp fills the page_revisions table of the freshly built artifact at out
// (which internal/build left empty) by comparing it with opts.Previous - see the
// package documentation for the rule - and records meta(revisions_at) and, when
// given, meta(revisions_base).
//
// The result is a pure function of out's data, the previous artifact and the
// stamp: rows are inserted in (kind, id) order, so two runs over the same inputs
// write the same bytes.
func Stamp(ctx context.Context, out string, opts Options) (Summary, error) {
	if opts.Previous == "" {
		return Summary{}, errors.New("revision: no previous artifact to compare with")
	}
	if opts.At.IsZero() {
		return Summary{}, errors.New("revision: no revision stamp")
	}
	newDB, err := query.Open(out, "")
	if err != nil {
		return Summary{}, err
	}
	if v := newDB.Info().SchemaVersion; v < query.MaxSchemaVersion {
		_ = newDB.Close()
		return Summary{}, fmt.Errorf("revision: %s is schema_version %d; stamping needs %d", out, v, query.MaxSchemaVersion)
	}
	current, err := Digests(ctx, newDB, opts.Workers)
	_ = newDB.Close()
	if err != nil {
		return Summary{}, fmt.Errorf("revision: digesting %s: %w", out, err)
	}

	prev, err := readPrevious(ctx, opts.Previous, opts.Workers)
	if err != nil {
		return Summary{}, fmt.Errorf("revision: reading the previous artifact %s: %w", opts.Previous, err)
	}

	at := opts.At.UTC().Truncate(time.Second)
	if prev.revisionsAt.After(at) {
		at = prev.revisionsAt
	}
	stamp := at.Format(time.RFC3339)

	sum := Summary{At: stamp, Pages: len(current)}
	rows := make(map[Key]string, len(current))
	for k, d := range current {
		if pd, ok := prev.digests[k]; ok && pd == d {
			if date, ok := prev.dates[k]; ok {
				rows[k] = date
				sum.Carried++
			} else {
				sum.Unknown++
			}
			continue
		}
		rows[k] = stamp
		sum.Changed++
	}
	if err := write(out, rows, stamp, opts.Base); err != nil {
		return Summary{}, err
	}
	return sum, nil
}

// previous is what Stamp reads from the previous release.
type previous struct {
	digests     map[Key]Digest
	dates       map[Key]string
	revisionsAt time.Time
}

func readPrevious(ctx context.Context, path string, workers int) (*previous, error) {
	db, err := query.Open(path, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	digests, err := Digests(ctx, db, workers)
	if err != nil {
		return nil, err
	}
	p := &previous{digests: digests, dates: map[Key]string{}}
	for _, family := range Families {
		for offset := 0; ; offset += enumWindow {
			entries, err := db.Pages(family, enumWindow, offset)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.RevisedAt != nil {
					p.dates[Key{family, e.ID}] = *e.RevisedAt
				}
			}
			if len(entries) < enumWindow {
				break
			}
		}
	}
	at, err := revisionsAt(path)
	if err != nil {
		return nil, err
	}
	p.revisionsAt = at
	return p, nil
}

// revisionsAt reads meta(revisions_at), the zero time when the artifact states
// none (one older than the revision layer, or one built without a previous
// release). The meta table is in every artifact, so this is asked of any version.
func revisionsAt(path string) (time.Time, error) {
	dsn, err := sqlitedsn.ReadOnly(path)
	if err != nil {
		return time.Time{}, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = db.Close() }()
	var v string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = 'revisions_at'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("meta(revisions_at) %q: %w", v, err)
	}
	return t.UTC(), nil
}

// write inserts rows into out's page_revisions in (kind, id) order and records
// the meta rows, in one transaction. A table that already holds rows is refused:
// an artifact is stamped once, against one predecessor.
func write(out string, rows map[Key]string, stamp, base string) (err error) {
	dsn, err := sqlitedsn.ReadWrite(out)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := db.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	var stamped bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM page_revisions) OR EXISTS(SELECT 1 FROM meta WHERE key = 'revisions_at')`).
		Scan(&stamped); err != nil {
		return err
	}
	if stamped {
		return fmt.Errorf("revision: %s is already stamped", out)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	ins, err := tx.Prepare(`INSERT INTO page_revisions(kind, id, revised_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = ins.Close() }()
	keys := slices.SortedFunc(maps.Keys(rows), func(a, b Key) int {
		return cmp.Or(cmp.Compare(a.Family, b.Family), cmp.Compare(a.ID, b.ID))
	})
	for _, k := range keys {
		if _, err = ins.Exec(string(k.Family), k.ID, rows[k]); err != nil {
			return err
		}
	}
	meta := [][2]string{{"revisions_at", stamp}}
	if base != "" {
		meta = append(meta, [2]string{"revisions_base", base})
	}
	for _, kv := range meta {
		if _, err = tx.Exec(`INSERT INTO meta(key, value) VALUES (?, ?)`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}
