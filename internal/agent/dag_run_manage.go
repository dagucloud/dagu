// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
)

const dagRunManageToolName = "dag_run_manage"

func init() {
	RegisterTool(ToolRegistration{
		Name:           dagRunManageToolName,
		Label:          "DAG Run Manage",
		Description:    "Inspect DAG run history, status, logs, and LLM step messages",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			return NewDAGRunManageTool(cfg.DAGRunStore, cfg.DAGRunWatcher)
		},
	})
}

type dagRunManageInput struct {
	Action      string   `json:"action"`
	DAGName     string   `json:"dagName,omitempty"`
	DAGRunID    string   `json:"dagRunId,omitempty"`
	SubDAGRunID string   `json:"subDAGRunId,omitempty"`
	WatchID     string   `json:"watchId,omitempty"`
	StepName    string   `json:"stepName,omitempty"`
	Stream      string   `json:"stream,omitempty"`
	Status      []string `json:"status,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	NotifyOn    []string `json:"notifyOn,omitempty"`
	Last        string   `json:"last,omitempty"`
	From        string   `json:"from,omitempty"`
	To          string   `json:"to,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Cursor      string   `json:"cursor,omitempty"`
	Head        int      `json:"head,omitempty"`
	Tail        int      `json:"tail,omitempty"`
	Offset      int      `json:"offset,omitempty"`
	Encoding    string   `json:"encoding,omitempty"`
	// DiagnoseOnFailure defaults to true for watch notifications.
	DiagnoseOnFailure *bool `json:"diagnoseOnFailure,omitempty"`
}

type dagRunManageLogRef struct {
	Action      string `json:"action"`
	DAGName     string `json:"dagName"`
	DAGRunID    string `json:"dagRunId"`
	SubDAGRunID string `json:"subDAGRunId,omitempty"`
	StepName    string `json:"stepName,omitempty"`
	Stream      string `json:"stream"`
}

type dagRunManageLogOutput struct {
	Action      string `json:"action"`
	DAGName     string `json:"dagName"`
	DAGRunID    string `json:"dagRunId"`
	SubDAGRunID string `json:"subDAGRunId,omitempty"`
	StepName    string `json:"stepName,omitempty"`
	Stream      string `json:"stream"`
	Path        string `json:"path,omitempty"`
	Content     string `json:"content"`
	LineCount   int    `json:"lineCount"`
	TotalLines  int    `json:"totalLines"`
	HasMore     bool   `json:"hasMore"`
	IsEstimate  bool   `json:"isEstimate"`
}

