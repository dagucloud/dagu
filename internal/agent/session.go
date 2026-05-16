// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
	"github.com/google/uuid"
)

const (
	queuedChatMessageSeparator = "\n\n"
	maxSessionTitleLength      = 50
)

type queuedChatMessage struct {
	displayContent string
	llmContent     string
}

// SessionManager manages a single active session.
// It links the Loop with SSE streaming and handles state management.
// Lock ordering: mu must be acquired before promptsMu when both are needed.
type SessionManager struct {
	id                    string
	user                  UserIdentity
	loop                  *Loop
	loopCancel            context.CancelFunc
	mu                    sync.Mutex
	title                 string
	createdAt             time.Time
	lastActivity          time.Time
	lastHeartbeat         time.Time
	model                 string
	messages              []Message
	queuedChatMessages    []queuedChatMessage
	flushingQueuedChat    bool
	subpub                *SubPub[StreamResponse]
	working               bool
	canceling             bool
	logger                *slog.Logger
	workingDir            string
	sequenceID            int64
	environment           EnvironmentInfo
	safeMode              bool
	hooks                 *Hooks
	onWorkingChange       func(id string, working bool)
	onMessage             func(ctx context.Context, msg Message) error
	pendingPrompts        map[string]chan UserPromptResponse
	promptTypes           map[string]PromptType // tracks each prompt's type for selective cancellation
	promptsMu             sync.Mutex
	inputCostPer1M        float64
	outputCostPer1M       float64
	thinkingEffort        llm.ThinkingEffort
	totalCost             float64
	memoryStore           MemoryStore
	docStore              DocStore
	workspaceStore        workspacepkg.Store
	dagStore              exec.DAGStore
	dagRunStore           exec.DAGRunStore
	dagRunWatcher         DAGRunWatcher
	dagName               string
	sessionStore          SessionStore
	parentSessionID       string
	delegateTask          string
	registry              SubSessionRegistry
	delegates             map[string]DelegateSnapshot // guarded by mu
	soul                  *Soul
	webSearch             *llm.WebSearchRequest
	webTools              *WebToolsConfig
	remoteContextResolver RemoteContextResolver
	dynamicSystemCtx      DynamicSystemContextFunc
	promptWaitInterval    time.Duration
}

// SessionSnapshot is a point-in-time copy of the session state.
type SessionSnapshot struct {
	Messages         []Message
	Session          Session
	Working          bool
	HasPendingPrompt bool
	HasQueuedInput   bool
	Model            string
	TotalCost        float64
	Delegates        []DelegateSnapshot
}

