// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"context"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

const (
	DefaultNotificationMonitorPollInterval   = 10 * time.Second
	DefaultNotificationSeenEvictInterval     = 10 * time.Minute
	DefaultNotificationSeenTTL               = 2 * time.Hour
	DefaultNotificationFlushTimeout          = 30 * time.Second
	DefaultNotificationLockHeartbeatInterval = time.Second
	DefaultNotificationLockRetryInterval     = 50 * time.Millisecond
	DefaultNotificationLockStaleThreshold    = 45 * time.Second
)

var defaultInterestedNotificationEventTypes = []eventstore.EventType{
	eventstore.TypeDAGRunWaiting,
	eventstore.TypeDAGRunSucceeded,
	eventstore.TypeDAGRunFailed,
	eventstore.TypeDAGRunAborted,
	eventstore.TypeDAGRunRejected,
}

// NotificationMonitorConfig controls source polling, batching, and shutdown behavior.
type NotificationMonitorConfig struct {
	PollInterval         time.Duration
	SeenEvictInterval    time.Duration
	SeenTTL              time.Duration
	FlushTimeout         time.Duration
	UrgentWindow         time.Duration
	SuccessWindow        time.Duration
	InterestedEventTypes []eventstore.EventType
}

// DefaultNotificationMonitorConfig returns the default monitor settings.
func DefaultNotificationMonitorConfig() NotificationMonitorConfig {
	return NotificationMonitorConfig{
		PollInterval:         DefaultNotificationMonitorPollInterval,
		SeenEvictInterval:    DefaultNotificationSeenEvictInterval,
		SeenTTL:              DefaultNotificationSeenTTL,
		FlushTimeout:         DefaultNotificationFlushTimeout,
		UrgentWindow:         DefaultUrgentNotificationWindow,
		SuccessWindow:        DefaultSuccessNotificationWindow,
		InterestedEventTypes: append([]eventstore.EventType(nil), defaultInterestedNotificationEventTypes...),
	}
}

func interestedEventTypeSet(eventTypes []eventstore.EventType) map[eventstore.EventType]struct{} {
	set := make(map[eventstore.EventType]struct{}, len(eventTypes))
	for _, eventType := range eventTypes {
		if eventType == "" {
			continue
		}
		set[eventType] = struct{}{}
	}
	return set
}

// NotificationTransport supplies destination discovery and transport-specific delivery.
type NotificationTransport interface {
	NotificationDestinations() []string
	FlushNotificationBatch(ctx context.Context, destination string, batch NotificationBatch, allowLLM bool) bool
}

// NotificationMonitor owns source polling, batching, durable delivery state, and shutdown drain.
type NotificationMonitor struct {
	eventService *eventstore.Service
	stateStore   *notificationStateStore
	lock         dirlock.DirLock
	lockDir      string
	transport    NotificationTransport
	logger       *slog.Logger
	cfg          NotificationMonitorConfig
	interested   map[eventstore.EventType]struct{}

	batcherMu sync.RWMutex
	batcher   *NotificationBatcher

	stateMu sync.Mutex
	state   notificationMonitorState

	sessionMu     sync.Mutex
	sessionCancel context.CancelFunc
	sessionLost   bool

	lastBootstrapFailure string
}

type queuedNotification struct {
	destination string
	event       NotificationEvent
}

// NewNotificationMonitor creates a shared notification monitor.
func NewNotificationMonitor(
	eventService *eventstore.Service,
	stateFile string,
	transport NotificationTransport,
	logger *slog.Logger,
	cfg NotificationMonitorConfig,
) *NotificationMonitor {
	normalizeNotificationMonitorConfig(&cfg)

	stateStore := newNotificationStateStore(stateFile)
	lockDir := notificationStateLockDir(stateFile)

	var lock dirlock.DirLock
	if lockDir != "" {
		lock = dirlock.New(lockDir, &dirlock.LockOptions{
			StaleThreshold: DefaultNotificationLockStaleThreshold,
			RetryInterval:  DefaultNotificationLockRetryInterval,
			OnWait: func() {
				logger.Info("Notification lock is held by another process; DAG run notifications are on standby",
					slog.String("lock_dir", lockDir),
				)
			},
		})
	}

	return &NotificationMonitor{
		eventService: eventService,
		stateStore:   stateStore,
		lock:         lock,
		lockDir:      lockDir,
		transport:    transport,
		logger:       logger,
		cfg:          cfg,
		interested:   interestedEventTypeSet(cfg.InterestedEventTypes),
		batcher:      NewNotificationBatcher(cfg.UrgentWindow, cfg.SuccessWindow),
		state:        newNotificationMonitorState(),
	}
}

