// Package forwarder implements the proxy's statement fan-out sink: after a
// statement write has been accepted by the backend LRS, statements whose verb
// is on the configured allowlist (see config.StatementForwardingConfig) are
// also POSTed to one or more listener endpoints — e.g. a HazReady .NET action
// that logs them into SQL for reporting. This keeps that behavior entirely
// proxy-side and LRS-agnostic: swap the backend LRS and this still works
// unchanged, since it operates on the statement bytes the proxy already has,
// not on any LRS-specific feature.
//
// Delivery is asynchronous and best-effort-with-retry, not durable: a single
// background worker drains an in-memory queue, retrying each destination with
// linear backoff up to its configured MaxRetries before giving up and logging
// the failure. Statements still queued when the process exits are lost — there
// is no persistent/replayable queue (e.g. backed by Postgres) yet. It never
// blocks or fails the original statement write; that write to the real LRS is
// the transaction of record, this is strictly a side effect of it succeeding.
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

// queueCapacity bounds how many not-yet-delivered statements can be buffered
// in memory. If the queue fills up (a sustained listener outage plus a burst
// of activity), newer statements are dropped rather than blocking statement
// writes to the LRS — the drop is logged loudly so it's visible in
// `journalctl -u lrsproxy`.
const queueCapacity = 2000

type job struct {
	tenantID  string
	verbID    string
	statement json.RawMessage
}

// Forwarder owns the verb allowlist, destination list, and delivery queue. A
// nil *Forwarder is valid and MaybeForward on it is a no-op, so callers don't
// need to nil-check before use.
type Forwarder struct {
	cfg    config.StatementForwardingConfig
	queue  chan job
	client *http.Client
}

// New creates a Forwarder from config and, if enabled, starts its background
// delivery worker. Safe to call even when cfg.Enabled is false — MaybeForward
// will just be a no-op.
func New(cfg config.StatementForwardingConfig) *Forwarder {
	f := &Forwarder{
		cfg:    cfg,
		queue:  make(chan job, queueCapacity),
		client: &http.Client{},
	}
	if cfg.Enabled && len(cfg.Destinations) > 0 {
		go f.worker()
		log.WithFields(log.Fields{
			"verbs":        cfg.Verbs,
			"destinations": len(cfg.Destinations),
		}).Info("Statement forwarding enabled")
	}
	return f
}

// MaybeForward enqueues statement for delivery if its verb is on the
// allowlist. Called once per statement, after the LRS write for the batch it
// belonged to has already succeeded. Never blocks the caller: a full queue
// drops the statement (logged), rather than backing up statement writes.
func (f *Forwarder) MaybeForward(tenantID, verbID string, statement json.RawMessage) {
	if f == nil || !f.cfg.Enabled || len(f.cfg.Destinations) == 0 {
		return
	}
	if verbID == "" || !f.verbAllowed(verbID) {
		return
	}

	select {
	case f.queue <- job{tenantID: tenantID, verbID: verbID, statement: statement}:
	default:
		log.WithFields(log.Fields{
			"tenant_id": tenantID,
			"verb_id":   verbID,
		}).Error("statement forward queue is full - dropping statement, listener may be down or falling behind")
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

// worker drains the queue sequentially, delivering each job to every
// configured destination before moving to the next. Single-worker by design:
// keeps delivery roughly ordered and avoids hammering the listener, at the
// cost of a slow/unreachable listener backing up the queue (see queueCapacity).
func (f *Forwarder) worker() {
	for j := range f.queue {
		for _, dest := range f.cfg.Destinations {
			f.deliverWithRetry(dest, j)
		}
	}
}

func (f *Forwarder) deliverWithRetry(dest config.ForwardDestination, j job) {
	envelope := map[string]interface{}{
		"tenant_id": j.tenantID,
		"verb_id":   j.verbID,
		"statement": j.statement,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		log.WithError(err).Error("statement forwarding: failed to marshal envelope, dropping")
		return
	}

	timeout := time.Duration(dest.TimeoutSeconds) * time.Second
	backoff := time.Duration(dest.RetryBackoffSeconds) * time.Second
	maxAttempts := dest.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := f.attemptDelivery(dest, body, timeout); err != nil {
			lastErr = err
			if attempt < maxAttempts {
				time.Sleep(backoff * time.Duration(attempt)) // linear backoff
			}
			continue
		}
		return // delivered
	}

	log.WithFields(log.Fields{
		"url":      dest.URL,
		"verb_id":  j.verbID,
		"attempts": maxAttempts,
		"error":    lastErr,
	}).Error("statement forwarding failed after all retries - statement was NOT delivered to the listener")
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
