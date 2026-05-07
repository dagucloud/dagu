// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/sock"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/google/uuid"
)

const staleLocalRunStartupGrace = 2 * time.Second

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithManagerClock overrides the Manager clock.
func WithManagerClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.nowFunc = now
		}
	}
}

// NewManager creates a new Manager instance.
// The Manager is used to interact with the DAG.
func NewManager(drs exec.DAGRunStore, ps exec.ProcStore, cfg *config.Config, opts ...ManagerOption) Manager {
	m := Manager{
		dagRunStore:   drs,
		procStore:     ps,
		subCmdBuilder: NewSubCmdBuilder(cfg),
		nowFunc:       time.Now,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// Manager provides methods to interact with DAGs, including starting, stopping,
// restarting, and retrieving status information. It communicates with the DAG
// through a socket interface and manages dag-run data.
type Manager struct {
	dagRunStore   exec.DAGRunStore // Store interface for persisting run data
	procStore     exec.ProcStore   // Store interface for process management
	subCmdBuilder *SubCmdBuilder   // Command builder for constructing command specs
	nowFunc       func() time.Time
}

// Stop stops running DAG-runs and can cancel an explicit failed DAG-run that is
// still pending DAG-level auto-retry when dagRunID is provided.
func (m *Manager) Stop(ctx context.Context, dag *core.DAG, dagRunID string) error {
	// Set DAG name in context for all logs in this function
	ctx = logger.WithValues(ctx, tag.Name(dag.Name))
	logger.Info(ctx, "Stopping DAG")

	if dagRunID == "" {
		// If DAGRunID is not specified, stop all matching DAG runs
		// Get the list of running DAG runs for the queue proc name
		aliveRuns, err := m.procStore.ListAlive(ctx, dag.ProcGroup())
		if err != nil {
			return fmt.Errorf("failed to list alive DAG runs: %w", err)
		}

		// If no runs are alive, nothing to stop
		if len(aliveRuns) == 0 {
			logger.Info(ctx, "No running DAG runs found")
			return nil
		}

		// Collect all matching DAG run IDs
		var matchingRunIDs []string
		for _, runRef := range aliveRuns {
			// Find the attempt for this run
			attempt, err := m.dagRunStore.FindAttempt(ctx, runRef)
			if err != nil {
				logger.Warn(ctx, "Failed to find attempt for running DAG",
					slog.String("run-ref", runRef.String()),
					tag.Error(err),
				)
				continue
			}

			// Read the DAG to check if it matches
			runDAG, err := attempt.ReadDAG(ctx)
			if err != nil {
				logger.Warn(ctx, "Failed to read DAG for running attempt",
					slog.String("run-ref", runRef.String()),
					tag.Error(err),
				)
				continue
			}

			// Check if the DAG name matches
			if runDAG.Name == dag.Name {
				matchingRunIDs = append(matchingRunIDs, runRef.ID)
				logger.Info(ctx, "Found matching DAG run to stop",
					tag.RunID(runRef.ID),
				)
			}
		}

		// If no matching DAGs were found
		if len(matchingRunIDs) == 0 {
			logger.Info(ctx, "No matching DAG run found to stop")
			return nil
		}

		// Stop all matching DAG runs
		var stopErrors []error
		for _, runID := range matchingRunIDs {
			if err := m.stopSingleDAGRun(ctx, dag, runID); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("failed to stop DAG run %s: %w", runID, err))
			}
		}

		// If any errors occurred, return them
		if len(stopErrors) > 0 {
			return fmt.Errorf("errors occurred while stopping DAG runs: %v", stopErrors)
		}

		return nil
	}

	// If dagRunID is specified, stop just that specific run
	return m.stopSingleDAGRun(ctx, dag, dagRunID)
}

