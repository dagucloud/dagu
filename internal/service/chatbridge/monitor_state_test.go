// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingBootstrapStateStore struct{}

func (failingBootstrapStateStore) Load(context.Context) ([]byte, bool, error) {
	return nil, false, nil
}

func (failingBootstrapStateStore) Save(context.Context, []byte) error {
	return errors.New("save failed")
}

func (failingBootstrapStateStore) Quarantine(context.Context) (string, error) {
	return "", nil
}

type blockingHeadStore struct {
	*stubNotificationStore
	started chan struct{}
	release chan struct{}
}

func (s *blockingHeadStore) DAGRunHeadCursor(ctx context.Context) (eventstore.DAGRunCursor, error) {
	close(s.started)
	select {
	case <-ctx.Done():
		return eventstore.DAGRunCursor{}, ctx.Err()
	case <-s.release:
		return s.stubNotificationStore.DAGRunHeadCursor(ctx)
	}
}

func newTestNotificationMonitorConfig() NotificationMonitorConfig {
	cfg := DefaultNotificationMonitorConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour
	return cfg
}

func newFileBackedMonitor(
	eventService *eventstore.Service,
	stateFile string,
	transport NotificationTransport,
	logger *slog.Logger,
	cfg NotificationMonitorConfig,
) *NotificationMonitor {
	return NewNotificationMonitor(
		eventService,
		filemonitor.NewStateStore(stateFile),
		filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: DefaultNotificationLockStaleThreshold,
			RetryInterval:  DefaultNotificationLockRetryInterval,
		}),
		transport,
		logger,
		cfg,
	)
}

func notificationMonitorEventuallyTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 3
	}
	return base
}

func requireNotificationMonitorShutdown(t *testing.T, name string, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(notificationMonitorEventuallyTimeout(2 * time.Second)):
		t.Fatalf("timed out waiting for %s shutdown", name)
	}
}

func TestNotificationMonitor_BootstrapsFromCurrentHeadAndOnlyDeliversFutureEvents(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := fileeventstore.New(baseDir)
	require.NoError(t, err)
	service := eventstore.New(store)

	oldStatus := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-old",
		AttemptID:  "attempt-old",
		Status:     ir.Failed,
		Error:      "old failure",
		FinishedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		oldStatus,
		nil,
	)))

	var (
		mu        sync.Mutex
		delivered []NotificationEvent
	)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, _ string, batch NotificationBatch, _ bool) bool {
			mu.Lock()
			defer mu.Unlock()
			for _, event := range batch.Events {
				if event.Status != nil {
					delivered = append(delivered, cloneNotificationEvent(event))
				}
			}
			return true
		},
	}

	cfg := newTestNotificationMonitorConfig()
	monitor := newFileBackedMonitor(service, filepath.Join(t.TempDir(), "state.json"), transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()
	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		bootstrapped := monitor.state.Bootstrapped
		monitor.stateMu.Unlock()
		return monitor.ownsNotificationLock() && monitor.notificationSessionActive() && bootstrapped
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	newStatus := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-new",
		AttemptID:  "attempt-new",
		Status:     ir.Failed,
		Error:      "new failure",
		Labels:     []string{"workspace=ops", "team=platform"},
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		newStatus,
		map[string]any{eventstore.DAGFileNameDataKey: "briefing-file"},
	)))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 1
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)
	mu.Lock()
	deliveredEvent := cloneNotificationEvent(delivered[0])
	mu.Unlock()
	require.NotNil(t, deliveredEvent.Status)
	assert.Equal(t, "run-new", deliveredEvent.Status.DAGRunID)
	assert.Equal(t, "briefing-file", deliveredEvent.DAGFile)
	assert.Equal(t, []string{"workspace=ops", "team=platform"}, deliveredEvent.Status.Labels)

	require.Eventually(t, func() bool {
		return !monitor.IsDelivered("dest-1", oldStatus) && monitor.IsDelivered("dest-1", newStatus)
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)
}

