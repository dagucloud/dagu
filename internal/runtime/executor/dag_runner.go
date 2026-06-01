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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	dagutools "github.com/dagucloud/dagu/internal/tools"
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

	// subWorkflowRunner runs child workflows through an injected adapter.
	subWorkflowRunner SubWorkflowRunner

	// workerSelector overrides the child DAG's selector for this invocation.
	workerSelector map[string]string

	// workspaceSeed carries immutable action source content for action sub-DAGs.
	workspaceSeed *WorkspaceSeed

	// Process tracking for ALL executions
	mu                 sync.Mutex
	processes          map[string]*cmdutil.ManagedProcess // runID -> local process
	distributedRuns    map[string]bool                    // runID -> true for distributed runs
	distributedCancels map[string]context.CancelFunc      // runID -> cancel active runner wait
	dagCtx             exec.Context                       // for DB access when cancelling distributed runs

	// killed should be closed when Kill is called
	killed     chan struct{}
	cancelOnce sync.Once

	// externalStepRetry shifts step retry waiting out of the child process and
	// back to the parent executor.
	externalStepRetry bool
}

type WorkspaceSeed struct {
	Descriptor workspacebundle.Descriptor
	Archive    []byte
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

			return newSubDAGExecutor(ctx, rCtx, dag, tempFile), nil
		}
	}

	// If not found as local DAG, look it up in the database
	dag, err := rCtx.DB.GetDAG(ctx, childName)
	if err != nil {
		return nil, fmt.Errorf("failed to find DAG %q: %w", childName, err)
	}

	return newSubDAGExecutor(ctx, rCtx, dag, ""), nil
}

// NewSubDAGExecutorForDAG creates a SubDAGExecutor for an already-loaded DAG.
func NewSubDAGExecutorForDAG(ctx context.Context, dag *core.DAG) (*SubDAGExecutor, error) {
	if dag == nil {
		return nil, fmt.Errorf("sub DAG is required")
	}
	rCtx := exec.GetContext(ctx)
	return newSubDAGExecutor(ctx, rCtx, dag, ""), nil
}

