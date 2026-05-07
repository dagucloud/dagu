// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/google/uuid"
)

const (
	defaultDAGRunWatchPollInterval  = 5 * time.Second
	defaultDAGRunWatchRetention     = 24 * time.Hour
	defaultDAGRunWatchMaxDuration   = 7 * 24 * time.Hour
	defaultDAGRunWatchPollTimeout   = 30 * time.Second
	defaultDAGRunWatchMaxTotal      = 1024
	defaultDAGRunWatchMaxPerSession = 64
)

// DAGRunWatchState is the lifecycle state of an agent-side DAG run watch.
type DAGRunWatchState string

const (
	DAGRunWatchStateRunning   DAGRunWatchState = "running"
	DAGRunWatchStateCompleted DAGRunWatchState = "completed"
	DAGRunWatchStateCanceled  DAGRunWatchState = "canceled"
	DAGRunWatchStateExpired   DAGRunWatchState = "expired"
)

// DAGRunWatcher registers lightweight, session-local watches for DAG runs.
// Watch registrations are intentionally in-memory; delivered notifications are
// persisted as normal session messages by the API layer.
type DAGRunWatcher interface {
	Watch(ctx context.Context, req DAGRunWatchRequest) (DAGRunWatchInfo, error)
	Status(ctx context.Context, req DAGRunWatchStatusRequest) (DAGRunWatchInfo, error)
	Cancel(ctx context.Context, req DAGRunWatchCancelRequest) (DAGRunWatchInfo, error)
}

type DAGRunWatchCleaner interface {
	ClearSession(sessionID string) int
}

// DAGRunWatchRequest describes a new run watch.
type DAGRunWatchRequest struct {
	SessionID         string
	User              UserIdentity
	DAGName           string
	DAGRunID          string
	SubDAGRunID       string
	NotifyOn          []string
	DiagnoseOnFailure bool
}

// DAGRunWatchStatusRequest identifies an existing watch.
type DAGRunWatchStatusRequest struct {
	SessionID   string
	WatchID     string
	DAGName     string
	DAGRunID    string
	SubDAGRunID string
}

// DAGRunWatchCancelRequest identifies a watch to cancel.
type DAGRunWatchCancelRequest struct {
	SessionID   string
	WatchID     string
	DAGName     string
	DAGRunID    string
	SubDAGRunID string
}

// DAGRunWatchInfo is returned to tools and stored by the registry.
type DAGRunWatchInfo struct {
	WatchID       string           `json:"watchId"`
	DAGName       string           `json:"dagName"`
	DAGRunID      string           `json:"dagRunId"`
	SubDAGRunID   string           `json:"subDAGRunId,omitempty"`
	State         DAGRunWatchState `json:"state"`
	Status        string           `json:"status,omitempty"`
	Notified      bool             `json:"notified"`
	CreatedAt     time.Time        `json:"createdAt"`
	CompletedAt   *time.Time       `json:"completedAt,omitempty"`
	LastCheckedAt time.Time        `json:"lastCheckedAt"`
	LastError     string           `json:"lastError,omitempty"`
}

type dagRunWatchNotifyFunc func(context.Context, DAGRunWatchRequest, DAGRunWatchInfo, *exec.DAGRunStatus) error

type dagRunWatchRegistry struct {
	store         exec.DAGRunStore
	notify        dagRunWatchNotifyFunc
	logger        *slog.Logger
	pollInterval  time.Duration
	retention     time.Duration
	maxDuration   time.Duration
	maxTotal      int
	maxPerSession int

	mu      sync.Mutex
	watches map[string]*dagRunWatchEntry
	byRun   map[string]string
}

type dagRunWatchEntry struct {
	req       DAGRunWatchRequest
	info      DAGRunWatchInfo
	cancel    context.CancelFunc
	notifying bool
}

type dagRunWatchRegistryOption func(*dagRunWatchRegistry)

func withDAGRunWatchPollInterval(interval time.Duration) dagRunWatchRegistryOption {
	return func(r *dagRunWatchRegistry) {
		r.pollInterval = interval
	}
}

func withDAGRunWatchMaxDuration(maxDuration time.Duration) dagRunWatchRegistryOption {
	return func(r *dagRunWatchRegistry) {
		r.maxDuration = maxDuration
	}
}

