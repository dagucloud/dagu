// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.StatusDetailsProvider = (*foreachExecutor)(nil)
	_ executor.NodeStatusDeterminer  = (*foreachExecutor)(nil)
)

type foreachExecutor struct {
	step          ir.Step
	stdout        io.Writer
	stderr        io.Writer
	cancel        context.CancelFunc
	statusDetails []ir.NodeStatusDetail
	// outcome is what the last Run saw, for DetermineNodeStatus.
	outcome runOutcome
	// stepDir holds the item bodies' logs and records for the current run.
	stepDir string
}

// runOutcome counts the item bodies of one run and keeps the error a run
// that failed as a whole reports.
type runOutcome struct {
	total     int
	failed    int
	cancelled bool
	err       error
}

type expandedItem struct {
	index int
	key   string
	value any
}

type itemResult struct {
	Index   int               `json:"index"`
	Key     string            `json:"key"`
	Status  string            `json:"status"`
	Outputs map[string]string `json:"outputs,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type aggregateOutput struct {
	Summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"summary"`
	Items   []itemResult        `json:"items"`
	Outputs []map[string]string `json:"outputs"`
}

func newExecutor(_ context.Context, step ir.Step) (executor.Executor, error) {
	if step.Foreach == nil {
		return nil, fmt.Errorf("foreach configuration is missing")
	}
	return &foreachExecutor{step: step}, nil
}

func (e *foreachExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *foreachExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *foreachExecutor) Kill(_ os.Signal) error {
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *foreachExecutor) Run(ctx context.Context) error {
	e.statusDetails = nil
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	defer cancel()

	e.outcome = runOutcome{}
	items, err := e.expandItems(ctx)
	if err != nil {
		e.outcome.err = err
		return err
	}

	stepDir, cleanup, err := e.resolveStepDir(ctx)
	if err != nil {
		e.outcome.err = err
		return err
	}
	defer cleanup()
	e.stepDir = stepDir
	writeItems(ctx, stepDir, items)

	results, dispatchErr := e.runItems(ctx, items)
	e.statusDetails = foreachStatusDetails(items, results, e.step.Foreach.Key != "")
	e.outcome = summarize(results, dispatchErr)
	if err := e.writeAggregate(results); err != nil {
		e.outcome.err = err
		return err
	}
	return e.outcome.err
}

// summarize decides what a run reports. A run every item body failed, or
// one cut short, is an error; a run some item bodies failed is not, since
// the work of the others is done and published, and DetermineNodeStatus
// reports it as partially succeeded.
func summarize(results []itemResult, dispatchErr error) runOutcome {
	outcome := runOutcome{total: len(results)}
	var first string
	for _, result := range results {
		// An item a cancellation kept from starting neither succeeded nor
		// failed; the cancellation itself decides the outcome.
		if result.Status == ir.NodeSucceeded.String() || result.Status == ir.NodeNotStarted.String() {
			continue
		}
		outcome.failed++
		if first == "" {
			first = result.Error
			if first == "" {
				first = result.Status
			}
		}
	}
	switch {
	case dispatchErr != nil:
		// A cancelled run is aborted; a run that hit its deadline failed.
		outcome.cancelled = errors.Is(dispatchErr, context.Canceled)
		outcome.err = dispatchErr
	case outcome.total > 0 && outcome.failed == outcome.total:
		outcome.err = fmt.Errorf("all %d item bodies failed; first error: %s", outcome.total, first)
	}
	return outcome
}

// DetermineNodeStatus implements NodeStatusDeterminer: a run some item
// bodies failed is partially succeeded, so the steps after the loop run
// and the aggregate tells them which items failed.
func (e *foreachExecutor) DetermineNodeStatus() (ir.NodeStatus, error) {
	switch {
	case e.outcome.cancelled:
		return ir.NodeAborted, nil
	case e.outcome.err != nil:
		return ir.NodeFailed, e.outcome.err
	case e.outcome.failed > 0:
		return ir.NodePartiallySucceeded, nil
	default:
		return ir.NodeSucceeded, nil
	}
}

func (e *foreachExecutor) GetStatusDetails() []ir.NodeStatusDetail {
	return append([]ir.NodeStatusDetail(nil), e.statusDetails...)
}

