// Package forwarder implements the proxy's statement fan-out sink: after a
// statement write has been accepted by the backend LRS, statements whose verb
// is on the configured allowlist (see config.StatementForwardingConfig) are
// also POSTed to one or more listener endpoints — e.g. a HazReady .NET action
// that logs them into SQL for reporting. This keeps that behavior entirely
// proxy-side and LRS-agnostic: swap the backend LRS and this still works
// unchanged, since it operates on the statement bytes the proxy already has,
// not on any LRS-specific feature.
//
// Delivery is asynchronous and durable: MaybeForward writes each job to a
// local SQLite queue (see store.go) before returning, and a single background
// worker polls that queue, retrying each job with linear backoff up to its
// destination's configured MaxRetries before marking it permanently failed.
// Because the queue is a file next to the binary rather than an in-memory
// channel, a job that's still pending when the process is killed or crashes
// is picked back up on the next start — nothing queued is lost to a restart.
// (It's still not durable against the disk/machine itself being lost; see
// store.go's OpenStore for the WAL/synchronous tradeoff that was chosen.)
// It never blocks or fails the original statement write; that write to the
// real LRS is the transaction of record, this is strictly a side effect of
// it succeeding.
//
// Delivery is at-least-once, not exactly-once: a crash between "listener
// returned 200" and "queue row marked delivered" replays that statement on
// the next start. Listener endpoints must tolerate receiving the same
// statement twice (e.g. upsert-or-ignore keyed on the statement's own xAPI
// id) rather than assuming each POST is new.
package forwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/inxsol/xapi-lrs-auth-proxy/internal/config"
)

// pollInterval is how often the worker checks the queue when it was empty
// (or everything found was still in its backoff window) on the last pass.
const pollInterval = 2 * time.Second

// pruneInterval and pruneRetention control the periodic cleanup of old
// delivered rows, so a long-lived healthy deployment doesn't grow the queue
// file forever.
const pruneInterval = 1 * time.Hour
const pruneRetention = 7 * 24 * time.Hour

// claimBatchSize bounds how many rows the worker reads per poll pass.
const claimBatchSize = 50

// Forwarder owns the verb allowlist, destination list, and delivery queue. A
// nil *Forwarder is valid and MaybeForward on it is a no-op, so callers don't
// need to nil-check before use. A non-nil Forwarder with a nil store is also
// a safe no-op — that's what New returns when forwarding is disabled, or
// when the queue file couldn't be opened (see New below); either way this
// package never takes down the rest of the proxy over a forwarding problem.
type Forwarder struct {
	cfg       config.StatementForwardingConfig
	store     *Store
	destByURL map[string]config.ForwardDestination
	client    *http.Client
}

// New creates a Forwarder from config and, if enabled, opens its durable
// queue and starts the background delivery worker. Safe to call even when
// cfg.Enabled is false — MaybeForward will just be a no-op. Never returns an
// error: statement forwarding is a side effect of a successful LRS write,
// not a precondition for the proxy to run, so a problem opening the queue
// file (permissions, disk full) is logged and leaves forwarding disabled for
// this run rather than failing proxy startup.
func New(cfg config.StatementForwardingConfig, queueDBPath string) *Forwarder {
	f := &Forwarder{
		cfg:    cfg,
		client: &http.Client{},
	}

	if !cfg.Enabled || len(cfg.Destinations) == 0 {
		return f
	}

	f.destByURL = make(map[string]config.ForwardDestination, len(cfg.Destinations))
	for _, d := range cfg.Destinations {
		f.destByURL[d.URL] = d
	}

	store, err := OpenStore(queueDBPath)
	if err != nil {
		log.WithError(err).WithField("path", queueDBPath).
			Error("statement forwarding: failed to open durable queue - forwarding disabled for this run")
		return f
	}
	f.store = store

	go f.worker()
	log.WithFields(log.Fields{
		"verbs":        cfg.Verbs,
		"destinations": len(cfg.Destinations),
		"queue_db":     queueDBPath,
	}).Info("Statement forwarding enabled")

	return f
}