// NewDAGRunManageTool creates an agent tool for DAG run inspection and watches.
func NewDAGRunManageTool(store exec.DAGRunStore, watchers ...DAGRunWatcher) *AgentTool {
	var watcher DAGRunWatcher
	if len(watchers) > 0 {
		watcher = watchers[0]
	}
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        dagRunManageToolName,
				Description: "Inspect DAG run history, detailed run state, scheduler/step logs, LLM messages, and session-local run watches. Use list to find runs, get to obtain step log refs, read_log/read_messages to drill into a step, diagnose to collect likely failure context, and watch after enqueueing instead of polling.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"list", "get", "read_log", "read_messages", "diagnose", "watch", "watch_status", "cancel_watch"},
							"description": "Operation to perform.",
						},
						"dagName":     map[string]any{"type": "string", "description": "DAG name. Required for get/read_log/read_messages/diagnose/watch; optional exact filter for list."},
						"dagRunId":    map[string]any{"type": "string", "description": "DAG run ID. Required for get/read_log/read_messages/diagnose/watch."},
						"subDAGRunId": map[string]any{"type": "string", "description": "Optional sub-DAG run ID under dagName/dagRunId."},
						"watchId":     map[string]any{"type": "string", "description": "Watch ID returned by watch. Use for watch_status or cancel_watch."},
						"stepName":    map[string]any{"type": "string", "description": "Step name for step logs or LLM messages."},
						"stream":      map[string]any{"type": "string", "enum": []string{"scheduler", "stdout", "stderr"}, "description": "Log stream to read. Use scheduler for run log; stdout/stderr for step logs."},
						"status":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional status filter for list, e.g. failed, running, succeeded."},
						"labels":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional labels filter for list."},
						"notifyOn":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional watch notification filter. Defaults to terminal; also supports failure or success."},
						"diagnoseOnFailure": map[string]any{
							"type":        "boolean",
							"description": "For watch notifications, include compact failed-step context when the run ends unsuccessfully. Defaults to true.",
						},
						"last":     map[string]any{"type": "string", "description": "Optional relative list window, e.g. 30m, 24h, 7d, 2w."},
						"from":     map[string]any{"type": "string", "description": "Optional RFC3339 or YYYY-MM-DD list start time."},
						"to":       map[string]any{"type": "string", "description": "Optional RFC3339 or YYYY-MM-DD list end time."},
						"limit":    map[string]any{"type": "integer", "description": "Maximum items for list (default 20, max 100)."},
						"cursor":   map[string]any{"type": "string", "description": "Opaque cursor returned by list."},
						"head":     map[string]any{"type": "integer", "description": "Read first N log lines."},
						"tail":     map[string]any{"type": "integer", "description": "Read last N log lines (default 500 for read_log/diagnose)."},
						"offset":   map[string]any{"type": "integer", "description": "1-based log line offset."},
						"encoding": map[string]any{"type": "string", "description": "Optional log file character encoding."},
					},
					"required": []string{"action"},
				},
			},
		},
		Run: func(ctx ToolContext, input json.RawMessage) ToolOut {
			return dagRunManageRun(ctx, input, store, watcher)
		},
		Audit: &AuditInfo{
			Action:          dagRunManageToolName,
			DetailExtractor: ExtractFields("action", "dagName", "dagRunId", "subDAGRunId", "watchId", "stepName", "stream"),
		},
	}
}

func dagRunManageRun(toolCtx ToolContext, input json.RawMessage, store exec.DAGRunStore, watcher DAGRunWatcher) ToolOut {
	var args dagRunManageInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	if toolCtx.Context == nil {
		toolCtx.Context = context.Background()
	}

	switch strings.TrimSpace(args.Action) {
	case "list":
		if store == nil {
			return toolError("%s is unavailable: dag-run store is not configured", dagRunManageToolName)
		}
		return dagRunManageList(toolCtx.Context, store, args)
	case "get":
		if store == nil {
			return toolError("%s is unavailable: dag-run store is not configured", dagRunManageToolName)
		}
		return dagRunManageGet(toolCtx.Context, store, args)
	case "read_log":
		if store == nil {
			return toolError("%s is unavailable: dag-run store is not configured", dagRunManageToolName)
		}
		return dagRunManageReadLog(toolCtx.Context, store, args)
	case "read_messages":
		if store == nil {
			return toolError("%s is unavailable: dag-run store is not configured", dagRunManageToolName)
		}
		return dagRunManageReadMessages(toolCtx.Context, store, args)
	case "diagnose":
		if store == nil {
			return toolError("%s is unavailable: dag-run store is not configured", dagRunManageToolName)
		}
		return dagRunManageDiagnose(toolCtx.Context, store, args)
	case "watch":
		return dagRunManageWatch(toolCtx, watcher, args)
	case "watch_status":
		return dagRunManageWatchStatus(toolCtx, watcher, args)
	case "cancel_watch":
		return dagRunManageCancelWatch(toolCtx, watcher, args)
	default:
		return toolError("Unknown action: %s. Use list, get, read_log, read_messages, diagnose, watch, watch_status, or cancel_watch.", args.Action)
	}
}

func dagRunManageWatch(toolCtx ToolContext, watcher DAGRunWatcher, args dagRunManageInput) ToolOut {
	if watcher == nil {
		return toolError("%s watch is unavailable: dag-run watcher is not configured", dagRunManageToolName)
	}
	diagnoseOnFailure := true
	if args.DiagnoseOnFailure != nil {
		diagnoseOnFailure = *args.DiagnoseOnFailure
	}
	info, err := watcher.Watch(toolCtx.Context, DAGRunWatchRequest{
		SessionID:         toolCtx.SessionID,
		User:              toolCtx.User,
		DAGName:           args.DAGName,
		DAGRunID:          args.DAGRunID,
		SubDAGRunID:       args.SubDAGRunID,
		NotifyOn:          args.NotifyOn,
		DiagnoseOnFailure: diagnoseOnFailure,
	})
	if err != nil {
		return toolError("Failed to watch DAG run: %v", err)
	}
	return dagManageJSON(map[string]any{
		"action":  "watch",
		"watch":   info,
		"watchId": info.WatchID,
		"state":   info.State,
		"status":  info.Status,
	})
}

