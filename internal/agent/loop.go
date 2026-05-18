// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/llm"
)

const (
	// idlePollingInterval is the interval for polling when no messages are queued.
	idlePollingInterval = 100 * time.Millisecond

	// maxToolCallDepth limits nested tool call chains to prevent infinite recursion.
	// This can happen if an LLM continuously makes tool calls without producing a final response.
	maxToolCallDepth = 50

	// loopHeartbeatInterval is the interval at which the loop emits heartbeats.
	loopHeartbeatInterval = 10 * time.Second
)

var errTurnInterrupted = errors.New("turn interrupted")

// MessageRecordFunc is called to record new messages to persistent storage.
// Implementations should log persistence errors internally rather than returning them,
// since the session continues operating even if individual messages fail to persist.
type MessageRecordFunc func(ctx context.Context, message Message)

// LoopConfig contains configuration for creating a Loop.
type LoopConfig struct {
	// Provider is the LLM provider for making requests.
	Provider llm.Provider
	// Model is the model ID to use for requests.
	Model string
	// History is the initial session history.
	History []llm.Message
	// Tools is the list of tools available to the agent.
	Tools []*AgentTool
	// RecordMessage is called to record new messages.
	RecordMessage MessageRecordFunc
	// Logger for logging events.
	Logger *slog.Logger
	// SystemPrompt is the system message to prepend.
	SystemPrompt string
	// DynamicSystemContext returns volatile context appended to the system
	// message for each LLM request.
	DynamicSystemContext DynamicSystemContextFunc
	// WorkingDir is the working directory for tools.
	WorkingDir string
	// SessionID is the ID of the session.
	SessionID string
	// OnWorking is called when the working state changes.
	OnWorking func(working bool)
	// OnHeartbeat is called periodically to signal the loop is alive.
	OnHeartbeat func()
	// EmitUIAction is called when a tool wants to emit a UI action.
	EmitUIAction UIActionFunc
	// EmitUserPrompt is called when a tool wants to emit a user prompt.
	EmitUserPrompt EmitUserPromptFunc
	// WaitUserResponse blocks until user responds to a prompt.
	WaitUserResponse WaitUserResponseFunc
	// SafeMode enables approval prompts for dangerous commands when true.
	SafeMode bool
	// ThinkingEffort configures default reasoning depth for supported models.
	ThinkingEffort llm.ThinkingEffort
	// Hooks provides lifecycle callbacks for tool execution.
	Hooks *Hooks
	// User is the authenticated user's identity.
	User UserIdentity
	// SessionStore is used for delegate sub-session persistence.
	SessionStore SessionStore
	// Registry manages sub-session lifecycle for delegate tools.
	Registry SubSessionRegistry
	// WebSearch configures provider-native web search for requests.
	WebSearch *llm.WebSearchRequest
	// OnTurnComplete is called after a queued message batch is successfully
	// processed (processLLMRequest returned nil). For single-shot callers
	// like the agent-step executor this is the signal to cancel the loop.
	OnTurnComplete func()
	// ReturnTurnErrors makes Go return turn-processing errors instead of
	// recording them and waiting for another queued message. Interactive
	// sessions keep this false so a later user message can recover.
	ReturnTurnErrors bool
	// LogicalRetryConfig overrides the default logical retry budget for LLM requests.
	LogicalRetryConfig llm.LogicalRetryConfig
}

// Loop manages a session turn with an LLM including tool execution.
type Loop struct {
	provider           llm.Provider
	model              string
	tools              []*AgentTool
	recordMessage      MessageRecordFunc
	history            []llm.Message
	messageQueue       []llm.Message
	totalUsage         llm.Usage
	mu                 sync.Mutex
	logger             *slog.Logger
	systemPrompt       string
	dynamicSystemCtx   DynamicSystemContextFunc
	workingDir         string
	sessionID          string
	onWorking          func(working bool)
	onHeartbeat        func()
	sequenceID         int64
	emitUIAction       UIActionFunc
	emitUserPrompt     EmitUserPromptFunc
	waitUserResponse   WaitUserResponseFunc
	safeMode           bool
	thinkingEffort     llm.ThinkingEffort
	hooks              *Hooks
	user               UserIdentity
	sessionStore       SessionStore
	registry           SubSessionRegistry
	webSearch          *llm.WebSearchRequest
	onTurnComplete     func()
	returnTurnErrors   bool
	logicalRetryConfig llm.LogicalRetryConfig
	activeTurn         bool
	interruptRequested bool
}