// stopSingleDAGRun stops a single DAG run by its ID. For explicit run IDs, it
// can also cancel a failed root run that is waiting for DAG-level auto-retry.
func (m *Manager) stopSingleDAGRun(ctx context.Context, dag *core.DAG, dagRunID string) error {
	// Set run ID in context for all logs in this function
	ctx = logger.WithValues(ctx, tag.RunID(dagRunID))

	// Check if the process is running locally using proc store
	alive, err := m.procStore.IsRunAlive(ctx, dag.ProcGroup(), exec.NewDAGRunRef(dag.Name, dagRunID))
	if err != nil {
		return fmt.Errorf("failed to retrieve status from proc store: %w", err)
	}

	// If running locally, try to stop via socket first
	if alive {
		addr := dag.SockAddr(dagRunID)
		if fileutil.FileExists(addr) {
			// In case the socket exists, we try to send a stop request
			client := sock.NewClient(addr)
			if _, err := client.Request("POST", "/stop"); err == nil {
				logger.Info(ctx, "Successfully stopped DAG via socket")
				return nil
			}
			logger.Debug(ctx, "Socket stop request failed, will use abort flag",
				tag.Error(err))
		}
	}

	runRef := exec.NewDAGRunRef(dag.Name, dagRunID)
	run, err := m.dagRunStore.FindAttempt(ctx, runRef)
	if err == nil {
		status, statusErr := run.ReadStatus(ctx)
		if statusErr == nil && exec.CanCancelFailedAutoRetryPendingRun(status) {
			if err := exec.CancelFailedAutoRetryPendingRun(ctx, m.dagRunStore, status); err != nil {
				return fmt.Errorf("failed to cancel pending auto-retry for dag-run %s: %w", dagRunID, err)
			}
			logger.Info(ctx, "Canceled pending auto-retry for failed DAG run")
			return nil
		}

		// Request cancel for active runs (works for both local and distributed
		// execution). This creates an abort flag that the runner or coordinator
		// can detect on heartbeat.
		if err := run.Abort(ctx); err != nil {
			return fmt.Errorf("failed to request cancel for dag-run %s: %w", dagRunID, err)
		}
		logger.Info(ctx, "Abort flag created for DAG run")
		return nil
	}

	// If we couldn't find the attempt and the process isn't running locally, nothing to do
	if !alive {
		logger.Info(ctx, "The DAG is not running locally and no attempt found")
		return nil
	}

	// Process is alive but we couldn't find the attempt - this shouldn't happen
	return fmt.Errorf("failed to find dag-run attempt: %w", err)
}

// GenDAGRunID generates a unique ID for a dag-run using UUID version 7.
func (m *Manager) GenDAGRunID(_ context.Context) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("failed to generate dag-run ID: %w", err)
	}
	return id.String(), nil
}

// IsRunning checks if a dag-run is currently running. It prefers the live socket
// status and falls back to a fresh proc heartbeat plus persisted running status.
func (m *Manager) IsRunning(ctx context.Context, dag *core.DAG, dagRunID string) bool {
	st, _ := m.currentStatus(ctx, dag, dagRunID)
	if st != nil && st.DAGRunID == dagRunID && st.Status == core.Running {
		return true
	}

	runRef := exec.NewDAGRunRef(dag.Name, dagRunID)
	if alive, err := m.procStore.IsRunAlive(ctx, dag.ProcGroup(), runRef); err == nil && alive {
		st, err := m.getPersistedOrCurrentStatus(ctx, dag, dagRunID)
		if err != nil {
			return false
		}
		return st.DAGRunID == dagRunID && st.Status == core.Running
	}

	return false
}

// GetCurrentStatus retrieves the current status of a dag-run by its run ID.
// If the dag-run is running, it queries the socket for the current status.
// If the socket doesn't exist or times out, it falls back to stored status or creates an initial status.
func (m *Manager) GetCurrentStatus(ctx context.Context, dag *core.DAG, dagRunID string) (*exec.DAGRunStatus, error) {
	if dagRunID == "" {
		status, err := m.currentStatus(ctx, dag, dagRunID)
		if err == nil {
			return status, nil
		}
		// The DAG is not running so return the default status
		return new(exec.InitialStatus(dag)), nil
	}
	status, err := m.getPersistedOrCurrentStatus(ctx, dag, dagRunID)
	if err == nil {
		return status, nil
	}
	currentStatus, currentErr := m.currentStatus(ctx, dag, dagRunID)
	if currentErr == nil {
		return currentStatus, nil
	}
	return nil, err
}