// Run starts the shared notification monitor loop.
func (m *NotificationMonitor) Run(ctx context.Context) {
	m.logger.Info("DAG run notification monitor started")
	defer m.logger.Info("DAG run notification monitor stopped")

	if m.lock == nil {
		m.initializeSession(ctx)
		m.runUnlockedLoop(ctx)
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		if !m.acquireNotificationLock(ctx) {
			return
		}

		sessionCtx, cancel := context.WithCancel(ctx)
		m.beginNotificationSession(cancel)
		m.initializeSession(sessionCtx)
		heartbeatDone := m.startNotificationLockHeartbeat(sessionCtx)

		continueWaiting := m.runOwnedLoop(ctx, sessionCtx)

		cancel()
		<-heartbeatDone
		m.releaseNotificationLock()
		m.endNotificationSession()
		if continueWaiting {
			m.resetInMemorySession()
		}
		if !continueWaiting {
			return
		}
	}
}

// NotifyCompletion queues a status update for every destination that has not yet acknowledged it.
func (m *NotificationMonitor) NotifyCompletion(status *exec.DAGRunStatus) bool {
	if status == nil {
		return false
	}
	eventType, ok := eventstore.PersistedDAGRunEventTypeForStatus(status.Status)
	if !ok || !m.isInterestedEventType(eventType) {
		return false
	}
	if !m.canMutateNotificationState("Notification lock is not held; cannot queue notification") {
		return false
	}

	m.logger.Info("DAG run notification queued",
		slog.String("dag", status.Name),
		slog.String("status", status.Status.String()),
		slog.String("dag_run_id", status.DAGRunID),
	)

	event := NotificationEvent{
		Key:        NotificationSeenKey(status),
		Type:       eventType,
		Status:     cloneNotificationStatus(status),
		ObservedAt: time.Now().UTC(),
	}
	return m.enqueueEvents(context.Background(), nil, []NotificationEvent{event})
}

// IsDelivered reports whether a destination has already acknowledged a status.
func (m *NotificationMonitor) IsDelivered(destination string, status *exec.DAGRunStatus) bool {
	if destination == "" || status == nil {
		return false
	}
	key := NotificationSeenKey(status)

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	destState := m.state.Destinations[destination]
	if destState == nil {
		return false
	}
	_, ok := destState.Delivered[key]
	return ok
}

func (m *NotificationMonitor) initializeSession(ctx context.Context) {
	m.loadPersistedState(ctx)

	destinations := m.transport.NotificationDestinations()
	var removed []string

	m.stateMu.Lock()
	persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		var changed bool
		removed, changed = reconcileDestinations(candidate, destinations)
		return changed
	})
	m.stateMu.Unlock()
	if !persisted {
		return
	}
	if len(removed) > 0 {
		m.discardPendingDestinations(removed)
	}

	m.ensureBootstrapped(ctx)
	m.requeuePending(ctx, destinations)
}

func (m *NotificationMonitor) loadPersistedState(ctx context.Context) {
	state := newNotificationMonitorState()
	if m.stateStore != nil {
		loadResult := m.stateStore.Load(ctx)
		state = loadResult.State
		if loadResult.Warning != nil {
			attrs := []any{slog.String("error", loadResult.Warning.Error())}
			if loadResult.QuarantinedPath != "" {
				attrs = append(attrs, slog.String("quarantined_path", loadResult.QuarantinedPath))
			}
			m.logger.Warn("Notification state was invalid; starting fresh", attrs...)
		}
	}

	m.stateMu.Lock()
	m.state = state
	m.state.normalize()
	m.lastBootstrapFailure = ""
	m.stateMu.Unlock()
}

