// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	osexec "os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type queueDispatchDeps struct {
	queueStore             queuedomain.QueueStore
	dagRunRepository       *persis.DAGRunRepository
	procRepository         queueProcessRepository
	dagRunLeaseStore       dispatch.DAGRunLeaseStore
	dispatchTaskStore      dispatch.DispatchTaskStore
	dispatchAdmissionStore dispatch.DispatchAdmissionStore
	workerHeartbeatStore   dispatch.WorkerHeartbeatStore
	workerStaleAfter       time.Duration
	dagExecutor            *DAGExecutor
	isSuspended            IsSuspendedFunc
	backoffConfig          BackoffConfig
	leaseStaleThreshold    time.Duration
	isClosed               func() bool
	wakeUp                 func()
	acquireDispatchHandoff func(context.Context) (func(), bool)
}

// queueDispatcher owns queue-item dispatch decisions after a queue has capacity.
type queueDispatcher struct {
	queueStore             queuedomain.QueueStore
	dagRunRepository       *persis.DAGRunRepository
	procRepository         queueProcessRepository
	dagRunLeaseStore       dispatch.DAGRunLeaseStore
	dispatchTaskStore      dispatch.DispatchTaskStore
	dispatchAdmissionStore dispatch.DispatchAdmissionStore
	workerHeartbeatStore   dispatch.WorkerHeartbeatStore
	workerStaleAfter       time.Duration
	dagExecutor            *DAGExecutor
	isSuspended            IsSuspendedFunc
	backoffConfig          BackoffConfig
	leaseStaleThreshold    time.Duration
	isClosed               func() bool
	wakeUp                 func()
	acquireDispatchHandoff func(context.Context) (func(), bool)

	queuedConditionCursorMu sync.Mutex
	queuedConditionCursor   map[string]int
}

type queueDispatchBatch struct {
	items                 []queuedomain.QueuedItemData
	maxConcurrency        int
	aliveCount            int
	nonAdmissionOccupancy int
}

type workerHeartbeatSnapshot struct {
	records []dispatch.WorkerHeartbeatRecord
	err     error
	loaded  bool
}

func (s *workerHeartbeatSnapshot) load(
	ctx context.Context,
	store dispatch.WorkerHeartbeatStore,
) ([]dispatch.WorkerHeartbeatRecord, error) {
	if !s.loaded {
		s.loaded = true
		s.records, s.err = store.List(ctx)
		if s.err != nil {
			logger.Error(ctx, "Failed to list worker heartbeats", tag.Error(s.err))
		}
	}
	return s.records, s.err
}

type dispatchAdmissionInput struct {
	status                *ir.DAGRunStatus
	maxConcurrency        int
	nonAdmissionOccupancy int
}

const (
	dagRunConditionRunnable              = "Runnable"
	dagRunConditionConcurrencyReady      = "ConcurrencyReady"
	dagRunConditionWorkerAssignmentReady = "WorkerAssignmentReady"
	dagRunConditionRunRecordReady        = "RunRecordReady"
	dagRunConditionWorkerReady           = "WorkerReady"
	dagRunConditionStartObserved         = "StartObserved"
	dagRunConditionQueueReady            = "QueueReady"

	dagRunConditionStatusFalse   = "False"
	dagRunConditionStatusUnknown = "Unknown"

	queuedConditionReasonMaxConcurrencyReached     = "MaxConcurrencyReached"
	queuedConditionReasonAssignmentPending         = "AssignmentPending"
	queuedConditionReasonAssignmentUnavailable     = "AssignmentUnavailable"
	queuedConditionReasonAttemptIdentityMissing    = "AttemptIdentityMissing"
	queuedConditionReasonDAGSnapshotUnavailable    = "DAGSnapshotUnavailable"
	queuedConditionReasonNoMatchingWorker          = "NoMatchingWorker"
	queuedConditionReasonNoAvailableWorker         = "NoAvailableWorker"
	queuedConditionReasonWorkerDispatchUnavailable = "WorkerDispatchUnavailable"
	queuedConditionReasonLaunchFailed              = "LaunchFailed"
	queuedConditionReasonStartupNotObserved        = "StartupNotObserved"
	queuedConditionReasonRunLivenessUnavailable    = "RunLivenessUnavailable"
	queuedConditionReasonQueueStateUnavailable     = "QueueStateUnavailable"

	queuedConditionRefreshInterval   = time.Minute
	queuedConditionRefreshBatchLimit = 100
)

var errQueuedConditionFresh = errors.New("queued condition is already fresh")

type queuedConditionDef struct {
	conditionType string
	status        string
	reason        string
	message       string
}

var (
	maxConcurrencyReachedConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonMaxConcurrencyReached,
			message:       "The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		},
		{
			conditionType: dagRunConditionConcurrencyReady,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonMaxConcurrencyReached,
			message:       "The queue active-run concurrency limit has been reached.",
		},
	}
	assignmentPendingDetailConditionDef = queuedConditionDef{
		conditionType: dagRunConditionWorkerAssignmentReady,
		status:        dagRunConditionStatusUnknown,
		reason:        queuedConditionReasonAssignmentPending,
		message:       "Worker assignment is already pending.",
	}
	assignmentPendingConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonAssignmentPending,
			message:       "The DAG-run is waiting while Dagu assigns it to a worker.",
		},
		assignmentPendingDetailConditionDef,
	}
	assignmentUnavailableConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonAssignmentUnavailable,
			message:       "Dagu cannot determine whether worker assignment can proceed.",
		},
		{
			conditionType: dagRunConditionWorkerAssignmentReady,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonAssignmentUnavailable,
			message:       "Worker assignment is temporarily unavailable.",
		},
	}
	attemptIdentityMissingConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonAttemptIdentityMissing,
			message:       "The DAG-run cannot start because its queued attempt identity is incomplete.",
		},
		{
			conditionType: dagRunConditionRunRecordReady,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonAttemptIdentityMissing,
			message:       "The queued attempt is missing the identity required for worker assignment.",
		},
	}
	dagSnapshotUnavailableConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonDAGSnapshotUnavailable,
			message:       "The DAG-run cannot start because its persisted DAG snapshot could not be read.",
		},
		{
			conditionType: dagRunConditionRunRecordReady,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonDAGSnapshotUnavailable,
			message:       "The queued attempt exists, but its DAG snapshot is unavailable.",
		},
	}
	noMatchingWorkerConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonNoMatchingWorker,
			message:       "The DAG-run cannot start because no healthy worker matches the required selector.",
		},
		{
			conditionType: dagRunConditionWorkerReady,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonNoMatchingWorker,
			message:       "No healthy worker matches the required worker selector.",
		},
	}
	noAvailableWorkerConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonNoAvailableWorker,
			message:       "The DAG-run cannot start because no healthy distributed worker is available.",
		},
		{
			conditionType: dagRunConditionWorkerReady,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonNoAvailableWorker,
			message:       "No healthy distributed worker is available.",
		},
	}
	workerDispatchUnavailableConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonWorkerDispatchUnavailable,
			message:       "Dagu attempted worker assignment, but assignment is temporarily unavailable.",
		},
		{
			conditionType: dagRunConditionWorkerAssignmentReady,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonWorkerDispatchUnavailable,
			message:       "Worker dispatch is temporarily unavailable.",
		},
	}
	launchFailedConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonLaunchFailed,
			message:       "The DAG-run cannot start because local launch failed before startup was observed.",
		},
		{
			conditionType: dagRunConditionStartObserved,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonLaunchFailed,
			message:       "Local launch failed before any started signal was observed.",
		},
	}
	startupNotObservedConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonStartupNotObserved,
			message:       "Dagu attempted to start the DAG-run but has not observed a heartbeat, lease, or status transition.",
		},
		{
			conditionType: dagRunConditionStartObserved,
			status:        dagRunConditionStatusFalse,
			reason:        queuedConditionReasonStartupNotObserved,
			message:       "No started signal was observed after the startup wait window.",
		},
	}
	runLivenessUnavailableConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionStartObserved,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonRunLivenessUnavailable,
			message:       "Dagu could not check run liveness while waiting for startup.",
		},
	}
	queueStateUnavailableConditionDefs = []queuedConditionDef{
		{
			conditionType: dagRunConditionRunnable,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonQueueStateUnavailable,
			message:       "The DAG-run cannot start because queue state could not be checked.",
		},
		{
			conditionType: dagRunConditionQueueReady,
			status:        dagRunConditionStatusUnknown,
			reason:        queuedConditionReasonQueueStateUnavailable,
			message:       "Dagu could not inspect queue state needed for dispatch.",
		},
	}
)

