// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var (
	errSubDAGCancelled  = errors.New("sub DAG execution cancelled")
	errDAGRunIDNotSet   = errors.New("DAG run ID is not set")
	errRootDAGRunNotSet = errors.New("root DAG run ID is not set")
)

// SubDAGExecutor is a helper for executing sub DAGs.
// It handles both regular DAGs and local DAGs (defined in the same file).
type SubDAGExecutor struct {
	// DAG is the sub DAG to execute.
	// For local DAGs, this DAG's Location will be set to a temporary file.
	DAG *core.DAG

	// tempFile holds the temporary file path for local DAGs.
	// This will be cleaned up after execution.
	tempFile string

	// coordinatorCli is used for distributed execution
	coordinatorCli exec.Dispatcher

	// workerSelector overrides the child DAG's selector for this invocation.
	workerSelector map[string]string

	// Process tracking for ALL executions
	mu              sync.Mutex
	cmds            map[string]*osexec.Cmd // runID -> cmd for local processes
	distributedRuns map[string]bool        // runID -> true for distributed runs
	dagCtx          exec.Context           // for DB access when cancelling distributed runs

	// killed should be closed when Kill is called
	killed     chan struct{}
	cancelOnce sync.Once

	// externalStepRetry shifts step retry waiting out of the child process and
	// back to the parent executor.
	externalStepRetry bool
}

// NewSubDAGExecutor creates a new SubDAGExecutor.
// It handles the logic for finding the DAG - either from the database
// or from local DAGs defined in the parent.
func NewSubDAGExecutor(ctx context.Context, childName string) (*SubDAGExecutor, error) {
	rCtx := exec.GetContext(ctx)

	// First, check if it's a local DAG in the parent
	if rCtx.DAG != nil && rCtx.DAG.LocalDAGs != nil {
		if localDAG, ok := rCtx.DAG.LocalDAGs[childName]; ok {
			// Collect extra docs from other local DAGs
			var extraDocs [][]byte
			for _, otherDAG := range rCtx.DAG.LocalDAGs {
				if otherDAG.Name != childName {
					extraDocs = append(extraDocs, otherDAG.YamlData)
				}
			}

			// Create a temporary file for the local DAG
			tempFile, err := fileutil.CreateTempDAGFile("local-dags", childName, localDAG.YamlData, extraDocs...)
			if err != nil {
				return nil, fmt.Errorf("failed to create temp file for local DAG: %w", err)
			}

			// Clone the DAG and set the location to the temporary file
			dag := localDAG.Clone()
			dag.Location = tempFile

			return &SubDAGExecutor{
				DAG:             dag,
				tempFile:        tempFile,
				coordinatorCli:  rCtx.CoordinatorCli,
				cmds:            make(map[string]*osexec.Cmd),
				distributedRuns: make(map[string]bool),
				dagCtx:          rCtx,
				killed:          make(chan struct{}),
			}, nil
		}
	}

	// If not found as local DAG, look it up in the database
	dag, err := rCtx.DB.GetDAG(ctx, childName)
	if err != nil {
		return nil, fmt.Errorf("failed to find DAG %q: %w", childName, err)
	}

	return &SubDAGExecutor{
		DAG:             dag,
		coordinatorCli:  rCtx.CoordinatorCli,
		cmds:            make(map[string]*osexec.Cmd),
		distributedRuns: make(map[string]bool),
		dagCtx:          rCtx,
		killed:          make(chan struct{}),
	}, nil
}