// NewLoop creates a new Loop instance.
func NewLoop(config LoopConfig) *Loop {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Loop{
		provider:           config.Provider,
		model:              config.Model,
		history:            config.History,
		tools:              config.Tools,
		recordMessage:      config.RecordMessage,
		logger:             logger,
		systemPrompt:       config.SystemPrompt,
		dynamicSystemCtx:   config.DynamicSystemContext,
		workingDir:         config.WorkingDir,
		sessionID:          config.SessionID,
		onWorking:          config.OnWorking,
		onHeartbeat:        config.OnHeartbeat,
		emitUIAction:       config.EmitUIAction,
		emitUserPrompt:     config.EmitUserPrompt,
		waitUserResponse:   config.WaitUserResponse,
		safeMode:           config.SafeMode,
		thinkingEffort:     config.ThinkingEffort,
		hooks:              config.Hooks,
		user:               config.User,
		sessionStore:       config.SessionStore,
		registry:           config.Registry,
		webSearch:          config.WebSearch,
		onTurnComplete:     config.OnTurnComplete,
		returnTurnErrors:   config.ReturnTurnErrors,
		logicalRetryConfig: config.LogicalRetryConfig,
	}
}

// SetDynamicSystemContext updates the volatile context provider used by future
// LLM requests.
func (l *Loop) SetDynamicSystemContext(fn DynamicSystemContextFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dynamicSystemCtx = fn
}

// QueueUserMessage adds a user message to the queue to be processed.
func (l *Loop) QueueUserMessage(message llm.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messageQueue = append(l.messageQueue, message)
	l.logger.Info("queued user message", "queue_size", len(l.messageQueue))
}

// RequestInterrupt asks the loop to stop the current turn at the next safe boundary.
func (l *Loop) RequestInterrupt() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.interruptRequested = true
}

// SetThinkingEffort updates the reasoning effort used for future turns.
func (l *Loop) SetThinkingEffort(effort llm.ThinkingEffort) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.thinkingEffort = effort
}

// SetProviderModel updates the provider and resolved model for future turns.
func (l *Loop) SetProviderModel(provider llm.Provider, model string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.provider = provider
	l.model = model
}

// AppendExternalHistory injects a message into the loop's in-memory LLM history.
// This keeps future turns consistent when assistant content is appended outside
// the loop itself, such as bot notifications seeded into an active session.
func (l *Loop) AppendExternalHistory(message llm.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.history = append(l.history, message)
}

// SetSafeMode updates the safe mode setting for this loop.
func (l *Loop) SetSafeMode(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.safeMode = enabled
}

// Go runs the session loop until the context is canceled.
func (l *Loop) Go(ctx context.Context) error {
	l.mu.Lock()
	provider := l.provider
	l.mu.Unlock()
	if provider == nil {
		return fmt.Errorf("no LLM provider configured")
	}

	l.logger.Info("starting session loop", "tools", len(l.tools))

	idleTimer := time.NewTimer(idlePollingInterval)
	defer idleTimer.Stop()

	heartbeatTicker := time.NewTicker(loopHeartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.logger.Info("session loop canceled")
			return ctx.Err()
		default:
		}

		// Non-blocking heartbeat drain for progress between iterations.
		select {
		case <-heartbeatTicker.C:
			if l.onHeartbeat != nil {
				l.onHeartbeat()
			}
		default:
		}

		hasActiveTurn, historyCount := l.activateQueuedTurn()

		if hasActiveTurn {
			l.logger.Info("processing queued messages", "history_count", historyCount)
			if err := l.processLLMRequest(ctx); err != nil {
				if errors.Is(err, errTurnInterrupted) {
					l.logger.Info("turn interrupted at safe boundary")
					l.finishActiveTurn()
					continue
				}
				l.logger.Error("failed to process LLM request", "error", err)
				l.finishActiveTurn()
				if l.returnTurnErrors {
					return err
				}
				continue
			}
			l.finishActiveTurn()
			l.logger.Info("finished processing queued messages")
			if l.onTurnComplete != nil {
				l.onTurnComplete()
			}
		} else {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idlePollingInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-heartbeatTicker.C:
				if l.onHeartbeat != nil {
					l.onHeartbeat()
				}
			case <-idleTimer.C:
			}
		}
	}
}