func maxConcurrencyReachedWithAssignmentPendingConditionDefs() []queuedConditionDef {
	defs := make([]queuedConditionDef, 0, len(maxConcurrencyReachedConditionDefs)+1)
	defs = append(defs, maxConcurrencyReachedConditionDefs...)
	defs = append(defs, assignmentPendingDetailConditionDef)
	return defs
}

func newQueueDispatcher(deps queueDispatchDeps) *queueDispatcher {
	if deps.isSuspended == nil {
		deps.isSuspended = func(context.Context, string) (bool, error) { return false, nil }
	}
	if deps.isClosed == nil {
		deps.isClosed = func() bool { return false }
	}
	if deps.wakeUp == nil {
		deps.wakeUp = func() {}
	}
	if deps.acquireDispatchHandoff == nil {
		deps.acquireDispatchHandoff = func(context.Context) (func(), bool) {
			return func() {}, true
		}
	}
	return &queueDispatcher{
		queueStore:             deps.queueStore,
		dagRunRepository:       deps.dagRunRepository,
		procRepository:         deps.procRepository,
		dagRunLeaseStore:       deps.dagRunLeaseStore,
		dispatchTaskStore:      deps.dispatchTaskStore,
		dispatchAdmissionStore: deps.dispatchAdmissionStore,
		workerHeartbeatStore:   deps.workerHeartbeatStore,
		workerStaleAfter:       deps.workerStaleAfter,
		dagExecutor:            deps.dagExecutor,
		isSuspended:            deps.isSuspended,
		backoffConfig:          deps.backoffConfig,
		leaseStaleThreshold:    deps.leaseStaleThreshold,
		isClosed:               deps.isClosed,
		wakeUp:                 deps.wakeUp,
		acquireDispatchHandoff: deps.acquireDispatchHandoff,
		queuedConditionCursor:  make(map[string]int),
	}
}

type queuedConditionStage struct {
	dispatcher   *queueDispatcher
	queueName    string
	itemID       string
	runRef       ir.DAGRunRef
	attemptID    string
	observations []ir.DAGRunCondition
	flushed      bool
}

func (d *queueDispatcher) queuedConditionItemsForRefresh(
	queueName string,
	items []queuedomain.QueuedItemData,
) []queuedomain.QueuedItemData {
	if len(items) <= queuedConditionRefreshBatchLimit {
		return items
	}

	d.queuedConditionCursorMu.Lock()
	start := d.queuedConditionCursor[queueName] % len(items)
	d.queuedConditionCursor[queueName] = (start + queuedConditionRefreshBatchLimit) % len(items)
	d.queuedConditionCursorMu.Unlock()

	selected := make([]queuedomain.QueuedItemData, 0, queuedConditionRefreshBatchLimit)
	for i := range queuedConditionRefreshBatchLimit {
		selected = append(selected, items[(start+i)%len(items)])
	}
	return selected
}

func (d *queueDispatcher) newQueuedConditionStage(
	runRef ir.DAGRunRef,
	queueName string,
	itemID string,
	attempt dagrun.Attempt,
	status *ir.DAGRunStatus,
) *queuedConditionStage {
	if d == nil || d.dagRunRepository == nil || status == nil || status.Status != ir.Queued {
		return nil
	}
	attemptID := status.AttemptID
	if attemptID == "" && attempt != nil {
		attemptID = attempt.ID()
	}
	return &queuedConditionStage{
		dispatcher: d,
		queueName:  queueName,
		itemID:     itemID,
		runRef:     runRef,
		attemptID:  attemptID,
	}
}

func (d *queueDispatcher) newQueuedConditionStageFromItem(
	ctx context.Context,
	queueName string,
	item queuedomain.QueuedItemData,
) *queuedConditionStage {
	if d == nil || d.dagRunRepository == nil || item == nil {
		return nil
	}
	runRef, err := item.Data()
	if err != nil {
		logger.Warn(ctx, "Failed to read queued item while staging queued condition", tag.Error(err))
		return nil
	}
	if runRef == nil {
		return nil
	}
	if d.procRepository != nil && queueName != "" {
		running, err := d.procRepository.IsRunAlive(ctx, queueName, *runRef)
		if err != nil {
			logger.Warn(ctx, "Failed to check queued item liveness while staging queued condition",
				tag.Error(err),
				tag.RunID(runRef.ID),
			)
			return nil
		}
		if running {
			return nil
		}
	}
	attempt, status, ok := d.readQueuedConditionStatus(ctx, *runRef)
	if !ok {
		return nil
	}
	started, err := d.hasFreshDistributedLease(ctx, queueName, *runRef, attempt, status)
	if err != nil {
		logger.Warn(ctx, "Failed to check distributed lease while staging queued condition",
			tag.Error(err),
			tag.RunID(runRef.ID),
		)
		return nil
	}
	if started {
		return nil
	}
	return d.newQueuedConditionStage(*runRef, queueName, item.ID(), attempt, status)
}

