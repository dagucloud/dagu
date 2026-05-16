// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/transform"
)

const dagEnqueueQueueConfigKey = "queue"

var _ executor.DAGExecutor = (*enqueueExecutor)(nil)
var _ executor.ParallelExecutor = (*enqueueExecutor)(nil)
var _ executor.SubRunProvider = (*enqueueExecutor)(nil)

type enqueueExecutor struct {
	step core.Step

	lock          sync.Mutex
	stdout        io.Writer
	stderr        io.Writer
	runParams     executor.RunParams
	runParamsList []executor.RunParams
	subRuns       []exec1.SubDAGRun
}

type enqueueRunOutput struct {
	Name          string `json:"name"`
	DAGRunID      string `json:"dagRunId"`
	Params        string `json:"params,omitempty"`
	Queue         string `json:"queue"`
	Status        string `json:"status"`
	AlreadyExists bool   `json:"alreadyExists,omitempty"`
}

type enqueueRunsOutput struct {
	Summary struct {
		Total  int `json:"total"`
		Queued int `json:"queued"`
	} `json:"summary"`
	Runs []enqueueRunOutput `json:"runs"`
}

func newEnqueueExecutor(_ context.Context, step core.Step) (executor.Executor, error) {
	if step.SubDAG == nil {
		return nil, fmt.Errorf("sub DAG configuration is missing")
	}

	if rawQueue, ok := step.ExecutorConfig.Config[dagEnqueueQueueConfigKey]; ok {
		queue, ok := rawQueue.(string)
		if !ok {
			return nil, fmt.Errorf("dag.enqueue with.queue must be a string")
		}
		step.ExecutorConfig.Config[dagEnqueueQueueConfigKey] = strings.TrimSpace(queue)
	}

	return &enqueueExecutor{step: step}, nil
}

func (e *enqueueExecutor) Run(ctx context.Context) error {
	paramsList, parallel := e.paramsSnapshot()
	if len(paramsList) == 0 {
		return fmt.Errorf("no sub DAG runs to enqueue")
	}

	outputs := make([]enqueueRunOutput, 0, len(paramsList))
	subRuns := make([]exec1.SubDAGRun, 0, len(paramsList))
	for _, params := range paramsList {
		output, err := e.enqueueOne(ctx, params)
		if err != nil {
			return err
		}
		outputs = append(outputs, output)
		subRuns = append(subRuns, exec1.SubDAGRun{
			DAGRunID: output.DAGRunID,
			Params:   output.Params,
			DAGName:  output.Name,
		})
	}

	e.lock.Lock()
	e.subRuns = subRuns
	e.lock.Unlock()

	return e.writeOutput(outputs, parallel)
}

func (e *enqueueExecutor) enqueueOne(ctx context.Context, runParams executor.RunParams) (enqueueRunOutput, error) {
	if runParams.RunID == "" {
		return enqueueRunOutput{}, fmt.Errorf("DAG run ID is not set")
	}

	rCtx := runtime.GetDAGContext(ctx)
	if rCtx.DAGRunStore == nil {
		return enqueueRunOutput{}, fmt.Errorf("dag.enqueue requires a DAG run store")
	}
	if rCtx.QueueStore == nil {
		return enqueueRunOutput{}, fmt.Errorf("dag.enqueue requires a queue store")
	}
	if !config.GetConfig(ctx).Queues.Enabled {
		return enqueueRunOutput{}, fmt.Errorf("queues are disabled in configuration")
	}

	target := runParams.DAGName
	if target == "" && e.step.SubDAG != nil {
		target = e.step.SubDAG.Name
	}
	if target == "" {
		return enqueueRunOutput{}, fmt.Errorf("sub DAG name is not set")
	}

	child, err := executor.NewSubDAGExecutor(ctx, target)
	if err != nil {
		return enqueueRunOutput{}, err
	}
	defer func() {
		if err := child.Cleanup(context.WithoutCancel(ctx)); err != nil {
			logger.Error(ctx, "Failed to cleanup sub DAG executor", tag.Error(err))
		}
	}()

	if len(e.step.WorkerSelector) > 0 && child.DAG.HasApprovalSteps() {
		return enqueueRunOutput{}, fmt.Errorf("%w: %s", ErrApprovalStepsWithWorker, target)
	}

	dagCopy, err := spec.ResolveRuntimeParams(ctx, child.DAG, runParams.Params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: config.GetConfig(ctx).Paths.BaseConfig,
	})
	if err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to resolve sub DAG params: %w", err)
	}
	dagCopy = dagCopy.Clone()
	dagCopy.Location = ""
	if len(e.step.WorkerSelector) > 0 {
		dagCopy.WorkerSelector = maps.Clone(e.step.WorkerSelector)
	}

	queueName := dagCopy.ProcGroup()
	if queueOverride := e.queueOverride(); queueOverride != "" {
		dagCopy.Queue = queueOverride
		queueName = queueOverride
	}

	dagRun := exec1.NewDAGRunRef(dagCopy.Name, runParams.RunID)
	if existing, err := rCtx.DAGRunStore.FindAttempt(ctx, dagRun); err == nil {
		return e.outputFromExisting(ctx, existing, dagCopy.Name, runParams, queueName), nil
	} else if !errors.Is(err, exec1.ErrDAGRunIDNotFound) {
		return enqueueRunOutput{}, fmt.Errorf("failed to check existing DAG run: %w", err)
	}

	logFile, err := logpath.Generate(ctx, rCtx.DAGRunLogDir, dagCopy.LogDir, dagCopy.Name, runParams.RunID)
	if err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to generate log file name: %w", err)
	}

	artifactDir := ""
	if dagCopy.ArtifactsEnabled() {
		artifactDir, err = logpath.GenerateDir(ctx, rCtx.DAGRunArtifactDir, dagCopy.Artifacts.Dir, dagCopy.Name, runParams.RunID)
		if err != nil {
			return enqueueRunOutput{}, fmt.Errorf("failed to generate artifact directory: %w", err)
		}
	}

	attempt, err := rCtx.DAGRunStore.CreateAttempt(ctx, dagCopy, time.Now(), runParams.RunID, exec1.NewDAGRunAttemptOptions{})
	if err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to create queued DAG run: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		if rmErr := rCtx.DAGRunStore.RemoveDAGRun(ctx, dagRun); rmErr != nil {
			logger.Error(ctx, "Failed to rollback queued DAG run",
				tag.DAG(dagCopy.Name),
				tag.RunID(runParams.RunID),
				tag.Error(rmErr),
			)
		}
	}()

	queuedAt := stringutil.FormatTime(time.Now())
	status := transform.NewStatusBuilder(dagCopy).Create(
		runParams.RunID,
		core.Queued,
		0,
		time.Time{},
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(artifactDir),
		transform.WithAttemptID(attempt.ID()),
		transform.WithPreconditions(dagCopy.Preconditions),
		transform.WithQueuedAt(queuedAt),
		transform.WithHierarchyRefs(dagRun, exec1.DAGRunRef{}),
		transform.WithTriggerType(core.TriggerTypeSubDAG),
	)

	if err := attempt.Open(ctx); err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to open queued DAG run: %w", err)
	}
	if err := attempt.Write(ctx, status); err != nil {
		_ = attempt.Close(ctx)
		return enqueueRunOutput{}, fmt.Errorf("failed to save queued DAG run status: %w", err)
	}
	if err := attempt.Close(ctx); err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to close queued DAG run: %w", err)
	}

	if err := rCtx.QueueStore.Enqueue(ctx, queueName, exec1.QueuePriorityLow, dagRun); err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to enqueue DAG run: %w", err)
	}
	committed = true

	logger.Info(ctx, "Enqueued sub DAG run",
		tag.DAG(dagCopy.Name),
		tag.RunID(runParams.RunID),
		tag.Queue(queueName),
		slog.Any("params", dagCopy.Params),
	)

	return enqueueRunOutput{
		Name:     dagCopy.Name,
		DAGRunID: runParams.RunID,
		Params:   runParams.Params,
		Queue:    queueName,
		Status:   core.Queued.String(),
	}, nil
}