// sleepWithContext sleeps for the given duration or until the context is canceled.
func (l *Loop) sleepWithContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// processLLMRequest sends a request to the LLM and handles the response.
func (l *Loop) processLLMRequest(ctx context.Context) error {
	resp, err := l.sendRequest(ctx)
	if err != nil {
		return err
	}

	if l.hasInterruptRequested() {
		if len(resp.ToolCalls) > 0 {
			l.recordCancelledToolResults(ctx, resp.ToolCalls)
		}
		l.setWorking(false)
		return errTurnInterrupted
	}

	if len(resp.ToolCalls) > 0 {
		l.logger.Info("handling tool calls", "count", len(resp.ToolCalls))
		return l.handleToolCalls(ctx, resp.ToolCalls)
	}

	l.setWorking(false)
	return nil
}

// sendRequest builds, sends, and records an LLM request. On success it
// accumulates usage and records the assistant message.
func (l *Loop) sendRequest(ctx context.Context) (*llm.ChatResponse, error) {
	history := l.copyHistory()
	messages := l.buildMessages(ctx, history)
	tools := l.buildToolDefinitions()
	l.mu.Lock()
	provider := l.provider
	model := l.model
	thinkingEffort := l.thinkingEffort
	webSearch := l.webSearch
	l.mu.Unlock()

	req := &llm.ChatRequest{
		Model:     model,
		Messages:  messages,
		Tools:     tools,
		WebSearch: webSearch,
	}
	if thinkingEffort != "" {
		req.Thinking = &llm.ThinkingRequest{
			Enabled: true,
			Effort:  thinkingEffort,
		}
	}

	l.logger.Debug("sending LLM request",
		"message_count", len(messages),
		"tool_count", len(tools),
		"model", model)

	l.setWorking(true)

	stopHeartbeat := startHeartbeatPump(ctx, loopHeartbeatInterval, l.onHeartbeat)
	defer stopHeartbeat()

	resp, err := llm.ChatWithRetry(ctx, provider, req, l.logicalRetryConfig)
	if err != nil {
		l.recordErrorMessage(ctx, fmt.Sprintf("LLM request failed: %v", err))
		l.setWorking(false)
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	l.logger.Debug("received LLM response",
		"content_length", len(resp.Content),
		"finish_reason", resp.FinishReason,
		"tool_calls", len(resp.ToolCalls))

	l.accumulateUsage(resp.Usage)
	l.recordAssistantMessage(ctx, resp)
	return resp, nil
}

func (l *Loop) activateQueuedTurn() (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.activeTurn && len(l.messageQueue) > 0 {
		l.history = append(l.history, l.messageQueue...)
		l.messageQueue = l.messageQueue[:0]
		l.activeTurn = true
		l.interruptRequested = false
	}

	return l.activeTurn, len(l.history)
}

func (l *Loop) finishActiveTurn() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.activeTurn = false
}

// setWorking safely calls the onWorking callback if configured.
func (l *Loop) setWorking(working bool) {
	if l.onWorking != nil {
		l.onWorking(working)
	}
}

func (l *Loop) hasInterruptRequested() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interruptRequested
}

func startHeartbeatPump(ctx context.Context, interval time.Duration, heartbeat func()) func() {
	if heartbeat == nil || interval <= 0 {
		return func() {}
	}

	done := make(chan struct{})
	var once sync.Once

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				heartbeat()
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
		})
	}
}

// copyHistory returns a thread-safe copy of the session history.
func (l *Loop) copyHistory() []llm.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]llm.Message(nil), l.history...)
}

// appendToHistory adds a message to history and returns the new sequence ID.
func (l *Loop) appendToHistory(msg llm.Message) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.history = append(l.history, msg)
	l.sequenceID++
	return l.sequenceID
}