// buildCommand builds the command to execute the sub DAG.
func (e *SubDAGExecutor) buildCommand(ctx context.Context, runParams RunParams, workDir string) (*osexec.Cmd, error) {
	if runParams.RunID == "" {
		return nil, errDAGRunIDNotSet
	}

	rCtx := exec.GetContext(ctx)
	if rCtx.RootDAGRun.Zero() {
		return nil, errRootDAGRunNotSet
	}

	args := []string{
		"start",
		fmt.Sprintf("--root=%s", rCtx.RootDAGRun.String()),
		fmt.Sprintf("--parent=%s", rCtx.DAGRunRef().String()),
		fmt.Sprintf("--run-id=%s", runParams.RunID),
		"--trigger-type=subdag",
	}
	if workDir != "" {
		args = append(args, fmt.Sprintf("--default-working-dir=%s", workDir))
	}

	if runParams.Params != "" {
		cmd, err := e.newLocalCLICommand(ctx, workDir, args, e.DAG.Location, "--", runParams.Params)
		if err != nil {
			return nil, err
		}
		return e.injectTraceContext(ctx, cmd), nil
	}

	cmd, err := e.newLocalCLICommand(ctx, workDir, args, e.DAG.Location)
	if err != nil {
		return nil, err
	}
	return e.injectTraceContext(ctx, cmd), nil
}

func (e *SubDAGExecutor) buildRetryCommand(
	ctx context.Context,
	runParams RunParams,
	stepName string,
	workDir string,
) (*osexec.Cmd, error) {
	if runParams.RunID == "" {
		return nil, errDAGRunIDNotSet
	}

	rCtx := exec.GetContext(ctx)
	if rCtx.RootDAGRun.Zero() {
		return nil, errRootDAGRunNotSet
	}

	args := []string{
		"retry",
		fmt.Sprintf("--run-id=%s", runParams.RunID),
		fmt.Sprintf("--root=%s", rCtx.RootDAGRun.String()),
	}
	if workDir != "" {
		args = append(args, fmt.Sprintf("--default-working-dir=%s", workDir))
	}
	if stepName != "" {
		args = append(args, fmt.Sprintf("--step=%s", stepName))
	}
	return e.newLocalCLICommand(ctx, workDir, args, e.DAG.Location)
}

func (e *SubDAGExecutor) SetExternalStepRetry(enabled bool) {
	e.externalStepRetry = enabled
}

// SetWorkerSelector sets a per-invocation worker selector for the sub DAG.
func (e *SubDAGExecutor) SetWorkerSelector(selector map[string]string) {
	e.workerSelector = cloneWorkerSelector(selector)
}

func (e *SubDAGExecutor) effectiveWorkerSelector() map[string]string {
	if len(e.workerSelector) > 0 {
		return e.workerSelector
	}
	return e.DAG.WorkerSelector
}

func (e *SubDAGExecutor) shouldDispatchToCoordinator(defaultMode config.ExecutionMode) bool {
	if e.DAG.ForceLocal {
		return false
	}
	if e.coordinatorCli == nil {
		return false
	}
	if len(e.effectiveWorkerSelector()) > 0 {
		return true
	}
	return defaultMode == config.ExecutionModeDistributed
}

func cloneWorkerSelector(selector map[string]string) map[string]string {
	if len(selector) == 0 {
		return nil
	}
	clone := make(map[string]string, len(selector))
	maps.Copy(clone, selector)
	return clone
}

func (e *SubDAGExecutor) newLocalCLICommand(
	ctx context.Context,
	workDir string,
	args []string,
	target string,
	trailingArgs ...string,
) (*osexec.Cmd, error) {
	executable, err := executablePath(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to find executable path: %w", err)
	}

	fullArgs := append([]string{}, args...)
	if configFile := config.ConfigFileUsed(ctx); configFile != "" {
		fullArgs = append(fullArgs, "--config", configFile)
	}
	fullArgs = append(fullArgs, target)
	fullArgs = append(fullArgs, trailingArgs...)

	cmd := osexec.CommandContext(ctx, executable, fullArgs...) // nolint:gosec
	cmd.Dir = workDir

	rCtx := exec.GetContext(ctx)
	cmd.Env = localCLIEnv(rCtx)
	if e.externalStepRetry {
		cmd.Env = append(cmd.Env, exec.EnvKeyExternalStepRetry+"=1")
	}

	cmdutil.SetupCommand(cmd)
	return cmd, nil
}