// MaybeForward enqueues statement for delivery, once per configured
// destination, if its verb is on the allowlist. Called once per statement,
// after the LRS write for the batch it belonged to has already succeeded.
// Never blocks meaningfully or fails the caller: an enqueue failure (queue
// file unwritable) is logged, not propagated - same "forwarding is a side
// effect" philosophy as everywhere else in this package.
func (f *Forwarder) MaybeForward(tenantID, verbID string, statement json.RawMessage) {
	if f == nil || f.store == nil {
		return
	}
	if verbID == "" || !f.verbAllowed(verbID) {
		return
	}

	for _, dest := range f.cfg.Destinations {
		if err := f.store.Enqueue(tenantID, verbID, dest.URL, statement); err != nil {
			log.WithError(err).WithFields(log.Fields{
				"tenant_id": tenantID,
				"verb_id":   verbID,
				"dest":      dest.URL,
			}).Error("statement forwarding: failed to enqueue statement, it will NOT be delivered")
		}
	}
}

func (f *Forwarder) verbAllowed(verbID string) bool {
	for _, v := range f.cfg.Verbs {
		if strings.EqualFold(v, verbID) {
			return true
		}
	}
	return false
}

// worker polls the durable queue, attempting every job that's currently due
// (pending, and past any backoff window from a prior failed attempt) once
// per pass, then sleeps if the pass found nothing to do. Unlike the old
// in-memory version, a job that's backing off does not block delivery of
// other, unrelated jobs behind it — see Store.RecordFailure.
func (f *Forwarder) worker() {
	defer f.store.Close()

	pruneTicker := time.NewTicker(pruneInterval)
	defer pruneTicker.Stop()

	for {
		select {
		case <-pruneTicker.C:
			if n, err := f.store.PruneDelivered(pruneRetention); err != nil {
				log.WithError(err).Error("statement forwarding: failed to prune delivered rows")
			} else if n > 0 {
				log.WithField("rows", n).Info("statement forwarding: pruned old delivered rows")
			}
		default:
		}

		jobs, err := f.store.ClaimPending(claimBatchSize)
		if err != nil {
			log.WithError(err).Error("statement forwarding: failed to read pending queue")
			time.Sleep(pollInterval)
			continue
		}

		if len(jobs) == 0 {
			time.Sleep(pollInterval)
			continue
		}

		for _, j := range jobs {
			f.deliverOne(j)
		}
	}
}

func (f *Forwarder) deliverOne(j QueuedJob) {
	dest, ok := f.destByURL[j.DestURL]
	if !ok {
		// The destination was removed from config since this row was
		// queued. Nothing will ever pick this up again if left pending -
		// mark it failed outright rather than retrying forever.
		log.WithField("dest", j.DestURL).Warn("statement forwarding: queued job's destination is no longer configured, giving up on it")
		_ = f.store.RecordFailure(j.ID, j.Attempts+1, j.Attempts+1, 0, "destination no longer configured")
		return
	}

	envelope := map[string]interface{}{
		"tenant_id": j.TenantID,
		"verb_id":   j.VerbID,
		"statement": json.RawMessage(j.Statement),
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		log.WithError(err).Error("statement forwarding: failed to marshal envelope, dropping")
		_ = f.store.RecordFailure(j.ID, j.Attempts+1, j.Attempts+1, 0, "failed to marshal envelope: "+err.Error())
		return
	}

	timeout := time.Duration(dest.TimeoutSeconds) * time.Second
	backoff := time.Duration(dest.RetryBackoffSeconds) * time.Second
	maxAttempts := dest.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	attempts := j.Attempts + 1

	if err := f.attemptDelivery(dest, body, timeout); err != nil {
		if recErr := f.store.RecordFailure(j.ID, attempts, maxAttempts, backoff, err.Error()); recErr != nil {
			log.WithError(recErr).Error("statement forwarding: failed to record delivery failure")
		}
		if attempts >= maxAttempts {
			log.WithFields(log.Fields{
				"url":      dest.URL,
				"verb_id":  j.VerbID,
				"attempts": attempts,
				"error":    err,
			}).Error("statement forwarding failed after all retries - statement was NOT delivered to the listener")
		}
		return
	}

	if err := f.store.MarkDelivered(j.ID); err != nil {
		log.WithError(err).Error("statement forwarding: delivered successfully but failed to record it - may retry needlessly")
	}
}

func (f *Forwarder) attemptDelivery(dest config.ForwardDestination, body []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", dest.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if dest.SharedSecret != "" {
		req.Header.Set("X-Cmi5-Forward-Secret", dest.SharedSecret)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("listener returned status %d", resp.StatusCode)
	}
	return nil
}