func TestNotificationMonitor_CompetingBootstrapPreservesStartupEvent(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := fileeventstore.New(baseDir)
	require.NoError(t, err)
	service := eventstore.New(store)

	delivered := make(chan string, 1)
	transport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, _ string, batch NotificationBatch, _ bool) bool {
			for _, event := range batch.Events {
				if event.Status != nil {
					delivered <- event.Status.DAGRunID
				}
			}
			return true
		},
	}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	earlierMonitor := newFileBackedMonitor(service, stateFile, transport, logger, newTestNotificationMonitorConfig())
	require.NoError(t, earlierMonitor.Bootstrap(context.Background()))

	status := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-startup",
		AttemptID:  "attempt-startup",
		Status:     ir.Failed,
		Error:      "startup failure",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceScheduler, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		status,
		nil,
	)))

	laterMonitor := newFileBackedMonitor(service, stateFile, transport, logger, newTestNotificationMonitorConfig())
	require.NoError(t, laterMonitor.Bootstrap(context.Background()))
	stopLaterMonitor := testutil.StartContextRunner(t, laterMonitor)
	defer stopLaterMonitor()

	select {
	case runID := <-delivered:
		assert.Equal(t, status.DAGRunID, runID)
	case <-time.After(notificationMonitorEventuallyTimeout(time.Second)):
		t.Fatal("timed out waiting for startup event delivery")
	}
}

