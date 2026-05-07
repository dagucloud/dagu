// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type workerInfo struct {
	workerID    string
	pollerID    string
	taskChan    chan *coordinatorv1.Task
	labels      map[string]string
	connectedAt time.Time
}

type heartbeatInfo struct {
	workerID        string
	labels          map[string]string
	stats           *coordinatorv1.WorkerStats
	lastHeartbeatAt time.Time
}

// defaultStaleHeartbeatThreshold is the default duration after which a worker's heartbeat is considered stale.
const defaultStaleHeartbeatThreshold = 30 * time.Second

// defaultStaleLeaseThreshold is the shared default duration after which a
// distributed run's lease is considered stale.
const defaultStaleLeaseThreshold = exec.DefaultStaleLeaseThreshold

// defaultLeaseRefreshWriteInterval is the maximum interval between persisted
// heartbeat-driven lease refreshes for a running distributed task.
const defaultLeaseRefreshWriteInterval = 5 * time.Second

const (
	remoteAttemptRejectedLeaseInactive = "stale attempt: lease no longer active"
	remoteAttemptRejectedSuperseded    = "stale attempt: superseded by newer attempt"
	remoteAttemptRejectedTerminal      = "stale attempt: run already terminal"
)

var (
	errNoAvailableWorkers        = errors.New("no available workers")
	errNoMatchingWorkers         = errors.New("no workers match the required selector")
	errRunHeartbeatRepairSkipped = errors.New("run heartbeat repair skipped")
)

type preparedDispatchAttempt struct {
	attempt      exec.DAGRunAttempt
	newlyCreated bool
}

type distributedLeaseState struct {
	attemptID  string
	attemptKey string
	lease      *exec.DAGRunLease
}

type Handler struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer

	mu             sync.Mutex
	waitingPollers map[string]*workerInfo    // pollerID -> worker info
	heartbeats     map[string]*heartbeatInfo // workerID -> heartbeat info
	owner          exec.CoordinatorEndpoint

	// Optional: for shared-nothing worker architecture
	dagRunStore               exec.DAGRunStore               // For status persistence
	logDir                    string                         // For log storage
	artifactDir               string                         // For artifact storage
	dispatchTaskStore         exec.DispatchTaskStore         // Shared distributed dispatch queue
	workerHeartbeatStore      exec.WorkerHeartbeatStore      // Shared worker presence
	dagRunLeaseStore          exec.DAGRunLeaseStore          // Shared distributed run leases
	activeDistributedRunStore exec.ActiveDistributedRunStore // Shared active distributed attempt index

	// Open attempts cache for status persistence
	attemptsMu   sync.RWMutex
	openAttempts map[string]exec.DAGRunAttempt // dagRunID -> open attempt

	// Per-run mutexes to prevent concurrent access to the same DAG run
	// This prevents races between ReportStatus and markRunFailed
	runMutexesMu sync.Mutex
	runMutexes   map[string]*sync.Mutex // dagRunID -> per-run mutex

	// Stale heartbeat threshold - configurable
	staleHeartbeatThreshold time.Duration

	// Stale lease threshold - configurable
	staleLeaseThreshold time.Duration

	// Zombie detector shutdown synchronization
	zombieDetectorMu      sync.Mutex
	zombieDetectorStarted bool
	zombieDetectorDone    chan struct{}

	eventService        *eventstore.Service
	eventSourceInstance string
}

// HandlerConfig holds configuration for creating a Handler.
type HandlerConfig struct {
	// DAGRunStore is the storage backend for DAG run status persistence.
	// Required for shared-nothing worker architecture.
	DAGRunStore exec.DAGRunStore

	// LogDir is the directory for log storage in shared-nothing mode.
	// Required for shared-nothing worker architecture.
	LogDir string

	// ArtifactDir is the directory for artifact storage in shared-nothing mode.
	// Required for shared-nothing worker architecture.
	ArtifactDir string

	// Owner identifies this coordinator instance for shared task ownership.
	Owner exec.CoordinatorEndpoint

	// DispatchTaskStore is the shared store for distributed pending tasks.
	DispatchTaskStore exec.DispatchTaskStore

	// WorkerHeartbeatStore is the shared store for worker presence.
	WorkerHeartbeatStore exec.WorkerHeartbeatStore

	// DAGRunLeaseStore is the shared store for active distributed attempt leases.
	DAGRunLeaseStore exec.DAGRunLeaseStore

	// ActiveDistributedRunStore is the shared store for the coordinator-owned
	// active distributed attempt index used by zombie detection.
	ActiveDistributedRunStore exec.ActiveDistributedRunStore

	// StaleHeartbeatThreshold is the duration after which a worker's heartbeat
	// is considered stale. Defaults to 30 seconds if not set.
	StaleHeartbeatThreshold time.Duration

	// StaleLeaseThreshold is the duration after which a distributed run's
	// lease is considered stale (worker stopped pushing status). Defaults to 90 seconds.
	StaleLeaseThreshold time.Duration

	// EventService persists coordinator-originated event envelopes.
	EventService *eventstore.Service

	// EventSourceInstance identifies this coordinator instance in event envelopes.
	EventSourceInstance string
}

// applyDefaults sets default values for optional fields.
func (c *HandlerConfig) applyDefaults() {
	if c.StaleHeartbeatThreshold == 0 {
		c.StaleHeartbeatThreshold = defaultStaleHeartbeatThreshold
	}
	if c.StaleLeaseThreshold == 0 {
		c.StaleLeaseThreshold = defaultStaleLeaseThreshold
	}
}

// NewHandler creates a new Handler with the given configuration.
func NewHandler(cfg HandlerConfig) *Handler {
	cfg.applyDefaults()
	return &Handler{
		waitingPollers:            make(map[string]*workerInfo),
		heartbeats:                make(map[string]*heartbeatInfo),
		openAttempts:              make(map[string]exec.DAGRunAttempt),
		runMutexes:                make(map[string]*sync.Mutex),
		owner:                     cfg.Owner,
		dagRunStore:               cfg.DAGRunStore,
		logDir:                    cfg.LogDir,
		artifactDir:               cfg.ArtifactDir,
		dispatchTaskStore:         cfg.DispatchTaskStore,
		workerHeartbeatStore:      cfg.WorkerHeartbeatStore,
		dagRunLeaseStore:          cfg.DAGRunLeaseStore,
		activeDistributedRunStore: cfg.ActiveDistributedRunStore,
		staleHeartbeatThreshold:   cfg.StaleHeartbeatThreshold,
		staleLeaseThreshold:       cfg.StaleLeaseThreshold,
		eventService:              cfg.EventService,
		eventSourceInstance:       cfg.EventSourceInstance,
	}
}

func (h *Handler) eventContext(ctx context.Context) context.Context {
	return eventstore.WithContext(ctx, h.eventService, eventstore.Source{
		Service:  eventstore.SourceServiceCoordinator,
		Instance: h.eventSourceInstance,
	})
}

// Close cleans up all resources held by the handler.
// This should be called during coordinator shutdown.
func (h *Handler) Close(ctx context.Context) {
	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()

	for dagRunID, attempt := range h.openAttempts {
		if err := attempt.Close(ctx); err != nil {
			logger.Warn(ctx, "Failed to close attempt during handler shutdown",
				tag.RunID(dagRunID), tag.Error(err))
		}
		delete(h.openAttempts, dagRunID)
	}
}

// getRunMutex returns a mutex for the given DAG run ID.
// This ensures serialized access to operations on the same DAG run
// across ReportStatus and zombie cleanup to prevent data races.
func (h *Handler) getRunMutex(dagRunID string) *sync.Mutex {
	h.runMutexesMu.Lock()
	defer h.runMutexesMu.Unlock()

	if mu, ok := h.runMutexes[dagRunID]; ok {
		return mu
	}

	mu := &sync.Mutex{}
	h.runMutexes[dagRunID] = mu
	return mu
}

// Poll implements long polling - workers wait until a task is available
func (h *Handler) Poll(ctx context.Context, req *coordinatorv1.PollRequest) (*coordinatorv1.PollResponse, error) {
	if req.PollerId == "" {
		return nil, status.Error(codes.InvalidArgument, "poller_id is required")
	}
	if h.dispatchTaskStore == nil {
		// Backward-compatible single-coordinator fallback for tests and legacy
		// in-memory coordination paths.
		h.mu.Lock()
		taskChan := make(chan *coordinatorv1.Task, 1)
		h.waitingPollers[req.PollerId] = &workerInfo{
			workerID:    req.WorkerId,
			pollerID:    req.PollerId,
			taskChan:    taskChan,
			labels:      req.Labels,
			connectedAt: time.Now(),
		}
		h.mu.Unlock()

		select {
		case task := <-taskChan:
			h.mu.Lock()
			delete(h.waitingPollers, req.PollerId)
			h.mu.Unlock()
			if task != nil {
				task.WorkerId = req.WorkerId
			}
			return &coordinatorv1.PollResponse{Task: task}, nil
		case <-ctx.Done():
			h.mu.Lock()
			delete(h.waitingPollers, req.PollerId)
			h.mu.Unlock()
			return nil, ctx.Err()
		}
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		claimed, err := h.dispatchTaskStore.ClaimNext(ctx, exec.DispatchTaskClaim{
			WorkerID:     req.WorkerId,
			PollerID:     req.PollerId,
			Labels:       req.Labels,
			Owner:        h.owner,
			ClaimTimeout: h.staleLeaseThreshold,
		})
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to claim task: "+err.Error())
		}
		if claimed != nil && claimed.Task != nil {
			claimed.Task.WorkerId = req.WorkerId
			return &coordinatorv1.PollResponse{Task: claimed.Task}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Dispatch tries to send a task to a waiting poller
// It fails if no pollers are available or no workers match the selector
func (h *Handler) Dispatch(ctx context.Context, req *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
	ctx = h.eventContext(ctx)
	if req.Task == nil {
		return nil, status.Error(codes.InvalidArgument, "task is required")
	}

	// Validate task.Definition is provided - required for distributed execution
	if req.Task.Definition == "" {
		return nil, status.Error(codes.InvalidArgument, "task.Definition is required for distributed execution")
	}

	logger.Info(ctx, "Handler Dispatch called",
		tag.RunID(req.Task.DagRunId),
		tag.Target(req.Task.Target),
		slog.String("operation", req.Task.Operation.String()),
	)

	if h.dispatchTaskStore == nil {
		if err := h.ensureWaitingWorkerAvailability(req.Task.WorkerSelector); err != nil {
			return nil, status.Error(dispatchErrorCode(err), err.Error())
		}

		var prepared *preparedDispatchAttempt
		if h.dagRunStore != nil {
			var err error
			prepared, err = h.prepareAttemptForDispatch(ctx, req.Task)
			if err != nil {
				return nil, status.Error(prepareAttemptErrorCode(err), "failed to prepare attempt: "+err.Error())
			}
		} else {
			h.ensureTaskAttemptMetadata(req.Task)
		}

		if err := h.dispatchToWaitingPoller(req.Task); err != nil {
			h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
			return nil, status.Error(dispatchErrorCode(err), err.Error())
		}
		return &coordinatorv1.DispatchResponse{}, nil
	}
	if h.dagRunStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "distributed dispatch requires DAG run storage")
	}

	healthyWorkers, err := h.listHealthyWorkers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list workers: "+err.Error())
	}
	if len(healthyWorkers) == 0 {
		return nil, status.Error(codes.Unavailable, errNoAvailableWorkers.Error())
	}
	if len(req.Task.WorkerSelector) > 0 && !anyWorkerMatches(healthyWorkers, req.Task.WorkerSelector) {
		return nil, status.Error(codes.FailedPrecondition, errNoMatchingWorkers.Error())
	}

	prepared, err := h.prepareAttemptForDispatch(ctx, req.Task)
	if err != nil {
		return nil, status.Error(prepareAttemptErrorCode(err), "failed to prepare attempt: "+err.Error())
	}
	if err := h.dispatchTaskStore.Enqueue(ctx, req.Task); err != nil {
		h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
		return nil, status.Error(codes.Internal, "failed to enqueue task: "+err.Error())
	}
	return &coordinatorv1.DispatchResponse{}, nil
}

func queueDispatchStatusForTask(task *coordinatorv1.Task) (*exec.DAGRunStatus, error) {
	if task == nil || task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return nil, nil
	}

	status, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to decode previous task status: %w", err)
	}
	if status == nil || status.Status != core.Queued {
		return nil, nil
	}
	return status, nil
}

func staleQueueDispatchError(reason string) error {
	return &exec.StaleQueueDispatchError{Reason: reason}
}

