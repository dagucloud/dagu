// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLoop(t *testing.T) {
	t.Parallel()

	t.Run("initializes with config", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{name: "test"},
			Model:    "test-model",
			Tools:    CreateTools(ToolConfig{}),
		})

		assert.NotNil(t, loop)
	})

	t.Run("uses default logger if not provided", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
		})

		assert.NotNil(t, loop)
	})
}

func TestLoop_QueueUserMessage(t *testing.T) {
	t.Parallel()

	t.Run("adds message to queue", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{Provider: &mockLLMProvider{}})

		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		loop.mu.Lock()
		defer loop.mu.Unlock()
		assert.Len(t, loop.messageQueue, 1)
		assert.Equal(t, "test", loop.messageQueue[0].Content)
	})
}

func TestLoop_Go(t *testing.T) {
	t.Parallel()

	t.Run("returns error with nil provider", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{Provider: nil})

		err := loop.Go(context.Background())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no LLM provider")
	})

	t.Run("processes queued messages", func(t *testing.T) {
		t.Parallel()

		requestCh := make(chan *llm.ChatRequest, 1)
		provider := newCapturingProvider(requestCh, simpleStopResponse("response"))

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test-model",
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		go func() { _ = loop.Go(ctx) }()

		capturedRequest := waitForRequest(t, requestCh, 400*time.Millisecond)
		assert.Equal(t, "test-model", capturedRequest.Model)
		assert.NotEmpty(t, capturedRequest.Messages)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		t.Parallel()

		provider := &mockLLMProvider{
			chatFunc: func(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(10 * time.Second):
					return &llm.ChatResponse{Content: "late"}, nil
				}
			},
		}

		loop := NewLoop(LoopConfig{Provider: provider})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := loop.Go(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("calls OnWorking callback", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		var workingStates []bool

		loop := NewLoop(LoopConfig{
			Provider: newStopProvider("done"),
			OnWorking: func(working bool) {
				mu.Lock()
				workingStates = append(workingStates, working)
				mu.Unlock()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		runLoopForDuration(t, loop, 200*time.Millisecond)

		mu.Lock()
		states := append([]bool{}, workingStates...)
		mu.Unlock()

		assert.Contains(t, states, true)
		assert.Contains(t, states, false)
	})

	t.Run("records messages via callback", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		var recordedMessages []Message

		loop := NewLoop(LoopConfig{
			Provider:  newStopProvider("response"),
			SessionID: "conv-1",
			RecordMessage: func(_ context.Context, msg Message) {
				mu.Lock()
				recordedMessages = append(recordedMessages, msg)
				mu.Unlock()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		runLoopForDuration(t, loop, 200*time.Millisecond)

		mu.Lock()
		msgs := append([]Message{}, recordedMessages...)
		mu.Unlock()

		require.NotEmpty(t, msgs)
		assert.Equal(t, MessageTypeAssistant, msgs[0].Type)
		assert.Equal(t, "conv-1", msgs[0].SessionID)
	})

	t.Run("handles tool calls", func(t *testing.T) {
		t.Parallel()

		callCount := atomic.Int32{}
		done := make(chan struct{})
		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				count := callCount.Add(1)
				if count == 1 {
					return &llm.ChatResponse{
						FinishReason: "tool_calls",
						ToolCalls: []llm.ToolCall{{
							ID:   "call-1",
							Type: "function",
							Function: llm.ToolCallFunction{
								Name:      "think",
								Arguments: `{"thought": "test"}`,
							},
						}},
					}, nil
				}
				close(done)
				return simpleStopResponse("done"), nil
			},
		}

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Tools:    CreateTools(ToolConfig{}),
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		go func() { _ = loop.Go(ctx) }()

		select {
		case <-done:
		case <-time.After(800 * time.Millisecond):
			t.Fatal("timeout waiting for tool handling")
		}

		assert.Equal(t, int32(2), callCount.Load())
	})

	t.Run("includes system prompt", func(t *testing.T) {
		t.Parallel()

		requestCh := make(chan *llm.ChatRequest, 1)
		provider := newCapturingProvider(requestCh, simpleStopResponse("response"))

		loop := NewLoop(LoopConfig{
			Provider:     provider,
			SystemPrompt: "You are a helpful assistant.",
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		go func() { _ = loop.Go(ctx) }()

		capturedRequest := waitForRequest(t, requestCh, 400*time.Millisecond)
		require.NotEmpty(t, capturedRequest.Messages)
		assert.Equal(t, llm.RoleSystem, capturedRequest.Messages[0].Role)
		assert.Equal(t, "You are a helpful assistant.", capturedRequest.Messages[0].Content)
	})

	t.Run("calls OnHeartbeat during tool calls", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		heartbeatCount := 0

		callCount := atomic.Int32{}
		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				n := callCount.Add(1)
				if n == 1 {
					return &llm.ChatResponse{
						FinishReason: "tool_calls",
						ToolCalls: []llm.ToolCall{{
							ID:   "hb-call",
							Type: "function",
							Function: llm.ToolCallFunction{
								Name:      "think",
								Arguments: `{"thought": "heartbeat test"}`,
							},
						}},
					}, nil
				}
				return simpleStopResponse("done"), nil
			},
		}

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Tools:    CreateTools(ToolConfig{}),
			OnHeartbeat: func() {
				mu.Lock()
				heartbeatCount++
				mu.Unlock()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		runLoopForDuration(t, loop, 500*time.Millisecond)

		mu.Lock()
		count := heartbeatCount
		mu.Unlock()

		require.GreaterOrEqual(t, count, 1, "OnHeartbeat should fire during tool call handling")
	})

	t.Run("emits working pulses across tool phases", func(t *testing.T) {
		t.Parallel()

		callCount := atomic.Int32{}
		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				n := callCount.Add(1)
				if n == 1 {
					return &llm.ChatResponse{
						Content:      "partial",
						FinishReason: "tool_calls",
						ToolCalls: []llm.ToolCall{{
							ID:   "pulse-call",
							Type: "function",
							Function: llm.ToolCallFunction{
								Name:      "think",
								Arguments: `{"thought": "pulse test"}`,
							},
						}},
					}, nil
				}
				return simpleStopResponse("done"), nil
			},
		}

		var workingStates []bool
		var mu sync.Mutex
		loop := NewLoop(LoopConfig{
			Provider: provider,
			Tools:    CreateTools(ToolConfig{}),
			OnWorking: func(working bool) {
				mu.Lock()
				workingStates = append(workingStates, working)
				mu.Unlock()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		runLoopForDuration(t, loop, 500*time.Millisecond)

		mu.Lock()
		states := append([]bool(nil), workingStates...)
		mu.Unlock()

		assert.Equal(t, []bool{true, true, true, false}, states)
	})

	t.Run("accumulates token usage", func(t *testing.T) {
		t.Parallel()

		done := make(chan struct{}, 1) // buffered to prevent blocking
		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				defer func() {
					select {
					case done <- struct{}{}:
					default:
					}
				}()
				return &llm.ChatResponse{
					Content:      "response",
					FinishReason: "stop",
					Usage: llm.Usage{
						PromptTokens:     10,
						CompletionTokens: 20,
						TotalTokens:      30,
					},
				}, nil
			},
		}

		loop := NewLoop(LoopConfig{Provider: provider})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go func() { _ = loop.Go(ctx) }()

		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
			t.Fatal("timeout waiting for LLM call")
		}

		require.Eventually(t, func() bool {
			loop.mu.Lock()
			defer loop.mu.Unlock()
			return loop.totalUsage.TotalTokens > 0
		}, 5*time.Second, 10*time.Millisecond)

		loop.mu.Lock()
		usage := loop.totalUsage
		loop.mu.Unlock()

		assert.Equal(t, 10, usage.PromptTokens)
		assert.Equal(t, 20, usage.CompletionTokens)
		assert.Equal(t, 30, usage.TotalTokens)
	})

	t.Run("retries transient failures without losing the turn", func(t *testing.T) {
		t.Parallel()

		var callCount atomic.Int32
		requestCh := make(chan *llm.ChatRequest, 2)
		var recorded []Message
		var recordMu sync.Mutex

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
				select {
				case requestCh <- req:
				default:
				}
				if callCount.Add(1) == 1 {
					return nil, llm.WrapError("openrouter", fmt.Errorf("failed to decode response: %w", context.Canceled))
				}
				return simpleStopResponse("recovered"), nil
			},
		}

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test-model",
			RecordMessage: func(_ context.Context, msg Message) {
				recordMu.Lock()
				recorded = append(recorded, msg)
				recordMu.Unlock()
			},
			OnTurnComplete: cancel,
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})

		err := loop.Go(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(2), callCount.Load())

		firstReq := waitForRequest(t, requestCh, 200*time.Millisecond)
		secondReq := waitForRequest(t, requestCh, 200*time.Millisecond)
		require.Len(t, firstReq.Messages, 1)
		require.Len(t, secondReq.Messages, 1)
		assert.Equal(t, "hello", firstReq.Messages[0].Content)
		assert.Equal(t, "hello", secondReq.Messages[0].Content)

		recordMu.Lock()
		msgs := append([]Message(nil), recorded...)
		recordMu.Unlock()
		require.Len(t, msgs, 1)
		assert.Equal(t, MessageTypeAssistant, msgs[0].Type)
		assert.Equal(t, "recovered", msgs[0].Content)
	})

	t.Run("records one terminal error after retry exhaustion", func(t *testing.T) {
		t.Parallel()

		var callCount atomic.Int32
		var recorded []Message
		var recordMu sync.Mutex

		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				callCount.Add(1)
				return nil, llm.WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF))
			},
		}

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test-model",
			RecordMessage: func(_ context.Context, msg Message) {
				recordMu.Lock()
				recorded = append(recorded, msg)
				recordMu.Unlock()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})

		runLoopForDuration(t, loop, 3500*time.Millisecond)

		assert.Equal(t, int32(3), callCount.Load())
		recordMu.Lock()
		msgs := append([]Message(nil), recorded...)
		recordMu.Unlock()
		require.Len(t, msgs, 1)
		assert.Equal(t, MessageTypeError, msgs[0].Type)
		assert.Contains(t, msgs[0].Content, "LLM request failed")
	})
}

