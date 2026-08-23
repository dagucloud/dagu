// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/spec"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
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
const defaultStaleHeartbeatThreshold = dispatch.DefaultStaleWorkerHeartbeatThreshold

// defaultStaleLeaseThreshold is the shared default duration after which a
// distributed run's lease is considered stale.
const defaultStaleLeaseThreshold = dagrun.DefaultStaleLeaseThreshold

const (
	defaultDispatchPollInitialWait = 250 * time.Millisecond
	defaultDispatchPollMaxWait     = time.Second
)

// defaultLeaseRefreshWriteInterval is the maximum interval between persisted
// heartbeat-driven lease refreshes for a running distributed task.
const defaultLeaseRefreshWriteInterval = 5 * time.Second

// runHeartbeatRepairTimeout bounds stale-failure repair work that should
// survive caller cancellation after a lease heartbeat has already succeeded.
const runHeartbeatRepairTimeout = 5 * time.Second

const (
	remoteAttemptRejectedLeaseInactive = "stale attempt: lease no longer active"
	remoteAttemptRejectedSuperseded    = "stale attempt: superseded by newer attempt"
	remoteAttemptRejectedTerminal      = "stale attempt: run already terminal"
	remoteAttemptRejectedManualAction  = "stale attempt: completed manual action changed"
)

var (
	errNoAvailableWorkers           = errors.New("no available workers")
	errNoMatchingWorkers            = errors.New("no workers match the required selector")
	errRunHeartbeatRepairSkipped    = errors.New("run heartbeat repair skipped")
	errManualActionCheckpointChange = errors.New("completed manual action checkpoint changed")
)

type preparedDispatchAttempt struct {
	attempt      dagrun.Attempt
	newlyCreated bool
}

type runClaim struct {
	attemptID  string
	attemptKey string
	lease      *dispatch.DAGRunLease
}

type Handler struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer

	mu             sync.Mutex
	waitingPollers map[string]*workerInfo    // pollerID -> worker info
	heartbeats     map[string]*heartbeatInfo // workerID -> heartbeat info
	owner          dispatch.CoordinatorEndpoint

	dispatchWakeMu          sync.Mutex
	dispatchWakeCh          chan struct{}
	dispatchWakeGeneration  int64
	dispatchPollInitialWait time.Duration
	dispatchPollMaxWait     time.Duration

	// Optional worker runtime services.
	dagRunRepository          *persis.DAGRunRepository           // For status persistence
	logDir                    string                             // For log storage
	artifactDir               string                             // For artifact storage
	stateStore                dagrun.StateStore                  // For persistent DAG state shared across DAG runs
	workspaceBundleStore      *workspacebundle.Store             // For immutable task workspace bundles
	dispatchTaskStore         dispatch.DispatchTaskStore         // Shared distributed dispatch queue
	dispatchAdmissionStore    dispatch.DispatchAdmissionStore    // Shared distributed admission state
	workerHeartbeatStore      dispatch.WorkerHeartbeatStore      // Shared worker presence
	dagRunLeaseStore          dispatch.DAGRunLeaseStore          // Shared distributed run leases
	activeDistributedRunStore dispatch.ActiveDistributedRunStore // Shared active distributed attempt index
	dagRepository             *persis.DAGRepository              // DAG definitions for the GetDAG RPC
	secretStore               secretpkg.Store                    // Secret registry for workers
	agentSessionCleanupQueue  *agentsession.CleanupQueue         // Deferred provider cleanup owned by workers

	// Open attempts cache for status persistence
	attemptsMu   sync.RWMutex
	openAttempts map[string]dagrun.Attempt // dagRunID -> open attempt

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
	// DAGRunRepository provides application access to persisted DAG-run statuses.
	// Required for worker status reporting.
	DAGRunRepository *persis.DAGRunRepository

	// LogDir is the directory for streamed worker log storage.
	// Required for worker log streaming.
	LogDir string

	// ArtifactDir is the directory for streamed worker artifact storage.
	// Required for worker artifact streaming.
	ArtifactDir string

	// StateStore is the persistent DAG state store used by state RPCs.
	StateStore dagrun.StateStore

	// WorkspaceBundleDir stores immutable task workspace bundles by digest.
	WorkspaceBundleDir string

	// Owner identifies this coordinator instance for shared task ownership.
	Owner dispatch.CoordinatorEndpoint

	// DispatchTaskStore is the shared store for distributed pending tasks.
	DispatchTaskStore dispatch.DispatchTaskStore

	// DispatchAdmissionStore reserves and binds distributed queue admission.
	DispatchAdmissionStore dispatch.DispatchAdmissionStore

	// WorkerHeartbeatStore is the shared store for worker presence.
	WorkerHeartbeatStore dispatch.WorkerHeartbeatStore

	// DAGRunLeaseStore is the shared store for active distributed attempt leases.
	DAGRunLeaseStore dispatch.DAGRunLeaseStore

	// ActiveDistributedRunStore is the shared store for the coordinator-owned
	// active distributed attempt index used by zombie detection.
	ActiveDistributedRunStore dispatch.ActiveDistributedRunStore

	// DAGRepository serves DAG definitions for the GetDAG RPC.
	// Optional - when nil, GetDAG returns Unimplemented.
	DAGRepository *persis.DAGRepository

	// SecretStore resolves Dagu-managed secret registry refs for workers.
	// Optional - when nil, ResolveSecretReference returns FailedPrecondition.
	SecretStore secretpkg.Store

	// AgentSessionCleanupQueue stores provider cleanup claimed by owning workers.
	AgentSessionCleanupQueue *agentsession.CleanupQueue

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
	var bundleStore *workspacebundle.Store
	if cfg.WorkspaceBundleDir != "" {
		bundleStore = workspacebundle.NewStore(cfg.WorkspaceBundleDir, workspacebundle.DefaultLimits())
	}
	dispatchAdmissionStore := cfg.DispatchAdmissionStore
	if dispatchAdmissionStore == nil {
		if admissionStore, ok := cfg.DispatchTaskStore.(dispatch.DispatchAdmissionStore); ok {
			dispatchAdmissionStore = admissionStore
		}
	}
	return &Handler{
		waitingPollers:            make(map[string]*workerInfo),
		heartbeats:                make(map[string]*heartbeatInfo),
		dispatchWakeCh:            make(chan struct{}),
		dispatchPollInitialWait:   defaultDispatchPollInitialWait,
		dispatchPollMaxWait:       defaultDispatchPollMaxWait,
		openAttempts:              make(map[string]dagrun.Attempt),
		runMutexes:                make(map[string]*sync.Mutex),
		owner:                     cfg.Owner,
		dagRunRepository:          cfg.DAGRunRepository,
		logDir:                    cfg.LogDir,
		artifactDir:               cfg.ArtifactDir,
		stateStore:                cfg.StateStore,
		workspaceBundleStore:      bundleStore,
		dispatchTaskStore:         cfg.DispatchTaskStore,
		dispatchAdmissionStore:    dispatchAdmissionStore,
		workerHeartbeatStore:      cfg.WorkerHeartbeatStore,
		dagRunLeaseStore:          cfg.DAGRunLeaseStore,
		activeDistributedRunStore: cfg.ActiveDistributedRunStore,
		dagRepository:             cfg.DAGRepository,
		secretStore:               cfg.SecretStore,
		agentSessionCleanupQueue:  cfg.AgentSessionCleanupQueue,
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

func (h *Handler) dispatchWakeSnapshot() (int64, <-chan struct{}) {
	h.dispatchWakeMu.Lock()
	defer h.dispatchWakeMu.Unlock()
	if h.dispatchWakeCh == nil {
		h.dispatchWakeCh = make(chan struct{})
	}
	return h.dispatchWakeGeneration, h.dispatchWakeCh
}

func (h *Handler) notifyDispatchAvailable() {
	h.dispatchWakeMu.Lock()
	defer h.dispatchWakeMu.Unlock()
	if h.dispatchWakeCh == nil {
		h.dispatchWakeCh = make(chan struct{})
	}
	h.dispatchWakeGeneration++
	close(h.dispatchWakeCh)
	h.dispatchWakeCh = make(chan struct{})
}

func (h *Handler) waitForDispatchPollRetry(ctx context.Context, pollerID string, observedGeneration int64, wait time.Duration) (bool, error) {
	generation, wakeCh := h.dispatchWakeSnapshot()
	if generation != observedGeneration {
		return true, nil
	}
	timer := time.NewTimer(jitterDispatchPollWait(wait, pollerID, generation))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-wakeCh:
		return true, nil
	case <-timer.C:
		return false, nil
	}
}

func jitterDispatchPollWait(wait time.Duration, pollerID string, generation int64) time.Duration {
	if wait <= 0 {
		return 0
	}
	spread := wait / 10
	if spread <= 0 {
		return wait
	}
	hash := int64(1469598103934665603)
	hash ^= generation
	for i := 0; i < len(pollerID); i++ {
		hash ^= int64(pollerID[i])
		hash *= 1099511628211
	}
	if hash < 0 {
		hash = ^hash
	}
	return wait + time.Duration(hash%(spread.Nanoseconds()+1))
}