func foreachStatusDetails(items []expandedItem, results []itemResult, useKey bool) []ir.NodeStatusDetail {
	details := make([]ir.NodeStatusDetail, 0, len(results))
	for i, result := range results {
		if i >= len(items) {
			break
		}
		details = append(details, ir.NodeStatusDetail{
			Label:  foreachItemLabel(items[i], useKey),
			Status: foreachItemStatus(result.Status),
		})
	}
	return details
}

func foreachItemLabel(item expandedItem, useKey bool) string {
	if useKey {
		return item.key
	}
	if value, ok := item.value.(string); ok {
		return value
	}
	if value, err := json.Marshal(item.value); err == nil {
		return string(value)
	}
	return item.key
}

func foreachItemStatus(status string) ir.NodeStatus {
	switch status {
	case ir.NodeSucceeded.String():
		return ir.NodeSucceeded
	case ir.NodeFailed.String():
		return ir.NodeFailed
	case ir.NodeAborted.String():
		return ir.NodeAborted
	case ir.NodePartiallySucceeded.String():
		return ir.NodePartiallySucceeded
	default:
		return ir.NodeNotStarted
	}
}

func (e *foreachExecutor) expandItems(ctx context.Context) ([]expandedItem, error) {
	cfg := e.step.Foreach
	values, err := resolveItems(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(values) > ir.MaxExpansionConcurrency {
		return nil, fmt.Errorf("foreach expansion produced %d items; maximum is %d", len(values), ir.MaxExpansionConcurrency)
	}

	items := make([]expandedItem, len(values))
	seen := make(map[string]int, len(values))
	for idx, value := range values {
		key := strconv.Itoa(idx)
		if cfg.Key != "" {
			itemCtx, err := contextWithItemScope(ctx, cfg.As, idx, "", value)
			if err != nil {
				return nil, err
			}
			key, err = resolveStringValue(itemCtx, cfg.Key, "foreach.key")
			if err != nil {
				return nil, fmt.Errorf("failed to resolve foreach.key for item %d: %w", idx, err)
			}
			if key == "" {
				return nil, fmt.Errorf("foreach.key resolved to an empty string for item %d", idx)
			}
			if containsUnresolvedSupportedReference(key) {
				return nil, fmt.Errorf("foreach.key resolved with unresolved reference for item %d: %s", idx, key)
			}
		}
		if prev, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate foreach item key %q at item %d; first seen at item %d", key, idx, prev)
		}
		seen[key] = idx
		items[idx] = expandedItem{index: idx, key: key, value: value}
	}
	return items, nil
}

func resolveItems(ctx context.Context, cfg *ir.ForeachConfig) ([]any, error) {
	if cfg.ItemsExpr != "" {
		resolved, err := resolveStringValue(ctx, cfg.ItemsExpr, "foreach.items")
		if err != nil {
			return nil, fmt.Errorf("failed to resolve foreach.items: %w", err)
		}
		var items []any
		if err := json.Unmarshal([]byte(resolved), &items); err != nil {
			return nil, fmt.Errorf("foreach.items string must resolve to a JSON array: %w", err)
		}
		return items, nil
	}

	resolved, err := runtime.EvalObject(ctx, map[string]any{"items": cfg.Items})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve foreach.items: %w", err)
	}
	items, ok := resolved["items"].([]any)
	if !ok {
		return nil, fmt.Errorf("foreach.items must resolve to an array, got %T", resolved["items"])
	}
	return normalizeJSONItems(items)
}

func normalizeJSONItems(items []any) ([]any, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("foreach.items must contain JSON-compatible values: %w", err)
	}
	var normalized []any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, fmt.Errorf("foreach.items must contain JSON-compatible values: %w", err)
	}
	return normalized, nil
}

func (e *foreachExecutor) runItems(ctx context.Context, items []expandedItem) ([]itemResult, error) {
	results := make([]itemResult, len(items))
	for _, item := range items {
		results[item.index] = itemResult{
			Index:  item.index,
			Key:    item.key,
			Status: ir.NodeNotStarted.String(),
		}
	}
	if len(items) == 0 {
		return results, nil
	}

	maxConcurrent := e.step.Foreach.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = ir.DefaultMaxConcurrent
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)
	var dispatchErr error
dispatch:
	for _, item := range items {
		select {
		case <-ctx.Done():
			dispatchErr = ctx.Err()
			break dispatch
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(item expandedItem) {
			defer wg.Done()
			defer func() { <-sem }()
			results[item.index] = e.runItem(ctx, item)
		}(item)
	}
	wg.Wait()
	if dispatchErr == nil {
		// A cancellation that arrived while the last items ran is reported
		// as such, not as those items' failure.
		dispatchErr = ctx.Err()
	}
	return results, dispatchErr
}

