package serve

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite" // the queue's driver, the artifact's too

	"github.com/kodestar/audiosilo-meta/internal/sqlitedsn"
)

// intakeSchema is the whole of what the queue keeps: an ASIN, how many POSTs named
// it and the first and last UTC day one did. It is deliberately this narrow.
// There is no column for a client or anything that identifies one, a day rather
// than a timestamp, and no rowid, so neither the rows nor their order can rebuild
// who sent what or which ASINs arrived together.
const intakeSchema = `CREATE TABLE IF NOT EXISTS intake_asins(asin TEXT PRIMARY KEY, count INTEGER NOT NULL, first_day TEXT NOT NULL, last_day TEXT NOT NULL) WITHOUT ROWID`

// intakePragmas keep the arrival order off the disk too, which the schema alone
// cannot promise. journal_mode=DELETE leaves no sidecar behind a commit, where a
// WAL file would hold each batch's pages in arrival order until a checkpoint; and
// secure_delete zeroes the content of a freed row rather than leaving it in a free
// page of the file.
var intakePragmas = []string{"journal_mode(DELETE)", "secure_delete(ON)"}

// intakeQueue is the SQLite file behind the intake routes. It is separate from the
// artifact on purpose: a swap replaces the artifact and prune clears the cache
// directory, and neither may touch this. It is also disposable. A lost file costs
// nothing a reporting server does not send again.
type intakeQueue struct {
	db *sql.DB
	// maxRows is the row cap, maxIntakeRows outside the tests.
	maxRows int
}

// openIntakeQueue opens the queue at path, creating the file, its parent and the
// table as needed, and proves it writable: a queue that can only be read would
// answer every report 503 for as long as the server runs, and reporting that once
// at boot leaves the routes honestly off instead.
func openIntakeQueue(path string) (*intakeQueue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Created here because SQLite would create it world-readable, and the queue is
	// a token-only read. Its rollback journal takes this file's mode.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // operator-supplied path, not user input
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	dsn, err := sqlitedsn.ReadWrite(path, intakePragmas...)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: every use is short and none spans client I/O (handlers read
	// the body before and write the answer after), and it means a writer never
	// meets SQLITE_BUSY from this process's own other connection.
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		intakeSchema,
		// A file that already held some other intake_asins fails here.
		`SELECT asin, count, first_day, last_day FROM intake_asins LIMIT 0`,
		// The write proof: a real write to the header page, which needs the
		// journal, so a read-only directory or file is found out now.
		`PRAGMA user_version = 1`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &intakeQueue{db: db, maxRows: maxIntakeRows}, nil
}

func (q *intakeQueue) close() error { return q.db.Close() }

// sqlArgs is asins as a query's bound arguments and the "?,?" list that takes them.
func sqlArgs(asins []string) (list string, args []any) {
	args = make([]any, len(asins))
	for i, a := range asins {
		args[i] = a
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(asins)), ","), args
}

// record counts one report of each of asins, a set of distinct valid ASINs no
// longer than one POST carries, as of the UTC day today. All of it or none: the
// caller answers 503 on an error and the report is simply not counted.
//
// At the cap, each ASIN the queue does not hold evicts the lowest row by (count,
// last_day), with the ASIN breaking a tie so one state has one outcome. The
// report's own rows are exempt. A batch's new ASIN is what the cap makes room for,
// and one it already holds is being seen right now. Spam therefore mostly displaces
// spam: a row seen once sorts below any row seen twice, oldest first.
func (q *intakeQueue) record(ctx context.Context, asins []string, today string) error {
	if len(asins) == 0 {
		return nil
	}
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	list, args := sqlArgs(asins)
	var others int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM intake_asins WHERE asin NOT IN (`+list+`)`, args...).Scan(&others); err != nil {
		return err
	}
	if over := others + len(asins) - q.maxRows; over > 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM intake_asins WHERE asin IN (
			SELECT asin FROM intake_asins WHERE asin NOT IN (`+list+`) ORDER BY count, last_day, asin LIMIT ?)`,
			append(args, over)...)
		if err != nil {
			return err
		}
	}
	upsert, err := tx.PrepareContext(ctx, `INSERT INTO intake_asins(asin, count, first_day, last_day) VALUES(?, 1, ?, ?)
		ON CONFLICT(asin) DO UPDATE SET count = count + 1, last_day = excluded.last_day`)
	if err != nil {
		return err
	}
	defer func() { _ = upsert.Close() }()
	// Sorted, so the statements' order says nothing about the body's either.
	for _, asin := range slices.Sorted(slices.Values(asins)) {
		if _, err := upsert.ExecContext(ctx, asin, today, today); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// intakeRow is one queue row, the read route's wire shape.
type intakeRow struct {
	ASIN     string `json:"asin"`
	Count    int    `json:"count"`
	FirstDay string `json:"first_day"`
	LastDay  string `json:"last_day"`
}

// list is the whole queue in the read route's order, never nil. The cap bounds it.
func (q *intakeQueue) list(ctx context.Context) ([]intakeRow, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT asin, count, first_day, last_day FROM intake_asins ORDER BY count DESC, first_day, asin`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []intakeRow{}
	for rows.Next() {
		var r intakeRow
		if err := rows.Scan(&r.ASIN, &r.Count, &r.FirstDay, &r.LastDay); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// asins is every ASIN the queue holds, which a purge checks against the artifact.
func (q *intakeQueue) asins(ctx context.Context) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT asin FROM intake_asins`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// intakeRemoveChunk bounds one remove statement's bound variables.
const intakeRemoveChunk = 500

// remove deletes asins from the queue, in one transaction.
func (q *intakeQueue) remove(ctx context.Context, asins []string) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for chunk := range slices.Chunk(asins, intakeRemoveChunk) {
		list, args := sqlArgs(chunk)
		if _, err := tx.ExecContext(ctx, `DELETE FROM intake_asins WHERE asin IN (`+list+`)`, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// expire deletes the rows last seen before the UTC day cutoff.
func (q *intakeQueue) expire(ctx context.Context, cutoff string) error {
	if _, err := q.db.ExecContext(ctx, `DELETE FROM intake_asins WHERE last_day < ?`, cutoff); err != nil {
		return fmt.Errorf("expire: %w", err)
	}
	return nil
}