func (e *enqueueExecutor) outputFromExisting(ctx context.Context, attempt exec1.DAGRunAttempt, dagName string, params executor.RunParams, queueName string) enqueueRunOutput {
	statusText := core.Queued.String()
	if status, err := attempt.ReadStatus(ctx); err == nil && status != nil {
		statusText = status.Status.String()
	}
	return enqueueRunOutput{
		Name:          dagName,
		DAGRunID:      params.RunID,
		Params:        params.Params,
		Queue:         queueName,
		Status:        statusText,
		AlreadyExists: true,
	}
}

func (e *enqueueExecutor) queueOverride() string {
	e.lock.Lock()
	defer e.lock.Unlock()

	queue, _ := e.step.ExecutorConfig.Config[dagEnqueueQueueConfigKey].(string)
	return strings.TrimSpace(queue)
}

func (e *enqueueExecutor) writeOutput(outputs []enqueueRunOutput, parallel bool) error {
	if e.stdout == nil {
		return nil
	}

	var payload any
	if !parallel && len(outputs) == 1 {
		payload = outputs[0]
	} else {
		summary := enqueueRunsOutput{Runs: outputs}
		summary.Summary.Total = len(outputs)
		for _, output := range outputs {
			if output.Status == core.Queued.String() && !output.AlreadyExists {
				summary.Summary.Queued++
			}
		}
		payload = summary
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal enqueue output: %w", err)
	}
	data = append(data, '\n')
	if _, err := e.stdout.Write(data); err != nil {
		return fmt.Errorf("failed to write enqueue output: %w", err)
	}
	return nil
}

func (e *enqueueExecutor) paramsSnapshot() ([]executor.RunParams, bool) {
	e.lock.Lock()
	defer e.lock.Unlock()

	if len(e.runParamsList) > 0 {
		return append([]executor.RunParams(nil), e.runParamsList...), true
	}
	if e.runParams.RunID != "" {
		return []executor.RunParams{e.runParams}, false
	}
	return nil, false
}

func (e *enqueueExecutor) SetParams(params executor.RunParams) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.runParams = params
	e.runParamsList = nil
}

func (e *enqueueExecutor) SetParamsList(paramsList []executor.RunParams) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.runParams = executor.RunParams{}
	e.runParamsList = append([]executor.RunParams(nil), paramsList...)
}

func (e *enqueueExecutor) GetSubRuns() []exec1.SubDAGRun {
	e.lock.Lock()
	defer e.lock.Unlock()
	return append([]exec1.SubDAGRun(nil), e.subRuns...)
}

func (e *enqueueExecutor) SetStdout(out io.Writer) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.stdout = out
}

func (e *enqueueExecutor) SetStderr(out io.Writer) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.stderr = out
}

func (e *enqueueExecutor) Kill(os.Signal) error {
	return nil
}

func init() {
	caps := core.ExecutorCapabilities{
		SubDAG:         true,
		WorkerSelector: true,
	}
	executor.RegisterExecutor(core.ExecutorTypeDAGEnqueue, newEnqueueExecutor, nil, caps)
}