// GetSavedStatus retrieves the saved status of a dag-run by its core.DAGRun reference.
// For stale local runs, it repairs the persisted status before returning it.
func (m *Manager) GetSavedStatus(ctx context.Context, dagRun exec.DAGRunRef) (*exec.DAGRunStatus, error) {
	attempt, err := m.dagRunStore.FindAttempt(ctx, dagRun)
	if err != nil {
		return nil, fmt.Errorf("failed to find status by run reference: %w", err)
	}
	st, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read status: %w", err)
	}

	if dagRun.ID == st.DAGRunID && st.Status == core.Running {
		var dag *core.DAG
		if isLocalWorkerID(st.WorkerID) {
			dag, err = attempt.ReadDAG(ctx)
			if err != nil {
				logger.Error(ctx, "Failed to read DAG for stale status check", tag.Error(err))
			}
		}
		st = m.resolveRunningStatus(ctx, dag, attempt, st, true)
	}

	return st, nil
}

// getPersistedOrCurrentStatus retrieves the persisted status of a dag-run by its ID.
// If the stored status indicates the DAG is running, it attempts to get the current status.
// If current status retrieval fails and the local proc is dead, it repairs the stale run.
func (m *Manager) getPersistedOrCurrentStatus(ctx context.Context, dag *core.DAG, dagRunID string) (
	*exec.DAGRunStatus, error,
) {
	dagRunRef := exec.NewDAGRunRef(dag.Name, dagRunID)
	attempt, err := m.dagRunStore.FindAttempt(ctx, dagRunRef)
	if err != nil {
		return nil, fmt.Errorf("failed to find status by dag-run ID: %w", err)
	}
	st, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read status: %w", err)
	}

	if st.Status == core.Running {
		st = m.resolveRunningStatus(ctx, dag, attempt, st, true)
	}

	return st, nil
}

// FindSubDAGRunStatus retrieves the status of a sub dag-run by its ID.
// It looks up the child attempt in the dag-run store and reads its status.
func (m *Manager) FindSubDAGRunStatus(ctx context.Context, rootDAGRun exec.DAGRunRef, subRunID string) (*exec.DAGRunStatus, error) {
	attempt, err := m.dagRunStore.FindSubAttempt(ctx, rootDAGRun, subRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to find sub dag-run attempt: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read status: %w", err)
	}
	return status, nil
}

// currentStatus retrieves the current status of a running DAG by querying its socket.
// This is a private method used internally by other status-related methods.
func (*Manager) currentStatus(_ context.Context, dag *core.DAG, dagRunID string) (*exec.DAGRunStatus, error) {
	client := sock.NewClient(dag.SockAddr(dagRunID))

	// Check if the socket file exists
	if !fileutil.FileExists(client.SocketAddr()) {
		return nil, fmt.Errorf("socket file does not exist for dag-run ID %s", dagRunID)
	}

	statusJSON, err := client.Request("GET", "/status")
	if err != nil {
		return nil, fmt.Errorf("failed to get current status: %w", err)
	}

	return exec.StatusFromJSON(statusJSON)
}

func isLocalWorkerID(workerID string) bool {
	return workerID == "" || workerID == "local"
}

func (m *Manager) findAttemptForProcEntry(ctx context.Context, entry exec.ProcEntry) (exec.DAGRunAttempt, error) {
	if entry.IsRoot() {
		return m.dagRunStore.FindAttempt(ctx, entry.DAGRun())
	}
	return m.dagRunStore.FindSubAttempt(ctx, entry.Meta.Root(), entry.Meta.DAGRunID)
}