// SessionManagerConfig contains configuration for creating a SessionManager.
type SessionManagerConfig struct {
	ID              string
	User            UserIdentity
	Model           string
	Logger          *slog.Logger
	WorkingDir      string
	Title           string
	CreatedAt       time.Time
	LastActivity    time.Time
	OnWorkingChange func(id string, working bool)
	OnMessage       func(ctx context.Context, msg Message) error
	History         []Message
	SequenceID      int64
	Environment     EnvironmentInfo
	SafeMode        bool
	Hooks           *Hooks
	InputCostPer1M  float64
	OutputCostPer1M float64
	ThinkingEffort  llm.ThinkingEffort
	MemoryStore     MemoryStore
	DocStore        DocStore
	WorkspaceStore  workspacepkg.Store
	// DAGStore provides DAG metadata and definitions for DAG management tools.
	DAGStore exec.DAGStore
	// DAGRunStore provides DAG run history for dag_run_manage.
	DAGRunStore exec.DAGRunStore
	// DAGRunWatcher provides session-local run watches for dag_run_manage.
	DAGRunWatcher DAGRunWatcher
	DAGName       string
	SessionStore  SessionStore
	// ParentSessionID links this session to its parent (non-empty = sub-session).
	ParentSessionID string
	// DelegateTask is the task description given to the sub-agent.
	DelegateTask string
	// Registry manages sub-session lifecycle for delegate tools.
	Registry SubSessionRegistry
	// Soul is the active soul for this session (nil means use default prompt).
	Soul *Soul
	// WebSearch configures provider-native web search for this session.
	WebSearch *llm.WebSearchRequest
	// WebTools configures first-class web_search and web_extract tools.
	WebTools *WebToolsConfig
	// RemoteContextResolver provides access to remote CLI contexts for remote_agent tools.
	RemoteContextResolver RemoteContextResolver
	// DynamicSystemContext returns volatile per-request context appended to the
	// system message without being persisted in session history.
	DynamicSystemContext DynamicSystemContextFunc
	// Delegates seeds known delegate sessions when restoring from storage.
	Delegates []DelegateSnapshot
	// PromptWaitInterval overrides the heartbeat interval used while waiting
	// for a user prompt response. Zero uses loopHeartbeatInterval.
	PromptWaitInterval time.Duration
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	id := cfg.ID
	if id == "" {
		id = uuid.New().String()
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	messages := copyMessages(cfg.History)

	// Reconstruct accumulated cost from historical messages and delegate
	// snapshots so that reactivated sessions display the correct total.
	var totalCost float64
	for _, msg := range messages {
		if msg.Cost != nil {
			totalCost += *msg.Cost
		}
	}
	for _, d := range cfg.Delegates {
		totalCost += d.Cost
	}

	now := time.Now()
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	lastActivity := cfg.LastActivity
	if lastActivity.IsZero() {
		lastActivity = createdAt
	}
	promptWaitInterval := cfg.PromptWaitInterval
	if promptWaitInterval <= 0 {
		promptWaitInterval = loopHeartbeatInterval
	}

	delegates := make(map[string]DelegateSnapshot, len(cfg.Delegates))
	for _, delegateSnapshot := range cfg.Delegates {
		delegates[delegateSnapshot.ID] = delegateSnapshot
	}

	return &SessionManager{
		id:                    id,
		user:                  cfg.User,
		model:                 cfg.Model,
		title:                 cfg.Title,
		createdAt:             createdAt,
		lastActivity:          lastActivity,
		logger:                logger.With("session_id", id),
		subpub:                NewSubPub[StreamResponse](),
		messages:              messages,
		workingDir:            cfg.WorkingDir,
		onWorkingChange:       cfg.OnWorkingChange,
		onMessage:             cfg.OnMessage,
		sequenceID:            cfg.SequenceID,
		environment:           cfg.Environment,
		safeMode:              cfg.SafeMode,
		hooks:                 cfg.Hooks,
		pendingPrompts:        make(map[string]chan UserPromptResponse),
		promptTypes:           make(map[string]PromptType),
		delegates:             delegates,
		inputCostPer1M:        cfg.InputCostPer1M,
		outputCostPer1M:       cfg.OutputCostPer1M,
		thinkingEffort:        cfg.ThinkingEffort,
		totalCost:             totalCost,
		memoryStore:           cfg.MemoryStore,
		docStore:              cfg.DocStore,
		workspaceStore:        cfg.WorkspaceStore,
		dagStore:              cfg.DAGStore,
		dagRunStore:           cfg.DAGRunStore,
		dagRunWatcher:         cfg.DAGRunWatcher,
		dagName:               cfg.DAGName,
		sessionStore:          cfg.SessionStore,
		parentSessionID:       cfg.ParentSessionID,
		delegateTask:          cfg.DelegateTask,
		registry:              cfg.Registry,
		soul:                  cfg.Soul,
		webSearch:             cfg.WebSearch,
		webTools:              cfg.WebTools,
		remoteContextResolver: cfg.RemoteContextResolver,
		dynamicSystemCtx:      cfg.DynamicSystemContext,
		promptWaitInterval:    promptWaitInterval,
	}
}

// UpdateUserContext updates user metadata for an existing session and active loop.
func (sm *SessionManager) UpdateUserContext(u UserIdentity) {
	sm.mu.Lock()
	sm.user = u
	loop := sm.loop
	sm.mu.Unlock()

	if loop != nil {
		loop.SetUserContext(u)
	}
}

// SetSafeMode updates the safe mode setting for this session.
func (sm *SessionManager) SetSafeMode(enabled bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.safeMode = enabled
	// Update active loop if one exists
	if sm.loop != nil {
		sm.loop.SetSafeMode(enabled)
	}
}

// UpdateThinkingEffort updates the default reasoning effort for future turns.
func (sm *SessionManager) UpdateThinkingEffort(effort llm.ThinkingEffort) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.thinkingEffort = effort
	if sm.loop != nil {
		sm.loop.SetThinkingEffort(effort)
	}
}

// UpdateLoopProvider updates the active loop runtime when the session is idle.
func (sm *SessionManager) UpdateLoopProvider(provider llm.Provider, resolvedModel string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.loop == nil || sm.working {
		return
	}
	sm.loop.SetProviderModel(provider, resolvedModel)
}

// SetDynamicSystemContext updates volatile context for future LLM requests.
func (sm *SessionManager) SetDynamicSystemContext(fn DynamicSystemContextFunc) {
	sm.mu.Lock()
	sm.dynamicSystemCtx = fn
	loop := sm.loop
	sm.mu.Unlock()
	if loop != nil {
		loop.SetDynamicSystemContext(fn)
	}
}

// copyMessages returns a shallow copy of the messages slice.
func copyMessages(src []Message) []Message {
	if len(src) == 0 {
		return nil
	}
	return append([]Message(nil), src...)
}

// ID returns the session ID.
func (sm *SessionManager) ID() string {
	return sm.id
}

// UserID returns the user ID that owns this session.
func (sm *SessionManager) UserID() string {
	return sm.user.UserID
}

// SetWorking updates the agent working state and notifies subscribers.
// Repeated true values are broadcast as progress pulses so transports can
// refresh visible activity indicators during multi-phase turns.
func (sm *SessionManager) SetWorking(working bool) {
	id, model, totalCost, hasPendingPrompt, hasQueuedUserInput, callback, changed, shouldBroadcast := sm.updateWorkingState(working)
	if !shouldBroadcast {
		return
	}
	sm.logger.Debug("broadcasting agent working state", "working", working, "changed", changed)
	sm.subpub.Broadcast(StreamResponse{
		SessionState: &SessionState{
			SessionID:          id,
			Working:            working,
			HasPendingPrompt:   hasPendingPrompt,
			HasQueuedUserInput: hasQueuedUserInput,
			Model:              model,
			TotalCost:          totalCost,
		},
	})
	if changed && callback != nil {
		callback(id, working)
	}
}

