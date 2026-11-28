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
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
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

	connectionTokens bool
}

type heartbeatInfo struct {
	workerID        string
	labels          map[string]string
	stats           *coordinatorv1.WorkerStats
	lastHeartbeatAt time.Time
	version         string

	connectionTokens bool
	connection       peerConnection
	environment      coordinatorv1.HostEnvironment

	host string
	// certificateID is the worker certificate the heartbeat arrived with,
	// and certificatePass the latest pass to its record, which the
	// following heartbeats with that certificate and host share.
	certificateID   string
	certificatePass *certificatePass
}

// certificatePass is a heartbeat's pass to its certificate's record. The
// heartbeats that follow share it, so a failed pass is retried by the next
// of them, and a newer pass is another value.
type certificatePass struct {
	// recheckAt is when the next heartbeat passes again. h.mu guards it.
	recheckAt time.Time
}

// defaultStaleHeartbeatThreshold is the default duration after which a worker's heartbeat is considered stale.
const defaultStaleHeartbeatThreshold = dispatch.DefaultStaleWorkerHeartbeatThreshold

// defaultStaleLeaseThreshold is the shared default duration after which a
// distributed run's lease is considered stale.
const defaultStaleLeaseThreshold = dagrun.DefaultStaleLeaseThreshold

// Dispatch wakes every waiting poller immediately, so the poll wait is only a
// fallback for pending records that arrive without an in-process wake, such
// as reservations recycled after expiry or tasks enqueued through another
// coordinator instance. The cap bounds idle timer wakeups per poller.
const (
	defaultDispatchPollInitialWait = 250 * time.Millisecond
	defaultDispatchPollMaxWait     = 5 * time.Second
	workspaceBundleTTL             = time.Hour
	workspaceBundleCleanupInterval = time.Hour
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
	errDispatchConflict             = errors.New("dispatch conflicts with the attempt it names")
	errDispatchSuperseded           = errors.New("dispatch names an attempt that is no longer the latest of its run")
	errNoAvailableWorkers           = errors.New("no available workers")
	errNoMatchingWorkers            = errors.New("no workers match the required selector")
	errNoConnectionWorkers          = fmt.Errorf("%w: the DAG declares connections, which no matching worker supports; upgrade the matching workers", errNoMatchingWorkers)
	errRunHeartbeatRepairSkipped    = errors.New("run heartbeat repair skipped")
	errManualActionCheckpointChange = errors.New("completed manual action checkpoint changed")
)

type preparedDispatchAttempt struct {
	attempt      dagrun.Attempt
	newlyCreated bool
	// admitted is set when an earlier dispatch of the attempt reached the
	// queue or a worker, so this one has nothing to add.
	admitted bool
}

// handoffFailedPrefix starts the error of an attempt whose dispatch the
// coordinator rolled back before any worker took it. A repeat of that
// dispatch admits the attempt again.
const handoffFailedPrefix = "failed to hand off distributed task to a worker"

// dispatchAttemptIDPattern bounds a caller-assigned attempt ID to characters
// safe in a file name on every platform.
var dispatchAttemptIDPattern = regexp.MustCompile(`^[-a-zA-Z0-9_]{1,64}$`)

type runClaim struct {
	attemptID  string
	attemptKey string
	lease      *dispatch.DAGRunLease
}

// runLockSet retains a per-run lock only while callers hold or wait for it.
type runLockSet struct {
	mu      sync.Mutex
	entries map[string]*runLock
}

type runLock struct {
	mu   sync.Mutex
	refs int
}

func (s *runLockSet) lock(dagRunID string) *runLock {
	s.mu.Lock()
	if s.entries == nil {
		s.entries = make(map[string]*runLock)
	}
	entry := s.entries[dagRunID]
	if entry == nil {
		entry = &runLock{}
		s.entries[dagRunID] = entry
	}
	entry.refs++
	s.mu.Unlock()

	entry.mu.Lock()
	return entry
}