func (d *queueDispatcher) readQueuedConditionStatus(
	ctx context.Context,
	runRef ir.DAGRunRef,
) (dagrun.Attempt, *ir.DAGRunStatus, bool) {
	attempt, err := d.dagRunRepository.FindAttempt(ctx, runRef)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil, nil, false
		}
		logger.Warn(ctx, "Failed to find queued DAG-run while staging condition",
			tag.Error(err),
			tag.RunID(runRef.ID),
		)
		return nil, nil, false
	}
	if attempt.Hidden() {
		return nil, nil, false
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrNoStatusData) || errors.Is(err, dagrun.ErrCorruptedStatusData) {
			return nil, nil, false
		}
		logger.Warn(ctx, "Failed to read queued DAG-run status while staging condition",
			tag.Error(err),
			tag.RunID(runRef.ID),
		)
		return nil, nil, false
	}
	if status == nil || status.Status != ir.Queued {
		return nil, nil, false
	}
	return attempt, status, true
}

func (s *queuedConditionStage) observe(defs ...queuedConditionDef) {
	if s == nil || s.flushed || len(defs) == 0 {
		return
	}
	checkedAt := time.Now()
	observations := make([]ir.DAGRunCondition, 0, len(defs))
	for _, def := range defs {
		observations = append(observations, ir.NewDAGRunCondition(
			def.conditionType,
			def.status,
			def.reason,
			def.message,
			checkedAt,
		))
	}
	s.observations = ir.MergeDAGRunConditions(s.observations, observations...)
}

func (s *queuedConditionStage) flush(ctx context.Context) {
	s.flushWithQueueCheck(ctx, true)
}

func (s *queuedConditionStage) flushWithoutQueueCheck(ctx context.Context) {
	s.flushWithQueueCheck(ctx, false)
}

func (s *queuedConditionStage) flushWithQueueCheck(ctx context.Context, checkQueue bool) {
	if s == nil || s.flushed || len(s.observations) == 0 {
		return
	}
	s.flushed = true
	if err := s.flushErr(ctx, checkQueue); err != nil {
		logger.Warn(ctx, "Failed to update queued DAG-run condition",
			tag.Error(err),
			tag.RunID(s.runRef.ID),
		)
	}
}

func (s *queuedConditionStage) flushErr(ctx context.Context, checkQueue bool) error {
	if checkQueue && !s.itemStillQueued(ctx) {
		return nil
	}

	attempt, status, ok := s.dispatcher.readQueuedConditionStatus(ctx, s.runRef)
	if !ok {
		return nil
	}
	expectedAttemptID := s.attemptID
	if expectedAttemptID == "" {
		expectedAttemptID = status.AttemptID
	}
	if expectedAttemptID == "" && attempt != nil {
		expectedAttemptID = attempt.ID()
	}
	observations := append([]ir.DAGRunCondition(nil), s.observations...)
	if !queuedConditionNeedsUpdate(status, observations) {
		return nil
	}

	_, _, err := s.dispatcher.dagRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		s.runRef,
		expectedAttemptID,
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			if !queuedConditionNeedsUpdate(latest, observations) {
				return errQueuedConditionFresh
			}
			latest.Conditions = mergeQueuedConditionObservations(latest.Conditions, observations)
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	if errors.Is(err, errQueuedConditionFresh) {
		return nil
	}
	return err
}

func (s *queuedConditionStage) itemStillQueued(ctx context.Context) bool {
	if s.dispatcher.queueStore == nil || s.queueName == "" || s.itemID == "" {
		return true
	}
	items, err := s.dispatcher.queueStore.List(ctx, s.queueName)
	if err != nil {
		logger.Warn(ctx, "Failed to verify queued item before updating condition",
			tag.Error(err),
			tag.RunID(s.runRef.ID),
		)
		return false
	}
	for _, item := range items {
		if item.ID() != s.itemID {
			continue
		}
		runRef, err := item.Data()
		if err != nil {
			logger.Warn(ctx, "Failed to read queued item before updating condition",
				tag.Error(err),
				tag.RunID(s.runRef.ID),
			)
			return false
		}
		return runRef != nil && *runRef == s.runRef
	}
	return false
}

func (d *queueDispatcher) selectDispatchBatch(
	ctx context.Context,
	queueName string,
	items []queuedomain.QueuedItemData,
	maxConcurrency int,
	inflightCount int,
) (queueDispatchBatch, error) {
	localAliveCount, err := d.procRepository.CountAlive(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count alive processes", tag.Error(err), tag.Queue(queueName))
		d.recordQueueStateUnavailableConditions(ctx, queueName, items)
		return queueDispatchBatch{}, fmt.Errorf("count alive processes: %w", err)
	}

	distributedAliveCount, err := d.countActiveDistributedRuns(ctx, queueName)
	if err != nil {
		logger.Error(ctx, "Failed to count distributed leases", tag.Error(err), tag.Queue(queueName))
		d.recordQueueStateUnavailableConditions(ctx, queueName, items)
		return queueDispatchBatch{}, fmt.Errorf("count distributed leases: %w", err)
	}
	aliveCount := localAliveCount + distributedAliveCount
	outstandingDispatchCount := 0
	if d.dispatchAdmissionStore == nil {
		outstandingDispatchCount, err = d.countOutstandingDispatchReservations(ctx, queueName)
		if err != nil {
			logger.Error(ctx, "Failed to count outstanding distributed dispatch reservations", tag.Error(err), tag.Queue(queueName))
			d.recordQueueStateUnavailableConditions(ctx, queueName, items)
			return queueDispatchBatch{}, fmt.Errorf("count outstanding distributed dispatch reservations: %w", err)
		}
	}
	nonAdmissionOccupancy := localAliveCount + inflightCount
	freeSlots := maxConcurrency - aliveCount - inflightCount - outstandingDispatchCount

	logger.Debug(ctx, "Queue capacity check",
		tag.MaxConcurrency(maxConcurrency),
		tag.Alive(aliveCount),
		slog.Int("outstanding-dispatches", outstandingDispatchCount),
		tag.Count(freeSlots),
	)

	if freeSlots <= 0 {
		logger.Debug(ctx, "Max concurrency reached",
			tag.MaxConcurrency(maxConcurrency),
			tag.Alive(aliveCount),
		)
		d.recordCapacityUnavailableConditions(ctx, queueName, items, outstandingDispatchCount > 0)
		return queueDispatchBatch{}, nil
	}

	runnableItems, err := d.selectRunnableQueueItemsInQueue(ctx, queueName, items, freeSlots)
	if err != nil {
		logger.Error(ctx, "Failed to select runnable queue items", tag.Error(err), tag.Queue(queueName))
		return queueDispatchBatch{}, fmt.Errorf("select runnable queue items: %w", err)
	}
	if len(runnableItems) == 0 {
		logger.Debug(ctx, "No queue items eligible for a new dispatch attempt")
		return queueDispatchBatch{}, nil
	}

	return queueDispatchBatch{
		items:                 runnableItems,
		maxConcurrency:        maxConcurrency,
		aliveCount:            aliveCount,
		nonAdmissionOccupancy: nonAdmissionOccupancy,
	}, nil
}