// createAttemptForTask creates a DAGRun attempt for a root-level task.
// This is called when the coordinator receives a dispatch for a root-level DAG run
// (not a sub-DAG), so it has a place to store status updates from the worker.
func (h *Handler) createAttemptForTask(ctx context.Context, task *coordinatorv1.Task) (*preparedDispatchAttempt, error) {
	if h.dagRunStore == nil {
		return nil, nil
	}

	loadOpts := []spec.LoadOption{spec.WithName(task.Target)}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	dag, err := spec.LoadYAML(ctx, []byte(task.Definition), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DAG definition: %w", err)
	}
	dag.SourceFile = task.SourceFile

	ref := exec.DAGRunRef{Name: dag.Name, ID: task.DagRunId}
	queueDispatchStatus, err := queueDispatchStatusForTask(task)
	if err != nil {
		return nil, err
	}

	// Check if dag-run already exists (e.g., queued via enqueue command)
	existingAttempt, findErr := h.dagRunStore.FindAttempt(ctx, ref)
	if queueDispatchStatus != nil {
		if queueDispatchStatus.AttemptID == "" {
			return nil, staleQueueDispatchError("queued attempt ID is missing")
		}
		if findErr != nil {
			if errors.Is(findErr, exec.ErrDAGRunIDNotFound) || errors.Is(findErr, exec.ErrNoStatusData) || errors.Is(findErr, exec.ErrCorruptedStatusFile) {
				return nil, staleQueueDispatchError("dag-run is no longer queued")
			}
			return nil, findErr
		}

		existingStatus, readErr := existingAttempt.ReadStatus(ctx)
		if readErr != nil {
			if errors.Is(readErr, exec.ErrNoStatusData) || errors.Is(readErr, exec.ErrCorruptedStatusFile) {
				return nil, staleQueueDispatchError("dag-run is no longer queued")
			}
			return nil, readErr
		}
		if existingAttempt.ID() != queueDispatchStatus.AttemptID {
			return nil, staleQueueDispatchError("queued attempt was superseded")
		}
		if existingStatus == nil || existingStatus.Status != core.Queued {
			statusLabel := "unknown"
			if existingStatus != nil {
				statusLabel = existingStatus.Status.String()
			}
			return nil, staleQueueDispatchError("latest attempt is " + statusLabel)
		}
	}
	if findErr == nil {
		existingStatus, readErr := existingAttempt.ReadStatus(ctx)
		if readErr == nil && existingStatus != nil && existingStatus.Status == core.Queued {
			task.AttemptId = existingAttempt.ID()
			task.AttemptKey = generateRootAttemptKey(task)

			if err := existingAttempt.Open(ctx); err != nil {
				return nil, fmt.Errorf("failed to open existing attempt: %w", err)
			}

			h.attemptsMu.Lock()
			h.openAttempts[task.DagRunId] = existingAttempt
			h.attemptsMu.Unlock()

			logger.Info(ctx, "Reusing existing queued attempt for dispatched task",
				tag.RunID(task.DagRunId),
				tag.Target(task.Target),
				tag.AttemptID(task.AttemptId),
				tag.AttemptKey(task.AttemptKey),
			)
			return &preparedDispatchAttempt{attempt: existingAttempt}, nil
		}
	}

	// Create new attempt (either first attempt or retry)
	isRetry := task.Operation == coordinatorv1.Operation_OPERATION_RETRY || findErr == nil
	opts := exec.NewDAGRunAttemptOptions{Retry: isRetry}

	attempt, err := h.dagRunStore.CreateAttempt(ctx, dag, time.Now(), task.DagRunId, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create attempt: %w", err)
	}

	task.AttemptId = attempt.ID()
	task.AttemptKey = generateRootAttemptKey(task)

	if err := attempt.Open(ctx); err != nil {
		return nil, fmt.Errorf("failed to open attempt: %w", err)
	}

	if err := h.writeInitialStatus(ctx, attempt, dag.Name, task.DagRunId, task.AttemptKey, task.ScheduleTime, exec.DAGRunRef{}, labelsForInitialStatus(task, dag)); err != nil {
		return nil, fmt.Errorf("failed to write initial status: %w", err)
	}

	h.attemptsMu.Lock()
	h.openAttempts[task.DagRunId] = attempt
	h.attemptsMu.Unlock()

	logger.Info(ctx, "Created DAGRun attempt for dispatched task",
		tag.RunID(task.DagRunId),
		tag.Target(task.Target),
		tag.AttemptID(task.AttemptId),
		tag.AttemptKey(task.AttemptKey),
	)

	return &preparedDispatchAttempt{attempt: attempt, newlyCreated: true}, nil
}

// generateRootAttemptKey creates an AttemptKey for root-level tasks (self-referential hierarchy).
func generateRootAttemptKey(task *coordinatorv1.Task) string {
	return exec.GenerateAttemptKey(task.Target, task.DagRunId, task.Target, task.DagRunId, task.AttemptId)
}

func (h *Handler) ensureTaskAttemptMetadata(task *coordinatorv1.Task) {
	if task == nil {
		return
	}
	if task.AttemptId == "" {
		task.AttemptId = uuid.Must(uuid.NewV7()).String()
	}
	if task.AttemptKey != "" {
		return
	}

	isRootRun := task.ParentDagRunId == "" &&
		(task.RootDagRunId == "" || task.RootDagRunId == task.DagRunId)
	if isRootRun {
		task.AttemptKey = generateRootAttemptKey(task)
		return
	}

	task.AttemptKey = exec.GenerateAttemptKey(
		task.RootDagRunName,
		task.RootDagRunId,
		task.Target,
		task.DagRunId,
		task.AttemptId,
	)
}

// createSubAttemptForTask creates a sub-DAG attempt under the root DAG run.
// This is called when the coordinator receives a dispatch for a sub-DAG
// (dispatched from a parent DAG), so it has a place to store status updates from the worker.
func (h *Handler) createSubAttemptForTask(ctx context.Context, task *coordinatorv1.Task) (*preparedDispatchAttempt, error) {
	if h.dagRunStore == nil {
		return nil, nil
	}

	rootRef := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}

	attempt, err := h.dagRunStore.CreateSubAttempt(ctx, rootRef, task.DagRunId)
	if err != nil {
		return nil, fmt.Errorf("failed to create sub-attempt: %w", err)
	}

	loadOpts := []spec.LoadOption{spec.WithName(task.Target)}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	dag, err := spec.LoadYAML(ctx, []byte(task.Definition), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DAG definition: %w", err)
	}
	dag.SourceFile = task.SourceFile
	attempt.SetDAG(dag)

	task.AttemptId = attempt.ID()
	task.AttemptKey = exec.GenerateAttemptKey(
		task.RootDagRunName, task.RootDagRunId,
		task.Target, task.DagRunId, attempt.ID(),
	)

	if err := attempt.Open(ctx); err != nil {
		return nil, fmt.Errorf("failed to open sub-attempt: %w", err)
	}

	if err := h.writeInitialStatus(ctx, attempt, task.Target, task.DagRunId, task.AttemptKey, task.ScheduleTime, rootRef, labelsForInitialStatus(task, dag)); err != nil {
		return nil, fmt.Errorf("failed to write initial status: %w", err)
	}

	h.attemptsMu.Lock()
	h.openAttempts[task.DagRunId] = attempt
	h.attemptsMu.Unlock()

	logger.Info(ctx, "Created sub-DAG attempt for distributed execution",
		tag.RunID(task.DagRunId),
		tag.DAG(task.Target),
		slog.String("root-dag-run-id", task.RootDagRunId),
		tag.AttemptKey(task.AttemptKey),
	)

	return &preparedDispatchAttempt{attempt: attempt, newlyCreated: true}, nil
}

// writeInitialStatus writes an initial NotStarted status to the attempt.
// This ensures the status file is not empty when read before the worker reports its first status.
func (h *Handler) writeInitialStatus(ctx context.Context, attempt exec.DAGRunAttempt, dagName, dagRunID, attemptKey, scheduleTime string, root exec.DAGRunRef, labels []string) error {
	initialStatus := exec.DAGRunStatus{
		Name:         dagName,
		DAGRunID:     dagRunID,
		AttemptID:    attempt.ID(),
		AttemptKey:   attemptKey,
		Status:       core.NotStarted,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		Root:         root,
		Labels:       labels,
		ScheduleTime: scheduleTime,
	}
	return attempt.Write(ctx, initialStatus)
}

func labelsForInitialStatus(task *coordinatorv1.Task, dag *core.DAG) []string {
	if task != nil {
		labels := splitTaskLabels(task.Labels)
		if len(labels) > 0 {
			return labels
		}
	}
	if dag == nil {
		return nil
	}
	return dag.Labels.Strings()
}

func splitTaskLabels(raw string) []string {
	parts := strings.Split(raw, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if label := strings.TrimSpace(part); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func (h *Handler) prepareAttemptForDispatch(ctx context.Context, task *coordinatorv1.Task) (*preparedDispatchAttempt, error) {
	if task == nil {
		return nil, fmt.Errorf("task is required")
	}
	if h.dagRunStore == nil {
		h.ensureTaskAttemptMetadata(task)
		return nil, nil
	}

	runMu := h.getRunMutex(task.DagRunId)
	runMu.Lock()
	defer runMu.Unlock()

	isRootRun := task.ParentDagRunId == "" &&
		(task.RootDagRunId == "" || task.RootDagRunId == task.DagRunId)
	if isRootRun {
		return h.createAttemptForTask(ctx, task)
	}
	if task.ParentDagRunId != "" {
		return h.createSubAttemptForTask(ctx, task)
	}

	h.ensureTaskAttemptMetadata(task)
	return nil, nil
}

func (h *Handler) ensureWaitingWorkerAvailability(selector map[string]string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	matched := false
	for _, worker := range h.waitingPollers {
		if !matchesSelector(worker.labels, selector) {
			continue
		}
		matched = true
		break
	}
	if matched {
		return nil
	}
	if len(selector) > 0 {
		return errNoMatchingWorkers
	}
	return errNoAvailableWorkers
}

func (h *Handler) dispatchToWaitingPoller(task *coordinatorv1.Task) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	matched := false
	for pollerID, worker := range h.waitingPollers {
		if !matchesSelector(worker.labels, task.WorkerSelector) {
			continue
		}
		matched = true
		select {
		case worker.taskChan <- task:
			delete(h.waitingPollers, pollerID)
			return nil
		default:
			delete(h.waitingPollers, pollerID)
		}
	}
	if len(task.WorkerSelector) > 0 && !matched {
		return errNoMatchingWorkers
	}
	return errNoAvailableWorkers
}

func dispatchErrorCode(err error) codes.Code {
	var staleErr *exec.StaleQueueDispatchError
	switch {
	case errors.Is(err, errNoMatchingWorkers):
		return codes.FailedPrecondition
	case errors.As(err, &staleErr):
		return codes.FailedPrecondition
	default:
		return codes.Unavailable
	}
}

func prepareAttemptErrorCode(err error) codes.Code {
	var staleErr *exec.StaleQueueDispatchError
	if errors.As(err, &staleErr) {
		return codes.FailedPrecondition
	}
	return codes.Internal
}

func (h *Handler) markPreparedAttemptDispatchFailed(ctx context.Context, task *coordinatorv1.Task, prepared *preparedDispatchAttempt, dispatchErr error) {
	if prepared == nil || prepared.attempt == nil {
		return
	}
	defer h.releasePreparedDispatchAttempt(context.WithoutCancel(ctx), task.GetDagRunId(), prepared.attempt)

	if !prepared.newlyCreated {
		return
	}

	storeCtx := context.WithoutCancel(ctx)
	runStatus, err := prepared.attempt.ReadStatus(storeCtx)
	if err != nil {
		logger.Warn(ctx, "Failed to read prepared attempt after dispatch handoff failure",
			tag.RunID(task.DagRunId),
			tag.Error(err),
		)
		return
	}
	if runStatus == nil {
		return
	}
	if runStatus.Status != core.NotStarted && runStatus.Status != core.Queued {
		return
	}

	runStatus.Status = core.Failed
	runStatus.FinishedAt = stringutil.FormatTime(time.Now())
	runStatus.Error = fmt.Sprintf("failed to hand off distributed task to a worker: %v", dispatchErr)
	if err := prepared.attempt.Write(storeCtx, *runStatus); err != nil {
		logger.Warn(ctx, "Failed to mark prepared attempt as failed after dispatch handoff failure",
			tag.RunID(task.DagRunId),
			tag.Error(err),
		)
		return
	}

	logger.Warn(ctx, "Marked prepared distributed attempt as FAILED after dispatch handoff failure",
		tag.RunID(task.DagRunId),
		tag.AttemptKey(task.AttemptKey),
		tag.Error(dispatchErr),
	)
}

func (h *Handler) releasePreparedDispatchAttempt(ctx context.Context, dagRunID string, attempt exec.DAGRunAttempt) {
	if attempt == nil {
		return
	}

	h.attemptsMu.Lock()
	if cachedAttempt, ok := h.openAttempts[dagRunID]; ok && cachedAttempt.ID() == attempt.ID() {
		delete(h.openAttempts, dagRunID)
	}
	h.attemptsMu.Unlock()

	if err := attempt.Close(ctx); err != nil {
		logger.Warn(ctx, "Failed to close prepared attempt after dispatch handoff failure",
			tag.RunID(dagRunID),
			tag.AttemptID(attempt.ID()),
			tag.Error(err),
		)
	}
}

// GetWorkers returns the list of currently connected workers
func (h *Handler) GetWorkers(_ context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
	if h.workerHeartbeatStore == nil {
		h.mu.Lock()
		defer h.mu.Unlock()

		workers := make([]*coordinatorv1.WorkerInfo, 0, len(h.heartbeats))
		now := time.Now()
		for _, hb := range h.heartbeats {
			workerInfo := &coordinatorv1.WorkerInfo{
				WorkerId:        hb.workerID,
				Labels:          hb.labels,
				LastHeartbeatAt: hb.lastHeartbeatAt.Unix(),
				HealthStatus:    calculateHealthStatus(now.Sub(hb.lastHeartbeatAt)),
			}
			if hb.stats != nil {
				workerInfo.TotalPollers = hb.stats.TotalPollers
				workerInfo.BusyPollers = hb.stats.BusyPollers
				workerInfo.RunningTasks = hb.stats.RunningTasks
			}
			workers = append(workers, workerInfo)
		}
		return &coordinatorv1.GetWorkersResponse{Workers: workers}, nil
	}

	records, err := h.workerHeartbeatStore.List(context.Background())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list workers: "+err.Error())
	}

	workers := make([]*coordinatorv1.WorkerInfo, 0, len(records))
	now := time.Now()

	for _, hb := range records {
		workerInfo := &coordinatorv1.WorkerInfo{
			WorkerId:        hb.WorkerID,
			Labels:          hb.Labels,
			LastHeartbeatAt: hb.LastHeartbeatTime().Unix(),
			HealthStatus:    calculateHealthStatus(now.Sub(hb.LastHeartbeatTime())),
		}

		if hb.Stats != nil {
			workerInfo.TotalPollers = hb.Stats.TotalPollers
			workerInfo.BusyPollers = hb.Stats.BusyPollers
			workerInfo.RunningTasks = hb.Stats.RunningTasks
		}

		workers = append(workers, workerInfo)
	}

	return &coordinatorv1.GetWorkersResponse{Workers: workers}, nil
}