func (s *runLockSet) unlock(dagRunID string, entry *runLock) {
	entry.mu.Unlock()

	s.mu.Lock()
	entry.refs--
	if entry.refs == 0 {
		delete(s.entries, dagRunID)
	}
	s.mu.Unlock()
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
	workspaceBundleDir        string                             // Root for immutable task workspace bundles and staging
	workspaceBundleStore      *workspacebundle.Store             // For immutable task workspace bundles
	dispatchTaskStore         dispatch.DispatchTaskStore         // Shared distributed dispatch queue
	dispatchAdmissionStore    dispatch.DispatchAdmissionStore    // Shared distributed admission state
	workerHeartbeatStore      dispatch.WorkerHeartbeatStore      // Shared worker presence
	dagRunLeaseStore          dispatch.DAGRunLeaseStore          // Shared distributed run leases
	activeDistributedRunStore dispatch.ActiveDistributedRunStore // Shared active distributed attempt index
	dagRepository             *persis.DAGRepository              // DAG definitions for the GetDAG RPC
	secretStore               secretpkg.Store                    // Secret registry for workers
	profileStore              profilepkg.Store                   // Runtime profiles for workers
	connectionStore           connection.Store                   // Connections that runs on workers declare
	connectionTokens          broker.Source                      // Access tokens of those connections
	projectsEnabled           bool                               // Resolve secrets and profiles without workspace layers
	nodeInventory             inventory.Store                    // Inventory nodes for runs on workers
	trustedHostKeys           runctx.TrustedHostKeys             // Host keys trusted for inventory nodes
	agentSessionCleanupQueue  *agentsession.CleanupQueue         // Deferred provider cleanup owned by workers
	workerNodes               *workerNodeRecorder                // Records workers that set an ID as nodes
	h2cCallers                *H2CCallers                        // Hosts that called over h2c; nil lists none
	joinReadiness             JoinReadinessConfig                // Settings reported by GetJoinReadiness
	hostProbe                 hostProbe                          // Where this process runs, for GetJoinReadiness
	peerCA                    *PeerCA                            // Signs worker certificates; nil without built-in TLS
	workerInvites             persis.WorkerInviteStore           // Invites that admit enrollments
	workerCertificates        persis.WorkerCertificateStore      // Records of issued worker certificates
	removals                  workerRemovals                     // Certificates in removal, refused or given no task
	peerMaintenance           sync.WaitGroup                     // The removal reread and the daily prune
	clock                     func() time.Time                   // The time removals and certificate heartbeats use
	removalReread             time.Duration                      // How often the removals are reread

	// Open attempts cache for status persistence
	attemptsMu   sync.RWMutex
	openAttempts map[string]dagrun.Attempt // dagRunID -> open attempt

	// Serializes status writes and repairs for each DAG run.
	runLocks runLockSet

	// Serializes the admission of dispatches for each DAG run, so a repeat
	// sees what an earlier call stored.
	admissionLocks runLockSet

	// Stale heartbeat threshold - configurable
	staleHeartbeatThreshold time.Duration

	// Stale lease threshold - configurable
	staleLeaseThreshold time.Duration

	// How long a dispatch waits for a matching worker; 0 refuses at once.
	workerWaitTimeout time.Duration

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

	// ProfileStore resolves runtime profiles for workers.
	// Optional - when nil, ResolveRuntimeProfile returns FailedPrecondition.
	ProfileStore profilepkg.Store

	// ConnectionStore and ConnectionTokens answer the connection tokens of
	// runs on workers. Optional - when either is nil,
	// ResolveConnectionToken returns Unavailable.
	ConnectionStore  connection.Store
	ConnectionTokens broker.Source

	// ProjectsEnabled resolves secrets and runtime profiles in the
	// project alone: the workspace a request names adds no layer.
	ProjectsEnabled bool

	// NodeInventory finds the project's inventory nodes for runs on workers.
	// Optional - when nil, ResolveNodes and node secret refs return
	// FailedPrecondition.
	NodeInventory inventory.Store

	// TrustedHostKeys finds the host keys trusted for inventory nodes.
	// Optional - when nil, ResolveNodes answers no trusted keys.
	TrustedHostKeys runctx.TrustedHostKeys

	// AgentSessionCleanupQueue stores provider cleanup claimed by owning workers.
	AgentSessionCleanupQueue *agentsession.CleanupQueue

	// WorkerNodeStore records the workers that are nodes (RFC 012).
	// Optional - when nil, no worker node is recorded.
	WorkerNodeStore inventory.WorkerNodeStore

	// JoinReadiness holds the settings that GetJoinReadiness reports.
	JoinReadiness JoinReadinessConfig

	// H2CCallers is what GetH2CCallers lists. Optional - when nil, it lists
	// none.
	H2CCallers *H2CCallers

	// PeerCA signs the certificates Enroll issues. Without it, the invite
	// calls and Enroll fail with FailedPrecondition, and WorkerInviteStore
	// and WorkerCertificateStore are unused.
	PeerCA *PeerCA

	// WorkerInviteStore keeps the invites that admit enrollments.
	WorkerInviteStore persis.WorkerInviteStore

	// WorkerCertificateStore records the certificates Enroll issues.
	WorkerCertificateStore persis.WorkerCertificateStore

	// StaleHeartbeatThreshold is the duration after which a worker's heartbeat
	// is considered stale. Defaults to 30 seconds if not set.
	StaleHeartbeatThreshold time.Duration

	// StaleLeaseThreshold is the duration after which a distributed run's
	// lease is considered stale (worker stopped pushing status). Defaults to 90 seconds.
	StaleLeaseThreshold time.Duration

	// WorkerWaitTimeout is how long a dispatch with no healthy matching worker
	// is held, as a queued run, for such a worker to register. 0 refuses the
	// dispatch at once, which is also the default.
	WorkerWaitTimeout time.Duration

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
	h := &Handler{
		waitingPollers:            make(map[string]*workerInfo),
		heartbeats:                make(map[string]*heartbeatInfo),
		dispatchWakeCh:            make(chan struct{}),
		dispatchPollInitialWait:   defaultDispatchPollInitialWait,
		dispatchPollMaxWait:       defaultDispatchPollMaxWait,
		openAttempts:              make(map[string]dagrun.Attempt),
		owner:                     cfg.Owner,
		dagRunRepository:          cfg.DAGRunRepository,
		logDir:                    cfg.LogDir,
		artifactDir:               cfg.ArtifactDir,
		stateStore:                cfg.StateStore,
		workspaceBundleDir:        cfg.WorkspaceBundleDir,
		workspaceBundleStore:      bundleStore,
		dispatchTaskStore:         cfg.DispatchTaskStore,
		dispatchAdmissionStore:    dispatchAdmissionStore,
		workerHeartbeatStore:      cfg.WorkerHeartbeatStore,
		dagRunLeaseStore:          cfg.DAGRunLeaseStore,
		activeDistributedRunStore: cfg.ActiveDistributedRunStore,
		dagRepository:             cfg.DAGRepository,
		secretStore:               cfg.SecretStore,
		profileStore:              cfg.ProfileStore,
		connectionStore:           cfg.ConnectionStore,
		connectionTokens:          cfg.ConnectionTokens,
		projectsEnabled:           cfg.ProjectsEnabled,
		nodeInventory:             cfg.NodeInventory,
		trustedHostKeys:           cfg.TrustedHostKeys,
		agentSessionCleanupQueue:  cfg.AgentSessionCleanupQueue,
		workerNodes:               newWorkerNodeRecorder(cfg.WorkerNodeStore),
		h2cCallers:                cfg.H2CCallers,
		joinReadiness:             cfg.JoinReadiness,
		hostProbe:                 systemHostProbe(),
		peerCA:                    cfg.PeerCA,
		workerInvites:             cfg.WorkerInviteStore,
		workerCertificates:        cfg.WorkerCertificateStore,
		clock:                     time.Now,
		removalReread:             removalRereadInterval,
		staleHeartbeatThreshold:   cfg.StaleHeartbeatThreshold,
		staleLeaseThreshold:       cfg.StaleLeaseThreshold,
		workerWaitTimeout:         cfg.WorkerWaitTimeout,
		eventService:              cfg.EventService,
		eventSourceInstance:       cfg.EventSourceInstance,
	}
	h.removals.now = func() time.Time { return h.clock() }
	return h
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

			connectionTokens: req.ConnectionTokens,
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

	cert := h.callerCertificate(ctx)
	pollWait := h.dispatchPollInitialWait
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		observedGeneration, _ := h.dispatchWakeSnapshot()
		if cert != nil {
			// A revocation during the long poll ends it.
			removal, removed := h.removals.get(cert.Subject.CommonName)
			if removal.State == persis.WorkerCertificateRevoked {
				return nil, status.Error(codes.Unauthenticated, removedWorkerMessage)
			}
			// A removing worker gets no new task, so it waits as when none
			// is pending.
			if removed {
				if _, err := h.waitForDispatchPollRetry(ctx, req.PollerId, observedGeneration, h.dispatchPollMaxWait); err != nil {
					return nil, err
				}
				continue
			}
		}
		claimed, err := h.dispatchTaskStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
			WorkerID:     req.WorkerId,
			PollerID:     req.PollerId,
			Labels:       req.Labels,
			Owner:        h.owner,
			ClaimTimeout: h.staleLeaseThreshold,

			ConnectionTokens: req.ConnectionTokens,
		})
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to claim task: "+err.Error())
		}
		if claimed != nil && claimed.Task != nil && cert != nil {
			// A removal that this coordinator applied while it claimed the
			// task takes the task back before the worker sees it.
			if removal, removed := h.removals.get(cert.Subject.CommonName); removed {
				if err := h.dispatchTaskStore.ReleaseClaim(context.WithoutCancel(ctx), claimed.ClaimToken); err != nil && !errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
					logger.Warn(ctx, "Failed to return a removing worker's task to the queue", tag.WorkerID(req.WorkerId), tag.Error(err))
				}
				if removal.State == persis.WorkerCertificateRevoked {
					return nil, status.Error(codes.Unauthenticated, removedWorkerMessage)
				}
				continue
			}
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
	if req.Task.AttemptId != "" && !dispatchAttemptIDPattern.MatchString(req.Task.AttemptId) {
		return nil, status.Error(codes.InvalidArgument, "invalid task.AttemptId")
	}
	if err := h.ensureWorkspaceBundle(ctx, req.Task); err != nil {
		return nil, err
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
		// Without the shared queue a handoff leaves no record to recognize a
		// repeat by, so each call prepares an attempt of its own.
		req.Task.AttemptId = ""
		req.Task.NeedsConnectionTokens = declaresConnections(ctx, req.Task)
		if err := h.ensureWaitingWorkerAvailability(ctx, req.Task.WorkerSelector, req.Task.TargetWorkerId, req.Task.NeedsConnectionTokens); err != nil {
			return nil, status.Error(dispatchErrorCode(err), err.Error())
		}
		if err := h.prepareDispatchTaskWorkspace(ctx, req.Task); err != nil {
			return nil, err
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

		if err := h.dispatchToWaitingPoller(ctx, req.Task); err != nil {
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

	admission := h.admissionLocks.lock(req.Task.DagRunId)
	defer h.admissionLocks.unlock(req.Task.DagRunId, admission)

	healthyWorkers, err := h.listHealthyWorkers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list workers: "+err.Error())
	}
	declares := sync.OnceValue(func() bool { return declaresConnections(ctx, req.Task) })
	shortfall := workerShortfall(healthyWorkers, req.Task, declares)
	// A repeat of an admitted dispatch needs no worker now, so a shortfall
	// does not refuse it.
	if shortfall != nil && (req.Task.BoundNode != "" || !h.holdsDispatch(req.Task)) {
		admitted, err := h.admittedRepeat(ctx, req.Task, false)
		if err != nil {
			return nil, status.Error(prepareAttemptErrorCode(err), "failed to prepare attempt: "+err.Error())
		}
		if admitted {
			return &coordinatorv1.DispatchResponse{}, nil
		}
	}
	// A run bound to a worker node fails at once while its worker is offline,
	// in removal, or too old for the run's connections, rather than waiting
	// for it (RFC 013 §Matching).
	// The removal state comes first and is read from the store, since this
	// coordinator's copy may not hold another coordinator's removal yet. A
	// repeat of a dispatch admitted before the removal still goes through,
	// also one admitted on another coordinator, so the repeat is looked up
	// in the store too: the refusal ends the run.
	if req.Task.BoundNode != "" {
		removing, readErr := h.workerInRemoval(ctx, req.Task.TargetWorkerId)
		if removing || readErr != nil {
			admitted, err := h.admittedRepeat(ctx, req.Task, true)
			if err != nil {
				return nil, status.Error(prepareAttemptErrorCode(err), "failed to prepare attempt: "+err.Error())
			}
			if admitted {
				return &coordinatorv1.DispatchResponse{}, nil
			}
			if readErr != nil {
				return nil, status.Error(codes.Unavailable, "failed to read the worker's removal state; try again")
			}
			return nil, status.Errorf(codes.FailedPrecondition, "worker node %q is being removed", req.Task.BoundNode)
		}
	}
	if shortfall != nil && req.Task.BoundNode != "" {
		if errors.Is(shortfall, errNoConnectionWorkers) {
			return nil, status.Errorf(codes.FailedPrecondition, "worker node %q: %v", req.Task.BoundNode, shortfall)
		}
		return nil, status.Errorf(codes.FailedPrecondition, "worker node %q is offline", req.Task.BoundNode)
	}
	if shortfall != nil && !h.holdsDispatch(req.Task) {
		return nil, status.Error(dispatchErrorCode(shortfall), shortfall.Error())
	}
	req.Task.NeedsConnectionTokens = declares()
	if err := h.prepareDispatchTaskWorkspace(ctx, req.Task); err != nil {
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, err
	}

	prepared, err := h.prepareAttemptForDispatch(ctx, req.Task)
	if err != nil {
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, status.Error(prepareAttemptErrorCode(err), "failed to prepare attempt: "+err.Error())
	}
	if prepared != nil && prepared.admitted {
		logger.Info(ctx, "Dispatch repeats an admitted attempt",
			tag.RunID(req.Task.DagRunId),
			tag.AttemptKey(req.Task.AttemptKey),
		)
		return &coordinatorv1.DispatchResponse{}, nil
	}
	dispatchTask, err := convert.ProtoToDispatchTask(req.Task)
	if err != nil {
		h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, status.Error(codes.Internal, "failed to encode task: "+err.Error())
	}
	if shortfall != nil {
		// The run is queued before the task is stored, so a worker that
		// registers in between cannot have its first status overwritten.
		dispatchTask.HoldUntil = time.Now().Add(h.workerWaitTimeout).UTC()
		if err := h.markPreparedAttemptHeld(ctx, req.Task, prepared, shortfall); err != nil {
			h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
			h.releaseAdmissionToken(ctx, admissionToken)
			return nil, status.Error(codes.Internal, "failed to queue task for a worker: "+err.Error())
		}
	}
	if err := h.enqueueOrBindDispatchTask(ctx, admissionToken, dispatchTask); err != nil {
		h.markPreparedAttemptDispatchFailed(ctx, req.Task, prepared, err)
		h.releaseAdmissionToken(ctx, admissionToken)
		return nil, status.Error(dispatchBindErrorCode(err), "failed to enqueue task: "+err.Error())
	}
	h.notifyDispatchAvailable()
	return &coordinatorv1.DispatchResponse{}, nil
}

func (h *Handler) ensureWorkspaceBundle(ctx context.Context, task *coordinatorv1.Task) error {
	digest := task.WorkspaceBundleDigest
	if digest == "" {
		return nil
	}
	if !workspacebundle.ValidDigest(digest) {
		return status.Error(codes.InvalidArgument, "invalid workspace bundle digest")
	}
	if h.workspaceBundleStore == nil {
		return status.Error(codes.FailedPrecondition, "workspace bundle storage is not configured")
	}
	exists, err := h.workspaceBundleStore.Touch(ctx, digest)
	if err != nil {
		return status.Error(workspaceBundleTouchErrorCode(err), "failed to verify workspace bundle: "+err.Error())
	}
	if !exists {
		return status.Error(codes.NotFound, "workspace bundle is missing or corrupt")
	}
	return nil
}

func workspaceBundleTouchErrorCode(err error) codes.Code {
	switch {
	case errors.Is(err, context.Canceled):
		return codes.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return codes.DeadlineExceeded
	case errors.Is(err, dirlock.ErrLockConflict),
		errors.Is(err, dirlock.ErrNotLocked),
		errors.Is(err, dirlock.ErrLockNotHeld):
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

func (h *Handler) prepareDispatchTaskWorkspace(ctx context.Context, task *coordinatorv1.Task) error {
	if task == nil || task.WorkspaceBundleDigest != "" || strings.TrimSpace(task.Definition) == "" {
		return nil
	}

	candidate, err := loadDispatchWorkspaceDAG(ctx, task, []byte(task.Definition), "")
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid dispatch DAG definition for workspace preparation: "+err.Error())
	}
	if !runtimeexec.HasDAGFileDependencies(candidate) {
		return nil
	}
	if h.dagRepository == nil {
		return status.Errorf(codes.FailedPrecondition, "DAG repository is not configured for dependency-bearing task %q", task.Target)
	}
	if h.workspaceBundleStore == nil || strings.TrimSpace(h.workspaceBundleDir) == "" {
		return status.Error(codes.FailedPrecondition, "workspace bundle store is not configured")
	}

	authoritative, err := h.dagRepository.GetDetails(ctx, task.Target, persis.DAGLoadOptions{})
	if err != nil {
		code := codes.Internal
		if errors.Is(err, persis.ErrDAGNotFound) {
			code = codes.FailedPrecondition
		}
		return status.Error(code, fmt.Sprintf("failed to load authoritative DAG %q: %v", task.Target, err))
	}
	if !bytes.Equal(authoritative.YamlData, []byte(task.Definition)) {
		return status.Errorf(codes.FailedPrecondition, "named DAG %q changed after remote resolution; reload the DAG and retry dispatch", task.Target)
	}
	authoritativeSource := strings.TrimSpace(authoritative.SourceFile)
	if authoritativeSource == "" {
		return status.Errorf(codes.FailedPrecondition, "authoritative DAG %q does not have a source file for dependency resolution", task.Target)
	}

	dag, err := loadDispatchWorkspaceDAG(ctx, task, authoritative.YamlData, authoritativeSource)
	if err != nil {
		return status.Error(codes.FailedPrecondition, "failed to rebuild authoritative DAG for workspace preparation: "+err.Error())
	}
	desc, archivePath, err := runtimeexec.PrepareDAGWorkspaceFile(ctx, dag, h.workspaceBundleDir)
	if err != nil {
		return status.Error(codes.FailedPrecondition, "failed to prepare authoritative DAG workspace: "+err.Error())
	}
	if desc == nil {
		return nil
	}
	defer func() { _ = fileutil.Remove(archivePath) }()

	archive, err := os.Open(archivePath) //nolint:gosec // archivePath is created by PrepareDAGWorkspaceFile.
	if err != nil {
		return status.Error(codes.Internal, "failed to open prepared DAG workspace: "+err.Error())
	}
	putErr := h.workspaceBundleStore.PutReader(ctx, *desc, archive)
	closeErr := archive.Close()
	if putErr != nil {
		return status.Error(codes.Internal, "failed to store prepared DAG workspace: "+putErr.Error())
	}
	if closeErr != nil {
		return status.Error(codes.Internal, "failed to close prepared DAG workspace: "+closeErr.Error())
	}

	task.WorkspaceBundleDigest = desc.Digest
	task.WorkspaceBundleSize = desc.Size
	task.WorkspaceBundleDagPath = desc.DAGPath
	task.WorkspaceBundleOriginalRef = desc.OriginalRef
	task.WorkspaceBundleResolvedRef = desc.ResolvedRef
	return nil
}

func loadDispatchWorkspaceDAG(ctx context.Context, task *coordinatorv1.Task, definition []byte, sourceFile string) (*ir.DAG, error) {
	loadOpts := []spec.LoadOption{spec.WithName(task.Target)}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	if task.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(task.Params))
	} else if task.Operation == coordinatorv1.Operation_OPERATION_RETRY && task.PreviousStatus != nil {
		previousStatus, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
		if err != nil {
			return nil, fmt.Errorf("decode previous task status: %w", err)
		}
		if previousStatus != nil && len(previousStatus.ParamsList) > 0 {
			loadOpts = append(loadOpts, spec.WithParams(spec.QuoteRuntimeParams(previousStatus.ParamsList, nil)))
		}
	}
	if sourceFile != "" {
		return spec.LoadYAMLAt(ctx, definition, sourceFile, loadOpts...)
	}
	return spec.LoadYAML(ctx, definition, loadOpts...)
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
		(status.Status != ir.Waiting && !status.Status.IsTerminal()) {
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
	applyTaskWorkerSelector(dag, task)
	// An unlabeled legacy child still needs its parent's provenance; named workspaces are already known.
	if task.BaseConfigWorkspace != nil || (dag.BaseConfigWorkspace != nil && *dag.BaseConfigWorkspace == "") {
		dag.BaseConfigWorkspace = task.BaseConfigWorkspace
	}
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
	// A fresh start has no queue identity; an existing run keeps the queue it
	// was enqueued into, which may be an override absent from the YAML.
	if task.QueueName == "" {
		task.QueueName = dag.ProcGroup()
		if existingStatus != nil && existingStatus.ProcGroup != "" {
			task.QueueName = existingStatus.ProcGroup
		}
	}
	if findErr == nil && task.AttemptId != "" && existingAttempt.ID() == task.AttemptId {
		task.AttemptKey = generateRootAttemptKey(task)
		return h.repeatedDispatchAttempt(ctx, task, existingAttempt, existingStatus, dag)
	}
	if existingStatus != nil && existingStatus.Status == ir.Queued {
		task.AttemptId = existingAttempt.ID()
		task.AttemptKey = generateRootAttemptKey(task)
		// The scheduler's queue dispatch binds its admission in the shared
		// store, so only another caller pays for the shared-record check.
		admitted, err := h.dispatchOutstanding(ctx, task.AttemptKey, queueDispatchStatus == nil)
		if err != nil {
			return nil, err
		}
		if admitted {
			return &preparedDispatchAttempt{attempt: existingAttempt, admitted: true}, nil
		}

		if err := existingAttempt.Open(ctx); err != nil {
			return nil, fmt.Errorf("failed to open existing attempt: %w", err)
		}

		h.cacheOpenAttempt(ctx, task.DagRunId, existingAttempt)

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
	opts := persis.DAGRunCreateAttemptOptions{Retry: isRetry, AttemptID: task.AttemptId, UniqueAttemptID: true}

	attempt, err := h.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), task.DagRunId, opts)
	if errors.Is(err, dagrun.ErrAttemptIDExists) {
		// Another coordinator may have admitted the same dispatch meanwhile.
		latest, findErr := h.dagRunRepository.FindAttempt(ctx, ref)
		if findErr != nil {
			return nil, fmt.Errorf("failed to find existing attempt: %w", findErr)
		}
		if latest.ID() != task.AttemptId {
			return nil, fmt.Errorf("%w: attempt %s", errDispatchSuperseded, task.AttemptId)
		}
		latestStatus, readErr := latest.ReadStatus(ctx)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read existing attempt: %w", readErr)
		}
		task.AttemptKey = generateRootAttemptKey(task)
		return h.repeatedDispatchAttempt(ctx, task, latest, latestStatus, dag)
	}
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

	h.cacheOpenAttempt(ctx, task.DagRunId, attempt)

	logger.Info(ctx, "Created DAGRun attempt for dispatched task",
		tag.RunID(task.DagRunId),
		tag.Target(task.Target),
		tag.AttemptID(task.AttemptId),
		tag.AttemptKey(task.AttemptKey),
	)

	return &preparedDispatchAttempt{attempt: attempt, newlyCreated: true}, nil
}

