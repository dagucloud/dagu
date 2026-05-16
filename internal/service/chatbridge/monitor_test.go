// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeNotificationTransport struct {
	destinations []string
	flushFn      func(context.Context, string, NotificationBatch, bool) bool
}

func (f *fakeNotificationTransport) NotificationDestinations() []string {
	return append([]string(nil), f.destinations...)
}

func (f *fakeNotificationTransport) FlushNotificationBatch(ctx context.Context, destination string, batch NotificationBatch, allowLLM bool) bool {
	if f.flushFn == nil {
		return true
	}
	return f.flushFn(ctx, destination, batch, allowLLM)
}

type fakeRoutingNotificationTransport struct {
	*fakeNotificationTransport
	routeFn func(NotificationEvent) []string
}

func (f *fakeRoutingNotificationTransport) NotificationDestinationsForEvent(event NotificationEvent) []string {
	if f.routeFn == nil {
		return nil
	}
	return f.routeFn(event)
}

type fakePolicyNotificationTransport struct {
	*fakeNotificationTransport
	shouldDeliverFn func(NotificationBatch) bool
}

func (f *fakePolicyNotificationTransport) ShouldDeliverNotificationBatch(batch NotificationBatch) bool {
	if f.shouldDeliverFn == nil {
		return true
	}
	return f.shouldDeliverFn(batch)
}

type stubNotificationStore struct {
	mu        sync.Mutex
	events    []*eventstore.Event
	failHead  bool
	readErr   error
	headCalls int
	readCalls int
}

var _ eventstore.Store = (*stubNotificationStore)(nil)
var _ eventstore.NotificationReader = (*stubNotificationStore)(nil)

func (s *stubNotificationStore) Emit(_ context.Context, event *eventstore.Event) error {
	if event == nil {
		return nil
	}
	event.Normalize()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *stubNotificationStore) Query(context.Context, eventstore.QueryFilter) (*eventstore.QueryResult, error) {
	return &eventstore.QueryResult{}, nil
}

func (s *stubNotificationStore) NotificationHeadCursor(context.Context) (eventstore.NotificationCursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.headCalls++
	if s.failHead {
		return eventstore.NotificationCursor{}, errors.New("head unavailable")
	}
	return s.currentCursorLocked(), nil
}

func (s *stubNotificationStore) ReadNotificationEvents(_ context.Context, cursor eventstore.NotificationCursor) ([]*eventstore.Event, eventstore.NotificationCursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.readCalls++
	if s.readErr != nil {
		return nil, cursor, s.readErr
	}

	index := int(cursor.Normalize().CommittedOffsets["events"])
	if index < 0 || index > len(s.events) {
		index = 0
	}
	events := append([]*eventstore.Event(nil), s.events[index:]...)
	return events, s.currentCursorLocked(), nil
}

func (s *stubNotificationStore) currentCursorLocked() eventstore.NotificationCursor {
	return eventstore.NotificationCursor{
		CommittedOffsets: map[string]int64{"events": int64(len(s.events))},
	}
}

func (s *stubNotificationStore) setHeadFailure(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failHead = fail
}

func (s *stubNotificationStore) stats() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headCalls, s.readCalls
}

func TestNotificationMonitor_ShutdownDrainRetriesInFlightBatchWithoutLLM(t *testing.T) {
	t.Parallel()

	type call struct {
		destination string
		allowLLM    bool
	}

	var (
		mu    sync.Mutex
		calls []call
	)
	firstCall := make(chan struct{}, 1)
	secondCall := make(chan struct{}, 1)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(ctx context.Context, destination string, _ NotificationBatch, allowLLM bool) bool {
			mu.Lock()
			calls = append(calls, call{destination: destination, allowLLM: allowLLM})
			callCount := len(calls)
			mu.Unlock()

			if callCount == 1 {
				firstCall <- struct{}{}
				<-ctx.Done()
				return false
			}
			secondCall <- struct{}{}
			return true
		},
	}
	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond
	cfg.FlushTimeout = time.Second
	cfg.PollInterval = time.Hour
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(nil, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Failed,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Error:     "boom",
	}
	require.True(t, monitor.NotifyCompletion(status))

	select {
	case <-firstCall:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first flush attempt")
	}

	cancel()

	select {
	case <-secondCall:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shutdown retry")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for monitor shutdown")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 2)
	assert.Equal(t, call{destination: "dest-1", allowLLM: true}, calls[0])
	assert.Equal(t, call{destination: "dest-1", allowLLM: false}, calls[1])
	assert.True(t, monitor.IsDelivered("dest-1", status))
}