func (m *NotificationMonitor) runUnlockedLoop(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()

	evictTicker := time.NewTicker(m.cfg.SeenEvictInterval)
	defer evictTicker.Stop()

	for {
		if ready := m.takeReadyBatches(); len(ready) > 0 {
			inFlight := m.flushReadyBatches(ctx, ready)
			if ctx.Err() != nil {
				m.drainPendingBatches(ctx, inFlight)
				return
			}
			continue
		}

		select {
		case <-ctx.Done():
			m.drainPendingBatches(ctx, nil)
			return
		case <-ticker.C:
			m.syncPendingDestinations(ctx)
			m.pollSource(ctx)
		case <-evictTicker.C:
			m.evictStaleDelivered(ctx)
		case <-m.batcherReadyC():
		}
	}
}

func (m *NotificationMonitor) runOwnedLoop(parentCtx, sessionCtx context.Context) bool {
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()

	evictTicker := time.NewTicker(m.cfg.SeenEvictInterval)
	defer evictTicker.Stop()

	for {
		if ready := m.takeReadyBatches(); len(ready) > 0 {
			inFlight := m.flushReadyBatches(sessionCtx, ready)
			if sessionCtx.Err() != nil {
				return m.finishOwnedLoop(parentCtx, inFlight)
			}
			continue
		}

		select {
		case <-parentCtx.Done():
			return m.finishOwnedLoop(parentCtx, nil)
		case <-sessionCtx.Done():
			return m.finishOwnedLoop(parentCtx, nil)
		case <-ticker.C:
			m.syncPendingDestinations(sessionCtx)
			m.pollSource(sessionCtx)
		case <-evictTicker.C:
			m.evictStaleDelivered(sessionCtx)
		case <-m.batcherReadyC():
		}
	}
}

func (m *NotificationMonitor) finishOwnedLoop(parentCtx context.Context, inFlight *NotificationPendingBatch) bool {
	if parentCtx.Err() != nil {
		if m.ownsNotificationLock() {
			m.drainPendingBatches(parentCtx, inFlight)
		} else {
			m.stopPendingBatches()
		}
		return false
	}

	m.stopPendingBatches()
	return true
}

func (m *NotificationMonitor) pollSource(ctx context.Context) {
	if m.eventService == nil {
		return
	}
	if !m.ensureNotificationLockOwnership("Notification lock lost before reading notification events") {
		return
	}
	if !m.ensureBootstrapped(ctx) {
		return
	}

	m.stateMu.Lock()
	if !m.state.Bootstrapped {
		m.stateMu.Unlock()
		return
	}
	cursor := m.state.SourceCursor
	m.stateMu.Unlock()

	events, nextCursor, err := m.eventService.ReadDAGRunEvents(ctx, cursor)
	if err != nil {
		m.logger.Debug("Failed to read DAG-run events", slog.String("error", err.Error()))
		return
	}

	destinations := m.transport.NotificationDestinations()
	pending := make([]NotificationEvent, 0, len(events))
	for _, event := range events {
		if event == nil || !m.isInterestedEventType(event.Type) {
			continue
		}
		status, err := eventstore.DAGRunStatusFromEvent(event)
		if err != nil {
			m.logger.Warn("Failed to decode DAG-run event payload",
				slog.String("event_id", event.ID),
				slog.String("error", err.Error()),
			)
			continue
		}
		observedAt := event.RecordedAt
		if observedAt.IsZero() {
			observedAt = time.Now().UTC()
		}
		pending = append(pending, NotificationEvent{
			Key:        NotificationSeenKey(status),
			Type:       event.Type,
			Status:     status,
			ObservedAt: observedAt.UTC(),
		})
	}

	queued, committed := m.commitSourceProgress(ctx, destinations, nextCursor, pending)
	if !committed || len(queued) == 0 {
		return
	}
	for _, item := range queued {
		m.enqueueBatch(item.destination, item.event)
	}
}

func (m *NotificationMonitor) syncPendingDestinations(ctx context.Context) {
	if !m.ensureNotificationLockOwnership("Notification lock lost before syncing destinations") {
		return
	}

	destinations := m.transport.NotificationDestinations()
	var removed []string

	m.stateMu.Lock()
	persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		var changed bool
		removed, changed = reconcileDestinations(candidate, destinations)
		return changed
	})
	m.stateMu.Unlock()
	if !persisted {
		return
	}
	if len(removed) > 0 {
		m.discardPendingDestinations(removed)
	}

	m.requeuePending(ctx, destinations)
}