func (e *SubDAGExecutor) injectTraceContext(ctx context.Context, cmd *osexec.Cmd) *osexec.Cmd {
	logCtx := logger.WithValues(ctx, tag.DAG(e.DAG.Name))
	traceEnvVars := extractTraceContext(ctx)
	if len(traceEnvVars) > 0 {
		cmd.Env = append(cmd.Env, traceEnvVars...)
		logger.Debug(logCtx, "Injecting trace context into sub DAG",
			slog.Any("trace-env-vars", traceEnvVars),
		)
	} else {
		logger.Debug(logCtx, "No trace context to inject into sub DAG")
	}
	return cmd
}

func localCLIEnv(rCtx exec.Context) []string {
	env := baseEnvForLocalCLI(rCtx)
	return append(env, inheritedEnvForLocalCLI(rCtx.AllEnvs())...)
}

func baseEnvForLocalCLI(rCtx exec.Context) []string {
	if rCtx.BaseEnv != nil {
		env := rCtx.BaseEnv.AsSlice()
		if len(env) > 0 {
			return env
		}
	}
	return os.Environ()
}

func inheritedEnvForLocalCLI(envs []string) []string {
	if !hasDAGToolsEnv(envs) {
		return envs
	}
	filtered := make([]string, 0, len(envs))
	for _, env := range envs {
		key, _, ok := strings.Cut(env, "=")
		if ok && isDAGToolsEnvKey(key) {
			continue
		}
		filtered = append(filtered, env)
	}
	return filtered
}

func hasDAGToolsEnv(envs []string) bool {
	for _, env := range envs {
		key, _, ok := strings.Cut(env, "=")
		if ok && strings.EqualFold(key, dagutools.EnvManifest) {
			return true
		}
	}
	return false
}