func TestLoop_InterruptAfterLLMResponseSkipsToolExecution(t *testing.T) {
	t.Parallel()

	var (
		callCount         atomic.Int32
		turnCompleteCount atomic.Int32
		recordMu          sync.Mutex
		recorded          []Message
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once

	loop := NewLoop(LoopConfig{
		Provider: &mockLLMProvider{
			chatFunc: func(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				callCount.Add(1)
				enteredOnce.Do(func() { close(entered) })
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return &llm.ChatResponse{
						ToolCalls: []llm.ToolCall{{
							ID:   "call-1",
							Type: "function",
							Function: llm.ToolCallFunction{
								Name:      "think",
								Arguments: `{"thought":"test"}`,
							},
						}},
					}, nil
				}
			},
		},
		Tools:     CreateTools(ToolConfig{}),
		SessionID: "sess-1",
		RecordMessage: func(_ context.Context, msg Message) {
			recordMu.Lock()
			recorded = append(recorded, msg)
			recordMu.Unlock()
		},
		OnTurnComplete: func() {
			turnCompleteCount.Add(1)
		},
	})
	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "run tool"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Go(ctx) }()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first LLM request")
	}

	loop.RequestInterrupt()
	close(release)

	require.Eventually(t, func() bool {
		recordMu.Lock()
		defer recordMu.Unlock()
		return len(recorded) >= 2
	}, time.Second, 10*time.Millisecond)

	cancel()
	<-done

	recordMu.Lock()
	msgs := append([]Message(nil), recorded...)
	recordMu.Unlock()
	require.Len(t, msgs, 2)
	assert.Equal(t, MessageTypeAssistant, msgs[0].Type)
	require.Len(t, msgs[0].ToolCalls, 1)
	assert.Equal(t, MessageTypeUser, msgs[1].Type)
	require.Len(t, msgs[1].ToolResults, 1)
	assert.Equal(t, "Tool execution was cancelled.", msgs[1].ToolResults[0].Content)
	assert.Equal(t, int32(1), callCount.Load(), "interrupt should stop before the follow-up LLM request")
	assert.Equal(t, int32(0), turnCompleteCount.Load(), "interrupted turns must not report completion")
}