// updateWorkingState atomically updates the working state and returns relevant data.
// Returns (id, model, totalCost, hasPendingPrompt, hasQueuedUserInput, callback, changed, shouldBroadcast)
// where changed indicates whether the boolean state changed and shouldBroadcast
// allows repeated working=true pulses to be sent while already working.
func (sm *SessionManager) updateWorkingState(working bool) (string, string, float64, bool, bool, func(string, bool), bool, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Ignore late working=true pulses from a loop that is already being
	// canceled. ensureLoop clears canceling before starting new work.
	if working && sm.canceling {
		return "", "", 0, false, false, nil, false, false
	}

	if sm.working == working {
		if working {
			sm.promptsMu.Lock()
			hasPending := len(sm.pendingPrompts) > 0
			sm.promptsMu.Unlock()
			return sm.id, sm.model, sm.totalCost, hasPending, sm.hasQueuedChatInputLocked(), sm.onWorkingChange, false, true
		}
		return "", "", 0, false, false, nil, false, false
	}

	sm.working = working

	sm.promptsMu.Lock()
	hasPending := len(sm.pendingPrompts) > 0
	sm.promptsMu.Unlock()

	return sm.id, sm.model, sm.totalCost, hasPending, sm.hasQueuedChatInputLocked(), sm.onWorkingChange, true, true
}

// IsWorking returns the current agent working state.
func (sm *SessionManager) IsWorking() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.working
}

// LastActivity returns the time of the most recent activity in this session.
func (sm *SessionManager) LastActivity() time.Time {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.lastActivity
}

// HasPendingPrompt returns true if the session has pending user prompts.
func (sm *SessionManager) HasPendingPrompt() bool {
	sm.promptsMu.Lock()
	defer sm.promptsMu.Unlock()
	return len(sm.pendingPrompts) > 0
}

// HasQueuedChatInput returns true when a merged bot follow-up is waiting to run.
func (sm *SessionManager) HasQueuedChatInput() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.hasQueuedChatInputLocked()
}

func (sm *SessionManager) hasQueuedChatInputLocked() bool {
	return sm.flushingQueuedChat || len(sm.queuedChatMessages) > 0
}

// CancelPendingPrompts sends a cancellation response to all pending general prompts,
// unblocking any goroutines waiting in WaitUserResponse. Command approval prompts
// are left pending because the user may intend to approve/reject them.
func (sm *SessionManager) CancelPendingPrompts() {
	sm.cancelPendingPrompts(false)
}

// CancelAllPendingPrompts sends a cancellation response to all pending prompts,
// including command approval prompts. This is used when the entire session is
// ending and no prompt should remain answerable.
func (sm *SessionManager) CancelAllPendingPrompts() {
	sm.cancelPendingPrompts(true)
}

func (sm *SessionManager) cancelPendingPrompts(includeApprovals bool) {
	sm.promptsMu.Lock()
	for promptID, ch := range sm.pendingPrompts {
		if !includeApprovals && sm.promptTypes[promptID] == PromptTypeCommandApproval {
			continue
		}
		select {
		case ch <- UserPromptResponse{PromptID: promptID, Cancelled: true}:
		default:
			slog.Debug("pending prompt already responded", "promptID", promptID)
		}
	}
	sm.promptsMu.Unlock()
}

// tryRouteToGeneralPrompt attempts to deliver text as a free-text response to
// a pending non-approval prompt. Returns the prompt ID and true on success.
// The lookup and send happen in a single critical section to prevent TOCTOU
// races with concurrent SubmitUserResponse calls.
func (sm *SessionManager) tryRouteToGeneralPrompt(text string) (string, bool) {
	sm.promptsMu.Lock()
	defer sm.promptsMu.Unlock()
	for id, ch := range sm.pendingPrompts {
		if sm.promptTypes[id] == PromptTypeCommandApproval {
			continue
		}
		select {
		case ch <- UserPromptResponse{PromptID: id, FreeTextResponse: text}:
			return id, true
		default:
			continue // channel full, prompt already answered
		}
	}
	return "", false
}

// RecordHeartbeat updates the heartbeat and activity timestamps.
func (sm *SessionManager) RecordHeartbeat() {
	sm.mu.Lock()
	sm.recordHeartbeatLocked(time.Now())
	sm.mu.Unlock()
}

// LastHeartbeat returns the time of the most recent heartbeat.
func (sm *SessionManager) LastHeartbeat() time.Time {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.lastHeartbeat
}

func (sm *SessionManager) recordHeartbeatLocked(now time.Time) {
	sm.lastHeartbeat = now
	sm.bumpLastActivityLocked(now)
}

func (sm *SessionManager) isCanceling() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.canceling
}

func (sm *SessionManager) bumpLastActivityLocked(now time.Time) time.Time {
	if !sm.lastActivity.IsZero() && !now.After(sm.lastActivity) {
		now = sm.lastActivity.Add(time.Nanosecond)
	}
	sm.lastActivity = now
	return now
}

// GetModel returns the model ID used by this session.
func (sm *SessionManager) GetModel() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.model
}

// SetModel updates the session's selected model configuration ID.
func (sm *SessionManager) SetModel(model string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.model = model
}

// GetMessages returns a copy of all messages in this session.
func (sm *SessionManager) GetMessages() []Message {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	msgs := make([]Message, len(sm.messages))
	copy(msgs, sm.messages)
	return msgs
}

// GetSession returns the session metadata.
func (sm *SessionManager) GetSession() Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return Session{
		ID:              sm.id,
		UserID:          sm.user.UserID,
		DAGName:         sm.dagName,
		Title:           sm.title,
		ParentSessionID: sm.parentSessionID,
		DelegateTask:    sm.delegateTask,
		Model:           sm.model,
		CreatedAt:       sm.createdAt,
		UpdatedAt:       sm.lastActivity,
	}
}

// Snapshot returns a consistent point-in-time copy of the session state.
func (sm *SessionManager) Snapshot() SessionSnapshot {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.snapshotLocked()
}