func (d *queueDispatcher) dispatchQueuedItem(
	ctx context.Context,
	item queuedomain.QueuedItemData,
	queueName string,
	batch queueDispatchBatch,
	incInflight,
	decInflight func(),
) bool {
	if d.isClosed() {
		return false
	}

	data, err := item.Data()
	if err != nil {
		logger.Error(ctx, "Failed to get item data", tag.Error(err))
		return false
	}

	runRef := *data
	runID := runRef.ID
	ctx = logger.WithValues(ctx, tag.RunID(runID))
	logger.Debug(ctx, "Processing queue item", tag.DAG(runRef.Name))

	running, err := d.procRepository.IsRunAlive(ctx, queueName, runRef)
	if err != nil {
		logger.Error(ctx, "Failed to check if run is alive", tag.Error(err))
		return false
	}
	if running {
		logger.Warn(ctx, "DAG run is already running, discarding")
		return true
	}

	attempt, err := d.dagRunRepository.FindAttempt(ctx, runRef)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			logger.Error(ctx, "DAG run not found, discarding")
			return true
		}
		logger.Error(ctx, "Failed to find run", tag.Error(err))
		return false
	}

	if attempt.Hidden() {
		logger.Info(ctx, "DAG run is hidden, discarding")
		return true
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrCorruptedStatusData) {
			logger.Error(ctx, "Status file is corrupted, marking as invalid", tag.Error(err))
			return true
		}
		logger.Error(ctx, "Failed to read status", tag.Error(err))
		return false
	}

	if status.Status != ir.Queued {
		logger.Info(ctx, "Status is not queued, skipping", tag.Status(status.Status.String()))
		return true
	}

	conditionStage := d.newQueuedConditionStage(runRef, queueName, item.ID(), attempt, status)
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to read DAG", tag.Error(err), tag.DAG(runRef.Name))
		conditionStage.observe(dagSnapshotUnavailableConditionDefs...)
		conditionStage.flush(ctx)
		return false
	}

	if isSchedulerManagedTriggerType(status.TriggerType) {
		suspended, err := isSuspendedDAG(ctx, d.isSuspended, status, dag, "")
		if err != nil {
			logger.Error(ctx, "Failed to check DAG suspension; leaving queued run pending", tag.Error(err))
			return false
		}
		if suspended {
			if err := d.dropSuspendedQueuedRun(ctx, queueName, runRef, attempt.ID(), status); err != nil {
				logger.Error(ctx, "Failed to drop suspended queued DAG run", tag.Error(err))
			}
			return false
		}
	}

	if schedTime, err := time.Parse(time.RFC3339, status.ScheduleTime); err == nil {
		if queueAge := time.Since(schedTime); queueAge > queueAgeWarningThreshold {
			logger.Warn(ctx, "Queued item has been waiting for dispatch",
				tag.DAG(runRef.Name),
				slog.Duration("queue_age", queueAge),
			)
		}
	}

	incInflight()
	defer decInflight()

	if d.dagExecutor.IsDistributed(dag) {
		token, reserved := d.reserveDistributedAdmission(ctx, queueName, runRef, attempt, dispatchAdmissionInput{
			status:                status,
			maxConcurrency:        batch.maxConcurrency,
			nonAdmissionOccupancy: batch.nonAdmissionOccupancy,
		}, conditionStage)
		if !reserved {
			return false
		}
		return d.dispatchAndWaitForStartupWithConditions(ctx, queueName, runRef, dag, runID, status, token, conditionStage)
	}

	execErrCh := make(chan error, 1)
	execDoneCh := make(chan struct{})
	var execDoneErr error
	go func() {
		defer d.wakeUp()
		err := d.dagExecutor.ExecuteDAG(ctx, dag, dispatch.DispatchOperationRetry, runID, status, status.TriggerType, status.ScheduleTime)
		execDoneErr = err
		close(execDoneCh)
		if err != nil {
			if isPreStartExecutionFailure(err) {
				select {
				case execErrCh <- err:
				default:
				}
				return
			}
			logger.Error(ctx, "Failed to execute DAG", tag.Error(err))
		}
	}()

	return d.waitForStartupWithConditions(ctx, queueName, runRef, startupWaitState{
		launchedAt: time.Now(),
		execErrCh:  execErrCh,
		execDone: func() (bool, error) {
			select {
			case <-execDoneCh:
				return true, execDoneErr
			default:
				return false, nil
			}
		},
	}, conditionStage)
}

func (d *queueDispatcher) dropSuspendedQueuedRun(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	attemptID string,
	status *ir.DAGRunStatus,
) error {
	finishedAt := stringutil.FormatTime(time.Now().UTC())
	currentStatus, swapped, err := d.dagRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		runRef,
		attemptID,
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Aborted
			latest.FinishedAt = finishedAt
			latest.Error = suspendedQueueDropReason
			latest.WorkerID = ""
			latest.PID = 0
			latest.PIDStartedAt = 0
			latest.LeaseAt = 0
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	if err != nil {
		return fmt.Errorf("abort suspended queued DAG run: %w", err)
	}

	if _, err := d.queueStore.DequeueByDAGRunID(ctx, queueName, runRef); err != nil && !errors.Is(err, queuedomain.ErrQueueItemNotFound) {
		return fmt.Errorf("dequeue suspended queued DAG run: %w", err)
	}

	if swapped {
		logger.Info(ctx, "Dropped queued scheduler-managed run for suspended DAG",
			tag.Status(ir.Aborted.String()),
			slog.String("trigger_type", status.TriggerType.String()),
		)
		return nil
	}

	logger.Info(ctx, "Removed stale queued scheduler-managed run for suspended DAG",
		slog.String("trigger_type", status.TriggerType.String()),
		slog.String("current_status", currentStatusString(currentStatus)),
	)
	return nil
}

func (d *queueDispatcher) dispatchAndWaitForStartup(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	dag *ir.DAG,
	runID string,
	dagStatus *ir.DAGRunStatus,
	admissionReservationToken string,
) bool {
	conditionStage := d.newQueuedConditionStage(runRef, queueName, "", nil, dagStatus)
	return d.dispatchAndWaitForStartupWithConditions(ctx, queueName, runRef, dag, runID, dagStatus, admissionReservationToken, conditionStage)
}

func (d *queueDispatcher) dispatchAndWaitForStartupWithConditions(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	dag *ir.DAG,
	runID string,
	dagStatus *ir.DAGRunStatus,
	admissionReservationToken string,
	conditionStage *queuedConditionStage,
) bool {
	launchedAt := time.Now()
	defer d.wakeUp()

	err := d.executeDistributedHandoff(ctx, dag, runID, dagStatus, admissionReservationToken)
	if err != nil {
		d.releaseAdmissionToken(ctx, admissionReservationToken)
		if staleErr, ok := errors.AsType[*queuedomain.StaleQueueDispatchError](err); ok {
			logger.Info(ctx, "Discarding stale distributed queue dispatch",
				tag.DAG(runRef.Name),
				tag.RunID(runRef.ID),
				tag.Queue(queueName),
				tag.Error(staleErr),
			)
			return true
		}
		logger.Warn(ctx, "Failed to dispatch DAG; leaving it queued for the next scan", tag.Error(err))
		if shouldRecordStartupCondition(err) {
			conditionStage.observe(queuedDispatchCondition(err)...)
			conditionStage.flush(ctx)
		}
		return false
	}

	if admissionReservationToken != "" {
		return true
	}
	return d.waitForStartupWithConditions(ctx, queueName, runRef, startupWaitState{
		launchedAt: launchedAt,
	}, conditionStage)
}