// cacheOpenAttempt keeps attempt open for the run's status writes and closes
// the handle it replaces, so an attempt a newer one replaced, or another
// handle to the same attempt, does not stay open.
func (h *Handler) cacheOpenAttempt(ctx context.Context, dagRunID string, attempt dagrun.Attempt) {
	h.attemptsMu.Lock()
	previous, ok := h.openAttempts[dagRunID]
	h.openAttempts[dagRunID] = attempt
	h.attemptsMu.Unlock()
	if !ok || previous == attempt {
		return
	}
	if err := previous.Close(context.WithoutCancel(ctx)); err != nil {
		logger.Warn(ctx, "Failed to close a replaced cached attempt",
			tag.RunID(dagRunID),
			tag.AttemptID(previous.ID()),
			tag.Error(err),
		)
	}
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
	// The child of a command step with nodes runs that step alone.
	if err == nil && task.NodeStep != "" {
		dag, err = dag.NodeStepChild(task.NodeStep)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse DAG definition: %w", err)
	}
	dag.SourceFile = task.SourceFile
	applyTaskWorkerSelector(dag, task)
	if task.BaseConfigWorkspace != nil || (dag.BaseConfigWorkspace != nil && *dag.BaseConfigWorkspace == "") {
		dag.BaseConfigWorkspace = task.BaseConfigWorkspace
	}
	labels := labelsForInitialStatus(task, dag)
	task.Labels = strings.Join(labels, ",")

	attempt, err := h.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), task.DagRunId, persis.DAGRunCreateAttemptOptions{
		RootDAGRun:      rootRef,
		AttemptID:       task.AttemptId,
		UniqueAttemptID: true,
	})
	if errors.Is(err, dagrun.ErrAttemptIDExists) {
		return h.repeatedSubDispatchAttempt(ctx, task, rootRef, dag)
	}
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

	h.cacheOpenAttempt(ctx, task.DagRunId, attempt)

	logger.Info(ctx, "Created sub-DAG attempt for distributed execution",
		tag.RunID(task.DagRunId),
		tag.DAG(task.Target),
		slog.String("root-dag-run-id", task.RootDagRunId),
		tag.AttemptKey(task.AttemptKey),
	)

	return &preparedDispatchAttempt{attempt: attempt, newlyCreated: true}, nil
}

// repeatedDispatchAttempt resolves a dispatch that names an attempt an
// earlier call created: a retry after the reply to that call was lost. The
// repeat is admitted when the earlier call reached the queue or a worker,
// refused when its definition differs or the earlier call was rolled back,
// and otherwise finishes the admission the earlier call left undone.
func (h *Handler) repeatedDispatchAttempt(
	ctx context.Context,
	task *coordinatorv1.Task,
	attempt dagrun.Attempt,
	runStatus *ir.DAGRunStatus,
	dag *ir.DAG,
) (*preparedDispatchAttempt, error) {
	admitted, runStatus, err := h.repeatAdmitted(ctx, task, attempt, runStatus, dag)
	if err != nil {
		return nil, err
	}
	if admitted {
		return &preparedDispatchAttempt{attempt: attempt, admitted: true}, nil
	}
	if rolledBack(runStatus) {
		reset, err := h.resetRolledBackAttempt(ctx, task, attempt, runStatus.Name)
		if err != nil {
			return nil, err
		}
		if !reset {
			// The attempt moved on after the decision, so another admission took it.
			return &preparedDispatchAttempt{attempt: attempt, admitted: true}, nil
		}
	}

	h.attemptsMu.RLock()
	cached, ok := h.openAttempts[task.DagRunId]
	h.attemptsMu.RUnlock()
	if ok && cached.ID() == attempt.ID() {
		attempt = cached
	} else {
		if err := attempt.Open(ctx); err != nil {
			return nil, fmt.Errorf("failed to open attempt %s: %w", task.AttemptId, err)
		}
		h.cacheOpenAttempt(ctx, task.DagRunId, attempt)
	}
	logger.Info(ctx, "Finishing the admission of a repeated dispatch",
		tag.RunID(task.DagRunId),
		tag.AttemptKey(task.AttemptKey),
	)
	return &preparedDispatchAttempt{attempt: attempt, newlyCreated: true}, nil
}

// repeatedSubDispatchAttempt resolves a sub-DAG dispatch whose attempt ID an
// attempt of the child run already holds.
func (h *Handler) repeatedSubDispatchAttempt(
	ctx context.Context,
	task *coordinatorv1.Task,
	rootRef ir.DAGRunRef,
	dag *ir.DAG,
) (*preparedDispatchAttempt, error) {
	existing, err := h.dagRunRepository.FindSubAttempt(ctx, rootRef, task.DagRunId)
	if err != nil {
		return nil, fmt.Errorf("failed to find existing sub-attempt: %w", err)
	}
	if existing.ID() != task.AttemptId {
		return nil, fmt.Errorf("%w: attempt %s", errDispatchSuperseded, task.AttemptId)
	}
	existingStatus, err := existing.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read existing sub-attempt: %w", err)
	}
	task.AttemptKey = ir.GenerateAttemptKey(
		task.RootDagRunName, task.RootDagRunId,
		task.Target, task.DagRunId, task.AttemptId,
	)
	return h.repeatedDispatchAttempt(ctx, task, existing, existingStatus, dag)
}

// rolledBack reports whether the coordinator failed the attempt when it
// could not hand its dispatch to the queue.
func rolledBack(runStatus *ir.DAGRunStatus) bool {
	return runStatus.Status == ir.Failed && strings.HasPrefix(runStatus.Error, handoffFailedPrefix)
}

// errRollbackMoved stops a reset of an attempt that is no longer rolled back.
var errRollbackMoved = errors.New("attempt is no longer rolled back")

// resetRolledBackAttempt returns a rolled-back attempt to NotStarted, so its
// dispatch can be admitted again. It reports false, changing nothing, when
// the attempt is no longer rolled back, and fails as superseded when it is
// no longer the run's latest.
func (h *Handler) resetRolledBackAttempt(ctx context.Context, task *coordinatorv1.Task, attempt dagrun.Attempt, dagName string) (bool, error) {
	var root ir.DAGRunRef
	if task.ParentDagRunId != "" {
		root = ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	}
	latest, swapped, err := h.dagRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		ir.DAGRunRef{Name: dagName, ID: task.DagRunId},
		attempt.ID(),
		ir.Failed,
		func(runStatus *ir.DAGRunStatus) error {
			if !rolledBack(runStatus) {
				return errRollbackMoved
			}
			runStatus.Status = ir.NotStarted
			runStatus.Error = ""
			runStatus.FinishedAt = ""
			return nil
		},
		persis.DAGRunCompareAndSwapOptions{RootDAGRun: root},
	)
	if errors.Is(err, errRollbackMoved) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to reset attempt %s: %w", attempt.ID(), err)
	}
	if !swapped && latest != nil && latest.AttemptID != attempt.ID() {
		return false, fmt.Errorf("%w: attempt %s", errDispatchSuperseded, task.AttemptId)
	}
	return swapped, nil
}

// repeatAdmitted reports whether the earlier dispatch of the attempt that
// task repeats reached the queue or a worker, and returns the status it
// decided on. It fails when the repeat's definition differs.
func (h *Handler) repeatAdmitted(
	ctx context.Context,
	task *coordinatorv1.Task,
	attempt dagrun.Attempt,
	runStatus *ir.DAGRunStatus,
	dag *ir.DAG,
) (bool, *ir.DAGRunStatus, error) {
	stored, err := attempt.ReadDAG(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("failed to read the DAG of attempt %s: %w", task.AttemptId, err)
	}
	if stored == nil || !bytes.Equal(stored.YamlData, dag.YamlData) {
		return false, nil, fmt.Errorf("%w: attempt %s holds a different definition", errDispatchConflict, task.AttemptId)
	}
	if runStatus == nil {
		return false, nil, fmt.Errorf("attempt %s has no status", task.AttemptId)
	}
	if awaitsAdmission(runStatus) {
		outstanding, err := h.dispatchOutstanding(ctx, task.AttemptKey, true)
		if err != nil || outstanding {
			return outstanding, runStatus, err
		}
		// Another coordinator writes the run's status, or a newer attempt,
		// before it drops the task, so a read after the lookup shows a run
		// that moved on.
		latest, err := h.latestAttempt(ctx, task, dag.Name)
		if err != nil {
			return false, nil, err
		}
		if latest.ID() != attempt.ID() {
			return false, nil, fmt.Errorf("%w: attempt %s", errDispatchSuperseded, task.AttemptId)
		}
		if runStatus, err = latest.ReadStatusUncached(ctx); err != nil {
			return false, nil, fmt.Errorf("failed to read attempt %s: %w", task.AttemptId, err)
		}
	}
	return !awaitsAdmission(runStatus), runStatus, nil
}