func (sm *SessionManager) snapshotLocked() SessionSnapshot {
	messages := make([]Message, len(sm.messages))
	copy(messages, sm.messages)

	sm.promptsMu.Lock()
	hasPendingPrompt := len(sm.pendingPrompts) > 0
	sm.promptsMu.Unlock()

	snapshot := SessionSnapshot{
		Messages: messages,
		Session: Session{
			ID:              sm.id,
			UserID:          sm.user.UserID,
			DAGName:         sm.dagName,
			Title:           sm.title,
			ParentSessionID: sm.parentSessionID,
			DelegateTask:    sm.delegateTask,
			Model:           sm.model,
			CreatedAt:       sm.createdAt,
			UpdatedAt:       sm.lastActivity,
		},
		Working:          sm.working,
		HasPendingPrompt: hasPendingPrompt,
		HasQueuedInput:   sm.hasQueuedChatInputLocked(),
		Model:            sm.model,
		TotalCost:        sm.totalCost,
	}
	if len(sm.delegates) > 0 {
		snapshot.Delegates = make([]DelegateSnapshot, 0, len(sm.delegates))
		for _, delegateSnapshot := range sm.delegates {
			snapshot.Delegates = append(snapshot.Delegates, delegateSnapshot)
		}
	}

	return snapshot
}

// StreamResponse converts a session snapshot to the stream payload shape.
func (s SessionSnapshot) StreamResponse() StreamResponse {
	return StreamResponse{
		Messages: s.Messages,
		Session:  &s.Session,
		SessionState: &SessionState{
			SessionID:          s.Session.ID,
			Working:            s.Working,
			HasPendingPrompt:   s.HasPendingPrompt,
			HasQueuedUserInput: s.HasQueuedInput,
			Model:              s.Model,
			TotalCost:          s.TotalCost,
		},
		Delegates: s.Delegates,
	}
}

// RecordExternalMessage records a message from an external source (e.g., a delegate child loop).
// It publishes the message to the SubPub for SSE streaming, persists it via onMessage,
// and returns the stored message with assigned ID/sequence metadata.
func (sm *SessionManager) RecordExternalMessage(ctx context.Context, msg Message) (Message, error) {
	msg.SessionID = sm.id
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}

	if msg.Type == MessageTypeAssistant && msg.Usage != nil {
		cost := sm.calculateCost(msg.Usage)
		if cost > 0 && msg.Cost == nil {
			msg.Cost = &cost
		}
		sm.addCost(cost)
	}

	sm.mu.Lock()
	sm.bumpLastActivityLocked(time.Now())
	loop := sm.loop
	sm.mu.Unlock()

	if loop != nil && msg.LLMData != nil {
		loop.AppendExternalHistory(*msg.LLMData)
	}

	msg.SequenceID = sm.appendMessage(msg)

	sm.subpub.Publish(msg.SequenceID, StreamResponse{
		Messages: []Message{msg},
	})

	if sm.onMessage != nil {
		if err := sm.onMessage(ctx, msg); err != nil {
			sm.logger.Warn("failed to persist message", "error", err)
			return Message{}, err
		}
	}

	return msg, nil
}

func (sm *SessionManager) enqueueImmediateUserMessage(ctx context.Context, displayContent, llmContent string) error {
	llmMsg := llm.Message{Role: llm.RoleUser, Content: llmContent}
	sm.recordAndPublishUserMessage(ctx, displayContent, &llmMsg)

	// Cancel any pending general prompts so the loop unblocks from WaitUserResponse.
	sm.CancelPendingPrompts()

	sm.mu.Lock()
	loop := sm.loop
	sm.mu.Unlock()
	if loop == nil {
		return errors.New("session loop not initialized")
	}
	sm.SetWorking(true)
	loop.QueueUserMessage(llmMsg)
	return nil
}