// calculateHealthStatus determines worker health based on time since last heartbeat.
func calculateHealthStatus(sinceLastHeartbeat time.Duration) coordinatorv1.WorkerHealthStatus {
	const (
		healthyThreshold = 5 * time.Second
		warningThreshold = 15 * time.Second
	)

	switch {
	case sinceLastHeartbeat < healthyThreshold:
		return coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_HEALTHY
	case sinceLastHeartbeat < warningThreshold:
		return coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_WARNING
	default:
		return coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_UNHEALTHY
	}
}

// Heartbeat receives periodic status updates from workers.
func (h *Handler) Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	if req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	receivedAt := time.Now().UTC()
	h.mu.Lock()
	h.heartbeats[req.WorkerId] = &heartbeatInfo{
		workerID:        req.WorkerId,
		labels:          req.Labels,
		stats:           req.Stats,
		lastHeartbeatAt: receivedAt,
	}
	h.mu.Unlock()

	if h.workerHeartbeatStore != nil {
		if err := h.workerHeartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        req.WorkerId,
			Labels:          req.Labels,
			Stats:           req.Stats,
			LastHeartbeatAt: receivedAt.UnixMilli(),
		}); err != nil {
			return nil, status.Error(codes.Internal, "failed to persist worker heartbeat: "+err.Error())
		}
	}
	if h.dagRunLeaseStore == nil {
		h.refreshLeasesFromHeartbeat(ctx, req.WorkerId, req.Stats, receivedAt)
	}

	cancelledRuns := h.getCancelledRunsForWorker(ctx, req.Stats)

	return &coordinatorv1.HeartbeatResponse{
		CancelledRuns: cancelledRuns,
	}, nil
}

// AckTaskClaim confirms that a worker accepted a claimed task and creates the
// initial active lease for that distributed attempt.
func (h *Handler) AckTaskClaim(ctx context.Context, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	if req.ClaimToken == "" {
		return nil, status.Error(codes.InvalidArgument, "claim_token is required")
	}
	if h.dispatchTaskStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "dispatch task store is not configured")
	}
	if h.dagRunLeaseStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "dag-run lease store is not configured")
	}

	claimed, err := h.dispatchTaskStore.GetClaim(ctx, req.ClaimToken)
	if err != nil {
		if errors.Is(err, exec.ErrDispatchTaskNotFound) {
			return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "claim not found or expired"}, nil
		}
		return nil, status.Error(codes.Internal, "failed to load claim: "+err.Error())
	}
	if claimed.WorkerID != "" && req.WorkerId != "" && claimed.WorkerID != req.WorkerId {
		return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "claim belongs to a different worker"}, nil
	}
	if claimed.Task == nil {
		return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "claim has no task payload"}, nil
	}

	now := time.Now().UTC()
	if err := h.dagRunLeaseStore.Upsert(ctx, buildLeaseFromTask(claimed.Task, req.WorkerId, h.owner, now)); err != nil {
		return nil, status.Error(codes.Internal, "failed to create run lease: "+err.Error())
	}
	h.upsertActiveDistributedRunFromTask(ctx, claimed.Task, req.WorkerId, now)
	if err := h.dispatchTaskStore.DeleteClaim(ctx, req.ClaimToken); err != nil {
		return nil, status.Error(codes.Internal, "failed to finalize task claim: "+err.Error())
	}

	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

// RunHeartbeat refreshes leases for tasks owned by this coordinator and returns
// cancellation directives for those exact tasks.
func (h *Handler) RunHeartbeat(ctx context.Context, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	if req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if h.dagRunLeaseStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "dag-run lease store is not configured")
	}
	if h.owner.ID != "" && req.OwnerCoordinatorId != h.owner.ID {
		return nil, status.Error(codes.FailedPrecondition, "run heartbeat sent to non-owner coordinator")
	}

	cancelledRuns := make([]*coordinatorv1.CancelledRun, 0)
	observedAt := time.Now().UTC()
	for _, task := range req.RunningTasks {
		if task == nil || task.AttemptKey == "" {
			continue
		}
		if err := h.dagRunLeaseStore.Touch(ctx, task.AttemptKey, observedAt); err != nil {
			if errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
				cancelledRuns = appendCancelledRunIfMissing(cancelledRuns, task.AttemptKey)
				continue
			}
			return nil, status.Error(codes.Internal, "failed to refresh run lease: "+err.Error())
		}
		h.repairStaleLeaseFailureFromRunHeartbeat(ctx, req.WorkerId, task, observedAt)
	}

	cancelledRuns = appendCancelledRuns(cancelledRuns, h.getCancelledRunsForWorker(ctx, &coordinatorv1.WorkerStats{
		RunningTasks: req.RunningTasks,
	}))
	return &coordinatorv1.RunHeartbeatResponse{CancelledRuns: cancelledRuns}, nil
}

func (h *Handler) repairStaleLeaseFailureFromRunHeartbeat(
	ctx context.Context,
	workerID string,
	task *coordinatorv1.RunningTask,
	observedAt time.Time,
) {
	if h.dagRunStore == nil || h.dagRunLeaseStore == nil || task == nil || task.AttemptKey == "" {
		return
	}

	lease, err := h.dagRunLeaseStore.Get(ctx, task.AttemptKey)
	if err != nil {
		if !errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
			logger.Warn(ctx, "Failed to read distributed lease after run heartbeat",
				tag.AttemptKey(task.AttemptKey),
				tag.Error(err),
			)
		}
		return
	}
	if lease == nil || lease.AttemptID == "" || lease.WorkerID != workerID {
		return
	}

	reason := staleDistributedLeaseReason(workerID)
	storeCtx := context.WithoutCancel(ctx)
	repairedStatus, swapped, err := h.dagRunStore.CompareAndSwapLatestAttemptStatus(
		storeCtx,
		lease.DAGRun,
		lease.AttemptID,
		core.Failed,
		func(status *exec.DAGRunStatus) error {
			if !h.canRepairStaleLeaseFailureFromRunHeartbeat(workerID, task, lease, status, reason, observedAt) {
				return errRunHeartbeatRepairSkipped
			}
			restoreStaleLeaseFailure(status, lease, workerID, reason)
			return nil
		},
	)
	if err != nil {
		if errors.Is(err, errRunHeartbeatRepairSkipped) {
			return
		}
		logger.Warn(ctx, "Failed to repair stale distributed run failure after heartbeat",
			tag.RunID(lease.DAGRun.ID),
			tag.AttemptKey(task.AttemptKey),
			tag.Error(err),
		)
		return
	}
	if !swapped {
		return
	}

	h.upsertActiveDistributedRun(ctx, repairedStatus, workerID, lease.AttemptID)
	logger.Info(ctx, "Repaired stale distributed run failure from fresh heartbeat",
		tag.DAG(lease.DAGRun.Name),
		tag.RunID(lease.DAGRun.ID),
		tag.AttemptKey(task.AttemptKey),
	)
}

func (h *Handler) canRepairStaleLeaseFailureFromRunHeartbeat(
	workerID string,
	task *coordinatorv1.RunningTask,
	lease *exec.DAGRunLease,
	status *exec.DAGRunStatus,
	reason string,
	observedAt time.Time,
) bool {
	if workerID == "" || task == nil || lease == nil || status == nil {
		return false
	}
	if status.Status != core.Failed || status.Error != reason {
		return false
	}
	if task.AttemptKey == "" || lease.AttemptKey != task.AttemptKey {
		return false
	}
	if task.DagRunId != "" && lease.DAGRun.ID != "" && task.DagRunId != lease.DAGRun.ID {
		return false
	}
	if task.DagName != "" && lease.DAGRun.Name != "" && task.DagName != lease.DAGRun.Name {
		return false
	}
	if lease.WorkerID != "" && lease.WorkerID != workerID {
		return false
	}
	return exec.LeaseMatchesStatus(lease, status, lease.AttemptID, observedAt, h.staleLeaseThreshold)
}

func restoreStaleLeaseFailure(status *exec.DAGRunStatus, lease *exec.DAGRunLease, workerID, reason string) {
	status.Status = core.Running
	status.Error = ""
	status.FinishedAt = ""
	status.WorkerID = workerID
	status.AttemptID = lease.AttemptID
	status.AttemptKey = lease.AttemptKey
	for _, node := range status.Nodes {
		if node == nil || node.Status != core.NodeFailed || node.Error != reason {
			continue
		}
		if node.StartedAt != "" && node.StartedAt != "-" {
			node.Status = core.NodeRunning
			node.FinishedAt = ""
		} else {
			node.Status = core.NodeNotStarted
			node.StartedAt = "-"
			node.FinishedAt = "-"
		}
		node.Error = ""
	}
}