func (m *NotificationMonitor) enqueueEvents(ctx context.Context, destinations []string, events []NotificationEvent) bool {
	if len(events) == 0 {
		return false
	}
	if !m.canMutateNotificationState("Notification lock is not held; cannot enqueue notification events") {
		return false
	}
	if destinations == nil {
		destinations = m.transport.NotificationDestinations()
	}
	if len(destinations) == 0 {
		m.logger.Warn("No notification destinations configured, cannot send notification")
		return false
	}

	var queued []queuedNotification
	accepted := false

	m.stateMu.Lock()
	persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		changed := ensureDestinations(candidate, destinations)
		var enqueueChanged bool
		queued, enqueueChanged, accepted = enqueueNotifications(candidate, destinations, events)
		return changed || enqueueChanged
	})
	m.stateMu.Unlock()
	if !persisted {
		return false
	}

	for _, item := range queued {
		m.enqueueBatch(item.destination, item.event)
	}
	return accepted
}

func (m *NotificationMonitor) requeuePending(ctx context.Context, destinations []string) {
	if len(destinations) == 0 {
		return
	}
	if !m.ensureNotificationLockOwnership("Notification lock lost before requeueing pending notifications") {
		return
	}

	allowed := make(map[string]struct{}, len(destinations))
	for _, destination := range destinations {
		allowed[destination] = struct{}{}
	}

	var queued []queuedNotification

	m.stateMu.Lock()
	persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		changed := false
		for destination, destState := range candidate.Destinations {
			if _, ok := allowed[destination]; !ok || destState == nil {
				continue
			}
			for key, event := range destState.Pending {
				if shouldSuppressNotificationEvent(event) {
					delete(destState.Pending, key)
					changed = true
				}
			}
		}
		return changed
	})
	if !persisted {
		m.stateMu.Unlock()
		return
	}
	for destination, destState := range m.state.Destinations {
		if _, ok := allowed[destination]; !ok || destState == nil {
			continue
		}
		for _, event := range destState.Pending {
			if event.Status == nil || event.Key == "" {
				continue
			}
			if shouldSuppressNotificationEvent(event) {
				continue
			}
			queued = append(queued, queuedNotification{
				destination: destination,
				event:       event,
			})
		}
	}
	m.stateMu.Unlock()

	slices.SortFunc(queued, func(a, b queuedNotification) int {
		if !a.event.ObservedAt.Equal(b.event.ObservedAt) {
			if a.event.ObservedAt.Before(b.event.ObservedAt) {
				return -1
			}
			return 1
		}
		switch {
		case a.destination < b.destination:
			return -1
		case a.destination > b.destination:
			return 1
		case a.event.Key < b.event.Key:
			return -1
		case a.event.Key > b.event.Key:
			return 1
		default:
			return 0
		}
	})

	for _, item := range queued {
		m.enqueueBatch(item.destination, item.event)
	}
}

func (m *NotificationMonitor) flushReadyBatches(ctx context.Context, ready []NotificationPendingBatch) *NotificationPendingBatch {
	for _, pending := range ready {
		acked := m.flushPendingBatch(ctx, pending, true)
		if !acked && ctx.Err() != nil {
			pendingCopy := pending
			return &pendingCopy
		}
	}
	return nil
}

func (m *NotificationMonitor) flushPendingBatch(ctx context.Context, pending NotificationPendingBatch, allowLLM bool) bool {
	if !m.ensureNotificationLockOwnership("Notification lock lost before delivering notification batch") {
		return false
	}

	destinations := m.transport.NotificationDestinations()
	if !slices.Contains(destinations, pending.Destination) {
		return false
	}

	// Successful completions still flow through the notification state machine so
	// they can replace pending wait alerts, but they should not post chat digests.
	if pending.Batch.Class == NotificationClassSuccessDigest {
		m.markBatchDelivered(ctx, pending.Destination, pending.Batch)
		return true
	}

	flushCtx := ctx
	cancel := func() {}
	if allowLLM {
		flushCtx, cancel = context.WithTimeout(ctx, m.cfg.FlushTimeout)
	}
	defer cancel()

	acked := m.transport.FlushNotificationBatch(flushCtx, pending.Destination, pending.Batch, allowLLM)
	if acked {
		m.markBatchDelivered(ctx, pending.Destination, pending.Batch)
	}
	return acked
}

