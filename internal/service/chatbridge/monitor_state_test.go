// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/fileeventstore"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestNotificationMonitorConfig() NotificationMonitorConfig {
	cfg := DefaultNotificationMonitorConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.SuccessWindow = 10 * time.Millisecond
	cfg.UrgentWindow = 10 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour
	return cfg
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

	cfg := newTestNotificationMonitorConfig()
	monitor := NewNotificationMonitor(service, filepath.Join(t.TempDir(), "state.json"), transport, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()
	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		bootstrapped := monitor.state.Bootstrapped
		monitor.stateMu.Unlock()
		return monitor.ownsNotificationLock() && monitor.notificationSessionActive() && bootstrapped
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

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
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return !monitor.IsDelivered("dest-1", oldStatus) && monitor.IsDelivered("dest-1", newStatus)
	}, notificationMonitorEventuallyTimeout(time.Second), 10*time.Millisecond)
}

func TestNotificationMonitor_RestartRequeuesPersistedPending(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := newTestNotificationMonitorConfig()

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Failed,
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
				ObservedAt: time.Now().UTC(),
			},
		},
		Delivered: make(map[string]time.Time),
	}
	require.NoError(t, newNotificationStateStore(stateFile).Save(context.Background(), state))

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
			calls++
			return true
		},
	}

	secondMonitor := NewNotificationMonitor(nil, stateFile, secondTransport, logger, cfg)
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

	monitor1 := NewNotificationMonitor(service, stateFile, newTransport("monitor-1"), logger, newTestNotificationMonitorConfig())
	monitor2 := NewNotificationMonitor(service, stateFile, newTransport("monitor-2"), logger, newTestNotificationMonitorConfig())

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

	firstStatus := &exec.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-first",
		AttemptID:  "attempt-first",
		Status:     core.Failed,
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
	}, time.Second, 10*time.Millisecond)

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

	secondStatus := &exec.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-second",
		AttemptID:  "attempt-second",
		Status:     core.Failed,
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

	monitor := NewNotificationMonitor(service, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		matches, globErr := filepath.Glob(stateFile + ".corrupt.*")
		if globErr != nil {
			return false
		}
		return len(matches) == 1
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		return monitor.state.Bootstrapped
	}, time.Second, 10*time.Millisecond)

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

	assert.False(t, monitor.IsDelivered("dest-1", oldStatus))
	require.Eventually(t, func() bool {
		return monitor.IsDelivered("dest-1", newStatus)
	}, time.Second, 10*time.Millisecond)
}

func TestNotificationStateStore_LoadUnsupportedVersionQuarantinesState(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(stateFile, []byte(`{"version":99}`), 0o600))

	result := newNotificationStateStore(stateFile).Load(context.Background())
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

	monitor := NewNotificationMonitor(service, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		return monitor.state.Bootstrapped
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, os.Chmod(stateDir, 0o500))
	defer func() {
		_ = os.Chmod(stateDir, 0o700)
	}()

	status := &exec.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-save-retry",
		AttemptID:  "attempt-save-retry",
		Status:     core.Failed,
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
	}, time.Second, 10*time.Millisecond)

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
	monitor := NewNotificationMonitor(
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

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-save-fail",
		AttemptID: "attempt-save-fail",
		Status:    core.Failed,
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
	monitor := NewNotificationMonitor(
		nil,
		stateFile,
		&fakeNotificationTransport{destinations: []string{"dest-1"}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		newTestNotificationMonitorConfig(),
	)
	monitor.lock = nil
	monitor.lockDir = ""

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-ack-save-fail",
		AttemptID: "attempt-ack-save-fail",
		Status:    core.Succeeded,
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
	status := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Failed,
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
	require.NoError(t, newNotificationStateStore(stateFile).Save(context.Background(), state))

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

	monitor := NewNotificationMonitor(nil, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		monitor.stateMu.Lock()
		defer monitor.stateMu.Unlock()
		_, removedExists := monitor.state.Destinations["removed-dest"]
		_, keepExists := monitor.state.Destinations["keep-dest"]
		return !removedExists && keepExists
	}, time.Second, 10*time.Millisecond)

	result := newNotificationStateStore(stateFile).Load(context.Background())
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

	monitor := NewNotificationMonitor(service, stateFile, transport, slog.New(slog.NewTextHandler(io.Discard, nil)), newTestNotificationMonitorConfig())
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

	lockDir := notificationStateLockDir(stateFile)
	lockTokenPath := filepath.Join(lockDir, ".dagu_lock", "owner")
	require.NoError(t, os.WriteFile(lockTokenPath, []byte("replacement-owner"), 0o600))
	require.Eventually(t, func() bool {
		return !monitor.ownsNotificationLock() && !monitor.notificationSessionActive()
	}, notificationMonitorEventuallyTimeout(2*time.Second), 10*time.Millisecond)

	status := &exec.DAGRunStatus{
		Name:       "briefing",
		DAGRunID:   "run-stolen-lock",
		AttemptID:  "attempt-stolen-lock",
		Status:     core.Failed,
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