// latestAttempt returns the newest attempt of the run that task dispatches.
func (h *Handler) latestAttempt(ctx context.Context, task *coordinatorv1.Task, dagName string) (dagrun.Attempt, error) {
	var attempt dagrun.Attempt
	var err error
	if task.ParentDagRunId != "" {
		rootRef := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
		attempt, err = h.dagRunRepository.FindSubAttempt(ctx, rootRef, task.DagRunId)
	} else {
		attempt, err = h.dagRunRepository.FindAttempt(ctx, ir.DAGRunRef{Name: dagName, ID: task.DagRunId})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find existing attempt: %w", err)
	}
	return attempt, nil
}

// awaitsAdmission reports whether no worker has taken the attempt and the
// coordinator has not failed it for any reason but a rolled-back handoff.
func awaitsAdmission(runStatus *ir.DAGRunStatus) bool {
	return runStatus.Status == ir.NotStarted || runStatus.Status == ir.Queued || rolledBack(runStatus)
}

// admittedRepeat reports whether task repeats a dispatch that was admitted,
// without preparing an attempt when it does not.
func (h *Handler) admittedRepeat(ctx context.Context, task *coordinatorv1.Task, fresh bool) (bool, error) {
	queued, err := queueDispatchStatusForTask(task)
	if err != nil {
		return false, err
	}
	if task.AttemptId == "" && queued == nil {
		return false, nil
	}
	if queued != nil && task.ParentDagRunId == "" {
		// The scheduler re-dispatches a queued run on every scan while no
		// worker can take it. Its admission is bound in the shared store, so
		// the cached index rules a repeat out before the run is looked up.
		attemptKey := ir.GenerateAttemptKey(task.Target, task.DagRunId, task.Target, task.DagRunId, queued.AttemptID)
		outstanding, err := h.dispatchOutstanding(ctx, attemptKey, fresh)
		if err != nil || !outstanding {
			return false, err
		}
	}
	held := h.runLocks.lock(task.DagRunId)
	defer h.runLocks.unlock(task.DagRunId, held)

	loadOpts := []spec.LoadOption{spec.WithName(task.Target)}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	dag, err := spec.LoadYAML(ctx, []byte(task.Definition), loadOpts...)
	if err != nil {
		return false, fmt.Errorf("failed to parse DAG definition: %w", err)
	}
	probe := &coordinatorv1.Task{
		AttemptId:        task.AttemptId,
		DagRunId:         task.DagRunId,
		RootDagRunName:   task.RootDagRunName,
		RootDagRunId:     task.RootDagRunId,
		ParentDagRunName: task.ParentDagRunName,
		ParentDagRunId:   task.ParentDagRunId,
	}
	var attempt dagrun.Attempt
	switch {
	case task.ParentDagRunId != "":
		if task.NodeStep != "" {
			if dag, err = dag.NodeStepChild(task.NodeStep); err != nil {
				return false, fmt.Errorf("failed to parse DAG definition: %w", err)
			}
		}
		rootRef := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
		attempt, err = h.dagRunRepository.FindSubAttempt(ctx, rootRef, task.DagRunId)
		probe.AttemptKey = ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, task.Target, task.DagRunId, task.AttemptId)
	case task.RootDagRunId == "" || task.RootDagRunId == task.DagRunId:
		// A queue dispatch runs the queued attempt, not the ID the client named.
		if queued != nil {
			probe.AttemptId = queued.AttemptID
		}
		attempt, err = h.dagRunRepository.FindAttempt(ctx, ir.DAGRunRef{Name: dag.Name, ID: task.DagRunId})
		probe.AttemptKey = ir.GenerateAttemptKey(task.Target, task.DagRunId, task.Target, task.DagRunId, probe.AttemptId)
	default:
		return false, nil
	}
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to find existing attempt: %w", err)
	}
	if probe.AttemptId == "" || attempt.ID() != probe.AttemptId {
		return false, nil
	}
	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read existing attempt: %w", err)
	}
	if queued != nil && task.ParentDagRunId == "" {
		// The queued attempt keeps the definition it was queued with, and its
		// task was found outstanding above.
		return runStatus != nil && runStatus.Status == ir.Queued, nil
	}
	admitted, _, err := h.repeatAdmitted(ctx, probe, attempt, runStatus, dag)
	return admitted, err
}

// dispatchOutstanding reports whether the attempt's task waits in the queue,
// is claimed, or runs under a lease. A fresh check also sees tasks that
// other coordinators queued since this one last read the shared records.
func (h *Handler) dispatchOutstanding(ctx context.Context, attemptKey string, fresh bool) (bool, error) {
	if h.dispatchTaskStore == nil {
		return false, nil
	}
	var outstanding bool
	var err error
	if fresh {
		outstanding, err = h.dispatchTaskStore.HasOutstandingAttemptUncached(ctx, attemptKey)
	} else {
		outstanding, err = h.dispatchTaskStore.HasOutstandingAttempt(ctx, attemptKey, 0)
	}
	if err != nil || outstanding || h.dagRunLeaseStore == nil {
		return outstanding, err
	}
	if _, err := h.dagRunLeaseStore.Get(ctx, attemptKey); err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// writeInitialStatus writes an initial NotStarted status to the attempt.
// This ensures the status file is not empty when read before the worker reports its first status.
func (h *Handler) writeInitialStatus(ctx context.Context, attempt dagrun.Attempt, task *coordinatorv1.Task, dagName string, root ir.DAGRunRef, labels []string) error {
	nodeList, err := retriedNodeList(task)
	if err != nil {
		return err
	}
	initialStatus := ir.DAGRunStatus{
		Name:             dagName,
		DAGRunID:         task.DagRunId,
		AttemptID:        attempt.ID(),
		AttemptKey:       task.AttemptKey,
		Status:           ir.NotStarted,
		StartedAt:        stringutil.FormatTime(time.Now().UTC()),
		Root:             root,
		Labels:           labels,
		TriggerActor:     task.TriggerActor,
		TriggerType:      ir.ParseTriggerType(task.TriggerType),
		ScheduleTime:     task.ScheduleTime,
		IntervalStart:    task.IntervalStart,
		NextScheduledAt:  task.NextScheduledAt,
		ScheduleTimezone: task.ScheduleTimezone,
		DefinitionID:     task.DefinitionId,
		ProfileName:      task.ProfileName,
		TargetNodes:      task.TargetNodes,
		NodeList:         nodeList,
		// Only a run bound to a worker node names its node in the task.
		BoundWorker: task.BoundNode,
		// Every step's model usage is recorded from the start.
		ModelUsageRecorded: true,
	}
	return attempt.Write(ctx, initialStatus)
}

// retriedNodeList returns the record of the node list that a retry of a
// narrowed run continues, so an attempt lost before its agent writes a
// status keeps it.
func retriedNodeList(task *coordinatorv1.Task) (*ir.NodeListRecord, error) {
	if len(task.TargetNodes) == 0 || task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return nil, nil
	}
	previous, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return nil, fmt.Errorf("decode previous task status: %w", err)
	}
	if previous == nil {
		return nil, nil
	}
	return previous.NodeList, nil
}