// executeTool runs a single tool call and returns the result.
func (l *Loop) executeTool(ctx context.Context, tc llm.ToolCall) ToolOut {
	tool := GetToolByName(l.tools, tc.Function.Name)
	if tool == nil {
		l.logger.Error("tool not found", "name", tc.Function.Name)
		return toolError("Tool '%s' not found", tc.Function.Name)
	}

	input := json.RawMessage(tc.Function.Arguments)
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}

	l.mu.Lock()
	safeMode := l.safeMode
	user := l.user
	provider := l.provider
	model := l.model
	l.mu.Unlock()

	info := ToolExecInfo{
		ToolName:  tc.Function.Name,
		Input:     input,
		SessionID: l.sessionID,
		User:      user,
		Audit:     tool.Audit,
		SafeMode:  safeMode,
		RequestCommandApproval: func(ctx context.Context, command, reason string) (bool, error) {
			question := "Command blocked by policy. Approve command?"
			if reason != "" {
				question = "Command blocked by policy (" + reason + "). Approve command?"
			}
			return requestCommandApproval(
				ctx,
				l.emitUserPrompt,
				l.waitUserResponse,
				command,
				l.workingDir,
				question,
			)
		},
	}

	if err := l.hooks.RunBeforeToolExec(ctx, info); err != nil {
		return toolError("Blocked by policy: %v", err)
	}

	// Build delegate context only for the delegate tool and only when a registry is available.
	var delegate *DelegateContext
	if tc.Function.Name == delegateToolName && l.registry != nil {
		delegate = &DelegateContext{
			Provider:     provider,
			Model:        model,
			SystemPrompt: l.currentSystemPrompt(ctx),
			Tools:        l.tools,
			Hooks:        l.hooks,
			Logger:       l.logger,
			SessionStore: l.sessionStore,
			ParentID:     l.sessionID,
			User:         user,
			Registry:     l.registry,
		}
	}

	result := tool.Run(ToolContext{
		Context:          ctx,
		WorkingDir:       l.workingDir,
		EmitUIAction:     l.emitUIAction,
		EmitUserPrompt:   l.emitUserPrompt,
		WaitUserResponse: l.waitUserResponse,
		SafeMode:         safeMode,
		Role:             user.Role,
		SessionID:        l.sessionID,
		User:             user,
		SessionStore:     l.sessionStore,
		Delegate:         delegate,
	}, input)

	l.hooks.RunAfterToolExec(ctx, info, result)

	return result
}

// SetUserContext updates user metadata used for hooks and tool authorization.
func (l *Loop) SetUserContext(u UserIdentity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.user = u
}

// handleToolCalls processes tool calls from the LLM response using iteration
// instead of recursion to prevent stack overflow with long tool call chains.
func (l *Loop) handleToolCalls(ctx context.Context, toolCalls []llm.ToolCall) error {
	for depth := range maxToolCallDepth {
		// Heartbeat so cleanup doesn't cancel long-running tool chains.
		if l.onHeartbeat != nil {
			l.onHeartbeat()
		}

		executed := l.executeToolCalls(ctx, toolCalls)
		if executed < len(toolCalls) {
			l.recordCancelledToolResults(ctx, toolCalls[executed:])
			l.setWorking(false)
			return errTurnInterrupted
		}
		if l.hasInterruptRequested() {
			l.setWorking(false)
			return errTurnInterrupted
		}

		resp, err := l.sendRequest(ctx)
		if err != nil {
			return err
		}
		if l.hasInterruptRequested() {
			if len(resp.ToolCalls) > 0 {
				l.recordCancelledToolResults(ctx, resp.ToolCalls)
			}
			l.setWorking(false)
			return errTurnInterrupted
		}

		if len(resp.ToolCalls) == 0 {
			l.setWorking(false)
			return nil
		}

		l.logger.Info("continuing tool calls", "count", len(resp.ToolCalls), "depth", depth+1)
		toolCalls = resp.ToolCalls
	}

	l.setWorking(false)
	l.logger.Warn("max tool call depth reached", "depth", maxToolCallDepth)
	return fmt.Errorf("max tool call depth (%d) reached", maxToolCallDepth)
}

// executeToolCalls runs all tool calls sequentially.
// The delegate tool handles its own parallelism internally.
func (l *Loop) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall) int {
	l.setWorking(true)
	for i, tc := range toolCalls {
		l.logger.Debug("executing tool", "name", tc.Function.Name, "id", tc.ID)
		result := l.executeTool(ctx, tc)
		l.recordToolResult(ctx, tc, result)
		if l.hasInterruptRequested() {
			return i + 1
		}
	}
	return len(toolCalls)
}

