// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
)

var (
	errSubDAGCancelled     = errors.New("sub DAG execution cancelled")
	errDAGRunIDNotSet      = errors.New("DAG run ID is not set")
	errRootDAGRunNotSet    = errors.New("root DAG run ID is not set")
	errNoSubWorkflowRunner = errors.New("no sub-workflow runner accepted request")
)

// SubDAGExecutor is a helper for executing sub DAGs.
// It handles both regular DAGs and local DAGs (defined in the same file).
type SubDAGExecutor struct {
	// DAG is the sub DAG to execute.
	// For local DAGs, this DAG's Location will be set to a temporary file.
	DAG *ir.DAG

	// tempFile holds the temporary file path for local DAGs.
	// This will be cleaned up after execution.
	tempFile string

	// subWorkflowRunner runs child workflows through an injected adapter.
	subWorkflowRunner SubWorkflowRunner

	// workerSelector overrides the child DAG's selector for this invocation.
	workerSelector map[string]string

	// workspaceSeed carries immutable files for child DAG execution.
	workspaceSeed *WorkspaceSeed

	mu         sync.Mutex
	activeRuns map[string]context.CancelFunc // runID -> cancel active runner wait
	dagCtx     runctx.Context

	// killed should be closed when Kill is called
	killed     chan struct{}
	cancelOnce sync.Once

	// externalStepRetry shifts step retry waiting out of the child process and
	// back to the parent executor.
	externalStepRetry bool
}

// WorkspaceSeed contains workspace contents shared across a DAG-run hierarchy.
// The seed and its archive must not be mutated after being passed to an executor.
type WorkspaceSeed struct {
	Descriptor workspacebundle.Descriptor
	Archive    []byte
}

// NewSubDAGExecutor creates a SubDAGExecutor for childName.
func NewSubDAGExecutor(ctx context.Context, childName string) (*SubDAGExecutor, error) {
	rCtx := runctx.GetContext(ctx)

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

			executor := newSubDAGExecutor(ctx, rCtx, dag, tempFile)
			if seed, ok := workspaceSeedFromContext(ctx); ok {
				executor.SetWorkspaceSeed(seed)
			}
			return executor, nil
		}
	}

	// Load the named DAG when no inline definition matched.
	if rCtx.DAGLoader == nil {
		return nil, fmt.Errorf("cannot resolve sub-DAG %q: no local DAG store available (hint: parent DAG was dispatched to a worker without local DAG cache — consider setting worker_selector: local on the parent DAG): %w", childName, persis.ErrDAGNotFound)
	}
	dag, err := rCtx.DAGLoader.GetDAG(ctx, childName)
	if err != nil {
		return nil, fmt.Errorf("failed to find DAG %q: %w", childName, err)
	}
	if dag == nil {
		return nil, fmt.Errorf("sub-DAG %q resolved to nil (hint: parent DAG may have been dispatched to a worker without local DAG cache — consider setting worker_selector: local on the parent DAG): %w", childName, persis.ErrDAGNotFound)
	}

	return newSubDAGExecutor(ctx, rCtx, dag, ""), nil
}

// NewSubDAGExecutorForDAG creates a SubDAGExecutor for an already-loaded DAG.
func NewSubDAGExecutorForDAG(ctx context.Context, dag *ir.DAG) (*SubDAGExecutor, error) {
	if dag == nil {
		return nil, fmt.Errorf("sub DAG is required")
	}
	rCtx := runctx.GetContext(ctx)
	return newSubDAGExecutor(ctx, rCtx, dag, ""), nil
}

func newSubDAGExecutor(ctx context.Context, rCtx runctx.Context, dag *ir.DAG, tempFile string) *SubDAGExecutor {
	subWorkflowRunner, _ := SubWorkflowRunnerFromContext(ctx)
	return &SubDAGExecutor{
		DAG:               dag,
		tempFile:          tempFile,
		subWorkflowRunner: subWorkflowRunner,
		activeRuns:        make(map[string]context.CancelFunc),
		dagCtx:            rCtx,
		killed:            make(chan struct{}),
	}
}