func nextDispatchPollWait(current, maxWait time.Duration) time.Duration {
	if current <= 0 {
		return maxWait
	}
	next := current * 2
	if next <= 0 || next > maxWait {
		return maxWait
	}
	return next
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

	pollWait := h.dispatchPollInitialWait
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		observedGeneration, _ := h.dispatchWakeSnapshot()
		claimed, err := h.dispatchTaskStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
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
			claimed.Task.WorkerID = req.WorkerId
			task, err := convert.DispatchTaskToProto(claimed.Task)
			if err != nil {
				releaseCtx := context.WithoutCancel(ctx)
				if releaseErr := h.dispatchTaskStore.ReleaseClaim(releaseCtx, claimed.ClaimToken); releaseErr != nil && !errors.Is(releaseErr, dispatch.ErrDispatchTaskNotFound) {
					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to encode claimed task: %v; failed to release task claim: %v", err, releaseErr))
				}
				return nil, status.Error(codes.Internal, "failed to encode claimed task: "+err.Error())
			}
			return &coordinatorv1.PollResponse{Task: task}, nil
		}

		woke, err := h.waitForDispatchPollRetry(ctx, req.PollerId, observedGeneration, pollWait)
		if err != nil {
			return nil, err
		}
		if woke {
			pollWait = h.dispatchPollInitialWait
			continue
		}
		pollWait = nextDispatchPollWait(pollWait, h.dispatchPollMaxWait)
	}
}

// Dispatch tries to send a task to a waiting poller
// It fails if no pollers are available or no workers match the selector
func (h *Handler) Dispatch(ctx context.Context, req *coordinatorv1.DispatchRequest) (*coordinatorv1.DispatchResponse, error) {
	ctx = h.eventContext(ctx)
	if req.Task == nil {
		return nil, status.Error(codes.InvalidArgument, "task is required")
	}
	admissionToken := strings.TrimSpace(req.AdmissionReservationToken)

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
		if admissionToken != "" {
			return nil, status.Error(codes.FailedPrecondition, "admission reservation requires dispatch task storage")
		}
		if err := h.ensureWaitingWorkerAvailability(req.Task.WorkerSelector, req.Task.TargetWorkerId); err != nil {
			return nil, status.Error(dispatchErrorCode(err), err.Error())
		}

		var prepared *preparedDispatchAttempt
		if h.dagRunRepository != nil {
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
	if h.dagRunRepository == nil {
		return nil, status.Error(codes.FailedPrecondition, "distributed dispatch requires DAG run storage")
	}
	if admissionToken != "" && h.dispatchAdmissionStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "dispatch admission store is not configured")
	}

	healthyWorkers, err := h.listHealthyWorkers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list workers: "+err.Error())
	}
	if len(healthyWorkers) == 0 {
		return nil, status.Error(codes.Unavailable, errNoAvailableWorkers.Error())
	}
	if !anyWorkerMatches(healthyWorkers, req.Task.WorkerSelector, req.Task.TargetWorkerId) {
		return nil, status.Error(codes.FailedPrecondition, errNoMatchingWorkers.Error())
	}

	prepared, err := h.prepareAttemptForDispatch(ctx, req.Task)
	if err != nil {
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, status.Error(prepareAttemptErrorCode(err), "failed to prepare attempt: "+err.Error())
	}
	dispatchTask, err := convert.ProtoToDispatchTask(req.Task)
	if err != nil {
		h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, status.Error(codes.Internal, "failed to encode task: "+err.Error())
	}
	if err := h.enqueueOrBindDispatchTask(ctx, admissionToken, dispatchTask); err != nil {
		h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, status.Error(dispatchBindErrorCode(err), "failed to enqueue task: "+err.Error())
	}
	h.notifyDispatchAvailable()
	return &coordinatorv1.DispatchResponse{}, nil
}

func dispatchBindErrorCode(err error) codes.Code {
	if errors.Is(err, dispatch.ErrDispatchAdmissionNotFound) ||
		errors.Is(err, dispatch.ErrDispatchAdmissionConflict) {
		return codes.FailedPrecondition
	}
	return codes.Internal
}

func (h *Handler) enqueueOrBindDispatchTask(ctx context.Context, admissionToken string, task *dispatch.DispatchTask) error {
	if admissionToken == "" {
		return h.dispatchTaskStore.Enqueue(ctx, task)
	}
	return h.dispatchAdmissionStore.BindAdmission(ctx, dispatch.DispatchAdmissionBindRequest{
		ReservationToken: admissionToken,
		Task:             task,
	})
}

func (h *Handler) releaseAdmissionToken(ctx context.Context, token string) {
	if token == "" || h.dispatchAdmissionStore == nil {
		return
	}
	err := h.dispatchAdmissionStore.ReleaseAdmissionToken(context.WithoutCancel(ctx), token)
	if err == nil ||
		errors.Is(err, dispatch.ErrDispatchAdmissionConflict) ||
		errors.Is(err, dispatch.ErrDispatchAdmissionNotFound) {
		return
	}
	logger.Warn(ctx, "Failed to release dispatch admission reservation",
		tag.Error(err),
	)
}

func (h *Handler) finalizeAdmissionForStatus(ctx context.Context, status *ir.DAGRunStatus, attemptID string) {
	if h.dispatchAdmissionStore == nil || status == nil ||
		(status.Status != ir.Waiting && !isTerminalRunStatus(status.Status)) {
		return
	}
	attemptKey := dispatch.AttemptKeyForStatus(status, attemptID)
	if attemptKey == "" {
		return
	}
	if err := h.dispatchAdmissionStore.FinalizeAdmissionAttempt(context.WithoutCancel(ctx), attemptKey); err != nil {
		logger.Warn(ctx, "Failed to finalize dispatch admission",
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
	}
}

func (h *Handler) finalizeAdmissionForAttempt(ctx context.Context, attempt dagrun.Attempt) {
	if h.dispatchAdmissionStore == nil || attempt == nil {
		return
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to read status for dispatch admission finalization",
			tag.Error(err),
		)
		return
	}
	h.finalizeAdmissionForStatus(ctx, status, attempt.ID())
}

func queueDispatchStatusForTask(task *coordinatorv1.Task) (*ir.DAGRunStatus, error) {
	if task == nil || task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return nil, nil
	}

	status, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to decode previous task status: %w", err)
	}
	if status == nil || status.Status != ir.Queued {
		return nil, nil
	}
	return status, nil
}

func staleQueueDispatchError(reason string) error {
	return &queue.StaleQueueDispatchError{Reason: reason}
}