func TestNotificationMonitor_ConcurrentBootstrapCapturesHeadOnce(t *testing.T) {
	t.Parallel()

	store := &stubNotificationStore{}
	service := eventstore.New(store)
	stateFile := filepath.Join(t.TempDir(), "state.json")
	transport := &fakeNotificationTransport{destinations: []string{"dest-1"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitors := []*NotificationMonitor{
		newFileBackedMonitor(service, stateFile, transport, logger, newTestNotificationMonitorConfig()),
		newFileBackedMonitor(service, stateFile, transport, logger, newTestNotificationMonitorConfig()),
	}

	ctx, cancel := context.WithTimeout(context.Background(), notificationMonitorEventuallyTimeout(time.Second))
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, len(monitors))
	for _, monitor := range monitors {
		go func() {
			<-start
			errs <- monitor.Bootstrap(ctx)
		}()
	}
	close(start)

	for range monitors {
		require.NoError(t, <-errs)
	}
	headCalls, _ := store.stats()
	assert.Equal(t, 1, headCalls)
}

func TestNotificationMonitor_BootstrapLeaseCannotBeReplaced(t *testing.T) {
	t.Parallel()

	store := &blockingHeadStore{
		stubNotificationStore: &stubNotificationStore{},
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	lockOpts := &dirlock.LockOptions{
		StaleThreshold: time.Millisecond,
		RetryInterval:  DefaultNotificationLockRetryInterval,
	}
	lease := filemonitor.NewLease(stateFile, lockOpts)
	monitor := NewNotificationMonitor(
		eventstore.New(store),
		filemonitor.NewStateStore(stateFile),
		lease,
		&fakeNotificationTransport{destinations: []string{"dest-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		newTestNotificationMonitorConfig(),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- monitor.Bootstrap(context.Background())
	}()
	<-store.started
	stale := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(lease.Location(), dirlock.LockDirectoryName), stale, stale))
	replacement := filemonitor.NewLease(stateFile, lockOpts)
	replacementErr := replacement.TryLock()
	defer func() {
		require.NoError(t, replacement.Unlock())
	}()
	close(store.release)

	bootstrapErr := <-errCh
	require.ErrorIs(t, replacementErr, dirlock.ErrLockConflict)
	require.NoError(t, bootstrapErr)
	assert.True(t, newNotificationStateStore(filemonitor.NewStateStore(stateFile)).IsBootstrapped(context.Background()))
}

func TestNotificationMonitor_BootstrapReturnsErrors(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	transport := &fakeNotificationTransport{destinations: []string{"dest-1"}}

	t.Run("head", func(t *testing.T) {
		store := &stubNotificationStore{failHead: true}
		stateFile := filepath.Join(t.TempDir(), "state.json")
		monitor := newFileBackedMonitor(eventstore.New(store), stateFile, transport, logger, newTestNotificationMonitorConfig())

		err := monitor.Bootstrap(context.Background())

		require.ErrorContains(t, err, "capture notification bootstrap cursor")
		assert.False(t, newNotificationStateStore(filemonitor.NewStateStore(stateFile)).IsBootstrapped(context.Background()))
	})

	t.Run("save", func(t *testing.T) {
		monitor := NewNotificationMonitor(
			eventstore.New(&stubNotificationStore{}),
			failingBootstrapStateStore{},
			nil,
			transport,
			logger,
			newTestNotificationMonitorConfig(),
		)

		err := monitor.Bootstrap(context.Background())

		require.ErrorContains(t, err, "persist notification bootstrap state")
	})

	t.Run("lease", func(t *testing.T) {
		blockedPath := filepath.Join(t.TempDir(), "blocked")
		require.NoError(t, os.WriteFile(blockedPath, []byte("file"), 0o600))
		stateFile := filepath.Join(t.TempDir(), "state.json")
		leaseFile := filepath.Join(blockedPath, "state.json")
		monitor := NewNotificationMonitor(
			eventstore.New(&stubNotificationStore{}),
			filemonitor.NewStateStore(stateFile),
			filemonitor.NewLease(leaseFile, &dirlock.LockOptions{}),
			transport,
			logger,
			newTestNotificationMonitorConfig(),
		)

		err := monitor.Bootstrap(context.Background())

		require.ErrorContains(t, err, "acquire notification bootstrap lock")
	})
}

func TestNotificationMonitor_RestartRequeuesPersistedPending(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := newTestNotificationMonitorConfig()

	status := &ir.DAGRunStatus{
		Name:      "briefing",
		Labels:    []string{"workspace=ops"},
		Status:    ir.Failed,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Error:     "boom",
	}
	state := newNotificationMonitorState()
	state.Bootstrapped = true
	state.Destinations["dest-1"] = &notificationDestinationState{
		Pending: map[string]NotificationEvent{
			NotificationSeenKey(status): {
				Key:        NotificationSeenKey(status),
				Status:     cloneNotificationStatus(status),
				DAGFile:    "briefing-file",
				ObservedAt: time.Now().UTC(),
			},
		},
		Delivered: make(map[string]time.Time),
	}
	require.NoError(t, newNotificationStateStore(filemonitor.NewStateStore(stateFile)).Save(context.Background(), state))

	var (
		mu    sync.Mutex
		calls int
	)
	secondTransport := &fakeNotificationTransport{
		destinations: []string{"dest-1"},
		flushFn: func(_ context.Context, destination string, batch NotificationBatch, _ bool) bool {
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, "dest-1", destination)
			require.Len(t, batch.Events, 1)
			assert.Equal(t, "run-1", batch.Events[0].Status.DAGRunID)
			assert.Equal(t, "briefing-file", batch.Events[0].DAGFile)
			assert.Equal(t, []string{"workspace=ops"}, batch.Events[0].Status.Labels)
			calls++
			return true
		},
	}

	secondMonitor := newFileBackedMonitor(nil, stateFile, secondTransport, logger, cfg)
	stopMonitor := testutil.StartContextRunner(t, secondMonitor)
	defer stopMonitor()
	require.Eventually(t, func() bool {
		mu.Lock()
		called := calls
		mu.Unlock()
		return called >= 1 && secondMonitor.IsDelivered("dest-1", status)
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, calls, 1)
	assert.True(t, secondMonitor.IsDelivered("dest-1", status))
}

func TestNotificationMonitor_BoundsFailedDeliveryBacklog(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
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
			return false
		},
	}
	cfg := newTestNotificationMonitorConfig()
	cfg.PendingLimit = 2
	monitor := newFileBackedMonitor(nil, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()
	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		return monitor.state.Bootstrapped
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	base := time.Now().UTC()
	events := make([]NotificationEvent, 0, 3)
	for i := 1; i <= 3; i++ {
		status := &ir.DAGRunStatus{
			Name:      "briefing",
			DAGRunID:  fmt.Sprintf("run-%d", i),
			AttemptID: fmt.Sprintf("attempt-%d", i),
			Status:    ir.Failed,
			Error:     "boom",
		}
		event := testNotificationEvent(status)
		event.ObservedAt = base.Add(time.Duration(i) * time.Second)
		events = append(events, event)
	}
	require.True(t, monitor.enqueueEvents(context.Background(), nil, events))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	result := newNotificationStateStore(filemonitor.NewStateStore(stateFile)).Load(context.Background())
	require.NoError(t, result.Warning)
	pending := result.State.Destinations["dest-1"].Pending
	require.Len(t, pending, 2)
	assert.NotContains(t, pending, events[0].Key)
	assert.Contains(t, pending, events[1].Key)
	assert.Contains(t, pending, events[2].Key)
}

func TestNotificationMonitor_TrimsPersistedBacklogBeforeRequeue(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	base := time.Now().UTC()
	events := []NotificationEvent{
		{Key: "event-a", Status: &ir.DAGRunStatus{Name: "a", DAGRunID: "run-a", AttemptID: "a1", Status: ir.Failed}, ObservedAt: base},
		{Key: "event-b", Status: &ir.DAGRunStatus{Name: "b", DAGRunID: "run-b", AttemptID: "a1", Status: ir.Failed}, ObservedAt: base},
		{Key: "event-c", Status: &ir.DAGRunStatus{Name: "c", DAGRunID: "run-c", AttemptID: "a1", Status: ir.Failed}, ObservedAt: base.Add(time.Second)},
	}
	state := newNotificationMonitorState()
	state.Bootstrapped = true
	state.Destinations["dest-1"] = &notificationDestinationState{
		Pending: map[string]NotificationEvent{
			events[0].Key: events[0],
			events[1].Key: events[1],
			events[2].Key: events[2],
		},
		Delivered: make(map[string]time.Time),
	}
	store := newNotificationStateStore(filemonitor.NewStateStore(stateFile))
	require.NoError(t, store.Save(context.Background(), state))

	var logs bytes.Buffer
	cfg := newTestNotificationMonitorConfig()
	cfg.PendingLimit = 2
	cfg.UrgentWindow = time.Hour
	monitor := newFileBackedMonitor(
		nil,
		stateFile,
		&fakeNotificationTransport{destinations: []string{"dest-1"}},
		slog.New(slog.NewTextHandler(&logs, nil)),
		cfg,
	)
	monitor.lock = nil
	defer monitor.currentBatcher().Stop()

	monitor.initializeSession(context.Background())

	result := store.Load(context.Background())
	require.NoError(t, result.Warning)
	pending := result.State.Destinations["dest-1"].Pending
	require.Len(t, pending, 2)
	assert.NotContains(t, pending, "event-a")
	assert.Contains(t, pending, "event-b")
	assert.Contains(t, pending, "event-c")
	assert.Equal(t, 1, strings.Count(logs.String(), "Dropped pending notifications after backlog limit"))
	assert.Contains(t, logs.String(), "destination=dest-1")
	assert.Contains(t, logs.String(), "dropped_count=1")
	assert.Contains(t, logs.String(), "limit=2")
}

func TestNotificationMonitor_StateLockAllowsSingleWriterAndTakeover(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := fileeventstore.New(baseDir)
	require.NoError(t, err)
	service := eventstore.New(store)

	stateFile := filepath.Join(t.TempDir(), "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var (
		mu         sync.Mutex
		deliveries = map[string][]string{
			"monitor-1": {},
			"monitor-2": {},
		}
	)
	newTransport := func(name string) *fakeNotificationTransport {
		return &fakeNotificationTransport{
			destinations: []string{"dest-1"},
			flushFn: func(_ context.Context, _ string, batch NotificationBatch, _ bool) bool {
				mu.Lock()
				defer mu.Unlock()
				for _, event := range batch.Events {
					if event.Status != nil {
						deliveries[name] = append(deliveries[name], event.Status.DAGRunID)
					}
				}
				return true
			},
		}
	}

	monitor1 := newFileBackedMonitor(service, stateFile, newTransport("monitor-1"), logger, newTestNotificationMonitorConfig())
	monitor2 := newFileBackedMonitor(service, stateFile, newTransport("monitor-2"), logger, newTestNotificationMonitorConfig())

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	done1 := make(chan struct{})
	go func() {
		monitor1.Run(ctx1)
		close(done1)
	}()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := make(chan struct{})
	go func() {
		monitor2.Run(ctx2)
		close(done2)
	}()
	defer func() {
		cancel1()
		cancel2()
		requireNotificationMonitorShutdown(t, "monitor-1", done1)
		requireNotificationMonitorShutdown(t, "monitor-2", done2)
	}()

	require.Eventually(t, func() bool {
		switch {
		case monitor1.ownsNotificationLock():
			monitor1.stateMu.Lock()
			bootstrapped := monitor1.state.Bootstrapped
			monitor1.stateMu.Unlock()
			return monitor1.notificationSessionActive() && bootstrapped
		case monitor2.ownsNotificationLock():
			monitor2.stateMu.Lock()
			bootstrapped := monitor2.state.Bootstrapped
			monitor2.stateMu.Unlock()
			return monitor2.notificationSessionActive() && bootstrapped
		default:
			return false
		}
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	firstStatus := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-first",
		AttemptID:  "attempt-first",
		Status:     ir.Failed,
		Error:      "first failure",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		firstStatus,
		nil,
	)))

	var firstOwner string
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		total := len(deliveries["monitor-1"]) + len(deliveries["monitor-2"])
		if total != 1 {
			return false
		}
		switch {
		case len(deliveries["monitor-1"]) == 1:
			firstOwner = "monitor-1"
		case len(deliveries["monitor-2"]) == 1:
			firstOwner = "monitor-2"
		default:
			return false
		}
		return true
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	switch firstOwner {
	case "monitor-1":
		cancel1()
		requireNotificationMonitorShutdown(t, "monitor-1", done1)
		require.Eventually(t, func() bool {
			monitor2.stateMu.Lock()
			bootstrapped := monitor2.state.Bootstrapped
			monitor2.stateMu.Unlock()
			return monitor2.ownsNotificationLock() && monitor2.notificationSessionActive() && bootstrapped
		}, notificationMonitorEventuallyTimeout(2*time.Second), 10*time.Millisecond)
	case "monitor-2":
		cancel2()
		requireNotificationMonitorShutdown(t, "monitor-2", done2)
		require.Eventually(t, func() bool {
			monitor1.stateMu.Lock()
			bootstrapped := monitor1.state.Bootstrapped
			monitor1.stateMu.Unlock()
			return monitor1.ownsNotificationLock() && monitor1.notificationSessionActive() && bootstrapped
		}, notificationMonitorEventuallyTimeout(2*time.Second), 10*time.Millisecond)
	default:
		t.Fatalf("first owner not determined: %q", firstOwner)
	}

	secondStatus := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-second",
		AttemptID:  "attempt-second",
		Status:     ir.Failed,
		Error:      "second failure",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		secondStatus,
		nil,
	)))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		switch firstOwner {
		case "monitor-1":
			return slices.Contains(deliveries["monitor-2"], "run-second")
		case "monitor-2":
			return slices.Contains(deliveries["monitor-1"], "run-second")
		default:
			return false
		}
	}, notificationMonitorEventuallyTimeout(2*time.Second), 10*time.Millisecond)
}