func (d *queueDispatcher) executeDistributedHandoff(
	ctx context.Context,
	dag *ir.DAG,
	runID string,
	dagStatus *ir.DAGRunStatus,
	admissionReservationToken string,
) error {
	release, acquired := d.acquireDispatchHandoff(ctx)
	if !acquired {
		return d.checkContextAndQuit(ctx)
	}
	defer release()
	return d.dagExecutor.ExecuteDAGWithAdmission(ctx, dag, dispatch.DispatchOperationRetry,
		runID, dagStatus, dagStatus.TriggerType, dagStatus.ScheduleTime, admissionReservationToken)
}

func (d *queueDispatcher) reserveDistributedAdmission(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	attempt dagrun.Attempt,
	input dispatchAdmissionInput,
	conditionStage *queuedConditionStage,
) (string, bool) {
	if d.dispatchAdmissionStore == nil {
		return "", true
	}
	if input.status == nil {
		return "", false
	}
	attemptID := input.status.AttemptID
	if attemptID == "" && attempt != nil {
		attemptID = attempt.ID()
	}
	attemptKey := queueAttemptKey(runRef, attempt, input.status)
	if attemptKey == "" || attemptID == "" {
		logger.Warn(ctx, "Skipping distributed queue dispatch because admission identity is incomplete",
			tag.RunID(runRef.ID),
			tag.Queue(queueName),
		)
		conditionStage.observe(attemptIdentityMissingConditionDefs...)
		conditionStage.flush(ctx)
		return "", false
	}
	decision, err := d.dispatchAdmissionStore.ReserveAdmission(ctx, dispatch.DispatchAdmissionRequest{
		QueueName:             queueName,
		MaxConcurrency:        input.maxConcurrency,
		NonAdmissionOccupancy: input.nonAdmissionOccupancy,
		AttemptKey:            attemptKey,
		AttemptID:             attemptID,
		DAGRun:                runRef,
		StaleThreshold:        d.leaseStaleThresholdOrDefault(),
	})
	if err != nil {
		logger.Error(ctx, "Failed to reserve distributed queue admission",
			tag.Error(err),
			tag.RunID(runRef.ID),
			tag.Queue(queueName),
		)
		conditionStage.observe(assignmentUnavailableConditionDefs...)
		conditionStage.flush(ctx)
		return "", false
	}
	if decision == nil || !decision.Reserved {
		logReason := ""
		if decision != nil {
			logReason = string(decision.Reason)
		}
		conditionStage.observe(dispatchAdmissionWaitingCondition(decision)...)
		conditionStage.flush(ctx)
		logger.Debug(ctx, "Distributed queue admission rejected",
			tag.RunID(runRef.ID),
			tag.Queue(queueName),
			slog.String("reason", logReason),
		)
		return "", false
	}
	return decision.ReservationToken, true
}

func (d *queueDispatcher) releaseAdmissionToken(ctx context.Context, token string) {
	if token == "" || d.dispatchAdmissionStore == nil {
		return
	}
	err := d.dispatchAdmissionStore.ReleaseAdmissionToken(context.WithoutCancel(ctx), token)
	if err == nil ||
		errors.Is(err, dispatch.ErrDispatchAdmissionConflict) ||
		errors.Is(err, dispatch.ErrDispatchAdmissionNotFound) {
		return
	}
	logger.Warn(ctx, "Failed to release distributed queue admission reservation",
		tag.Error(err),
	)
}

func (d *queueDispatcher) waitForStartup(ctx context.Context, queueName string, runRef ir.DAGRunRef, waitState startupWaitState) bool {
	return d.waitForStartupWithConditions(ctx, queueName, runRef, waitState, nil)
}

func (d *queueDispatcher) waitForStartupWithConditions(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	waitState startupWaitState,
	conditionStage *queuedConditionStage,
) bool {
	policy := backoff.NewExponentialBackoffPolicy(d.backoffConfig.InitialInterval)
	policy.MaxInterval = d.backoffConfig.MaxInterval
	policy.MaxRetries = d.backoffConfig.MaxRetries
	if waitState.execDone != nil {
		policy.MaxRetries = 0
	}

	var started bool
	var startupObservationErrors int
	operation := func(ctx context.Context) error {
		var err error
		started, err = d.checkStartupStatus(ctx, queueName, runRef, waitState)
		if shouldBoundLocalStartupError(waitState, err) {
			startupObservationErrors++
			if d.backoffConfig.MaxRetries > 0 && startupObservationErrors > d.backoffConfig.MaxRetries {
				return backoff.PermanentError(err)
			}
		}
		return err
	}

	if err := backoff.Retry(ctx, operation, policy, nil); err != nil {
		logger.Error(ctx, "Failed to execute DAG after retries", tag.Error(err))
		if shouldRecordStartupCondition(err) {
			if errors.Is(err, errRunLivenessUnavailable) {
				conditionStage.observe(runLivenessUnavailableConditionDefs...)
			} else if localLaunchFailed(err) {
				conditionStage.observe(launchFailedConditionDefs...)
			} else {
				conditionStage.observe(startupNotObservedConditionDefs...)
			}
			if localStartupFailedPermanently(err) {
				if finalizeErr := d.failQueuedRunBeforeStartup(ctx, queueName, runRef, err, conditionStage); finalizeErr != nil {
					logger.Error(ctx, "Failed to finalize queued DAG run after launch failure", tag.Error(finalizeErr))
					conditionStage.flush(ctx)
				}
			} else {
				conditionStage.flush(ctx)
			}
		}
	}

	return started
}

func (d *queueDispatcher) failQueuedRunBeforeStartup(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	failure error,
	conditionStage *queuedConditionStage,
) error {
	attemptID := ""
	itemID := ""
	if conditionStage != nil {
		attemptID = conditionStage.attemptID
		itemID = conditionStage.itemID
	}
	if itemID == "" {
		return errors.New("delete failed DAG run queue item: missing queue item ID")
	}
	if attemptID == "" {
		attempt, err := d.dagRunRepository.FindAttempt(ctx, runRef)
		if err != nil {
			return fmt.Errorf("find queued DAG run attempt: %w", err)
		}
		attemptID = attempt.ID()
	}

	finishedAt := stringutil.FormatTime(time.Now().UTC())
	currentStatus, swapped, err := d.dagRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		runRef,
		attemptID,
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Failed
			latest.FinishedAt = finishedAt
			latest.Error = startupFailureMessage(failure)
			latest.WorkerID = ""
			latest.PID = 0
			latest.PIDStartedAt = 0
			latest.LeaseAt = 0
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	if err != nil {
		return fmt.Errorf("mark queued DAG run as failed: %w", err)
	}
	if !swapped && (currentStatus == nil || currentStatus.Status == ir.Queued) {
		return nil
	}

	if _, err := d.queueStore.DeleteByItemIDs(ctx, queueName, []string{itemID}); err != nil {
		return fmt.Errorf("delete failed DAG run queue item: %w", err)
	}
	return nil
}