func (e *SubDAGExecutor) SetExternalStepRetry(enabled bool) {
	e.externalStepRetry = enabled
}

// SetWorkerSelector sets a per-invocation worker selector for the sub DAG.
func (e *SubDAGExecutor) SetWorkerSelector(selector map[string]string) {
	e.workerSelector = cloneWorkerSelector(selector)
}

func (e *SubDAGExecutor) SetWorkspaceSeed(seed WorkspaceSeed) {
	e.workspaceSeed = &seed
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
func (e *SubDAGExecutor) Execute(ctx context.Context, runParams RunParams, workDir string) (*ir.RunStatus, error) {
	ctx = logger.WithValues(ctx, tag.SubDAG(e.DAG.Name), tag.SubRunID(runParams.RunID))

	req := e.subWorkflowRequest(ctx, runParams, workDir)
	if err := validateSubWorkflowRequest(req); err != nil {
		return nil, err
	}
	if !e.shouldRunWithSubWorkflowRunner(ctx, req) {
		return nil, errNoSubWorkflowRunner
	}

	logger.Info(ctx, "Executing sub DAG via injected sub-workflow runner")

	runCtx, cancel := context.WithCancel(ctx)
	e.trackRun(runParams.RunID, cancel)
	defer e.clearRun(runParams.RunID)

	if err := e.cancellationErr(ctx); err != nil {
		return nil, err
	}
	return e.subWorkflowRunner.Run(runCtx, req)
}

// Reuse returns the persisted result of a child run without executing it.
func (e *SubDAGExecutor) Reuse(ctx context.Context, runParams RunParams, workDir string) (*ir.RunStatus, error) {
	ctx = logger.WithValues(ctx, tag.SubDAG(e.DAG.Name), tag.SubRunID(runParams.RunID))

	req := e.subWorkflowRequest(ctx, runParams, workDir)
	req.Reuse = true
	if err := validateSubWorkflowRequest(req); err != nil {
		return nil, err
	}
	if !e.shouldRunWithSubWorkflowRunner(ctx, req) {
		return nil, errNoSubWorkflowRunner
	}

	runCtx, cancel := context.WithCancel(ctx)
	e.trackRun(runParams.RunID, cancel)
	defer e.clearRun(runParams.RunID)

	if err := e.cancellationErr(ctx); err != nil {
		return nil, err
	}
	return e.subWorkflowRunner.Run(runCtx, req)
}

// Retry executes a parent-managed step retry for a previously started sub DAG.
func (e *SubDAGExecutor) Retry(ctx context.Context, runParams RunParams, stepName, workDir string, path dagrun.RetryPath) (*ir.RunStatus, error) {
	ctx = logger.WithValues(ctx, tag.SubDAG(e.DAG.Name), tag.SubRunID(runParams.RunID))

	req := e.subWorkflowRequest(ctx, runParams, workDir)
	req.RetryPath = path
	if err := validateSubWorkflowRequest(req); err != nil {
		return nil, err
	}
	if !e.shouldRunWithSubWorkflowRunner(ctx, req) {
		return nil, errNoSubWorkflowRunner
	}

	logger.Info(ctx, "Retrying sub DAG via injected sub-workflow runner", tag.Step(stepName))

	runCtx, cancel := context.WithCancel(ctx)
	e.trackRun(runParams.RunID, cancel)
	defer e.clearRun(runParams.RunID)

	if err := e.cancellationErr(ctx); err != nil {
		return nil, err
	}
	return e.subWorkflowRunner.Retry(runCtx, SubWorkflowRetryRequest{
		SubWorkflowRequest: req,
		StepName:           stepName,
		IncludeDownstream:  runctx.GetContext(ctx).IncludeDownstream,
	})
}

func validateSubWorkflowRequest(req SubWorkflowRequest) error {
	if req.RunID == "" {
		return errDAGRunIDNotSet
	}
	if req.RootDAGRun.Zero() {
		return errRootDAGRunNotSet
	}
	return nil
}

func (e *SubDAGExecutor) subWorkflowRequest(ctx context.Context, runParams RunParams, workDir string) SubWorkflowRequest {
	rCtx := runctx.GetContext(ctx)
	var parent ir.DAGRunRef
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
		ParallelItem:      runParams.ParallelItem,
		ProfileName:       rCtx.ProfileName,
		TriggerActor:      rCtx.TriggerActor,
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

func (e *SubDAGExecutor) trackRun(runID string, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activeRuns == nil {
		e.activeRuns = make(map[string]context.CancelFunc)
	}
	e.activeRuns[runID] = cancel
}

func (e *SubDAGExecutor) clearRun(runID string) {
	e.mu.Lock()
	cancel := e.activeRuns[runID]
	delete(e.activeRuns, runID)
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
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

// Kill cancels all running sub DAG executions.
func (e *SubDAGExecutor) Kill(sig os.Signal) error {
	return e.Stop(cmdutil.TerminationFromSignal(sig))
}

// Stop cancels all running sub DAG executions according to the requested lifecycle intent.
func (e *SubDAGExecutor) Stop(intent cmdutil.TerminationIntent) error {
	e.cancelOnce.Do(func() {
		close(e.killed)
	})

	type activeRun struct {
		runID  string
		cancel context.CancelFunc
	}

	e.mu.Lock()
	activeRuns := make([]activeRun, 0, len(e.activeRuns))
	for runID, cancel := range e.activeRuns {
		activeRuns = append(activeRuns, activeRun{
			runID:  runID,
			cancel: cancel,
		})
	}
	e.mu.Unlock()

	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, run := range activeRuns {
		if e.subWorkflowRunner != nil {
			if err := e.subWorkflowRunner.Cancel(ctx, SubWorkflowCancelRequest{
				DAG:        e.DAG,
				RootDAGRun: e.dagCtx.RootDAGRun,
				RunID:      run.runID,
				Intent:     subWorkflowCancelIntent(intent),
			}); err != nil {
				errs = append(errs, err)
				logger.Warn(ctx, "Failed to request sub DAG cancellation",
					tag.SubRunID(run.runID),
					tag.SubDAG(e.DAG.Name),
					tag.Error(err),
				)
			} else {
				logger.Info(ctx, "Requested sub DAG cancellation",
					tag.SubRunID(run.runID),
					tag.SubDAG(e.DAG.Name),
				)
			}
		} else if e.dagCtx.RunStateStore != nil {
			attempt, err := e.dagCtx.RunStateStore.OpenChildAttempt(ctx, e.dagCtx.RootDAGRun, run.runID)
			if err == nil {
				err = attempt.RequestCancel(ctx)
			}
			if err != nil {
				if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
					errs = append(errs, err)
					logger.Warn(ctx, "Failed to request child DAG cancellation",
						tag.SubRunID(run.runID),
						tag.SubDAG(e.DAG.Name),
						tag.Error(err),
					)
				}
			} else {
				logger.Info(ctx, "Requested sub DAG cancellation",
					tag.SubRunID(run.runID),
					tag.SubDAG(e.DAG.Name),
				)
			}
		}
		if run.cancel != nil {
			run.cancel()
		}
	}

	return errors.Join(errs...)
}

func subWorkflowCancelIntent(intent cmdutil.TerminationIntent) SubWorkflowCancelIntent {
	if intent.Mode == "" {
		intent = cmdutil.TerminationFromSignal(intent.Signal)
	}
	mode := SubWorkflowCancelModeGraceful
	if intent.IsForce() {
		mode = SubWorkflowCancelModeForce
	}
	return SubWorkflowCancelIntent{
		Mode:   mode,
		Signal: intent.Signal,
	}
}
