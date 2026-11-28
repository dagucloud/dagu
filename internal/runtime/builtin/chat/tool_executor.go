// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// ToolCallResult bundles a tool result with sub-DAG run info for tracking.
// This enables UI drill-down into tool executions.
type ToolCallResult struct {
	Result ir.ToolResult
	SubRun ir.SubDAGRun
}

// ToolExecutor handles the execution of tool calls by running DAGs.
type ToolExecutor struct {
	registry *ToolRegistry

	// Track running executors for cleanup
	mu            sync.Mutex
	runningDAGs   map[string]*executor.SubDAGExecutor
	parentWorkDir string
}

// NewToolExecutor creates a new ToolExecutor with the given registry.
func NewToolExecutor(registry *ToolRegistry, workDir string) *ToolExecutor {
	return &ToolExecutor{
		registry:      registry,
		runningDAGs:   make(map[string]*executor.SubDAGExecutor),
		parentWorkDir: workDir,
	}
}

// ExecuteToolCalls executes multiple tool calls and returns the results.
// Tool calls are executed sequentially to maintain consistency.
// Each result includes both the tool result and sub-DAG run info for UI tracking.
func (e *ToolExecutor) ExecuteToolCalls(ctx context.Context, toolCalls []llmpkg.ToolCall) []ToolCallResult {
	results := make([]ToolCallResult, 0, len(toolCalls))

	for _, tc := range toolCalls {
		result, subRun := e.executeToolCall(ctx, tc)
		results = append(results, ToolCallResult{
			Result: result,
			SubRun: subRun,
		})
	}

	return results
}

// executeToolCall executes a single tool call and returns the result with sub-DAG run info.
func (e *ToolExecutor) executeToolCall(ctx context.Context, tc llmpkg.ToolCall) (ir.ToolResult, ir.SubDAGRun) {
	toolName := tc.Function.Name

	ctx = logger.WithValues(ctx,
		tag.Tool(toolName),
		tag.ToolCallID(tc.ID),
	)
	logger.Info(ctx, "Executing tool call")

	// Get the DAG for this tool
	dag, ok := e.registry.GetDAGByToolName(toolName)
	if !ok {
		logger.Error(ctx, "Tool not found in registry")
		return ir.ToolResult{
			ToolCallID: tc.ID,
			Name:       toolName,
			Error:      fmt.Sprintf("tool %q not found", toolName),
		}, ir.SubDAGRun{}
	}

	// Parse the arguments from JSON string
	var args map[string]any
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			logger.Error(ctx, "Failed to parse tool arguments", tag.Error(err))
			return ir.ToolResult{
				ToolCallID: tc.ID,
				Name:       toolName,
				Error:      fmt.Sprintf("failed to parse arguments: %v", err),
			}, ir.SubDAGRun{}
		}
	}

	logger.Debug(ctx, "Parsed tool arguments", slog.Any("args", args))

	// The model sets only the inputs that the tool shows it. Another name could set
	// a variable of the child run, such as PATH or LD_PRELOAD.
	if name, ok := undeclaredArg(args, e.registry.tools[toolName]); ok {
		return ir.ToolResult{
			ToolCallID: tc.ID,
			Name:       toolName,
			Error:      fmt.Sprintf("tool %q has no input %q", toolName, name),
		}, ir.SubDAGRun{}
	}

	// Build parameter string from arguments
	params := buildParamString(args)

	// Generate a unique run ID for this tool execution using the standard sub-DAG ID generation.
	// Using repeated=true ensures uniqueness even if the same tool is called multiple times
	// in a single tool calling loop. This also ensures proper UI/API integration since the
	// ID follows the deterministic pattern: Base58EncodeSHA256(parentRunID:stepName:params:random)
	runID := runtime.GenerateSubDAGRunID(ctx, params, true)

	// Create SubDAGExecutor for this tool DAG
	subDAGExec, err := executor.NewSubDAGExecutor(ctx, e.registry.dagNames[toolName])
	if err != nil {
		logger.Error(ctx, "Failed to create SubDAGExecutor", tag.Error(err))
		return ir.ToolResult{
			ToolCallID: tc.ID,
			Name:       toolName,
			Error:      fmt.Sprintf("failed to create executor: %v", err),
		}, ir.SubDAGRun{}
	}
	defer func() {
		if cleanErr := subDAGExec.Cleanup(ctx); cleanErr != nil {
			logger.Warn(ctx, "Failed to cleanup SubDAGExecutor", tag.Error(cleanErr))
		}
	}()

	// Track the running executor for potential cleanup
	e.mu.Lock()
	e.runningDAGs[runID] = subDAGExec
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.runningDAGs, runID)
		e.mu.Unlock()
	}()

	// Execute the DAG
	runParams := executor.RunParams{
		RunID:  runID,
		Params: params,
	}

	logger.Info(ctx, "Starting tool DAG execution",
		tag.SubRunID(runID),
		tag.SubDAG(dag.Name),
		slog.String("params", params),
	)

	result, err := subDAGExec.Execute(ctx, runParams, e.parentWorkDir)

	// Build SubDAGRun info for UI drill-down tracking
	subRun := ir.SubDAGRun{
		DAGRunID: runID,
		Params:   params,
		DAGName:  dag.Name, // Use DAG name for UI display
	}

	if err != nil {
		logger.Error(ctx, "Tool DAG execution failed", tag.Error(err))
		return ir.ToolResult{
			ToolCallID: tc.ID,
			Name:       toolName,
			Error:      fmt.Sprintf("execution failed: %v", err),
		}, subRun
	}

	// Format the result content from DAG outputs
	content := formatToolResult(result)

	logger.Info(ctx, "Tool execution completed successfully",
		tag.SubRunID(runID),
		slog.Int("output_length", len(content)),
	)

	return ir.ToolResult{
		ToolCallID: tc.ID,
		Name:       toolName,
		Content:    content,
	}, subRun
}