func TestNotificationMonitor_CorruptStateIsQuarantinedAndOnlyFutureEventsAreDelivered(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := fileeventstore.New(baseDir)
	require.NoError(t, err)
	service := eventstore.New(store)

	stateFile := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(stateFile, []byte("{not-json"), 0o600))

	oldStatus := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-old",
		AttemptID:  "attempt-old",
		Status:     ir.Failed,
		Error:      "old failure",
		FinishedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		oldStatus,
		nil,
	)))

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

	monitor := newFileBackedMonitor(service, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		matches, globErr := filepath.Glob(stateFile + ".corrupt.*")
		if globErr != nil {
			return false
		}
		return len(matches) == 1
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)
	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		return monitor.state.Bootstrapped
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	newStatus := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-new",
		AttemptID:  "attempt-new",
		Status:     ir.Failed,
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
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	assert.False(t, monitor.IsDelivered("dest-1", oldStatus))
	require.Eventually(t, func() bool {
		return monitor.IsDelivered("dest-1", newStatus)
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)
}

func TestNotificationStateStore_LoadUnsupportedVersionQuarantinesState(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(stateFile, []byte(`{"version":99}`), 0o600))

	result := newNotificationStateStore(filemonitor.NewStateStore(stateFile)).Load(context.Background())
	require.Error(t, result.Warning)
	assert.True(t, result.Recovered)
	assert.NotEmpty(t, result.QuarantinedPath)
	assert.False(t, result.State.Bootstrapped)

	matches, err := filepath.Glob(stateFile + ".corrupt.*")
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