func (h *Handler) listHealthyWorkers(ctx context.Context) ([]exec.WorkerHeartbeatRecord, error) {
	if h.workerHeartbeatStore == nil {
		return nil, nil
	}

	records, err := h.workerHeartbeatStore.List(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	healthy := make([]exec.WorkerHeartbeatRecord, 0, len(records))
	for _, record := range records {
		if now.Sub(record.LastHeartbeatTime()) <= h.staleHeartbeatThreshold {
			healthy = append(healthy, record)
		}
	}
	return healthy, nil
}

func (h *Handler) refreshLeasesFromHeartbeat(ctx context.Context, workerID string, stats *coordinatorv1.WorkerStats, observedAt time.Time) {
	if h.dagRunStore == nil || stats == nil || len(stats.RunningTasks) == 0 {
		return
	}
	for _, task := range stats.RunningTasks {
		h.refreshLeaseForRunningTask(ctx, workerID, task, observedAt)
	}
}

func (h *Handler) refreshLeaseForRunningTask(ctx context.Context, workerID string, task *coordinatorv1.RunningTask, observedAt time.Time) {
	if task == nil {
		return
	}

	attempt, err := h.getOrOpenAttemptForRunningTask(ctx, task)
	if err != nil {
		logger.Warn(ctx, "Failed to resolve running task for lease refresh",
			tag.RunID(task.DagRunId),
			tag.WorkerID(workerID),
			tag.Error(err),
		)
		return
	}

	runMu := h.getRunMutex(task.DagRunId)
	runMu.Lock()
	defer runMu.Unlock()

	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to read status for heartbeat lease refresh",
			tag.RunID(task.DagRunId),
			tag.WorkerID(workerID),
			tag.Error(err),
		)
		return
	}

	if runStatus.Status != core.Running || runStatus.WorkerID == "" {
		return
	}
	if runStatus.WorkerID != workerID {
		return
	}
	if task.AttemptKey != "" && runStatus.AttemptKey != task.AttemptKey {
		return
	}
	if h.shouldThrottleLeaseRefresh(runStatus, observedAt) {
		return
	}

	runStatus.LeaseAt = observedAt.UnixMilli()
	if err := attempt.Write(ctx, *runStatus); err != nil {
		logger.Warn(ctx, "Failed to persist heartbeat lease refresh",
			tag.RunID(task.DagRunId),
			tag.WorkerID(workerID),
			tag.Error(err),
		)
	}
}

func (h *Handler) getOrOpenAttemptForRunningTask(ctx context.Context, task *coordinatorv1.RunningTask) (exec.DAGRunAttempt, error) {
	if task == nil {
		return nil, fmt.Errorf("running task is nil")
	}

	isSubDAG := task.RootDagRunId != "" && task.RootDagRunId != task.DagRunId
	if isSubDAG {
		if task.RootDagRunName == "" {
			return nil, fmt.Errorf("missing root dag run name for sub-dag %s", task.DagRunId)
		}
		return h.getOrOpenSubAttempt(ctx, exec.DAGRunRef{
			Name: task.RootDagRunName,
			ID:   task.RootDagRunId,
		}, task.DagRunId)
	}

	return h.getOrOpenAttempt(ctx, task.DagName, task.DagRunId)
}

func (h *Handler) shouldThrottleLeaseRefresh(status *exec.DAGRunStatus, observedAt time.Time) bool {
	if status == nil || status.LeaseAt == 0 {
		return false
	}

	lastLease := time.UnixMilli(status.LeaseAt)
	if lastLease.After(observedAt) {
		return false
	}
	return observedAt.Sub(lastLease) < h.leaseRefreshWriteInterval()
}

func (h *Handler) leaseRefreshWriteInterval() time.Duration {
	interval := defaultLeaseRefreshWriteInterval
	if h.staleLeaseThreshold > 0 {
		halfThreshold := h.staleLeaseThreshold / 2
		if halfThreshold > 0 && halfThreshold < interval {
			interval = halfThreshold
		}
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func anyWorkerMatches(workers []exec.WorkerHeartbeatRecord, selector map[string]string) bool {
	if len(selector) == 0 {
		return len(workers) > 0
	}
	for _, worker := range workers {
		if matchesSelector(worker.Labels, selector) {
			return true
		}
	}
	return false
}

func matchesSelector(workerLabels, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}
	for key, value := range selector {
		if workerLabels[key] != value {
			return false
		}
	}
	return true
}

func (h *Handler) collectAndRemoveStaleHeartbeats() []*heartbeatInfo {
	h.mu.Lock()
	defer h.mu.Unlock()

	staleThreshold := time.Now().Add(-h.staleHeartbeatThreshold)
	var stale []*heartbeatInfo
	for workerID, info := range h.heartbeats {
		if info.lastHeartbeatAt.Before(staleThreshold) {
			stale = append(stale, info)
			delete(h.heartbeats, workerID)
		}
	}
	return stale
}

func buildLeaseFromTask(task *coordinatorv1.Task, workerID string, owner exec.CoordinatorEndpoint, now time.Time) exec.DAGRunLease {
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.Zero() {
		root = exec.DAGRunRef{Name: task.Target, ID: task.DagRunId}
	}
	queueName := task.QueueName
	if queueName == "" {
		queueName = task.Target
	}
	return exec.DAGRunLease{
		AttemptKey: task.AttemptKey,
		DAGRun: exec.DAGRunRef{
			Name: task.Target,
			ID:   task.DagRunId,
		},
		Root:            root,
		AttemptID:       task.AttemptId,
		QueueName:       queueName,
		WorkerID:        workerID,
		Owner:           owner,
		ClaimedAt:       now.UnixMilli(),
		LastHeartbeatAt: now.UnixMilli(),
	}
}

func queueNameForStatus(status *exec.DAGRunStatus) string {
	if status == nil || status.ProcGroup == "" {
		if status == nil {
			return ""
		}
		return status.Name
	}
	return status.ProcGroup
}

func appendCancelledRuns(dst []*coordinatorv1.CancelledRun, src []*coordinatorv1.CancelledRun) []*coordinatorv1.CancelledRun {
	for _, cancelled := range src {
		if cancelled == nil || cancelled.AttemptKey == "" {
			continue
		}
		dst = appendCancelledRunIfMissing(dst, cancelled.AttemptKey)
	}
	return dst
}

func appendCancelledRunIfMissing(cancelledRuns []*coordinatorv1.CancelledRun, attemptKey string) []*coordinatorv1.CancelledRun {
	if attemptKey == "" {
		return cancelledRuns
	}
	for _, cancelled := range cancelledRuns {
		if cancelled != nil && cancelled.AttemptKey == attemptKey {
			return cancelledRuns
		}
	}
	return append(cancelledRuns, &coordinatorv1.CancelledRun{AttemptKey: attemptKey})
}

// getCancelledRunsForWorker checks which of the worker's running tasks have been cancelled.
func (h *Handler) getCancelledRunsForWorker(ctx context.Context, stats *coordinatorv1.WorkerStats) []*coordinatorv1.CancelledRun {
	if h.dagRunStore == nil || stats == nil || len(stats.RunningTasks) == 0 {
		return nil
	}

	var cancelledRuns []*coordinatorv1.CancelledRun
	for _, task := range stats.RunningTasks {
		if h.isTaskCancelled(ctx, task) {
			cancelledRuns = appendCancelledRunIfMissing(cancelledRuns, task.AttemptKey)
		}
	}
	return cancelledRuns
}

// isTaskCancelled checks if a task has been marked for cancellation.
func (h *Handler) isTaskCancelled(ctx context.Context, task *coordinatorv1.RunningTask) bool {
	if task == nil {
		return false
	}

	attempt, runStatus, err := h.resolveLatestAttemptForRunningTask(ctx, task)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) {
			return true
		}
		logger.Warn(ctx, "Failed to resolve latest attempt while checking cancellation",
			tag.RunID(task.DagRunId),
			tag.AttemptKey(task.AttemptKey),
			tag.Error(err),
		)
		return false
	}

	if task.AttemptKey != "" && runStatus.AttemptKey != "" && runStatus.AttemptKey != task.AttemptKey {
		return true
	}
	if isCancellableTerminalRunStatus(runStatus.Status) {
		return true
	}

	aborting, err := attempt.IsAborting(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to check abort state while checking cancellation",
			tag.RunID(task.DagRunId),
			tag.AttemptKey(task.AttemptKey),
			tag.Error(err),
		)
		return false
	}
	return aborting
}

// ReportStatus receives status updates from workers and persists them.
// This is used in shared-nothing architecture where workers don't have filesystem access.
func (h *Handler) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	ctx = h.eventContext(ctx)

	if h.dagRunStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "status reporting not configured: DAG run storage not available")
	}
	if h.owner.ID != "" && req.OwnerCoordinatorId != h.owner.ID {
		return nil, status.Error(codes.FailedPrecondition, "status update sent to non-owner coordinator")
	}

	if req.Status == nil {
		return nil, status.Error(codes.InvalidArgument, "status is required")
	}

	// Convert proto to execution.DAGRunStatus
	dagRunStatus, convErr := convert.ProtoToDAGRunStatus(req.Status)
	if convErr != nil {
		return nil, status.Error(codes.InvalidArgument, "failed to convert status: "+convErr.Error())
	}

	// Transform worker-local log paths to coordinator paths (shared-nothing mode)
	h.transformLogPaths(dagRunStatus)
	if h.dagRunLeaseStore == nil {
		dagRunStatus.LeaseAt = time.Now().UnixMilli()
	}

	// Acquire per-run mutex to serialize with markRunFailed
	runMu := h.getRunMutex(dagRunStatus.DAGRunID)
	runMu.Lock()
	defer runMu.Unlock()

	latestAttempt, latestStatus, err := h.resolveLatestAttempt(ctx, dagRunStatus.Name, dagRunStatus.DAGRunID, dagRunStatus.Root)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) {
			h.logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, nil, remoteAttemptRejectedLeaseInactive)
			return &coordinatorv1.ReportStatusResponse{
				Accepted: false,
				Error:    remoteAttemptRejectedLeaseInactive,
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to resolve latest attempt: "+err.Error())
	}

	accepted, rejectReason := h.remoteStatusDecision(ctx, latestStatus, dagRunStatus)
	if !accepted {
		h.logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, latestStatus, rejectReason)
		return &coordinatorv1.ReportStatusResponse{
			Accepted: false,
			Error:    rejectReason,
		}, nil
	}
	if err := h.transformArtifactPaths(ctx, latestAttempt, latestStatus, dagRunStatus); err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve artifact path: "+err.Error())
	}

	attempt, err := h.replaceOpenAttempt(ctx, dagRunStatus.DAGRunID, latestAttempt, latestStatus.AttemptID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get/open latest attempt: "+err.Error())
	}
	// Write the status
	if err := attempt.Write(ctx, *dagRunStatus); err != nil {
		return nil, status.Error(codes.Internal, "failed to write status: "+err.Error())
	}

	// Persist chat messages for each node (shared-nothing mode)
	// This enables message persistence when workers don't have filesystem access
	h.persistChatMessages(ctx, attempt, dagRunStatus)

	// Keep distributed liveness in the dedicated lease store and active index,
	// not in run history.
	h.syncDistributedRunTrackingFromStatus(ctx, req.WorkerId, dagRunStatus, attempt.ID())

	// Note: We don't close the attempt immediately on terminal status because
	// the agent may push the same terminal status multiple times from different
	// code paths. Attempts are cleaned up during coordinator shutdown.
	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

func isTerminalRunStatus(status core.Status) bool {
	return status != core.NotStarted && !status.IsActive()
}

func isCancellableTerminalRunStatus(status core.Status) bool {
	return isTerminalRunStatus(status) && !status.IsSuccess()
}

func sameAttemptStatus(current, incoming *exec.DAGRunStatus) bool {
	if current == nil || incoming == nil {
		return false
	}
	if current.AttemptID == "" && current.AttemptKey == "" {
		return true
	}
	if current.AttemptID != "" && incoming.AttemptID != "" && current.AttemptID != incoming.AttemptID {
		return false
	}
	if current.AttemptKey != "" && incoming.AttemptKey != "" && current.AttemptKey != incoming.AttemptKey {
		return false
	}
	if current.AttemptID != "" && incoming.AttemptID != "" {
		return true
	}
	return current.AttemptKey != "" && current.AttemptKey == incoming.AttemptKey
}

func (h *Handler) remoteStatusDecision(ctx context.Context, latest, incoming *exec.DAGRunStatus) (accepted bool, rejectionReason string) {
	if latest == nil || incoming == nil {
		return false, remoteAttemptRejectedLeaseInactive
	}
	if !sameAttemptStatus(latest, incoming) {
		return false, remoteAttemptRejectedSuperseded
	}
	if !isTerminalRunStatus(latest.Status) {
		return true, ""
	}
	if h.isLeaseInactive(ctx, latest.AttemptKey) && (incoming.Status.IsActive() || incoming.Status == core.NotStarted) {
		return false, remoteAttemptRejectedLeaseInactive
	}
	if latest.Status == incoming.Status {
		return true, ""
	}
	return false, remoteAttemptRejectedTerminal
}