// undeclaredArg returns the first argument, in name order, that is not an input
// that the tool shows to the model.
func undeclaredArg(args map[string]any, info *toolInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	for _, name := range slices.Sorted(maps.Keys(args)) {
		if !slices.ContainsFunc(info.Params, func(p toolParam) bool { return p.Name == name }) {
			return name, true
		}
	}
	return "", false
}

// Kill terminates all running tool DAG executions.
func (e *ToolExecutor) Kill(sig os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var errs []error
	for runID, subDAG := range e.runningDAGs {
		if err := subDAG.Kill(sig); err != nil {
			errs = append(errs, fmt.Errorf("failed to kill tool DAG %s: %w", runID, err))
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// buildParamString converts tool arguments to the parameters of the child run:
// a JSON object of strings, with the keys in order. A value cannot add a pair
// or change another pair, whatever quotes or spaces it holds.
func buildParamString(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	values := make(map[string]string, len(args))
	for key, value := range args {
		values[key] = formatArgValue(value)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(values); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// formatArgValue converts an argument value to a string representation.
func formatArgValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		// JSON numbers decode as float64 and insert in decimal form.
		return cmnvalue.FormatScalar(v)
	case bool:
		return fmt.Sprintf("%t", v)
	case nil:
		return ""
	default:
		// For arrays and objects, marshal back to JSON
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

// formatToolResult converts a DAG execution result to a tool result content string.
func formatToolResult(result *ir.RunStatus) string {
	if result == nil {
		return "Tool execution completed but no result returned"
	}

	// Check if the DAG succeeded
	if !result.Status.IsSuccess() {
		return fmt.Sprintf("Tool execution failed with status: %s", result.Status.String())
	}

	// If there are outputs, return them as JSON
	if len(result.Outputs) > 0 {
		data, err := json.Marshal(result.Outputs)
		if err != nil {
			// Fallback to simple string format
			var parts []string
			for k, v := range result.Outputs {
				parts = append(parts, fmt.Sprintf("%s: %s", k, v))
			}
			return strings.Join(parts, "\n")
		}
		return string(data)
	}

	// No outputs, return a simple success message
	return "Tool execution completed successfully"
}