func newDAGRunWatchRegistry(store exec.DAGRunStore, notify dagRunWatchNotifyFunc, logger *slog.Logger, opts ...dagRunWatchRegistryOption) *dagRunWatchRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	r := &dagRunWatchRegistry{
		store:         store,
		notify:        notify,
		logger:        logger,
		pollInterval:  defaultDAGRunWatchPollInterval,
		retention:     defaultDAGRunWatchRetention,
		maxDuration:   defaultDAGRunWatchMaxDuration,
		maxTotal:      defaultDAGRunWatchMaxTotal,
		maxPerSession: defaultDAGRunWatchMaxPerSession,
		watches:       map[string]*dagRunWatchEntry{},
		byRun:         map[string]string{},
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.pollInterval <= 0 {
		r.pollInterval = defaultDAGRunWatchPollInterval
	}
	if r.retention <= 0 {
		r.retention = defaultDAGRunWatchRetention
	}
	if r.maxDuration <= 0 {
		r.maxDuration = defaultDAGRunWatchMaxDuration
	}
	return r
}

func (r *dagRunWatchRegistry) Watch(ctx context.Context, req DAGRunWatchRequest) (DAGRunWatchInfo, error) {
	if r == nil || r.store == nil {
		return DAGRunWatchInfo{}, errors.New("dag-run watcher is not configured")
	}
	req = normalizeDAGRunWatchRequest(req)
	if err := validateDAGRunWatchRequest(req); err != nil {
		return DAGRunWatchInfo{}, err
	}

	key := dagRunWatchKey(req.SessionID, req.DAGName, req.DAGRunID, req.SubDAGRunID)
	r.mu.Lock()
	r.pruneLocked(time.Now())
	if watchID := r.byRun[key]; watchID != "" {
		info := r.watches[watchID].info
		r.mu.Unlock()
		return info, nil
	}
	if r.maxTotal > 0 && r.runningWatchCountLocked("") >= r.maxTotal {
		r.mu.Unlock()
		return DAGRunWatchInfo{}, fmt.Errorf("too many active DAG run watches: limit is %d", r.maxTotal)
	}
	if r.maxPerSession > 0 && r.runningWatchCountLocked(req.SessionID) >= r.maxPerSession {
		r.mu.Unlock()
		return DAGRunWatchInfo{}, fmt.Errorf("too many active DAG run watches for this session: limit is %d", r.maxPerSession)
	}
	r.mu.Unlock()

	status, err := r.readStatus(ctx, req.DAGName, req.DAGRunID, req.SubDAGRunID)
	if err != nil {
		return DAGRunWatchInfo{}, err
	}

	now := time.Now()
	watchCtx, cancel := context.WithCancel(context.Background())
	state := DAGRunWatchStateRunning
	if isDAGRunWatchTerminal(status) {
		state = DAGRunWatchStateCompleted
	}
	info := DAGRunWatchInfo{
		WatchID:       uuid.NewString(),
		DAGName:       req.DAGName,
		DAGRunID:      req.DAGRunID,
		SubDAGRunID:   req.SubDAGRunID,
		State:         state,
		Status:        status.Status.String(),
		CreatedAt:     now,
		LastCheckedAt: now,
	}
	entry := &dagRunWatchEntry{req: req, info: info, cancel: cancel}

	r.mu.Lock()
	if watchID := r.byRun[key]; watchID != "" {
		cancel()
		info := r.watches[watchID].info
		r.mu.Unlock()
		return info, nil
	}
	r.watches[info.WatchID] = entry
	r.byRun[key] = info.WatchID
	r.mu.Unlock()

	if isDAGRunWatchTerminal(status) {
		cancel()
		return r.complete(ctx, info.WatchID, status)
	}

	go r.poll(watchCtx, info.WatchID)
	return info, nil
}

func (r *dagRunWatchRegistry) Status(ctx context.Context, req DAGRunWatchStatusRequest) (DAGRunWatchInfo, error) {
	if r == nil || r.store == nil {
		return DAGRunWatchInfo{}, errors.New("dag-run watcher is not configured")
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return DAGRunWatchInfo{}, errors.New("sessionId is required")
	}
	watchID, entry, err := r.findWatch(req.SessionID, req.WatchID, req.DAGName, req.DAGRunID, req.SubDAGRunID)
	if err != nil {
		return DAGRunWatchInfo{}, err
	}
	if entry.info.State != DAGRunWatchStateRunning {
		return entry.info, nil
	}
	if info, expired := r.expireIfOverdue(watchID, time.Now()); expired {
		return info, nil
	}

	status, readErr := r.readStatus(ctx, entry.req.DAGName, entry.req.DAGRunID, entry.req.SubDAGRunID)
	if readErr != nil {
		return r.markWatchError(watchID, readErr.Error()), nil
	}
	if isDAGRunWatchTerminal(status) {
		return r.complete(ctx, watchID, status)
	}
	return r.updateWatchStatus(watchID, status), nil
}