func (m *NotificationMonitor) drainPendingBatches(ctx context.Context, inFlight *NotificationPendingBatch) {
	drained := m.drainAndResetBatcher()
	if inFlight != nil {
		drained = append([]NotificationPendingBatch{*inFlight}, drained...)
	}
	if len(drained) == 0 {
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.cfg.FlushTimeout)
	defer cancel()

	for _, pending := range drained {
		if shutdownCtx.Err() != nil {
			return
		}
		m.flushPendingBatch(shutdownCtx, pending, false)
	}
}

func (m *NotificationMonitor) stopPendingBatches() {
	m.resetBatcher()
}

func (m *NotificationMonitor) markBatchDelivered(ctx context.Context, destination string, batch NotificationBatch) {
	if !m.ensureNotificationLockOwnership("Notification lock lost before acknowledging delivered batch") {
		return
	}

	now := time.Now().UTC()

	m.stateMu.Lock()
	m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		destState := destinationState(candidate, destination)
		changed := false
		for _, event := range batch.Events {
			if event.Key == "" {
				continue
			}
			if _, ok := destState.Pending[event.Key]; ok {
				delete(destState.Pending, event.Key)
				changed = true
			}
			// Refresh the delivery timestamp on each successful acknowledgement so
			// TTL-based eviction retains recently retried deliveries.
			if deliveredAt, ok := destState.Delivered[event.Key]; !ok || !deliveredAt.Equal(now) {
				destState.Delivered[event.Key] = now
				changed = true
			}
		}
		return changed
	})
	m.stateMu.Unlock()
}

func (m *NotificationMonitor) evictStaleDelivered(ctx context.Context) {
	if !m.ensureNotificationLockOwnership("Notification lock lost before evicting delivered notifications") {
		return
	}

	cutoff := time.Now().Add(-m.cfg.SeenTTL)
	changed := false

	m.stateMu.Lock()
	m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		changed = false
		for _, destination := range candidate.Destinations {
			if destination == nil {
				continue
			}
			for key, deliveredAt := range destination.Delivered {
				if deliveredAt.Before(cutoff) {
					delete(destination.Delivered, key)
					changed = true
				}
			}
		}
		return changed
	})
	m.stateMu.Unlock()
}

func (m *NotificationMonitor) saveCandidateStateLocked(ctx context.Context, state notificationMonitorState) bool {
	if !m.canPersistNotificationState("Notification lock lost before persisting notification state") {
		return false
	}
	if m.stateStore == nil {
		return true
	}
	if err := m.stateStore.Save(ctx, state); err != nil {
		m.logger.Warn("Failed to persist notification state", slog.String("error", err.Error()))
		return false
	}
	return true
}

func (m *NotificationMonitor) applyStateTransitionLocked(ctx context.Context, mutate func(candidate *notificationMonitorState) bool) bool {
	candidate := cloneNotificationMonitorState(m.state)
	if !mutate(&candidate) {
		return true
	}
	if !m.saveCandidateStateLocked(ctx, candidate) {
		return false
	}
	m.state = candidate
	return true
}

func (m *NotificationMonitor) ensureBootstrapped(ctx context.Context) bool {
	if !m.canMutateNotificationState("Notification lock lost before bootstrapping notification cursor") {
		return false
	}

	m.stateMu.Lock()
	if m.state.Bootstrapped {
		m.lastBootstrapFailure = ""
		m.stateMu.Unlock()
		return true
	}
	m.stateMu.Unlock()

	if m.eventService == nil {
		m.stateMu.Lock()
		if m.state.Bootstrapped {
			m.lastBootstrapFailure = ""
			m.stateMu.Unlock()
			return true
		}
		persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
			candidate.Bootstrapped = true
			return true
		})
		if !persisted {
			m.recordBootstrapFailure("Failed to persist notification bootstrap state")
			m.stateMu.Unlock()
			return false
		}
		m.lastBootstrapFailure = ""
		m.stateMu.Unlock()
		return true
	}

	cursor, err := m.eventService.DAGRunHeadCursor(ctx)
	if err != nil {
		m.recordBootstrapFailure("Failed to bootstrap notification cursor: " + err.Error())
		return false
	}

	m.stateMu.Lock()
	if m.state.Bootstrapped {
		m.lastBootstrapFailure = ""
		m.stateMu.Unlock()
		return true
	}
	persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		candidate.SourceCursor = cursor.Normalize()
		candidate.Bootstrapped = true
		return true
	})
	if !persisted {
		m.recordBootstrapFailure("Failed to persist notification bootstrap state")
		m.stateMu.Unlock()
		return false
	}
	m.lastBootstrapFailure = ""
	m.stateMu.Unlock()
	return true
}