// createAttemptForTask creates a DAGRun attempt for a root-level task.
// This is called when the coordinator receives a dispatch for a root-level DAG run
// (not a sub-DAG), so it has a place to store status updates from the worker.
func (h *Handler) createAttemptForTask(ctx context.Context, task *coordinatorv1.Task) (*preparedDispatchAttempt, error) {
	if h.dagRunRepository == nil {
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
	labels := labelsForInitialStatus(task, dag)
	task.Labels = strings.Join(labels, ",")

	ref := ir.DAGRunRef{Name: dag.Name, ID: task.DagRunId}
	queueDispatchStatus, err := queueDispatchStatusForTask(task)
	if err != nil {
		return nil, err
	}

	// Check if dag-run already exists (e.g., queued via enqueue command)
	existingAttempt, findErr := h.dagRunRepository.FindAttempt(ctx, ref)
	if queueDispatchStatus != nil {
		if queueDispatchStatus.AttemptID == "" {
			return nil, staleQueueDispatchError("queued attempt ID is missing")
		}
		if findErr != nil {
			if errors.Is(findErr, dagrun.ErrDAGRunIDNotFound) || errors.Is(findErr, dagrun.ErrNoStatusData) || errors.Is(findErr, dagrun.ErrCorruptedStatusData) {
				return nil, staleQueueDispatchError("dag-run is no longer queued")
			}
			return nil, findErr
		}
	}
	if findErr != nil && !errors.Is(findErr, dagrun.ErrDAGRunIDNotFound) {
		return nil, fmt.Errorf("failed to find existing attempt: %w", findErr)
	}

	var existingStatus *ir.DAGRunStatus
	if findErr == nil {
		var readErr error
		existingStatus, readErr = existingAttempt.ReadStatus(ctx)
		if readErr != nil {
			if queueDispatchStatus != nil {
				if errors.Is(readErr, dagrun.ErrNoStatusData) || errors.Is(readErr, dagrun.ErrCorruptedStatusData) {
					return nil, staleQueueDispatchError("dag-run is no longer queued")
				}
				return nil, readErr
			}
			return nil, fmt.Errorf("failed to read existing attempt: %w", readErr)
		}
	}
	if queueDispatchStatus != nil {
		if existingAttempt.ID() != queueDispatchStatus.AttemptID {
			return nil, staleQueueDispatchError("queued attempt was superseded")
		}
		if existingStatus == nil || existingStatus.Status != ir.Queued {
			statusLabel := "unknown"
			if existingStatus != nil {
				statusLabel = existingStatus.Status.String()
			}
			return nil, staleQueueDispatchError("latest attempt is " + statusLabel)
		}
	}
	if existingStatus != nil && existingStatus.Status == ir.Queued {
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

	// Create new attempt (either first attempt or retry)
	isRetry := task.Operation == coordinatorv1.Operation_OPERATION_RETRY || findErr == nil
	opts := persis.DAGRunCreateAttemptOptions{Retry: isRetry}

	attempt, err := h.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), task.DagRunId, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create attempt: %w", err)
	}

	task.AttemptId = attempt.ID()
	task.AttemptKey = generateRootAttemptKey(task)

	if err := attempt.Open(ctx); err != nil {
		return nil, fmt.Errorf("failed to open attempt: %w", err)
	}

	if writeErr := h.writeInitialStatus(ctx, attempt, task, dag.Name, ir.DAGRunRef{}, labels); writeErr != nil {
		closeErr := attempt.Close(context.WithoutCancel(ctx))
		return nil, errors.Join(fmt.Errorf("failed to write initial status: %w", writeErr), closeErr)
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
	return ir.GenerateAttemptKey(task.Target, task.DagRunId, task.Target, task.DagRunId, task.AttemptId)
}

func (h *Handler) ensureTaskAttemptMetadata(task *coordinatorv1.Task) {
	if task == nil {
		return
	}
	if task.AttemptId == "" {
		task.AttemptId = uuid.NewString()
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

	task.AttemptKey = ir.GenerateAttemptKey(
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
	if h.dagRunRepository == nil {
		return nil, nil
	}

	rootRef := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}

	loadOpts := []spec.LoadOption{spec.WithName(task.Target)}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	dag, err := spec.LoadYAML(ctx, []byte(task.Definition), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DAG definition: %w", err)
	}
	dag.SourceFile = task.SourceFile
	labels := labelsForInitialStatus(task, dag)
	task.Labels = strings.Join(labels, ",")

	attempt, err := h.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), task.DagRunId, persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sub-attempt: %w", err)
	}

	task.AttemptId = attempt.ID()
	task.AttemptKey = ir.GenerateAttemptKey(
		task.RootDagRunName, task.RootDagRunId,
		task.Target, task.DagRunId, attempt.ID(),
	)

	if err := attempt.Open(ctx); err != nil {
		return nil, fmt.Errorf("failed to open sub-attempt: %w", err)
	}

	if writeErr := h.writeInitialStatus(ctx, attempt, task, task.Target, rootRef, labels); writeErr != nil {
		closeErr := attempt.Close(context.WithoutCancel(ctx))
		return nil, errors.Join(fmt.Errorf("failed to write initial status: %w", writeErr), closeErr)
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
func (h *Handler) writeInitialStatus(ctx context.Context, attempt dagrun.Attempt, task *coordinatorv1.Task, dagName string, root ir.DAGRunRef, labels []string) error {
	initialStatus := ir.DAGRunStatus{
		Name:         dagName,
		DAGRunID:     task.DagRunId,
		AttemptID:    attempt.ID(),
		AttemptKey:   task.AttemptKey,
		Status:       ir.NotStarted,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		Root:         root,
		Labels:       labels,
		TriggerActor: task.TriggerActor,
		ScheduleTime: task.ScheduleTime,
		DefinitionID: task.DefinitionId,
	}
	return attempt.Write(ctx, initialStatus)
}

func labelsForInitialStatus(task *coordinatorv1.Task, dag *ir.DAG) []string {
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
	if h.dagRunRepository == nil {
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

func (h *Handler) ensureWaitingWorkerAvailability(selector map[string]string, targetWorkerID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	matched := false
	for _, worker := range h.waitingPollers {
		if targetWorkerID != "" && worker.workerID != targetWorkerID {
			continue
		}
		if !matchesSelector(worker.labels, selector) {
			continue
		}
		matched = true
		break
	}
	if matched {
		return nil
	}
	if len(selector) > 0 || targetWorkerID != "" {
		return errNoMatchingWorkers
	}
	return errNoAvailableWorkers
}

func (h *Handler) dispatchToWaitingPoller(task *coordinatorv1.Task) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	matched := false
	for pollerID, worker := range h.waitingPollers {
		if task.TargetWorkerId != "" && worker.workerID != task.TargetWorkerId {
			continue
		}
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
	if (len(task.WorkerSelector) > 0 || task.TargetWorkerId != "") && !matched {
		return errNoMatchingWorkers
	}
	return errNoAvailableWorkers
}

func dispatchErrorCode(err error) codes.Code {
	var staleErr *queue.StaleQueueDispatchError
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
	if _, ok := errors.AsType[*queue.StaleQueueDispatchError](err); ok {
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
	if runStatus.Status != ir.NotStarted && runStatus.Status != ir.Queued {
		return
	}

	runStatus.Status = ir.Failed
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

func (h *Handler) releasePreparedDispatchAttempt(ctx context.Context, dagRunID string, attempt dagrun.Attempt) {
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
			workerInfo.RunningTasks = convert.WorkerStatsToProto(hb.Stats).RunningTasks
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
		if err := h.workerHeartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        req.WorkerId,
			Labels:          req.Labels,
			Stats:           convert.ProtoToWorkerStats(req.Stats),
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
		if errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
			if req.AttemptKey != "" && req.WorkerId != "" {
				lease, leaseErr := h.dagRunLeaseStore.Get(ctx, req.AttemptKey)
				if leaseErr == nil && lease.MatchesClaim(req.AttemptKey, req.WorkerId) &&
					lease.ClaimToken == req.ClaimToken {
					return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
				}
				if leaseErr != nil && !errors.Is(leaseErr, dispatch.ErrDAGRunLeaseNotFound) {
					return nil, status.Error(codes.Internal, "failed to load acknowledged claim: "+leaseErr.Error())
				}
			}
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
	claimOwner := claimed.Owner
	if claimOwner == (dispatch.CoordinatorEndpoint{}) {
		claimOwner = claimed.Task.Owner
	}
	workerID := req.WorkerId
	if workerID == "" {
		workerID = claimed.WorkerID
	}
	if workerID == "" {
		return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "worker_id is required"}, nil
	}

	if claimOwner != (dispatch.CoordinatorEndpoint{}) {
		claimed.Task.Owner = claimOwner
	}
	task, err := convert.DispatchTaskToProto(claimed.Task)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encode claimed task: "+err.Error())
	}
	if req.AttemptKey != "" && req.AttemptKey != task.AttemptKey {
		return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "claim belongs to a different attempt"}, nil
	}
	if err := h.attemptOwnership().recordTaskClaim(ctx, task, workerID); err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseConflict) {
			return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "attempt claim conflicts with the active lease"}, nil
		}
		return nil, status.Error(codes.Internal, "failed to create run lease: "+err.Error())
	}
	if err := h.dispatchTaskStore.DeleteClaim(ctx, req.ClaimToken); err != nil {
		return nil, status.Error(codes.Internal, "failed to finalize task claim: "+err.Error())
	}

	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

// ClaimAgentSessionCleanup reserves deferred provider cleanup for its owning worker.
func (h *Handler) ClaimAgentSessionCleanup(
	ctx context.Context,
	req *coordinatorv1.ClaimAgentSessionCleanupRequest,
) (*coordinatorv1.ClaimAgentSessionCleanupResponse, error) {
	if req.WorkerId == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if h.agentSessionCleanupQueue == nil || h.dagRunRepository == nil {
		return nil, status.Error(codes.Unimplemented, "agent session cleanup is not configured")
	}
	job, err := h.agentSessionCleanupQueue.Claim(ctx, req.WorkerId, time.Minute)
	if errors.Is(err, persis.ErrNotFound) {
		return &coordinatorv1.ClaimAgentSessionCleanupResponse{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to claim agent session cleanup: "+err.Error())
	}
	_, findErr := h.dagRunRepository.FindAttempt(ctx, job.Root)
	if findErr == nil {
		if err := h.agentSessionCleanupQueue.Release(ctx, req.WorkerId, job.ID, job.ClaimToken, "DAG run still exists"); err != nil {
			return nil, status.Error(codes.Internal, "failed to release agent session cleanup: "+err.Error())
		}
		return &coordinatorv1.ClaimAgentSessionCleanupResponse{}, nil
	}
	if !errors.Is(findErr, dagrun.ErrDAGRunIDNotFound) {
		_ = h.agentSessionCleanupQueue.Release(ctx, req.WorkerId, job.ID, job.ClaimToken, findErr.Error())
		return nil, status.Error(codes.Internal, "failed to verify removed DAG run: "+findErr.Error())
	}
	return &coordinatorv1.ClaimAgentSessionCleanupResponse{
		Found:                true,
		JobId:                job.ID,
		ClaimToken:           job.ClaimToken,
		Provider:             job.Resource.Provider,
		SessionId:            job.Resource.SessionID,
		Directory:            job.Resource.Directory,
		OwnerCoordinatorId:   h.owner.ID,
		OwnerCoordinatorHost: h.owner.Host,
		OwnerCoordinatorPort: int32(h.owner.Port), //nolint:gosec // Configured network ports fit in int32.
	}, nil
}

// CompleteAgentSessionCleanup completes or releases a provider cleanup claim.
func (h *Handler) CompleteAgentSessionCleanup(
	ctx context.Context,
	req *coordinatorv1.CompleteAgentSessionCleanupRequest,
) (*coordinatorv1.CompleteAgentSessionCleanupResponse, error) {
	if req.WorkerId == "" || req.JobId == "" || req.ClaimToken == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id, job_id, and claim_token are required")
	}
	if h.agentSessionCleanupQueue == nil {
		return nil, status.Error(codes.Unimplemented, "agent session cleanup is not configured")
	}
	var err error
	if req.Error == "" {
		err = h.agentSessionCleanupQueue.Complete(ctx, req.WorkerId, req.JobId, req.ClaimToken)
	} else {
		err = h.agentSessionCleanupQueue.Release(ctx, req.WorkerId, req.JobId, req.ClaimToken, req.Error)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to update agent session cleanup: "+err.Error())
	}
	return &coordinatorv1.CompleteAgentSessionCleanupResponse{}, nil
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
	cancelledRuns := make([]*coordinatorv1.CancelledRun, 0)
	observedAt := time.Now().UTC()
	for _, task := range req.RunningTasks {
		if task == nil || task.AttemptKey == "" {
			continue
		}
		identity, identityErr := runningTaskIdentity(req.WorkerId, task)
		if identityErr != nil {
			cancelledRuns = appendCancelledRunIfMissing(cancelledRuns, task.AttemptKey)
			continue
		}
		if err := h.validateAttempt(ctx, identity); err != nil {
			if status.Code(err) == codes.FailedPrecondition {
				cancelledRuns = appendCancelledRunIfMissing(cancelledRuns, task.AttemptKey)
				continue
			}
			return nil, err
		}
		if err := h.refreshRunLease(ctx, task.AttemptKey, observedAt); err != nil {
			if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) || errors.Is(err, persis.ErrCorrupt) {
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

func (h *Handler) refreshRunLease(ctx context.Context, attemptKey string, observedAt time.Time) error {
	lease, err := h.dagRunLeaseStore.Get(ctx, attemptKey)
	if err != nil {
		return err
	}
	if lease != nil && observedAt.Before(lease.LastHeartbeatTime().Add(h.leaseRefreshWriteInterval())) {
		return nil
	}
	return h.dagRunLeaseStore.Touch(ctx, attemptKey, observedAt)
}

func (h *Handler) repairStaleLeaseFailureFromRunHeartbeat(
	ctx context.Context,
	workerID string,
	task *coordinatorv1.RunningTask,
	observedAt time.Time,
) {
	if h.dagRunRepository == nil || h.dagRunLeaseStore == nil || task == nil || task.AttemptKey == "" {
		return
	}

	repairCtx, cancelRepair := context.WithTimeout(context.WithoutCancel(ctx), runHeartbeatRepairTimeout)
	defer cancelRepair()

	lease, err := h.dagRunLeaseStore.Get(repairCtx, task.AttemptKey)
	if err != nil {
		if !errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
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

	reason := dispatch.DistributedLeaseExpiredReason(workerID)
	_, currentStatus, err := h.resolveLatestAttempt(repairCtx, lease.DAGRun.Name, lease.DAGRun.ID, lease.Root)
	if err != nil {
		if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) && !errors.Is(err, dagrun.ErrNoStatusData) {
			logger.Warn(ctx, "Failed to read distributed run before stale failure repair",
				tag.RunID(lease.DAGRun.ID),
				tag.AttemptKey(task.AttemptKey),
				tag.Error(err),
			)
		}
		return
	}
	if !h.canRepairStaleLeaseFailureFromRunHeartbeat(workerID, task, lease, currentStatus, reason, observedAt) {
		return
	}

	repairedStatus, swapped, err := h.dagRunRepository.CompareAndSwapLatestAttemptStatus(
		repairCtx,
		lease.DAGRun,
		lease.AttemptID,
		ir.Failed,
		func(status *ir.DAGRunStatus) error {
			if !h.canRepairStaleLeaseFailureFromRunHeartbeat(workerID, task, lease, status, reason, observedAt) {
				return errRunHeartbeatRepairSkipped
			}
			restoreStaleLeaseFailure(status, lease, workerID, reason)
			return nil
		}, persis.DAGRunCompareAndSwapOptions{RootDAGRun: lease.Root, ExpectedAttemptKey: lease.AttemptKey},
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

	h.attemptOwnership().upsertActiveFromStatus(repairCtx, repairedStatus, workerID, lease.AttemptID)
	logger.Info(ctx, "Repaired stale distributed run failure from fresh heartbeat",
		tag.DAG(lease.DAGRun.Name),
		tag.RunID(lease.DAGRun.ID),
		tag.AttemptKey(task.AttemptKey),
	)
}

func (h *Handler) canRepairStaleLeaseFailureFromRunHeartbeat(
	workerID string,
	task *coordinatorv1.RunningTask,
	lease *dispatch.DAGRunLease,
	status *ir.DAGRunStatus,
	reason string,
	observedAt time.Time,
) bool {
	if workerID == "" || task == nil || lease == nil || status == nil {
		return false
	}
	if status.Status != ir.Failed || status.Error != reason {
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
	return dispatch.LeaseMatchesStatus(lease, status, lease.AttemptID, observedAt, h.staleLeaseThreshold)
}

func restoreStaleLeaseFailure(status *ir.DAGRunStatus, lease *dispatch.DAGRunLease, workerID, reason string) {
	status.Status = ir.Running
	status.Error = ""
	status.FinishedAt = ""
	status.WorkerID = workerID
	status.AttemptID = lease.AttemptID
	status.AttemptKey = lease.AttemptKey
	for _, node := range status.Nodes {
		if node == nil || node.Status != ir.NodeFailed || node.Error != reason {
			continue
		}
		if node.StartedAt != "" && node.StartedAt != "-" {
			node.Status = ir.NodeRunning
			node.FinishedAt = ""
		} else {
			node.Status = ir.NodeNotStarted
			node.StartedAt = "-"
			node.FinishedAt = "-"
		}
		node.Error = ""
	}
}

func (h *Handler) listHealthyWorkers(ctx context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
	if h.workerHeartbeatStore == nil {
		return nil, nil
	}

	records, err := h.workerHeartbeatStore.List(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	healthy := make([]dispatch.WorkerHeartbeatRecord, 0, len(records))
	for _, record := range records {
		if dispatch.WorkerHeartbeatFresh(record, now, h.staleHeartbeatThreshold) {
			healthy = append(healthy, record)
		}
	}
	return healthy, nil
}

func (h *Handler) refreshLeasesFromHeartbeat(ctx context.Context, workerID string, stats *coordinatorv1.WorkerStats, observedAt time.Time) {
	if h.dagRunRepository == nil || stats == nil || len(stats.RunningTasks) == 0 {
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

	if runStatus.Status != ir.Running || runStatus.WorkerID == "" {
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

func (h *Handler) getOrOpenAttemptForRunningTask(ctx context.Context, task *coordinatorv1.RunningTask) (dagrun.Attempt, error) {
	if task == nil {
		return nil, fmt.Errorf("running task is nil")
	}

	isSubDAG := task.RootDagRunId != "" && task.RootDagRunId != task.DagRunId
	if isSubDAG {
		if task.RootDagRunName == "" {
			return nil, fmt.Errorf("missing root dag run name for sub-dag %s", task.DagRunId)
		}
		return h.getOrOpenSubAttempt(ctx, ir.DAGRunRef{
			Name: task.RootDagRunName,
			ID:   task.RootDagRunId,
		}, task.DagRunId)
	}

	return h.getOrOpenAttempt(ctx, task.DagName, task.DagRunId)
}

func (h *Handler) shouldThrottleLeaseRefresh(status *ir.DAGRunStatus, observedAt time.Time) bool {
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

func anyWorkerMatches(workers []dispatch.WorkerHeartbeatRecord, selector map[string]string, targetWorkerID string) bool {
	for _, worker := range workers {
		if dispatch.WorkerHeartbeatMatches(worker, selector, targetWorkerID) {
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
	if h.dagRunRepository == nil || stats == nil || len(stats.RunningTasks) == 0 {
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
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
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
func (h *Handler) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	ctx = h.eventContext(ctx)

	if h.dagRunRepository == nil {
		return nil, status.Error(codes.FailedPrecondition, "status reporting not configured: DAG run storage not available")
	}
	if req.Status == nil {
		return nil, status.Error(codes.InvalidArgument, "status is required")
	}

	// Convert proto to execution.DAGRunStatus
	dagRunStatus, convErr := convert.ProtoToDAGRunStatus(req.Status)
	if convErr != nil {
		return nil, status.Error(codes.InvalidArgument, "failed to convert status: "+convErr.Error())
	}
	if len(dagRunStatus.Labels) == 0 {
		dagRunStatus.Labels = splitTaskLabels(req.Labels)
	}
	leaseMissing := false
	if h.dagRunLeaseStore != nil {
		var validationErr error
		leaseMissing, validationErr = h.validateStatusLease(ctx, req.WorkerId, dagRunStatus)
		if validationErr != nil {
			if status.Code(validationErr) == codes.FailedPrecondition {
				return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: status.Convert(validationErr).Message()}, nil
			}
			return nil, validationErr
		}
	}

	// Transform worker-local log paths to coordinator paths.
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
		bootstrappedAttempt, bootstrapped, bootstrapErr := h.bootstrapMissingSubAttempt(ctx, req.WorkerId, req.SourceFile, dagRunStatus, err)
		if bootstrapErr != nil {
			return nil, status.Error(codes.Internal, "failed to bootstrap sub-attempt: "+bootstrapErr.Error())
		}
		if bootstrapped {
			h.transformLogPaths(dagRunStatus)
			if err := h.transformArtifactPaths(ctx, bootstrappedAttempt, nil, dagRunStatus); err != nil {
				return nil, status.Error(codes.Internal, "failed to resolve artifact path: "+err.Error())
			}
			if err := bootstrappedAttempt.Write(ctx, *dagRunStatus); err != nil {
				return nil, status.Error(codes.Internal, "failed to write status: "+err.Error())
			}

			h.persistChatMessages(ctx, bootstrappedAttempt, dagRunStatus)

			ownership := h.attemptOwnership()
			// Live tracking must complete after the status write even if the
			// reporting worker disconnects.
			ownership.syncFromStatus(context.WithoutCancel(ctx), req.WorkerId, dagRunStatus, bootstrappedAttempt.ID())
			h.finalizeAdmissionForStatus(ctx, dagRunStatus, bootstrappedAttempt.ID())
			h.closeCachedWaitingAttempt(ctx, dagRunStatus, bootstrappedAttempt)

			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		}
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
			logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, nil, remoteAttemptRejectedLeaseInactive)
			return &coordinatorv1.ReportStatusResponse{
				Accepted: false,
				Error:    remoteAttemptRejectedLeaseInactive,
			}, nil
		}
		return nil, status.Error(codes.Internal, "failed to resolve latest attempt: "+err.Error())
	}

	ownership := h.attemptOwnership()
	if leaseMissing && latestStatus.Status != ir.NotStarted && !isTerminalRunStatus(latestStatus.Status) {
		logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, latestStatus, remoteAttemptRejectedLeaseInactive)
		return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: remoteAttemptRejectedLeaseInactive}, nil
	}
	accepted, rejectReason := ownership.statusDecision(ctx, latestStatus, dagRunStatus, statusDecisionOptions{
		CancellationRequested: h.sameAttemptCancellationRequested(ctx, latestAttempt, latestStatus, dagRunStatus),
		ClaimKey:              dagRunStatus.EffectiveClaimKey(),
	})
	if !accepted {
		logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, latestStatus, rejectReason)
		return &coordinatorv1.ReportStatusResponse{
			Accepted: false,
			Error:    rejectReason,
		}, nil
	}
	if err := h.transformArtifactPaths(ctx, latestAttempt, latestStatus, dagRunStatus); err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve artifact path: "+err.Error())
	}
	if len(latestStatus.Labels) > 0 {
		dagRunStatus.Labels = append([]string(nil), latestStatus.Labels...)
	}

	attempt := latestAttempt
	if dagRunStatus.Status == ir.Waiting {
		h.closeCachedAttemptForRun(ctx, context.WithoutCancel(ctx), dagRunStatus.DAGRunID, latestAttempt.ID())
		persisted, swapped, err := h.dagRunRepository.CompareAndSwapLatestAttemptStatus(
			ctx,
			dagRunStatus.DAGRun(),
			latestAttempt.ID(),
			latestStatus.Status,
			func(current *ir.DAGRunStatus) error {
				if !preservesCompletedManualActions(current, dagRunStatus) {
					return errManualActionCheckpointChange
				}
				*current = *dagRunStatus
				return nil
			}, persis.DAGRunCompareAndSwapOptions{RootDAGRun: dagRunStatus.Root, ExpectedAttemptKey: dagRunStatus.AttemptKey},
		)
		if errors.Is(err, errManualActionCheckpointChange) {
			logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, latestStatus, remoteAttemptRejectedManualAction)
			return &coordinatorv1.ReportStatusResponse{
				Accepted: false,
				Error:    remoteAttemptRejectedManualAction,
			}, nil
		}
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to write waiting status: "+err.Error())
		}
		if !swapped {
			logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, persisted, remoteAttemptRejectedSuperseded)
			return &coordinatorv1.ReportStatusResponse{
				Accepted: false,
				Error:    remoteAttemptRejectedSuperseded,
			}, nil
		}
		dagRunStatus = persisted
	} else {
		attempt, err = h.replaceOpenAttempt(ctx, dagRunStatus.DAGRunID, latestAttempt, latestStatus.AttemptID)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to get/open latest attempt: "+err.Error())
		}
		if err := attempt.Write(ctx, *dagRunStatus); err != nil {
			return nil, status.Error(codes.Internal, "failed to write status: "+err.Error())
		}
	}

	// Persist chat messages for each node.
	h.persistChatMessages(ctx, attempt, dagRunStatus)

	// Live tracking must complete after the status write even if the reporting
	// worker disconnects.
	ownership.syncFromStatus(context.WithoutCancel(ctx), req.WorkerId, dagRunStatus, attempt.ID())
	h.finalizeAdmissionForStatus(ctx, dagRunStatus, attempt.ID())
	h.closeCachedWaitingAttempt(ctx, dagRunStatus, attempt)

	// Note: We don't close the attempt immediately on terminal status because
	// the agent may push the same terminal status multiple times from different
	// code paths. Attempts are cleaned up during coordinator shutdown.
	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

func preservesCompletedManualActions(current, incoming *ir.DAGRunStatus) bool {
	if current == nil || incoming == nil {
		return false
	}
	incomingNodes := make(map[string]*ir.Node, len(incoming.Nodes))
	for _, node := range incoming.Nodes {
		if node != nil {
			incomingNodes[manualActionNodeKey(node.Step)] = node
		}
	}
	for _, node := range current.Nodes {
		if node == nil {
			continue
		}
		completedHumanTask := node.Step.HumanTask != nil && len(node.HumanTaskInput) > 0
		completedApproval := node.Step.Approval != nil && node.ApprovedAt != ""
		pushedBack := node.ApprovalIteration > 0
		if !completedHumanTask && !completedApproval && !pushedBack {
			continue
		}
		next := incomingNodes[manualActionNodeKey(node.Step)]
		if next == nil {
			return false
		}
		if (completedHumanTask || completedApproval) &&
			(next.Status != node.Status || next.FinishedAt != node.FinishedAt) {
			return false
		}
		if completedHumanTask &&
			(next.Step.HumanTask == nil ||
				!bytes.Equal(next.HumanTaskInput, node.HumanTaskInput) ||
				next.HumanTaskCompletedBy != node.HumanTaskCompletedBy ||
				next.HumanTaskCompletedByID != node.HumanTaskCompletedByID ||
				!equalOptionalString(next.StepOutputsValue, node.StepOutputsValue)) {
			return false
		}
		if completedApproval &&
			(next.Step.Approval == nil ||
				next.ApprovedAt != node.ApprovedAt ||
				next.ApprovedBy != node.ApprovedBy ||
				next.ApprovedByID != node.ApprovedByID ||
				!maps.Equal(next.ApprovalInputs, node.ApprovalInputs)) {
			return false
		}
		if pushedBack &&
			(next.ApprovalIteration != node.ApprovalIteration ||
				!maps.Equal(next.PushBackInputs, node.PushBackInputs) ||
				!reflect.DeepEqual(next.PushBackHistory, node.PushBackHistory) ||
				next.PushBackPreviousStdout != node.PushBackPreviousStdout) {
			return false
		}
	}
	return true
}

func manualActionNodeKey(step ir.Step) string {
	if step.ID != "" {
		return "id:" + step.ID
	}
	return "name:" + step.Name
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func (h *Handler) closeCachedWaitingAttempt(
	ctx context.Context,
	status *ir.DAGRunStatus,
	attempt dagrun.Attempt,
) {
	if status == nil || attempt == nil || status.Status != ir.Waiting {
		return
	}
	h.closeCachedAttemptForRun(ctx, context.WithoutCancel(ctx), status.DAGRunID, attempt.ID())
}

func (h *Handler) bootstrapMissingSubAttempt(
	ctx context.Context,
	workerID string,
	sourceFile string,
	runStatus *ir.DAGRunStatus,
	resolveErr error,
) (dagrun.Attempt, bool, error) {
	if !errors.Is(resolveErr, dagrun.ErrDAGRunIDNotFound) || runStatus == nil {
		return nil, false, nil
	}

	rootRef := runStatus.Root
	if rootRef.ID == "" || rootRef.Name == "" || rootRef.ID == runStatus.DAGRunID {
		return nil, false, nil
	}
	if runStatus.Name == "" || runStatus.DAGRunID == "" {
		return nil, false, nil
	}

	reportingWorkerID, ok := remoteWorkerIDForBootstrap(runStatus, workerID)
	if !ok {
		return nil, false, nil
	}

	rootAttempt, err := h.dagRunRepository.FindAttempt(ctx, rootRef)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("find root attempt: %w", err)
	}
	rootStatus, err := rootAttempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrNoStatusData) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read root status: %w", err)
	}
	if rootStatus == nil || isTerminalRunStatus(rootStatus.Status) {
		return nil, false, nil
	}

	dag := &ir.DAG{Name: runStatus.Name, SourceFile: sourceFile}
	attempt, err := h.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), runStatus.DAGRunID, persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
	})
	if err != nil {
		return nil, false, fmt.Errorf("create sub-attempt: %w", err)
	}
	if err := attempt.Open(ctx); err != nil {
		return nil, false, fmt.Errorf("open sub-attempt: %w", err)
	}

	attemptID := runStatus.AttemptID
	if attemptID == "" {
		attemptID = attempt.ID()
		runStatus.AttemptID = attemptID
	}
	runStatus.AttemptKey = ir.GenerateAttemptKey(
		rootRef.Name,
		rootRef.ID,
		runStatus.Name,
		runStatus.DAGRunID,
		attemptID,
	)
	if runStatus.WorkerID == "" {
		runStatus.WorkerID = reportingWorkerID
	}

	h.attemptsMu.Lock()
	h.openAttempts[runStatus.DAGRunID] = attempt
	h.attemptsMu.Unlock()

	return attempt, true, nil
}