func TestNotificationMonitor_NotifyCompletionSkipsFailedRunWithAutoRetryRemaining(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls int
	)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, _ string, _ NotificationBatch, _ bool) bool {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return true
		},
	}

	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.PollInterval = time.Hour
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(nil, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	status := &exec.DAGRunStatus{
		Name:           "briefing",
		Status:         core.Failed,
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Error:          "boom",
		AutoRetryCount: 0,
		AutoRetryLimit: 2,
	}
	require.False(t, monitor.NotifyCompletion(status))

	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0 || monitor.IsDelivered("dest-1", status)
	}, 50*time.Millisecond, 5*time.Millisecond)
}

func TestNotificationMonitor_PollSourceRoutesEventsPerDestination(t *testing.T) {
	t.Parallel()

	type call struct {
		destination string
		allowLLM    bool
	}

	store := &stubNotificationStore{}
	service := eventstore.New(store)
	var (
		mu    sync.Mutex
		calls []call
	)
	transport := &fakeRoutingNotificationTransport{
		fakeNotificationTransport: &fakeNotificationTransport{
			destinations: []string{"dest-a", "dest-b"},
			flushFn: func(_ context.Context, destination string, _ NotificationBatch, allowLLM bool) bool {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, call{destination: destination, allowLLM: allowLLM})
				return true
			},
		},
		routeFn: func(event NotificationEvent) []string {
			if event.Status == nil {
				return nil
			}
			switch event.Status.Name {
			case "dag-a":
				return []string{"dest-a"}
			case "dag-b":
				return []string{"dest-b"}
			default:
				return nil
			}
		},
	}
	cfg := DefaultNotificationMonitorConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond

	monitor := NewNotificationMonitor(service, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		headCalls, _ := store.stats()
		return headCalls > 0
	}, time.Second, 10*time.Millisecond)

	for _, status := range []*exec.DAGRunStatus{
		{Name: "dag-a", Status: core.Failed, DAGRunID: "run-a", AttemptID: "attempt-a"},
		{Name: "dag-b", Status: core.Failed, DAGRunID: "run-b", AttemptID: "attempt-b"},
	} {
		require.NoError(t, store.Emit(context.Background(), eventstore.NewDAGRunEvent(
			eventstore.Source{Service: eventstore.SourceServiceServer},
			eventstore.TypeDAGRunFailed,
			status,
			nil,
		)))
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	destinations := []string{calls[0].destination, calls[1].destination}
	assert.ElementsMatch(t, []string{"dest-a", "dest-b"}, destinations)
}

func TestNotificationMonitor_RequeuePendingDropsFailedRunWithAutoRetryRemaining(t *testing.T) {
	t.Parallel()

	transport := &fakeNotificationTransport{destinations: []string{"dest-1"}}
	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.PollInterval = time.Hour
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(nil, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	status := &exec.DAGRunStatus{
		Name:           "briefing",
		Status:         core.Failed,
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Error:          "boom",
		AutoRetryCount: 0,
		AutoRetryLimit: 2,
	}
	event := testNotificationEvent(status)
	monitor.state = newNotificationMonitorState()
	monitor.state.Bootstrapped = true
	monitor.state.Destinations["dest-1"] = &notificationDestinationState{
		Pending: map[string]NotificationEvent{
			event.Key: event,
		},
		Delivered: make(map[string]time.Time),
	}

	monitor.requeuePending(context.Background(), []string{"dest-1"})

	monitor.stateMu.Lock()
	assert.Empty(t, monitor.state.Destinations["dest-1"].Pending)
	monitor.stateMu.Unlock()
	require.Never(t, func() bool {
		return len(monitor.currentBatcher().TakeReady()) > 0
	}, 50*time.Millisecond, 5*time.Millisecond)

	status.AutoRetryCount = status.AutoRetryLimit
	require.True(t, monitor.NotifyCompletion(status))

	ready := waitForReadyBatch(t, monitor.currentBatcher())
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, status.AutoRetryLimit, ready.Batch.Events[0].Status.AutoRetryCount)
}

func TestNotificationMonitor_BootstrapFailureDoesNotReplayFromZeroCursor(t *testing.T) {
	t.Parallel()

	store := &stubNotificationStore{failHead: true}
	service := eventstore.New(store)

	var (
		mu        sync.Mutex
		delivered []string
	)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, _ string, batch NotificationBatch, _ bool) bool {
			mu.Lock()
			defer mu.Unlock()
			for _, event := range batch.Events {
				if event.Status != nil {
					delivered = append(delivered, event.Status.DAGRunID)
				}
			}
			return true
		},
	}

	cfg := DefaultNotificationMonitorConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(service, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	oldStatus := &exec.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-old",
		AttemptID:  "attempt-old",
		Status:     core.Failed,
		Error:      "old failure",
		FinishedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		oldStatus,
		nil,
	)))

	require.Eventually(t, func() bool {
		headCalls, readCalls := store.stats()
		return headCalls > 0 && readCalls == 0
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	assert.Empty(t, delivered)
	mu.Unlock()

	store.setHeadFailure(false)
	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		return monitor.state.Bootstrapped
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	assert.Empty(t, delivered)
	mu.Unlock()

	newStatus := &exec.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-new",
		AttemptID:  "attempt-new",
		Status:     core.Failed,
		Error:      "new failure",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		newStatus,
		nil,
	)))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 1 && delivered[0] == "run-new"
	}, time.Second, 10*time.Millisecond)
}