func (m *Manager) resolveRunningStatus(
	ctx context.Context,
	dag *core.DAG,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
	isRoot bool,
) *exec.DAGRunStatus {
	if status == nil || status.Status != core.Running || !isLocalWorkerID(status.WorkerID) {
		return status
	}

	if isRoot {
		currentStatus, err := m.currentStatus(ctx, dag, status.DAGRunID)
		if err == nil {
			return currentStatus
		}
		logger.Debug(ctx, "Failed to get current status from socket", tag.Error(err))
	}

	repaired, repairErr := m.repairStaleLocalRunIfDead(ctx, attempt, dag, status)
	if repairErr != nil {
		logger.Error(ctx, "Failed to repair stale running status", tag.Error(repairErr))
		return status
	}
	return repaired
}

// GetLatestStatus retrieves the latest status of a DAG.
// If the DAG is running, it attempts to get the current status from the socket.
// If that fails and the local proc is dead, it repairs the stale run before returning it.
func (m *Manager) GetLatestStatus(ctx context.Context, dag *core.DAG) (exec.DAGRunStatus, error) {
	if entry, err := m.procStore.LatestFreshEntryByDAGName(ctx, dag.ProcGroup(), dag.Name); err == nil && entry != nil {
		attempt, findErr := m.findAttemptForProcEntry(ctx, *entry)
		if findErr == nil {
			st, readErr := attempt.ReadStatus(ctx)
			if readErr == nil && st.AttemptID == entry.Meta.AttemptID {
				st = m.resolveRunningStatus(ctx, dag, attempt, st, entry.IsRoot())
				return *st, nil
			}
		}
	} else if err != nil {
		logger.Debug(ctx, "Failed to resolve freshest proc entry for latest status", tag.Error(err))
	}

	// Find the latest status by name
	attempt, err := m.dagRunStore.LatestAttempt(ctx, dag.Name)
	if err != nil {
		// If the latest status is not found, return the default status
		ret := exec.InitialStatus(dag)
		return ret, nil
	}

	// Read the latest status
	st, err := attempt.ReadStatus(ctx)
	if err != nil {
		// If the latest status is not found, return the default status
		ret := exec.InitialStatus(dag)
		return ret, nil
	}

	if st.Status == core.Running && isLocalWorkerID(st.WorkerID) {
		runDAG, err := attempt.ReadDAG(ctx)
		if err != nil {
			logger.Debug(ctx, "Failed to read DAG for current status lookup", tag.Error(err))
		} else {
			dag = runDAG
		}
	}
	if st.Status == core.Running {
		st = m.resolveRunningStatus(ctx, dag, attempt, st, st.Parent.Zero())
	}

	return *st, nil
}

// repairStaleLocalRunIfDead repairs a persisted local Running status only when
// the run has no matching fresh proc heartbeat. "Dead" here means the proc
// store cannot find any non-stale heartbeat file for the local run; it is not
// an OS-level PID liveness check. Distributed runs are excluded because local
// proc heartbeats are not authoritative for remote workers.
func (m *Manager) repairStaleLocalRunIfDead(
	ctx context.Context,
	attempt exec.DAGRunAttempt,
	dag *core.DAG,
	st *exec.DAGRunStatus,
) (*exec.DAGRunStatus, error) {
	if !isLocalWorkerID(st.WorkerID) {
		return st, nil
	}
	if shouldDelayStaleLocalRunRepair(st, m.currentTime()) {
		logger.Debug(ctx, "Skipping stale local run repair during startup grace window",
			tag.RunID(st.DAGRunID),
			slog.String("started-at", st.StartedAt),
			slog.Duration("grace", staleLocalRunStartupGrace),
		)
		return st, nil
	}

	alive, err := m.procStore.IsAttemptAlive(ctx, dag.ProcGroup(), st.DAGRun(), st.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("check alive: %w", err)
	}
	if alive {
		return st, nil
	}

	runAlive, err := m.procStore.IsRunAlive(ctx, dag.ProcGroup(), st.DAGRun())
	if err != nil {
		return nil, fmt.Errorf("check run alive: %w", err)
	}
	if runAlive {
		logger.Debug(ctx, "Skipping stale local run repair because DAG run still has a fresh proc heartbeat",
			tag.RunID(st.DAGRunID),
			tag.AttemptID(st.AttemptID),
		)
		return st, nil
	}

	repaired, _, err := RepairStaleLocalRun(ctx, attempt, dag)
	if err != nil {
		return nil, fmt.Errorf("repair stale local run: %w", err)
	}
	return repaired, nil
}

