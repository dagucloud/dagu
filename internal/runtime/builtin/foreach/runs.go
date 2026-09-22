// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/goccy/go-yaml"
)

// Each item owns a persisted child attempt; its execution environment keeps the
// enclosing step outputs and foreach bindings available to the body.
type itemRun struct {
	ctx        context.Context
	item       expandedItem
	ref        ir.SubDAGRun
	parent     ir.DAGRunRef
	root       ir.DAGRunRef
	dag        *ir.DAG
	plan       *runtime.Plan
	runner     *runtime.Runner
	attempt    runstate.Attempt
	started    time.Time
	pidStarted int64
	open       bool
	lastState  ir.Status
	logFile    string
	logWriter  *os.File
	log        logger.Logger
}

func (e *foreachExecutor) GetSubRuns() []ir.SubDAGRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ir.SubDAGRun(nil), e.subRuns...)
}

func (e *foreachExecutor) SetProgressCallback(callback func()) { e.progress = callback }

func (e *foreachExecutor) notifyProgress() {
	if e.progress != nil {
		e.progress()
	}
}

func (e *foreachExecutor) prepareItem(ctx context.Context, item expandedItem) (*itemRun, error) {
	itemCtx, err := contextWithItemScope(ctx, e.step.Foreach.As, item.index, item.key, item.value)
	if err != nil {
		return nil, err
	}
	parent := runtime.GetDAGContext(ctx)
	root := parent.RootDAGRun
	if root.Zero() {
		root = parent.DAGRunRef()
	}
	metadata, err := json.Marshal(struct {
		Index int    `json:"index"`
		Key   string `json:"key"`
		Item  any    `json:"item"`
	}{item.index, item.key, item.value})
	if err != nil {
		return nil, err
	}
	runID := runtime.GenerateSubDAGRunIDForTarget(ctx, "foreach", parent.AttemptID+":"+string(metadata), e.step.RepeatPolicy.RepeatMode != "")
	child := *parent.DAG
	child.Name = e.step.Name
	child.Type = ir.TypeGraph
	child.Steps = cloneSteps(e.step.Foreach.Steps)
	child.HandlerOn = ir.HandlerOn{}
	child.Preconditions = nil
	child.RetryPolicy = nil
	child.Location = ""
	child.YamlData, err = bodySpec(parent.DAG.YamlData, e.step)
	if err != nil {
		return nil, err
	}
	plan, err := runtime.NewPlan(child.Steps...)
	if err != nil {
		return nil, err
	}
	request := runstate.BeginAttemptRequest{DAG: &child, RunID: runID, RootDAGRun: root}
	var attempt runstate.Attempt
	if parent.RunStateStore != nil {
		if _, lookupErr := parent.RunStateStore.OpenChildAttempt(ctx, root, runID); lookupErr == nil {
			request.Retry = true
		}
		attempt, err = parent.RunStateStore.BeginAttempt(ctx, request)
	} else {
		attempt = runstate.NewNoopAttempt(request)
	}
	if err != nil {
		return nil, err
	}
	if err = attempt.Open(ctx); err != nil {
		_ = attempt.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	childContext := parent
	childContext.DAG = &child
	childContext.DAGRunID = runID
	childContext.AttemptID = attempt.ID()
	childContext.RootDAGRun = root
	childContext.TriggerType = ir.TriggerTypeSubDAG
	// A dedicated item directory also separates nested loops with identical IDs.
	logDir := filepath.Join(bodyLogDir(ctx), runID, attempt.ID())
	childContext.EnvScope = parent.EnvScope.WithEntry(runenv.EnvKeyDAGRunLogFile, filepath.Join(logDir, "run.log"), cmnvalue.EnvSourceDAGEnv)
	itemCtx = runtime.WithDAGContext(itemCtx, childContext)
	env := runtime.GetEnv(itemCtx)
	env.Context = childContext
	env.Scope = env.Scope.WithEntry(runenv.EnvKeyDAGRunLogFile, filepath.Join(logDir, "run.log"), cmnvalue.EnvSourceDAGEnv)
	itemCtx = runtime.WithEnv(itemCtx, env)
	pidStarted, _ := procutil.StartTime(os.Getpid())
	run := &itemRun{ctx: itemCtx, item: item, dag: &child, plan: plan, attempt: attempt, root: root, parent: parent.DAGRunRef(), pidStarted: pidStarted,
		open: true, logFile: filepath.Join(logDir, "run.log"),
		ref:    ir.SubDAGRun{DAGRunID: runID, DAGName: child.Name, Params: string(metadata)},
		runner: runtime.New(&runtime.Config{LogDir: logDir, DAGRunID: runID, MessagesHandler: attempt}),
	}
	if err = run.record(ir.Queued, nil); err != nil {
		_ = attempt.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return run, nil
}

func (r *itemRun) record(state ir.Status, executionErr error) error {
	if !r.open {
		if err := r.attempt.Open(context.WithoutCancel(r.ctx)); err != nil {
			return err
		}
		r.open = true
	}
	var logErr error
	if state != r.lastState || executionErr != nil {
		logErr = r.logState(state, executionErr)
		if logErr != nil {
			executionErr = errors.Join(executionErr, logErr)
			state = ir.Failed
		}
		r.lastState = state
	}

	if state == ir.Running && r.started.IsZero() {
		r.started = time.Now()
	}
	opts := []ir.StatusOption{
		ir.WithHierarchyRefs(r.root, r.parent),
		ir.WithLogFilePath(r.logFile),
		ir.WithAttemptID(r.attempt.ID()),
		ir.WithPIDStartedAt(r.pidStarted),
		ir.WithTriggerType(ir.TriggerTypeSubDAG),
		ir.WithWorkerID(runtime.GetDAGContext(r.ctx).WorkerID),
		transform.WithNodes(r.plan.NodeData()),
	}
	if state != ir.Running && state != ir.Queued {
		opts = append(opts, ir.WithFinishedAt(time.Now()))
	}
	if executionErr != nil {
		opts = append(opts, ir.WithError(executionErr.Error()))
	}
	status := ir.NewStatusBuilder(r.dag).Create(r.ref.DAGRunID, state, os.Getpid(), r.started, opts...)
	status.Params = r.ref.Params
	err := errors.Join(logErr, r.attempt.RecordStatus(context.WithoutCancel(r.ctx), status))
	if state != ir.Running {
		err = errors.Join(err, r.close())
	}
	return err
}

func (r *itemRun) close() error {
	if !r.open {
		return nil
	}
	r.open = false
	return r.attempt.Close(context.WithoutCancel(r.ctx))
}

func (r *itemRun) openLog() error {
	if err := fileutil.MkdirAll(filepath.Dir(r.logFile), 0750); err != nil {
		return err
	}
	file, err := fileutil.OpenOrCreateFile(r.logFile)
	if err != nil {
		return err
	}
	r.logWriter = file
	r.log = logger.NewLogger(logger.WithRunWriter(file), logger.WithQuiet())
	r.ctx = logger.WithLogger(r.ctx, r.log)
	return nil
}

func (r *itemRun) logState(state ir.Status, executionErr error) error {
	transient := r.logWriter == nil
	if transient {
		if err := r.openLog(); err != nil {
			return err
		}
		defer func() { _ = r.logWriter.Close(); r.logWriter = nil; r.log = nil }()
	}
	if executionErr != nil {
		r.log.Error("Foreach item failed", slog.String("error", executionErr.Error()))
	} else {
		r.log.Info("Foreach item "+state.String(), slog.Int("index", r.item.index), slog.String("key", r.item.key))
	}
	return nil
}

func bodySpec(data []byte, step ir.Step) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("read foreach body specification: %w", err)
	}
	var candidates []map[string]any
	switch steps := document["steps"].(type) {
	case []any:
		for _, candidate := range steps {
			if item, ok := candidate.(map[string]any); ok {
				candidates = append(candidates, item)
			}
		}
	case map[string]any:
		for id, candidate := range steps {
			if item, ok := candidate.(map[string]any); ok {
				if _, present := item["id"]; !present {
					item["id"] = id
				}
				if _, present := item["name"]; !present {
					item["name"] = id
				}
				candidates = append(candidates, item)
			}
		}
	}
	for _, candidate := range candidates {
		if candidate["id"] != step.ID && candidate["name"] != step.Name {
			continue
		}
		body, ok := candidate["foreach"].(map[string]any)
		if !ok {
			continue
		}
		return yaml.Marshal(map[string]any{"name": step.Name, "type": ir.TypeGraph, "steps": body["steps"]})
	}
	return nil, fmt.Errorf("foreach body specification not found for %s", step.Name)
}