func (r *dagRunWatchRegistry) Cancel(_ context.Context, req DAGRunWatchCancelRequest) (DAGRunWatchInfo, error) {
	if r == nil {
		return DAGRunWatchInfo{}, errors.New("dag-run watcher is not configured")
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return DAGRunWatchInfo{}, errors.New("sessionId is required")
	}
	watchID, _, err := r.findWatch(req.SessionID, req.WatchID, req.DAGName, req.DAGRunID, req.SubDAGRunID)
	if err != nil {
		return DAGRunWatchInfo{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.watches[watchID]
	if entry == nil {
		return DAGRunWatchInfo{}, errors.New("watch not found")
	}
	if entry.info.State != DAGRunWatchStateRunning {
		return entry.info, nil
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	now := time.Now()
	entry.info.State = DAGRunWatchStateCanceled
	entry.info.CompletedAt = &now
	delete(r.byRun, dagRunWatchKey(entry.req.SessionID, entry.req.DAGName, entry.req.DAGRunID, entry.req.SubDAGRunID))
	return entry.info, nil
}

func (r *dagRunWatchRegistry) poll(ctx context.Context, watchID string) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		r.mu.Lock()
		entry := r.watches[watchID]
		if entry == nil || entry.info.State != DAGRunWatchStateRunning {
			r.mu.Unlock()
			return
		}
		req := entry.req
		r.mu.Unlock()
		if _, expired := r.expireIfOverdue(watchID, time.Now()); expired {
			return
		}

		statusCtx, cancelStatus := context.WithTimeout(ctx, r.pollTimeout())
		status, err := r.readStatus(statusCtx, req.DAGName, req.DAGRunID, req.SubDAGRunID)
		cancelStatus()
		if err != nil {
			r.markWatchError(watchID, err.Error())
			r.logger.Debug("failed to refresh DAG run watch", "watch_id", watchID, "dag", req.DAGName, "run_id", req.DAGRunID, "error", err)
			continue
		}
		if isDAGRunWatchTerminal(status) {
			notifyCtx, cancelNotify := context.WithTimeout(ctx, r.pollTimeout())
			if _, err := r.complete(notifyCtx, watchID, status); err != nil {
				r.logger.Warn("failed to notify DAG run watch", "watch_id", watchID, "dag", req.DAGName, "run_id", req.DAGRunID, "error", err)
			}
			cancelNotify()
			return
		}
		r.updateWatchStatus(watchID, status)
	}
}

func (r *dagRunWatchRegistry) complete(ctx context.Context, watchID string, status *exec.DAGRunStatus) (DAGRunWatchInfo, error) {
	now := time.Now()
	r.mu.Lock()
	entry := r.watches[watchID]
	if entry == nil {
		r.mu.Unlock()
		return DAGRunWatchInfo{}, errors.New("watch not found")
	}
	if entry.info.State == DAGRunWatchStateCanceled {
		info := entry.info
		r.mu.Unlock()
		return info, nil
	}
	entry.info.State = DAGRunWatchStateCompleted
	entry.info.Status = status.Status.String()
	entry.info.LastCheckedAt = now
	entry.info.CompletedAt = &now
	entry.info.LastError = ""
	if entry.cancel != nil {
		entry.cancel()
	}
	shouldNotify := r.notify != nil && dagRunWatchShouldNotify(entry.req, status) && !entry.info.Notified && !entry.notifying
	if shouldNotify {
		entry.notifying = true
	}
	req := entry.req
	info := entry.info
	r.mu.Unlock()

	if !shouldNotify {
		return info, nil
	}
	if err := r.notify(ctx, req, info, status); err != nil {
		r.mu.Lock()
		if entry := r.watches[watchID]; entry != nil {
			entry.info.LastError = "notification failed: " + err.Error()
			entry.notifying = false
			info = entry.info
		}
		r.mu.Unlock()
		return info, err
	}

	r.mu.Lock()
	if entry := r.watches[watchID]; entry != nil {
		entry.info.Notified = true
		entry.notifying = false
		info = entry.info
	}
	r.mu.Unlock()
	return info, nil
}

func (r *dagRunWatchRegistry) findWatch(sessionID, watchID, dagName, dagRunID, subDAGRunID string) (string, *dagRunWatchEntry, error) {
	sessionID = strings.TrimSpace(sessionID)
	watchID = strings.TrimSpace(watchID)
	r.mu.Lock()
	defer r.mu.Unlock()

	if watchID == "" {
		dagName = strings.TrimSpace(dagName)
		dagRunID = strings.TrimSpace(dagRunID)
		subDAGRunID = strings.TrimSpace(subDAGRunID)
		if dagName == "" || dagRunID == "" {
			return "", nil, errors.New("watchId or dagName/dagRunId is required")
		}
		watchID = r.byRun[dagRunWatchKey(sessionID, dagName, dagRunID, subDAGRunID)]
	}
	entry := r.watches[watchID]
	if entry == nil || entry.req.SessionID != sessionID {
		return "", nil, errors.New("watch not found")
	}
	copied := *entry
	return watchID, &copied, nil
}

func (r *dagRunWatchRegistry) readStatus(ctx context.Context, dagName, dagRunID, subDAGRunID string) (*exec.DAGRunStatus, error) {
	if r.store == nil {
		return nil, errors.New("dag-run store is not configured")
	}
	root := exec.NewDAGRunRef(dagName, dagRunID)
	var (
		attempt exec.DAGRunAttempt
		err     error
	)
	if subDAGRunID != "" {
		attempt, err = r.store.FindSubAttempt(ctx, root, subDAGRunID)
	} else {
		attempt, err = r.store.FindAttempt(ctx, root)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find DAG run: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read DAG run status: %w", err)
	}
	return status, nil
}

func (r *dagRunWatchRegistry) updateWatchStatus(watchID string, status *exec.DAGRunStatus) DAGRunWatchInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.watches[watchID]
	if entry == nil {
		return DAGRunWatchInfo{}
	}
	entry.info.Status = status.Status.String()
	entry.info.LastCheckedAt = time.Now()
	entry.info.LastError = ""
	return entry.info
}