func (h *Handler) isLeaseInactive(ctx context.Context, attemptKey string) bool {
	if h.dagRunLeaseStore == nil || attemptKey == "" {
		return false
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, attemptKey)
	switch {
	case err == nil:
		return !lease.IsFresh(time.Now().UTC(), h.staleLeaseThreshold)
	case errors.Is(err, exec.ErrDAGRunLeaseNotFound):
		return true
	default:
		logger.Warn(ctx, "Failed to read distributed lease for status validation",
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
		return false
	}
}

func (h *Handler) logRejectedRemoteStatusUpdate(
	ctx context.Context,
	workerID string,
	incoming *exec.DAGRunStatus,
	latest *exec.DAGRunStatus,
	reason string,
) {
	attrs := []slog.Attr{
		tag.WorkerID(workerID),
		slog.String("reason", reason),
	}
	if incoming != nil {
		attrs = append(attrs,
			tag.RunID(incoming.DAGRunID),
			tag.AttemptID(incoming.AttemptID),
			tag.AttemptKey(incoming.AttemptKey),
			slog.String("reported-status", incoming.Status.String()),
		)
	}
	if latest != nil {
		attrs = append(attrs,
			slog.String("latest-attempt-id", latest.AttemptID),
			slog.String("latest-attempt-key", latest.AttemptKey),
			slog.String("latest-status", latest.Status.String()),
		)
	}
	logger.Warn(ctx, "Rejected remote status update", attrs...)
}

// transformLogPaths rewrites worker-local log paths to coordinator paths.
// This is called when logDir is configured (shared-nothing mode).
func (h *Handler) transformLogPaths(status *exec.DAGRunStatus) {
	if h.logDir == "" {
		return // Not in shared-nothing mode, keep original paths
	}

	dagName := status.Name
	dagRunID := status.DAGRunID
	attemptID := status.AttemptID

	// For sub-DAGs, logs are stored under root DAG's directory
	if status.Root.ID != "" && status.Root.ID != dagRunID {
		dagName = status.Root.Name
		dagRunID = status.Root.ID
	}

	// Use dagRunID as attemptID if not set (matches log_handler.go logic)
	if attemptID == "" {
		attemptID = status.DAGRunID
	}

	// Helper to compute coordinator log path
	computePath := func(stepName string, streamType coordinatorv1.LogStreamType) string {
		ext := StreamTypeToExtension(streamType)
		filename := fmt.Sprintf("%s.%s", fileutil.SafeName(stepName), ext)
		return filepath.Join(
			h.logDir,
			fileutil.SafeName(dagName),
			fileutil.SafeName(dagRunID),
			fileutil.SafeName(attemptID),
			filename,
		)
	}

	// Transform node log paths
	transformNode := func(node *exec.Node, fallbackName string) {
		if node == nil {
			return
		}
		// Use step name, or fallback for handler nodes with empty names
		stepName := node.Step.Name
		if stepName == "" {
			stepName = fallbackName
		}
		node.Stdout = computePath(stepName, coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT)
		node.Stderr = computePath(stepName, coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR)
	}

	// Transform all regular nodes
	for _, node := range status.Nodes {
		transformNode(node, "step")
	}

	// Transform handler nodes with explicit fallback names
	transformNode(status.OnInit, "on_init")
	transformNode(status.OnExit, "on_exit")
	transformNode(status.OnSuccess, "on_success")
	transformNode(status.OnFailure, "on_failure")
	transformNode(status.OnAbort, "on_abort")
	transformNode(status.OnWait, "on_wait")

	// Transform scheduler log path
	status.Log = filepath.Join(
		h.logDir,
		fileutil.SafeName(dagName),
		fileutil.SafeName(dagRunID),
		fileutil.SafeName(attemptID),
		"scheduler.log",
	)
}

// transformArtifactPaths rewrites worker-local artifact directories to coordinator paths.
func (h *Handler) transformArtifactPaths(
	ctx context.Context,
	attempt exec.DAGRunAttempt,
	latestStatus *exec.DAGRunStatus,
	incoming *exec.DAGRunStatus,
) error {
	if incoming == nil {
		return nil
	}
	if latestStatus != nil && latestStatus.ArchiveDir != "" {
		incoming.ArchiveDir = latestStatus.ArchiveDir
	} else {
		if incoming.ArchiveDir == "" {
			return nil
		}
		if attempt == nil {
			return fmt.Errorf("dag run attempt is required to resolve artifact path")
		}

		dag, err := attempt.ReadDAG(ctx)
		if err != nil {
			return fmt.Errorf("read DAG for artifact path: %w", err)
		}
		if dag == nil {
			return fmt.Errorf("read DAG for artifact path: DAG is nil")
		}

		baseDir := h.artifactDir
		if dag.Artifacts != nil && dag.Artifacts.Dir != "" {
			baseDir = dag.Artifacts.Dir
		}
		baseDir = strings.TrimSpace(baseDir)
		if baseDir == "" {
			return fmt.Errorf("artifact directory is not configured")
		}
		baseDir, err = eval.String(ctx, baseDir, eval.WithOSExpansion())
		if err != nil {
			return fmt.Errorf("expand artifact directory: %w", err)
		}
		baseDir = strings.TrimSpace(baseDir)
		if baseDir == "" {
			return fmt.Errorf("artifact directory is empty after expansion")
		}

		archiveName := filepath.Base(filepath.Clean(incoming.ArchiveDir))
		if archiveName == "." || archiveName == string(filepath.Separator) || archiveName == "" {
			return fmt.Errorf("invalid artifact directory %q", incoming.ArchiveDir)
		}

		incoming.ArchiveDir = filepath.Join(
			baseDir,
			fileutil.SafeName(dag.Name),
			archiveName,
		)
	}
	if incoming.ArchiveDir == "" {
		return nil
	}
	if err := os.MkdirAll(incoming.ArchiveDir, 0o750); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	return nil
}

// persistChatMessages writes chat messages from status to the attempt.
// This enables message persistence in shared-nothing mode where workers
// don't have filesystem access to the coordinator's storage.
// Errors are logged but don't fail the status update since messages are auxiliary data.
func (h *Handler) persistChatMessages(ctx context.Context, attempt exec.DAGRunAttempt, status *exec.DAGRunStatus) {
	// Helper to persist messages for a single node
	persistNode := func(node *exec.Node, fallbackName string) {
		if node == nil || len(node.ChatMessages) == 0 {
			return
		}
		// Use step name, or fallback for handler nodes with empty names
		stepName := node.Step.Name
		if stepName == "" {
			stepName = fallbackName
		}
		if err := attempt.WriteStepMessages(ctx, stepName, node.ChatMessages); err != nil {
			logger.Warn(ctx, "Failed to persist chat messages",
				tag.Step(stepName),
				tag.Error(err),
			)
		}
	}

	// Persist messages for regular nodes
	for _, node := range status.Nodes {
		persistNode(node, "step")
	}

	// Persist messages for handler nodes with explicit fallback names
	persistNode(status.OnInit, "on_init")
	persistNode(status.OnExit, "on_exit")
	persistNode(status.OnSuccess, "on_success")
	persistNode(status.OnFailure, "on_failure")
	persistNode(status.OnAbort, "on_abort")
	persistNode(status.OnWait, "on_wait")
}

func (h *Handler) syncDistributedRunTrackingFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	h.syncLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
	h.syncActiveDistributedRunFromStatus(ctx, workerID, status, fallbackAttemptID)
}

func (h *Handler) syncLeaseFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if h.dagRunLeaseStore == nil || status == nil {
		return
	}

	switch status.Status {
	case core.Running, core.NotStarted:
		h.upsertDistributedLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
	case core.Failed, core.Aborted, core.Succeeded, core.Queued,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
		attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
		if attemptKey == "" {
			return
		}
		if err := h.dagRunLeaseStore.Delete(ctx, attemptKey); err != nil {
			logger.Warn(ctx, "Failed to delete distributed run lease",
				tag.RunID(status.DAGRunID),
				tag.Error(err),
			)
		}
	}
}

func (h *Handler) upsertDistributedLeaseFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if h.dagRunLeaseStore == nil || status == nil {
		return
	}

	attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
		return
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if attemptID == "" {
		return
	}

	if workerID == "" {
		workerID = status.WorkerID
	}
	if !exec.IsRemoteWorkerID(workerID) {
		return
	}

	queueName := queueNameForStatus(status)
	now := time.Now().UTC()
	lease := exec.DAGRunLease{
		AttemptKey: attemptKey,
		DAGRun: exec.DAGRunRef{
			Name: status.Name,
			ID:   status.DAGRunID,
		},
		Root:            status.Root,
		AttemptID:       attemptID,
		QueueName:       queueName,
		WorkerID:        workerID,
		Owner:           h.owner,
		ClaimedAt:       now.UnixMilli(),
		LastHeartbeatAt: now.UnixMilli(),
	}
	if existing, err := h.dagRunLeaseStore.Get(ctx, attemptKey); err == nil && existing != nil {
		lease.ClaimedAt = existing.ClaimedAt
		if status.ProcGroup == "" && existing.QueueName != "" {
			lease.QueueName = existing.QueueName
		}
	}
	if err := h.dagRunLeaseStore.Upsert(ctx, lease); err != nil {
		logger.Warn(ctx, "Failed to upsert distributed run lease",
			tag.RunID(status.DAGRunID),
			tag.Error(err),
		)
	}
}

func (h *Handler) restoreConfirmedDistributedRunTrackingFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if status == nil {
		return
	}

	switch status.Status {
	case core.Running, core.NotStarted, core.Queued:
		h.upsertDistributedLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
		h.upsertActiveDistributedRun(ctx, status, workerID, fallbackAttemptID)
	case core.Failed, core.Aborted, core.Succeeded,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
	}
}

func (h *Handler) syncActiveDistributedRunFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if h.activeDistributedRunStore == nil || status == nil {
		return
	}

	attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
		return
	}

	switch status.Status {
	case core.Running, core.NotStarted:
		h.upsertActiveDistributedRun(ctx, status, workerID, fallbackAttemptID)
	case core.Failed, core.Aborted, core.Succeeded, core.Queued,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
		if err := h.activeDistributedRunStore.Delete(ctx, attemptKey); err != nil {
			logger.Warn(ctx, "Failed to delete active distributed run",
				tag.RunID(status.DAGRunID),
				tag.AttemptKey(attemptKey),
				tag.Error(err),
			)
		}
	}
}

// getOrOpenAttempt retrieves an open attempt from cache or opens a new one.
// Uses double-check locking to avoid holding the mutex during blocking I/O.
func (h *Handler) getOrOpenAttempt(ctx context.Context, dagName, dagRunID string) (exec.DAGRunAttempt, error) {
	ref := exec.DAGRunRef{Name: dagName, ID: dagRunID}
	return h.getOrOpenAttemptWithFinder(ctx, dagRunID, func() (exec.DAGRunAttempt, error) {
		return h.dagRunStore.FindAttempt(ctx, ref)
	})
}

func (h *Handler) resolveLatestAttempt(
	ctx context.Context,
	dagName, dagRunID string,
	rootRef exec.DAGRunRef,
) (exec.DAGRunAttempt, *exec.DAGRunStatus, error) {
	if h.dagRunStore == nil {
		return nil, nil, exec.ErrDAGRunIDNotFound
	}

	var (
		attempt exec.DAGRunAttempt
		err     error
	)
	if rootRef.ID != "" && rootRef.ID != dagRunID {
		if rootRef.Name == "" {
			return nil, nil, fmt.Errorf("missing root dag run name for sub-dag %s", dagRunID)
		}
		attempt, err = h.dagRunStore.FindSubAttempt(ctx, rootRef, dagRunID)
	} else {
		attempt, err = h.dagRunStore.FindAttempt(ctx, exec.DAGRunRef{Name: dagName, ID: dagRunID})
	}
	if err != nil {
		return nil, nil, err
	}

	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	return attempt, runStatus, nil
}

func (h *Handler) resolveLatestAttemptForRunningTask(ctx context.Context, task *coordinatorv1.RunningTask) (exec.DAGRunAttempt, *exec.DAGRunStatus, error) {
	rootRef := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	return h.resolveLatestAttempt(ctx, task.DagName, task.DagRunId, rootRef)
}

func (h *Handler) replaceOpenAttempt(
	ctx context.Context,
	cacheKey string,
	latestAttempt exec.DAGRunAttempt,
	expectedAttemptID string,
) (exec.DAGRunAttempt, error) {
	h.attemptsMu.Lock()
	cachedAttempt, ok := h.openAttempts[cacheKey]
	if ok && cachedAttempt.ID() == expectedAttemptID {
		h.attemptsMu.Unlock()
		return cachedAttempt, nil
	}
	if ok {
		delete(h.openAttempts, cacheKey)
	}
	h.attemptsMu.Unlock()

	if ok {
		if err := cachedAttempt.Close(ctx); err != nil {
			logger.Warn(ctx, "Failed to close stale cached attempt",
				tag.RunID(cacheKey),
				tag.AttemptID(cachedAttempt.ID()),
				tag.Error(err),
			)
		}
	}

	if err := latestAttempt.Open(ctx); err != nil {
		return nil, err
	}

	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()
	h.openAttempts[cacheKey] = latestAttempt
	return latestAttempt, nil
}