// applyTaskWorkerSelector records the selector the task was dispatched with in
// the attempt's DAG snapshot, so a retry, a reschedule, or a queued re-dispatch
// routes the way the run was routed, not the way its YAML would.
func applyTaskWorkerSelector(dag *ir.DAG, task *coordinatorv1.Task) {
	if len(task.WorkerSelector) == 0 {
		return
	}
	dag.WorkerSelector = maps.Clone(task.WorkerSelector)
	dag.ForceLocal = false
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

	held := h.runLocks.lock(task.DagRunId)
	defer h.runLocks.unlock(task.DagRunId, held)

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

func (h *Handler) ensureWaitingWorkerAvailability(ctx context.Context, selector map[string]string, targetWorkerID string, needsConnectionTokens bool) error {
	h.mu.Lock()
	for _, worker := range h.waitingPollers {
		if targetWorkerID != "" && worker.workerID != targetWorkerID {
			continue
		}
		if !ir.MatchWorkerSelector(worker.labels, selector) {
			continue
		}
		if needsConnectionTokens && !worker.connectionTokens {
			continue
		}
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	return h.waitingWorkerUnavailable(ctx, selector, targetWorkerID, needsConnectionTokens)
}

// Waiting pollers describe immediate capacity, not worker presence. Consult
// fresh heartbeats before diagnosing a selector or target mismatch.
func (h *Handler) waitingWorkerUnavailable(ctx context.Context, selector map[string]string, targetWorkerID string, needsConnectionTokens bool) error {
	if len(selector) == 0 && targetWorkerID == "" && !needsConnectionTokens {
		return errNoAvailableWorkers
	}
	healthyWorkers, err := h.listHealthyWorkers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list workers: %w", err)
	}
	if !anyWorkerMatches(healthyWorkers, selector, targetWorkerID, false) {
		if len(selector) == 0 && targetWorkerID == "" {
			return errNoAvailableWorkers
		}
		return errNoMatchingWorkers
	}
	if !anyWorkerMatches(healthyWorkers, selector, targetWorkerID, needsConnectionTokens) {
		return errNoConnectionWorkers
	}
	return errNoAvailableWorkers
}

func (h *Handler) dispatchToWaitingPoller(ctx context.Context, task *coordinatorv1.Task) error {
	h.mu.Lock()

	matched := false
	for pollerID, worker := range h.waitingPollers {
		if task.TargetWorkerId != "" && worker.workerID != task.TargetWorkerId {
			continue
		}
		if !ir.MatchWorkerSelector(worker.labels, task.WorkerSelector) {
			continue
		}
		if task.NeedsConnectionTokens && !worker.connectionTokens {
			continue
		}
		matched = true
		select {
		case worker.taskChan <- task:
			delete(h.waitingPollers, pollerID)
			h.mu.Unlock()
			return nil
		default:
			delete(h.waitingPollers, pollerID)
		}
	}
	h.mu.Unlock()
	if matched {
		return errNoAvailableWorkers
	}
	return h.waitingWorkerUnavailable(ctx, task.WorkerSelector, task.TargetWorkerId, task.NeedsConnectionTokens)
}

func dispatchErrorCode(err error) codes.Code {
	var staleErr *queue.StaleQueueDispatchError
	switch {
	case errors.Is(err, errNoMatchingWorkers):
		return codes.FailedPrecondition
	case errors.As(err, &staleErr):
		return codes.FailedPrecondition
	case errors.Is(err, errNoAvailableWorkers):
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

func prepareAttemptErrorCode(err error) codes.Code {
	if _, ok := errors.AsType[*queue.StaleQueueDispatchError](err); ok {
		return codes.FailedPrecondition
	}
	if errors.Is(err, errDispatchConflict) || errors.Is(err, errDispatchSuperseded) {
		return codes.FailedPrecondition
	}
	return codes.Internal
}

func (h *Handler) markPreparedAttemptDispatchFailed(ctx context.Context, task *coordinatorv1.Task, prepared *preparedDispatchAttempt, dispatchErr error) {
	if prepared == nil || prepared.attempt == nil {
		return
	}
	dagRunID := task.GetDagRunId()
	held := h.runLocks.lock(dagRunID)
	defer h.runLocks.unlock(dagRunID, held)
	defer h.releasePreparedDispatchAttempt(context.WithoutCancel(ctx), dagRunID, prepared.attempt)

	if !prepared.newlyCreated {
		return
	}

	storeCtx := context.WithoutCancel(ctx)
	runStatus, err := prepared.attempt.ReadStatusUncached(storeCtx)
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
	// A repeat on another coordinator may have queued the same attempt
	// meanwhile; failing it then would fail a run that is admitted.
	outstanding, err := h.dispatchOutstanding(storeCtx, task.AttemptKey, true)
	if err != nil {
		logger.Warn(ctx, "Failed to check for another admission before failing the attempt",
			tag.RunID(task.DagRunId),
			tag.Error(err),
		)
	}
	if outstanding {
		return
	}

	runStatus.Status = ir.Failed
	runStatus.FinishedAt = stringutil.FormatTime(time.Now())
	runStatus.Error = fmt.Sprintf("%s: %v", handoffFailedPrefix, dispatchErr)
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

// workerShortfall reports why no healthy worker can take the task now:
// errNoAvailableWorkers when there is none at all, errNoMatchingWorkers when
// none carries the task's selector or target, errNoConnectionWorkers when
// none of those asks for connection tokens and declares reports that the
// task needs them, nil when one can take it. declares is called only in the
// last case, so a dispatch that the other two refuse loads no definition.
func workerShortfall(healthyWorkers []dispatch.WorkerHeartbeatRecord, task *coordinatorv1.Task, declares func() bool) error {
	if len(healthyWorkers) == 0 {
		return errNoAvailableWorkers
	}
	if !anyWorkerMatches(healthyWorkers, task.WorkerSelector, task.TargetWorkerId, false) {
		return errNoMatchingWorkers
	}
	if !anyWorkerMatches(healthyWorkers, task.WorkerSelector, task.TargetWorkerId, true) && declares() {
		return errNoConnectionWorkers
	}
	return nil
}

// holdsDispatch reports whether a task with no healthy matching worker is
// kept for one to register rather than refused. The scheduler's re-dispatch
// of a queued run is always refused: the queue already holds that run and
// reads the refusal to keep it queued.
func (h *Handler) holdsDispatch(task *coordinatorv1.Task) bool {
	if h.workerWaitTimeout <= 0 {
		return false
	}
	queued, err := queueDispatchStatusForTask(task)
	return err == nil && queued == nil
}

// markPreparedAttemptHeld records that the run is waiting for a worker: the
// attempt becomes Queued with the conditions the scheduler's queue writes for
// the same situation, so the run reads the same however it was dispatched.
func (h *Handler) markPreparedAttemptHeld(ctx context.Context, task *coordinatorv1.Task, prepared *preparedDispatchAttempt, shortfall error) error {
	if prepared == nil || prepared.attempt == nil {
		return nil
	}
	dagRunID := task.GetDagRunId()
	held := h.runLocks.lock(dagRunID)
	defer h.runLocks.unlock(dagRunID, held)

	runStatus, err := prepared.attempt.ReadStatusUncached(ctx)
	if err != nil {
		return fmt.Errorf("read prepared attempt: %w", err)
	}
	if runStatus == nil {
		return nil
	}
	now := time.Now()
	switch runStatus.Status { //nolint:exhaustive // only a run that has not started can wait for a worker
	case ir.NotStarted:
		runStatus.Status = ir.Queued
		runStatus.QueuedAt = stringutil.FormatTime(now)
	case ir.Queued:
	default:
		return nil
	}
	var conditions []ir.DAGRunCondition
	switch {
	case errors.Is(shortfall, errNoConnectionWorkers):
		conditions = ir.NoConnectionWorkerConditions(now)
	case errors.Is(shortfall, errNoAvailableWorkers):
		conditions = ir.NoAvailableWorkerConditions(now)
	default:
		conditions = ir.NoMatchingWorkerConditions(now)
	}
	runStatus.Conditions = ir.MergeDAGRunConditions(runStatus.Conditions, conditions...)
	if err := prepared.attempt.Write(ctx, *runStatus); err != nil {
		return fmt.Errorf("write queued status: %w", err)
	}
	logger.Info(ctx, "Holding dispatched task until a matching worker registers",
		tag.RunID(dagRunID),
		tag.AttemptKey(task.AttemptKey),
		slog.Any("worker-selector", task.WorkerSelector),
		slog.Duration("wait", h.workerWaitTimeout),
	)
	return nil
}

// expireHeldDispatches fails the runs of held tasks whose wait has passed.
// It runs with the zombie detector, so the failure follows the deadline by
// at most one detection interval.
func (h *Handler) expireHeldDispatches(ctx context.Context, now time.Time) {
	if h.dispatchTaskStore == nil || h.dagRunRepository == nil {
		return
	}
	expired, err := h.dispatchTaskStore.ListExpiredHeld(ctx, now)
	if err != nil {
		logger.Warn(ctx, "Failed to list held dispatches past their wait", tag.Error(err))
		return
	}
	for _, task := range expired {
		h.failHeldDispatch(ctx, task, now)
	}
}

// failHeldDispatch fails the run of one held task that no worker claimed in
// time, then drops the task. A run that is no longer queued, was claimed after
// all, or no longer exists only loses the stale task. A run that cannot be read
// keeps its task, so the next pass tries again instead of leaving the run
// queued with nothing behind it.
func (h *Handler) failHeldDispatch(ctx context.Context, task *dispatch.DispatchTask, now time.Time) {
	if task == nil {
		return
	}
	held := h.runLocks.lock(task.DAGRunID)
	defer h.runLocks.unlock(task.DAGRunID, held)

	storeCtx := context.WithoutCancel(ctx)
	rootRef := ir.DAGRunRef{Name: task.RootDAGRunName, ID: task.RootDAGRunID}
	attempt, runStatus, err := h.resolveLatestAttempt(storeCtx, task.Target, task.DAGRunID, rootRef)
	switch {
	case errors.Is(err, dagrun.ErrDAGRunIDNotFound), errors.Is(err, dagrun.ErrNoStatusData):
		// Nothing is left to fail; only the task remains.
	case err != nil:
		logger.Warn(ctx, "Failed to read the run of a held dispatch past its wait",
			tag.RunID(task.DAGRunID), tag.Error(err))
		return
	case runStatus != nil && runStatus.Status == ir.Queued && runStatus.WorkerID == "" &&
		(task.AttemptID == "" || runStatus.AttemptID == task.AttemptID):
		// runStatus may be the cached status other readers share.
		failed := *runStatus
		failed.Status = ir.Failed
		failed.FinishedAt = stringutil.FormatTime(now)
		failed.Error = heldDispatchFailure(task, h.workerWaitTimeout)
		if writeErr := h.writeHeldAttemptStatus(storeCtx, task.DAGRunID, attempt, failed); writeErr != nil {
			logger.Warn(ctx, "Failed to fail the run of a held dispatch past its wait",
				tag.RunID(task.DAGRunID), tag.Error(writeErr))
			return
		}
		h.finalizeAdmissionForStatus(storeCtx, &failed, attempt.ID())
		logger.Warn(ctx, "No worker took a held dispatch within its wait; the run failed",
			tag.RunID(task.DAGRunID),
			tag.AttemptKey(task.AttemptKey),
			slog.Any("worker-selector", task.WorkerSelector),
			slog.Duration("wait", h.workerWaitTimeout),
		)
	}
	if err := h.dispatchTaskStore.DeletePending(storeCtx, task.AttemptKey); err != nil {
		logger.Warn(ctx, "Failed to drop a held dispatch past its wait",
			tag.RunID(task.DAGRunID), tag.AttemptKey(task.AttemptKey), tag.Error(err))
	}
}

// dropOrphanedDispatches removes the pending tasks that have waited a lease
// period for runs that no longer await them, such as a run dequeued before any
// worker claimed it. A task whose run still waits keeps its place however long
// the matching workers stay busy.
func (h *Handler) dropOrphanedDispatches(ctx context.Context, now time.Time) {
	if h.dispatchTaskStore == nil || h.dagRunRepository == nil {
		return
	}
	tasks, err := h.dispatchTaskStore.ListStalePending(ctx, now.Add(-h.staleLeaseThreshold))
	if err != nil {
		logger.Warn(ctx, "Failed to list long-pending dispatches", tag.Error(err))
		return
	}
	storeCtx := context.WithoutCancel(ctx)
	for _, task := range tasks {
		if !h.dispatchOrphaned(storeCtx, task) {
			continue
		}
		if err := h.dispatchTaskStore.DeletePending(storeCtx, task.AttemptKey); err != nil {
			logger.Warn(ctx, "Failed to drop a pending dispatch whose run moved on",
				tag.RunID(task.DAGRunID), tag.AttemptKey(task.AttemptKey), tag.Error(err))
			continue
		}
		logger.Info(ctx, "Dropped a pending dispatch whose run moved on",
			tag.RunID(task.DAGRunID), tag.AttemptKey(task.AttemptKey))
	}
}

// dispatchOrphaned reports whether the run of a pending task is gone or no
// longer waits for the attempt the task starts. A run that cannot be read
// keeps its task, so the next pass checks it again.
func (h *Handler) dispatchOrphaned(ctx context.Context, task *dispatch.DispatchTask) bool {
	rootRef := ir.DAGRunRef{Name: task.RootDAGRunName, ID: task.RootDAGRunID}
	_, runStatus, err := h.resolveLatestAttempt(ctx, task.Target, task.DAGRunID, rootRef)
	switch {
	case errors.Is(err, dagrun.ErrDAGRunIDNotFound), errors.Is(err, dagrun.ErrNoStatusData):
		return true
	case err != nil:
		logger.Warn(ctx, "Failed to read the run of a long-pending dispatch",
			tag.RunID(task.DAGRunID), tag.Error(err))
		return false
	}
	if task.AttemptID != "" && runStatus.AttemptID != task.AttemptID {
		return true
	}
	return runStatus.Status != ir.Queued && runStatus.Status != ir.NotStarted
}

// claimedAttemptMovedOn reports whether the run of a claimed task has a newer
// attempt or has finished the claimed one. A run that cannot be read does not
// count as moved on.
func (h *Handler) claimedAttemptMovedOn(ctx context.Context, task *coordinatorv1.Task) bool {
	rootRef := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	attempt, runStatus, err := h.resolveLatestAttempt(ctx, task.Target, task.DagRunId, rootRef)
	if err != nil || runStatus == nil {
		return false
	}
	if task.AttemptId != "" && attempt.ID() != task.AttemptId {
		return true
	}
	return runStatus.Status.IsTerminal()
}

// writeHeldAttemptStatus writes through the attempt handle kept open since the
// dispatch when there is one, so no two handles write the same run; otherwise
// it opens the attempt for the one write.
func (h *Handler) writeHeldAttemptStatus(ctx context.Context, dagRunID string, attempt dagrun.Attempt, runStatus ir.DAGRunStatus) error {
	h.attemptsMu.Lock()
	cached, ok := h.openAttempts[dagRunID]
	if ok && cached.ID() == attempt.ID() {
		delete(h.openAttempts, dagRunID)
	} else {
		cached = nil
	}
	h.attemptsMu.Unlock()
	if cached != nil {
		defer func() { _ = cached.Close(ctx) }()
		return cached.Write(ctx, runStatus)
	}
	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("open attempt: %w", err)
	}
	defer func() { _ = attempt.Close(ctx) }()
	return attempt.Write(ctx, runStatus)
}

// heldDispatchFailure is the run error for a held dispatch no worker took.
func heldDispatchFailure(task *dispatch.DispatchTask, wait time.Duration) string {
	var reason string
	switch {
	case len(task.WorkerSelector) > 0:
		reason = fmt.Sprintf("no worker matched the selector %s within %s", ir.FormatWorkerSelector(task.WorkerSelector), wait)
	case task.TargetWorkerID != "":
		reason = fmt.Sprintf("worker %s did not register within %s", task.TargetWorkerID, wait)
	default:
		reason = fmt.Sprintf("no worker became available within %s", wait)
	}
	if task.NeedsConnectionTokens {
		reason += "; the DAG declares connections, so the matching workers must support them: upgrade older ones"
	}
	return reason
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
				Version:         hb.version,
				Connection:      convert.WorkerConnectionToProto(hb.connection.kind),
				PlaintextReason: hb.connection.plaintextReason,
				Environment:     hb.environment,
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
			Version:         hb.Version,
			Connection:      convert.WorkerConnectionToProto(hb.Connection),
			PlaintextReason: hb.PlaintextReason,
			Environment:     convert.HostEnvironmentToProto(hb.Environment),
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

// GetH2CCallers lists the hosts, other than workers, that called the
// coordinator over h2c in the last 10 minutes.
func (h *Handler) GetH2CCallers(_ context.Context, _ *coordinatorv1.GetH2CCallersRequest) (*coordinatorv1.GetH2CCallersResponse, error) {
	if h.h2cCallers == nil {
		return &coordinatorv1.GetH2CCallersResponse{}, nil
	}
	return &coordinatorv1.GetH2CCallersResponse{Callers: h.h2cCallers.list()}, nil
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
	receivedAt := h.clock().UTC()
	cert := h.callerCertificate(ctx)
	connection := callerConnection(ctx, cert)
	info := &heartbeatInfo{
		workerID:        req.WorkerId,
		labels:          req.Labels,
		stats:           req.Stats,
		lastHeartbeatAt: receivedAt,
		version:         req.Version,

		connectionTokens: req.ConnectionTokens,
		connection:       connection,
		environment:      req.Environment,

		host: req.Host,
	}
	var pass *certificatePass
	h.mu.Lock()
	if cert != nil {
		info.certificateID = cert.Subject.CommonName
		prev := h.heartbeats[req.WorkerId]
		if prev != nil && prev.certificateID == info.certificateID && prev.host == req.Host &&
			prev.certificatePass != nil && receivedAt.Before(prev.certificatePass.recheckAt) {
			info.certificatePass = prev.certificatePass
		} else {
			pass = &certificatePass{recheckAt: receivedAt.Add(certificateHeartbeatPass)}
			info.certificatePass = pass
		}
	}
	h.heartbeats[req.WorkerId] = info
	h.mu.Unlock()

	if h.workerHeartbeatStore != nil {
		if err := h.workerHeartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        req.WorkerId,
			Labels:          req.Labels,
			Stats:           convert.ProtoToWorkerStats(req.Stats),
			LastHeartbeatAt: receivedAt.UnixMilli(),
			Host:            req.Host,
			Machine:         convert.ProtoToMachineSample(req.Machine),
			Version:         req.Version,

			ConnectionTokens: req.ConnectionTokens,
			Connection:       connection.kind,
			PlaintextReason:  connection.plaintextReason,
			Environment:      convert.ProtoToHostEnvironment(req.Environment),
			CertificateID:    info.certificateID,
		}); err != nil {
			if pass != nil {
				// The pass this heartbeat reserved did not happen, so the
				// next heartbeat that shares it makes it.
				h.mu.Lock()
				pass.recheckAt = time.Time{}
				h.mu.Unlock()
			}
			return nil, status.Error(codes.Internal, "failed to persist worker heartbeat: "+err.Error())
		}
	}
	if cert != nil {
		h.observeCertificateHeartbeat(ctx, cert, req, receivedAt, pass)
	}
	h.workerNodes.observe(ctx, req.WorkerId, req.Host, req.Labels, receivedAt)
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
	if resp, err := h.refuseRemovingAcceptance(ctx, req.WorkerId, req.ClaimToken); resp != nil || err != nil {
		return resp, err
	}

	claimed, err := h.dispatchTaskStore.GetClaim(ctx, req.ClaimToken)
	if err != nil {
		if errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
			return h.ackMissingClaim(ctx, req.AttemptKey, req.WorkerId, req.ClaimToken)
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
	// A duplicate task of an attempt that finished or was replaced must not
	// start it again under that attempt's lease.
	if h.claimedAttemptMovedOn(ctx, task) {
		if err := h.dispatchTaskStore.DeleteClaim(ctx, req.ClaimToken); err != nil {
			return nil, status.Error(codes.Internal, "failed to drop task claim: "+err.Error())
		}
		return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "attempt already finished or superseded"}, nil
	}
	var certificateID string
	if cert := h.callerCertificate(ctx); cert != nil {
		certificateID = cert.Subject.CommonName
	}
	// The mark keeps the claim's expiry from returning the task to the
	// queue while this call writes the lease.
	if err := h.dispatchTaskStore.MarkClaimLeasing(ctx, req.ClaimToken); err != nil {
		if errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
			// An overlapping acknowledgement of the claim may have
			// finished since the claim was read.
			return h.ackMissingClaim(ctx, task.AttemptKey, workerID, req.ClaimToken)
		}
		return nil, status.Error(codes.Internal, "failed to mark task claim: "+err.Error())
	}
	if err := h.attemptOwnership().recordTaskClaim(ctx, task, workerID, certificateID); err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseConflict) {
			return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "attempt claim conflicts with the active lease"}, nil
		}
		return nil, status.Error(codes.Internal, "failed to create run lease: "+err.Error())
	}
	if err := h.settleAcceptedRun(ctx); err != nil {
		// The lease is the stale-lease check's to end now, so the claim
		// goes and its expiry never queues the attempt again.
		if status.Code(err) != codes.Unavailable {
			h.dropClaim(ctx, req.ClaimToken)
		}
		return nil, err
	}
	// A refusal of the same claim on another call may have dropped the
	// claim already; the lease this call wrote stands.
	if err := h.dispatchTaskStore.DeleteClaim(ctx, req.ClaimToken); err != nil && !errors.Is(err, dispatch.ErrDispatchTaskNotFound) {
		return nil, status.Error(codes.Internal, "failed to finalize task claim: "+err.Error())
	}

	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