func remoteWorkerIDForBootstrap(runStatus *ir.DAGRunStatus, fallbackWorkerID string) (string, bool) {
	if runStatus == nil {
		return "", false
	}
	if runStatus.WorkerID != "" {
		if !dispatch.IsRemoteWorkerID(runStatus.WorkerID) {
			return "", false
		}
		if fallbackWorkerID != "" && fallbackWorkerID != runStatus.WorkerID {
			return "", false
		}
		return runStatus.WorkerID, true
	}
	if !dispatch.IsRemoteWorkerID(fallbackWorkerID) {
		return "", false
	}
	return fallbackWorkerID, true
}

func (h *Handler) sameAttemptCancellationRequested(
	ctx context.Context,
	attempt dagrun.Attempt,
	latest *ir.DAGRunStatus,
	incoming *ir.DAGRunStatus,
) bool {
	if attempt == nil || latest == nil || incoming == nil {
		return false
	}
	if latest.Status != ir.Failed || incoming.Status != ir.Aborted || !sameAttemptStatus(latest, incoming) {
		return false
	}
	aborting, err := attempt.IsAborting(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to check abort state while validating remote terminal status",
			tag.RunID(incoming.DAGRunID),
			tag.AttemptKey(incoming.AttemptKey),
			tag.Error(err),
		)
		return false
	}
	return aborting
}