// getOrOpenSubAttempt retrieves an open sub-attempt from cache or opens a new one.
// This is used for sub-DAG status reporting in distributed execution.
func (h *Handler) getOrOpenSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	return h.getOrOpenAttemptWithFinder(ctx, subDAGRunID, func() (exec.DAGRunAttempt, error) {
		return h.dagRunStore.FindSubAttempt(ctx, rootRef, subDAGRunID)
	})
}

// getOrOpenAttemptWithFinder is a generic helper that retrieves an open attempt from cache
// or uses the provided finder function to locate and open a new one.
// Uses per-run mutex to prevent concurrent I/O operations on the same DAG run,
// avoiding races between ReportStatus and markRunFailed.
func (h *Handler) getOrOpenAttemptWithFinder(ctx context.Context, cacheKey string, finder func() (exec.DAGRunAttempt, error)) (exec.DAGRunAttempt, error) {
	// First check: fast path with read lock (no per-run mutex needed for cache hit)
	h.attemptsMu.RLock()
	if attempt, ok := h.openAttempts[cacheKey]; ok {
		h.attemptsMu.RUnlock()
		return attempt, nil
	}
	h.attemptsMu.RUnlock()

	// Acquire per-run mutex to serialize I/O operations for this DAG run
	runMu := h.getRunMutex(cacheKey)
	runMu.Lock()
	defer runMu.Unlock()

	// Re-check cache after acquiring per-run mutex (another goroutine may have opened it)
	h.attemptsMu.RLock()
	if attempt, ok := h.openAttempts[cacheKey]; ok {
		h.attemptsMu.RUnlock()
		return attempt, nil
	}
	h.attemptsMu.RUnlock()

	// Perform I/O with per-run mutex held
	attempt, err := finder()
	if err != nil {
		return nil, err
	}

	if err := attempt.Open(ctx); err != nil {
		return nil, err
	}

	// Add to cache under write lock
	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()

	// Final check: if somehow another goroutine got through, use theirs
	if existing, ok := h.openAttempts[cacheKey]; ok {
		if err := attempt.Close(ctx); err != nil {
			logger.Warn(ctx, "Failed to close duplicate opened attempt", tag.Error(err))
		}
		return existing, nil
	}

	h.openAttempts[cacheKey] = attempt
	return attempt, nil
}

// StreamLogs receives log streams from workers and writes them to local filesystem.
// This is used in shared-nothing architecture where workers don't have filesystem access.
func (h *Handler) StreamLogs(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
	if h.logDir == "" {
		return status.Error(codes.FailedPrecondition, "log streaming not configured: logDir is empty")
	}

	// Delegate to the log handler
	logHandler := newLogHandler(h.logDir, h.owner.ID)
	defer logHandler.Close(stream.Context()) // Ensure file handles are closed on stream end or error
	return logHandler.handleStream(stream)
}

// StreamArtifacts receives artifact streams from workers and writes them to local filesystem.
func (h *Handler) StreamArtifacts(stream coordinatorv1.CoordinatorService_StreamArtifactsServer) error {
	if h.artifactDir == "" {
		return status.Error(codes.FailedPrecondition, "artifact streaming not configured: artifactDir is empty")
	}
	if h.dagRunStore == nil {
		return status.Error(codes.FailedPrecondition, "artifact streaming not configured: dagRunStore is empty")
	}

	artifactHandler := newArtifactHandler(h.dagRunStore, h.owner.ID)
	defer artifactHandler.Close(stream.Context())
	return artifactHandler.handleStream(stream)
}

// GetDAGRunStatus retrieves the status of a DAG run.
// This is used by parent DAGs to poll the status of remote sub-DAGs in shared-nothing mode.
func (h *Handler) GetDAGRunStatus(ctx context.Context, req *coordinatorv1.GetDAGRunStatusRequest) (*coordinatorv1.GetDAGRunStatusResponse, error) {
	if h.dagRunStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "DAG run status query not configured: dagRunStore is nil")
	}

	if req.DagName == "" || req.DagRunId == "" {
		return nil, status.Error(codes.InvalidArgument, "dag_name and dag_run_id are required")
	}

	var attempt exec.DAGRunAttempt
	var err error

	// Always read the latest attempt from disk rather than using the openAttempts
	// cache. In shared-storage mode, the worker creates a separate attempt on disk
	// that the cache doesn't know about. Reading from disk via FindSubAttempt/
	// FindAttempt calls LatestAttempt which returns the newest attempt.
	// In shared-nothing mode, only the coordinator's attempt exists on disk and
	// ReportStatus writes to it (synced), so reading from disk is also correct.
	if req.RootDagRunName != "" && req.RootDagRunId != "" {
		// Look up as a sub-DAG
		rootRef := exec.DAGRunRef{Name: req.RootDagRunName, ID: req.RootDagRunId}
		attempt, err = h.dagRunStore.FindSubAttempt(ctx, rootRef, req.DagRunId)
	} else {
		// Look up as a top-level DAG run
		ref := exec.DAGRunRef{Name: req.DagName, ID: req.DagRunId}
		attempt, err = h.dagRunStore.FindAttempt(ctx, ref)
	}

	if err != nil {
		// Not found is not an error, just return found=false
		return &coordinatorv1.GetDAGRunStatusResponse{
			Found: false,
		}, nil
	}

	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return &coordinatorv1.GetDAGRunStatusResponse{
			Found: false,
			Error: fmt.Sprintf("failed to read status: %v", err),
		}, nil
	}

	protoStatus, convErr := convert.DAGRunStatusToProto(runStatus)
	if convErr != nil {
		return &coordinatorv1.GetDAGRunStatusResponse{
			Found: false,
			Error: fmt.Sprintf("failed to convert status: %v", convErr),
		}, nil
	}

	return &coordinatorv1.GetDAGRunStatusResponse{
		Found:  true,
		Status: protoStatus,
	}, nil
}

// StartZombieDetector starts a background goroutine that periodically checks for zombie runs.
// It detects workers that have stopped sending heartbeats and marks their running tasks as failed.
// The interval parameter controls how often the detector runs (recommended: 45 seconds).
// Call WaitZombieDetector after canceling the context to ensure clean shutdown.
// This method is safe to call multiple times; subsequent calls are no-ops.
func (h *Handler) StartZombieDetector(ctx context.Context, interval time.Duration) {
	h.zombieDetectorMu.Lock()
	defer h.zombieDetectorMu.Unlock()

	if h.zombieDetectorStarted {
		return // Already started
	}
	h.zombieDetectorStarted = true
	h.zombieDetectorDone = make(chan struct{})

	go func() {
		defer close(h.zombieDetectorDone)
		h.detectAndCleanupZombies(ctx)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.detectAndCleanupZombies(ctx)
			}
		}
	}()
}

// WaitZombieDetector waits for the zombie detector goroutine to finish.
// This should be called after the context passed to StartZombieDetector is canceled.
func (h *Handler) WaitZombieDetector() {
	h.zombieDetectorMu.Lock()
	done := h.zombieDetectorDone
	h.zombieDetectorMu.Unlock()

	if done != nil {
		<-done
	}
}

// detectAndCleanupZombies checks for stale workers and marks their tasks as failed.
func (h *Handler) detectAndCleanupZombies(ctx context.Context) {
	// Pass 1: clean up stale worker presence records used for discovery.
	if h.workerHeartbeatStore != nil {
		_, _ = h.workerHeartbeatStore.DeleteStale(ctx, time.Now().Add(-h.staleHeartbeatThreshold))
	} else {
		for _, info := range h.collectAndRemoveStaleHeartbeats() {
			h.markWorkerTasksFailed(ctx, info)
		}
	}

	// Pass 2: lease-based detection — catches distributed runs whose workers
	// stopped reporting owner-bound run heartbeats, including after coordinator
	// restarts or owner coordinator loss.
	h.detectStaleLeases(ctx)
}

// detectStaleLeases reconciles durable distributed-run leases and marks stale
// attempts as failed. When the active-run index is available, recovery is
// bounded by the number of active distributed attempts instead of historical
// DAG-run status files.
func (h *Handler) detectStaleLeases(ctx context.Context) {
	if h.dagRunStore == nil {
		return
	}
	if h.dagRunLeaseStore == nil {
		activeStatuses := []core.Status{core.Running}
		statuses, err := h.dagRunStore.ListStatuses(ctx,
			exec.WithStatuses(activeStatuses),
			exec.WithoutLimit(),
		)
		if err != nil {
			logger.Error(ctx, "Failed to list active statuses for lease check", tag.Error(err))
			return
		}
		for _, st := range statuses {
			if st.WorkerID == "" || st.LeaseAt == 0 {
				continue
			}
			if !exec.IsLeaseActive(st, h.staleLeaseThreshold) {
				reason := fmt.Sprintf("lease expired: worker %s stopped reporting status", st.WorkerID)
				h.markRunFailed(ctx, st.Name, st.DAGRunID, reason)
			}
		}
		return
	}

	now := time.Now().UTC()
	h.detectLeasedDistributedRuns(ctx, now)

	if h.activeDistributedRunStore != nil {
		h.detectIndexedDistributedStatuses(ctx, now)
		return
	}

	h.detectOrphanedDistributedStatuses(ctx, now)
}

func (h *Handler) detectLeasedDistributedRuns(ctx context.Context, now time.Time) {
	leases, err := h.dagRunLeaseStore.ListAll(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list active distributed leases", tag.Error(err))
		return
	}

	for _, lease := range leases {
		h.reconcileDistributedLease(ctx, lease, now)
	}
}

func (h *Handler) reconcileDistributedLease(ctx context.Context, lease exec.DAGRunLease, now time.Time) {
	if lease.AttemptKey == "" {
		logger.Warn(ctx, "Skipping distributed lease reconciliation due to missing attempt key",
			tag.DAG(lease.DAGRun.Name),
			tag.RunID(lease.DAGRun.ID),
		)
		return
	}

	attempt, runStatus, err := h.resolveLatestAttempt(ctx, lease.DAGRun.Name, lease.DAGRun.ID, lease.Root)
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrDAGRunIDNotFound),
		errors.Is(err, exec.ErrNoStatusData),
		errors.Is(err, exec.ErrCorruptedStatusFile):
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete distributed lease for missing leased run",
			"Failed to delete active distributed run for missing leased run",
		)
		return
	default:
		logger.Error(ctx, "Failed to resolve leased distributed run",
			tag.DAG(lease.DAGRun.Name),
			tag.RunID(lease.DAGRun.ID),
			tag.AttemptKey(lease.AttemptKey),
			tag.Error(err),
		)
		return
	}

	attemptID := lease.AttemptID
	if attemptID == "" && runStatus != nil {
		attemptID = runStatus.AttemptID
	}
	if attemptID == "" && attempt != nil {
		attemptID = attempt.ID()
	}

	if runStatus == nil {
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete distributed lease for empty leased status",
			"Failed to delete active distributed run for empty leased status",
		)
		return
	}

	workerID, ok := distributedWorkerIDForStatus(runStatus, lease.WorkerID)
	if !ok || !exec.LeaseIdentityMatchesStatus(&lease, runStatus, attemptID) {
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete superseded distributed lease",
			"Failed to delete superseded active distributed run",
		)
		return
	}

	switch runStatus.Status {
	case core.Running, core.NotStarted, core.Queued:
		if lease.IsFresh(now, h.staleLeaseThreshold) {
			h.upsertActiveDistributedRun(ctx, runStatus, workerID, attemptID)
			return
		}
	case core.Failed, core.Aborted, core.Succeeded, core.PartiallySucceeded, core.Waiting, core.Rejected:
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete inactive distributed lease",
			"Failed to delete inactive active distributed run",
		)
		return
	default:
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete unknown-state distributed lease",
			"Failed to delete unknown-state active distributed run",
		)
		return
	}

	if h.workerHeartbeatStore == nil {
		h.markStatusLeaseRunFailed(ctx, runStatus, attemptID, lease.AttemptKey, staleDistributedLeaseReason(workerID))
		return
	}

	reconciledStatus, repaired, err := h.confirmAndRepairStaleDistributedRun(ctx, runStatus, attemptID, workerID)
	if err != nil {
		logger.Error(ctx, "Failed to confirm stale distributed run from lease reconciliation",
			tag.DAG(lease.DAGRun.Name),
			tag.RunID(lease.DAGRun.ID),
			tag.AttemptKey(lease.AttemptKey),
			tag.Error(err),
		)
		return
	}
	if repaired {
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete stale distributed lease after confirmed failure",
			"Failed to delete active distributed run after confirmed failure",
		)
		logger.Warn(ctx, "Marked stale distributed run as FAILED",
			tag.DAG(lease.DAGRun.Name),
			tag.RunID(lease.DAGRun.ID),
			slog.String("reason", staleDistributedLeaseReason(workerID)),
		)
		return
	}
	if reconciledStatus == nil {
		return
	}
	if reconciledStatus.AttemptID != attemptID || (!reconciledStatus.Status.IsActive() && reconciledStatus.Status != core.NotStarted) {
		h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete superseded distributed lease after reconciliation",
			"Failed to delete superseded active distributed run after reconciliation",
		)
		return
	}
	if reconciledWorkerID, ok := distributedWorkerIDForStatus(reconciledStatus, workerID); ok {
		h.restoreConfirmedDistributedRunTrackingFromStatus(ctx, reconciledWorkerID, reconciledStatus, attemptID)
	}
}