func TestLoop_InterruptAfterCompletedReplyStillDeliversAssistant(t *testing.T) {
	t.Parallel()

	var (
		callCount         atomic.Int32
		turnCompleteCount atomic.Int32
		recordMu          sync.Mutex
		recorded          []Message
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once

	loop := NewLoop(LoopConfig{
		Provider: &mockLLMProvider{
			chatFunc: func(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				callCount.Add(1)
				enteredOnce.Do(func() { close(entered) })
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return simpleStopResponse("done"), nil
				}
			},
		},
		SessionID: "sess-1",
		RecordMessage: func(_ context.Context, msg Message) {
			recordMu.Lock()
			recorded = append(recorded, msg)
			recordMu.Unlock()
		},
		OnTurnComplete: func() {
			turnCompleteCount.Add(1)
		},
	})
	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "say hi"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Go(ctx) }()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first LLM request")
	}

	loop.RequestInterrupt()
	close(release)

	require.Eventually(t, func() bool {
		recordMu.Lock()
		defer recordMu.Unlock()
		return len(recorded) >= 1
	}, time.Second, 10*time.Millisecond)

	cancel()
	<-done

	recordMu.Lock()
	msgs := append([]Message(nil), recorded...)
	recordMu.Unlock()
	require.Len(t, msgs, 1)
	assert.Equal(t, MessageTypeAssistant, msgs[0].Type)
	assert.Equal(t, "done", msgs[0].Content)
	assert.Equal(t, int32(1), callCount.Load())
	assert.Equal(t, int32(0), turnCompleteCount.Load(), "completed reply from an interrupted turn should not count as a clean turn completion")
}