func shouldRecordStartupCondition(err error) bool {
	return err != nil &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, errProcessorClosed)
}

func localStartupFailedPermanently(err error) bool {
	if !errors.Is(err, backoff.ErrPermanent) {
		return false
	}
	var startupErr startupExecutionError
	return errors.As(err, &startupErr) || errors.Is(err, errExecutionExitedBeforeStartup)
}

func startupFailureMessage(err error) string {
	if startupErr, ok := errors.AsType[startupExecutionError](err); ok {
		return startupErr.Error()
	}
	return err.Error()
}

func localLaunchFailed(err error) bool {
	if err == nil ||
		errors.Is(err, errNotStarted) ||
		errors.Is(err, errExecutionExitedBeforeStartup) {
		return false
	}
	if _, ok := errors.AsType[startupExecutionError](err); !ok {
		return false
	}
	var exitErr *osexec.ExitError
	return !errors.As(err, &exitErr)
}

func shouldBoundLocalStartupError(waitState startupWaitState, err error) bool {
	return waitState.execDone != nil &&
		err != nil &&
		!errors.Is(err, errNotStarted) &&
		!errors.Is(err, backoff.ErrPermanent)
}

func (d *queueDispatcher) checkStartupStatus(ctx context.Context, queueName string, runRef ir.DAGRunRef, waitState startupWaitState) (bool, error) {
	if err := d.checkContextAndQuit(ctx); err != nil {
		return false, err
	}
	if err := readStartupExecutionError(waitState.execErrCh); err != nil {
		logger.Warn(ctx, "DAG execution failed before startup was observed", tag.Error(err))
		return false, backoff.PermanentError(err)
	}

	isAlive, err := d.procRepository.IsRunAlive(ctx, queueName, runRef)
	livenessErr := err
	if err != nil {
		logger.Warn(ctx, "Failed to check run liveness", tag.Error(err), tag.Queue(queueName), tag.RunID(runRef.ID))
	} else if isAlive {
		logger.Info(ctx, "DAG run has started (heartbeat detected)")
		return true, nil
	}
	execDone, execDoneErr := waitState.executionDone()
	if d.inStartupGracePeriod(waitState.launchedAt) && d.dagRunLeaseStore == nil && !execDone {
		return false, errNotStarted
	}

	attempt, err := d.dagRunRepository.FindAttempt(ctx, runRef)
	if err != nil {
		logger.Debug(ctx, "Failed to read attempt, keep checking")
		return false, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return false, err
	}

	if status.Status != ir.Queued {
		logger.Info(ctx, "DAG execution has started or finished", tag.Status(status.Status.String()))
		return true, nil
	}
	if execDone {
		if execDoneErr != nil {
			return false, backoff.PermanentError(newStartupExecutionError(execDoneErr))
		}
		return false, backoff.PermanentError(errExecutionExitedBeforeStartup)
	}
	started, err := d.hasFreshDistributedLease(ctx, queueName, runRef, attempt, status)
	if err != nil {
		logger.Warn(ctx, "Failed to check distributed run lease",
			tag.Error(err),
			tag.Queue(queueName),
			tag.RunID(runRef.ID),
		)
	} else if started {
		logger.Info(ctx, "DAG run has started (distributed lease detected)")
		return true, nil
	}
	if d.inStartupGracePeriod(waitState.launchedAt) {
		return false, errNotStarted
	}
	if err != nil {
		return false, newRunLivenessUnavailableError(err)
	}
	if livenessErr != nil {
		return false, newRunLivenessUnavailableError(livenessErr)
	}

	return false, errNotStarted
}

type runLivenessUnavailableError struct {
	err error
}

func newRunLivenessUnavailableError(err error) error {
	if err == nil {
		return errRunLivenessUnavailable
	}
	return runLivenessUnavailableError{err: err}
}

func (e runLivenessUnavailableError) Error() string {
	return e.err.Error()
}

func (e runLivenessUnavailableError) Unwrap() error {
	return e.err
}

func (e runLivenessUnavailableError) Is(target error) bool {
	return target == errRunLivenessUnavailable
}

func (d *queueDispatcher) inStartupGracePeriod(launchedAt time.Time) bool {
	grace := d.backoffConfig.StartupGracePeriod
	return grace > 0 && time.Since(launchedAt) < grace
}

func (d *queueDispatcher) selectRunnableQueueItems(
	ctx context.Context,
	items []queuedomain.QueuedItemData,
	freeSlots int,
) ([]queuedomain.QueuedItemData, error) {
	return d.selectRunnableQueueItemsInQueue(ctx, "", items, freeSlots)
}

func (d *queueDispatcher) selectRunnableQueueItemsInQueue(
	ctx context.Context,
	queueName string,
	items []queuedomain.QueuedItemData,
	freeSlots int,
) ([]queuedomain.QueuedItemData, error) {
	if freeSlots <= 0 {
		return nil, nil
	}

	runnable := make([]queuedomain.QueuedItemData, 0, min(freeSlots, len(items)))
	workerSnapshot := workerHeartbeatSnapshot{}
	var workerConditionStages []*queuedConditionStage
	defer func() {
		d.flushQueuedConditionStages(ctx, queueName, workerConditionStages)
	}()
	for _, item := range items {
		if len(runnable) >= freeSlots {
			break
		}
		runRef, err := item.Data()
		if err != nil {
			logger.Error(ctx, "Failed to get item data while selecting runnable queue items", tag.Error(err))
			continue
		}
		if d.dispatchAdmissionStore == nil && d.dispatchTaskStore != nil {
			reserved, err := d.hasOutstandingDispatchReservation(ctx, *runRef)
			if err != nil {
				return nil, err
			}
			if reserved {
				conditionStage := d.newQueuedConditionStageFromItem(ctx, queueName, item)
				conditionStage.observe(assignmentPendingConditionDefs...)
				conditionStage.flush(ctx)
				logger.Debug(ctx, "Skipping queue item with outstanding distributed dispatch reservation",
					tag.RunID(runRef.ID),
				)
				continue
			}
		}
		if d.workerHeartbeatStore != nil {
			recordCondition := len(workerConditionStages) < queuedConditionRefreshBatchLimit
			eligible, conditionStage := d.queueItemHasEligibleWorker(
				ctx, queueName, item, *runRef, &workerSnapshot, recordCondition,
			)
			if !eligible {
				if conditionStage != nil {
					workerConditionStages = append(workerConditionStages, conditionStage)
				}
				continue
			}
		}
		runnable = append(runnable, item)
	}

	return runnable, nil
}