func staleDistributedLeaseReason(workerID string) string {
	return exec.DistributedLeaseExpiredReason(workerID)
}

func (h *Handler) confirmAndRepairStaleDistributedRun(
	ctx context.Context,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
	fallbackWorkerID string,
) (*exec.DAGRunStatus, bool, error) {
	return runtime.ConfirmAndRepairStaleDistributedRun(ctx, runtime.DistributedRunRepairConfig{
		DAGRunStore:                   h.dagRunStore,
		DAGRunLeaseStore:              h.dagRunLeaseStore,
		WorkerHeartbeatStore:          h.workerHeartbeatStore,
		StaleLeaseThreshold:           h.staleLeaseThreshold,
		StaleWorkerHeartbeatThreshold: h.staleHeartbeatThreshold,
	}, status, fallbackAttemptID, fallbackWorkerID)
}

func (h *Handler) detectOrphanedDistributedStatuses(ctx context.Context, now time.Time) {
	statuses, err := h.dagRunStore.ListStatuses(ctx,
		exec.WithStatuses([]core.Status{core.Running, core.NotStarted}),
		exec.WithoutLimit(),
	)
	if err != nil {
		logger.Error(ctx, "Failed to list distributed statuses for orphaned lease check", tag.Error(err))
		return
	}

	for _, status := range statuses {
		if status == nil || !exec.IsRemoteWorkerID(status.WorkerID) {
			continue
		}

		leaseState, ok := h.loadDistributedLeaseForStatus(ctx, status)
		if !ok {
			continue
		}

		if exec.LeaseMatchesStatus(leaseState.lease, status, leaseState.attemptID, now, h.staleLeaseThreshold) {
			continue
		}

		if h.workerHeartbeatStore == nil {
			h.markStatusLeaseRunFailed(ctx, status, leaseState.attemptID, leaseState.attemptKey, staleDistributedLeaseReason(status.WorkerID))
			continue
		}

		reconciledStatus, repaired, err := h.confirmAndRepairStaleDistributedRun(ctx, status, leaseState.attemptID, status.WorkerID)
		if err != nil {
			logger.Error(ctx, "Failed to confirm stale orphaned distributed run",
				tag.DAG(status.Name),
				tag.RunID(status.DAGRunID),
				tag.AttemptKey(leaseState.attemptKey),
				tag.Error(err),
			)
			continue
		}
		if repaired {
			h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), status.DAGRun(), leaseState.attemptKey,
				"Failed to delete orphaned distributed lease after confirmed failure",
				"Failed to delete orphaned active distributed run after confirmed failure",
			)
			continue
		}
		if reconciledStatus == nil {
			continue
		}
		if reconciledStatus.AttemptID != leaseState.attemptID || (!reconciledStatus.Status.IsActive() && reconciledStatus.Status != core.NotStarted) {
			h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), status.DAGRun(), leaseState.attemptKey,
				"Failed to delete superseded orphaned distributed lease after reconciliation",
				"Failed to delete superseded orphaned active distributed run after reconciliation",
			)
			continue
		}
		if reconciledWorkerID, ok := distributedWorkerIDForStatus(reconciledStatus, status.WorkerID); ok {
			h.restoreConfirmedDistributedRunTrackingFromStatus(ctx, reconciledWorkerID, reconciledStatus, leaseState.attemptID)
		}

	}
}

func (h *Handler) detectIndexedDistributedStatuses(ctx context.Context, now time.Time) {
	records, err := h.activeDistributedRunStore.ListAll(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list active distributed runs", tag.Error(err))
		return
	}

	for _, record := range records {
		if record.AttemptKey == "" {
			continue
		}

		_, runStatus, err := h.resolveLatestAttempt(ctx, record.DAGRun.Name, record.DAGRun.ID, record.Root)
		switch {
		case err == nil:
		case errors.Is(err, exec.ErrDAGRunIDNotFound),
			errors.Is(err, exec.ErrNoStatusData),
			errors.Is(err, exec.ErrCorruptedStatusFile):
			h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete distributed lease for missing indexed run",
				"Failed to delete active distributed run for missing indexed run",
			)
			continue
		default:
			logger.Error(ctx, "Failed to resolve indexed distributed run",
				tag.DAG(record.DAGRun.Name),
				tag.RunID(record.DAGRun.ID),
				tag.AttemptKey(record.AttemptKey),
				tag.Error(err),
			)
			continue
		}

		workerID, ok := distributedWorkerIDForStatus(runStatus, record.WorkerID)
		if !ok || !h.indexedDistributedRunMatchesStatus(record, runStatus) {
			h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete superseded distributed lease from active index",
				"Failed to delete superseded active distributed run",
			)
			continue
		}

		lease, err := h.dagRunLeaseStore.Get(ctx, record.AttemptKey)
		switch {
		case err == nil:
		case errors.Is(err, exec.ErrDAGRunLeaseNotFound):
			lease = nil
		default:
			logger.Error(ctx, "Failed to read distributed lease for indexed run",
				tag.AttemptKey(record.AttemptKey),
				tag.Error(err),
			)
			continue
		}

		if exec.LeaseMatchesStatus(lease, runStatus, record.AttemptID, now, h.staleLeaseThreshold) {
			h.upsertActiveDistributedRun(ctx, runStatus, workerID, record.AttemptID)
			continue
		}

		if h.workerHeartbeatStore == nil {
			h.markStatusLeaseRunFailed(ctx, runStatus, record.AttemptID, record.AttemptKey, staleDistributedLeaseReason(workerID))
			continue
		}

		reconciledStatus, repaired, err := h.confirmAndRepairStaleDistributedRun(ctx, runStatus, record.AttemptID, workerID)
		if err != nil {
			logger.Error(ctx, "Failed to confirm stale indexed distributed run",
				tag.DAG(record.DAGRun.Name),
				tag.RunID(record.DAGRun.ID),
				tag.AttemptKey(record.AttemptKey),
				tag.Error(err),
			)
			continue
		}
		if repaired {
			h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete stale indexed distributed lease after confirmed failure",
				"Failed to delete stale indexed active distributed run after confirmed failure",
			)
			continue
		}
		if reconciledStatus == nil {
			continue
		}
		if reconciledStatus.AttemptID != record.AttemptID || (!reconciledStatus.Status.IsActive() && reconciledStatus.Status != core.NotStarted) {
			h.deleteDistributedTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete superseded indexed distributed lease after reconciliation",
				"Failed to delete superseded indexed active distributed run after reconciliation",
			)
			continue
		}
		if reconciledWorkerID, ok := distributedWorkerIDForStatus(reconciledStatus, workerID); ok {
			h.restoreConfirmedDistributedRunTrackingFromStatus(ctx, reconciledWorkerID, reconciledStatus, record.AttemptID)
			continue
		}

	}
}

func (h *Handler) loadDistributedLeaseForStatus(
	ctx context.Context,
	runStatus *exec.DAGRunStatus,
) (*distributedLeaseState, bool) {
	attemptID, err := h.resolveAttemptIDForStatus(ctx, runStatus)
	if err != nil {
		logger.Error(ctx, "Failed to resolve distributed attempt for lease check",
			tag.DAG(runStatus.Name),
			tag.RunID(runStatus.DAGRunID),
			tag.Error(err),
		)
		return nil, false
	}

	attemptKey := exec.AttemptKeyForStatus(runStatus, attemptID)
	if attemptKey == "" {
		logger.Warn(ctx, "Skipping distributed lease check due to missing attempt key",
			tag.DAG(runStatus.Name),
			tag.RunID(runStatus.DAGRunID),
			tag.AttemptID(attemptID),
		)
		return nil, false
	}

	lease, err := h.dagRunLeaseStore.Get(ctx, attemptKey)
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrDAGRunLeaseNotFound):
		lease = nil
	default:
		logger.Error(ctx, "Failed to read distributed lease",
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
		return nil, false
	}

	return &distributedLeaseState{
		attemptID:  attemptID,
		attemptKey: attemptKey,
		lease:      lease,
	}, true
}

