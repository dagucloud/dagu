// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agent provides an AI-powered chat interface for managing DAGs.
package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/llm"
)

// MessageType identifies the type of message in a session.
type MessageType string

// PromptType identifies the type of user prompt.
type PromptType string

const (
	// PromptTypeGeneral represents a general question prompt.
	PromptTypeGeneral PromptType = "general"
	// PromptTypeCommandApproval represents a command approval prompt.
	PromptTypeCommandApproval PromptType = "command_approval"
)

const (
	// MessageTypeUser represents a message from the user.
	MessageTypeUser MessageType = "user"
	// MessageTypeAssistant represents a message from the AI assistant.
	MessageTypeAssistant MessageType = "assistant"
	// MessageTypeError represents an error message.
	MessageTypeError MessageType = "error"
	// MessageTypeUIAction represents a UI action to be performed.
	MessageTypeUIAction MessageType = "ui_action"
	// MessageTypeUserPrompt represents a question from agent to user.
	MessageTypeUserPrompt MessageType = "user_prompt"
)

// UIActionType identifies the type of UI action.
type UIActionType string

const (
	// UIActionNavigate represents a navigation action.
	UIActionNavigate UIActionType = "navigate"
)

// UIAction represents an action to be performed by the UI (e.g., navigate).
type UIAction struct {
	// Type is the action type (e.g., "navigate").
	Type UIActionType `json:"type"`
	// Path is the target path for navigation actions.
	Path string `json:"path,omitempty"`
}

// UserPromptOption represents a single option for a user prompt.
type UserPromptOption struct {
	// ID is the unique identifier for this option.
	ID string `json:"id"`
	// Label is the display text for this option.
	Label string `json:"label"`
	// Description provides additional context for this option.
	Description string `json:"description,omitempty"`
}

// UserPrompt represents a question from agent to user.
type UserPrompt struct {
	// PromptID is the unique identifier for this prompt.
	PromptID string `json:"prompt_id"`
	// Question is the question text to display.
	Question string `json:"question"`
	// Options are the predefined choices (2-4 options).
	Options []UserPromptOption `json:"options,omitempty"`
	// AllowFreeText enables an optional text input field.
	AllowFreeText bool `json:"allow_free_text"`
	// FreeTextPlaceholder is the placeholder for the text input.
	FreeTextPlaceholder string `json:"free_text_placeholder,omitempty"`
	// MultiSelect allows selecting multiple options.
	MultiSelect bool `json:"multi_select"`
	// PromptType identifies the type of prompt (general, command_approval).
	PromptType PromptType `json:"prompt_type,omitempty"`
	// Command is the shell command requiring approval (for command_approval type).
	Command string `json:"command,omitempty"`
	// WorkingDir is the working directory for the command (for command_approval type).
	WorkingDir string `json:"working_dir,omitempty"`
}

// UserPromptResponse is the user's answer to a prompt.
type UserPromptResponse struct {
	// PromptID identifies which prompt this responds to.
	PromptID string `json:"prompt_id"`
	// SelectedOptionIDs are the IDs of selected options.
	SelectedOptionIDs []string `json:"selected_option_ids,omitempty"`
	// FreeTextResponse is the user's text input.
	FreeTextResponse string `json:"free_text_response,omitempty"`
	// Cancelled indicates the user skipped the prompt.
	Cancelled bool `json:"cancelled,omitempty"`
}

// Message represents a message in a session.
type Message struct {
	// ID is the unique identifier for this message.
	ID string `json:"id"`
	// SessionID links this message to its parent session.
	SessionID string `json:"session_id"`
	// Type identifies the message type (user, assistant, error, ui_action, user_prompt).
	Type MessageType `json:"type"`
	// SequenceID orders messages within a session.
	SequenceID int64 `json:"sequence_id"`
	// Content is the text content of the message.
	Content string `json:"content,omitempty"`
	// ToolCalls contains tool calls made by the assistant.
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
	// ToolResults contains results from executed tool calls.
	ToolResults []ToolResult `json:"tool_results,omitempty"`
	// Usage contains token usage statistics.
	Usage *llm.Usage `json:"usage,omitempty"`
	// Cost is the estimated cost of this message in USD.
	Cost *float64 `json:"cost,omitempty"`
	// CreatedAt is when this message was created.
	CreatedAt time.Time `json:"created_at"`
	// LLMData contains the original LLM message for provider reconstruction.
	LLMData *llm.Message `json:"llm_data,omitempty"`
	// UIAction contains a UI action when Type is MessageTypeUIAction.
	UIAction *UIAction `json:"ui_action,omitempty"`
	// UserPrompt contains a prompt when Type is MessageTypeUserPrompt.
	UserPrompt *UserPrompt `json:"user_prompt,omitempty"`
	// DelegateIDs references the sub-session IDs for delegate tool results.
	DelegateIDs []string `json:"delegate_ids,omitempty"`
}