func dagRunManageWatchStatus(toolCtx ToolContext, watcher DAGRunWatcher, args dagRunManageInput) ToolOut {
	if watcher == nil {
		return toolError("%s watch is unavailable: dag-run watcher is not configured", dagRunManageToolName)
	}
	info, err := watcher.Status(toolCtx.Context, DAGRunWatchStatusRequest{
		SessionID:   toolCtx.SessionID,
		WatchID:     args.WatchID,
		DAGName:     args.DAGName,
		DAGRunID:    args.DAGRunID,
		SubDAGRunID: args.SubDAGRunID,
	})
	if err != nil {
		return toolError("Failed to read DAG run watch: %v", err)
	}
	return dagManageJSON(map[string]any{
		"action":  "watch_status",
		"watch":   info,
		"watchId": info.WatchID,
		"state":   info.State,
		"status":  info.Status,
	})
}

func dagRunManageCancelWatch(toolCtx ToolContext, watcher DAGRunWatcher, args dagRunManageInput) ToolOut {
	if watcher == nil {
		return toolError("%s watch is unavailable: dag-run watcher is not configured", dagRunManageToolName)
	}
	info, err := watcher.Cancel(toolCtx.Context, DAGRunWatchCancelRequest{
		SessionID:   toolCtx.SessionID,
		WatchID:     args.WatchID,
		DAGName:     args.DAGName,
		DAGRunID:    args.DAGRunID,
		SubDAGRunID: args.SubDAGRunID,
	})
	if err != nil {
		return toolError("Failed to cancel DAG run watch: %v", err)
	}
	return dagManageJSON(map[string]any{
		"action":  "cancel_watch",
		"watch":   info,
		"watchId": info.WatchID,
		"state":   info.State,
		"status":  info.Status,
	})
}