func TestNotificationMonitor_ShutdownDrainFlushesPendingBatchWithoutLLM(t *testing.T) {
	t.Parallel()

	type call struct {
		destination string
		allowLLM    bool
	}

	var (
		mu    sync.Mutex
		calls []call
	)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, destination string, _ NotificationBatch, allowLLM bool) bool {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, call{destination: destination, allowLLM: allowLLM})
			return true
		},
	}
	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = time.Hour
	cfg.SuccessWindow = time.Hour
	cfg.FlushTimeout = time.Second
	cfg.PollInterval = time.Hour
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(nil, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Failed,
		DAGRunID:  "run-2",
		AttemptID: "attempt-2",
		Error:     "boom",
	}
	require.True(t, monitor.NotifyCompletion(status))
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for monitor shutdown")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 1)
	assert.Equal(t, call{destination: "dest-1", allowLLM: false}, calls[0])
	assert.True(t, monitor.IsDelivered("dest-1", status))
}

func TestNotificationMonitor_SuccessEventsAreAcknowledgedWithoutDelivery(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls []string
	)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, destination string, _ NotificationBatch, _ bool) bool {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, destination)
			return true
		},
	}

	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond
	cfg.PollInterval = time.Hour
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(nil, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	first := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Succeeded,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
	}
	second := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Succeeded,
		DAGRunID:  "run-2",
		AttemptID: "attempt-2",
	}

	require.True(t, monitor.NotifyCompletion(first))
	require.True(t, monitor.NotifyCompletion(second))

	require.Eventually(t, func() bool {
		return monitor.IsDelivered("dest-1", first) && monitor.IsDelivered("dest-1", second)
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, calls, "successful completions should be acknowledged without transport delivery")
}

func TestNotificationMonitor_SuccessEventsCanBeDeliveredByOptInTransport(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls []NotificationBatch
	)
	transport := &fakePolicyNotificationTransport{
		fakeNotificationTransport: &fakeNotificationTransport{
			destinations: []string{"dest-1"},
			flushFn: func(_ context.Context, _ string, batch NotificationBatch, _ bool) bool {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, batch)
				return true
			},
		},
		shouldDeliverFn: func(batch NotificationBatch) bool {
			return batch.Class == NotificationClassSuccessDigest
		},
	}

	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond
	cfg.PollInterval = time.Hour
	cfg.SeenEvictInterval = time.Hour

	monitor := NewNotificationMonitor(nil, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Succeeded,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
	}
	require.True(t, monitor.NotifyCompletion(status))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 1
	}, time.Second, 10*time.Millisecond)
	assert.True(t, monitor.IsDelivered("dest-1", status))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 1)
	assert.Equal(t, NotificationClassSuccessDigest, calls[0].Class)
	require.Len(t, calls[0].Events, 1)
	assert.Equal(t, eventstore.TypeDAGRunSucceeded, calls[0].Events[0].Type)
}

func TestNotificationMonitor_PollSourceFiltersInterestedEventTypes(t *testing.T) {
	t.Parallel()

	store := &stubNotificationStore{}
	service := eventstore.New(store)
	transport := &fakeNotificationTransport{destinations: []string{"dest-1"}}

	cfg := DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = 5 * time.Millisecond
	cfg.SuccessWindow = 5 * time.Millisecond
	cfg.InterestedEventTypes = []eventstore.EventType{eventstore.TypeDAGRunRunning}

	monitor := NewNotificationMonitor(service, "", transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	monitor.initializeSession(context.Background())

	queued := &exec.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Queued,
		QueuedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	running := *queued
	running.Status = core.Running
	running.StartedAt = time.Now().UTC().Format(time.RFC3339)

	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunQueued,
		queued,
		nil,
	)))
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunRunning,
		&running,
		nil,
	)))

	monitor.pollSource(context.Background())

	ready := waitForReadyBatch(t, monitor.currentBatcher())
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, NotificationClassInformational, ready.Batch.Class)
	assert.Equal(t, eventstore.TypeDAGRunRunning, ready.Batch.Events[0].Type)
	assert.Equal(t, core.Running, ready.Batch.Events[0].Status.Status)
}