func (sm *SessionManager) enqueueInternalUserMessage(provider llm.Provider, modelID string, resolvedModel string, content string) error {
	if provider == nil {
		return errors.New("LLM provider is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("message is required")
	}
	if err := sm.ensureLoop(provider, modelID, resolvedModel); err != nil {
		return err
	}

	llmMsg := llm.Message{Role: llm.RoleUser, Content: content}
	sm.mu.Lock()
	sm.bumpLastActivityLocked(time.Now())
	loop := sm.loop
	sm.mu.Unlock()
	if loop == nil {
		return errors.New("session loop not initialized")
	}
	sm.SetWorking(true)
	loop.QueueUserMessage(llmMsg)
	return nil
}

// EnqueueChatMessage accepts bot text for a session. When the agent is already
// working, the text is merged into a single queued follow-up and marked as a
// safe-boundary interrupt instead of immediately entering LLM history.
func (sm *SessionManager) EnqueueChatMessage(ctx context.Context, provider llm.Provider, modelID string, resolvedModel string, content string) (bool, error) {
	return sm.EnqueueChatMessageWithLLMContent(ctx, provider, modelID, resolvedModel, content, content)
}

func (sm *SessionManager) EnqueueChatMessageWithLLMContent(ctx context.Context, provider llm.Provider, modelID string, resolvedModel string, displayContent string, llmContent string) (bool, error) {
	if provider == nil {
		return false, errors.New("LLM provider is required")
	}

	if err := sm.ensureLoop(provider, modelID, resolvedModel); err != nil {
		return false, err
	}

	// If a general prompt is pending, route text as the prompt response.
	if _, routed := sm.tryRouteToGeneralPrompt(displayContent); routed {
		sm.recordAndPublishUserMessage(ctx, displayContent, nil)
		return false, nil
	}

	sm.mu.Lock()
	shouldQueue := sm.working || sm.hasQueuedChatInputLocked()
	if shouldQueue {
		sm.queuedChatMessages = append(sm.queuedChatMessages, queuedChatMessage{
			displayContent: displayContent,
			llmContent:     llmContent,
		})
		sm.bumpLastActivityLocked(time.Now())
	}
	loop := sm.loop
	sm.mu.Unlock()

	if shouldQueue {
		if loop == nil {
			return false, errors.New("session loop not initialized")
		}
		if sm.IsWorking() {
			loop.RequestInterrupt()
		}
		return true, nil
	}

	return false, sm.enqueueImmediateUserMessage(ctx, displayContent, llmContent)
}

// AcceptUserMessage enqueues a user message, ensuring the loop is ready first.
// If a general (non-approval) prompt is pending, the text is routed as the
// prompt response instead of starting a new LLM turn. This lets users answer
// ask_user prompts by typing in the main chat input.
func (sm *SessionManager) AcceptUserMessage(ctx context.Context, provider llm.Provider, modelID string, resolvedModel string, content string) error {
	return sm.AcceptUserMessageWithLLMContent(ctx, provider, modelID, resolvedModel, content, content)
}

func (sm *SessionManager) AcceptUserMessageWithLLMContent(ctx context.Context, provider llm.Provider, modelID string, resolvedModel string, displayContent string, llmContent string) error {
	if provider == nil {
		return errors.New("LLM provider is required")
	}

	if err := sm.ensureLoop(provider, modelID, resolvedModel); err != nil {
		return err
	}

	// If a general prompt is pending, route text as the prompt response.
	if _, routed := sm.tryRouteToGeneralPrompt(displayContent); routed {
		// Record for UI display only (LLMData=nil excludes from LLM history
		// on session restore — the tool_result carries the response content).
		sm.recordAndPublishUserMessage(ctx, displayContent, nil)
		return nil
	}

	return sm.enqueueImmediateUserMessage(ctx, displayContent, llmContent)
}

// BeginQueuedChatFlush drains the current merged chat buffer and marks a flush
// as in progress so concurrent enqueues continue merging instead of racing a new turn.
func (sm *SessionManager) BeginQueuedChatFlush() (string, string, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.flushingQueuedChat || len(sm.queuedChatMessages) == 0 {
		return "", "", false
	}
	sm.flushingQueuedChat = true
	displayParts := make([]string, 0, len(sm.queuedChatMessages))
	llmParts := make([]string, 0, len(sm.queuedChatMessages))
	for _, msg := range sm.queuedChatMessages {
		displayParts = append(displayParts, msg.displayContent)
		llmParts = append(llmParts, msg.llmContent)
	}
	displayText := strings.Join(displayParts, queuedChatMessageSeparator)
	llmText := strings.Join(llmParts, queuedChatMessageSeparator)
	sm.queuedChatMessages = nil
	return displayText, llmText, true
}

// RestoreQueuedChatInput restores a drained merged chat buffer after a failed flush.
func (sm *SessionManager) RestoreQueuedChatInput(displayContent, llmContent string) {
	if displayContent == "" && llmContent == "" {
		sm.CompleteQueuedChatFlush()
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.flushingQueuedChat = false
	restored := queuedChatMessage{
		displayContent: displayContent,
		llmContent:     llmContent,
	}
	sm.queuedChatMessages = append([]queuedChatMessage{restored}, sm.queuedChatMessages...)
	sm.bumpLastActivityLocked(time.Now())
}

// CompleteQueuedChatFlush clears the in-flight flush marker after the queued turn starts.
func (sm *SessionManager) CompleteQueuedChatFlush() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.flushingQueuedChat = false
}

// recordAndPublishUserMessage builds a user message, publishes it to SSE
// subscribers, and persists it via the onMessage callback.
func (sm *SessionManager) recordAndPublishUserMessage(ctx context.Context, content string, llmMsg *llm.Message) {
	msg := sm.buildUserMessage(content, llmMsg)
	sm.subpub.Publish(msg.SequenceID, StreamResponse{Messages: []Message{msg}})
	if sm.onMessage != nil {
		if err := sm.onMessage(ctx, msg); err != nil {
			sm.logger.Warn("failed to persist user message", "error", err)
		}
	}
}

// buildUserMessage adds a user message to the session and returns it.
func (sm *SessionManager) buildUserMessage(content string, llmMsg *llm.Message) Message {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	now = sm.bumpLastActivityLocked(now)
	sm.sequenceID++

	msg := Message{
		ID:         uuid.New().String(),
		SessionID:  sm.id,
		Type:       MessageTypeUser,
		SequenceID: sm.sequenceID,
		Content:    content,
		CreatedAt:  now,
		LLMData:    llmMsg,
	}

	sm.setTitleFromMessageLocked(msg)
	sm.messages = append(sm.messages, msg)
	return msg
}

// Subscribe returns a function that blocks until the next message is available.
func (sm *SessionManager) Subscribe(ctx context.Context) func() (StreamResponse, bool) {
	sm.mu.Lock()
	lastSeq := sm.sequenceID
	sm.mu.Unlock()

	return sm.subpub.Subscribe(ctx, lastSeq)
}

// SubscribeWithSnapshot atomically captures current state and subscribes.
func (sm *SessionManager) SubscribeWithSnapshot(ctx context.Context) (StreamResponse, func() (StreamResponse, bool)) {
	sm.mu.Lock()
	lastSeq := sm.sequenceID
	snapshot := sm.snapshotLocked()
	next := sm.subpub.Subscribe(ctx, lastSeq)
	sm.mu.Unlock()

	return snapshot.StreamResponse(), next
}

// Cancel stops the session loop. The context parameter is unused
// because cancellation is performed synchronously via the internal cancel function.
func (sm *SessionManager) Cancel(_ context.Context) error {
	sm.mu.Lock()
	sm.canceling = true
	sm.mu.Unlock()

	// Cancel pending prompts first, before cancelling the loop context.
	// This lets waiting prompt handlers exit cleanly instead of surfacing
	// raw context cancellation errors.
	sm.CancelAllPendingPrompts()

	if cancel := sm.clearLoop(); cancel != nil {
		cancel()
	}
	sm.SetWorking(false)
	sm.logger.Info("session cancelled")
	return nil
}

// clearLoop resets the loop state and returns the cancel function.
func (sm *SessionManager) clearLoop() context.CancelFunc {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cancel := sm.loopCancel
	sm.loopCancel = nil
	sm.loop = nil
	return cancel
}

// ensureLoop creates the loop if it doesn't exist.
// The lock is released during createLoop (which may be slow) and re-acquired
// afterward with a double-check to handle concurrent callers.
func (sm *SessionManager) ensureLoop(provider llm.Provider, modelID string, resolvedModel string) error {
	sm.mu.Lock()
	if sm.loop != nil {
		sm.mu.Unlock()
		return nil
	}
	sm.canceling = false
	sm.model = modelID
	history := sm.extractLLMHistoryLocked()
	safeMode := sm.safeMode
	thinkingEffort := sm.thinkingEffort
	sm.mu.Unlock()

	loopCtx, cancel := context.WithCancel(context.Background())
	loopInstance := sm.createLoop(provider, resolvedModel, history, safeMode, thinkingEffort)

	sm.mu.Lock()
	if sm.loop != nil {
		sm.mu.Unlock()
		cancel()
		return nil
	}
	sm.loop = loopInstance
	sm.loopCancel = cancel
	sm.mu.Unlock()

	go sm.runLoop(loopCtx, loopInstance)
	return nil
}

// createLoop creates a new Loop instance with the current configuration.
func (sm *SessionManager) createLoop(provider llm.Provider, model string, history []llm.Message, safeMode bool, thinkingEffort llm.ThinkingEffort) *Loop {
	memory := sm.loadMemory()
	return NewLoop(LoopConfig{
		Provider: provider,
		Model:    model,
		History:  history,
		Tools: CreateTools(ToolConfig{
			DAGsDir:               sm.environment.DAGsDir,
			DAGStore:              sm.dagStore,
			DAGRunStore:           sm.dagRunStore,
			DAGRunWatcher:         sm.dagRunWatcher,
			DocStore:              sm.docStore,
			WorkspaceStore:        sm.workspaceStore,
			RemoteContextResolver: sm.remoteContextResolver,
			WebTools:              sm.webTools,
		}),
		RecordMessage: sm.createRecordMessageFunc(),
		Logger:        sm.logger,
		SystemPrompt: GenerateSystemPrompt(SystemPromptParams{
			Env:             sm.environment,
			Memory:          memory,
			Role:            sm.user.Role,
			WorkspaceAccess: sm.user.WorkspaceAccess,
			Soul:            sm.soul,
		}),
		DynamicSystemContext: sm.dynamicSystemCtx,
		WorkingDir:           sm.workingDir,
		SessionID:            sm.id,
		OnWorking:            sm.SetWorking,
		OnHeartbeat:          sm.RecordHeartbeat,
		EmitUIAction:         sm.createEmitUIActionFunc(),
		EmitUserPrompt:       sm.createEmitUserPromptFunc(),
		WaitUserResponse:     sm.createWaitUserResponseFunc(),
		SafeMode:             safeMode,
		ThinkingEffort:       thinkingEffort,
		Hooks:                sm.hooks,
		User:                 sm.user,
		SessionStore:         sm.sessionStore,
		Registry:             sm.registry,
		WebSearch:            sm.webSearch,
	})
}

// loadMemory loads memory content from the memory store.
func (sm *SessionManager) loadMemory() MemoryContent {
	if sm.memoryStore == nil {
		return MemoryContent{}
	}
	ctx := context.Background()
	global, err := sm.memoryStore.LoadGlobalMemory(ctx)
	if err != nil {
		sm.logger.Debug("failed to load global memory", "error", err)
	}
	var dagMem string
	if sm.dagName != "" {
		dagMem, err = sm.memoryStore.LoadDAGMemory(ctx, sm.dagName)
		if err != nil {
			sm.logger.Debug("failed to load DAG memory", "error", err, "dag_name", sm.dagName)
		}
	}
	return MemoryContent{
		GlobalMemory: global,
		DAGMemory:    dagMem,
		DAGName:      sm.dagName,
		MemoryDir:    sm.memoryStore.MemoryDir(),
	}
}

// runLoop executes the session loop and handles cleanup.
func (sm *SessionManager) runLoop(ctx context.Context, loop *Loop) {
	defer func() {
		if r := recover(); r != nil {
			sm.logger.Error("session loop panicked", "panic", r, "stack", string(debug.Stack()))
		}
		sm.SetWorking(false)
		sm.logger.Info("session loop goroutine exiting")
		sm.clearLoop()
	}()

	err := loop.Go(ctx)
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		sm.logger.Info("session loop canceled normally")
	} else {
		sm.logger.Error("session loop stopped with error", "error", err)
	}
}