func dagRunManageList(ctx context.Context, store exec.DAGRunStore, args dagRunManageInput) ToolOut {
	limit := clampAgentLimit(args.Limit, 20, 100)
	opts := []exec.ListDAGRunStatusesOption{
		exec.WithLimit(limit),
		exec.WithAllHistory(),
	}
	if args.DAGName != "" {
		opts = append(opts, exec.WithExactName(args.DAGName))
	}
	if args.DAGRunID != "" {
		opts = append(opts, exec.WithDAGRunID(args.DAGRunID))
	}
	if args.Cursor != "" {
		opts = append(opts, exec.WithCursor(args.Cursor))
	}
	if len(args.Labels) > 0 {
		opts = append(opts, exec.WithLabels(args.Labels))
	}
	if len(args.Status) > 0 {
		statuses, err := parseDAGRunStatuses(args.Status)
		if err != nil {
			return toolError("%v", err)
		}
		opts = append(opts, exec.WithStatuses(statuses))
	}
	if args.Last != "" && (args.From != "" || args.To != "") {
		return toolError("cannot use --last with --from or --to (conflicting time range specifications)")
	}
	if args.Last != "" {
		from, err := parseDAGManageRelativeTime(args.Last, time.Now())
		if err != nil {
			return toolError("%v", err)
		}
		opts = append(opts, exec.WithFrom(exec.NewUTC(from)))
	}
	if args.From != "" {
		from, err := parseDAGManageAbsoluteTime(args.From, false)
		if err != nil {
			return toolError("invalid from time: %v", err)
		}
		opts = append(opts, exec.WithFrom(exec.NewUTC(from)))
	}
	if args.To != "" {
		to, err := parseDAGManageAbsoluteTime(args.To, true)
		if err != nil {
			return toolError("invalid to time: %v", err)
		}
		opts = append(opts, exec.WithTo(exec.NewUTC(to)))
	}

	page, err := store.ListStatusesPage(ctx, opts...)
	if err != nil {
		return toolError("Failed to list DAG runs: %v", err)
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, status := range page.Items {
		if status == nil {
			continue
		}
		items = append(items, dagRunManageStatusPayload(status, "", false))
	}
	return dagManageJSON(map[string]any{
		"action":     "list",
		"items":      items,
		"limit":      limit,
		"cursor":     args.Cursor,
		"nextCursor": page.NextCursor,
		"hasMore":    page.NextCursor != "",
	})
}

func dagRunManageGet(ctx context.Context, store exec.DAGRunStore, args dagRunManageInput) ToolOut {
	status, _, out, ok := dagRunManageReadStatusForTool(ctx, store, args)
	if !ok {
		return out
	}
	return dagManageJSON(map[string]any{
		"action": "get",
		"run":    dagRunManageStatusPayload(status, args.SubDAGRunID, true),
	})
}

func dagRunManageReadLog(ctx context.Context, store exec.DAGRunStore, args dagRunManageInput) ToolOut {
	status, _, out, ok := dagRunManageReadStatusForTool(ctx, store, args)
	if !ok {
		return out
	}
	if args.StepName != "" && strings.TrimSpace(args.Stream) == "" {
		return toolError("stream is required when stepName is set; use stdout or stderr")
	}
	stream := normalizeDAGRunLogStream(args.Stream)
	if stream == "" {
		return toolError("stream is required and must be one of: scheduler, stdout, stderr")
	}
	if args.StepName != "" && stream == "scheduler" {
		return toolError("scheduler logs do not use stepName; omit stepName or use stdout/stderr")
	}
	logPath, err := selectDAGRunManageLogPath(status, args.StepName, stream)
	if err != nil {
		return toolError("%v", err)
	}
	read, err := readDAGManageLogFile(logPath, args)
	if err != nil {
		return toolError("Failed to read %s log: %v", stream, err)
	}
	read.Action = "read_log"
	read.DAGName = args.DAGName
	read.DAGRunID = args.DAGRunID
	read.SubDAGRunID = args.SubDAGRunID
	read.StepName = args.StepName
	read.Stream = stream
	read.Path = logPath
	return dagManageJSON(read)
}

func dagRunManageReadMessages(ctx context.Context, store exec.DAGRunStore, args dagRunManageInput) ToolOut {
	status, attempt, out, ok := dagRunManageReadStatusForTool(ctx, store, args)
	if !ok {
		return out
	}
	if strings.TrimSpace(args.StepName) == "" {
		return toolError("stepName is required for read_messages")
	}
	messages, err := dagRunManageStepMessages(ctx, status, attempt, args.StepName)
	if err != nil {
		return toolError("Failed to read step messages: %v", err)
	}
	return dagManageJSON(map[string]any{
		"action":      "read_messages",
		"dagName":     args.DAGName,
		"dagRunId":    args.DAGRunID,
		"subDAGRunId": args.SubDAGRunID,
		"stepName":    args.StepName,
		"messages":    messages,
		"count":       len(messages),
	})
}

func dagRunManageDiagnose(ctx context.Context, store exec.DAGRunStore, args dagRunManageInput) ToolOut {
	status, attempt, out, ok := dagRunManageReadStatusForTool(ctx, store, args)
	if !ok {
		return out
	}
	var primary *exec.Node
	if args.StepName != "" {
		node, err := status.NodeByName(args.StepName)
		if err != nil {
			return toolError("%v", err)
		}
		primary = node
	} else {
		primary = firstFailedDAGRunNode(status)
	}

	logArgs := args
	if logArgs.Tail == 0 && logArgs.Head == 0 && logArgs.Offset == 0 && logArgs.Limit == 0 {
		logArgs.Tail = 500
	}
	logs := map[string]any{}
	if status.Log != "" {
		logs["scheduler"] = readDAGManageLogForDiagnose(status.Log, logArgs)
	}
	var messages []exec.LLMMessage
	if primary != nil {
		if primary.Stdout != "" {
			logs["stdout"] = readDAGManageLogForDiagnose(primary.Stdout, logArgs)
		}
		if primary.Stderr != "" {
			logs["stderr"] = readDAGManageLogForDiagnose(primary.Stderr, logArgs)
		}
		messages, _ = dagRunManageStepMessages(ctx, status, attempt, primary.Step.Name)
	}

	primaryName := ""
	primaryStatus := ""
	if primary != nil {
		primaryName = primary.Step.Name
		primaryStatus = primary.Status.String()
	}

	errs := make([]string, 0)
	for _, err := range status.Errors() {
		errs = append(errs, err.Error())
	}

	return dagManageJSON(map[string]any{
		"action":                  "diagnose",
		"dagName":                 status.Name,
		"dagRunId":                status.DAGRunID,
		"subDAGRunId":             args.SubDAGRunID,
		"status":                  status.Status.String(),
		"error":                   status.Error,
		"errors":                  errs,
		"primaryFailedStep":       primary,
		"primaryFailedStepName":   primaryName,
		"primaryFailedStepStatus": primaryStatus,
		"logs":                    logs,
		"messages":                messages,
		"messageCount":            len(messages),
	})
}

func dagRunManageReadStatusForTool(ctx context.Context, store exec.DAGRunStore, args dagRunManageInput) (*exec.DAGRunStatus, exec.DAGRunAttempt, ToolOut, bool) {
	if args.DAGName == "" {
		return nil, nil, toolError("dagName is required"), false
	}
	if args.DAGRunID == "" {
		return nil, nil, toolError("dagRunId is required"), false
	}
	status, attempt, err := readDAGRunStatus(ctx, store, args.DAGName, args.DAGRunID, args.SubDAGRunID)
	if err != nil {
		return nil, nil, toolError("%v", err), false
	}
	return status, attempt, ToolOut{}, true
}

func readDAGRunStatus(ctx context.Context, store exec.DAGRunStore, dagName, dagRunID, subDAGRunID string) (*exec.DAGRunStatus, exec.DAGRunAttempt, error) {
	if store == nil {
		return nil, nil, errors.New("dag-run store is not configured")
	}
	root := exec.NewDAGRunRef(dagName, dagRunID)
	var (
		attempt exec.DAGRunAttempt
		err     error
	)
	if subDAGRunID != "" {
		attempt, err = store.FindSubAttempt(ctx, root, subDAGRunID)
	} else {
		attempt, err = store.FindAttempt(ctx, root)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find DAG run: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read DAG run status: %w", err)
	}
	return status, attempt, nil
}

func dagRunManageStepMessages(ctx context.Context, status *exec.DAGRunStatus, attempt exec.DAGRunAttempt, stepName string) ([]exec.LLMMessage, error) {
	messages, readErr := attempt.ReadStepMessages(ctx, stepName)
	if readErr == nil && len(messages) > 0 {
		return messages, nil
	}
	if node, err := status.NodeByName(stepName); err == nil && len(node.ChatMessages) > 0 {
		return node.ChatMessages, nil
	}
	if readErr != nil {
		return nil, readErr
	}
	return messages, nil
}

func dagRunManageStatusPayload(status *exec.DAGRunStatus, subDAGRunID string, includeRun bool) map[string]any {
	if status == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"dagName":  status.Name,
		"dagRunId": status.DAGRunID,
		"status":   status.Status.String(),
	}
	for key, value := range map[string]string{
		"subDAGRunId":  subDAGRunID,
		"attemptId":    status.AttemptID,
		"attemptKey":   status.AttemptKey,
		"workerId":     status.WorkerID,
		"startedAt":    status.StartedAt,
		"finishedAt":   status.FinishedAt,
		"queuedAt":     status.QueuedAt,
		"scheduleTime": status.ScheduleTime,
		"error":        status.Error,
		"params":       status.Params,
	} {
		if value != "" {
			out[key] = value
		}
	}
	if len(status.ParamsList) > 0 {
		out["paramsList"] = status.ParamsList
	}
	if len(status.Labels) > 0 {
		out["labels"] = status.Labels
	}
	if status.TriggerType != core.TriggerTypeUnknown {
		out["triggerType"] = status.TriggerType.String()
	}
	if nodes := dagRunManageNodePayloads(status.Name, status.DAGRunID, subDAGRunID, status); len(nodes) > 0 {
		out["nodes"] = nodes
	}
	if refs := dagRunManageLogRefs(status.Name, status.DAGRunID, subDAGRunID, status); len(refs) > 0 {
		out["logRefs"] = refs
	}
	if refs := dagRunManageStepLogRefs(status.Name, status.DAGRunID, subDAGRunID, status); len(refs) > 0 {
		out["stepLogRefs"] = refs
	}
	if !status.Root.Zero() {
		out["root"] = status.Root
	}
	if !status.Parent.Zero() {
		out["parent"] = status.Parent
	}
	if includeRun {
		out["run"] = status
	}
	return out
}