func (d *queueDispatcher) queueItemHasEligibleWorker(
	ctx context.Context,
	queueName string,
	item queuedomain.QueuedItemData,
	runRef ir.DAGRunRef,
	workerSnapshot *workerHeartbeatSnapshot,
	recordCondition bool,
) (bool, *queuedConditionStage) {
	attempt, err := d.dagRunRepository.FindAttempt(ctx, runRef)
	if err != nil || attempt.Hidden() {
		return true, nil
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil || status.Status != ir.Queued {
		return true, nil
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil || !d.dagExecutor.IsDistributed(dag) {
		return true, nil
	}

	records, err := workerSnapshot.load(ctx, d.workerHeartbeatStore)
	var conditionDefs []queuedConditionDef
	if err != nil {
		conditionDefs = assignmentUnavailableConditionDefs
	} else {
		available, defs := workerAvailableInSnapshot(records, time.Now().UTC(), d.workerStaleAfter, dag, status)
		if available {
			return true, nil
		}
		conditionDefs = defs
	}
	if !recordCondition {
		return false, nil
	}

	conditionStage := d.newQueuedConditionStage(runRef, queueName, item.ID(), attempt, status)
	conditionStage.observe(conditionDefs...)
	return false, conditionStage
}

func (d *queueDispatcher) flushQueuedConditionStages(
	ctx context.Context,
	queueName string,
	stages []*queuedConditionStage,
) {
	if len(stages) == 0 {
		return
	}
	if d.queueStore == nil || queueName == "" {
		for _, stage := range stages {
			stage.flushWithoutQueueCheck(ctx)
		}
		return
	}

	items, err := d.queueStore.List(ctx, queueName)
	if err != nil {
		logger.Warn(ctx, "Failed to verify queued items before updating worker conditions", tag.Error(err))
		return
	}
	stagesByItemID := make(map[string]*queuedConditionStage, len(stages))
	for _, stage := range stages {
		if stage != nil {
			stagesByItemID[stage.itemID] = stage
		}
	}
	for _, item := range items {
		stage, ok := stagesByItemID[item.ID()]
		if !ok {
			continue
		}
		runRef, err := item.Data()
		if err != nil {
			logger.Warn(ctx, "Failed to read queued item before updating worker conditions", tag.Error(err))
			continue
		}
		if runRef == nil || *runRef != stage.runRef {
			continue
		}
		stage.flushWithoutQueueCheck(ctx)
	}
}

func workerAvailableInSnapshot(
	records []dispatch.WorkerHeartbeatRecord,
	now time.Time,
	staleThreshold time.Duration,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
) (bool, []queuedConditionDef) {
	targetWorkerID := ir.RetryAgentOwnerWorkerID(status, false)
	healthyWorkers := 0
	for _, record := range records {
		if !dispatch.WorkerHeartbeatFresh(record, now, staleThreshold) {
			continue
		}
		healthyWorkers++
		if dispatch.WorkerHeartbeatMatches(record, dag.WorkerSelector, targetWorkerID) {
			return true, nil
		}
	}
	if healthyWorkers == 0 {
		return false, noAvailableWorkerConditionDefs
	}
	return false, noMatchingWorkerConditionDefs
}

func dispatchAdmissionWaitingCondition(decision *dispatch.DispatchAdmissionDecision) []queuedConditionDef {
	if decision == nil {
		return assignmentPendingConditionDefs
	}
	switch decision.Reason {
	case dispatch.DispatchAdmissionRejectedNoCapacity:
		return maxConcurrencyReachedConditionDefs
	case dispatch.DispatchAdmissionRejectedDuplicate:
		return assignmentPendingConditionDefs
	default:
		return assignmentPendingConditionDefs
	}
}

func queuedDispatchCondition(err error) []queuedConditionDef {
	if isNoMatchingWorker(err) {
		return noMatchingWorkerConditionDefs
	}
	if isNoAvailableWorker(err) {
		return noAvailableWorkerConditionDefs
	}
	return workerDispatchUnavailableConditionDefs
}

func isNoMatchingWorker(err error) bool {
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "no workers match the required selector") {
		return true
	}
	return strings.Contains(strings.ToLower(errorMessage(err)), "no workers match the required selector")
}

func isNoAvailableWorker(err error) bool {
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Unavailable && strings.Contains(st.Message(), "no available workers") {
		return true
	}
	return strings.Contains(strings.ToLower(errorMessage(err)), "no available workers")
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (d *queueDispatcher) recordCapacityUnavailableConditions(
	ctx context.Context,
	queueName string,
	items []queuedomain.QueuedItemData,
	checkOutstandingDispatch bool,
) {
	items = d.queuedConditionItemsForRefresh(queueName, items)
	for _, item := range items {
		conditionStage := d.newQueuedConditionStageFromItem(ctx, queueName, item)
		if conditionStage == nil {
			continue
		}
		defs := maxConcurrencyReachedConditionDefs
		if checkOutstandingDispatch {
			reserved, err := d.hasOutstandingDispatchReservation(ctx, conditionStage.runRef)
			if err != nil {
				logger.Warn(ctx, "Failed to check outstanding dispatch reservation while updating queued condition",
					tag.Error(err),
					tag.RunID(conditionStage.runRef.ID),
				)
			} else if reserved {
				defs = maxConcurrencyReachedWithAssignmentPendingConditionDefs()
			}
		}
		conditionStage.observe(defs...)
		conditionStage.flush(ctx)
	}
}

func (d *queueDispatcher) recordQueueStateUnavailableConditions(
	ctx context.Context,
	queueName string,
	items []queuedomain.QueuedItemData,
) {
	items = d.queuedConditionItemsForRefresh(queueName, items)
	for _, item := range items {
		conditionStage := d.newQueuedConditionStageFromItem(ctx, queueName, item)
		conditionStage.observe(queueStateUnavailableConditionDefs...)
		conditionStage.flush(ctx)
	}
}

func queuedConditionNeedsUpdate(
	status *ir.DAGRunStatus,
	observations []ir.DAGRunCondition,
) bool {
	if status == nil || len(observations) == 0 {
		return false
	}
	if hasNewerQueuedCondition(status.Conditions, observations) {
		return false
	}
	if hasUnobservedQueuedConditionType(status.Conditions, observations) {
		return true
	}
	for _, observation := range observations {
		if queuedConditionObservationNeedsUpdate(status, observation) {
			return true
		}
	}
	return false
}

func hasNewerQueuedCondition(
	conditions []ir.DAGRunCondition,
	observations []ir.DAGRunCondition,
) bool {
	newestObservedAt, ok := newestConditionCheckedAt(observations)
	if !ok {
		return false
	}
	for _, condition := range conditions {
		if !isQueuedConditionType(condition.Type) {
			continue
		}
		checkedAt, ok := conditionCheckedAt(condition)
		if ok && checkedAt.After(newestObservedAt) {
			return true
		}
	}
	return false
}

func newestConditionCheckedAt(conditions []ir.DAGRunCondition) (time.Time, bool) {
	var newest time.Time
	for _, condition := range conditions {
		checkedAt, ok := conditionCheckedAt(condition)
		if !ok {
			continue
		}
		if newest.IsZero() || checkedAt.After(newest) {
			newest = checkedAt
		}
	}
	return newest, !newest.IsZero()
}

func conditionCheckedAt(condition ir.DAGRunCondition) (time.Time, bool) {
	checkedAt, err := stringutil.ParseTime(condition.CheckedAt)
	return checkedAt, err == nil && !checkedAt.IsZero()
}

func mergeQueuedConditionObservations(
	conditions []ir.DAGRunCondition,
	observations []ir.DAGRunCondition,
) []ir.DAGRunCondition {
	return ir.MergeDAGRunConditions(withoutQueuedConditionTypes(conditions), observations...)
}

func withoutQueuedConditionTypes(conditions []ir.DAGRunCondition) []ir.DAGRunCondition {
	if !hasQueuedConditionType(conditions) {
		return conditions
	}
	filtered := make([]ir.DAGRunCondition, 0, len(conditions))
	for _, condition := range conditions {
		if isQueuedConditionType(condition.Type) {
			continue
		}
		filtered = append(filtered, condition)
	}
	return filtered
}

func hasQueuedConditionType(conditions []ir.DAGRunCondition) bool {
	for _, condition := range conditions {
		if isQueuedConditionType(condition.Type) {
			return true
		}
	}
	return false
}

func hasUnobservedQueuedConditionType(
	conditions []ir.DAGRunCondition,
	observations []ir.DAGRunCondition,
) bool {
	observed := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		observed[observation.Type] = struct{}{}
	}
	for _, condition := range conditions {
		if !isQueuedConditionType(condition.Type) {
			continue
		}
		if _, ok := observed[condition.Type]; !ok {
			return true
		}
	}
	return false
}

func isQueuedConditionType(conditionType string) bool {
	switch conditionType {
	case "Queued",
		dagRunConditionRunnable,
		dagRunConditionConcurrencyReady,
		dagRunConditionWorkerAssignmentReady,
		dagRunConditionRunRecordReady,
		dagRunConditionWorkerReady,
		dagRunConditionStartObserved,
		dagRunConditionQueueReady:
		return true
	default:
		return false
	}
}

func queuedConditionObservationNeedsUpdate(status *ir.DAGRunStatus, observation ir.DAGRunCondition) bool {
	observedAt, ok := conditionCheckedAt(observation)
	if !ok {
		return true
	}
	current, ok := queuedConditionByType(status.Conditions, observation.Type)
	if !ok {
		return true
	}
	currentAt, ok := conditionCheckedAt(current)
	if !ok {
		return true
	}
	if currentAt.After(observedAt) {
		return false
	}
	if current.Status != observation.Status ||
		current.Reason != observation.Reason ||
		current.Message != observation.Message {
		return true
	}
	return observedAt.Sub(currentAt) >= queuedConditionRefreshInterval
}

func queuedConditionByType(conditions []ir.DAGRunCondition, conditionType string) (ir.DAGRunCondition, bool) {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition, true
		}
	}
	return ir.DAGRunCondition{}, false
}