func shouldDelayStaleLocalRunRepair(st *exec.DAGRunStatus, now time.Time) bool {
	startedAt, ok := statusStartTime(st)
	if !ok {
		return false
	}
	age := now.Sub(startedAt)
	return age >= 0 && age < staleLocalRunStartupGrace
}

func statusStartTime(st *exec.DAGRunStatus) (time.Time, bool) {
	if startedAt, err := stringutil.ParseTime(st.StartedAt); err == nil && !startedAt.IsZero() {
		return startedAt, true
	}
	if st.CreatedAt > 0 {
		return time.UnixMilli(st.CreatedAt), true
	}
	return time.Time{}, false
}

func (m *Manager) currentTime() time.Time {
	if m.nowFunc == nil {
		return time.Now()
	}
	return m.nowFunc()
}

// ListRecentStatus retrieves the n most recent statuses for a DAG by name.
// It returns a slice of Status objects, filtering out any that cannot be read.
func (m *Manager) ListRecentStatus(ctx context.Context, name string, n int) []exec.DAGRunStatus {
	attempts := m.dagRunStore.RecentAttempts(ctx, name, n)

	var statuses []exec.DAGRunStatus
	for _, att := range attempts {
		if status, err := att.ReadStatus(ctx); err == nil {
			statuses = append(statuses, *status)
		}
	}

	return statuses
}

// UpdateStatus updates the status of a dag-run.
func (m *Manager) UpdateStatus(ctx context.Context, rootDAGRun exec.DAGRunRef, newStatus exec.DAGRunStatus) error {
	// Find the attempt for the status.
	var attempt exec.DAGRunAttempt

	if rootDAGRun.ID == newStatus.DAGRunID {
		// If the dag-run ID matches the root dag-run ID, find the attempt by the root dag-run ID
		att, err := m.dagRunStore.FindAttempt(ctx, rootDAGRun)
		if err != nil {
			return fmt.Errorf("failed to find the dag-run: %w", err)
		}
		attempt = att
	} else {
		// If the dag-run ID does not match the root dag-run ID,
		// find the attempt by the sub dag-run ID
		att, err := m.dagRunStore.FindSubAttempt(ctx, rootDAGRun, newStatus.DAGRunID)
		if err != nil {
			return fmt.Errorf("failed to find sub dag-run: %w", err)
		}
		attempt = att
	}

	// Open, write, and close the run
	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("failed to open dag-run data: %w", err)
	}

	// Ensure the run data is closed even if write fails
	defer func() {
		if closeErr := attempt.Close(ctx); closeErr != nil {
			logger.Errorf(ctx, "Failed to close dag-run data: %v", closeErr)
		}
	}()

	if err := attempt.Write(ctx, newStatus); err != nil {
		return fmt.Errorf("failed to write status: %w", err)
	}

	return nil
}

// execWithRecovery executes a function with panic recovery and detailed error reporting
// It captures stack traces and provides structured error information for debugging
func execWithRecovery(ctx context.Context, fn func()) {
	defer func() {
		if panicObj := recover(); panicObj != nil {
			stack := debug.Stack()

			// Convert panic object to error
			var err error
			switch v := panicObj.(type) {
			case error:
				err = v
			case string:
				err = fmt.Errorf("panic: %s", v)
			default:
				err = fmt.Errorf("panic: %v", v)
			}

			// Log with structured information
			logger.Error(ctx, "Recovered from panic",
				slog.String("err", err.Error()),
				slog.String("err-type", fmt.Sprintf("%T", panicObj)),
				slog.String("stack-trace", string(stack)),
			)
		}
	}()

	// Execute the function
	fn()
}
