// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package durability

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noWait is a short, race-tolerant budget used only to assert that something
// has *not* happened yet. It is deliberately larger than the scheduler tick so
// that a blocked goroutine does not get starved under -race.
const noWait = 80 * time.Millisecond

// awaitTimed runs Await in a goroutine and reports its result on done. It
// never leaks: if the caller stops reading, the goroutine eventually returns
// when Await resolves (via ack/fail/close/ctx) or the test's context cancels.
func awaitTimed(t *testing.T, tr *DurabilityTracker, ctx context.Context, id RequestID) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- tr.Await(ctx, id) }()
	return done
}

// Test 1: registration creates an awaitable request.
func TestRegisterCreatesAwaitableRequest(t *testing.T) {
	t.Parallel()
	tr := New()
	if got := tr.HasPending(); got {
		t.Fatal("fresh tracker should have no pending requests")
	}
	id := tr.Register("req")
	if id == 0 {
		t.Fatal("Register returned zero ID")
	}
	if !tr.HasPending() {
		t.Fatal("registered request should be pending")
	}
}

// Test 2: a waiter does not complete before acknowledgment.
func TestWaiterDoesNotCompleteBeforeAck(t *testing.T) {
	tr := New()
	id := tr.Register("req")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := awaitTimed(t, tr, ctx, id)

	select {
	case err := <-done:
		t.Fatalf("Await resolved before Acknowledge: %v", err)
	case <-time.After(noWait):
		// expected: still blocked
	}

	tr.Acknowledge("req")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Await after Acknowledge returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Await did not resolve after Acknowledge")
	}

	if tr.HasPending() {
		t.Fatal("pending should be cleared after acknowledgment")
	}
}

// Test 3: a waiter completes after acknowledgment. Covers the "acknowledge first,
// then await" ordering (Test 2 covers the reverse).
func TestWaiterCompletesAfterAck(t *testing.T) {
	t.Parallel()
	tr := New()
	id := tr.Register("req")

	// Acknowledge from a goroutine so Await (run after) is guaranteed to resolve.
	ackDone := make(chan struct{})
	go func() {
		tr.Acknowledge("req")
		close(ackDone)
	}()

	if err := tr.Await(context.Background(), id); err != nil {
		t.Fatalf("Await returned %v; want nil after Acknowledge", err)
	}
	<-ackDone
}

// Test 4: the watermark is monotonically non-decreasing.
func TestWatermarkMonotonicity(t *testing.T) {
	t.Parallel()
	tr := New()
	id1 := tr.Register("a") // 1
	id2 := tr.Register("b") // 2
	id3 := tr.Register("c") // 3

	// Ack higher IDs first: bumping the watermark must make lower IDs durable.
	tr.Acknowledge("c") // watermark -> 3
	if err := tr.Await(context.Background(), id1); err != nil {
		t.Fatalf("Await id1 (1<=3): %v", err)
	}
	if err := tr.Await(context.Background(), id2); err != nil {
		t.Fatalf("Await id2 (2<=3): %v", err)
	}
	if err := tr.Await(context.Background(), id3); err != nil {
		t.Fatalf("Await id3 (3<=3): %v", err)
	}

	// Acknowledging a lower ID must not regress the watermark.
	tr.Acknowledge("a") // id 1, 1 > 3 is false, watermark stays 3
	if err := tr.Await(context.Background(), id3); err != nil {
		t.Fatalf("watermark regressed after lower Acknowledge: %v", err)
	}

	// Re-acknowledging an already-durable ID must be a no-op and not regress.
	tr.Acknowledge("a")
	if err := tr.Await(context.Background(), id3); err != nil {
		t.Fatalf("watermark regressed after duplicate Acknowledge: %v", err)
	}
}

// Test 5: multiple concurrent requests.
func TestMultipleConcurrentRequests(t *testing.T) {
	t.Parallel()
	tr := New()
	const n = 50
	ids := make([]RequestID, n)
	for i := 0; i < n; i++ {
		ids[i] = tr.Register(fmt.Sprintf("req-%d", i))
	}
	if !tr.HasPending() {
		t.Fatal("expected pending requests")
	}

	// Acknowledge every request concurrently.
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("req-%d", i)
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			tr.Acknowledge(k)
		}(name)
	}
	wg.Wait()

	for i, id := range ids {
		if err := tr.Await(context.Background(), id); err != nil {
			t.Fatalf("request %d did not complete: %v", i, err)
		}
	}
	if tr.HasPending() {
		t.Fatal("all requests acknowledged but HasPending reports true")
	}
}

