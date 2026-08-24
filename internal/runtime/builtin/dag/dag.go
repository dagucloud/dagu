// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var _ executor.DAGExecutor = (*dagExecutor)(nil)
var _ executor.NodeStatusDeterminer = (*dagExecutor)(nil)
var _ executor.OutputsProvider = (*dagExecutor)(nil)

type dagExecutor struct {
	child     *executor.SubDAGExecutor
	lock      sync.Mutex
	workDir   string
	stdout    io.Writer
	stderr    io.Writer
	runParams executor.RunParams
	step      ir.Step
	result    *ir.RunStatus
	outputs   map[string]any
	cancel    context.CancelFunc
}

// declaredChildOutputs returns the outputs a child run published explicitly,
// through `outputs.write` or `stdout.outputs`. It returns nil when the child
// published nothing that way, so a step whose child only sets flat `output:`
// variables keeps reporting through the child run itself.
func declaredChildOutputs(result *ir.RunStatus) map[string]any {
	if result == nil || len(result.OutputValues) == 0 {
		return nil
	}
	outputs := make(map[string]any, len(result.OutputValues))
	maps.Copy(outputs, result.OutputValues)
	return outputs
}

func (e *dagExecutor) setOutputs(outputs map[string]any) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.outputs = outputs
}

// GetOutputs implements executor.OutputsProvider.
func (e *dagExecutor) GetOutputs() map[string]any {
	e.lock.Lock()
	defer e.lock.Unlock()
	if len(e.outputs) == 0 {
		return nil
	}
	outputs := make(map[string]any, len(e.outputs))
	maps.Copy(outputs, e.outputs)
	return outputs
}

// Errors for DAG executor
var (
	ErrWorkingDirNotExist      = fmt.Errorf("working directory does not exist")
	ErrApprovalStepsWithWorker = fmt.Errorf("sub-DAG with approval steps cannot be dispatched to workers")
	ErrHumanTaskStepsInSubDAG  = fmt.Errorf("human task steps are not allowed in sub-DAGs")
)

// errTargetRunMissing reports that a retry targets a child DAG run the step
// does not produce.
func errTargetRunMissing(runID, stepName string) error {
	return fmt.Errorf("target child DAG run %s is not present in step %s", runID, stepName)
}

func validateSubDAG(childDAG *ir.DAG, name string, workerSelector map[string]string) error {
	if len(workerSelector) > 0 && childDAG.HasApprovalSteps() {
		return fmt.Errorf("%w: %s", ErrApprovalStepsWithWorker, name)
	}
	if childDAG.HasHumanTaskSteps() {
		return fmt.Errorf("%w: %s", ErrHumanTaskStepsInSubDAG, name)
	}
	return nil
}

func newDAGExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	if step.SubDAG == nil {
		return nil, fmt.Errorf("sub DAG configuration is missing")
	}

	child, err := executor.NewSubDAGExecutor(ctx, step.SubDAG.Name)
	if err != nil {
		return nil, err
	}

	if err := validateSubDAG(child.DAG, step.SubDAG.Name, nil); err != nil {
		_ = child.Cleanup(context.WithoutCancel(ctx))
		return nil, err
	}

	dir := runtime.GetEnv(ctx).WorkingDir
	if dir != "" && !fileutil.FileExists(dir) {
		_ = child.Cleanup(context.WithoutCancel(ctx))
		return nil, ErrWorkingDirNotExist
	}

	return &dagExecutor{
		child:   child,
		workDir: dir,
		step:    step,
	}, nil
}

func (e *dagExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	defer cancel()

	// Ensure cleanup happens even if there's an error
	defer func() {
		if err := e.child.Cleanup(ctx); err != nil {
			logger.Error(ctx, "Failed to cleanup sub DAG executor", tag.Error(err))
		}
	}()

	runParams, err := resolveChildRunParams(ctx, e.child.DAG, e.runParams)
	if err != nil {
		return err
	}
	if err := validateSubDAG(e.child.DAG, e.child.DAG.Name, runParams.WorkerSelector); err != nil {
		return err
	}
	e.runParams = runParams
	e.child.SetWorkerSelector(runParams.WorkerSelector)

	var result *ir.RunStatus
	var execErr error
	path := runctx.GetContext(ctx).RetryPath
	if hop, ok := path.Current(); ok && hop.Step == e.step.Name {
		if hop.RunID != e.runParams.RunID {
			return errTargetRunMissing(hop.RunID, e.step.Name)
		}
		result, execErr = e.child.Retry(ctx, e.runParams, path.NextStep(), e.workDir, path.Advance())
	} else {
		result, execErr = e.child.Execute(ctx, e.runParams, e.workDir)
	}
	if result != nil {
		e.lock.Lock()
		e.result = result
		e.lock.Unlock()
	}

	e.setOutputs(declaredChildOutputs(result))

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal outputs: %w", execErr)
	}

	// Add a newline at the end of the JSON output
	jsonData = append(jsonData, '\n')

	if e.stdout != nil {
		if _, err := e.stdout.Write(jsonData); err != nil {
			return fmt.Errorf("failed to write outputs: %w", err)
		}
	}

	return execErr
}

// DetermineNodeStatus implements NodeStatusDeterminer.
func (e *dagExecutor) DetermineNodeStatus() (ir.NodeStatus, error) {
	if e.result == nil {
		return ir.NodeFailed, fmt.Errorf("sub DAG %q execution produced no result", e.child.DAG.Name)
	}

	// Check if the status is partial success or success
	// For error cases, we return an error with the status
	switch e.result.Status {
	case ir.Succeeded:
		return ir.NodeSucceeded, nil
	case ir.PartiallySucceeded:
		return ir.NodePartiallySucceeded, nil
	case ir.Waiting:
		// Sub-DAG is waiting for human approval
		// Propagate the waiting status to the parent
		return ir.NodeWaiting, nil
	case ir.Aborted:
		if e.result.PreconditionNotMet {
			return ir.NodeSkipped, nil
		}
		return ir.NodeFailed, fmt.Errorf("sub DAG run %s failed with status: %s", e.result.DAGRunID, e.result.Status)
	case ir.NotStarted, ir.Running, ir.Failed, ir.Queued:
		return ir.NodeFailed, fmt.Errorf("sub DAG run %s failed with status: %s", e.result.DAGRunID, e.result.Status)
	default:
		// This should never happen, but satisfies the exhaustive check
		return ir.NodeFailed, fmt.Errorf("sub DAG run %s failed with unknown status: %s", e.result.DAGRunID, e.result.Status)
	}
}

func (e *dagExecutor) SetParams(params executor.RunParams) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.runParams = params
}

func (e *dagExecutor) SetStdout(out io.Writer) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.stdout = out
}

func (e *dagExecutor) SetStderr(out io.Writer) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.stderr = out
}

func (e *dagExecutor) Kill(sig os.Signal) error {
	// Kill all child processes (both local and distributed)
	var err error
	if e.child != nil {
		err = e.child.Kill(sig)
	}
	if e.cancel != nil {
		e.cancel()
	}
	return err
}

func init() {
	caps := registry.ExecutorCapabilities{
		SubDAG:         true,
		WorkerSelector: true,
	}
	executor.RegisterExecutor("subworkflow", newDAGExecutor, nil, caps)
	executor.RegisterExecutor("dag", newDAGExecutor, nil, caps)
}