func isDAGToolsEnvKey(key string) bool {
	for _, candidate := range []string{
		"PATH",
		"AQUA_ROOT_DIR",
		"AQUA_CONFIG",
		"AQUA_DISABLE_LAZY_INSTALL",
		"AQUA_CHECKSUM",
		"AQUA_REQUIRE_CHECKSUM",
		"AQUA_ENFORCE_CHECKSUM",
		"AQUA_ENFORCE_REQUIRE_CHECKSUM",
		dagutools.EnvManifest,
	} {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	return false
}

// BuildCoordinatorTask creates a coordinator task for distributed execution
func (e *SubDAGExecutor) BuildCoordinatorTask(ctx context.Context, runParams RunParams) (*coordinatorv1.Task, error) {
	rCtx := exec.GetContext(ctx)

	if runParams.RunID == "" {
		return nil, errDAGRunIDNotSet
	}

	if rCtx.RootDAGRun.Zero() {
		return nil, errRootDAGRunNotSet
	}

	taskOpts, err := e.coordinatorTaskOptions(ctx, WithTaskParams(runParams.Params))
	if err != nil {
		return nil, err
	}

	// Build task for coordinator dispatch using DAG.CreateTask
	task := CreateTask(
		e.DAG.Name,
		string(e.DAG.YamlData),
		coordinatorv1.Operation_OPERATION_START,
		runParams.RunID,
		taskOpts...,
	)

	taskCtx := logger.WithValues(ctx,
		tag.RunID(runParams.RunID),
		tag.Target(e.DAG.Name),
	)
	logger.Info(taskCtx, "Built coordinator task for sub DAG",
		slog.Any("worker-selector", e.effectiveWorkerSelector()),
	)

	return task, nil
}

func (e *SubDAGExecutor) buildCoordinatorRetryTask(
	ctx context.Context,
	runParams RunParams,
	stepName string,
	previousStatus *exec.DAGRunStatus,
) (*coordinatorv1.Task, error) {
	rCtx := exec.GetContext(ctx)
	if runParams.RunID == "" {
		return nil, errDAGRunIDNotSet
	}
	if rCtx.RootDAGRun.Zero() {
		return nil, errRootDAGRunNotSet
	}

	taskOpts, err := e.coordinatorTaskOptions(
		ctx,
		WithStep(stepName),
		WithPreviousStatus(previousStatus),
	)
	if err != nil {
		return nil, err
	}

	task := CreateTask(
		e.DAG.Name,
		string(e.DAG.YamlData),
		coordinatorv1.Operation_OPERATION_RETRY,
		runParams.RunID,
		taskOpts...,
	)
	return task, nil
}

func (e *SubDAGExecutor) coordinatorTaskOptions(ctx context.Context, extra ...TaskOption) ([]TaskOption, error) {
	rCtx := exec.GetContext(ctx)

	// Prefer the sub-DAG base config and fall back to the parent's base config.
	baseConfig := string(e.DAG.BaseConfigData)
	if baseConfig == "" && rCtx.DAG != nil {
		baseConfig = string(rCtx.DAG.BaseConfigData)
	}

	options := []TaskOption{
		WithRootDagRun(rCtx.RootDAGRun),
		WithParentDagRun(exec.DAGRunRef{
			Name: rCtx.DAG.Name,
			ID:   rCtx.DAGRunID,
		}),
		WithWorkerSelector(e.effectiveWorkerSelector()),
		WithBaseConfig(baseConfig),
	}
	if e.DAG.SourceFile != "" {
		options = append(options, WithSourceFile(e.DAG.SourceFile))
	}
	if e.externalStepRetry {
		options = append(options, WithExternalStepRetry(true))
	}
	snapshot, err := agentsnapshot.BuildFromContext(ctx, e.DAG)
	if err != nil {
		return nil, fmt.Errorf("build distributed agent snapshot: %w", err)
	}
	if len(snapshot) > 0 {
		options = append(options, WithAgentSnapshot(snapshot))
	}
	options = append(options, extra...)
	return options, nil
}

// Cleanup removes any temporary files created for local DAGs.
// This should be called after the sub DAG execution is complete.
func (e *SubDAGExecutor) Cleanup(ctx context.Context) error {
	if e.tempFile == "" {
		return nil
	}

	ctx = logger.WithValues(ctx, tag.File(e.tempFile))
	logger.Info(ctx, "Cleaning up temporary DAG file")

	if err := os.Remove(e.tempFile); err != nil && !os.IsNotExist(err) {
		logger.Error(ctx, "Failed to remove temporary DAG file", tag.File(e.tempFile), tag.Error(err))
		return fmt.Errorf("failed to remove temp file: %w", err)
	}

	return nil
}

// Execute executes the sub DAG and returns the result.
// This is useful for parallel execution where results need to be collected.
func (e *SubDAGExecutor) Execute(ctx context.Context, runParams RunParams, workDir string) (*exec.RunStatus, error) {
	ctx = logger.WithValues(ctx, tag.SubDAG(e.DAG.Name), tag.SubRunID(runParams.RunID))

	rCtx := exec.GetContext(ctx)
	if e.shouldDispatchToCoordinator(rCtx.DefaultExecMode) {
		// Handle distributed execution
		logger.Info(ctx, "Executing sub DAG via distributed execution")

		e.mu.Lock()
		e.distributedRuns[runParams.RunID] = true
		e.mu.Unlock()

		return e.dispatch(ctx, runParams)
	}

	cmd, err := e.buildCommand(ctx, runParams, workDir)
	if err != nil {
		return nil, err
	}
	return e.runLocalCommand(ctx, runParams.RunID, cmd)
}

// Retry executes a parent-managed step retry for a previously started sub DAG.
func (e *SubDAGExecutor) Retry(ctx context.Context, runParams RunParams, stepName, workDir string) (*exec.RunStatus, error) {
	ctx = logger.WithValues(ctx, tag.SubDAG(e.DAG.Name), tag.SubRunID(runParams.RunID))

	rCtx := exec.GetContext(ctx)
	if e.shouldDispatchToCoordinator(rCtx.DefaultExecMode) {
		logger.Info(ctx, "Retrying sub DAG via distributed execution", tag.Step(stepName))

		e.mu.Lock()
		e.distributedRuns[runParams.RunID] = true
		e.mu.Unlock()

		if err := e.dispatchRetryToCoordinator(ctx, runParams, stepName); err != nil {
			return nil, fmt.Errorf("distributed step retry failed: %w", err)
		}
		return e.waitCompletion(ctx, runParams.RunID)
	}

	cmd, err := e.buildRetryCommand(ctx, runParams, stepName, workDir)
	if err != nil {
		return nil, err
	}
	return e.runLocalCommand(ctx, runParams.RunID, cmd)
}

func (e *SubDAGExecutor) runLocalCommand(ctx context.Context, runID string, cmd *osexec.Cmd) (*exec.RunStatus, error) {
	// Discard subprocess output (logging is handled internally by the sub-DAG)
	cmd.Stdout = io.Discard
	stderrTail := NewTailWriter(io.Discard, 4096)
	cmd.Stderr = stderrTail

	// Ensure we clear command reference when done
	defer func() {
		e.mu.Lock()
		delete(e.cmds, runID)
		e.mu.Unlock()
	}()

	logger.Info(ctx, "Executing sub DAG locally")

	// Start the command first to initialize cmd.Process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start sub dag-run: %w", err)
	}

	// Store command reference for Kill AFTER starting, so cmd.Process is already set
	e.mu.Lock()
	e.cmds[runID] = cmd
	e.mu.Unlock()

	waitErr := cmd.Wait()
	if waitErr != nil {
		logger.Error(ctx, "Sub DAG execution returned error", tag.Error(waitErr))
	}

	// Check for cancellation before retrieving results
	select {
	case <-e.killed:
		return nil, errSubDAGCancelled
	case <-ctx.Done():
		return nil, errors.Join(errSubDAGCancelled, ctx.Err())
	default:
	}

	rCtx := exec.GetContext(ctx)
	result, resultErr := rCtx.DB.GetSubDAGRunStatus(ctx, runID, rCtx.RootDAGRun)
	if resultErr != nil {
		errMsg := fmt.Sprintf("sub dag-run %q failed and wrote no status", runID)
		if waitErr != nil {
			errMsg += fmt.Sprintf(" (process: %v)", waitErr)
		}
		if tail := strings.TrimSpace(stderrTail.Tail()); tail != "" {
			errMsg += fmt.Sprintf("; stderr: %s", tail)
		}
		return nil, fmt.Errorf("%s: %w", errMsg, resultErr)
	}

	if result.Status.IsSuccess() {
		logger.Info(ctx, "Sub DAG completed successfully")
		return result, nil
	}

	return result, waitErr
}