// transformLogPaths rewrites worker-local log paths to coordinator paths.
func (h *Handler) transformLogPaths(status *ir.DAGRunStatus) {
	if h.logDir == "" {
		return
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
	transformNode := func(node *ir.Node, fallbackName string) {
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
	attempt dagrun.Attempt,
	latestStatus *ir.DAGRunStatus,
	incoming *ir.DAGRunStatus,
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
		resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{})
		baseDir, err = resolver.String(ctx, baseDir, cmnvalue.CoordinatorArtifactBaseDirField("artifacts.dir"))
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
// Errors are logged but don't fail the status update since messages are auxiliary data.
func (h *Handler) persistChatMessages(ctx context.Context, attempt dagrun.Attempt, status *ir.DAGRunStatus) {
	// Helper to persist messages for a single node
	persistNode := func(node *ir.Node, fallbackName string) {
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

// getOrOpenAttempt retrieves an open attempt from cache or opens a new one.
// Uses double-check locking to avoid holding the mutex during blocking I/O.
func (h *Handler) getOrOpenAttempt(ctx context.Context, dagName, dagRunID string) (dagrun.Attempt, error) {
	ref := ir.DAGRunRef{Name: dagName, ID: dagRunID}
	return h.getOrOpenAttemptWithFinder(ctx, dagRunID, func() (dagrun.Attempt, error) {
		return h.dagRunRepository.FindAttempt(ctx, ref)
	})
}

func (h *Handler) resolveLatestAttempt(
	ctx context.Context,
	dagName, dagRunID string,
	rootRef ir.DAGRunRef,
) (dagrun.Attempt, *ir.DAGRunStatus, error) {
	if h.dagRunRepository == nil {
		return nil, nil, dagrun.ErrDAGRunIDNotFound
	}

	var (
		attempt dagrun.Attempt
		err     error
	)
	if rootRef.ID != "" && rootRef.ID != dagRunID {
		if rootRef.Name == "" {
			return nil, nil, fmt.Errorf("missing root dag run name for sub-dag %s", dagRunID)
		}
		attempt, err = h.dagRunRepository.FindSubAttempt(ctx, rootRef, dagRunID)
	} else {
		attempt, err = h.dagRunRepository.FindAttempt(ctx, ir.DAGRunRef{Name: dagName, ID: dagRunID})
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

func (h *Handler) resolveLatestAttemptForRunningTask(ctx context.Context, task *coordinatorv1.RunningTask) (dagrun.Attempt, *ir.DAGRunStatus, error) {
	rootRef := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	return h.resolveLatestAttempt(ctx, task.DagName, task.DagRunId, rootRef)
}

func (h *Handler) replaceOpenAttempt(
	ctx context.Context,
	cacheKey string,
	latestAttempt dagrun.Attempt,
	expectedAttemptID string,
) (dagrun.Attempt, error) {
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
func (h *Handler) getOrOpenSubAttempt(ctx context.Context, rootRef ir.DAGRunRef, subDAGRunID string) (dagrun.Attempt, error) {
	return h.getOrOpenAttemptWithFinder(ctx, subDAGRunID, func() (dagrun.Attempt, error) {
		return h.dagRunRepository.FindSubAttempt(ctx, rootRef, subDAGRunID)
	})
}

// getOrOpenAttemptWithFinder is a generic helper that retrieves an open attempt from cache
// or uses the provided finder function to locate and open a new one.
// Uses per-run mutex to prevent concurrent I/O operations on the same DAG run,
// avoiding races between ReportStatus and markRunFailed.
func (h *Handler) getOrOpenAttemptWithFinder(ctx context.Context, cacheKey string, finder func() (dagrun.Attempt, error)) (dagrun.Attempt, error) {
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
func (h *Handler) StreamLogs(stream coordinatorv1.CoordinatorService_StreamLogsServer) error {
	if h.logDir == "" {
		return status.Error(codes.FailedPrecondition, "log streaming not configured: logDir is empty")
	}

	// Delegate to the log handler
	logHandler := newLogHandler(h.logDir)
	if h.dagRunLeaseStore != nil {
		logHandler.attemptValidator = h.validateAttempt
	}
	defer logHandler.Close(stream.Context()) // Ensure file handles are closed on stream end or error
	return logHandler.handleStream(stream)
}

// StreamArtifacts receives artifact streams from workers and writes them to local filesystem.
func (h *Handler) StreamArtifacts(stream coordinatorv1.CoordinatorService_StreamArtifactsServer) error {
	if h.artifactDir == "" {
		return status.Error(codes.FailedPrecondition, "artifact streaming not configured: artifactDir is empty")
	}
	if h.dagRunRepository == nil {
		return status.Error(codes.FailedPrecondition, "artifact streaming not configured: dagRunRepository is empty")
	}

	artifactHandler := newArtifactHandler(h.dagRunRepository)
	if h.dagRunLeaseStore != nil {
		artifactHandler.attemptValidator = h.validateAttempt
	}
	return artifactHandler.handleStream(stream)
}

// GetDAGRunStatus retrieves the status of a DAG run.
// Parent DAGs use this to poll remote sub-DAG status through the coordinator.
func (h *Handler) GetDAGRunStatus(ctx context.Context, req *coordinatorv1.GetDAGRunStatusRequest) (*coordinatorv1.GetDAGRunStatusResponse, error) {
	if h.dagRunRepository == nil {
		return nil, status.Error(codes.FailedPrecondition, "DAG run status query not configured: dagRunRepository is nil")
	}

	if req.DagName == "" || req.DagRunId == "" {
		return nil, status.Error(codes.InvalidArgument, "dag_name and dag_run_id are required")
	}

	var attempt dagrun.Attempt
	var err error

	// Always read the latest attempt from disk rather than using the openAttempts
	// cache. Remote workers report status to the coordinator, and ReportStatus
	// writes to the coordinator-owned attempt before this status query reads it.
	if req.RootDagRunName != "" && req.RootDagRunId != "" {
		// Look up as a sub-DAG
		rootRef := ir.DAGRunRef{Name: req.RootDagRunName, ID: req.RootDagRunId}
		attempt, err = h.dagRunRepository.FindSubAttempt(ctx, rootRef, req.DagRunId)
	} else {
		// Look up as a top-level DAG run
		ref := ir.DAGRunRef{Name: req.DagName, ID: req.DagRunId}
		attempt, err = h.dagRunRepository.FindAttempt(ctx, ref)
	}

	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
		return &coordinatorv1.GetDAGRunStatusResponse{
			Found: false,
		}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to find DAG run status: "+err.Error())
	}

	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to read DAG run status: "+err.Error())
	}

	protoStatus, convErr := convert.DAGRunStatusToProto(runStatus)
	if convErr != nil {
		return nil, status.Error(codes.Internal, "failed to convert DAG run status: "+convErr.Error())
	}

	return &coordinatorv1.GetDAGRunStatusResponse{
		Found:  true,
		Status: protoStatus,
	}, nil
}

// GetDAG retrieves the raw specification of a DAG by name.
// Workers use this to obtain DAG definitions that may not be available locally.
func (h *Handler) GetDAG(ctx context.Context, req *coordinatorv1.GetDAGRequest) (*coordinatorv1.GetDAGResponse, error) {
	if h.dagRepository == nil {
		return nil, status.Error(codes.Unimplemented, "DAG store not configured: GetDAG is not available")
	}
	spec, err := h.dagRepository.GetSpec(ctx, req.Name)
	if err != nil {
		return &coordinatorv1.GetDAGResponse{Error: err.Error()}, nil
	}
	return &coordinatorv1.GetDAGResponse{Spec: spec}, nil
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
		if h.dagRunLeaseStore != nil {
			timer := time.NewTimer(h.staleLeaseThreshold)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
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
	if h.dagRunRepository == nil {
		return
	}
	if h.dagRunLeaseStore == nil {
		activeStatuses := []ir.Status{ir.Running}
		statuses, err := h.dagRunRepository.ListStatuses(ctx, persis.DAGRunListOptions{Statuses: activeStatuses, Unbounded: true})
		if err != nil {
			logger.Error(ctx, "Failed to list active statuses for lease check", tag.Error(err))
			return
		}
		for _, st := range statuses {
			if st.WorkerID == "" || st.LeaseAt == 0 {
				continue
			}
			if !dagrun.IsLeaseActive(st, h.staleLeaseThreshold) {
				reason := fmt.Sprintf("lease expired: worker %s stopped reporting status", st.WorkerID)
				h.markRunFailed(ctx, st.Name, st.DAGRunID, reason)
			}
		}
		return
	}

	now := time.Now().UTC()
	h.reconcileLeases(ctx, now)

	if h.activeDistributedRunStore != nil {
		h.reconcileActiveRuns(ctx, now)
		return
	}

	h.reconcileRemoteStatuses(ctx, now)
}

func (h *Handler) reconcileLeases(ctx context.Context, now time.Time) {
	leases, err := h.dagRunLeaseStore.ListAll(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list active distributed leases", tag.Error(err))
		return
	}

	for _, lease := range leases {
		h.reconcileLease(ctx, lease, now)
	}
}

func (h *Handler) reconcileLease(ctx context.Context, lease dispatch.DAGRunLease, now time.Time) {
	if lease.AttemptKey == "" {
		logger.Warn(ctx, "Skipping distributed lease reconciliation due to missing attempt key",
			tag.DAG(lease.DAGRun.Name),
			tag.RunID(lease.DAGRun.ID),
		)
		return
	}
	ownership := h.attemptOwnership()

	attempt, runStatus, err := h.resolveLatestAttempt(ctx, lease.DAGRun.Name, lease.DAGRun.ID, lease.Root)
	switch {
	case err == nil:
	case errors.Is(err, dagrun.ErrDAGRunIDNotFound),
		errors.Is(err, dagrun.ErrNoStatusData),
		errors.Is(err, dagrun.ErrCorruptedStatusData):
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
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
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete distributed lease for empty leased status",
			"Failed to delete active distributed run for empty leased status",
		)
		return
	}

	workerID, ok := remoteWorkerID(runStatus, lease.WorkerID)
	if !ok || !dispatch.LeaseIdentityMatchesStatus(&lease, runStatus, attemptID) {
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete superseded distributed lease",
			"Failed to delete superseded active distributed run",
		)
		return
	}

	switch runStatus.Status {
	case ir.Running, ir.NotStarted, ir.Queued:
		if lease.MatchesClaim(runStatus.EffectiveClaimKey(), workerID) && lease.IsFresh(now, h.staleLeaseThreshold) {
			ownership.upsertActiveFromStatus(ctx, runStatus, workerID, attemptID)
			return
		}
	case ir.Failed, ir.Aborted, ir.Succeeded, ir.PartiallySucceeded, ir.Waiting, ir.Rejected:
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete inactive distributed lease",
			"Failed to delete inactive active distributed run",
		)
		return
	default:
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete unknown-state distributed lease",
			"Failed to delete unknown-state active distributed run",
		)
		return
	}

	if h.workerHeartbeatStore == nil {
		h.failLeasedRun(ctx, runStatus, attemptID, lease.AttemptKey, dispatch.DistributedLeaseExpiredReason(workerID))
		return
	}

	reconciledStatus, repaired, err := h.repairStaleRun(ctx, runStatus, attemptID, workerID)
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
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete stale distributed lease after confirmed failure",
			"Failed to delete active distributed run after confirmed failure",
		)
		logger.Warn(ctx, "Marked stale distributed run as FAILED",
			tag.DAG(lease.DAGRun.Name),
			tag.RunID(lease.DAGRun.ID),
			slog.String("reason", dispatch.DistributedLeaseExpiredReason(workerID)),
		)
		return
	}
	if reconciledStatus == nil {
		return
	}
	if reconciledStatus.AttemptID != attemptID || (!reconciledStatus.Status.IsActive() && reconciledStatus.Status != ir.NotStarted) {
		ownership.deleteTracking(ctx, context.WithoutCancel(ctx), lease.DAGRun, lease.AttemptKey,
			"Failed to delete superseded distributed lease after reconciliation",
			"Failed to delete superseded active distributed run after reconciliation",
		)
		return
	}
	if reconciledWorkerID, ok := remoteWorkerID(reconciledStatus, workerID); ok {
		ownership.restoreConfirmedFromStatus(ctx, reconciledWorkerID, reconciledStatus, attemptID)
	}
}

func (h *Handler) repairStaleRun(
	ctx context.Context,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
	fallbackWorkerID string,
) (*ir.DAGRunStatus, bool, error) {
	repairCtx := context.WithoutCancel(ctx)
	if status != nil && status.DAGRunID != "" {
		runMu := h.getRunMutex(status.DAGRunID)
		runMu.Lock()
		defer runMu.Unlock()

		attemptID := status.AttemptID
		if attemptID == "" {
			attemptID = fallbackAttemptID
		}
		h.closeCachedAttemptForRun(ctx, repairCtx, status.DAGRunID, attemptID)
	}

	return runtime.RepairStaleRemoteRun(repairCtx, runtime.StaleRunRepairConfig{
		DAGRunRepository:              h.dagRunRepository,
		DAGRunLeaseStore:              h.dagRunLeaseStore,
		WorkerHeartbeatStore:          h.workerHeartbeatStore,
		StaleLeaseThreshold:           h.staleLeaseThreshold,
		StaleWorkerHeartbeatThreshold: h.staleHeartbeatThreshold,
	}, status, fallbackAttemptID, fallbackWorkerID)
}

func (h *Handler) reconcileRemoteStatuses(ctx context.Context, now time.Time) {
	ownership := h.attemptOwnership()
	statuses, err := h.dagRunRepository.ListStatuses(ctx, persis.DAGRunListOptions{Statuses: []ir.Status{ir.Running, ir.NotStarted}, Unbounded: true})
	if err != nil {
		logger.Error(ctx, "Failed to list distributed statuses for orphaned lease check", tag.Error(err))
		return
	}

	for _, status := range statuses {
		if status == nil || !dispatch.IsRemoteWorkerID(status.WorkerID) {
			continue
		}

		leaseState, ok := h.loadClaim(ctx, status)
		if !ok {
			continue
		}

		if leaseState.lease.MatchesClaim(status.EffectiveClaimKey(), status.WorkerID) &&
			leaseState.lease.IsFresh(now, h.staleLeaseThreshold) {
			continue
		}

		if h.workerHeartbeatStore == nil {
			h.failLeasedRun(ctx, status, leaseState.attemptID, leaseState.attemptKey, dispatch.DistributedLeaseExpiredReason(status.WorkerID))
			continue
		}

		reconciledStatus, repaired, err := h.repairStaleRun(ctx, status, leaseState.attemptID, status.WorkerID)
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
			ownership.deleteTracking(ctx, context.WithoutCancel(ctx), status.DAGRun(), leaseState.attemptKey,
				"Failed to delete orphaned distributed lease after confirmed failure",
				"Failed to delete orphaned active distributed run after confirmed failure",
			)
			continue
		}
		if reconciledStatus == nil {
			continue
		}
		if reconciledStatus.AttemptID != leaseState.attemptID || (!reconciledStatus.Status.IsActive() && reconciledStatus.Status != ir.NotStarted) {
			ownership.deleteTracking(ctx, context.WithoutCancel(ctx), status.DAGRun(), leaseState.attemptKey,
				"Failed to delete superseded orphaned distributed lease after reconciliation",
				"Failed to delete superseded orphaned active distributed run after reconciliation",
			)
			continue
		}
		if reconciledWorkerID, ok := remoteWorkerID(reconciledStatus, status.WorkerID); ok {
			ownership.restoreConfirmedFromStatus(ctx, reconciledWorkerID, reconciledStatus, leaseState.attemptID)
		}

	}
}