func TestLoop_ExecuteTool(t *testing.T) {
	t.Parallel()

	t.Run("executes known tool", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			Tools:    CreateTools(ToolConfig{}),
		})

		result := loop.executeTool(context.Background(), llm.ToolCall{
			ID:   "test-id",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "think",
				Arguments: `{"thought": "testing"}`,
			},
		})

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "Thought recorded")
	})

	t.Run("returns error for unknown tool", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			Tools:    CreateTools(ToolConfig{}),
		})

		result := loop.executeTool(context.Background(), llm.ToolCall{
			ID:   "test-id",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "unknown_tool",
				Arguments: `{}`,
			},
		})

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "not found")
	})

	t.Run("handles empty arguments", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			Tools:    CreateTools(ToolConfig{}),
		})

		result := loop.executeTool(context.Background(), llm.ToolCall{
			ID:   "test-id",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "think",
				Arguments: "",
			},
		})

		assert.False(t, result.IsError)
	})

	t.Run("invokes after hook", func(t *testing.T) {
		t.Parallel()

		hooks := NewHooks()
		var capturedInfo ToolExecInfo
		var capturedResult ToolOut

		hooks.OnAfterToolExec(func(_ context.Context, info ToolExecInfo, result ToolOut) {
			capturedInfo = info
			capturedResult = result
		})

		loop := NewLoop(LoopConfig{
			Provider:  &mockLLMProvider{},
			Tools:     CreateTools(ToolConfig{}),
			SessionID: "conv-hook",
			User: UserIdentity{
				UserID:    "user-1",
				Username:  "alice",
				IPAddress: "10.0.0.1",
				Role:      auth.RoleManager,
			},
			Hooks: hooks,
		})

		result := loop.executeTool(context.Background(), llm.ToolCall{
			ID:   "test-id",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "think",
				Arguments: `{"thought": "testing hooks"}`,
			},
		})

		assert.False(t, result.IsError)
		assert.Equal(t, "think", capturedInfo.ToolName)
		assert.Equal(t, "conv-hook", capturedInfo.SessionID)
		assert.Equal(t, "user-1", capturedInfo.User.UserID)
		assert.Equal(t, "alice", capturedInfo.User.Username)
		assert.Equal(t, "10.0.0.1", capturedInfo.User.IPAddress)
		assert.Equal(t, auth.RoleManager, capturedInfo.User.Role)
		assert.Equal(t, result.Content, capturedResult.Content)
		// think tool has nil Audit (not audited)
		assert.Nil(t, capturedInfo.Audit)
	})

	t.Run("populates Audit from tool", func(t *testing.T) {
		t.Parallel()

		hooks := NewHooks()
		var capturedInfo ToolExecInfo

		hooks.OnAfterToolExec(func(_ context.Context, info ToolExecInfo, _ ToolOut) {
			capturedInfo = info
		})

		auditInfo := &AuditInfo{
			Action: "test_action",
			DetailExtractor: func(input json.RawMessage) map[string]any {
				return map[string]any{"raw": string(input)}
			},
		}

		customTool := &AgentTool{
			Tool: llm.Tool{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        "audited_tool",
					Description: "A tool with audit info",
					Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
				},
			},
			Run: func(_ ToolContext, _ json.RawMessage) ToolOut {
				return ToolOut{Content: "ok"}
			},
			Audit: auditInfo,
		}

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			Tools:    []*AgentTool{customTool},
			Hooks:    hooks,
		})

		loop.executeTool(context.Background(), llm.ToolCall{
			ID:   "test-id",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "audited_tool",
				Arguments: `{"key":"val"}`,
			},
		})

		require.NotNil(t, capturedInfo.Audit)
		assert.Equal(t, "test_action", capturedInfo.Audit.Action)
		details := capturedInfo.Audit.DetailExtractor(capturedInfo.Input)
		assert.Equal(t, `{"key":"val"}`, details["raw"])
	})

	t.Run("before hook blocks execution", func(t *testing.T) {
		t.Parallel()

		hooks := NewHooks()
		hooks.OnBeforeToolExec(func(_ context.Context, _ ToolExecInfo) error {
			return errors.New("forbidden")
		})

		toolCalled := false
		hooks.OnAfterToolExec(func(_ context.Context, _ ToolExecInfo, _ ToolOut) {
			toolCalled = true
		})

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			Tools:    CreateTools(ToolConfig{}),
			Hooks:    hooks,
		})

		result := loop.executeTool(context.Background(), llm.ToolCall{
			ID:   "test-id",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "think",
				Arguments: `{"thought": "should not run"}`,
			},
		})

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "Blocked by policy")
		assert.Contains(t, result.Content, "forbidden")
		assert.False(t, toolCalled, "after hook should not be called when before hook blocks")
	})
}