func (e *foreachExecutor) runItem(ctx context.Context, item expandedItem) itemResult {
	logDir := filepath.Join(e.stepDir, strconv.Itoa(item.index))
	recorder := newItemRecorder(logDir, item)
	// An item that fails before its body runs is still recorded, so the
	// record and the aggregate agree on it.
	failed := func(err error) itemResult {
		recorder.finished(ctx, ir.NodeFailed, err.Error())
		return itemResult{Index: item.index, Key: item.key, Status: ir.NodeFailed.String(), Error: err.Error()}
	}

	itemCtx, err := contextWithItemScope(ctx, e.step.Foreach.As, item.index, item.key, item.value)
	if err != nil {
		return failed(err)
	}

	plan, err := runtime.NewPlan(cloneSteps(e.step.Foreach.Steps)...)
	if err != nil {
		return failed(err)
	}
	recorder.planned(plan)

	runner := runtime.New(&runtime.Config{
		LogDir:   logDir,
		DAGRunID: bodyDAGRunID(ctx, item.index),
	})
	progressCh := make(chan runtime.ProgressUpdate)
	recorded := make(chan struct{})
	go func() {
		defer close(recorded)
		recorder.progress(ctx, progressCh)
	}()
	err = runner.Run(itemCtx, plan, progressCh)
	close(progressCh)
	<-recorded

	result := e.itemResult(itemCtx, plan, item, err, runner.Status(itemCtx, plan))
	recorder.finished(ctx, recordedItemStatus(ctx, result), result.Error)
	return result
}

// itemResult turns a body run's outcome into the item's aggregate entry.
func (e *foreachExecutor) itemResult(ctx context.Context, plan *runtime.Plan, item expandedItem, runErr error, status ir.Status) itemResult {
	if runErr != nil || status != ir.Succeeded {
		message := status.String()
		if runErr != nil {
			message = runErr.Error()
		}
		return itemResult{Index: item.index, Key: item.key, Status: ir.NodeFailed.String(), Error: message}
	}

	outputs, err := e.collectOutputs(ctx, plan)
	if err != nil {
		return itemResult{Index: item.index, Key: item.key, Status: ir.NodeFailed.String(), Error: err.Error()}
	}
	return itemResult{
		Index:   item.index,
		Key:     item.key,
		Status:  ir.NodeSucceeded.String(),
		Outputs: outputs,
	}
}

// recordedItemStatus is the status an item's record keeps. The aggregate
// reports a cancelled item as failed; the record tells it apart as aborted.
func recordedItemStatus(ctx context.Context, result itemResult) ir.NodeStatus {
	if result.Status == ir.NodeFailed.String() && errors.Is(ctx.Err(), context.Canceled) {
		return ir.NodeAborted
	}
	return foreachItemStatus(result.Status)
}

func cloneSteps(steps []ir.Step) []ir.Step {
	cloned := make([]ir.Step, len(steps))
	for i, step := range steps {
		cloned[i] = step
		if step.ExecutorConfig.Config != nil {
			cloned[i].ExecutorConfig.Config = make(map[string]any, len(step.ExecutorConfig.Config))
			maps.Copy(cloned[i].ExecutorConfig.Config, step.ExecutorConfig.Config)
		}
		cloned[i].Depends = append([]string(nil), step.Depends...)
		cloned[i].InferredDepends = append([]ir.InferredDependency(nil), step.InferredDepends...)
		cloned[i].Env = append([]string(nil), step.Env...)
		cloned[i].Commands = append([]ir.CommandEntry(nil), step.Commands...)
		cloned[i].Outputs = append([]ir.StepOutputDeclaration(nil), step.Outputs...)
	}
	return cloned
}

func (e *foreachExecutor) collectOutputs(ctx context.Context, plan *runtime.Plan) (map[string]string, error) {
	collect := e.step.Foreach.Collect
	outputs := make(map[string]string, len(collect))
	if len(collect) == 0 {
		return outputs, nil
	}

	env := runtime.NewPlanEnv(ctx, ir.Step{}, plan)
	ctx = runtime.WithEnv(ctx, env)
	for _, name := range sortedCollectNames(collect) {
		value, err := resolveStringValue(ctx, collect[name], "foreach.collect."+name)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve foreach.collect.%s: %w", name, err)
		}
		if containsUnresolvedSupportedReference(value) {
			return nil, fmt.Errorf("foreach.collect.%s resolved with unresolved reference: %s", name, value)
		}
		outputs[name] = value
	}
	return outputs, nil
}