func (h *Handler) reconcileActiveRuns(ctx context.Context, now time.Time) {
	ownership := h.attemptOwnership()
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
		case errors.Is(err, dagrun.ErrDAGRunIDNotFound),
			errors.Is(err, dagrun.ErrNoStatusData),
			errors.Is(err, dagrun.ErrCorruptedStatusData):
			ownership.deleteTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
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

		workerID, ok := remoteWorkerID(runStatus, record.WorkerID)
		if !ok || !ownership.indexedRunMatchesStatus(record, runStatus) {
			ownership.deleteTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete superseded distributed lease from active index",
				"Failed to delete superseded active distributed run",
			)
			continue
		}

		claimKey := runStatus.EffectiveClaimKey()
		if claimKey == "" {
			claimKey = record.AttemptKey
		}
		lease, err := h.dagRunLeaseStore.Get(ctx, claimKey)
		switch {
		case err == nil:
		case errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
			lease = nil
		default:
			logger.Error(ctx, "Failed to read claim lease for indexed run",
				tag.AttemptKey(claimKey),
				tag.Error(err),
			)
			continue
		}

		if lease.MatchesClaim(claimKey, workerID) && lease.IsFresh(now, h.staleLeaseThreshold) {
			ownership.upsertActiveFromStatus(ctx, runStatus, workerID, record.AttemptID)
			continue
		}

		if h.workerHeartbeatStore == nil {
			h.failLeasedRun(ctx, runStatus, record.AttemptID, record.AttemptKey, dispatch.DistributedLeaseExpiredReason(workerID))
			continue
		}

		reconciledStatus, repaired, err := h.repairStaleRun(ctx, runStatus, record.AttemptID, workerID)
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
			ownership.deleteTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete stale indexed distributed lease after confirmed failure",
				"Failed to delete stale indexed active distributed run after confirmed failure",
			)
			continue
		}
		if reconciledStatus == nil {
			continue
		}
		if reconciledStatus.AttemptID != record.AttemptID || (!reconciledStatus.Status.IsActive() && reconciledStatus.Status != ir.NotStarted) {
			ownership.deleteTracking(ctx, context.WithoutCancel(ctx), record.DAGRun, record.AttemptKey,
				"Failed to delete superseded indexed distributed lease after reconciliation",
				"Failed to delete superseded indexed active distributed run after reconciliation",
			)
			continue
		}
		if reconciledWorkerID, ok := remoteWorkerID(reconciledStatus, workerID); ok {
			ownership.restoreConfirmedFromStatus(ctx, reconciledWorkerID, reconciledStatus, record.AttemptID)
			continue
		}

	}
}