func TestLoop_BuildMessages(t *testing.T) {
	t.Parallel()

	t.Run("without system prompt", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider:     &mockLLMProvider{},
			SystemPrompt: "",
		})

		messages := loop.buildMessages(context.Background(), []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
		})

		assert.Len(t, messages, 1)
		assert.Equal(t, llm.RoleUser, messages[0].Role)
	})

	t.Run("with system prompt", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider:     &mockLLMProvider{},
			SystemPrompt: "Be helpful.",
		})

		messages := loop.buildMessages(context.Background(), []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
		})

		assert.Len(t, messages, 2)
		assert.Equal(t, llm.RoleSystem, messages[0].Role)
		assert.Equal(t, "Be helpful.", messages[0].Content)
		assert.Equal(t, llm.RoleUser, messages[1].Role)
	})

	t.Run("with dynamic system context", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider:     &mockLLMProvider{},
			SystemPrompt: "Be helpful.",
			DynamicSystemContext: func(context.Context) string {
				return "<recent_gateway_events>\n- dag: briefing\n  run_id: run-1\n</recent_gateway_events>"
			},
		})

		messages := loop.buildMessages(context.Background(), []llm.Message{
			{Role: llm.RoleUser, Content: "rerun it"},
		})

		require.Len(t, messages, 2)
		assert.Equal(t, llm.RoleSystem, messages[0].Role)
		assert.Contains(t, messages[0].Content, "Be helpful.")
		assert.Contains(t, messages[0].Content, "<recent_gateway_events>")
		assert.Contains(t, messages[0].Content, "dag: briefing")
		assert.Equal(t, llm.RoleUser, messages[1].Role)
	})

	t.Run("dynamic system context without base prompt", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			DynamicSystemContext: func(context.Context) string {
				return "<recent_gateway_events>\n- dag: briefing\n  run_id: run-1\n</recent_gateway_events>"
			},
		})

		messages := loop.buildMessages(context.Background(), []llm.Message{
			{Role: llm.RoleUser, Content: "rerun it"},
		})

		require.Len(t, messages, 2)
		assert.Equal(t, llm.RoleSystem, messages[0].Role)
		assert.Contains(t, messages[0].Content, "<recent_gateway_events>")
		assert.Equal(t, llm.RoleUser, messages[1].Role)
	})
}

func TestLoop_BuildToolDefinitions(t *testing.T) {
	t.Parallel()

	t.Run("converts agent tools to LLM tools", func(t *testing.T) {
		t.Parallel()

		loop := NewLoop(LoopConfig{
			Provider: &mockLLMProvider{},
			Tools:    CreateTools(ToolConfig{}),
		})

		tools := loop.buildToolDefinitions()

		assert.Len(t, tools, len(loop.tools))
		for _, tool := range tools {
			assert.Equal(t, "function", tool.Type)
			assert.NotEmpty(t, tool.Function.Name)
		}
	})
}

func TestStartHeartbeatPump(t *testing.T) {
	t.Parallel()

	var beats atomic.Int32
	ctx := t.Context()

	stop := startHeartbeatPump(ctx, 10*time.Millisecond, func() {
		beats.Add(1)
	})
	defer stop()

	require.Eventually(t, func() bool {
		return beats.Load() >= 2
	}, 200*time.Millisecond, 10*time.Millisecond)
}

// Test helpers

func waitForRequest(t *testing.T, requestCh <-chan *llm.ChatRequest, timeout time.Duration) *llm.ChatRequest {
	t.Helper()
	select {
	case req := <-requestCh:
		require.NotNil(t, req)
		return req
	case <-time.After(timeout):
		t.Fatal("timeout waiting for LLM request")
		return nil
	}
}

func runLoopForDuration(t *testing.T, loop *Loop, duration time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = loop.Go(ctx)
	}()

	select {
	case <-done:
	case <-time.After(duration + 5*time.Second):
		t.Fatal("runLoopForDuration: loop did not finish within timeout")
	}
}

// newEchoTool creates a simple tool that returns its input as output.
func newEchoTool(name string) *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        name,
				Description: "echoes input",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		Run: func(_ ToolContext, input json.RawMessage) ToolOut {
			return ToolOut{Content: "echo: " + string(input)}
		},
	}
}