// extractLLMHistoryLocked converts stored messages to LLM format.
// If the history ends with an assistant message containing tool calls
// that have no matching tool results (e.g. due to cancellation or crash),
// synthetic "cancelled" results are appended so the LLM API accepts
// the history without errors.
// Must be called with sm.mu held.
func (sm *SessionManager) extractLLMHistoryLocked() []llm.Message {
	history := make([]llm.Message, 0, len(sm.messages))
	for _, msg := range sm.messages {
		if msg.LLMData != nil {
			history = append(history, *msg.LLMData)
		}
	}
	return repairOrphanedToolCalls(history)
}

// repairOrphanedToolCalls scans the entire history for assistant messages
// with tool calls that lack matching tool-role results, and inserts
// synthetic cancelled results immediately after the tool result block.
// This handles orphaned tool calls anywhere in the history (not just the
// last assistant message), which can occur from mid-conversation crashes
// or cancellations.
func repairOrphanedToolCalls(history []llm.Message) []llm.Message {
	if len(history) == 0 {
		return history
	}

	var result []llm.Message
	for i := 0; i < len(history); i++ {
		result = append(result, history[i])

		msg := history[i]
		if msg.Role != llm.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}

		// Collect IDs of tool results that immediately follow this assistant message.
		answered := make(map[string]struct{})
		j := i + 1
		for j < len(history) && history[j].Role == llm.RoleTool {
			if history[j].ToolCallID != "" {
				answered[history[j].ToolCallID] = struct{}{}
			}
			result = append(result, history[j])
			j++
		}

		// Synthesize results for any orphaned tool calls.
		for _, tc := range msg.ToolCalls {
			if _, ok := answered[tc.ID]; !ok {
				result = append(result, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tc.ID,
					Content:    "Tool execution was cancelled.",
				})
			}
		}

		i = j - 1 // skip already-processed tool results
	}

	return result
}