// ToolResult represents the result of a tool call.
type ToolResult struct {
	// ToolCallID links this result to its corresponding tool call.
	ToolCallID string `json:"tool_call_id"`
	// Content is the output from the tool execution.
	Content string `json:"content"`
	// IsError indicates whether the tool execution failed.
	IsError bool `json:"is_error,omitempty"`
}

// Session represents a chat session with metadata.
type Session struct {
	// ID is the unique identifier for this session.
	ID string `json:"id"`
	// UserID identifies the user who owns this session.
	UserID string `json:"user_id,omitempty"`
	// DAGName stores the primary DAG context for this session's memory scope.
	DAGName string `json:"dag_name,omitempty"`
	// Title is a human-readable name for the session.
	Title string `json:"title,omitempty"`
	// CreatedAt is when this session was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when this session was last modified.
	UpdatedAt time.Time `json:"updated_at"`
	// ParentSessionID links this session to its parent (non-empty = sub-session).
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// DelegateTask is the task description given to the sub-agent.
	DelegateTask string `json:"delegate_task,omitempty"`
	// Model stores the selected model configuration ID for this session.
	Model string `json:"model,omitempty"`
}

// SessionState represents the current state of a session.
type SessionState struct {
	// SessionID identifies which session this state belongs to.
	SessionID string `json:"session_id"`
	// Working indicates whether the agent is currently processing.
	Working bool `json:"working"`
	// HasPendingPrompt indicates whether the agent is waiting for user input.
	HasPendingPrompt bool `json:"has_pending_prompt"`
	// HasQueuedUserInput indicates whether a queued follow-up turn is waiting.
	HasQueuedUserInput bool `json:"has_queued_user_input,omitempty"`
	// Model is the LLM model being used for this session.
	Model string `json:"model,omitempty"`
	// TotalCost is the accumulated cost of the session in USD.
	TotalCost float64 `json:"total_cost"`
}

// DelegateEventType identifies the lifecycle phase of a delegate.
type DelegateEventType string

const (
	// DelegateEventStarted indicates a delegate sub-agent has started.
	DelegateEventStarted DelegateEventType = "started"
	// DelegateEventCompleted indicates a delegate sub-agent has completed.
	DelegateEventCompleted DelegateEventType = "completed"
)

// DelegateStatus is a typed enum for delegate lifecycle state.
type DelegateStatus string

const (
	// DelegateStatusRunning indicates a delegate sub-agent is currently running.
	DelegateStatusRunning DelegateStatus = "running"
	// DelegateStatusCompleted indicates a delegate sub-agent has completed.
	DelegateStatusCompleted DelegateStatus = "completed"
)

// DelegateSnapshot tracks a sub-agent's lifecycle on the parent session.
type DelegateSnapshot struct {
	// ID is the sub-session ID.
	ID string `json:"id"`
	// Task is the delegate task description.
	Task string `json:"task"`
	// Status is the delegate's lifecycle state.
	Status DelegateStatus `json:"status"`
	// Cost is the sub-agent's accumulated cost in USD (set on completion).
	Cost float64 `json:"cost,omitempty"`
}

// DelegateEvent notifies the parent SSE stream about delegate lifecycle changes.
type DelegateEvent struct {
	// Type is "started" or "completed".
	Type DelegateEventType `json:"type"`
	// DelegateID is the sub-session ID.
	DelegateID string `json:"delegate_id"`
	// Task is the delegate task description.
	Task string `json:"task"`
	// Cost is the sub-agent's accumulated cost in USD (set on "completed").
	Cost float64 `json:"cost,omitempty"`
}

// DelegateMessages carries delegate sub-agent messages piped through the parent SSE.
type DelegateMessages struct {
	// DelegateID is the sub-session ID.
	DelegateID string `json:"delegate_id"`
	// Messages are the delegate's messages.
	Messages []Message `json:"messages"`
}