func (m *NotificationMonitor) commitSourceProgress(ctx context.Context, destinations []string, nextCursor eventstore.NotificationCursor, events []NotificationEvent) ([]queuedNotification, bool) {
	if !m.canMutateNotificationState("Notification lock lost before advancing notification cursor") {
		return nil, false
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if !m.state.Bootstrapped {
		return nil, false
	}

	var (
		queued   []queuedNotification
		accepted bool
	)
	persisted := m.applyStateTransitionLocked(ctx, func(candidate *notificationMonitorState) bool {
		changed := ensureDestinations(candidate, destinations)
		cursorChanged := !candidate.SourceCursor.Equal(nextCursor)
		candidate.SourceCursor = nextCursor.Normalize()
		var enqueueChanged bool
		queued, enqueueChanged, accepted = enqueueNotifications(candidate, destinations, events)
		return changed || cursorChanged || enqueueChanged
	})
	if !persisted {
		return nil, false
	}
	if !accepted {
		return nil, true
	}
	return queued, true
}

func (m *NotificationMonitor) recordBootstrapFailure(message string) {
	if message == m.lastBootstrapFailure {
		return
	}
	m.lastBootstrapFailure = message
	m.logger.Warn(message)
}

func (m *NotificationMonitor) acquireNotificationLock(ctx context.Context) bool {
	if m.lock == nil {
		return true
	}

	for {
		if err := m.lock.Lock(ctx); err != nil {
			if ctx.Err() != nil {
				return false
			}
			m.logger.Warn("Failed to acquire notification lock",
				slog.String("lock_dir", m.lockDir),
				slog.String("error", err.Error()),
			)
			select {
			case <-ctx.Done():
				return false
			case <-time.After(DefaultNotificationLockRetryInterval):
			}
			continue
		}

		m.logger.Info("Acquired notification lock; DAG run notifications are active",
			slog.String("lock_dir", m.lockDir),
		)
		return true
	}
}

func (m *NotificationMonitor) releaseNotificationLock() {
	if m.lock == nil || !m.lock.IsHeldByMe() {
		return
	}
	if err := m.lock.Unlock(); err != nil {
		m.logger.Warn("Failed to release notification lock",
			slog.String("lock_dir", m.lockDir),
			slog.String("error", err.Error()),
		)
	}
}

func (m *NotificationMonitor) startNotificationLockHeartbeat(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if m.lock == nil {
		close(done)
		return done
	}

	go func() {
		defer close(done)

		ticker := time.NewTicker(DefaultNotificationLockHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.lock.Heartbeat(ctx); err != nil {
					m.signalLockLoss("Notification lock heartbeat failed; entering standby", err)
					return
				}
			}
		}
	}()

	return done
}

func (m *NotificationMonitor) beginNotificationSession(cancel context.CancelFunc) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	m.sessionCancel = cancel
	m.sessionLost = false
}

func (m *NotificationMonitor) endNotificationSession() {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	m.sessionCancel = nil
	m.sessionLost = false
}

func (m *NotificationMonitor) signalLockLoss(message string, err error) {
	if m.lock == nil {
		return
	}

	m.sessionMu.Lock()
	if m.sessionCancel == nil || m.sessionLost {
		m.sessionMu.Unlock()
		return
	}
	m.sessionLost = true
	cancel := m.sessionCancel
	m.sessionMu.Unlock()

	attrs := []any{slog.String("lock_dir", m.lockDir)}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	m.logger.Warn(message, attrs...)
	cancel()
}

func (m *NotificationMonitor) canPersistNotificationState(message string) bool {
	return m.ensureNotificationLockOwnership(message)
}

func (m *NotificationMonitor) canMutateNotificationState(message string) bool {
	return m.ensureNotificationLockOwnership(message)
}

func (m *NotificationMonitor) ensureNotificationLockOwnership(message string) bool {
	if m.lock == nil {
		return true
	}
	if m.lock.IsHeldByMe() {
		return true
	}

	m.sessionMu.Lock()
	sessionActive := m.sessionCancel != nil
	m.sessionMu.Unlock()
	if sessionActive {
		m.signalLockLoss(message, nil)
	}
	return false
}