func TestNotificationMonitor_SaveFailureDoesNotLoseUnreadEvents(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions do not reliably block notification state writes")
	}

	baseDir := t.TempDir()
	store, err := fileeventstore.New(baseDir)
	require.NoError(t, err)
	service := eventstore.New(store)

	stateDir := t.TempDir()
	stateFile := filepath.Join(stateDir, "state.json")

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

	monitor := newFileBackedMonitor(service, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		return monitor.state.Bootstrapped
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	require.NoError(t, os.Chmod(stateDir, 0o500))
	defer func() {
		_ = os.Chmod(stateDir, 0o700)
	}()

	status := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-save-retry",
		AttemptID:  "attempt-save-retry",
		Status:     ir.Failed,
		Error:      "retry failure",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		status,
		nil,
	)))

	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) > 0
	}, 150*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, os.Chmod(stateDir, 0o700))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) == 1 && delivered[0] == "run-save-retry"
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) > 1
	}, 150*time.Millisecond, 10*time.Millisecond)
	assert.True(t, monitor.IsDelivered("dest-1", status))
}

func TestNotificationMonitor_NotifyCompletionSaveFailureDoesNotMutateLiveState(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions do not reliably block notification state writes")
	}

	stateDir := t.TempDir()
	stateFile := filepath.Join(stateDir, "state.json")
	monitor := newFileBackedMonitor(
		nil,
		stateFile,
		&fakeNotificationTransport{destinations: []string{"dest-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		newTestNotificationMonitorConfig(),
	)
	monitor.lock = nil
	monitor.lockDir = ""

	require.NoError(t, os.Chmod(stateDir, 0o500))
	defer func() {
		_ = os.Chmod(stateDir, 0o700)
	}()

	status := &ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-save-fail",
		AttemptID: "attempt-save-fail",
		Status:    ir.Failed,
		Error:     "boom",
	}
	require.False(t, monitor.NotifyCompletion(status))

	monitor.stateMu.Lock()
	defer monitor.stateMu.Unlock()
	destState := monitor.state.Destinations["dest-1"]
	require.Nil(t, destState)
}