// StreamResponse is sent over SSE to the UI.
type StreamResponse struct {
	// Messages contains new or updated messages.
	Messages []Message `json:"messages,omitempty"`
	// Session contains session metadata updates.
	Session *Session `json:"session,omitempty"`
	// SessionState contains the current processing state.
	SessionState *SessionState `json:"session_state,omitempty"`
	// DelegateEvent contains delegate lifecycle notifications.
	DelegateEvent *DelegateEvent `json:"delegate_event,omitempty"`
	// DelegateMessages contains messages from a delegate piped through the parent SSE.
	DelegateMessages *DelegateMessages `json:"delegate_messages,omitempty"`
	// Delegates contains snapshots of all delegate sub-agents for state recovery.
	Delegates []DelegateSnapshot `json:"delegates,omitempty"`
}

// DAGContext contains a DAG reference from the frontend.
type DAGContext struct {
	// DAGFile is the DAG file path or identifier.
	DAGFile string `json:"dag_file"`
	// DAGRunID identifies a specific run of the DAG.
	DAGRunID string `json:"dag_run_id,omitempty"`
}

// ChatRequest is the request body for sending a chat message.
type ChatRequest struct {
	// Message is the user's input text.
	Message string `json:"message"`
	// Model specifies which LLM model to use.
	Model string `json:"model,omitempty"`
	// DAGContexts provides DAG references for context-aware responses.
	DAGContexts []DAGContext `json:"dag_contexts,omitempty"`
	// SafeMode enables approval prompts for dangerous commands when true.
	SafeMode bool `json:"safe_mode,omitempty"`
	// SoulID overrides the default soul for this session.
	SoulID string `json:"soul_id,omitempty"`
	// SessionID is an optional client-provided session ID (UUID v4) for idempotent creation.
	SessionID string `json:"session_id,omitempty"`
}

// ResolvedDAGContext contains server-resolved information for a DAG.
type ResolvedDAGContext struct {
	// DAGFilePath is the absolute path to the DAG file.
	DAGFilePath string
	// DAGName is the human-readable name of the DAG.
	DAGName string
	// DAGRunID identifies a specific run (present when viewing a specific run).
	DAGRunID string
	// RunStatus is the execution status (running, success, or failed).
	RunStatus string
}

// NewSessionResponse is the response for creating a new session.
type NewSessionResponse struct {
	// SessionID is the ID of the newly created session.
	SessionID string `json:"session_id"`
	// Status indicates the result of the creation request.
	Status string `json:"status"`
}

// ToolOut represents the output of a tool execution.
type ToolOut struct {
	// Content is the output sent back to the LLM.
	Content string
	// IsError indicates whether the tool execution failed.
	IsError bool
	// DelegateIDs references the sub-sessions created by the delegate tool.
	DelegateIDs []string
	// AuditDetails contains extra audit details set by the tool at runtime.
	// These are merged with DetailExtractor output in the audit hook;
	// AuditDetails takes precedence on key collisions.
	AuditDetails map[string]any
}

// ToolFunc is the function signature for tool execution.
type ToolFunc func(ctx ToolContext, input json.RawMessage) ToolOut

// UIActionFunc emits UI actions during tool execution.
type UIActionFunc func(action UIAction)

// EmitUserPromptFunc emits a user prompt during tool execution.
type EmitUserPromptFunc func(prompt UserPrompt)

// WaitUserResponseFunc blocks until user responds to a prompt.
type WaitUserResponseFunc func(ctx context.Context, promptID string) (UserPromptResponse, error)

// UserIdentity groups the authenticated user's identity fields.
type UserIdentity struct {
	// UserID is the authenticated user's ID.
	UserID string
	// Username is the authenticated user's display name.
	Username string
	// IPAddress is the client's IP address.
	IPAddress string
	// Role is the authenticated user's role.
	Role auth.Role
	// WorkspaceAccess restricts the user's access to selected workspaces.
	// Nil is treated as all-workspaces for backward compatibility.
	WorkspaceAccess *auth.WorkspaceAccess
}