func (m *NotificationMonitor) ownsNotificationLock() bool {
	if m.lock == nil {
		return true
	}
	return m.lock.IsHeldByMe()
}

func (m *NotificationMonitor) notificationSessionActive() bool {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	return m.sessionCancel != nil
}

func (m *NotificationMonitor) resetInMemorySession() {
	m.stateMu.Lock()
	m.state = newNotificationMonitorState()
	m.lastBootstrapFailure = ""
	m.stateMu.Unlock()
	m.resetBatcher()
}

func (m *NotificationMonitor) currentBatcher() *NotificationBatcher {
	m.batcherMu.RLock()
	defer m.batcherMu.RUnlock()
	return m.batcher
}

func (m *NotificationMonitor) batcherReadyC() <-chan struct{} {
	return m.currentBatcher().ReadyC()
}

func (m *NotificationMonitor) takeReadyBatches() []NotificationPendingBatch {
	return m.currentBatcher().TakeReady()
}

func (m *NotificationMonitor) enqueueBatch(destination string, event NotificationEvent) bool {
	return m.currentBatcher().Enqueue(destination, event)
}

func (m *NotificationMonitor) isInterestedEventType(eventType eventstore.EventType) bool {
	if eventType == "" {
		return false
	}
	_, ok := m.interested[eventType]
	return ok
}

func (m *NotificationMonitor) resetBatcher() {
	replacement := NewNotificationBatcher(m.cfg.UrgentWindow, m.cfg.SuccessWindow)

	m.batcherMu.Lock()
	current := m.batcher
	m.batcher = replacement
	m.batcherMu.Unlock()

	if current != nil {
		current.Stop()
	}
}

func (m *NotificationMonitor) drainAndResetBatcher() []NotificationPendingBatch {
	replacement := NewNotificationBatcher(m.cfg.UrgentWindow, m.cfg.SuccessWindow)

	m.batcherMu.Lock()
	current := m.batcher
	m.batcher = replacement
	m.batcherMu.Unlock()

	if current == nil {
		return nil
	}
	return current.DrainAndStop()
}

func (m *NotificationMonitor) discardPendingDestinations(destinations []string) {
	if len(destinations) == 0 {
		return
	}
	m.currentBatcher().DiscardDestinations(destinations)
}

func notificationStateLockDir(stateFile string) string {
	if stateFile == "" {
		return ""
	}
	return filepath.Clean(stateFile) + ".lock"
}

func normalizeNotificationMonitorConfig(cfg *NotificationMonitorConfig) {
	defaults := DefaultNotificationMonitorConfig()
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaults.PollInterval
	}
	if cfg.SeenEvictInterval <= 0 {
		cfg.SeenEvictInterval = defaults.SeenEvictInterval
	}
	if cfg.SeenTTL <= 0 {
		cfg.SeenTTL = defaults.SeenTTL
	}
	if cfg.FlushTimeout <= 0 {
		cfg.FlushTimeout = defaults.FlushTimeout
	}
	if cfg.UrgentWindow <= 0 {
		cfg.UrgentWindow = defaults.UrgentWindow
	}
	if cfg.SuccessWindow <= 0 {
		cfg.SuccessWindow = defaults.SuccessWindow
	}
	if cfg.InterestedEventTypes == nil {
		cfg.InterestedEventTypes = append([]eventstore.EventType(nil), defaults.InterestedEventTypes...)
	}
}

func cloneNotificationMonitorState(state notificationMonitorState) notificationMonitorState {
	clone := notificationMonitorState{
		Version:      state.Version,
		Bootstrapped: state.Bootstrapped,
		SourceCursor: state.SourceCursor.Normalize(),
		Destinations: make(map[string]*notificationDestinationState, len(state.Destinations)),
	}
	clone.SourceCursor.CommittedOffsets = maps.Clone(state.SourceCursor.CommittedOffsets)
	for destination, destState := range state.Destinations {
		if destState == nil {
			clone.Destinations[destination] = &notificationDestinationState{
				Pending:   make(map[string]NotificationEvent),
				Delivered: make(map[string]time.Time),
			}
			continue
		}
		pending := make(map[string]NotificationEvent, len(destState.Pending))
		for key, event := range destState.Pending {
			pending[key] = NotificationEvent{
				Key:        event.Key,
				Type:       event.Type,
				Status:     cloneNotificationStatus(event.Status),
				ObservedAt: event.ObservedAt,
			}
		}
		clone.Destinations[destination] = &notificationDestinationState{
			Pending:   pending,
			Delivered: maps.Clone(destState.Delivered),
		}
	}
	clone.normalize()
	return clone
}

