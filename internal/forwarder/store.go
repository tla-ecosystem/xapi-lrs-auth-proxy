// store.go backs the forwarder's delivery queue with a local SQLite file
// instead of an in-memory channel, so a statement accepted for forwarding
// survives a proxy crash or restart rather than being lost. Single-tenant,
// single-process deployment (this proxy) doesn't need a shared/external
// queue - an embedded file next to the binary gives the actual guarantee
// that was missing (survive a process restart) without adding a database
// server to operate. See the package doc comment in forwarder.go for the
// full reasoning.
package forwarder

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers as "sqlite" - no cgo/C toolchain needed to build or deploy this
)

// QueuedJob is one not-yet-delivered (or not-yet-exhausted) row read back
// off the queue.
type QueuedJob struct {
	ID        int64
	TenantID  string
	VerbID    string
	DestURL   string
	Statement []byte
	Attempts  int
}

// Store is the SQLite-backed queue. One row per (statement, destination)
// pair - MaybeForward fans a statement out to N rows at enqueue time (one
// per configured destination) so each destination's delivery/retry state is
// tracked independently; a listener that's down doesn't hold up a listener
// that's up.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if needed) the SQLite file at path and ensures
// its schema exists.
//
// journal_mode=WAL + synchronous=NORMAL is the standard "durable against a
// process crash, without paying fsync-per-write latency" combination - it's
// what this is actually for (the proxy process dying or being restarted
// mid-queue). It is not durable against the OS itself crashing or the
// machine losing power in the instant after a commit; if that stronger
// guarantee is ever needed, switching synchronous to FULL is a one-line
// change here, at the cost of slower writes.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite queue at %q: %w", path, err)
	}

	// The forwarder's worker is a single goroutine and MaybeForward's writes
	// are individually tiny, so there's no need for a connection pool -
	// forcing a single connection also sidesteps SQLite's well-known
	// "database is locked" errors under concurrent writers from the same
	// process, without needing busy_timeout tuning to paper over it.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("set %q: %w", p, err)
		}
	}

	schema := `
		CREATE TABLE IF NOT EXISTS forward_queue (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id       TEXT NOT NULL,
			verb_id         TEXT NOT NULL,
			dest_url        TEXT NOT NULL,
			statement       BLOB NOT NULL,
			status          TEXT NOT NULL DEFAULT 'pending', -- pending | delivered | failed
			attempts        INTEGER NOT NULL DEFAULT 0,
			last_error      TEXT,
			next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			delivered_at    DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_forward_queue_claim
			ON forward_queue(status, next_attempt_at, id);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Enqueue inserts a new pending row. Called once per (statement,
// destination) pair from MaybeForward - synchronous, but a single-row
// SQLite insert under WAL is on the order of microseconds, not something
// that meaningfully slows the statement write path it's called from.
func (s *Store) Enqueue(tenantID, verbID, destURL string, statement []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO forward_queue (tenant_id, verb_id, dest_url, statement) VALUES (?, ?, ?, ?)`,
		tenantID, verbID, destURL, statement,
	)
	return err
}

// ClaimPending reads up to limit rows that are due for an attempt (pending,
// and either never attempted or past their backoff window). This is a plain
// read, not a lock/dequeue - the single worker goroutine is the only reader,
// so there's no multi-consumer race to guard against here. If that ever
// changes (e.g. a second worker), this needs an UPDATE ... RETURNING-style
// claim instead.
func (s *Store) ClaimPending(limit int) ([]QueuedJob, error) {
	rows, err := s.db.Query(
		`SELECT id, tenant_id, verb_id, dest_url, statement, attempts
		 FROM forward_queue
		 WHERE status = 'pending' AND next_attempt_at <= CURRENT_TIMESTAMP
		 ORDER BY id
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []QueuedJob
	for rows.Next() {
		var j QueuedJob
		if err := rows.Scan(&j.ID, &j.TenantID, &j.VerbID, &j.DestURL, &j.Statement, &j.Attempts); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// MarkDelivered records a successful delivery.
func (s *Store) MarkDelivered(id int64) error {
	_, err := s.db.Exec(
		`UPDATE forward_queue SET status = 'delivered', delivered_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	return err
}

// RecordFailure records a failed delivery attempt. If attempts has now
// reached maxAttempts the row is marked 'failed' (terminal - matches today's
// "give up after MaxRetries" behavior); otherwise it stays 'pending' with
// next_attempt_at pushed out by backoff, so ClaimPending picks it up again
// once that window passes. Deliberately does not block the worker goroutine
// with time.Sleep the way the old in-memory version did - that meant one
// job's backoff delayed every job queued behind it. Scheduling the retry via
// next_attempt_at instead means other pending jobs still get attempted on
// this and later passes while this one waits.
func (s *Store) RecordFailure(id int64, attempts, maxAttempts int, backoff time.Duration, errMsg string) error {
	status := "pending"
	nextAttemptAt := time.Now().Add(backoff * time.Duration(attempts)) // linear backoff, same as before
	if attempts >= maxAttempts {
		status = "failed"
	}
	_, err := s.db.Exec(
		`UPDATE forward_queue SET attempts = ?, last_error = ?, status = ?, next_attempt_at = ? WHERE id = ?`,
		attempts, errMsg, status, nextAttemptAt, id,
	)
	return err
}

// PruneDelivered deletes delivered rows older than olderThan, so the queue
// file doesn't grow forever on a long-lived, healthy deployment. Failed
// (exhausted-retries) rows are deliberately left alone - those represent a
// listener outage that should stay visible/queryable rather than silently
// disappearing.
func (s *Store) PruneDelivered(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	res, err := s.db.Exec(`DELETE FROM forward_queue WHERE status = 'delivered' AND delivered_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