// ackMissingClaim answers an acknowledgement of the claim claimToken that
// is gone: accepted when the lease of attemptKey records that claim for
// workerID, as after an acknowledgement that finished first, unless the
// call's certificate was revoked meanwhile, as an acknowledgement that then
// refused the claim found.
func (h *Handler) ackMissingClaim(ctx context.Context, attemptKey, workerID, claimToken string) (*coordinatorv1.AckTaskClaimResponse, error) {
	if attemptKey != "" && workerID != "" {
		lease, err := h.dagRunLeaseStore.Get(ctx, attemptKey)
		if err == nil && lease.MatchesClaim(attemptKey, workerID) && lease.ClaimToken == claimToken {
			if cert := h.callerCertificate(ctx); cert != nil {
				record, err := h.workerCertificates.Get(ctx, cert.Subject.CommonName)
				switch {
				case err == nil && record.State == persis.WorkerCertificateRevoked:
					h.removals.apply(removalOf(record))
					return nil, status.Error(codes.Unauthenticated, removedWorkerMessage)
				case err != nil && !errors.Is(err, persis.ErrWorkerCertificateNotFound):
					// The worker retries an unavailable acceptance.
					return nil, status.Error(codes.Unavailable, "failed to read the worker's certificate; try again")
				}
			}
			return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
		}
		if err != nil && !errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
			return nil, status.Error(codes.Internal, "failed to load acknowledged claim: "+err.Error())
		}
	}
	return &coordinatorv1.AckTaskClaimResponse{Accepted: false, Error: "claim not found or expired"}, nil
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
		if err := h.refreshRunLeaseAndRepair(ctx, req.WorkerId, task, observedAt); err != nil {
			if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) || errors.Is(err, persis.ErrCorrupt) {
				cancelledRuns = appendCancelledRunIfMissing(cancelledRuns, task.AttemptKey)
				continue
			}
			return nil, status.Error(codes.Internal, "failed to refresh run lease: "+err.Error())
		}
	}

	cancelledRuns = appendCancelledRuns(cancelledRuns, h.getCancelledRunsForWorker(ctx, &coordinatorv1.WorkerStats{
		RunningTasks: req.RunningTasks,
	}))
	return &coordinatorv1.RunHeartbeatResponse{CancelledRuns: cancelledRuns}, nil
}

// refreshRunLeaseAndRepair records a run heartbeat and undoes a stale-run
// failure that was recorded while the lease was still fresh. It holds the run
// lock that the zombie detector holds while it fails a run and deletes its
// lease, so a heartbeat sees either the leased run or the failed run, never
// the failed run still leased.
func (h *Handler) refreshRunLeaseAndRepair(
	ctx context.Context,
	workerID string,
	task *coordinatorv1.RunningTask,
	observedAt time.Time,
) error {
	held := h.runLocks.lock(task.DagRunId)
	defer h.runLocks.unlock(task.DagRunId, held)

	leaseWasFresh, err := h.refreshRunLease(ctx, task, observedAt)
	if err != nil {
		return err
	}
	// A lease that expired before this heartbeat justifies a stale-run
	// failure, so the failure stands and the worker is told to stop.
	if leaseWasFresh {
		h.repairStaleLeaseFailureFromRunHeartbeat(ctx, workerID, task, observedAt)
	}
	return nil
}