// createRecordMessageFunc returns a function for recording messages to the session.
// Persistence errors are logged but not propagated — the session continues operating
// even if individual messages fail to persist, to avoid disrupting the user's workflow.
func (sm *SessionManager) createRecordMessageFunc() MessageRecordFunc {
	return func(ctx context.Context, msg Message) {
		msg.SessionID = sm.id
		if msg.ID == "" {
			msg.ID = uuid.New().String()
		}

		// Calculate and accumulate cost for assistant messages with usage data
		if msg.Type == MessageTypeAssistant && msg.Usage != nil {
			cost := sm.calculateCost(msg.Usage)
			if cost > 0 {
				msg.Cost = &cost
			}
			sm.addCost(cost)
		}

		msg.SequenceID = sm.appendMessage(msg)

		sm.subpub.Publish(msg.SequenceID, StreamResponse{
			Messages: []Message{msg},
		})

		if sm.onMessage != nil {
			if err := sm.onMessage(ctx, msg); err != nil {
				sm.logger.Warn("failed to persist message", "error", err)
			}
		}
	}
}

// appendMessage adds a message to the session and returns the new sequence ID.
func (sm *SessionManager) appendMessage(msg Message) int64 {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.setTitleFromMessageLocked(msg)
	sm.messages = append(sm.messages, msg)
	sm.sequenceID++
	return sm.sequenceID
}

func (sm *SessionManager) setTitleFromMessageLocked(msg Message) {
	if sm.title == "" && msg.Type == MessageTypeUser {
		sm.title = titleFromUserMessage(msg.Content)
	}
}

func titleFromUserMessage(content string) string {
	title := strings.Join(strings.Fields(content), " ")
	if title == "" {
		return ""
	}
	runes := []rune(title)
	if len(runes) <= maxSessionTitleLength {
		return title
	}
	return string(runes[:maxSessionTitleLength-3]) + "..."
}

// createEmitUIActionFunc returns a function for emitting UI actions.
func (sm *SessionManager) createEmitUIActionFunc() UIActionFunc {
	return func(action UIAction) {
		sm.mu.Lock()
		sm.sequenceID++
		seqID := sm.sequenceID
		msg := Message{
			ID:         fmt.Sprintf("ui-%d", seqID),
			SessionID:  sm.id,
			Type:       MessageTypeUIAction,
			SequenceID: seqID,
			UIAction:   &action,
			CreatedAt:  time.Now(),
		}
		sm.messages = append(sm.messages, msg)
		sm.mu.Unlock()

		sm.subpub.Publish(msg.SequenceID, StreamResponse{
			Messages: []Message{msg},
		})

		if sm.onMessage != nil {
			if err := sm.onMessage(context.Background(), msg); err != nil {
				sm.logger.Warn("failed to persist ui action message", "error", err)
			}
		}
	}
}

// createEmitUserPromptFunc returns a function for emitting user prompts.
func (sm *SessionManager) createEmitUserPromptFunc() EmitUserPromptFunc {
	return func(prompt UserPrompt) {
		// Always allow free-text responses for general prompts so users
		// have an escape hatch regardless of what the LLM specified.
		if prompt.PromptType != PromptTypeCommandApproval {
			prompt.AllowFreeText = true
			if prompt.FreeTextPlaceholder == "" {
				prompt.FreeTextPlaceholder = "Or type your answer..."
			}
		}

		// Track prompt type for selective cancellation in CancelPendingPrompts.
		sm.promptsMu.Lock()
		sm.promptTypes[prompt.PromptID] = prompt.PromptType
		sm.promptsMu.Unlock()

		msg := Message{
			ID:         fmt.Sprintf("prompt-%s", prompt.PromptID),
			SessionID:  sm.id,
			Type:       MessageTypeUserPrompt,
			UserPrompt: &prompt,
			CreatedAt:  time.Now(),
		}

		msg.SequenceID = sm.appendMessage(msg)

		sm.subpub.Publish(msg.SequenceID, StreamResponse{
			Messages: []Message{msg},
		})

		if sm.onMessage != nil {
			if err := sm.onMessage(context.Background(), msg); err != nil {
				sm.logger.Warn("failed to persist user prompt message", "error", err)
			}
		}
	}
}