// dispatch runs the sub DAG via coordinator and returns the result.
func (e *SubDAGExecutor) dispatch(ctx context.Context, runParams RunParams) (*exec.RunStatus, error) {
	dispatchCtx := logger.WithValues(ctx,
		tag.RunID(runParams.RunID),
		tag.DAG(e.DAG.Name),
	)

	if err := e.dispatchToCoordinator(ctx, runParams); err != nil {
		logger.Error(dispatchCtx, "Distributed sub DAG dispatch failed", tag.Error(err))
		return nil, fmt.Errorf("distributed execution failed: %w", err)
	}

	logger.Info(dispatchCtx, "Distributed sub DAG dispatched; awaiting completion")
	return e.waitCompletion(ctx, runParams.RunID)
}

// dispatchToCoordinator builds and dispatches a task to the coordinator.
func (e *SubDAGExecutor) dispatchToCoordinator(ctx context.Context, runParams RunParams) error {
	if e.coordinatorCli == nil {
		return fmt.Errorf("no coordinator client configured for distributed execution")
	}

	task, err := e.BuildCoordinatorTask(ctx, runParams)
	if err != nil {
		return fmt.Errorf("failed to build coordinator task: %w", err)
	}

	taskCtx := logger.WithValues(ctx,
		tag.RunID(task.DagRunId),
		tag.Target(task.Target),
	)
	logger.Info(taskCtx, "Dispatching task to coordinator",
		slog.Any("worker-selector", task.WorkerSelector),
	)

	if err := e.coordinatorCli.Dispatch(ctx, task); err != nil {
		return fmt.Errorf("failed to dispatch task: %w", err)
	}

	return nil
}