func waitForLoopDone(ctx context.Context, cancel context.CancelFunc, loop *Loop, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- loop.Go(ctx) }()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		cancel()
		return <-done
	}
}

func TestLoop_ToolCallFlow(t *testing.T) {
	t.Parallel()

	var recorded []Message
	var mu sync.Mutex

	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "tc-1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "echo",
					Arguments: `{"msg":"test"}`,
				},
			}},
		},
		&llm.ChatResponse{Content: "final answer", FinishReason: "stop"},
	)

	ctx, cancel := context.WithCancel(context.Background())
	loop := NewLoop(LoopConfig{
		Provider: provider,
		Model:    "test",
		Tools:    []*AgentTool{newEchoTool("echo")},
		RecordMessage: func(_ context.Context, msg Message) {
			mu.Lock()
			recorded = append(recorded, msg)
			mu.Unlock()
		},
		OnTurnComplete: func() { cancel() },
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "run tool"})

	err := waitForLoopDone(ctx, cancel, loop, 5*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	mu.Lock()
	defer mu.Unlock()

	// Should have: assistant (tool_calls), tool result, assistant (final)
	require.GreaterOrEqual(t, len(recorded), 3)
	assert.Equal(t, MessageTypeAssistant, recorded[0].Type)
	assert.NotEmpty(t, recorded[0].ToolCalls)
	assert.Equal(t, MessageTypeUser, recorded[1].Type)
	require.Len(t, recorded[1].ToolResults, 1)
	assert.Equal(t, "tc-1", recorded[1].ToolResults[0].ToolCallID)
	assert.Equal(t, MessageTypeAssistant, recorded[2].Type)
	assert.Equal(t, "final answer", recorded[2].Content)
}

func TestLoop_DelegateToolIsolation(t *testing.T) {
	t.Parallel()

	store := newMockSessionStore()
	parentID := "parent-sess"

	require.NoError(t, store.CreateSession(context.Background(), &Session{
		ID: parentID, UserID: "user1",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "del-1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "delegate",
					Arguments: `{"tasks": [{"task": "sub task"}]}`,
				},
			}},
		},
		&llm.ChatResponse{Content: "parent done", FinishReason: "stop"},
	)

	var parentMessages []Message
	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	loop := NewLoop(LoopConfig{
		Provider:     provider,
		Model:        "test",
		Tools:        []*AgentTool{NewDelegateTool(), newEchoTool("echo")},
		SessionID:    parentID,
		SessionStore: store,
		User:         UserIdentity{UserID: "user1"},
		Registry:     newMockSubSessionRegistry(),
		RecordMessage: func(_ context.Context, msg Message) {
			mu.Lock()
			parentMessages = append(parentMessages, msg)
			mu.Unlock()
		},
		OnTurnComplete: func() { cancel() },
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "delegate this"})

	err := waitForLoopDone(ctx, cancel, loop, 10*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	// Verify sub-session was created
	store.mu.Lock()
	var subSessions []*Session
	for _, s := range store.sessions {
		if s.ParentSessionID == parentID {
			subSessions = append(subSessions, s)
		}
	}
	store.mu.Unlock()

	require.Len(t, subSessions, 1)
	assert.Equal(t, parentID, subSessions[0].ParentSessionID)
	assert.Equal(t, "sub task", subSessions[0].DelegateTask)

	// Verify parent messages contain delegate tool result with DelegateIDs
	mu.Lock()
	defer mu.Unlock()
	var hasDelegateResult bool
	for _, msg := range parentMessages {
		if len(msg.ToolResults) > 0 && len(msg.DelegateIDs) > 0 {
			hasDelegateResult = true
		}
	}
	assert.True(t, hasDelegateResult, "parent should have a delegate tool result with DelegateIDs")
}