func (h *Handler) loadClaim(
	ctx context.Context,
	runStatus *ir.DAGRunStatus,
) (*runClaim, bool) {
	attemptID, err := h.resolveAttemptIDForStatus(ctx, runStatus)
	if err != nil {
		logger.Error(ctx, "Failed to resolve distributed attempt for lease check",
			tag.DAG(runStatus.Name),
			tag.RunID(runStatus.DAGRunID),
			tag.Error(err),
		)
		return nil, false
	}

	attemptKey := dispatch.AttemptKeyForStatus(runStatus, attemptID)
	if attemptKey == "" {
		logger.Warn(ctx, "Skipping distributed lease check due to missing attempt key",
			tag.DAG(runStatus.Name),
			tag.RunID(runStatus.DAGRunID),
			tag.AttemptID(attemptID),
		)
		return nil, false
	}

	claimKey := runStatus.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = attemptKey
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, claimKey)
	switch {
	case err == nil:
	case errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
		lease = nil
	default:
		logger.Error(ctx, "Failed to read claim lease",
			tag.AttemptKey(claimKey),
			tag.Error(err),
		)
		return nil, false
	}

	return &runClaim{
		attemptID:  attemptID,
		attemptKey: attemptKey,
		lease:      lease,
	}, true
}

func (h *Handler) failLeasedRun(
	ctx context.Context,
	status *ir.DAGRunStatus,
	attemptID string,
	attemptKey string,
	reason string,
) {
	if status == nil {
		return
	}
	h.failCurrentRemoteAttempt(
		ctx,
		status.DAGRun(),
		status.Root,
		attemptID,
		attemptKey,
		reason,
		status.Status,
	)
}