func (e *SubDAGExecutor) dispatchRetryToCoordinator(ctx context.Context, runParams RunParams, stepName string) error {
	if e.coordinatorCli == nil {
		return fmt.Errorf("no coordinator client configured for distributed execution")
	}

	previousStatus, err := e.getFullSubDAGRunStatus(ctx, runParams.RunID)
	if err != nil {
		return fmt.Errorf("failed to load sub DAG status for retry: %w", err)
	}

	task, err := e.buildCoordinatorRetryTask(ctx, runParams, stepName, previousStatus)
	if err != nil {
		return fmt.Errorf("failed to build retry coordinator task: %w", err)
	}

	taskCtx := logger.WithValues(ctx,
		tag.RunID(task.DagRunId),
		tag.Target(task.Target),
		tag.Step(stepName),
	)
	logger.Info(taskCtx, "Dispatching retry task to coordinator",
		slog.Any("worker-selector", task.WorkerSelector),
	)

	if err := e.coordinatorCli.Dispatch(ctx, task); err != nil {
		return fmt.Errorf("failed to dispatch retry task: %w", err)
	}
	return nil
}

func (e *SubDAGExecutor) waitCompletion(ctx context.Context, dagRunID string) (*exec.RunStatus, error) {
	waitCtx := logger.WithValues(ctx,
		tag.RunID(dagRunID),
		tag.DAG(e.DAG.Name),
	)

	// Poll for completion
	pollInterval := 1 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Periodically log while waiting so long polls do not look stalled
	logInterval := 15 * time.Second
	logTicker := time.NewTicker(logInterval)
	defer logTicker.Stop()
	start := time.Now()

	// Track consecutive errors to detect connection loss
	const maxConsecutiveErrors = 10
	var consecutiveErrors int

	for {
		select {
		case <-e.killed:
			return e.waitForCancellation(ctx, dagRunID, start)

		case <-ticker.C:
			// Get sub DAG run status (single RPC call)
			result, err := e.getSubDAGRunStatus(ctx, dagRunID)
			if err != nil {
				consecutiveErrors++
				logger.Warn(waitCtx, "Failed to get sub DAG status",
					tag.Error(err),
					slog.Int("consecutive_errors", consecutiveErrors),
				)
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("lost connection to coordinator after %d attempts: %w", consecutiveErrors, err)
				}
				continue
			}
			consecutiveErrors = 0 // Reset on success

			if len(result.PendingStepRetries) > 0 {
				logger.Info(waitCtx, "Distributed sub DAG returned pending step retry")
				return result, nil
			}

			if result.Status.IsActive() || result.Status == core.NotStarted {
				logger.Debug(waitCtx, "Sub DAG run not completed yet")
				continue
			}

			logger.Info(waitCtx, "Distributed execution completed", tag.Name(result.Name))
			return result, nil

		case <-logTicker.C:
			logger.Info(waitCtx, "Waiting for distributed sub DAG run to complete",
				tag.Duration(time.Since(start).Round(time.Second)),
			)
		}
	}
}

// waitForCancellation waits for a distributed sub DAG run to terminate after cancellation is requested.
func (e *SubDAGExecutor) waitForCancellation(ctx context.Context, dagRunID string, startTime time.Time) (*exec.RunStatus, error) {
	waitCtx := logger.WithValues(ctx,
		tag.RunID(dagRunID),
		tag.DAG(e.DAG.Name),
	)

	logger.Info(waitCtx, "Cancellation requested for distributed sub DAG run; waiting for termination")

	const (
		cancellationTimeout = 30 * time.Second
		pollInterval        = 1 * time.Second
		logInterval         = 5 * time.Second
	)

	timeout := time.After(cancellationTimeout)
	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	logTicker := time.NewTicker(logInterval)
	defer logTicker.Stop()

	// Use a fresh context for status queries since the original context may be canceled
	cancelWaitCtx := context.WithoutCancel(ctx)

	var lastStatus *exec.RunStatus

	for {
		status, err := e.getSubDAGRunStatus(cancelWaitCtx, dagRunID)
		if err != nil {
			logger.Warn(waitCtx, "Failed to get sub DAG run status during cancellation wait",
				tag.Error(err),
			)
			if shouldAbortCancellationWait(err) {
				return nil, errors.Join(errSubDAGCancelled, err)
			}
		}
		lastStatus = status

		if status != nil && !status.Status.IsActive() && status.Status != core.NotStarted {
			return status, nil
		}

		select {
		case <-timeout:
			return nil, fmt.Errorf("distributed execution cancellation timed out for dag-run ID %s", dagRunID)

		case <-pollTicker.C:
			// continue polling

		case <-logTicker.C:
			statusStr := "unknown"
			if lastStatus != nil {
				statusStr = lastStatus.Status.String()
			}
			logger.Info(waitCtx, "Still waiting for distributed sub DAG run to terminate",
				tag.Duration(time.Since(startTime).Round(time.Second)),
				tag.Status(statusStr),
			)
		}
	}
}