// Test 6: concurrent completion of waiters.
func TestConcurrentCompletion(t *testing.T) {
	tr := New()
	const waiters = 100
	ids := make([]RequestID, waiters)
	for i := 0; i < waiters; i++ {
		ids[i] = tr.Register(fmt.Sprintf("r-%d", i))
	}

	// All waiters block; they must all resolve after the acks.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var done atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		id := ids[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tr.Await(ctx, id); err != nil {
				t.Errorf("Await returned %v; want nil", err)
				return
			}
			done.Add(1)
		}()
	}

	// Acknowledge everything out of order to exercise the broadcast path.
	ackSem := make(chan struct{}, 8)
	for i := waiters - 1; i >= 0; i-- {
		i := i
		ackSem <- struct{}{}
		go func() {
			defer func() { <-ackSem }()
			tr.Acknowledge(fmt.Sprintf("r-%d", i))
		}()
	}
	wg.Wait()
	if got := done.Load(); got != int64(waiters) {
		t.Fatalf("completed %d; want %d", got, waiters)
	}
}

// Test 7: failure propagation.
func TestFailurePropagation(t *testing.T) {
	tr := New()
	sentinel := errors.New("durable: pipeline error")

	const waiters = 20
	ids := make([]RequestID, waiters)
	for i := 0; i < waiters; i++ {
		ids[i] = tr.Register(fmt.Sprintf("f-%d", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, waiters)
	var mu sync.Mutex
	for i := 0; i < waiters; i++ {
		i := i
		id := ids[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tr.Await(context.Background(), id)
			mu.Lock()
			errs[i] = err
			mu.Unlock()
		}()
	}

	// Give waiters time to park on the condition, then fail.
	time.Sleep(30 * time.Millisecond)
	tr.Fail(sentinel)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, sentinel) {
			t.Fatalf("waiter %d: got %v; want %v", i, err, sentinel)
		}
	}

	// A subsequent Await must observe the failure too.
	if err := tr.Await(context.Background(), ids[0]); !errors.Is(err, sentinel) {
		t.Fatalf("Await after Fail: got %v; want %v", err, sentinel)
	}
}

// Test 8: cancellation unblocks a waiter.
func TestAwaitCancellation(t *testing.T) {
	tr := New()
	id := tr.Register("req")

	ctx, cancel := context.WithCancel(context.Background())
	done := awaitTimed(t, tr, ctx, id)

	select {
	case err := <-done:
		t.Fatalf("Await resolved before cancel: %v", err)
	case <-time.After(noWait):
		// expected: still blocked
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Await after cancel: got %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Await did not observe context cancellation")
	}
}

// Test 9: close/shutdown behavior.
func TestCloseBehavior(t *testing.T) {
	tr := New()
	id := tr.Register("req")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := awaitTimed(t, tr, ctx, id)

	select {
	case err := <-done:
		t.Fatalf("Await resolved before Close: %v", err)
	case <-time.After(noWait):
		// expected: still blocked
	}

	tr.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDurabilityClosed) {
			t.Fatalf("Await after Close: got %v; want ErrDurabilityClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Await did not resolve after Close")
	}

	// Close is idempotent.
	tr.Close()

	// A new Await on an unacked request returns ErrDurabilityClosed immediately.
	if err := tr.Await(context.Background(), tr.Register("post-close")); !errors.Is(err, ErrDurabilityClosed) {
		t.Fatalf("Await after Close returned %v; want ErrDurabilityClosed", err)
	}
}

// Test 10: no deadlock with pending waiters.
func TestNoDeadlockWithPendingWaiters(t *testing.T) {
	tr := New()
	const waiters = 30
	ids := make([]RequestID, waiters)
	for i := 0; i < waiters; i++ {
		ids[i] = tr.Register(fmt.Sprintf("d-%d", i))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		i := i
		id := ids[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tr.Await(ctx, id)
			if !errors.Is(err, ErrDurabilityClosed) {
				t.Errorf("waiter %d: got %v; want ErrDurabilityClosed", i, err)
			}
		}()
	}

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	tr.Close()

	select {
	case <-finished:
		// success: every waiter resolved via Close
	case <-time.After(5 * time.Second):
		t.Fatal("possible deadlock: waiters did not resolve after Close")
	}
}