func dagRunManageLogRefs(dagName, dagRunID, subDAGRunID string, status *exec.DAGRunStatus) map[string]dagRunManageLogRef {
	if status.Log != "" {
		return map[string]dagRunManageLogRef{
			"scheduler": newDAGRunManageLogRef(dagName, dagRunID, subDAGRunID, "", "scheduler"),
		}
	}
	return nil
}

func dagRunManageNodePayloads(dagName, dagRunID, subDAGRunID string, status *exec.DAGRunStatus) []map[string]any {
	nodes := make([]map[string]any, 0, len(status.Nodes))
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		item := map[string]any{
			"stepName": node.Step.Name,
			"status":   node.Status.String(),
		}
		for key, value := range map[string]string{
			"executorType": node.Step.ExecutorConfig.Type,
			"startedAt":    node.StartedAt,
			"finishedAt":   node.FinishedAt,
			"error":        node.Error,
		} {
			if value != "" {
				item[key] = value
			}
		}
		if node.RetryCount > 0 {
			item["retryCount"] = node.RetryCount
		}
		if len(node.SubRuns) > 0 {
			item["subRuns"] = node.SubRuns
		}
		if refs := dagRunManageNodeLogRefs(dagName, dagRunID, subDAGRunID, node); len(refs) > 0 {
			item["logRefs"] = refs
		}
		nodes = append(nodes, item)
	}
	return nodes
}