// createWaitUserResponseFunc returns a function that blocks until user responds.
func (sm *SessionManager) createWaitUserResponseFunc() WaitUserResponseFunc {
	return func(ctx context.Context, promptID string) (UserPromptResponse, error) {
		ch := make(chan UserPromptResponse, 1)

		sm.mu.Lock()
		sm.recordHeartbeatLocked(time.Now())
		sm.promptsMu.Lock()
		sm.pendingPrompts[promptID] = ch
		sm.promptsMu.Unlock()
		sm.mu.Unlock()
		stopHeartbeat := sm.startPromptWaitHeartbeat(ctx)

		defer func() {
			stopHeartbeat()
			sm.promptsMu.Lock()
			delete(sm.pendingPrompts, promptID)
			delete(sm.promptTypes, promptID)
			sm.promptsMu.Unlock()
		}()

		select {
		case resp := <-ch:
			sm.RecordHeartbeat()
			return resp, nil
		case <-ctx.Done():
			select {
			case resp := <-ch:
				sm.RecordHeartbeat()
				return resp, nil
			default:
			}
			if sm.isCanceling() {
				sm.RecordHeartbeat()
				return UserPromptResponse{PromptID: promptID, Cancelled: true}, nil
			}
			return UserPromptResponse{}, ctx.Err()
		}
	}
}

func (sm *SessionManager) startPromptWaitHeartbeat(ctx context.Context) func() {
	sm.RecordHeartbeat()
	if ctx == nil || sm.promptWaitInterval <= 0 {
		return func() {}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(stopped)

		ticker := time.NewTicker(sm.promptWaitInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				sm.RecordHeartbeat()
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}

// SubmitUserResponse delivers a user's response to a pending prompt.
// Returns true if the response was delivered, false if no pending prompt exists.
func (sm *SessionManager) SubmitUserResponse(response UserPromptResponse) bool {
	sm.promptsMu.Lock()
	ch, exists := sm.pendingPrompts[response.PromptID]
	sm.promptsMu.Unlock()

	if !exists {
		slog.Warn("no pending prompt for response", "promptID", response.PromptID)
		return false
	}

	select {
	case ch <- response:
		sm.RecordHeartbeat()
		return true
	default:
		slog.Warn("response dropped, channel full", "promptID", response.PromptID)
		return false
	}
}

// nextSequenceID increments and returns the next sequence ID.
func (sm *SessionManager) nextSequenceID() int64 {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sequenceID++
	return sm.sequenceID
}

// GetTotalCost returns the accumulated cost of the session in USD.
func (sm *SessionManager) GetTotalCost() float64 {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.totalCost
}

// UpdatePricing updates the per-token pricing for this session.
func (sm *SessionManager) UpdatePricing(inputCostPer1M, outputCostPer1M float64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.inputCostPer1M = inputCostPer1M
	sm.outputCostPer1M = outputCostPer1M
}

// calculateCost computes the cost of a single message from its token usage.
func (sm *SessionManager) calculateCost(usage *llm.Usage) float64 {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if usage == nil {
		return 0
	}
	return (float64(usage.PromptTokens) * sm.inputCostPer1M / 1_000_000) +
		(float64(usage.CompletionTokens) * sm.outputCostPer1M / 1_000_000)
}

// SetDelegateStarted records that a delegate sub-agent has started.
func (sm *SessionManager) SetDelegateStarted(id, task string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.delegates[id] = DelegateSnapshot{
		ID:     id,
		Task:   task,
		Status: DelegateStatusRunning,
	}
}

// SetDelegateCompleted records that a delegate sub-agent has completed.
func (sm *SessionManager) SetDelegateCompleted(id string, cost float64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if snap, ok := sm.delegates[id]; ok {
		snap.Status = DelegateStatusCompleted
		snap.Cost = cost
		sm.delegates[id] = snap
	}
}

// GetDelegates returns snapshots of all tracked delegate sub-agents.
func (sm *SessionManager) GetDelegates() []DelegateSnapshot {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.delegates) == 0 {
		return nil
	}
	result := make([]DelegateSnapshot, 0, len(sm.delegates))
	for _, snap := range sm.delegates {
		result = append(result, snap)
	}
	return result
}

// addCost adds a cost amount to the running total.
func (sm *SessionManager) addCost(cost float64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.totalCost += cost
}

// sessionRegistry implements SubSessionRegistry by combining
// API-level session storage with SessionManager-level notifications.
type sessionRegistry struct {
	sessions *sync.Map       // API's active sessions map
	parent   *SessionManager // parent session manager
}

func (r *sessionRegistry) RegisterSubSession(id string, mgr *SessionManager) {
	r.sessions.Store(id, mgr)
}

func (r *sessionRegistry) DeregisterSubSession(id string) {
	r.sessions.Delete(id)
}

func (r *sessionRegistry) NotifyParent(event StreamResponse) {
	r.parent.subpub.Broadcast(event)
}

func (r *sessionRegistry) AddCost(cost float64) {
	r.parent.addCost(cost)
}

func (r *sessionRegistry) ParentSessionManager() *SessionManager {
	return r.parent
}
