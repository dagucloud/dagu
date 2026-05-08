// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/llm"
	"github.com/google/uuid"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "delegate",
		Label:          "Delegate",
		Description:    "Spawn sub-agents for parallel tasks",
		DefaultEnabled: true,
		Factory:        func(_ ToolConfig) *AgentTool { return NewDelegateTool() },
	})
}

const (
	// delegateToolName is the name of the delegate tool.
	delegateToolName = "delegate"

	// maxConcurrentDelegates is the maximum number of sub-agents that can run in parallel.
	maxConcurrentDelegates = 8

	// taskSummaryTruncateLen is the max length for task text in result summaries.
	taskSummaryTruncateLen = 60

	// taskLogTruncateLen is the max length for task text in log context.
	taskLogTruncateLen = 100
)

// delegateTask is a single sub-task within a batched delegate call.
type delegateTask struct {
	Task string `json:"task"`
}

// delegateInput is the parsed input for the delegate tool.
type delegateInput struct {
	Tasks []delegateTask `json:"tasks"`
}

// singleDelegateResult holds the output of one sub-agent execution.
type singleDelegateResult struct {
	DelegateID string
	Content    string
	IsError    bool
	Cost       float64
}

// NewDelegateTool creates a delegate tool for spawning sub-agent loops.
func NewDelegateTool() *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name: delegateToolName,
				Description: fmt.Sprintf(
					"Spawn sub-agents for focused sub-tasks. Each sub-agent works independently "+
						"with the same tools and returns a summary. All tasks run in parallel. "+
						"Maximum %d tasks per call.",
					maxConcurrentDelegates,
				),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tasks": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"task": map[string]any{
										"type":        "string",
										"description": "Description of the sub-task for the sub-agent to complete",
									},
								},
								"required": []string{"task"},
							},
							"maxItems":    maxConcurrentDelegates,
							"description": "List of sub-tasks to delegate to sub-agents in parallel",
						},
					},
					"required": []string{"tasks"},
				},
			},
		},
		Run: delegateRun,
	}
}

// delegateRun executes the delegate tool by spawning sub-agent loops in parallel.
func delegateRun(ctx ToolContext, input json.RawMessage) ToolOut {
	if ctx.Delegate == nil {
		return toolError("Delegate capability is not available in this context")
	}

	var args delegateInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Invalid input: %v", err)
	}

	if len(args.Tasks) == 0 {
		return toolError("At least one task is required")
	}

	for _, t := range args.Tasks {
		if strings.TrimSpace(t.Task) == "" {
			return toolError("Task description cannot be empty")
		}
	}

	// Cap at maxConcurrentDelegates.
	tasks := args.Tasks
	truncated := 0
	if len(tasks) > maxConcurrentDelegates {
		truncated = len(tasks) - maxConcurrentDelegates
		tasks = tasks[:maxConcurrentDelegates]
		slog.Warn("Delegate tasks truncated", "requested", len(args.Tasks), "max", maxConcurrentDelegates)
	}

	// Run all tasks in parallel.
	results := make([]singleDelegateResult, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					results[i] = singleDelegateResult{
						Content: fmt.Sprintf("Sub-agent panicked: %v", r),
						IsError: true,
					}
				}
			}()
			results[i] = runSingleDelegate(ctx, task)
		})
	}
	wg.Wait()

	// Aggregate results.
	var delegateIDs []string
	var summaries []string
	allFailed := true

	for i, r := range results {
		delegateIDs = append(delegateIDs, r.DelegateID)
		prefix := fmt.Sprintf("[%d] %s", i+1, truncate(tasks[i].Task, taskSummaryTruncateLen))
		if r.IsError {
			summaries = append(summaries, fmt.Sprintf("%s: ERROR: %s", prefix, r.Content))
		} else {
			allFailed = false
			summaries = append(summaries, fmt.Sprintf("%s: %s", prefix, r.Content))
		}
	}

	if truncated > 0 {
		summaries = append(summaries, fmt.Sprintf("(%d additional tasks truncated — max %d per call)", truncated, maxConcurrentDelegates))
	}

	return ToolOut{
		Content:     strings.Join(summaries, "\n\n"),
		IsError:     allFailed,
		DelegateIDs: delegateIDs,
	}
}