func newSubDAGExecutor(ctx context.Context, rCtx exec.Context, dag *core.DAG, tempFile string) *SubDAGExecutor {
	subWorkflowRunner, _ := SubWorkflowRunnerFromContext(ctx)
	return &SubDAGExecutor{
		DAG:                dag,
		tempFile:           tempFile,
		subWorkflowRunner:  subWorkflowRunner,
		processes:          make(map[string]*cmdutil.ManagedProcess),
		distributedRuns:    make(map[string]bool),
		distributedCancels: make(map[string]context.CancelFunc),
		dagCtx:             rCtx,
		killed:             make(chan struct{}),
	}
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

	target := e.DAG.Location
	if e.workspaceSeed != nil && workDir != "" {
		target = filepath.Join(workDir, filepath.FromSlash(e.workspaceSeed.Descriptor.DAGPath))
	}

	if runParams.Params != "" {
		cmd, err := e.newLocalCLICommand(ctx, workDir, args, target, "--", runParams.Params)
		if err != nil {
			return nil, err
		}
		return e.injectTraceContext(ctx, cmd), nil
	}

	cmd, err := e.newLocalCLICommand(ctx, workDir, args, target)
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

func (e *SubDAGExecutor) SetWorkspaceSeed(seed WorkspaceSeed) {
	e.workspaceSeed = &WorkspaceSeed{
		Descriptor: seed.Descriptor,
		Archive:    append([]byte(nil), seed.Archive...),
	}
}

func (e *SubDAGExecutor) effectiveWorkerSelector() map[string]string {
	if len(e.workerSelector) > 0 {
		return e.workerSelector
	}
	return e.DAG.WorkerSelector
}

func (e *SubDAGExecutor) shouldRunWithSubWorkflowRunner(ctx context.Context, req SubWorkflowRequest) bool {
	if e.subWorkflowRunner == nil {
		return false
	}
	return e.subWorkflowRunner.ShouldRun(ctx, req)
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

// Cleanup removes any temporary files created for local DAGs.
// This should be called after the sub DAG execution is complete.
func (e *SubDAGExecutor) Cleanup(ctx context.Context) error {
	if e.tempFile == "" {
		return nil
	}

	ctx = logger.WithValues(ctx, tag.File(e.tempFile))
	logger.Info(ctx, "Cleaning up temporary DAG file")

	if err := fileutil.Remove(e.tempFile); err != nil && !os.IsNotExist(err) {
		logger.Error(ctx, "Failed to remove temporary DAG file", tag.File(e.tempFile), tag.Error(err))
		return fmt.Errorf("failed to remove temp file: %w", err)
	}

	return nil
}

// Execute executes the sub DAG and returns the result.
// This is useful for parallel execution where results need to be collected.
func (e *SubDAGExecutor) Execute(ctx context.Context, runParams RunParams, workDir string) (*exec.RunStatus, error) {
	ctx = logger.WithValues(ctx, tag.SubDAG(e.DAG.Name), tag.SubRunID(runParams.RunID))

	req := e.subWorkflowRequest(ctx, runParams, workDir)
	if e.shouldRunWithSubWorkflowRunner(ctx, req) {
		logger.Info(ctx, "Executing sub DAG via injected sub-workflow runner")

		runCtx, cancel := context.WithCancel(ctx)
		e.trackDistributedRun(runParams.RunID, cancel)
		defer e.clearDistributedCancel(runParams.RunID)

		if err := e.cancellationErr(ctx); err != nil {
			return nil, err
		}
		return e.subWorkflowRunner.Run(runCtx, req)
	}

	if e.workspaceSeed != nil {
		var cleanup func()
		var err error
		workDir, cleanup, err = e.materializeLocalWorkspace(runParams)
		if err != nil {
			return nil, err
		}
		defer cleanup()
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

	req := e.subWorkflowRequest(ctx, runParams, workDir)
	if e.shouldRunWithSubWorkflowRunner(ctx, req) {
		logger.Info(ctx, "Retrying sub DAG via injected sub-workflow runner", tag.Step(stepName))

		runCtx, cancel := context.WithCancel(ctx)
		e.trackDistributedRun(runParams.RunID, cancel)
		defer e.clearDistributedCancel(runParams.RunID)

		if err := e.cancellationErr(ctx); err != nil {
			return nil, err
		}
		return e.subWorkflowRunner.Retry(runCtx, SubWorkflowRetryRequest{
			SubWorkflowRequest: req,
			StepName:           stepName,
		})
	}

	cmd, err := e.buildRetryCommand(ctx, runParams, stepName, workDir)
	if err != nil {
		return nil, err
	}
	return e.runLocalCommand(ctx, runParams.RunID, cmd)
}

func (e *SubDAGExecutor) subWorkflowRequest(ctx context.Context, runParams RunParams, workDir string) SubWorkflowRequest {
	rCtx := exec.GetContext(ctx)
	var parent exec.DAGRunRef
	if rCtx.DAG != nil {
		parent = rCtx.DAGRunRef()
	}
	req := SubWorkflowRequest{
		DAG:               e.DAG,
		ParentDAG:         rCtx.DAG,
		RootDAGRun:        rCtx.RootDAGRun,
		ParentDAGRun:      parent,
		RunID:             runParams.RunID,
		Params:            runParams.Params,
		WorkDir:           workDir,
		WorkerSelector:    cloneWorkerSelector(e.effectiveWorkerSelector()),
		ExternalStepRetry: e.externalStepRetry,
	}
	if e.workspaceSeed != nil {
		req.Workspace = &SubWorkflowWorkspace{
			Descriptor: e.workspaceSeed.Descriptor,
			Archive:    e.workspaceSeed.Archive,
		}
	}
	return req
}

func (e *SubDAGExecutor) trackDistributedRun(runID string, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.distributedRuns == nil {
		e.distributedRuns = make(map[string]bool)
	}
	if e.distributedCancels == nil {
		e.distributedCancels = make(map[string]context.CancelFunc)
	}
	e.distributedRuns[runID] = true
	e.distributedCancels[runID] = cancel
}

func (e *SubDAGExecutor) clearDistributedCancel(runID string) {
	e.mu.Lock()
	cancel := e.distributedCancels[runID]
	delete(e.distributedCancels, runID)
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (e *SubDAGExecutor) runLocalCommand(ctx context.Context, runID string, cmd *osexec.Cmd) (*exec.RunStatus, error) {
	// Discard subprocess output (logging is handled internally by the sub-DAG)
	cmd.Stdout = io.Discard
	stderrTail := NewTailWriter(io.Discard, 4096)
	cmd.Stderr = stderrTail

	// Ensure we clear command reference when done
	defer func() {
		e.mu.Lock()
		delete(e.processes, runID)
		e.mu.Unlock()
	}()

	logger.Info(ctx, "Executing sub DAG locally")

	process, err := cmdutil.StartManagedProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start sub dag-run: %w", err)
	}
	defer func() { _ = process.Release() }()

	e.mu.Lock()
	e.processes[runID] = process
	e.mu.Unlock()

	waitErr := process.Wait()
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

func (e *SubDAGExecutor) materializeLocalWorkspace(runParams RunParams) (string, func(), error) {
	if e.workspaceSeed == nil {
		return "", func() {}, nil
	}
	tmp, err := os.MkdirTemp("", "dagu-action-workspace-*")
	if err != nil {
		return "", nil, fmt.Errorf("create local action workspace: %w", err)
	}
	cleanup := func() {
		_ = fileutil.RemoveAll(tmp)
	}
	dest := filepath.Join(tmp, "workspace")
	if err := workspacebundle.Extract(e.workspaceSeed.Archive, dest, e.workspaceSeed.Descriptor, workspacebundle.DefaultLimits()); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("materialize action workspace for run %q: %w", runParams.RunID, err)
	}
	return dest, cleanup, nil
}

func (e *SubDAGExecutor) cancellationErr(ctx context.Context) error {
	select {
	case <-e.killed:
		return errSubDAGCancelled
	default:
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(errSubDAGCancelled, err)
	}
	return nil
}

// Kill terminates all running sub DAG processes (both local and distributed)
func (e *SubDAGExecutor) Kill(sig os.Signal) error {
	return e.Stop(cmdutil.TerminationFromSignal(sig))
}

// Stop terminates all running sub DAG processes (both local and distributed)
// according to the requested lifecycle intent.
func (e *SubDAGExecutor) Stop(intent cmdutil.TerminationIntent) error {
	type distributedRun struct {
		runID  string
		cancel context.CancelFunc
	}
	type localProcess struct {
		runID   string
		process *cmdutil.ManagedProcess
	}

	e.mu.Lock()
	distributedRuns := make([]distributedRun, 0, len(e.distributedRuns))
	for runID := range e.distributedRuns {
		distributedRuns = append(distributedRuns, distributedRun{
			runID:  runID,
			cancel: e.distributedCancels[runID],
		})
	}
	processes := make([]localProcess, 0, len(e.processes))
	for runID, process := range e.processes {
		processes = append(processes, localProcess{
			runID:   runID,
			process: process,
		})
	}
	e.mu.Unlock()

	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Cancel distributed runs
	for _, run := range distributedRuns {
		if run.cancel != nil {
			run.cancel()
		}
		if e.subWorkflowRunner != nil {
			if err := e.subWorkflowRunner.Cancel(ctx, SubWorkflowCancelRequest{
				DAG:        e.DAG,
				RootDAGRun: e.dagCtx.RootDAGRun,
				RunID:      run.runID,
			}); err != nil {
				errs = append(errs, err)
				logger.Warn(ctx, "Failed to request distributed sub DAG cancellation",
					tag.RunID(run.runID),
					tag.DAG(e.DAG.Name),
					tag.Error(err),
				)
			} else {
				logger.Info(ctx, "Requested distributed sub DAG cancellation",
					tag.RunID(run.runID),
					tag.DAG(e.DAG.Name),
				)
			}
			continue
		}

		if e.dagCtx.DB != nil {
			if err := e.dagCtx.DB.RequestChildCancel(ctx, run.runID, e.dagCtx.RootDAGRun); err != nil {
				if errors.Is(err, exec.ErrDAGRunIDNotFound) {
					continue
				}
				errs = append(errs, err)
				logger.Warn(ctx, "Failed to request child cancel via local DB",
					tag.RunID(run.runID),
					tag.DAG(e.DAG.Name),
					tag.Error(err),
				)
			} else {
				logger.Info(ctx, "Requested distributed sub DAG cancellation via local DB",
					tag.RunID(run.runID),
					tag.DAG(e.DAG.Name),
				)
			}
		}
	}

	// Kill local processes
	for _, local := range processes {
		if local.process == nil {
			continue
		}
		if _, err := local.process.Stop(cmdutil.StopRequest{Intent: intent}); err != nil {
			errs = append(errs, err)
			logger.Warn(ctx, "Failed to stop local sub DAG process",
				tag.RunID(local.runID),
				tag.DAG(e.DAG.Name),
				tag.Error(err),
			)
		} else {
			logger.Info(ctx, "Requested stop for local sub DAG process",
				tag.RunID(local.runID),
				tag.DAG(e.DAG.Name),
				slog.String("stop-mode", string(intent.Mode)),
				tag.Signal(intent.SignalName()),
			)
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