func (h *Handler) resolveAttemptIDForStatus(ctx context.Context, status *ir.DAGRunStatus) (string, error) {
	if status == nil {
		return "", nil
	}
	if status.AttemptID != "" {
		return status.AttemptID, nil
	}

	storeCtx := context.WithoutCancel(ctx)
	if !status.Root.Zero() {
		attempt, err := h.dagRunRepository.FindSubAttempt(storeCtx, status.Root, status.DAGRunID)
		if err != nil {
			return "", err
		}
		return attempt.ID(), nil
	}

	attempt, err := h.dagRunRepository.FindAttempt(storeCtx, status.DAGRun())
	if err != nil {
		return "", err
	}
	return attempt.ID(), nil
}

func (h *Handler) failCurrentRemoteAttempt(
	ctx context.Context,
	dagRun ir.DAGRunRef,
	root ir.DAGRunRef,
	attemptID string,
	attemptKey string,
	reason string,
	expectedStatuses ...ir.Status,
) {
	storeCtx := context.WithoutCancel(ctx)
	runMu := h.getRunMutex(dagRun.ID)
	runMu.Lock()
	defer runMu.Unlock()

	if attemptID == "" {
		logger.Error(ctx, "Skipping distributed stale-run repair due to missing attempt ID",
			tag.DAG(dagRun.Name),
			tag.RunID(dagRun.ID),
		)
		return
	}

	h.closeCachedAttemptForRun(ctx, storeCtx, dagRun.ID, attemptID)

	mutate := func(status *ir.DAGRunStatus) error {
		finishedAt := time.Now()
		finishedAtStr := stringutil.FormatTime(finishedAt)
		status.Status = ir.Failed
		status.FinishedAt = finishedAtStr
		status.Error = reason
		for i, node := range status.Nodes {
			if node == nil {
				continue
			}
			switch node.Status {
			case ir.NodeRunning, ir.NodeNotStarted, ir.NodeRetrying, ir.NodeWaiting:
				status.Nodes[i].Status = ir.NodeFailed
				status.Nodes[i].FinishedAt = finishedAtStr
				status.Nodes[i].Error = reason
			case ir.NodeFailed, ir.NodeAborted, ir.NodeSucceeded, ir.NodeSkipped, ir.NodePartiallySucceeded, ir.NodeRejected:
				// Keep terminal node results intact when the run is failed due to lease loss.
			}
		}
		return nil
	}

	var (
		status  *ir.DAGRunStatus
		swapped bool
		err     error
	)

	for _, expectedStatus := range expectedStatuses {
		status, swapped, err = h.dagRunRepository.CompareAndSwapLatestAttemptStatus(
			storeCtx,
			dagRun,
			attemptID,
			expectedStatus,
			mutate, persis.DAGRunCompareAndSwapOptions{RootDAGRun: root, ExpectedAttemptKey: attemptKey},
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
		h.attemptOwnership().deleteTracking(ctx, storeCtx, dagRun, attemptKey,
			"Failed to delete orphaned distributed lease",
			"Failed to delete orphaned active distributed run",
		)
		return
	}
	if status.AttemptID != attemptID || (!status.Status.IsActive() && status.Status != ir.NotStarted) {
		h.attemptOwnership().deleteTracking(ctx, storeCtx, dagRun, attemptKey,
			"Failed to delete superseded distributed lease",
			"Failed to delete superseded active distributed run",
		)
		return
	}
	if !swapped {
		return
	}

	h.attemptOwnership().deleteTracking(ctx, storeCtx, dagRun, attemptKey,
		"Failed to delete stale distributed lease after failure",
		"Failed to delete active distributed run after failure",
	)
	if h.dispatchAdmissionStore != nil && attemptKey != "" {
		if err := h.dispatchAdmissionStore.FinalizeAdmissionAttempt(storeCtx, attemptKey); err != nil {
			logger.Warn(ctx, "Failed to finalize dispatch admission after stale-run failure",
				tag.AttemptKey(attemptKey),
				tag.Error(err),
			)
		}
	}

	logger.Warn(ctx, "Marked stale distributed run as FAILED",
		tag.DAG(dagRun.Name),
		tag.RunID(dagRun.ID),
		slog.String("reason", reason),
	)
}

func (h *Handler) closeCachedAttemptForRun(ctx, closeCtx context.Context, dagRunID, attemptID string) {
	h.attemptsMu.Lock()
	cachedAttempt, ok := h.openAttempts[dagRunID]
	if ok && attemptID != "" && cachedAttempt.ID() != attemptID {
		ok = false
	}
	if ok {
		delete(h.openAttempts, dagRunID)
	}
	h.attemptsMu.Unlock()

	if !ok {
		return
	}

	if err := cachedAttempt.Close(closeCtx); err != nil {
		logger.Warn(ctx, "Failed to close cached distributed attempt",
			tag.RunID(dagRunID),
			tag.AttemptID(cachedAttempt.ID()),
			tag.Error(err),
		)
	}
}

// markRunFailed is kept for compatibility with older tests and non-lease based
// cleanup paths. It marks the latest active attempt failed without requiring a
// lease record.
func (h *Handler) markRunFailed(ctx context.Context, dagName, dagRunID, reason string) {
	if h.dagRunRepository == nil {
		return
	}
	storeCtx := context.WithoutCancel(ctx)

	runMu := h.getRunMutex(dagRunID)
	runMu.Lock()
	defer runMu.Unlock()

	var attempt dagrun.Attempt
	var needsOpen bool

	h.attemptsMu.RLock()
	cachedAttempt, ok := h.openAttempts[dagRunID]
	h.attemptsMu.RUnlock()

	if ok {
		attempt = cachedAttempt
		needsOpen = false
	} else {
		ref := ir.DAGRunRef{Name: dagName, ID: dagRunID}
		foundAttempt, err := h.dagRunRepository.FindAttempt(storeCtx, ref)
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

	if !dagRunStatus.Status.IsActive() && dagRunStatus.Status != ir.NotStarted {
		return
	}

	finishedAt := stringutil.FormatTime(time.Now())
	dagRunStatus.Status = ir.Failed
	dagRunStatus.FinishedAt = finishedAt
	dagRunStatus.Error = reason

	for i, node := range dagRunStatus.Nodes {
		if node.Status == ir.NodeRunning || node.Status == ir.NodeNotStarted || node.Status == ir.NodeWaiting || node.Status == ir.NodeRetrying {
			dagRunStatus.Nodes[i].Status = ir.NodeFailed
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
	h.finalizeAdmissionForStatus(ctx, dagRunStatus, attempt.ID())

	logger.Warn(ctx, "Marked zombie run as FAILED",
		tag.DAG(dagName), tag.RunID(dagRunID), slog.String("reason", reason))
}

// markWorkerTasksFailed is kept for compatibility with tests that exercise the
// worker-heartbeat cleanup path directly.
func (h *Handler) markWorkerTasksFailed(ctx context.Context, info *heartbeatInfo) {
	if h.dagRunRepository == nil || info == nil || info.stats == nil {
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
// Parent workers use this for sub-DAG cancellation through the coordinator.
func (h *Handler) RequestCancel(ctx context.Context, req *coordinatorv1.RequestCancelRequest) (*coordinatorv1.RequestCancelResponse, error) {
	if h.dagRunRepository == nil {
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
	var attempt dagrun.Attempt
	var err error

	isSubDAG := req.RootDagRunId != "" && req.RootDagRunId != req.DagRunId
	if isSubDAG {
		rootRef := ir.DAGRunRef{Name: req.RootDagRunName, ID: req.RootDagRunId}
		attempt, err = h.dagRunRepository.FindSubAttempt(ctx, rootRef, req.DagRunId)
		logger.Info(ctx, "Looking up sub-DAG attempt for cancellation",
			slog.String("root-dag-run-id", req.RootDagRunId),
		)
	} else {
		ref := ir.DAGRunRef{Name: req.DagName, ID: req.DagRunId}
		attempt, err = h.dagRunRepository.FindAttempt(ctx, ref)
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
	h.finalizeAdmissionForAttempt(ctx, attempt)

	logger.Info(ctx, "DAG run cancellation requested successfully")
	return &coordinatorv1.RequestCancelResponse{Accepted: true}, nil
}

func finalizeNotStartedCancellation(ctx context.Context, attempt dagrun.Attempt) error {
	if attempt == nil {
		return nil
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read attempt status: %w", err)
	}
	if status == nil || status.Status != ir.NotStarted {
		return nil
	}

	finishedAt := stringutil.FormatTime(time.Now().UTC())
	status.Status = ir.Aborted
	status.FinishedAt = finishedAt
	status.Error = context.Canceled.Error()
	status.WorkerID = ""
	status.PID = 0
	status.PIDStartedAt = 0
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