// SubSessionRegistry manages sub-session lifecycle for delegate tools.
// It consolidates the registration, notification, and cost tracking
// callbacks that were previously separate function fields.
type SubSessionRegistry interface {
	// RegisterSubSession registers a sub-session's SessionManager for SSE streaming.
	RegisterSubSession(id string, mgr *SessionManager)
	// DeregisterSubSession removes a completed sub-session from the SSE registry.
	DeregisterSubSession(id string)
	// NotifyParent broadcasts an event to the parent session's SSE stream.
	NotifyParent(event StreamResponse)
	// AddCost adds a cost amount to the parent session's running total.
	AddCost(cost float64)
	// ParentSessionManager returns the parent session manager for delegate tracking.
	ParentSessionManager() *SessionManager
}

// DelegateContext provides configuration for spawning sub-agent loops.
type DelegateContext struct {
	// Provider is the LLM provider for sub-agent calls.
	Provider llm.Provider
	// Model is the LLM model identifier.
	Model string
	// SystemPrompt is the system prompt for sub-agents.
	SystemPrompt string
	// Tools is the parent's tool set (delegate will be filtered out).
	Tools []*AgentTool
	// Hooks is the parent's hook configuration.
	Hooks *Hooks
	// Logger is the logger for sub-agent operations.
	Logger *slog.Logger
	// SessionStore is used for persisting sub-sessions.
	SessionStore SessionStore
	// ParentID is the parent session ID.
	ParentID string
	// User is the authenticated user's identity.
	User UserIdentity
	// Registry manages sub-session lifecycle. Nil if not available.
	Registry SubSessionRegistry
}

// ToolContext provides context for tool execution.
type ToolContext struct {
	// Context is the parent context for cancellation propagation.
	Context context.Context
	// WorkingDir is the current working directory for the tool.
	WorkingDir string
	// EmitUIAction is a callback to emit UI actions during execution.
	EmitUIAction UIActionFunc
	// EmitUserPrompt is a callback to emit user prompts during execution.
	EmitUserPrompt EmitUserPromptFunc
	// WaitUserResponse blocks until user responds to a prompt.
	WaitUserResponse WaitUserResponseFunc
	// SafeMode enables approval prompts for dangerous commands when true.
	SafeMode bool
	// Role is the authenticated role of the current user.
	// Empty means role checks should be skipped (e.g., auth-disabled compatibility).
	Role auth.Role
	// SessionID is the current agent session ID.
	SessionID string
	// User is the authenticated user for this tool call.
	User UserIdentity
	// SessionStore provides read access to persisted agent sessions.
	SessionStore SessionStore
	// Delegate provides sub-agent spawning capability. Nil when not available.
	Delegate *DelegateContext
}

// AuditInfo configures how a tool's executions appear in audit logs.
// Nil means the tool is not audited.
type AuditInfo struct {
	// Action is the audit action name (e.g. "bash_exec", "file_read").
	Action string
	// DetailExtractor extracts audit details from the tool's input JSON.
	// If nil, only the tool name is logged.
	DetailExtractor func(input json.RawMessage) map[string]any
}

// ExtractFields returns a DetailExtractor that pulls the named fields from
// the tool's JSON input. Only non-nil values are included in the result.
func ExtractFields(fields ...string) func(json.RawMessage) map[string]any {
	return func(input json.RawMessage) map[string]any {
		var raw map[string]any
		_ = json.Unmarshal(input, &raw)
		result := make(map[string]any, len(fields))
		for _, f := range fields {
			if v, ok := raw[f]; ok {
				result[f] = v
			}
		}
		return result
	}
}

// AgentTool extends llm.Tool with an execution function.
type AgentTool struct {
	llm.Tool
	// Run is the function that executes this tool.
	Run ToolFunc
	// Audit configures audit logging for this tool. Nil means not audited.
	Audit *AuditInfo
}

// EnvironmentInfo contains Dagu environment paths for the system prompt.
type EnvironmentInfo struct {
	// DAGsDir is the directory containing DAG definition files.
	DAGsDir string
	// DocsDir is the directory for documents.
	DocsDir string
	// LogDir is the directory for log files.
	LogDir string
	// DataDir is the directory for data storage.
	DataDir string
	// SessionsDir is the directory for persisted agent sessions.
	SessionsDir string
	// ConfigFile is the path to the configuration file.
	ConfigFile string
	// WorkingDir is the current working directory.
	WorkingDir string
	// BaseConfigFile is the path to the base configuration file.
	BaseConfigFile string
	// ReferencesDir is the directory containing built-in reference documents.
	ReferencesDir string
}