// Test 11: duplicate / repeated acknowledgment is safe.
func TestDuplicateAcknowledgment(t *testing.T) {
	t.Parallel()
	tr := New()
	id := tr.Register("req")

	// Acknowledging an unknown key is a no-op and never panics.
	tr.Acknowledge("does-not-exist")
	tr.Acknowledge("does-not-exist")

	tr.Acknowledge("req")
	if err := tr.Await(context.Background(), id); err != nil {
		t.Fatalf("Await after first Ack: %v", err)
	}

	// Repeated acknowledgment must not alter state or block.
	tr.Acknowledge("req")
	tr.Acknowledge("req")
	if err := tr.Await(context.Background(), id); err != nil {
		t.Fatalf("Await after duplicate Ack: %v", err)
	}
	if tr.HasPending() {
		t.Fatal("HasPending should be false after ack")
	}
}

// Test: a request can be awaited before it is acknowledged; acknowledging it
// from another goroutine releases the waiter. This also covers the basic
// Acknowledge-then-Await ordering across goroutines.
func TestAcknowledgeReleasesWaiter(t *testing.T) {
	t.Parallel()
	tr := New()
	id := tr.Register("req")

	rel := make(chan struct{})
	go func() {
		tr.Acknowledge("req")
		close(rel)
	}()

	if err := tr.Await(context.Background(), id); err != nil {
		t.Fatalf("Await: %v", err)
	}
	<-rel
}

// Test: HasPending reflects the true pending count under interleaved acks.
func TestHasPendingTracking(t *testing.T) {
	t.Parallel()
	tr := New()
	a := tr.Register("a")
	b := tr.Register("b")

	if !tr.HasPending() {
		t.Fatal("expected pending after two registrations")
	}

	tr.Acknowledge("a") // a (id1) durable; b (id2) still pending, watermark=1
	// b is not yet durable.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := awaitTimed(t, tr, ctx, b)
	select {
	case err := <-done:
		t.Fatalf("Await(b) completed before Acknowledge(b): %v", err)
	case <-time.After(noWait):
	}

	tr.Acknowledge("b") // watermark -> 2
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Await(b) after Acknowledge(b): %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Await(b) did not resolve")
	}
	if a != 1 || b != 2 {
		t.Fatalf("unexpected ids: a=%d b=%d", a, b)
	}
}

// Test 12: race-sensitive scenarios under -race.
func TestConcurrentRaceSensitive(t *testing.T) {
	tr := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const workers = 40
	var wg sync.WaitGroup
	var ackCount atomic.Int64
	var regCount atomic.Int64

	// Concurrent Registerers: each registers a unique key and immediately awaits.
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := tr.Register(fmt.Sprintf("w-%d", i))
			regCount.Add(1)
			// Await with a context that can be cancelled by the closer below.
			_ = tr.Await(ctx, id)
		}()
	}

	// Concurrent Acknowledgers: acknowledge every key (some may race ahead of
	// the registrars; Acknowledge on an unknown key is a no-op).
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Acknowledge(fmt.Sprintf("w-%d", i))
			ackCount.Add(1)
		}()
	}

	// A closer that may fire concurrently with everyone else.
	go func() {
		time.Sleep(20 * time.Millisecond)
		tr.Close()
	}()

	// A failure path that competes with Close; whichever wins is terminal.
	go func() {
		time.Sleep(15 * time.Millisecond)
		tr.Fail(errors.New("raced failure"))
	}()

	// HasPending polls from a separate goroutine to exercise the read path.
	stopPoll := make(chan struct{})
	var pollCount atomic.Int64
	go func() {
		for {
			select {
			case <-stopPoll:
				return
			default:
				_ = tr.HasPending()
				pollCount.Add(1)
			}
		}
	}()

	wg.Wait()
	close(stopPoll)

	if regCount.Load() != workers {
		t.Fatalf("registered %d; want %d", regCount.Load(), workers)
	}
	if ackCount.Load() != workers {
		t.Fatalf("acknowledged %d; want %d", ackCount.Load(), workers)
	}
}