// recordToolResult adds a tool result to history and records it.
func (l *Loop) recordToolResult(ctx context.Context, tc llm.ToolCall, result ToolOut) {
	toolMessage := llm.Message{
		Role:       llm.RoleTool,
		Content:    result.Content,
		ToolCallID: tc.ID,
	}
	seqID := l.appendToHistory(toolMessage)

	if l.recordMessage == nil {
		return
	}

	msg := Message{
		SessionID:  l.sessionID,
		Type:       MessageTypeUser, // Tool results are from user perspective
		SequenceID: seqID,
		ToolResults: []ToolResult{{
			ToolCallID: tc.ID,
			Content:    result.Content,
			IsError:    result.IsError,
		}},
		CreatedAt:   time.Now(),
		LLMData:     &toolMessage,
		DelegateIDs: result.DelegateIDs,
	}
	l.recordMessage(ctx, msg)
}

func (l *Loop) recordCancelledToolResults(ctx context.Context, toolCalls []llm.ToolCall) {
	for _, tc := range toolCalls {
		l.recordToolResult(ctx, tc, ToolOut{Content: "Tool execution was cancelled."})
	}
}

// nextSequenceID increments and returns the next sequence ID.
func (l *Loop) nextSequenceID() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sequenceID++
	return l.sequenceID
}

// recordErrorMessage records an error message to the session.
func (l *Loop) recordErrorMessage(ctx context.Context, errMsg string) {
	if l.recordMessage == nil {
		return
	}

	msg := Message{
		SessionID:  l.sessionID,
		Type:       MessageTypeError,
		SequenceID: l.nextSequenceID(),
		Content:    errMsg,
		CreatedAt:  time.Now(),
	}
	l.recordMessage(ctx, msg)
}

// buildMessages prepares the message list for an LLM request by optionally
// prepending the system prompt to the session history.
func (l *Loop) buildMessages(ctx context.Context, history []llm.Message) []llm.Message {
	systemPrompt := l.currentSystemPrompt(ctx)
	if systemPrompt == "" {
		return history
	}

	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt,
	})
	return append(messages, history...)
}

func (l *Loop) currentSystemPrompt(ctx context.Context) string {
	l.mu.Lock()
	base := l.systemPrompt
	dynamicProvider := l.dynamicSystemCtx
	l.mu.Unlock()

	var dynamic string
	if dynamicProvider != nil {
		dynamic = strings.TrimSpace(dynamicProvider(ctx))
	}
	switch {
	case strings.TrimSpace(base) == "":
		return dynamic
	case dynamic == "":
		return base
	default:
		return base + "\n\n" + dynamic
	}
}

// buildToolDefinitions converts agent tools to LLM tool definitions.
func (l *Loop) buildToolDefinitions() []llm.Tool {
	llmTools := make([]llm.Tool, len(l.tools))
	for i, t := range l.tools {
		llmTools[i] = t.Tool
	}
	return llmTools
}

// accumulateUsage adds response usage statistics to the total.
func (l *Loop) accumulateUsage(usage llm.Usage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.totalUsage.PromptTokens += usage.PromptTokens
	l.totalUsage.CompletionTokens += usage.CompletionTokens
	l.totalUsage.TotalTokens += usage.TotalTokens
}

// recordAssistantMessage adds the assistant response to history and records it.
func (l *Loop) recordAssistantMessage(ctx context.Context, resp *llm.ChatResponse) {
	assistantMessage := llm.Message{
		Role:             llm.RoleAssistant,
		Content:          resp.Content,
		ReasoningContent: resp.ReasoningContent,
		ToolCalls:        resp.ToolCalls,
	}
	seqID := l.appendToHistory(assistantMessage)

	if l.recordMessage == nil {
		return
	}

	msg := Message{
		SessionID:  l.sessionID,
		Type:       MessageTypeAssistant,
		SequenceID: seqID,
		Content:    resp.Content,
		ToolCalls:  resp.ToolCalls,
		Usage:      &resp.Usage,
		CreatedAt:  time.Now(),
		LLMData:    &assistantMessage,
	}
	l.recordMessage(ctx, msg)
}
