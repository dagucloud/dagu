// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/require"
)

type monitorEventStore struct {
	mu             sync.Mutex
	events         []*eventstore.Event
	headCalls      int
	readCalls      int
	lastHeadOffset int64
	onReadEvents   func([]*eventstore.Event)
}

var _ eventstore.Store = (*monitorEventStore)(nil)
var _ eventstore.DAGRunReader = (*monitorEventStore)(nil)

func newFileNotificationMonitor(
	eventService *eventstore.Service,
	stateFile string,
	transport chatbridge.NotificationTransport,
	logger *slog.Logger,
	cfg chatbridge.NotificationMonitorConfig,
) *chatbridge.NotificationMonitor {
	return chatbridge.NewNotificationMonitor(
		eventService,
		filemonitor.NewStateStore(stateFile),
		filemonitor.NewLease(stateFile, &dirlock.LockOptions{
			StaleThreshold: chatbridge.DefaultNotificationLockStaleThreshold,
			RetryInterval:  chatbridge.DefaultNotificationLockRetryInterval,
		}),
		transport,
		logger,
		cfg,
	)
}

func monitorEventuallyTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 3
	}
	return base
}

func (s *monitorEventStore) Emit(_ context.Context, event *eventstore.Event) error {
	if event == nil {
		return nil
	}
	event.Normalize()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *monitorEventStore) Query(context.Context, eventstore.QueryFilter) (*eventstore.QueryResult, error) {
	return &eventstore.QueryResult{}, nil
}

func (s *monitorEventStore) DAGRunHeadCursor(context.Context) (eventstore.DAGRunCursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.headCalls++
	s.lastHeadOffset = int64(len(s.events))
	return s.currentCursorLocked(), nil
}

func (s *monitorEventStore) ReadDAGRunEvents(_ context.Context, cursor eventstore.DAGRunCursor) ([]*eventstore.Event, eventstore.DAGRunCursor, error) {
	s.mu.Lock()
	s.readCalls++

	index := int(cursor.Normalize().CommittedOffsets["events"])
	if index < 0 || index > len(s.events) {
		index = 0
	}
	events := append([]*eventstore.Event(nil), s.events[index:]...)
	nextCursor := s.currentCursorLocked()
	onReadEvents := s.onReadEvents
	s.mu.Unlock()

	if onReadEvents != nil {
		onReadEvents(events)
	}
	return events, nextCursor, nil
}

func (s *monitorEventStore) currentCursorLocked() eventstore.DAGRunCursor {
	return eventstore.DAGRunCursor{
		CommittedOffsets: map[string]int64{"events": int64(len(s.events))},
	}
}

func (s *monitorEventStore) stats() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headCalls, s.readCalls
}

func (s *monitorEventStore) lastHead() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHeadOffset
}

type mutableNotificationTransport struct {
	mu           sync.Mutex
	destinations []string
	delivered    []string
}

func (t *mutableNotificationTransport) NotificationDestinations() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.destinations...)
}

func (t *mutableNotificationTransport) FlushNotificationBatch(_ context.Context, _ string, batch chatbridge.NotificationBatch, _ bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, event := range batch.Events {
		if event.Status != nil {
			t.delivered = append(t.delivered, event.Status.Name)
		}
	}
	return true
}

func (t *mutableNotificationTransport) setDestinations(destinations []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.destinations = append([]string(nil), destinations...)
}

func (t *mutableNotificationTransport) deliveredNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.delivered...)
}

func TestNotificationMonitorWithoutDestinationsAdvancesCursorWithoutReadingEvents(t *testing.T) {
	t.Parallel()

	store := &monitorEventStore{}
	service := eventstore.New(store)
	transport := &mutableNotificationTransport{}

	cfg := chatbridge.DefaultNotificationMonitorConfig()
	cfg.PollInterval = 5 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour
	cfg.UrgentWindow = 5 * time.Millisecond
	cfg.SuccessWindow = 5 * time.Millisecond

	monitor := chatbridge.NewNotificationMonitor(
		service,
		nil,
		nil,
		transport,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg,
	)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	require.Eventually(t, func() bool {
		headCalls, _ := store.stats()
		return headCalls > 0
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	require.NoError(t, store.Emit(context.Background(), newMonitorDAGRunEvent("old-run")))

	require.Eventually(t, func() bool {
		return store.lastHead() >= 1
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)
}

func TestNotificationMonitorDeliversOnlyFutureEventsAfterDestinationIsAdded(t *testing.T) {
	t.Parallel()

	store := &monitorEventStore{}
	service := eventstore.New(store)
	transport := &mutableNotificationTransport{}

	cfg := chatbridge.DefaultNotificationMonitorConfig()
	cfg.PollInterval = 5 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour
	cfg.UrgentWindow = 5 * time.Millisecond
	cfg.SuccessWindow = 5 * time.Millisecond

	monitor := newFileNotificationMonitor(
		service,
		filepath.Join(t.TempDir(), "state.json"),
		transport,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg,
	)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	stopped := false
	defer func() {
		if !stopped {
			stopMonitor()
		}
	}()

	require.Eventually(t, func() bool {
		headCalls, _ := store.stats()
		return headCalls > 0
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	require.NoError(t, store.Emit(context.Background(), newMonitorDAGRunEvent("old-run")))
	require.Eventually(t, func() bool {
		return store.lastHead() >= 1
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	transport.setDestinations([]string{"dest-1"})
	newEvent := newMonitorDAGRunEvent("new-run")
	newStatus, err := eventstore.DAGRunStatusFromEvent(newEvent)
	require.NoError(t, err)
	require.NoError(t, store.Emit(context.Background(), newEvent))

	require.Eventually(t, func() bool {
		return slices.Equal(transport.deliveredNames(), []string{"new-run"}) &&
			monitor.IsDelivered("dest-1", newStatus)
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	stopMonitor()
	stopped = true
	require.Equal(t, []string{"new-run"}, transport.deliveredNames())
}

func TestNotificationMonitorDoesNotDeliverEventsReadAfterShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	store := &monitorEventStore{
		onReadEvents: func(events []*eventstore.Event) {
			if len(events) > 0 {
				cancel()
			}
		},
	}
	service := eventstore.New(store)
	transport := &mutableNotificationTransport{destinations: []string{"dest-1"}}

	cfg := chatbridge.DefaultNotificationMonitorConfig()
	cfg.PollInterval = 5 * time.Millisecond
	cfg.SeenEvictInterval = time.Hour
	cfg.UrgentWindow = 5 * time.Millisecond
	cfg.SuccessWindow = 5 * time.Millisecond

	monitor := newFileNotificationMonitor(
		service,
		filepath.Join(t.TempDir(), "state.json"),
		transport,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg,
	)
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for monitor shutdown")
		}
	}()

	require.Eventually(t, func() bool {
		headCalls, _ := store.stats()
		return headCalls > 0
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)

	require.NoError(t, store.Emit(context.Background(), newMonitorDAGRunEvent("cancelled-run")))

	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, monitorEventuallyTimeout(time.Second), 10*time.Millisecond)
	require.Empty(t, transport.deliveredNames())
}

func newMonitorDAGRunEvent(name string) *eventstore.Event {
	return eventstore.NewDAGRunEvent(
		eventstore.Source{Service: eventstore.SourceServiceScheduler},
		eventstore.TypeDAGRunFailed,
		&ir.DAGRunStatus{
			Name:      name,
			Status:    ir.Failed,
			DAGRunID:  name + "-run",
			AttemptID: name + "-attempt",
		},
		nil,
	)
}