func (r *dagRunWatchRegistry) markWatchError(watchID, message string) DAGRunWatchInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.watches[watchID]
	if entry == nil {
		return DAGRunWatchInfo{}
	}
	entry.info.LastCheckedAt = time.Now()
	entry.info.LastError = message
	return entry.info
}

func (r *dagRunWatchRegistry) ClearSession(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for watchID, entry := range r.watches {
		if entry == nil || entry.req.SessionID != sessionID {
			continue
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		delete(r.watches, watchID)
		delete(r.byRun, dagRunWatchKey(entry.req.SessionID, entry.req.DAGName, entry.req.DAGRunID, entry.req.SubDAGRunID))
		removed++
	}
	return removed
}

func (r *dagRunWatchRegistry) expireIfOverdue(watchID string, now time.Time) (DAGRunWatchInfo, bool) {
	if r.maxDuration <= 0 {
		return DAGRunWatchInfo{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.watches[watchID]
	if entry == nil || entry.info.State != DAGRunWatchStateRunning {
		if entry == nil {
			return DAGRunWatchInfo{}, false
		}
		return entry.info, false
	}
	if now.Sub(entry.info.CreatedAt) <= r.maxDuration {
		return entry.info, false
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	entry.info.State = DAGRunWatchStateExpired
	entry.info.CompletedAt = &now
	entry.info.LastCheckedAt = now
	entry.info.LastError = fmt.Sprintf("watch expired after %s", r.maxDuration)
	delete(r.byRun, dagRunWatchKey(entry.req.SessionID, entry.req.DAGName, entry.req.DAGRunID, entry.req.SubDAGRunID))
	return entry.info, true
}

func (r *dagRunWatchRegistry) pruneLocked(now time.Time) {
	if r.retention <= 0 {
		return
	}
	for watchID, entry := range r.watches {
		if entry == nil || entry.info.CompletedAt == nil {
			continue
		}
		if now.Sub(*entry.info.CompletedAt) <= r.retention {
			continue
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		delete(r.watches, watchID)
		delete(r.byRun, dagRunWatchKey(entry.req.SessionID, entry.req.DAGName, entry.req.DAGRunID, entry.req.SubDAGRunID))
	}
}

func (r *dagRunWatchRegistry) runningWatchCountLocked(sessionID string) int {
	count := 0
	for _, entry := range r.watches {
		if entry == nil || entry.info.State != DAGRunWatchStateRunning {
			continue
		}
		if sessionID != "" && entry.req.SessionID != sessionID {
			continue
		}
		count++
	}
	return count
}

func (r *dagRunWatchRegistry) pollTimeout() time.Duration {
	if r.pollInterval > defaultDAGRunWatchPollTimeout {
		return r.pollInterval
	}
	return defaultDAGRunWatchPollTimeout
}

func normalizeDAGRunWatchRequest(req DAGRunWatchRequest) DAGRunWatchRequest {
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.DAGName = strings.TrimSpace(req.DAGName)
	req.DAGRunID = strings.TrimSpace(req.DAGRunID)
	req.SubDAGRunID = strings.TrimSpace(req.SubDAGRunID)
	req.NotifyOn = normalizeDAGRunWatchNotifyOn(req.NotifyOn)
	return req
}

func validateDAGRunWatchRequest(req DAGRunWatchRequest) error {
	if req.SessionID == "" {
		return errors.New("sessionId is required")
	}
	if req.DAGName == "" {
		return errors.New("dagName is required")
	}
	if req.DAGRunID == "" {
		return errors.New("dagRunId is required")
	}
	return nil
}

func normalizeDAGRunWatchNotifyOn(values []string) []string {
	if len(values) == 0 {
		return []string{"terminal"}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "", "terminal", "complete", "completion", "finished", "finish":
			out = append(out, "terminal")
		case "failure", "failed", "error":
			out = append(out, "failure")
		case "success", "succeeded":
			out = append(out, "success")
		}
	}
	if len(out) == 0 {
		return []string{"terminal"}
	}
	return out
}

func dagRunWatchShouldNotify(req DAGRunWatchRequest, status *exec.DAGRunStatus) bool {
	if status == nil {
		return false
	}
	for _, value := range normalizeDAGRunWatchNotifyOn(req.NotifyOn) {
		switch value {
		case "terminal":
			return true
		case "failure":
			if !status.Status.IsSuccess() {
				return true
			}
		case "success":
			if status.Status.IsSuccess() {
				return true
			}
		}
	}
	return false
}

func isDAGRunWatchTerminal(status *exec.DAGRunStatus) bool {
	if status == nil {
		return false
	}
	return status.Status != core.NotStarted && !status.Status.IsActive()
}

func dagRunWatchKey(sessionID, dagName, dagRunID, subDAGRunID string) string {
	return strings.Join([]string{sessionID, dagName, dagRunID, subDAGRunID}, "\x00")
}

func formatDAGRunWatchNotification(req DAGRunWatchRequest, info DAGRunWatchInfo, status *exec.DAGRunStatus) string {
	target := req.DAGName + "/" + req.DAGRunID
	if req.SubDAGRunID != "" {
		target += " (subDAGRunId: " + req.SubDAGRunID + ")"
	}
	statusText := info.Status
	if statusText == "" && status != nil {
		statusText = status.Status.String()
	}
	lines := []string{
		"DAG run finished: " + target,
		"Status: " + statusText,
		"Watch ID: " + info.WatchID,
	}
	if status != nil && status.Error != "" {
		lines = append(lines, "Error: "+status.Error)
	}
	if req.DiagnoseOnFailure && status != nil && !status.Status.IsSuccess() {
		if node := firstFailedDAGRunNode(status); node != nil {
			stepLine := fmt.Sprintf("Primary failed step: %s (%s)", node.Step.Name, node.Status.String())
			lines = append(lines, stepLine)
			if node.Error != "" {
				lines = append(lines, "Step error: "+node.Error)
			}
		}
	}
	lines = append(lines, "Use `dag_run_manage` with action `get`, `diagnose`, `read_log`, or `read_messages` for details.")
	return strings.Join(lines, "\n")
}

func (a *API) notifyDAGRunWatch(ctx context.Context, req DAGRunWatchRequest, info DAGRunWatchInfo, status *exec.DAGRunStatus) error {
	content := formatDAGRunWatchNotification(req, info, status)
	llmMsg := llm.Message{Role: llm.RoleAssistant, Content: content}
	_, err := a.AppendExternalMessage(ctx, req.SessionID, req.User, Message{
		Type:      MessageTypeAssistant,
		Content:   content,
		CreatedAt: time.Now(),
		LLMData:   &llmMsg,
	})
	return err
}