func dagRunManageStepLogRefs(dagName, dagRunID, subDAGRunID string, status *exec.DAGRunStatus) map[string]map[string]dagRunManageLogRef {
	refs := map[string]map[string]dagRunManageLogRef{}
	for _, node := range status.Nodes {
		if node == nil || node.Step.Name == "" {
			continue
		}
		nodeRefs := dagRunManageNodeLogRefs(dagName, dagRunID, subDAGRunID, node)
		if len(nodeRefs) > 0 {
			refs[node.Step.Name] = nodeRefs
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

func dagRunManageNodeLogRefs(dagName, dagRunID, subDAGRunID string, node *exec.Node) map[string]dagRunManageLogRef {
	refs := map[string]dagRunManageLogRef{}
	if node.Stdout != "" {
		refs["stdout"] = newDAGRunManageLogRef(dagName, dagRunID, subDAGRunID, node.Step.Name, "stdout")
	}
	if node.Stderr != "" {
		refs["stderr"] = newDAGRunManageLogRef(dagName, dagRunID, subDAGRunID, node.Step.Name, "stderr")
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

func newDAGRunManageLogRef(dagName, dagRunID, subDAGRunID, stepName, stream string) dagRunManageLogRef {
	return dagRunManageLogRef{
		Action:      "read_log",
		DAGName:     dagName,
		DAGRunID:    dagRunID,
		SubDAGRunID: subDAGRunID,
		StepName:    stepName,
		Stream:      stream,
	}
}

func selectDAGRunManageLogPath(status *exec.DAGRunStatus, stepName, stream string) (string, error) {
	if stream == "scheduler" {
		if status.Log == "" {
			return "", fmt.Errorf("scheduler log path is empty")
		}
		return status.Log, nil
	}
	if stepName == "" {
		return "", fmt.Errorf("stepName is required for %s logs", stream)
	}
	node, err := status.NodeByName(stepName)
	if err != nil {
		return "", err
	}
	switch stream {
	case "stdout":
		if node.Stdout == "" {
			return "", fmt.Errorf("stdout log path is empty for step %s", stepName)
		}
		return node.Stdout, nil
	case "stderr":
		if node.Stderr == "" {
			return "", fmt.Errorf("stderr log path is empty for step %s", stepName)
		}
		return node.Stderr, nil
	default:
		return "", fmt.Errorf("unknown log stream: %s", stream)
	}
}

func readDAGManageLogFile(path string, args dagRunManageInput) (dagRunManageLogOutput, error) {
	options := buildDAGManageLogOptions(args)
	content, lineCount, totalLines, hasMore, isEstimate, err := fileutil.ReadLogContent(path, options)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dagRunManageLogOutput{}, fmt.Errorf("log file not found: %s", path)
		}
		return dagRunManageLogOutput{}, err
	}
	return dagRunManageLogOutput{
		Content:    content,
		LineCount:  lineCount,
		TotalLines: totalLines,
		HasMore:    hasMore,
		IsEstimate: isEstimate,
	}, nil
}

func buildDAGManageLogOptions(args dagRunManageInput) fileutil.LogReadOptions {
	tail := args.Tail
	if tail == 0 && args.Head == 0 && args.Offset == 0 && args.Limit == 0 {
		tail = 500
	}
	return fileutil.LogReadOptions{
		Head:     args.Head,
		Tail:     tail,
		Offset:   args.Offset,
		Limit:    args.Limit,
		Encoding: args.Encoding,
	}
}

func readDAGManageLogForDiagnose(path string, args dagRunManageInput) map[string]any {
	read, err := readDAGManageLogFile(path, args)
	if err != nil {
		return map[string]any{
			"path":  path,
			"error": err.Error(),
		}
	}
	return map[string]any{
		"path":       path,
		"content":    read.Content,
		"lineCount":  read.LineCount,
		"totalLines": read.TotalLines,
		"hasMore":    read.HasMore,
		"isEstimate": read.IsEstimate,
	}
}

func firstFailedDAGRunNode(status *exec.DAGRunStatus) *exec.Node {
	if status == nil {
		return nil
	}
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case core.NodeFailed, core.NodeAborted, core.NodeRejected:
			return node
		}
	}
	return nil
}

func normalizeDAGRunLogStream(stream string) string {
	switch strings.ToLower(strings.TrimSpace(stream)) {
	case "", "scheduler":
		return "scheduler"
	case "stdout", "out":
		return "stdout"
	case "stderr", "err":
		return "stderr"
	default:
		return ""
	}
}

func parseDAGRunStatuses(values []string) ([]core.Status, error) {
	statuses := make([]core.Status, 0, len(values))
	for _, value := range values {
		status, ok := parseDAGRunStatus(value)
		if !ok {
			return nil, fmt.Errorf("unknown status %q", value)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func parseDAGRunStatus(value string) (core.Status, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "not_started", "notstarted":
		return core.NotStarted, true
	case "running":
		return core.Running, true
	case "failed", "failure", "error":
		return core.Failed, true
	case "aborted", "cancelled", "canceled":
		return core.Aborted, true
	case "succeeded", "success":
		return core.Succeeded, true
	case "queued":
		return core.Queued, true
	case "partially_succeeded", "partial_success":
		return core.PartiallySucceeded, true
	case "waiting":
		return core.Waiting, true
	case "rejected":
		return core.Rejected, true
	default:
		return core.NotStarted, false
	}
}

func parseDAGManageRelativeTime(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return time.Time{}, fmt.Errorf("relative time is empty")
	}
	unit := value[len(value)-1]
	rawNumber := strings.TrimSpace(value[:len(value)-1])
	n, err := strconv.Atoi(rawNumber)
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid relative time %q", value)
	}
	var d time.Duration
	switch unit {
	case 'm':
		d = time.Duration(n) * time.Minute
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("invalid relative time unit %q in %q", string(unit), value)
	}
	return now.Add(-d), nil
}

func parseDAGManageAbsoluteTime(value string, endOfDay bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("time is empty")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t, nil
}

func clampAgentLimit(value, defaultValue, maxValue int) int {
	if value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func dagManageJSON(v any) ToolOut {
	data, err := json.Marshal(v)
	if err != nil {
		return toolError("Failed to encode DAG management output: %v", err)
	}
	return ToolOut{Content: string(data)}
}