// refreshRunLease records a run heartbeat on the lease and reports whether
// the lease was still fresh when the heartbeat arrived. An expired lease of a
// run that has already ended stays expired, so a later heartbeat cannot
// treat it as fresh.
func (h *Handler) refreshRunLease(ctx context.Context, task *coordinatorv1.RunningTask, observedAt time.Time) (bool, error) {
	lease, err := h.dagRunLeaseStore.Get(ctx, task.AttemptKey)
	if err != nil {
		return false, err
	}
	fresh := lease != nil && lease.IsFresh(observedAt, h.staleLeaseThreshold)
	if !fresh {
		_, runStatus, err := h.resolveLatestAttemptForRunningTask(ctx, task)
		if err == nil && runStatus != nil && runStatus.Status.IsTerminal() {
			return false, nil
		}
	}
	if lease != nil && observedAt.Before(lease.LastHeartbeatTime().Add(h.leaseRefreshWriteInterval())) {
		return fresh, nil
	}
	return fresh, h.dagRunLeaseStore.Touch(ctx, task.AttemptKey, observedAt)
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
	var records []dispatch.WorkerHeartbeatRecord
	if h.workerHeartbeatStore == nil {
		h.mu.Lock()
		for _, hb := range h.heartbeats {
			records = append(records, dispatch.WorkerHeartbeatRecord{
				WorkerID:        hb.workerID,
				Labels:          hb.labels,
				LastHeartbeatAt: hb.lastHeartbeatAt.UnixMilli(),

				ConnectionTokens: hb.connectionTokens,
			})
		}
		h.mu.Unlock()
	} else {
		var err error
		records, err = h.workerHeartbeatStore.List(ctx)
		if err != nil {
			return nil, err
		}
	}

	now := h.clock().UTC()
	healthy := make([]dispatch.WorkerHeartbeatRecord, 0, len(records))
	for _, record := range records {
		if !dispatch.WorkerHeartbeatFresh(record, now, h.staleHeartbeatThreshold) {
			continue
		}
		// A worker in removal takes no new task, so it cannot take a
		// dispatch either.
		if _, removed := h.removals.get(record.CertificateID); record.CertificateID != "" && removed {
			continue
		}
		healthy = append(healthy, record)
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

	held := h.runLocks.lock(task.DagRunId)
	defer h.runLocks.unlock(task.DagRunId, held)

	attempt, err := h.openRunningAttemptLocked(ctx, task)
	if err != nil {
		logger.Warn(ctx, "Failed to resolve running task for lease refresh",
			tag.RunID(task.DagRunId),
			tag.WorkerID(workerID),
			tag.Error(err),
		)
		return
	}

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

	// Every heartbeat reads the status, so the read stays cached and the
	// change goes to a copy.
	refreshed := *runStatus
	refreshed.LeaseAt = observedAt.UnixMilli()
	if err := attempt.Write(ctx, refreshed); err != nil {
		logger.Warn(ctx, "Failed to persist heartbeat lease refresh",
			tag.RunID(task.DagRunId),
			tag.WorkerID(workerID),
			tag.Error(err),
		)
	}
}

func (h *Handler) openRunningAttemptLocked(ctx context.Context, task *coordinatorv1.RunningTask) (dagrun.Attempt, error) {
	if task == nil {
		return nil, fmt.Errorf("running task is nil")
	}

	isSubDAG := task.RootDagRunId != "" && task.RootDagRunId != task.DagRunId
	if isSubDAG {
		if task.RootDagRunName == "" {
			return nil, fmt.Errorf("missing root dag run name for sub-dag %s", task.DagRunId)
		}
		return h.getOrOpenSubLocked(ctx, ir.DAGRunRef{
			Name: task.RootDagRunName,
			ID:   task.RootDagRunId,
		}, task.DagRunId)
	}

	return h.getOrOpenRootLocked(ctx, task.DagName, task.DagRunId)
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

// anyWorkerMatches reports whether a worker matches selector and
// targetWorkerID, and asks for connection tokens when
// needsConnectionTokens is set.
func anyWorkerMatches(workers []dispatch.WorkerHeartbeatRecord, selector map[string]string, targetWorkerID string, needsConnectionTokens bool) bool {
	for _, worker := range workers {
		if needsConnectionTokens && !worker.ConnectionTokens {
			continue
		}
		if dispatch.WorkerHeartbeatMatches(worker, selector, targetWorkerID) {
			return true
		}
	}
	return false
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
	var activeLease *dispatch.DAGRunLease
	leaseMissing := false
	if h.dagRunLeaseStore != nil {
		var validationErr error
		activeLease, leaseMissing, validationErr = h.validateStatusLease(ctx, req.WorkerId, dagRunStatus)
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
	held := h.runLocks.lock(dagRunStatus.DAGRunID)
	defer h.runLocks.unlock(dagRunStatus.DAGRunID, held)

	latestAttempt, latestStatus, err := h.resolveLatestAttempt(ctx, dagRunStatus.Name, dagRunStatus.DAGRunID, dagRunStatus.Root)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			profileErr := h.reconcileStatusProfile(ctx, activeLease, nil, dagRunStatus)
			if errors.Is(profileErr, errProfileMismatch) {
				logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, nil, remoteAttemptRejectedSuperseded)
				return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: remoteAttemptRejectedSuperseded}, nil
			}
			if profileErr != nil {
				return nil, status.Error(codes.Internal, "failed to reconcile runtime profile: "+profileErr.Error())
			}
		}
		bootstrappedAttempt, bootstrapped, bootstrapErr := h.bootstrapMissingSubAttempt(ctx, req.WorkerId, req.SourceFile, dagRunStatus, err)
		if bootstrapErr != nil {
			return nil, status.Error(codes.Internal, "failed to bootstrap sub-attempt: "+bootstrapErr.Error())
		}
		if bootstrapped {
			h.transformLogPaths(dagRunStatus)
			h.keepLegacyLogPaths(nil, dagRunStatus)
			if err := h.transformArtifactPaths(ctx, bootstrappedAttempt, nil, dagRunStatus); err != nil {
				return nil, status.Error(codes.Internal, "failed to resolve artifact path: "+err.Error())
			}
			if err := bootstrappedAttempt.Write(ctx, *dagRunStatus); err != nil {
				h.closeCachedAttemptForRun(ctx, context.WithoutCancel(ctx), dagRunStatus.DAGRunID, bootstrappedAttempt.ID())
				return nil, status.Error(codes.Internal, "failed to write status: "+err.Error())
			}

			h.persistChatMessages(ctx, bootstrappedAttempt, dagRunStatus)

			ownership := h.attemptOwnership()
			// Live tracking must complete after the status write even if the
			// reporting worker disconnects.
			ownership.syncFromStatus(context.WithoutCancel(ctx), req.WorkerId, dagRunStatus, bootstrappedAttempt.ID())
			h.finalizeAdmissionForStatus(ctx, dagRunStatus, bootstrappedAttempt.ID())
			h.closeCachedInactiveAttempt(ctx, dagRunStatus, bootstrappedAttempt)

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
	if leaseMissing && latestStatus.Status != ir.NotStarted && !latestStatus.Status.IsTerminal() {
		logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, latestStatus, remoteAttemptRejectedLeaseInactive)
		return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: remoteAttemptRejectedLeaseInactive}, nil
	}
	profileErr := h.reconcileStatusProfile(ctx, activeLease, latestStatus, dagRunStatus)
	if errors.Is(profileErr, errProfileMismatch) {
		logRejectedRemoteStatusUpdate(ctx, req.WorkerId, dagRunStatus, latestStatus, remoteAttemptRejectedSuperseded)
		return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: remoteAttemptRejectedSuperseded}, nil
	}
	if profileErr != nil {
		return nil, status.Error(codes.Internal, "failed to reconcile runtime profile: "+profileErr.Error())
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
	h.keepLegacyLogPaths(latestStatus, dagRunStatus)
	if len(latestStatus.Labels) > 0 {
		dagRunStatus.Labels = append([]string(nil), latestStatus.Labels...)
	}
	// A worker's failure report is built from the task and lacks these.
	if latestStatus.RetryDisabled {
		dagRunStatus.RetryDisabled = true
	}
	if dagRunStatus.TriggerSubject == nil {
		dagRunStatus.TriggerSubject = latestStatus.TriggerSubject
	}
	// A worker that predates node lists reports neither, and both must
	// outlive its report so a retry stays narrowed.
	if len(dagRunStatus.TargetNodes) == 0 && len(latestStatus.TargetNodes) > 0 {
		dagRunStatus.TargetNodes = append([]string(nil), latestStatus.TargetNodes...)
	}
	if dagRunStatus.NodeList == nil && latestStatus.NodeList != nil {
		record := *latestStatus.NodeList
		dagRunStatus.NodeList = &record
	}
	// So must the worker node a run was started bound to, which keeps its
	// retries on that worker.
	if dagRunStatus.BoundWorker == "" {
		dagRunStatus.BoundWorker = latestStatus.BoundWorker
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
			h.closeCachedAttemptForRun(ctx, context.WithoutCancel(ctx), dagRunStatus.DAGRunID, attempt.ID())
			return nil, status.Error(codes.Internal, "failed to write status: "+err.Error())
		}
	}

	// Persist chat messages for each node.
	h.persistChatMessages(ctx, attempt, dagRunStatus)

	// Live tracking must complete after the status write even if the reporting
	// worker disconnects.
	ownership.syncFromStatus(context.WithoutCancel(ctx), req.WorkerId, dagRunStatus, attempt.ID())
	h.finalizeAdmissionForStatus(ctx, dagRunStatus, attempt.ID())
	h.closeCachedInactiveAttempt(ctx, dagRunStatus, attempt)

	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