func TestNotificationMonitor_MarkBatchDeliveredSaveFailureDoesNotMutateLiveState(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions do not reliably block notification state writes")
	}

	stateDir := t.TempDir()
	stateFile := filepath.Join(stateDir, "state.json")
	monitor := newFileBackedMonitor(
		nil,
		stateFile,
		&fakeNotificationTransport{destinations: []string{"dest-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		newTestNotificationMonitorConfig(),
	)
	monitor.lock = nil
	monitor.lockDir = ""

	status := &ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-ack-save-fail",
		AttemptID: "attempt-ack-save-fail",
		Status:    ir.Succeeded,
	}
	event := NotificationEvent{
		Key:        NotificationSeenKey(status),
		Status:     cloneNotificationStatus(status),
		ObservedAt: time.Now().UTC(),
	}
	monitor.state.Destinations["dest-1"] = &notificationDestinationState{
		Pending: map[string]NotificationEvent{
			event.Key: event,
		},
		Delivered: make(map[string]time.Time),
	}

	require.NoError(t, os.Chmod(stateDir, 0o500))
	defer func() {
		_ = os.Chmod(stateDir, 0o700)
	}()

	monitor.markBatchDelivered(context.Background(), "dest-1", NotificationBatch{
		Class:  NotificationClassSuccessDigest,
		Events: []NotificationEvent{event},
	})

	monitor.stateMu.Lock()
	defer monitor.stateMu.Unlock()
	destState := monitor.state.Destinations["dest-1"]
	require.NotNil(t, destState)
	assert.Contains(t, destState.Pending, event.Key)
	assert.Empty(t, destState.Delivered)
}