func ensureDestinations(state *notificationMonitorState, destinations []string) bool {
	changed := false
	for _, destination := range destinations {
		if destination == "" {
			continue
		}
		if _, ok := state.Destinations[destination]; ok {
			continue
		}
		state.Destinations[destination] = &notificationDestinationState{
			Pending:   make(map[string]NotificationEvent),
			Delivered: make(map[string]time.Time),
		}
		changed = true
	}
	return changed
}

func reconcileDestinations(state *notificationMonitorState, destinations []string) ([]string, bool) {
	allowed := make(map[string]struct{}, len(destinations))
	for _, destination := range destinations {
		if destination == "" {
			continue
		}
		allowed[destination] = struct{}{}
	}

	changed := ensureDestinations(state, destinations)
	removed := make([]string, 0)
	for destination := range state.Destinations {
		if _, ok := allowed[destination]; ok {
			continue
		}
		delete(state.Destinations, destination)
		removed = append(removed, destination)
		changed = true
	}
	slices.Sort(removed)
	return removed, changed
}

func destinationState(state *notificationMonitorState, destination string) *notificationDestinationState {
	if destination == "" {
		return &notificationDestinationState{
			Pending:   make(map[string]NotificationEvent),
			Delivered: make(map[string]time.Time),
		}
	}
	current, ok := state.Destinations[destination]
	if !ok || current == nil {
		current = &notificationDestinationState{
			Pending:   make(map[string]NotificationEvent),
			Delivered: make(map[string]time.Time),
		}
		state.Destinations[destination] = current
	}
	if current.Pending == nil {
		current.Pending = make(map[string]NotificationEvent)
	}
	if current.Delivered == nil {
		current.Delivered = make(map[string]time.Time)
	}
	return current
}

func enqueueNotifications(state *notificationMonitorState, destinations []string, events []NotificationEvent) ([]queuedNotification, bool, bool) {
	var (
		queued   []queuedNotification
		changed  bool
		accepted bool
	)

	for _, destination := range destinations {
		destState := destinationState(state, destination)
		for _, event := range events {
			if event.Status == nil || event.Key == "" {
				continue
			}
			if shouldSuppressNotificationEvent(event) {
				if removePendingNotificationsForRun(destState, NotificationRunKey(event.Status)) {
					changed = true
				}
				continue
			}
			if _, ok := destState.Delivered[event.Key]; ok {
				continue
			}
			if pending, ok := destState.Pending[event.Key]; ok {
				if shouldSuppressNotificationEvent(pending) {
					delete(destState.Pending, event.Key)
				} else {
					queued = append(queued, queuedNotification{
						destination: destination,
						event:       pending,
					})
					accepted = true
					continue
				}
			}

			runKey := NotificationRunKey(event.Status)
			for pendingKey, pending := range destState.Pending {
				if pending.Status == nil {
					continue
				}
				if NotificationRunKey(pending.Status) != runKey || pendingKey == event.Key {
					continue
				}
				delete(destState.Pending, pendingKey)
			}

			destState.Pending[event.Key] = NotificationEvent{
				Key:        event.Key,
				Type:       event.Type,
				Status:     cloneNotificationStatus(event.Status),
				ObservedAt: event.ObservedAt,
			}
			queued = append(queued, queuedNotification{
				destination: destination,
				event:       event,
			})
			accepted = true
			changed = true
		}
	}

	return queued, changed, accepted
}

func removePendingNotificationsForRun(destState *notificationDestinationState, runKey string) bool {
	if destState == nil || runKey == "" {
		return false
	}
	changed := false
	for pendingKey, pending := range destState.Pending {
		if pending.Status == nil || NotificationRunKey(pending.Status) != runKey {
			continue
		}
		delete(destState.Pending, pendingKey)
		changed = true
	}
	return changed
}