// sameTimestamp reports whether two recorded times are the same instant. A
// writer from before timestamps carried milliseconds drops the fraction, so a
// value without one matches any time in the same second.
func sameTimestamp(a, b string) bool {
	if a == b {
		return true
	}
	ta, errA := stringutil.ParseTime(a)
	tb, errB := stringutil.ParseTime(b)
	if errA != nil || errB != nil || ta.IsZero() || tb.IsZero() {
		return false
	}
	if !strings.Contains(a, ".") || !strings.Contains(b, ".") {
		return ta.Truncate(time.Second).Equal(tb.Truncate(time.Second))
	}
	return ta.Equal(tb)
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
			(next.Status != node.Status || !sameTimestamp(next.FinishedAt, node.FinishedAt)) {
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

func (h *Handler) closeCachedInactiveAttempt(
	ctx context.Context,
	status *ir.DAGRunStatus,
	attempt dagrun.Attempt,
) {
	if status == nil || attempt == nil {
		return
	}
	if status.Status != ir.Waiting && !status.Status.IsTerminal() {
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
	if rootStatus == nil || rootStatus.Status.IsTerminal() {
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

	h.cacheOpenAttempt(ctx, runStatus.DAGRunID, attempt)

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

// transformLogPaths rewrites worker-local log paths to coordinator paths, as
// this version names them. keepLegacyLogPaths then corrects the paths of a
// run in flight across an upgrade.
func (h *Handler) transformLogPaths(status *ir.DAGRunStatus) {
	if h.logDir == "" {
		return
	}
	dagName, dagRunID, attemptID := logAttempt(status)
	stdout := StreamTypeToExtension(coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT)
	stderr := StreamTypeToExtension(coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR)
	logNodes(status, func(node *ir.Node, name string) {
		node.Stdout = attemptLogFile(h.logDir, dagName, dagRunID, attemptID, stepLogFile(name, stdout))
		node.Stderr = attemptLogFile(h.logDir, dagName, dagRunID, attemptID, stepLogFile(name, stderr))
	})
	status.Log = attemptLogFile(h.logDir, dagName, dagRunID, attemptID, schedulerLogFile)
}

// keepLegacyLogPaths points the log paths of a run in flight across an
// upgrade back at the files a released version named, which the log handler
// goes on writing (see attemptLogPath). Only a path that latest, the status
// stored before, does not hold already is checked, so a run this version
// started reaches the file system on its first report only. A nil latest
// checks every path.
func (h *Handler) keepLegacyLogPaths(latest, status *ir.DAGRunStatus) {
	if h.logDir == "" {
		return
	}
	stored := map[string]*ir.Node{}
	if latest != nil {
		logNodes(latest, func(node *ir.Node, name string) { stored[name] = node })
	}
	dagName, dagRunID, attemptID := logAttempt(status)
	names := logStepNames(status)
	steps := func() []string { return names }
	stdout := StreamTypeToExtension(coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT)
	stderr := StreamTypeToExtension(coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR)
	logNodes(status, func(node *ir.Node, name string) {
		prev := stored[name]
		if prev == nil || prev.Stdout != node.Stdout {
			node.Stdout = stepLogPath(h.logDir, dagName, dagRunID, attemptID, name, stdout, steps)
		}
		if prev == nil || prev.Stderr != node.Stderr {
			node.Stderr = stepLogPath(h.logDir, dagName, dagRunID, attemptID, name, stderr, steps)
		}
	})
	if latest == nil || latest.Log != status.Log {
		status.Log = schedulerLogPath(h.logDir, dagName, dagRunID, attemptID)
	}
}

// logAttempt returns the DAG, run, and attempt that name the coordinator
// directory of a run's logs. A sub-DAG run keeps its logs under its root run.
func logAttempt(status *ir.DAGRunStatus) (dagName, dagRunID, attemptID string) {
	dagName, dagRunID, attemptID = status.Name, status.DAGRunID, status.AttemptID
	if status.Root.ID != "" && status.Root.ID != dagRunID {
		dagName, dagRunID = status.Root.Name, status.Root.ID
	}
	// Use dagRunID as attemptID if not set (matches log_handler.go logic)
	if attemptID == "" {
		attemptID = status.DAGRunID
	}
	return dagName, dagRunID, attemptID
}

// logNodes calls fn with each node of a run and the name the coordinator
// keeps its logs under.
func logNodes(status *ir.DAGRunStatus, fn func(node *ir.Node, name string)) {
	visit := func(node *ir.Node, fallbackName string) {
		if node == nil {
			return
		}
		// Use step name, or fallback for handler nodes with empty names
		name := node.Step.Name
		if name == "" {
			name = fallbackName
		}
		fn(node, name)
	}
	for _, node := range status.Nodes {
		visit(node, "step")
	}
	visit(status.OnInit, "on_init")
	visit(status.OnExit, "on_exit")
	visit(status.OnSuccess, "on_success")
	visit(status.OnFailure, "on_failure")
	visit(status.OnAbort, "on_abort")
	visit(status.OnWait, "on_wait")
}

// logStepNames returns the names the coordinator keeps the logs of a run's
// steps under.
func logStepNames(status *ir.DAGRunStatus) []string {
	var names []string
	logNodes(status, func(_ *ir.Node, name string) { names = append(names, name) })
	return names
}

// runLogStepNames returns the names the coordinator keeps the logs of the
// steps of a chunk's run under, or none when the run cannot be read.
func (h *Handler) runLogStepNames(ctx context.Context, chunk *coordinatorv1.LogChunk) []string {
	root := ir.DAGRunRef{Name: chunk.RootDagRunName, ID: chunk.RootDagRunId}
	_, runStatus, err := h.resolveLatestAttempt(ctx, chunk.DagName, chunk.DagRunId, root)
	if err != nil || runStatus == nil {
		return nil
	}
	return logStepNames(runStatus)
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

		overrideDir := ""
		if dag.Artifacts != nil {
			overrideDir = dag.Artifacts.Dir
		}

		// The worker directory is throwaway staging, so a fresh coordinator
		// path is assigned rather than mirroring the reported name.
		artifactDir, err := artifactpath.NewRunDir(ctx, h.artifactDir, overrideDir, dag.Name, incoming.DAGRunID, artifactRunTime(incoming))
		if err != nil {
			return fmt.Errorf("resolve artifact directory: %w", err)
		}
		incoming.ArchiveDir = artifactDir
	}
	if incoming.ArchiveDir == "" {
		return nil
	}
	if err := os.MkdirAll(incoming.ArchiveDir, 0o750); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	return nil
}

// artifactRunTime reports the time that places a run in the artifact date
// tree, falling back to now when the reported start time is unusable.
func artifactRunTime(status *ir.DAGRunStatus) time.Time {
	if startedAt, err := stringutil.ParseTime(status.StartedAt); err == nil && !startedAt.IsZero() {
		return startedAt
	}
	return time.Now()
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

func (h *Handler) getOrOpenRootLocked(ctx context.Context, dagName, dagRunID string) (dagrun.Attempt, error) {
	ref := ir.DAGRunRef{Name: dagName, ID: dagRunID}
	return h.getOrOpenLocked(ctx, dagRunID, func() (dagrun.Attempt, error) {
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
	held := h.runLocks.lock(subDAGRunID)
	defer h.runLocks.unlock(subDAGRunID, held)

	return h.getOrOpenSubLocked(ctx, rootRef, subDAGRunID)
}

func (h *Handler) getOrOpenSubLocked(ctx context.Context, rootRef ir.DAGRunRef, subDAGRunID string) (dagrun.Attempt, error) {
	return h.getOrOpenLocked(ctx, subDAGRunID, func() (dagrun.Attempt, error) {
		return h.dagRunRepository.FindSubAttempt(ctx, rootRef, subDAGRunID)
	})
}

// getOrOpenLocked retrieves or opens an attempt while its run lock is held.
func (h *Handler) getOrOpenLocked(ctx context.Context, cacheKey string, finder func() (dagrun.Attempt, error)) (dagrun.Attempt, error) {
	h.attemptsMu.RLock()
	if attempt, ok := h.openAttempts[cacheKey]; ok {
		h.attemptsMu.RUnlock()
		return attempt, nil
	}
	h.attemptsMu.RUnlock()

	attempt, err := finder()
	if err != nil {
		return nil, err
	}

	if err := attempt.Open(ctx); err != nil {
		return nil, err
	}

	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()
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
	logHandler.stepNames = func(chunk *coordinatorv1.LogChunk) []string { return h.runLogStepNames(stream.Context(), chunk) }
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
		if h.dagRunLeaseStore != nil && !h.awaitLeaseGrace(ctx, interval) {
			return
		}
		h.detectAndCleanupZombies(ctx)
		h.cleanupWorkspaceBundles(ctx, time.Now().UTC())

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		bundleTicker := time.NewTicker(workspaceBundleCleanupInterval)
		defer bundleTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.detectAndCleanupZombies(ctx)
			case now := <-bundleTicker.C:
				h.cleanupWorkspaceBundles(ctx, now.UTC())
			}
		}
	}()
}

// awaitLeaseGrace waits one lease period before the first stale-run pass, so
// a replacement coordinator does not fail runs whose workers have not yet
// reported to it. Held dispatches carry their own deadline and keep expiring
// meanwhile. It reports false when ctx ends first.
func (h *Handler) awaitLeaseGrace(ctx context.Context, interval time.Duration) bool {
	grace := time.NewTimer(h.staleLeaseThreshold)
	defer grace.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-grace.C:
			return true
		case now := <-ticker.C:
			h.expireHeldDispatches(ctx, now.UTC())
		}
	}
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
	h.reconcileWorkerNodes(ctx)

	// Pass 1: clean up stale worker presence records used for discovery.
	if h.workerHeartbeatStore != nil {
		_, _ = h.workerHeartbeatStore.DeleteStale(ctx, time.Now().Add(-h.staleHeartbeatThreshold))
		// The local entries hold only this coordinator's view of each
		// worker, such as when its heartbeat passes to its certificate
		// record, so the stale ones go too.
		h.collectAndRemoveStaleHeartbeats()
	} else {
		for _, info := range h.collectAndRemoveStaleHeartbeats() {
			h.markWorkerTasksFailed(ctx, info)
		}
	}

	// Pass 2: lease-based detection — catches distributed runs whose workers
	// stopped reporting owner-bound run heartbeats, including after coordinator
	// restarts or owner coordinator loss.
	h.detectStaleLeases(ctx)

	// Pass 3: held dispatches whose wait for a matching worker has passed.
	h.expireHeldDispatches(ctx, time.Now().UTC())

	// Pass 4: pending dispatches left behind by runs that moved on.
	h.dropOrphanedDispatches(ctx, time.Now().UTC())
}

func (h *Handler) cleanupWorkspaceBundles(ctx context.Context, now time.Time) {
	if h.workspaceBundleStore == nil {
		return
	}
	if _, err := h.workspaceBundleStore.CleanupReferenced(
		ctx,
		now.Add(-workspaceBundleTTL),
		h.bundleDigests,
	); err != nil {
		logger.Warn(ctx, "Failed to clean expired workspace bundles", tag.Error(err))
	}
}

func (h *Handler) bundleDigests(ctx context.Context) (map[string]struct{}, error) {
	digests := make(map[string]struct{})
	if h.dispatchTaskStore != nil {
		taskDigests, err := h.dispatchTaskStore.ListBundleDigests(ctx)
		if err != nil {
			return nil, fmt.Errorf("list dispatch bundle references: %w", err)
		}
		for _, digest := range taskDigests {
			if !workspacebundle.ValidDigest(digest) {
				return nil, fmt.Errorf("invalid dispatch bundle digest %q", digest)
			}
			digests[digest] = struct{}{}
		}
	}

	if h.dagRunLeaseStore != nil {
		leases, err := h.dagRunLeaseStore.ListAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("list lease bundle references: %w", err)
		}
		for _, lease := range leases {
			digest := lease.WorkspaceBundleDigest
			if digest == "" {
				continue
			}
			if !workspacebundle.ValidDigest(digest) {
				return nil, fmt.Errorf("invalid lease bundle digest %q", digest)
			}
			digests[digest] = struct{}{}
		}
	}
	return digests, nil
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

	reconciledStatus, repaired, err := h.repairStaleRun(ctx, runStatus, attemptID, workerID, lease.AttemptKey)
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
	if reconciledStatus.AttemptID != attemptID || reconciledStatus.Status.IsTerminal() {
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
	attemptKey string,
) (*ir.DAGRunStatus, bool, error) {
	repairCtx := context.WithoutCancel(ctx)
	if status != nil && status.DAGRunID != "" {
		held := h.runLocks.lock(status.DAGRunID)
		defer h.runLocks.unlock(status.DAGRunID, held)

		attemptID := status.AttemptID
		if attemptID == "" {
			attemptID = fallbackAttemptID
		}
		h.closeCachedAttemptForRun(ctx, repairCtx, status.DAGRunID, attemptID)
	}

	reconciledStatus, repaired, err := runtime.RepairStaleRemoteRun(repairCtx, runtime.StaleRunRepairConfig{
		DAGRunRepository:              h.dagRunRepository,
		DAGRunLeaseStore:              h.dagRunLeaseStore,
		WorkerHeartbeatStore:          h.workerHeartbeatStore,
		StaleLeaseThreshold:           h.staleLeaseThreshold,
		StaleWorkerHeartbeatThreshold: h.staleHeartbeatThreshold,
	}, status, fallbackAttemptID, fallbackWorkerID)
	if err == nil && repaired {
		// The lease goes in the same critical section as the failure, so a
		// run heartbeat never finds the failed run still leased.
		h.attemptOwnership().deleteTracking(ctx, repairCtx, status.DAGRun(), attemptKey,
			"Failed to delete stale distributed lease after confirmed failure",
			"Failed to delete active distributed run after confirmed failure",
		)
	}
	return reconciledStatus, repaired, err
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

		reconciledStatus, repaired, err := h.repairStaleRun(ctx, status, leaseState.attemptID, status.WorkerID, leaseState.attemptKey)
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
			continue
		}
		if reconciledStatus == nil {
			continue
		}
		if reconciledStatus.AttemptID != leaseState.attemptID || reconciledStatus.Status.IsTerminal() {
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

		reconciledStatus, repaired, err := h.repairStaleRun(ctx, runStatus, record.AttemptID, workerID, record.AttemptKey)
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
			continue
		}
		if reconciledStatus == nil {
			continue
		}
		if reconciledStatus.AttemptID != record.AttemptID || reconciledStatus.Status.IsTerminal() {
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
	held := h.runLocks.lock(dagRun.ID)
	defer h.runLocks.unlock(dagRun.ID, held)

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
	if status.AttemptID != attemptID || status.Status.IsTerminal() {
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

	held := h.runLocks.lock(dagRunID)
	defer h.runLocks.unlock(dagRunID, held)

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
	if !needsOpen {
		defer h.closeCachedAttemptForRun(ctx, storeCtx, dagRunID, attempt.ID())
	}

	dagRunStatus, err := attempt.ReadStatusUncached(storeCtx)
	if err != nil {
		logger.Error(ctx, "Failed to read status for zombie cleanup",
			tag.DAG(dagName), tag.RunID(dagRunID), tag.Error(err))
		return
	}

	if dagRunStatus.Status.IsTerminal() {
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
	h.dropHeldDispatch(ctx, attempt)

	logger.Info(ctx, "DAG run cancellation requested successfully")
	return &coordinatorv1.RequestCancelResponse{Accepted: true}, nil
}

// dropHeldDispatch removes the pending task of a cancelled run that was
// waiting for a worker, so a worker registering later does not run it.
func (h *Handler) dropHeldDispatch(ctx context.Context, attempt dagrun.Attempt) {
	if h.dispatchTaskStore == nil || attempt == nil {
		return
	}
	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil || runStatus == nil || runStatus.Status != ir.Aborted {
		return
	}
	attemptKey := dispatch.AttemptKeyForStatus(runStatus, attempt.ID())
	if attemptKey == "" {
		return
	}
	if err := h.dispatchTaskStore.DeletePending(context.WithoutCancel(ctx), attemptKey); err != nil {
		logger.Warn(ctx, "Failed to drop the pending task of a cancelled run",
			tag.AttemptKey(attemptKey), tag.Error(err))
	}
}

// notStartedCancellationReason is the run error shown to users for a run
// canceled before any worker started it.
const notStartedCancellationReason = "canceled before the run started"

func finalizeNotStartedCancellation(ctx context.Context, attempt dagrun.Attempt) error {
	if attempt == nil {
		return nil
	}

	status, err := attempt.ReadStatusUncached(ctx)
	if err != nil {
		return fmt.Errorf("read attempt status: %w", err)
	}
	if status == nil {
		return nil
	}
	// A queued run that no worker has claimed is also one that never started;
	// a held dispatch waits in that state.
	if status.Status != ir.NotStarted && (status.Status != ir.Queued || status.WorkerID != "") {
		return nil
	}

	finishedAt := stringutil.FormatTime(time.Now().UTC())
	status.Status = ir.Aborted
	status.FinishedAt = finishedAt
	status.Error = notStartedCancellationReason
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