// runSingleDelegate executes one sub-agent for a single task.
func runSingleDelegate(ctx ToolContext, task delegateTask) singleDelegateResult {
	dc := ctx.Delegate
	delegateID := uuid.New().String()

	logger := dc.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("delegate_id", delegateID, "task", truncate(task.Task, taskLogTruncateLen))

	// Persist sub-session in the store.
	if dc.SessionStore != nil {
		now := time.Now()
		subSession := &Session{
			ID:              delegateID,
			UserID:          dc.User.UserID,
			ParentSessionID: dc.ParentID,
			DelegateTask:    task.Task,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := dc.SessionStore.CreateSession(ctx.Context, subSession); err != nil {
			return singleDelegateResult{
				DelegateID: delegateID,
				Content:    fmt.Sprintf("Failed to create sub-session: %v", err),
				IsError:    true,
			}
		}
	}

	// Build onMessage callback for persistence.
	var onMessage func(msgCtx context.Context, msg Message) error
	if dc.SessionStore != nil {
		onMessage = func(msgCtx context.Context, msg Message) error {
			return dc.SessionStore.AddMessage(msgCtx, delegateID, &msg)
		}
	}

	// Create a sub-SessionManager for SSE streaming of the delegate's messages.
	subMgr := NewSessionManager(SessionManagerConfig{
		ID:              delegateID,
		User:            dc.User,
		Logger:          logger,
		WorkingDir:      ctx.WorkingDir,
		OnMessage:       onMessage,
		ParentSessionID: dc.ParentID,
		DelegateTask:    task.Task,
	})

	// Register sub-session for SSE streaming.
	if dc.Registry != nil {
		dc.Registry.RegisterSubSession(delegateID, subMgr)
	}

	// Forward delegate messages to parent SSE so the frontend doesn't need
	// separate EventSource connections per delegate (browser limits ~6).
	forwardToParent := func(msg Message) {
		if dc.Registry != nil {
			dc.Registry.NotifyParent(StreamResponse{
				DelegateMessages: &DelegateMessages{
					DelegateID: delegateID,
					Messages:   []Message{msg},
				},
			})
		}
	}

	userContent := task.Task

	// Record the task as the initial user message and forward to parent.
	// ID must be set before calling RecordExternalMessage because it takes
	// Message by value — the caller's copy keeps the assigned ID so
	// forwardToParent sends a message with a real ID (not "").
	userMsg := Message{
		ID:        uuid.New().String(),
		Type:      MessageTypeUser,
		Content:   userContent,
		CreatedAt: time.Now(),
	}
	if _, err := subMgr.RecordExternalMessage(ctx.Context, userMsg); err != nil {
		logger.Warn("Failed to record initial delegate message", "error", err)
	}
	forwardToParent(userMsg)

	// Set working state before notifying parent so the SSE snapshot has working=true.
	subMgr.SetWorking(true)

	// Notify parent that delegate started.
	if dc.Registry != nil {
		dc.Registry.ParentSessionManager().SetDelegateStarted(delegateID, task.Task)
		dc.Registry.NotifyParent(StreamResponse{
			DelegateEvent: &DelegateEvent{
				Type:       DelegateEventStarted,
				DelegateID: delegateID,
				Task:       task.Task,
			},
		})
	}

	// Filter out tools unavailable to sub-agents:
	// - delegate: prevents recursion
	// - ask_user: requires interactive user prompt channel
	// - navigate: requires UI action channel
	childTools := filterOutTool(dc.Tools, delegateToolName)
	childTools = filterOutTool(childTools, "ask_user")
	childTools = filterOutTool(childTools, "navigate")
	childTools = filterOutTool(childTools, "remote_agent")
	childTools = filterOutTool(childTools, "list_contexts")

	// Create a cancellable context for the child loop.
	childCtx, cancelChild := context.WithCancel(ctx.Context)
	defer cancelChild()

	// Track last assistant content and errors for result extraction.
	var lastAssistantContent string
	var lastError string
	recordMessage := func(msgCtx context.Context, msg Message) {
		if msg.Type == MessageTypeAssistant && msg.Content != "" {
			lastAssistantContent = msg.Content
		}
		if msg.Type == MessageTypeError {
			lastError = msg.Content
		}
		// Assign ID before RecordExternalMessage (which takes by value) so
		// that forwardToParent sends a message with a real ID. Without this,
		// every forwarded message has ID "" and the frontend dedup logic
		// overwrites index 0, making only the last message visible.
		if msg.ID == "" {
			msg.ID = uuid.New().String()
		}
		msg.SessionID = delegateID
		if _, err := subMgr.RecordExternalMessage(msgCtx, msg); err != nil {
			logger.Warn("Failed to record delegate message", "error", err)
		}
		forwardToParent(msg)
	}

	loop := NewLoop(LoopConfig{
		Provider:      dc.Provider,
		Model:         dc.Model,
		Tools:         childTools,
		RecordMessage: recordMessage,
		Logger:        logger,
		SystemPrompt:  dc.SystemPrompt,
		WorkingDir:    ctx.WorkingDir,
		SessionID:     delegateID,
		Hooks:         dc.Hooks,
		SafeMode:      ctx.SafeMode,
		User:          dc.User,
		SessionStore:  dc.SessionStore,
		OnWorking: func(working bool) {
			subMgr.SetWorking(working)
			if !working {
				cancelChild()
			}
		},
	})

	// Queue the task as a user message.
	loop.QueueUserMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: userContent,
	})

	logger.Info("Starting sub-agent")

	// Run the child loop synchronously. It returns context.Canceled when we cancel it.
	err := loop.Go(childCtx)

	// Roll up sub-agent cost to parent session.
	subCost := subMgr.GetTotalCost()
	if dc.Registry != nil {
		dc.Registry.AddCost(subCost)

		// Notify parent that delegate completed.
		dc.Registry.ParentSessionManager().SetDelegateCompleted(delegateID, subCost)
		dc.Registry.NotifyParent(StreamResponse{
			DelegateEvent: &DelegateEvent{
				Type:       DelegateEventCompleted,
				DelegateID: delegateID,
				Task:       task.Task,
				Cost:       subCost,
			},
		})

		// Deregister sub-session from SSE registry to prevent memory leaks.
		dc.Registry.DeregisterSubSession(delegateID)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Sub-agent failed", "error", err)
		return singleDelegateResult{
			DelegateID: delegateID,
			Content:    fmt.Sprintf("Sub-agent failed: %v", err),
			IsError:    true,
			Cost:       subCost,
		}
	}

	logger.Info("Sub-agent completed")

	// Check if the child loop captured an error (e.g., provider failure).
	if lastAssistantContent == "" && lastError != "" {
		return singleDelegateResult{
			DelegateID: delegateID,
			Content:    fmt.Sprintf("Sub-agent failed: %s", lastError),
			IsError:    true,
			Cost:       subCost,
		}
	}

	return singleDelegateResult{
		DelegateID: delegateID,
		Content:    cmp.Or(lastAssistantContent, "Sub-agent completed but produced no output."),
		Cost:       subCost,
	}
}

// filterOutTool returns a copy of the tools slice with the named tool removed.
func filterOutTool(tools []*AgentTool, name string) []*AgentTool {
	filtered := make([]*AgentTool, 0, len(tools))
	for _, t := range tools {
		if t.Function.Name != name {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// truncate shortens a string to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