func TestNotificationMonitor_RemovedDestinationsArePurgedOnStartup(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	status := &ir.DAGRunStatus{
		Name:      "briefing",
		Status:    ir.Failed,
		DAGRunID:  "run-removed",
		AttemptID: "attempt-removed",
		Error:     "boom",
	}
	state := newNotificationMonitorState()
	state.Bootstrapped = true
	state.Destinations["removed-dest"] = &notificationDestinationState{
		Pending: map[string]NotificationEvent{
			NotificationSeenKey(status): {
				Key:        NotificationSeenKey(status),
				Status:     cloneNotificationStatus(status),
				ObservedAt: time.Now().UTC(),
			},
		},
		Delivered: map[string]time.Time{
			NotificationSeenKey(status): time.Now().UTC(),
		},
	}
	require.NoError(t, newNotificationStateStore(filemonitor.NewStateStore(stateFile)).Save(context.Background(), state))

	var (
		mu    sync.Mutex
		calls []string
	)
	transport := &fakeNotificationTransport{
		destinations: []string{"keep-dest"},
		flushFn: func(_ context.Context, destination string, _ NotificationBatch, _ bool) bool {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, destination)
			return true
		},
	}

	monitor := newFileBackedMonitor(nil, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		_, removedExists := monitor.state.Destinations["removed-dest"]
		_, keepExists := monitor.state.Destinations["keep-dest"]
		return !removedExists && keepExists
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	result := newNotificationStateStore(filemonitor.NewStateStore(stateFile)).Load(context.Background())
	require.NoError(t, result.Warning)
	assert.NotContains(t, result.State.Destinations, "removed-dest")
	assert.Contains(t, result.State.Destinations, "keep-dest")

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, calls)
}

func TestNotificationMonitor_LockTheftSelfFencesActiveOwner(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := fileeventstore.New(baseDir)
	require.NoError(t, err)
	service := eventstore.New(store)

	stateFile := filepath.Join(t.TempDir(), "state.json")

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

	monitor := newFileBackedMonitor(service, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		requireNotificationMonitorShutdown(t, "monitor", done)
	}()

	require.Eventually(t, func() bool {
		return monitor.ownsNotificationLock() && monitor.notificationSessionActive()
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	lockDir := filepath.Clean(stateFile) + ".lock"
	lockTokenPath := filepath.Join(lockDir, ".dagu_lock", "owner")
	require.NoError(t, os.WriteFile(lockTokenPath, []byte("replacement-owner"), 0o600))
	require.Eventually(t, func() bool {
		return !monitor.ownsNotificationLock() && !monitor.notificationSessionActive()
	}, notificationMonitorEventuallyTimeout(2*time.Second), 10*time.Millisecond)

	status := &ir.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-stolen-lock",
		AttemptID:  "attempt-stolen-lock",
		Status:     ir.Failed,
		Error:      "lock failure",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, service.Emit(context.Background(), eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceServer, Instance: "test"},
		eventstore.TypeDAGRunFailed,
		status,
		nil,
	)))

	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) > 0
	}, 150*time.Millisecond, 10*time.Millisecond)
}