func (h *Handler) upsertActiveDistributedRun(
	ctx context.Context,
	runStatus *exec.DAGRunStatus,
	workerID string,
	fallbackAttemptID string,
) {
	if h.activeDistributedRunStore == nil || runStatus == nil {
		return
	}

	attemptKey := exec.AttemptKeyForStatus(runStatus, fallbackAttemptID)
	if attemptKey == "" {
		return
	}

	attemptID := runStatus.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if workerID == "" {
		workerID = runStatus.WorkerID
	}
	if !exec.IsRemoteWorkerID(workerID) {
		return
	}

	record := exec.ActiveDistributedRun{
		AttemptKey: attemptKey,
		DAGRun:     runStatus.DAGRun(),
		Root:       runStatus.Root,
		AttemptID:  attemptID,
		WorkerID:   workerID,
		Status:     runStatus.Status,
		UpdatedAt:  time.Now().UTC().UnixMilli(),
	}
	if err := h.activeDistributedRunStore.Upsert(ctx, record); err != nil {
		logger.Warn(ctx, "Failed to upsert active distributed run",
			tag.RunID(runStatus.DAGRunID),
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
	}
}

func (h *Handler) upsertActiveDistributedRunFromTask(
	ctx context.Context,
	task *coordinatorv1.Task,
	workerID string,
	now time.Time,
) {
	if h.activeDistributedRunStore == nil || task == nil || task.AttemptKey == "" {
		return
	}
	if !exec.IsRemoteWorkerID(workerID) {
		return
	}

	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.Zero() {
		root = exec.DAGRunRef{Name: task.Target, ID: task.DagRunId}
	}

	record := exec.ActiveDistributedRun{
		AttemptKey: task.AttemptKey,
		DAGRun: exec.DAGRunRef{
			Name: task.Target,
			ID:   task.DagRunId,
		},
		Root:      root,
		AttemptID: task.AttemptId,
		WorkerID:  workerID,
		Status:    core.Queued,
		UpdatedAt: now.UnixMilli(),
	}
	if err := h.activeDistributedRunStore.Upsert(ctx, record); err != nil {
		logger.Warn(ctx, "Failed to upsert active distributed run from task claim",
			tag.RunID(task.DagRunId),
			tag.AttemptKey(task.AttemptKey),
			tag.Error(err),
		)
	}
}

func (h *Handler) indexedDistributedRunMatchesStatus(
	record exec.ActiveDistributedRun,
	runStatus *exec.DAGRunStatus,
) bool {
	if _, ok := distributedWorkerIDForStatus(runStatus, record.WorkerID); !ok {
		return false
	}
	if runStatus.Status != core.Running &&
		runStatus.Status != core.NotStarted &&
		runStatus.Status != core.Queued {
		return false
	}

	attemptKey := exec.AttemptKeyForStatus(runStatus, record.AttemptID)
	if attemptKey == "" || attemptKey != record.AttemptKey {
		return false
	}
	if record.AttemptID != "" {
		attemptID := runStatus.AttemptID
		if attemptID == "" {
			attemptID = record.AttemptID
		}
		if attemptID != record.AttemptID {
			return false
		}
	}
	return true
}

func distributedWorkerIDForStatus(status *exec.DAGRunStatus, fallbackWorkerID string) (string, bool) {
	if status == nil {
		return "", false
	}
	if exec.IsRemoteWorkerID(status.WorkerID) {
		return status.WorkerID, true
	}
	if status.WorkerID != "" {
		return "", false
	}
	if status.Status != core.Queued && status.Status != core.NotStarted {
		return "", false
	}
	if !exec.IsRemoteWorkerID(fallbackWorkerID) {
		return "", false
	}
	return fallbackWorkerID, true
}

func (h *Handler) markStatusLeaseRunFailed(
	ctx context.Context,
	status *exec.DAGRunStatus,
	attemptID string,
	attemptKey string,
	reason string,
) {
	if status == nil {
		return
	}
	h.failDistributedAttemptIfCurrent(
		ctx,
		status.DAGRun(),
		attemptID,
		attemptKey,
		reason,
		status.Status,
	)
}

func (h *Handler) resolveAttemptIDForStatus(ctx context.Context, status *exec.DAGRunStatus) (string, error) {
	if status == nil {
		return "", nil
	}
	if status.AttemptID != "" {
		return status.AttemptID, nil
	}

	storeCtx := context.WithoutCancel(ctx)
	if !status.Root.Zero() {
		attempt, err := h.dagRunStore.FindSubAttempt(storeCtx, status.Root, status.DAGRunID)
		if err != nil {
			return "", err
		}
		return attempt.ID(), nil
	}

	attempt, err := h.dagRunStore.FindAttempt(storeCtx, status.DAGRun())
	if err != nil {
		return "", err
	}
	return attempt.ID(), nil
}

func (h *Handler) failDistributedAttemptIfCurrent(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	attemptID string,
	attemptKey string,
	reason string,
	expectedStatuses ...core.Status,
) {
	storeCtx := context.WithoutCancel(ctx)
	if attemptID == "" {
		logger.Error(ctx, "Skipping distributed stale-run repair due to missing attempt ID",
			tag.DAG(dagRun.Name),
			tag.RunID(dagRun.ID),
		)
		return
	}

	mutate := func(status *exec.DAGRunStatus) error {
		finishedAt := time.Now()
		finishedAtStr := stringutil.FormatTime(finishedAt)
		status.Status = core.Failed
		status.FinishedAt = finishedAtStr
		status.Error = reason
		for i, node := range status.Nodes {
			if node == nil {
				continue
			}
			switch node.Status {
			case core.NodeRunning, core.NodeNotStarted, core.NodeRetrying, core.NodeWaiting:
				status.Nodes[i].Status = core.NodeFailed
				status.Nodes[i].FinishedAt = finishedAtStr
				status.Nodes[i].Error = reason
			case core.NodeFailed, core.NodeAborted, core.NodeSucceeded, core.NodeSkipped, core.NodePartiallySucceeded, core.NodeRejected:
				// Keep terminal node results intact when the run is failed due to lease loss.
			}
		}
		return nil
	}

	var (
		status  *exec.DAGRunStatus
		swapped bool
		err     error
	)

	for _, expectedStatus := range expectedStatuses {
		status, swapped, err = h.dagRunStore.CompareAndSwapLatestAttemptStatus(
			storeCtx,
			dagRun,
			attemptID,
			expectedStatus,
			mutate,
		)
		if err != nil {
			logger.Error(ctx, "Failed to fail stale distributed run",
				tag.RunID(dagRun.ID),
				slog.String("expected_status", expectedStatus.String()),
				tag.Error(err),
			)
			return
		}
		if swapped || status == nil || status.AttemptID != attemptID || status.Status == expectedStatus {
			break
		}
	}

	if status == nil {
		h.deleteDistributedTracking(ctx, storeCtx, dagRun, attemptKey,
			"Failed to delete orphaned distributed lease",
			"Failed to delete orphaned active distributed run",
		)
		return
	}
	if status.AttemptID != attemptID || !status.Status.IsActive() && status.Status != core.NotStarted {
		h.deleteDistributedTracking(ctx, storeCtx, dagRun, attemptKey,
			"Failed to delete superseded distributed lease",
			"Failed to delete superseded active distributed run",
		)
		return
	}
	if !swapped {
		return
	}

	h.deleteDistributedTracking(ctx, storeCtx, dagRun, attemptKey,
		"Failed to delete stale distributed lease after failure",
		"Failed to delete active distributed run after failure",
	)

	logger.Warn(ctx, "Marked stale distributed run as FAILED",
		tag.DAG(dagRun.Name),
		tag.RunID(dagRun.ID),
		slog.String("reason", reason),
	)
}

func (h *Handler) deleteDistributedLease(
	ctx context.Context,
	storeCtx context.Context,
	dagRun exec.DAGRunRef,
	attemptKey string,
	message string,
) {
	if h.dagRunLeaseStore == nil || attemptKey == "" {
		return
	}
	if err := h.dagRunLeaseStore.Delete(storeCtx, attemptKey); err != nil &&
		!errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.Error(err),
		)
	}
}

func (h *Handler) deleteActiveDistributedRun(
	ctx context.Context,
	storeCtx context.Context,
	dagRun exec.DAGRunRef,
	attemptKey string,
	message string,
) {
	if h.activeDistributedRunStore == nil || attemptKey == "" {
		return
	}
	if err := h.activeDistributedRunStore.Delete(storeCtx, attemptKey); err != nil &&
		!errors.Is(err, exec.ErrActiveRunNotFound) {
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
	}
}

func (h *Handler) deleteDistributedTracking(
	ctx context.Context,
	storeCtx context.Context,
	dagRun exec.DAGRunRef,
	attemptKey string,
	leaseMessage string,
	activeRunMessage string,
) {
	h.deleteDistributedLease(ctx, storeCtx, dagRun, attemptKey, leaseMessage)
	h.deleteActiveDistributedRun(ctx, storeCtx, dagRun, attemptKey, activeRunMessage)
}

// markRunFailed is kept for compatibility with older tests and non-lease based
// cleanup paths. It marks the latest active attempt failed without requiring a
// lease record.
func (h *Handler) markRunFailed(ctx context.Context, dagName, dagRunID, reason string) {
	if h.dagRunStore == nil {
		return
	}
	storeCtx := context.WithoutCancel(ctx)

	runMu := h.getRunMutex(dagRunID)
	runMu.Lock()
	defer runMu.Unlock()

	var attempt exec.DAGRunAttempt
	var needsOpen bool

	h.attemptsMu.RLock()
	cachedAttempt, ok := h.openAttempts[dagRunID]
	h.attemptsMu.RUnlock()

	if ok {
		attempt = cachedAttempt
		needsOpen = false
	} else {
		ref := exec.DAGRunRef{Name: dagName, ID: dagRunID}
		foundAttempt, err := h.dagRunStore.FindAttempt(storeCtx, ref)
		if err != nil {
			logger.Error(ctx, "Failed to find attempt for zombie cleanup",
				tag.DAG(dagName), tag.RunID(dagRunID), tag.Error(err))
			return
		}
		attempt = foundAttempt
		needsOpen = true
	}

	dagRunStatus, err := attempt.ReadStatus(storeCtx)
	if err != nil {
		logger.Error(ctx, "Failed to read status for zombie cleanup",
			tag.DAG(dagName), tag.RunID(dagRunID), tag.Error(err))
		return
	}

	if !dagRunStatus.Status.IsActive() && dagRunStatus.Status != core.NotStarted {
		return
	}

	finishedAt := stringutil.FormatTime(time.Now())
	dagRunStatus.Status = core.Failed
	dagRunStatus.FinishedAt = finishedAt
	dagRunStatus.Error = reason

	for i, node := range dagRunStatus.Nodes {
		if node.Status == core.NodeRunning || node.Status == core.NodeNotStarted || node.Status == core.NodeWaiting || node.Status == core.NodeRetrying {
			dagRunStatus.Nodes[i].Status = core.NodeFailed
			dagRunStatus.Nodes[i].FinishedAt = finishedAt
			dagRunStatus.Nodes[i].Error = reason
		}
	}

	if needsOpen {
		if err := attempt.Open(storeCtx); err != nil {
			logger.Error(ctx, "Failed to open attempt for zombie cleanup",
				tag.DAG(dagName), tag.RunID(dagRunID), tag.Error(err))
			return
		}
		defer func() {
			if err := attempt.Close(storeCtx); err != nil {
				logger.Warn(ctx, "Failed to close attempt in markRunFailed",
					tag.DAG(dagName), tag.RunID(dagRunID), tag.Error(err))
			}
		}()
	}

	storeCtx = h.eventContext(storeCtx)
	if err := attempt.Write(storeCtx, *dagRunStatus); err != nil {
		logger.Error(ctx, "Failed to write failed status for zombie cleanup",
			tag.DAG(dagName), tag.RunID(dagRunID), tag.Error(err))
		return
	}

	logger.Warn(ctx, "Marked zombie run as FAILED",
		tag.DAG(dagName), tag.RunID(dagRunID), slog.String("reason", reason))
}

// markWorkerTasksFailed is kept for compatibility with tests that exercise the
// worker-heartbeat cleanup path directly.
func (h *Handler) markWorkerTasksFailed(ctx context.Context, info *heartbeatInfo) {
	if h.dagRunStore == nil || info == nil || info.stats == nil {
		return
	}
	for _, task := range info.stats.RunningTasks {
		if task == nil {
			continue
		}
		h.markRunFailed(ctx, task.DagName, task.DagRunId, fmt.Sprintf("worker %s became unresponsive", info.workerID))
	}
}

// RequestCancel handles requests to cancel a DAG run.
// This is used in shared-nothing mode for sub-DAG cancellation where the parent
// worker cannot directly access the sub-DAG's attempt.
func (h *Handler) RequestCancel(ctx context.Context, req *coordinatorv1.RequestCancelRequest) (*coordinatorv1.RequestCancelResponse, error) {
	if h.dagRunStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "cancellation not available: DAG run storage not configured")
	}
	if req.DagName == "" {
		return nil, status.Error(codes.InvalidArgument, "dag_name is required")
	}
	if req.DagRunId == "" {
		return nil, status.Error(codes.InvalidArgument, "dag_run_id is required")
	}

	ctx = logger.WithValues(ctx,
		tag.DAG(req.DagName),
		tag.RunID(req.DagRunId),
	)

	// Find the attempt (either root or sub-DAG)
	var attempt exec.DAGRunAttempt
	var err error

	isSubDAG := req.RootDagRunId != "" && req.RootDagRunId != req.DagRunId
	if isSubDAG {
		rootRef := exec.DAGRunRef{Name: req.RootDagRunName, ID: req.RootDagRunId}
		attempt, err = h.dagRunStore.FindSubAttempt(ctx, rootRef, req.DagRunId)
		logger.Info(ctx, "Looking up sub-DAG attempt for cancellation",
			slog.String("root-dag-run-id", req.RootDagRunId),
		)
	} else {
		ref := exec.DAGRunRef{Name: req.DagName, ID: req.DagRunId}
		attempt, err = h.dagRunStore.FindAttempt(ctx, ref)
		logger.Info(ctx, "Looking up DAG attempt for cancellation")
	}

	if err != nil {
		logger.Warn(ctx, "Failed to find DAG run for cancellation", tag.Error(err))
		return &coordinatorv1.RequestCancelResponse{
			Accepted: false,
			Error:    fmt.Sprintf("failed to find DAG run: %v", err),
		}, nil
	}

	// Set the abort flag
	if err := attempt.Abort(ctx); err != nil {
		logger.Warn(ctx, "Failed to abort DAG run", tag.Error(err))
		return &coordinatorv1.RequestCancelResponse{
			Accepted: false,
			Error:    fmt.Sprintf("failed to abort: %v", err),
		}, nil
	}

	if err := finalizeNotStartedCancellation(ctx, attempt); err != nil {
		logger.Warn(ctx, "Failed to finalize pending cancelled DAG run", tag.Error(err))
		return &coordinatorv1.RequestCancelResponse{
			Accepted: false,
			Error:    fmt.Sprintf("failed to finalize cancellation: %v", err),
		}, nil
	}

	logger.Info(ctx, "DAG run cancellation requested successfully")
	return &coordinatorv1.RequestCancelResponse{Accepted: true}, nil
}

func finalizeNotStartedCancellation(ctx context.Context, attempt exec.DAGRunAttempt) error {
	if attempt == nil {
		return nil
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read attempt status: %w", err)
	}
	if status == nil || status.Status != core.NotStarted {
		return nil
	}

	finishedAt := stringutil.FormatTime(time.Now().UTC())
	status.Status = core.Aborted
	status.FinishedAt = finishedAt
	status.Error = context.Canceled.Error()
	status.WorkerID = ""
	status.PID = 0
	status.LeaseAt = 0

	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("open attempt for cancellation finalization: %w", err)
	}
	defer func() { _ = attempt.Close(ctx) }()

	if err := attempt.Write(ctx, *status); err != nil {
		return fmt.Errorf("write cancelled attempt status: %w", err)
	}

	return nil
}
