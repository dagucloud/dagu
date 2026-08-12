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

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

const dagEnqueueQueueConfigKey = "queue"

var _ executor.DAGExecutor = (*enqueueExecutor)(nil)
var _ executor.ParallelExecutor = (*enqueueExecutor)(nil)
var _ executor.SubRunProvider = (*enqueueExecutor)(nil)

type enqueueExecutor struct {
	step ir.Step

	lock          sync.Mutex
	stdout        io.Writer
	stderr        io.Writer
	runParams     executor.RunParams
	runParamsList []executor.RunParams
	subRuns       []ir.SubDAGRun
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

func newEnqueueExecutor(_ context.Context, step ir.Step) (executor.Executor, error) {
	if step.SubDAG == nil {
		return nil, fmt.Errorf("sub DAG configuration is missing")
	}

	if rawQueue, ok := step.ExecutorConfig.Config[dagEnqueueQueueConfigKey]; ok {
		if _, ok := rawQueue.(string); !ok {
			return nil, fmt.Errorf("dag.enqueue with.queue must be a string")
		}
	}

	return &enqueueExecutor{step: step}, nil
}

func (e *enqueueExecutor) Run(ctx context.Context) error {
	paramsList, parallel := e.paramsSnapshot()
	if len(paramsList) == 0 {
		return fmt.Errorf("no sub DAG runs to enqueue")
	}

	outputs, err := e.enqueueAll(ctx, paramsList, parallel)
	if err != nil {
		return err
	}

	subRuns := make([]ir.SubDAGRun, 0, len(outputs))
	for _, output := range outputs {
		subRuns = append(subRuns, subDAGRunFromEnqueueOutput(output))
	}

	e.lock.Lock()
	e.subRuns = subRuns
	e.lock.Unlock()

	return e.writeOutput(outputs, parallel)
}

func (e *enqueueExecutor) enqueueAll(ctx context.Context, paramsList []executor.RunParams, parallel bool) ([]enqueueRunOutput, error) {
	if !parallel || len(paramsList) == 1 {
		return e.enqueueSequential(ctx, paramsList)
	}
	return e.enqueueParallel(ctx, paramsList)
}

func (e *enqueueExecutor) enqueueSequential(ctx context.Context, paramsList []executor.RunParams) ([]enqueueRunOutput, error) {
	outputs := make([]enqueueRunOutput, 0, len(paramsList))
	for _, params := range paramsList {
		output, err := e.enqueueOne(ctx, params)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (e *enqueueExecutor) enqueueParallel(ctx context.Context, paramsList []executor.RunParams) ([]enqueueRunOutput, error) {
	limit := ir.DefaultMaxConcurrent
	if e.step.Parallel != nil && e.step.Parallel.MaxConcurrent > 0 {
		limit = e.step.Parallel.MaxConcurrent
	}
	if limit > len(paramsList) {
		limit = len(paramsList)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	outputs := make([]enqueueRunOutput, len(paramsList))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	setErr := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	for i, params := range paramsList {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			output, err := e.enqueueOne(ctx, params)
			if err != nil {
				setErr(err)
				return
			}
			outputs[i] = output
		})
	}
	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return outputs, nil
}

func subDAGRunFromEnqueueOutput(output enqueueRunOutput) ir.SubDAGRun {
	return ir.SubDAGRun{
		DAGRunID: output.DAGRunID,
		Params:   output.Params,
		DAGName:  output.Name,
	}
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

	if err := validateSubDAG(child.DAG, target, nil); err != nil {
		return enqueueRunOutput{}, err
	}

	dagCopy, err := spec.ResolveRuntimeParams(ctx, child.DAG, runParams.Params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: config.GetConfig(ctx).Paths.BaseConfig,
	})
	if err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to resolve sub DAG params: %w", err)
	}
	dagCopy = dagCopy.Clone()
	dagCopy.Location = ""
	if len(runParams.WorkerSelector) > 0 {
		dagCopy.WorkerSelector = maps.Clone(runParams.WorkerSelector)
	}
	if err := validateSubDAG(dagCopy, target, dagCopy.WorkerSelector); err != nil {
		return enqueueRunOutput{}, err
	}

	queueName := dagCopy.ProcGroup()
	if queueOverride := e.queueOverride(); queueOverride != "" {
		dagCopy.Queue = queueOverride
		queueName = queueOverride
	}

	dagRun := ir.NewDAGRunRef(dagCopy.Name, runParams.RunID)
	if existing, err := rCtx.DAGRunStore.FindAttempt(ctx, dagRun); err == nil {
		return e.outputFromExisting(ctx, existing, dagCopy.Name, runParams, queueName), nil
	} else if !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return enqueueRunOutput{}, fmt.Errorf("failed to check existing DAG run: %w", err)
	}

	_, err = intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunStore:     rCtx.DAGRunStore,
		QueueStore:      rCtx.QueueStore,
		DAG:             dagCopy,
		DAGRunID:        runParams.RunID,
		QueueName:       queueName,
		LogBaseDir:      rCtx.DAGRunLogDir,
		ArtifactBaseDir: rCtx.DAGRunArtifactDir,
		TriggerType:     ir.TriggerTypeSubDAG,
		TriggerActor:    rCtx.TriggerActor,
		ProfileName:     rCtx.ProfileName,
	})
	if err != nil {
		return enqueueRunOutput{}, fmt.Errorf("failed to enqueue DAG run: %w", err)
	}

	logger.Info(ctx, "Enqueued sub DAG run",
		tag.SubDAG(dagCopy.Name),
		tag.SubRunID(runParams.RunID),
		tag.Queue(queueName),
		slog.Any("params", dagCopy.Params),
	)

	return enqueueRunOutput{
		Name:     dagCopy.Name,
		DAGRunID: runParams.RunID,
		Params:   runParams.Params,
		Queue:    queueName,
		Status:   ir.Queued.String(),
	}, nil
}

func (e *enqueueExecutor) outputFromExisting(ctx context.Context, attempt dagrun.Attempt, dagName string, params executor.RunParams, queueName string) enqueueRunOutput {
	statusText := ir.Queued.String()
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
			if output.Status == ir.Queued.String() && !output.AlreadyExists {
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

func (e *enqueueExecutor) GetSubRuns() []ir.SubDAGRun {
	e.lock.Lock()
	defer e.lock.Unlock()
	return append([]ir.SubDAGRun(nil), e.subRuns...)
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
	caps := registry.ExecutorCapabilities{
		SubDAG:         true,
		WorkerSelector: true,
	}
	executor.RegisterExecutor(ir.ExecutorTypeDAGEnqueue, newEnqueueExecutor, nil, caps)
}
