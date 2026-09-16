// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package durability provides an in-memory tracker for request durability
// and graceful shutdown of in-flight requests.
package durability

import (
	"context"
	"errors"
	"sync"
)

// ErrDurabilityClosed is returned by Await when the tracker has been closed
// before the awaited request was acknowledged.
var ErrDurabilityClosed = errors.New("durability tracker closed")

// ErrDurabilityFailed is returned by Await after Fail has been called without
// a non-nil error. Fail guards against a nil error so that Await never returns
// a nil error from a failed tracker.
var ErrDurabilityFailed = errors.New("durability tracker failed")

// RequestID identifies a registered request. It is a monotonic, strictly
// increasing identifier allocated by Register. The first allocated ID is 1, so
// a zero value is never a valid registered request.
type RequestID uint64

// DurabilityTracker tracks outstanding requests that must be acknowledged
// before they are considered durable. A caller Register's a request to obtain
// a RequestID, hands the ID to goroutines that Await completion, and later
// Acknowledge's the request (by its key) once the durable condition holds.
//
// The tracker maintains a monotonically non-decreasing watermark equal to the
// highest acknowledged ID. Await considers a request durable once its ID is at
// or below the watermark. Fail and Close broadcast to all waiters and are
// terminal: once either fires, Await never returns nil again.
//
// All methods are safe for concurrent use. The tracker owns its mutex and
// never nests it under any other lock.
type DurabilityTracker struct {
	mu        sync.Mutex
	cond      *sync.Cond
	nextID    RequestID              // monotonic allocator
	watermark RequestID              // highest acknowledged ID
	pending   map[RequestID]struct{} // registered but not yet durable
	keys      map[any]RequestID      // caller key -> RequestID
	failure   error                  // non-nil once Fail has been called
	failed    bool
	closed    bool
}

// New creates a DurabilityTracker ready to register and track requests.
func New() *DurabilityTracker {
	t := &DurabilityTracker{
		pending: make(map[RequestID]struct{}),
		keys:    make(map[any]RequestID),
	}
	t.cond = sync.NewCond(&t.mu)
	return t
}

// Register enqueues a new request identified by key and returns its
// RequestID. IDs are strictly increasing. The request remains pending until the
// tracker fails, closes, or the request is acknowledged via Acknowledge.
//
// Register is safe to call after Close, but the returned ID can only ever
// resolve to ErrDurabilityClosed through Await: a registration issued on a
// closed tracker is not recorded, so it contributes neither to the watermark
// nor to HasPending. key must be comparable; passing a non-comparable key
// panics by contract.
func (t *DurabilityTracker) Register(key any) RequestID {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	id := t.nextID
	if t.closed {
		// Rejected: the request is not tracked. Await on id will observe
		// the closed flag and return ErrDurabilityClosed.
		return id
	}
	t.pending[id] = struct{}{}
	t.keys[key] = id
	return id
}

// Await blocks until the request identified by id becomes durable, the
// tracker fails, the tracker closes, or ctx is cancelled.
//
// It returns nil once id is at or below the watermark, the error passed to the
// first Fail call if Fail has been invoked, ErrDurabilityClosed if Close has
// been invoked, or ctx.Err() if ctx is cancelled before any of the above.
func (t *DurabilityTracker) Await(ctx context.Context, id RequestID) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// sync.Cond has no context awareness, so a dedicated goroutine watches
	// ctx and re-broadcasts the condition when it is cancelled. The goroutine
	// exits once Await returns (close(stop) signals it to stop) or once ctx is
	// done, whichever comes first; either path frees it.
	stop := make(chan struct{})
	defer close(stop)
	go t.watchCtx(ctx, stop)

	for {
		if t.closed {
			return ErrDurabilityClosed
		}
		if t.failed {
			return t.failure
		}
		if id <= t.watermark {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		t.cond.Wait()
	}
}

// watchCtx wakes blocked Await callers when ctx is cancelled. It exits as soon
// as the caller stops (close(stop)), avoiding a leaked goroutine when Await
// resolves for another reason (acknowledgment, failure, or close).
func (t *DurabilityTracker) watchCtx(ctx context.Context, stop <-chan struct{}) {
	select {
	case <-ctx.Done():
		t.mu.Lock()
		t.cond.Broadcast()
		t.mu.Unlock()
	case <-stop:
	}
}

// Acknowledge marks the request registered under key as durable. It raises the
// watermark to at least that request's ID and resolves every Await for IDs at
// or below the new watermark. Idempotent: acknowledging the same key twice (or
// acknowledging an already-durable ID) is a no-op. Acknowledges issued after
// Close are ignored.
func (t *DurabilityTracker) Acknowledge(key any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	id, ok := t.keys[key]
	if !ok {
		return
	}
	if id > t.watermark {
		t.watermark = id
	}
	for pid := range t.pending {
		if pid <= t.watermark {
			delete(t.pending, pid)
		}
	}
	t.cond.Broadcast()
}

// Fail transitions the tracker into a failed state. Every outstanding and
// future Await call returns err until the tracker is closed. Fail is
// idempotent: the first non-nil error is retained. A nil err is reported as
// ErrDurabilityFailed. Fail has no effect after Close.
func (t *DurabilityTracker) Fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failed || t.closed {
		return
	}
	if err == nil {
		err = ErrDurabilityFailed
	}
	t.failed = true
	t.failure = err
	t.cond.Broadcast()
}

// Close shuts the tracker down. Every outstanding Await call returns
// ErrDurabilityClosed, and subsequent Register calls are inert. Close is
// idempotent.
func (t *DurabilityTracker) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.cond.Broadcast()
}

// HasPending reports whether any previously registered request has not yet been
// acknowledged (i.e., made durable). It is false for an idle tracker or a
// tracker whose every registration has been acknowledged, failed, or closed.
func (t *DurabilityTracker) HasPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending) > 0
}