func shouldAbortCancellationWait(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "no coordinators available") ||
		strings.Contains(msg, "no coordinator client configured") ||
		strings.Contains(msg, "no available workers")
}

// getSubDAGRunStatus retrieves the status of a sub-DAG run.
// For distributed runs, it queries the coordinator; otherwise, it uses the local DB.
func (e *SubDAGExecutor) getSubDAGRunStatus(ctx context.Context, dagRunID string) (*exec.RunStatus, error) {
	rCtx := exec.GetContext(ctx)

	if e.coordinatorCli != nil {
		return e.getStatusFromCoordinator(ctx, dagRunID, rCtx.RootDAGRun)
	}

	if rCtx.DB != nil {
		return rCtx.DB.GetSubDAGRunStatus(ctx, dagRunID, rCtx.RootDAGRun)
	}

	return nil, fmt.Errorf("no coordinator or database available to get sub-DAG status")
}

func (e *SubDAGExecutor) getFullSubDAGRunStatus(ctx context.Context, dagRunID string) (*exec.DAGRunStatus, error) {
	rCtx := exec.GetContext(ctx)
	if e.coordinatorCli == nil {
		return nil, fmt.Errorf("no coordinator available to get full sub-DAG status")
	}
	return e.getFullStatusFromCoordinator(ctx, dagRunID, rCtx.RootDAGRun)
}

// getStatusFromCoordinator queries the coordinator for sub-DAG run status.
func (e *SubDAGExecutor) getStatusFromCoordinator(ctx context.Context, dagRunID string, rootDAGRun exec.DAGRunRef) (*exec.RunStatus, error) {
	dagRunStatus, err := e.getFullStatusFromCoordinator(ctx, dagRunID, rootDAGRun)
	if err != nil {
		return nil, err
	}
	outputs := extractOutputsFromNodes(dagRunStatus.Nodes)

	return &exec.RunStatus{
		Name:               dagRunStatus.Name,
		DAGRunID:           dagRunID,
		Params:             dagRunStatus.Params,
		Outputs:            outputs,
		Status:             dagRunStatus.Status,
		PendingStepRetries: exec.PendingStepRetriesFromStatus(dagRunStatus),
	}, nil
}

func (e *SubDAGExecutor) getFullStatusFromCoordinator(
	ctx context.Context,
	dagRunID string,
	rootDAGRun exec.DAGRunRef,
) (*exec.DAGRunStatus, error) {
	rootRef := &rootDAGRun
	resp, err := e.coordinatorCli.GetDAGRunStatus(ctx, e.DAG.Name, dagRunID, rootRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get DAG run status from coordinator: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("no response from coordinator")
	}
	if !resp.Found {
		return nil, fmt.Errorf("DAG run not found in coordinator")
	}

	dagRunStatus, convErr := convert.ProtoToDAGRunStatus(resp.Status)
	if convErr != nil {
		return nil, fmt.Errorf("failed to convert status: %w", convErr)
	}
	return dagRunStatus, nil
}

