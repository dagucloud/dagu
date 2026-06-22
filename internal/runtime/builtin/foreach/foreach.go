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

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

var errForeachItemFailed = errors.New("one or more foreach item bodies failed")

type foreachExecutor struct {
	step   core.Step
	stdout io.Writer
	stderr io.Writer
	cancel context.CancelFunc
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

func newExecutor(_ context.Context, step core.Step) (executor.Executor, error) {
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
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	defer cancel()

	items, err := e.expandItems(ctx)
	if err != nil {
		return err
	}

	results, runErr := e.runItems(ctx, items)
	if err := e.writeAggregate(results); err != nil {
		return err
	}
	return runErr
}

func (e *foreachExecutor) expandItems(ctx context.Context) ([]expandedItem, error) {
	cfg := e.step.Foreach
	values, err := resolveItems(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(values) > core.MaxExpansionConcurrency {
		return nil, fmt.Errorf("foreach expansion produced %d items; maximum is %d", len(values), core.MaxExpansionConcurrency)
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

func resolveItems(ctx context.Context, cfg *core.ForeachConfig) ([]any, error) {
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
			Status: core.NodeNotStarted.String(),
		}
	}
	if len(items) == 0 {
		return results, nil
	}

	maxConcurrent := e.step.Foreach.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = core.DefaultMaxConcurrent
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)
	for _, item := range items {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
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

	var failed bool
	for _, result := range results {
		if result.Status != core.NodeSucceeded.String() {
			failed = true
			break
		}
	}
	if failed {
		return results, errForeachItemFailed
	}
	return results, nil
}

func (e *foreachExecutor) runItem(ctx context.Context, item expandedItem) itemResult {
	itemCtx, err := contextWithItemScope(ctx, e.step.Foreach.As, item.index, item.key, item.value)
	if err != nil {
		return itemResult{Index: item.index, Key: item.key, Status: core.NodeFailed.String(), Error: err.Error()}
	}

	plan, err := runtime.NewPlan(cloneSteps(e.step.Foreach.Steps)...)
	if err != nil {
		return itemResult{Index: item.index, Key: item.key, Status: core.NodeFailed.String(), Error: err.Error()}
	}

	bodyRunID := bodyDAGRunID(ctx, item.index)
	runner := runtime.New(&runtime.Config{
		LogDir:   bodyLogDir(ctx),
		DAGRunID: bodyRunID,
	})
	err = runner.Run(itemCtx, plan, nil)
	status := runner.Status(itemCtx, plan)
	if err != nil || status != core.Succeeded {
		message := status.String()
		if err != nil {
			message = err.Error()
		}
		return itemResult{Index: item.index, Key: item.key, Status: core.NodeFailed.String(), Error: message}
	}

	outputs, err := e.collectOutputs(itemCtx, plan)
	if err != nil {
		return itemResult{Index: item.index, Key: item.key, Status: core.NodeFailed.String(), Error: err.Error()}
	}
	return itemResult{
		Index:   item.index,
		Key:     item.key,
		Status:  core.NodeSucceeded.String(),
		Outputs: outputs,
	}
}

func cloneSteps(steps []core.Step) []core.Step {
	cloned := make([]core.Step, len(steps))
	for i, step := range steps {
		cloned[i] = step
		if step.ExecutorConfig.Config != nil {
			cloned[i].ExecutorConfig.Config = make(map[string]any, len(step.ExecutorConfig.Config))
			maps.Copy(cloned[i].ExecutorConfig.Config, step.ExecutorConfig.Config)
		}
		cloned[i].Depends = append([]string(nil), step.Depends...)
		cloned[i].Env = append([]string(nil), step.Env...)
		cloned[i].Commands = append([]core.CommandEntry(nil), step.Commands...)
		cloned[i].Outputs = append([]core.StepOutputDeclaration(nil), step.Outputs...)
	}
	return cloned
}

func (e *foreachExecutor) collectOutputs(ctx context.Context, plan *runtime.Plan) (map[string]string, error) {
	collect := e.step.Foreach.Collect
	outputs := make(map[string]string, len(collect))
	if len(collect) == 0 {
		return outputs, nil
	}

	env := runtime.NewPlanEnv(ctx, core.Step{}, plan)
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
		if result.Status == core.NodeSucceeded.String() {
			output.Summary.Succeeded++
			if result.Outputs == nil {
				output.Outputs = append(output.Outputs, map[string]string{})
			} else {
				output.Outputs = append(output.Outputs, result.Outputs)
			}
			continue
		}
		output.Summary.Failed++
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

func bodyLogDir(ctx context.Context) string {
	env := runtime.GetEnv(ctx)
	if env.Scope != nil {
		if stdout, ok := env.Scope.Get(coreexec.EnvKeyDAGRunStepStdoutFile); ok && stdout != "" {
			return filepath.Join(filepath.Dir(stdout), "foreach")
		}
	}
	rCtx := runtime.GetDAGContext(ctx)
	if rCtx.DAGRunLogDir != "" {
		return filepath.Join(rCtx.DAGRunLogDir, "foreach")
	}
	return filepath.Join(os.TempDir(), "dagu-foreach")
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
	executor.RegisterExecutor(core.ExecutorTypeForeach, newExecutor, nil, core.ExecutorCapabilities{})
}