func TestLoop_RecordMessageError(t *testing.T) {
	t.Parallel()

	provider := newStopProvider("hello")
	ctx, cancel := context.WithCancel(context.Background())

	loop := NewLoop(LoopConfig{
		Provider:       provider,
		Model:          "test",
		RecordMessage:  func(_ context.Context, _ Message) {},
		OnTurnComplete: func() { cancel() },
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

	err := waitForLoopDone(ctx, cancel, loop, 5*time.Second)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestLoop_BatchedDelegates(t *testing.T) {
	t.Parallel()

	store := newMockSessionStore()
	parentID := "parent-batched"

	require.NoError(t, store.CreateSession(context.Background(), &Session{
		ID: parentID, UserID: "user1",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "d1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "delegate",
					Arguments: `{"tasks":[{"task":"task-1"},{"task":"task-2"},{"task":"task-3"}]}`,
				},
			}},
		},
		&llm.ChatResponse{Content: "all done", FinishReason: "stop"},
	)

	ctx, cancel := context.WithCancel(context.Background())

	loop := NewLoop(LoopConfig{
		Provider:       provider,
		Model:          "test",
		Tools:          []*AgentTool{NewDelegateTool()},
		SessionID:      parentID,
		SessionStore:   store,
		User:           UserIdentity{UserID: "user1"},
		Registry:       newMockSubSessionRegistry(),
		OnTurnComplete: func() { cancel() },
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "do 3 things"})

	err := waitForLoopDone(ctx, cancel, loop, 15*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	store.mu.Lock()
	var subCount int
	for _, s := range store.sessions {
		if s.ParentSessionID == parentID {
			subCount++
		}
	}
	store.mu.Unlock()

	assert.Equal(t, 3, subCount, "should have 3 sub-sessions from batched delegate call")
}

func TestLoop_BatchedDelegateExceedsMax(t *testing.T) {
	t.Parallel()

	store := newMockSessionStore()
	parentID := "parent-exceed"

	require.NoError(t, store.CreateSession(context.Background(), &Session{
		ID: parentID, UserID: "user1",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	// Build a batched delegate call with 12 tasks (exceeds max of 8)
	var tasks []string
	for i := range 12 {
		tasks = append(tasks, fmt.Sprintf(`{"task":"task-%d"}`, i))
	}
	tasksJSON := "[" + strings.Join(tasks, ",") + "]"

	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "d1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "delegate",
					Arguments: fmt.Sprintf(`{"tasks":%s}`, tasksJSON),
				},
			}},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)

	ctx, cancel := context.WithCancel(context.Background())

	loop := NewLoop(LoopConfig{
		Provider:       provider,
		Model:          "test",
		Tools:          []*AgentTool{NewDelegateTool()},
		SessionID:      parentID,
		SessionStore:   store,
		User:           UserIdentity{UserID: "user1"},
		Registry:       newMockSubSessionRegistry(),
		OnTurnComplete: func() { cancel() },
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "do 12 things"})

	err := waitForLoopDone(ctx, cancel, loop, 10*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	store.mu.Lock()
	var subCount int
	for _, s := range store.sessions {
		if s.ParentSessionID == parentID {
			subCount++
		}
	}
	store.mu.Unlock()

	// Delegate tool caps at maxConcurrentDelegates internally
	assert.Equal(t, maxConcurrentDelegates, subCount, "should have exactly maxConcurrentDelegates sub-sessions")
}

func TestLoop_ExecuteTool_PassesSubSessionRegistry(t *testing.T) {
	t.Parallel()

	registry := newMockSubSessionRegistry()

	store := newMockSessionStore()
	require.NoError(t, store.CreateSession(context.Background(), &Session{
		ID: "parent-cb", UserID: "user1",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))

	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "del-cb",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "delegate",
					Arguments: `{"tasks": [{"task": "callback test"}]}`,
				},
			}},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)

	ctx, cancel := context.WithCancel(context.Background())

	loop := NewLoop(LoopConfig{
		Provider:     provider,
		Model:        "test",
		Tools:        []*AgentTool{NewDelegateTool()},
		SessionID:    "parent-cb",
		SessionStore: store,
		User:         UserIdentity{UserID: "user1"},
		Registry:     registry,
		OnWorking: func(working bool) {
			if !working {
				cancel()
			}
		},
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test callbacks"})

	err := waitForLoopDone(ctx, cancel, loop, 15*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	registry.mu.Lock()
	defer registry.mu.Unlock()
	assert.NotEmpty(t, registry.registered, "RegisterSubSession should be called during delegate execution")
	assert.NotEmpty(t, registry.events, "NotifyParent should be called during delegate execution")
}

func TestLoop_DelegateContextNilWhenNoRegistry(t *testing.T) {
	t.Parallel()

	// When registry is nil, the delegate tool should get no DelegateContext and return "not available".
	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "del-nil",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "delegate",
					Arguments: `{"tasks": [{"task": "no registry"}]}`,
				},
			}},
		},
		&llm.ChatResponse{Content: "recovered", FinishReason: "stop"},
	)

	ctx, cancel := context.WithCancel(context.Background())

	loop := NewLoop(LoopConfig{
		Provider: provider,
		Model:    "test",
		Tools:    []*AgentTool{NewDelegateTool()},
		// No Registry set
		OnWorking: func(working bool) {
			if !working {
				cancel()
			}
		},
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "try delegate"})

	err := waitForLoopDone(ctx, cancel, loop, 10*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	// The loop should have recovered: the delegate tool returned an error, but the LLM responded with "recovered".
	var history []llm.Message
	loop.mu.Lock()
	history = append(history, loop.history...)
	loop.mu.Unlock()

	var hasToolResult bool
	for _, msg := range history {
		if msg.Role == llm.RoleTool {
			hasToolResult = true
			assert.Contains(t, msg.Content, "not available",
				"delegate tool result should indicate it's not available without a registry")
		}
	}
	assert.True(t, hasToolResult, "loop should have a tool result in history")
}