// extractOutputsFromNodes extracts output variables from nodes.
// Output variables may be stored in two formats:
// 1. Local format: key="VAR", value="VAR=actual_value" (KeyValue string)
// 2. Proto format: key="VAR", value="actual_value" (direct value)
// This function normalizes both formats to extract the actual value.
func extractOutputsFromNodes(nodes []*exec.Node) map[string]string {
	outputs := make(map[string]string)
	for _, node := range nodes {
		if node.OutputVariables == nil {
			continue
		}
		node.OutputVariables.Range(func(key, value any) bool {
			k, ok := key.(string)
			if !ok {
				return true
			}
			v, ok := value.(string)
			if !ok {
				return true
			}

			// Handle both local format (KEY=value) and proto format (just value)
			// Local format stores "KEY=value" as the value string
			// Proto format stores just "value" directly
			if after, ok0 := strings.CutPrefix(v, k+"="); ok0 {
				// Local format: extract value after "KEY="
				outputs[k] = after
			} else {
				// Proto format or already normalized: use value directly
				outputs[k] = v
			}
			return true
		})
	}
	return outputs
}

// Kill terminates all running sub DAG processes (both local and distributed)
func (e *SubDAGExecutor) Kill(sig os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Cancel distributed runs
	for runID := range e.distributedRuns {
		// For distributed runs with coordinator, use coordinator to request cancellation.
		// This is required for shared-nothing mode where the parent worker cannot
		// directly access the sub-DAG's attempt on the coordinator's dagRunStore.
		if e.coordinatorCli != nil {
			if err := e.coordinatorCli.RequestCancel(ctx, e.DAG.Name, runID, &e.dagCtx.RootDAGRun); err != nil {
				errs = append(errs, err)
				logger.Warn(ctx, "Failed to request distributed cancellation via coordinator",
					tag.RunID(runID),
					tag.DAG(e.DAG.Name),
					tag.Error(err),
				)
			} else {
				logger.Info(ctx, "Requested distributed sub DAG cancellation via coordinator",
					tag.RunID(runID),
					tag.DAG(e.DAG.Name),
				)
			}
			continue
		}

		// Fallback: try local DB (for non-shared-nothing mode with local dagRunStore)
		if e.dagCtx.DB != nil {
			if err := e.dagCtx.DB.RequestChildCancel(ctx, runID, e.dagCtx.RootDAGRun); err != nil {
				if errors.Is(err, exec.ErrDAGRunIDNotFound) {
					continue
				}
				errs = append(errs, err)
				logger.Warn(ctx, "Failed to request child cancel via local DB",
					tag.RunID(runID),
					tag.DAG(e.DAG.Name),
					tag.Error(err),
				)
			} else {
				logger.Info(ctx, "Requested distributed sub DAG cancellation via local DB",
					tag.RunID(runID),
					tag.DAG(e.DAG.Name),
				)
			}
		}
	}

	// Kill local processes
	for runID, cmd := range e.cmds {
		if cmd != nil && cmd.Process != nil {
			if err := cmdutil.KillProcessGroup(cmd, sig); err != nil {
				errs = append(errs, err)
				logger.Warn(ctx, "Failed to kill local sub DAG process",
					tag.RunID(runID),
					tag.DAG(e.DAG.Name),
					tag.Error(err),
				)
			} else {
				logger.Info(ctx, "Requested kill for local sub DAG process",
					tag.RunID(runID),
					tag.DAG(e.DAG.Name),
				)
			}
		}
	}

	e.cancelOnce.Do(func() {
		close(e.killed)
	})

	return errors.Join(errs...)
}

// executablePath returns the path to the dagu executable.
func executablePath(ctx context.Context) (string, error) {
	if cfg := config.GetConfig(ctx); cfg != nil && cfg.Paths.Executable != "" {
		return cfg.Paths.Executable, nil
	}
	if path := os.Getenv("DAGU_EXECUTABLE"); path != "" {
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return executable, nil
}

// extractTraceContext extracts OpenTelemetry trace context from the current context
// and returns it as environment variables for child processes.
func extractTraceContext(ctx context.Context) []string {
	return telemetry.InjectTraceContext(ctx)
}