func (d *queueDispatcher) hasOutstandingDispatchReservation(ctx context.Context, runRef ir.DAGRunRef) (bool, error) {
	if d.dispatchTaskStore == nil {
		return false, nil
	}

	attempt, err := d.dagRunRepository.FindAttempt(ctx, runRef)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return false, nil
		}
		return false, err
	}
	if attempt.Hidden() {
		return false, nil
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrNoStatusData) || errors.Is(err, dagrun.ErrCorruptedStatusData) {
			return false, nil
		}
		return false, err
	}
	if status == nil || status.Status != ir.Queued {
		return false, nil
	}

	attemptKey := queueAttemptKey(runRef, attempt, status)
	if attemptKey == "" {
		return false, nil
	}
	return d.dispatchTaskStore.HasOutstandingAttempt(ctx, attemptKey, d.leaseStaleThresholdOrDefault())
}

func (d *queueDispatcher) countActiveDistributedRuns(ctx context.Context, queueName string) (int, error) {
	if d.dagRunLeaseStore == nil {
		return 0, nil
	}

	leases, err := d.dagRunLeaseStore.ListByQueue(ctx, queueName)
	if err != nil {
		return 0, fmt.Errorf("list distributed leases for queue %q: %w", queueName, err)
	}

	count := 0
	staleThreshold := d.leaseStaleThresholdOrDefault()
	now := time.Now().UTC()
	for _, lease := range leases {
		if lease.IsFresh(now, staleThreshold) {
			count++
		}
	}
	return count, nil
}

func (d *queueDispatcher) countOutstandingDispatchReservations(ctx context.Context, queueName string) (int, error) {
	if d.dispatchTaskStore == nil {
		return 0, nil
	}
	count, err := d.dispatchTaskStore.CountOutstandingByQueue(ctx, queueName, d.leaseStaleThresholdOrDefault())
	if err != nil {
		return 0, fmt.Errorf("list outstanding distributed dispatches for queue %q: %w", queueName, err)
	}
	return count, nil
}

func (d *queueDispatcher) hasFreshDistributedLease(
	ctx context.Context,
	queueName string,
	runRef ir.DAGRunRef,
	attempt dagrun.Attempt,
	status *ir.DAGRunStatus,
) (bool, error) {
	if d.dagRunLeaseStore == nil || status == nil {
		return false, nil
	}

	attemptID := status.AttemptID
	if attemptID == "" && attempt != nil {
		attemptID = attempt.ID()
	}
	attemptKey := queueAttemptKey(runRef, attempt, status)
	if attemptKey == "" {
		return false, nil
	}

	lease, err := d.dagRunLeaseStore.Get(ctx, attemptKey)
	if err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
			return false, nil
		}
		return false, err
	}
	if lease == nil {
		return false, nil
	}
	if lease.DAGRun != runRef {
		return false, nil
	}
	if queueName != "" && lease.QueueName != "" && lease.QueueName != queueName {
		return false, nil
	}
	if attemptID != "" && lease.AttemptID != "" && lease.AttemptID != attemptID {
		return false, nil
	}

	return lease.IsFresh(time.Now().UTC(), d.leaseStaleThresholdOrDefault()), nil
}

func (d *queueDispatcher) leaseStaleThresholdOrDefault() time.Duration {
	if d.leaseStaleThreshold <= 0 {
		return dagrun.DefaultStaleLeaseThreshold
	}
	return d.leaseStaleThreshold
}

func (d *queueDispatcher) checkContextAndQuit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		logger.Debug(ctx, "Context canceled")
		return backoff.PermanentError(ctx.Err())
	default:
	}
	if d.isClosed() {
		logger.Info(ctx, "Processor is closed")
		return backoff.PermanentError(errProcessorClosed)
	}
	return nil
}

// isPreStartExecutionFailure reports whether an execution error proves the DAG
// never reached an observable started state. Spawn and dispatch failures should
// abort the startup wait immediately, while process exit errors should continue
// to rely on heartbeat/status because the attempt did start.
func isPreStartExecutionFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var exitErr *osexec.ExitError
	return !errors.As(err, &exitErr)
}