func TestLoop_DelegateContextOnlyForDelegateTool(t *testing.T) {
	t.Parallel()

	// When registry IS set, non-delegate tools should still work normally.
	registry := newMockSubSessionRegistry()

	var mu sync.Mutex
	var executedTools []string

	thinkTool := &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "think",
				Description: "Think about something",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{"thought": map[string]any{"type": "string"}}},
			},
		},
		Run: func(_ ToolContext, _ json.RawMessage) ToolOut {
			mu.Lock()
			executedTools = append(executedTools, "think")
			mu.Unlock()
			return ToolOut{Content: "thought recorded"}
		},
	}

	provider := newSequenceProvider(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:   "think-1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "think",
					Arguments: `{"thought": "testing non-delegate"}`,
				},
			}},
		},
		&llm.ChatResponse{Content: "done thinking", FinishReason: "stop"},
	)

	ctx, cancel := context.WithCancel(context.Background())

	loop := NewLoop(LoopConfig{
		Provider: provider,
		Model:    "test",
		Tools:    []*AgentTool{NewDelegateTool(), thinkTool},
		Registry: registry,
		OnWorking: func(working bool) {
			if !working {
				cancel()
			}
		},
	})

	loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "think about it"})

	err := waitForLoopDone(ctx, cancel, loop, 10*time.Second)
	assert.ErrorIs(t, err, context.Canceled)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, executedTools, "think", "non-delegate tool should execute normally")

	// No sub-sessions should have been registered since we only used the think tool.
	registry.mu.Lock()
	defer registry.mu.Unlock()
	assert.Empty(t, registry.registered, "no sub-sessions should be registered for non-delegate tools")
}

func TestLoop_OnTurnComplete(t *testing.T) {
	t.Parallel()

	t.Run("fires after successful turn and loop exits", func(t *testing.T) {
		t.Parallel()

		provider := newStopProvider("hello from LLM")

		var turnCompleted atomic.Int32

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test",
			OnTurnComplete: func() {
				turnCompleted.Add(1)
				cancel()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		err := loop.Go(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), turnCompleted.Load(),
			"OnTurnComplete should fire exactly once after the LLM responds")
	})

	t.Run("fires after tool calls complete", func(t *testing.T) {
		t.Parallel()

		provider := newSequenceProvider(
			&llm.ChatResponse{
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{{
					ID:   "tc-1",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "think",
						Arguments: `{"thought": "thinking"}`,
					},
				}},
			},
			&llm.ChatResponse{Content: "done", FinishReason: "stop"},
		)

		var turnCompleted atomic.Int32

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test",
			Tools:    CreateTools(ToolConfig{}),
			OnTurnComplete: func() {
				turnCompleted.Add(1)
				cancel()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "use a tool"})

		err := loop.Go(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), turnCompleted.Load(),
			"OnTurnComplete should fire once after tool calls + final response")
	})

	t.Run("does not fire on LLM error", func(t *testing.T) {
		t.Parallel()

		callCount := atomic.Int32{}
		provider := &mockLLMProvider{
			chatFunc: func(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				callCount.Add(1)
				return nil, fmt.Errorf("API error")
			},
		}

		var turnCompleted atomic.Int32

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test",
			OnTurnComplete: func() {
				turnCompleted.Add(1)
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "fail"})

		_ = loop.Go(ctx)

		assert.Equal(t, int32(0), turnCompleted.Load(),
			"OnTurnComplete should not fire when LLM request fails")
		assert.GreaterOrEqual(t, callCount.Load(), int32(1),
			"provider should have been called at least once")
	})

	t.Run("loop exits promptly without idle polling", func(t *testing.T) {
		t.Parallel()

		provider := newStopProvider("quick response")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		loop := NewLoop(LoopConfig{
			Provider: provider,
			Model:    "test",
			OnTurnComplete: func() {
				cancel()
			},
		})
		loop.QueueUserMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

		start := time.Now()
		err := loop.Go(ctx)
		elapsed := time.Since(start)

		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, elapsed, 2*time.Second,
			"loop should exit promptly via OnTurnComplete, not wait for idle timeout")
	})
}