func resolveStringValue(ctx context.Context, raw, key string) (string, error) {
	resolved, err := runtime.EvalObject(ctx, map[string]any{key: raw})
	if err != nil {
		return "", err
	}
	value, ok := resolved[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must resolve to a string, got %T", key, resolved[key])
	}
	return value, nil
}

func contextWithItemScope(ctx context.Context, alias string, index int, key string, item any) (context.Context, error) {
	env := runtime.GetEnv(ctx)
	scope := env.Scope
	if scope == nil {
		scope = cmnvalue.NewEnvScope(nil, false)
	}

	payload := map[string]any{
		"index": strconv.Itoa(index),
		"key":   key,
		alias:   item,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ctx, fmt.Errorf("failed to serialize foreach item scope: %w", err)
	}
	env.Scope = scope.WithEntry("foreach", string(data), cmnvalue.EnvSourceStepEnv)
	env.Foreach = cmnvalue.Values(payload)
	return runtime.WithEnv(ctx, env), nil
}

func containsUnresolvedSupportedReference(value string) bool {
	return strings.Contains(value, "${foreach.") ||
		strings.Contains(value, "${steps.") ||
		(strings.Contains(value, "${") && strings.Contains(value, ".outputs."))
}

func (e *foreachExecutor) writeAggregate(results []itemResult) error {
	output := aggregateOutput{
		Items:   results,
		Outputs: make([]map[string]string, 0, len(results)),
	}
	output.Summary.Total = len(results)
	for _, result := range results {
		switch result.Status {
		case ir.NodeSucceeded.String():
			output.Summary.Succeeded++
			if result.Outputs == nil {
				output.Outputs = append(output.Outputs, map[string]string{})
			} else {
				output.Outputs = append(output.Outputs, result.Outputs)
			}
		case ir.NodeNotStarted.String():
			// An item a cancellation kept from starting is listed, not
			// counted as a body that failed.
		default:
			output.Summary.Failed++
		}
	}

	w := e.stdout
	if w == nil {
		w = io.Discard
	}
	data, err := json.Marshal(output)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// scratchLogDirPrefix names the temporary directories holding body logs of
// runs that have no log directory of their own.
const scratchLogDirPrefix = "dagu-foreach-"

// resolveStepDir returns the directory holding the item bodies' logs and
// records. It sits beside the parent step's log file, where run removal
// finds it. A run without a log directory gets a scratch directory that the
// returned cleanup removes once the step has finished.
func (e *foreachExecutor) resolveStepDir(ctx context.Context) (string, func(), error) {
	keep := func() {}
	env := runtime.GetEnv(ctx)
	if env.Scope != nil {
		if stdout, ok := env.Scope.Get(runenv.EnvKeyDAGRunStepStdoutFile); ok && stdout != "" {
			return logpath.ForeachStepDir(stdout, e.step.Name), keep, nil
		}
	}
	rCtx := runtime.GetDAGContext(ctx)
	if rCtx.DAGRunLogDir != "" {
		return filepath.Join(rCtx.DAGRunLogDir, logpath.ForeachLogDirName, fileutil.SafeName(e.step.Name)), keep, nil
	}
	dir, err := os.MkdirTemp("", scratchLogDirPrefix)
	if err != nil {
		return "", keep, fmt.Errorf("failed to create scratch log directory: %w", err)
	}
	cleanup := func() {
		if err := fileutil.RemoveAll(dir); err != nil {
			logger.Warn(ctx, "Failed to remove scratch log directory",
				tag.Error(err),
				tag.Dir(dir))
		}
	}
	return dir, cleanup, nil
}

func bodyDAGRunID(ctx context.Context, index int) string {
	runID := runtime.GetDAGContext(ctx).DAGRunID
	if runID == "" {
		runID = "foreach"
	}
	return fmt.Sprintf("%s-foreach-%d", runID, index)
}

func sortedCollectNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

func init() {
	executor.RegisterExecutor(ir.ExecutorTypeForeach, newExecutor, nil, registry.ExecutorCapabilities{})
}
