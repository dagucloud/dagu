// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSessionManager(t *testing.T) {
	t.Parallel()

	t.Run("generates ID if not provided", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		assert.NotEmpty(t, sm.ID())
	})

	t.Run("uses provided ID", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "custom-id"})
		assert.Equal(t, "custom-id", sm.ID())
	})

	t.Run("stores user ID", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{User: UserIdentity{UserID: "user-123"}})
		assert.Equal(t, "user-123", sm.UserID())
	})

	t.Run("copies history messages", func(t *testing.T) {
		t.Parallel()

		history := []Message{
			{ID: "1", Content: "first"},
			{ID: "2", Content: "second"},
		}

		sm := NewSessionManager(SessionManagerConfig{
			History: history,
		})

		msgs := sm.GetMessages()
		assert.Len(t, msgs, 2)
		assert.Equal(t, "first", msgs[0].Content)
		assert.Equal(t, "second", msgs[1].Content)
	})
}

func TestNewSessionManager_ReconstructsTotalCostFromHistory(t *testing.T) {
	t.Parallel()

	cost1, cost2 := 0.05, 0.03
	sm := NewSessionManager(SessionManagerConfig{
		History: []Message{
			{Type: MessageTypeUser, Content: "hello"},
			{Type: MessageTypeAssistant, Cost: &cost1},
			{Type: MessageTypeAssistant, Cost: &cost2},
		},
	})
	assert.InDelta(t, 0.08, sm.GetTotalCost(), 1e-9)
}

func TestNewSessionManager_ReconstructsTotalCostIncludingDelegates(t *testing.T) {
	t.Parallel()

	msgCost := 0.05
	sm := NewSessionManager(SessionManagerConfig{
		History: []Message{
			{Type: MessageTypeAssistant, Cost: &msgCost},
		},
		Delegates: []DelegateSnapshot{
			{ID: "del-1", Cost: 0.03},
			{ID: "del-2", Cost: 0.02},
		},
	})
	// 0.05 (message) + 0.03 + 0.02 (delegates) = 0.10
	assert.InDelta(t, 0.10, sm.GetTotalCost(), 1e-9)
}

func TestSessionManager_SetWorking(t *testing.T) {
	t.Parallel()

	t.Run("updates working state", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "test"})

		assert.False(t, sm.IsWorking())

		sm.SetWorking(true)
		assert.True(t, sm.IsWorking())

		sm.SetWorking(false)
		assert.False(t, sm.IsWorking())
	})

	t.Run("calls callback on state change", func(t *testing.T) {
		t.Parallel()

		type workingChange struct {
			id      string
			working bool
		}

		var mu sync.Mutex
		var callbackCalls []workingChange

		sm := NewSessionManager(SessionManagerConfig{
			ID: "test-sess",
			OnWorkingChange: func(id string, working bool) {
				mu.Lock()
				callbackCalls = append(callbackCalls, workingChange{id, working})
				mu.Unlock()
			},
		})

		sm.SetWorking(true)
		sm.SetWorking(true)
		sm.SetWorking(false)

		mu.Lock()
		calls := append([]workingChange{}, callbackCalls...)
		mu.Unlock()

		require.Len(t, calls, 2)
		assert.True(t, calls[0].working)
		assert.False(t, calls[1].working)
		assert.Equal(t, "test-sess", calls[0].id)
	})

	t.Run("broadcasts to subscribers", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "test"})
		ctx := t.Context()
		next := sm.Subscribe(ctx)

		sm.SetWorking(true)

		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, ok := next()
			if ok && resp.SessionState != nil {
				assert.True(t, resp.SessionState.Working)
			}
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for broadcast")
		}
	})

	t.Run("broadcasts repeated working pulses while already working", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "test"})
		ctx := t.Context()
		next := sm.Subscribe(ctx)

		sm.SetWorking(true)
		first, ok := next()
		require.True(t, ok)
		require.NotNil(t, first.SessionState)
		assert.True(t, first.SessionState.Working)

		sm.SetWorking(true)
		second, ok := next()
		require.True(t, ok)
		require.NotNil(t, second.SessionState)
		assert.True(t, second.SessionState.Working)
	})
}

func TestSessionManager_AcceptUserMessage(t *testing.T) {
	t.Parallel()

	t.Run("requires provider", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		err := sm.AcceptUserMessage(context.Background(), nil, "config-id", "provider-model", "hello")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "required")
	})

	t.Run("starts loop and queues message", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		provider := &mockLLMProvider{
			chatFunc: func(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				select {
				case <-entered:
				default:
					close(entered)
				}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}

		sm := NewSessionManager(SessionManagerConfig{})
		err := sm.AcceptUserMessage(context.Background(), provider, "config-id", "provider-model", "hello")

		require.NoError(t, err)
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for active turn")
		}
		require.Eventually(t, func() bool {
			return sm.IsWorking()
		}, time.Second, 10*time.Millisecond)

		_ = sm.Cancel(context.Background())
	})

	t.Run("adds message to session", func(t *testing.T) {
		t.Parallel()

		provider := newStopProvider("response")

		sm := NewSessionManager(SessionManagerConfig{})
		_ = sm.AcceptUserMessage(context.Background(), provider, "config-id", "provider-model", "hello")

		require.Eventually(t, func() bool {
			return len(sm.GetMessages()) > 0
		}, 5*time.Second, 10*time.Millisecond)

		msgs := sm.GetMessages()
		require.NotEmpty(t, msgs)
		assert.Equal(t, MessageTypeUser, msgs[0].Type)
		assert.Equal(t, "hello", msgs[0].Content)

		_ = sm.Cancel(context.Background())
	})
}

func TestSessionManager_EnqueueChatMessage_MergesWhileWorking(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	provider := &mockLLMProvider{
		chatFunc: func(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
			select {
			case <-entered:
			default:
				close(entered)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return simpleStopResponse("done"), nil
			}
		},
	}

	sm := NewSessionManager(SessionManagerConfig{})
	require.NoError(t, sm.AcceptUserMessage(context.Background(), provider, "config-id", "provider-model", "first"))

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active turn")
	}

	queued, err := sm.EnqueueChatMessageWithLLMContent(context.Background(), provider, "config-id", "provider-model", "second", "second llm")
	require.NoError(t, err)
	assert.True(t, queued)

	queued, err = sm.EnqueueChatMessageWithLLMContent(context.Background(), provider, "config-id", "provider-model", "third", "third llm")
	require.NoError(t, err)
	assert.True(t, queued)

	msgs := sm.GetMessages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "first", msgs[0].Content)
	assert.True(t, sm.HasQueuedChatInput())

	text, llmText, ok := sm.BeginQueuedChatFlush()
	require.True(t, ok)
	assert.Equal(t, "second\n\nthird", text)
	assert.Equal(t, "second llm\n\nthird llm", llmText)
	assert.True(t, sm.HasQueuedChatInput(), "flush marker should keep queued state visible")

	sm.RestoreQueuedChatInput(text, llmText)
	assert.True(t, sm.HasQueuedChatInput())

	text, llmText, ok = sm.BeginQueuedChatFlush()
	require.True(t, ok)
	assert.Equal(t, "second\n\nthird", text)
	assert.Equal(t, "second llm\n\nthird llm", llmText)
	sm.CompleteQueuedChatFlush()
	assert.False(t, sm.HasQueuedChatInput())

	close(release)
	_ = sm.Cancel(context.Background())
}

func TestSessionManager_Subscribe(t *testing.T) {
	t.Parallel()

	t.Run("receives messages", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "test"})

		ctx := t.Context()

		next := sm.Subscribe(ctx)

		sm.SetWorking(true)

		resp, ok := next()
		assert.True(t, ok)
		assert.NotNil(t, resp.SessionState)
	})
}

func TestSessionManager_SubscribeWithSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("returns initial snapshot", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			ID:   "test",
			User: UserIdentity{UserID: "user1"},
			History: []Message{
				{ID: "1", Content: "first"},
			},
		})

		ctx := t.Context()

		snapshot, _ := sm.SubscribeWithSnapshot(ctx)

		assert.NotNil(t, snapshot.Session)
		assert.Equal(t, "test", snapshot.Session.ID)
		assert.NotNil(t, snapshot.SessionState)
		assert.Len(t, snapshot.Messages, 1)
	})
}

func TestSessionManager_Snapshot(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(SessionManagerConfig{
		ID:   "snapshot-test",
		User: UserIdentity{UserID: "user1"},
		History: []Message{
			{ID: "1", Content: "first"},
		},
	})
	sm.SetWorking(true)
	sm.SetDelegateStarted("delegate-1", "summarize")
	sm.promptsMu.Lock()
	sm.pendingPrompts["prompt-1"] = make(chan UserPromptResponse, 1)
	sm.promptsMu.Unlock()

	snapshot := sm.Snapshot()

	require.Len(t, snapshot.Messages, 1)
	assert.Equal(t, "snapshot-test", snapshot.Session.ID)
	assert.Equal(t, "user1", snapshot.Session.UserID)
	assert.True(t, snapshot.Working)
	assert.True(t, snapshot.HasPendingPrompt)
	require.Len(t, snapshot.Delegates, 1)

	response := snapshot.StreamResponse()
	require.NotNil(t, response.Session)
	require.NotNil(t, response.SessionState)
	assert.Equal(t, "snapshot-test", response.Session.ID)
	assert.True(t, response.SessionState.Working)
	assert.True(t, response.SessionState.HasPendingPrompt)
	require.Len(t, response.Delegates, 1)
}

func TestSessionManager_Cancel(t *testing.T) {
	t.Parallel()

	t.Run("cancels active loop", func(t *testing.T) {
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

		sm := NewSessionManager(SessionManagerConfig{})
		_ = sm.AcceptUserMessage(context.Background(), provider, "config-id", "provider-model", "hello")

		require.Eventually(t, func() bool {
			return sm.IsWorking()
		}, 5*time.Second, 10*time.Millisecond)

		err := sm.Cancel(context.Background())
		assert.NoError(t, err)
		assert.False(t, sm.IsWorking())
	})

	t.Run("safe to call when no loop", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		err := sm.Cancel(context.Background())
		assert.NoError(t, err)
	})

	t.Run("ignores late working pulse after cancel", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		sm.SetWorking(true)

		err := sm.Cancel(context.Background())
		require.NoError(t, err)
		assert.False(t, sm.IsWorking())

		// Simulate a late callback from the loop goroutine after cancellation.
		sm.SetWorking(true)
		assert.False(t, sm.IsWorking())
	})
}

func TestSessionManager_GetMessages(t *testing.T) {
	t.Parallel()

	t.Run("returns copy of messages", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			History: []Message{
				{ID: "1", Content: "first"},
				{ID: "2", Content: "second"},
			},
		})

		msgs := sm.GetMessages()
		assert.Len(t, msgs, 2)

		msgs[0].Content = "modified"
		originalMsgs := sm.GetMessages()
		assert.Equal(t, "first", originalMsgs[0].Content)
	})
}

func TestSessionManager_GetSession(t *testing.T) {
	t.Parallel()

	t.Run("returns session metadata", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			ID:   "sess-123",
			User: UserIdentity{UserID: "user-456"},
		})

		sess := sm.GetSession()

		assert.Equal(t, "sess-123", sess.ID)
		assert.Equal(t, "user-456", sess.UserID)
		assert.False(t, sess.CreatedAt.IsZero())
	})

	t.Run("uses first user message as title", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			ID:   "sess-title",
			User: UserIdentity{UserID: "user-title"},
		})

		_, err := sm.RecordExternalMessage(context.Background(), Message{
			Type:    MessageTypeUser,
			Content: "find flaky tests and summarize the root cause",
		})
		require.NoError(t, err)

		sess := sm.GetSession()
		assert.Equal(t, "find flaky tests and summarize the root cause", sess.Title)
	})
}

func TestSessionManager_GetModel(t *testing.T) {
	t.Parallel()

	t.Run("returns empty before accept", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		assert.Empty(t, sm.GetModel())
	})

	t.Run("returns model after accept", func(t *testing.T) {
		t.Parallel()

		provider := newStopProvider("hi")

		sm := NewSessionManager(SessionManagerConfig{})
		_ = sm.AcceptUserMessage(context.Background(), provider, "test-model", "test-model", "hello")

		assert.Equal(t, "test-model", sm.GetModel())

		_ = sm.Cancel(context.Background())
	})
}

func TestSessionManager_CostTracking(t *testing.T) {
	t.Parallel()

	pricedConfig := SessionManagerConfig{
		InputCostPer1M:  3.0,
		OutputCostPer1M: 15.0,
	}

	t.Run("calculateCost with usage", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(pricedConfig)
		usage := &llm.Usage{PromptTokens: 1000, CompletionTokens: 500}
		cost := sm.calculateCost(usage)
		assert.InDelta(t, 0.0105, cost, 1e-9)
	})

	t.Run("calculateCost with nil usage returns zero", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(pricedConfig)
		assert.Equal(t, 0.0, sm.calculateCost(nil))
	})

	t.Run("addCost accumulates", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		sm.addCost(0.01)
		sm.addCost(0.01)
		assert.InDelta(t, 0.02, sm.GetTotalCost(), 1e-9)
	})

	t.Run("GetTotalCost starts at zero", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		assert.Equal(t, 0.0, sm.GetTotalCost())
	})

	t.Run("calculateCost with zero pricing returns zero", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		usage := &llm.Usage{PromptTokens: 1000, CompletionTokens: 500}
		assert.Equal(t, 0.0, sm.calculateCost(usage))
	})
}

func TestSessionManager_UpdatePricing(t *testing.T) {
	t.Parallel()

	t.Run("updates pricing for subsequent cost calculations", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})

		usage := &llm.Usage{PromptTokens: 1000, CompletionTokens: 500}
		cost := sm.calculateCost(usage)
		assert.Equal(t, 0.0, cost)

		sm.UpdatePricing(5.0, 25.0)

		cost = sm.calculateCost(usage)
		// (1000 * 5 / 1_000_000) + (500 * 25 / 1_000_000) = 0.005 + 0.0125 = 0.0175
		assert.InDelta(t, 0.0175, cost, 1e-9)
	})
}

func TestSessionManager_LastActivity(t *testing.T) {
	t.Parallel()

	t.Run("initial LastActivity is recent", func(t *testing.T) {
		t.Parallel()

		before := time.Now()
		sm := NewSessionManager(SessionManagerConfig{})
		after := time.Now()

		la := sm.LastActivity()
		assert.False(t, la.Before(before), "LastActivity should not be before creation")
		assert.False(t, la.After(after), "LastActivity should not be after creation")
	})

	t.Run("LastActivity updated on AcceptUserMessage", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})
		initialActivity := sm.LastActivity()

		provider := newStopProvider("hi")
		_ = sm.AcceptUserMessage(context.Background(), provider, "config-id", "provider-model", "hello")
		defer func() { _ = sm.Cancel(context.Background()) }()

		require.Eventually(t, func() bool {
			return sm.LastActivity().After(initialActivity)
		}, 5*time.Second, 10*time.Millisecond, "LastActivity should be updated after AcceptUserMessage")
	})
}

func TestSessionManager_CostInRecordMessage(t *testing.T) {
	t.Parallel()

	const (
		inputCost  = 3.0
		outputCost = 15.0
		// (1000 * 3 / 1_000_000) + (500 * 15 / 1_000_000) = 0.0105
		expectedCostPerMsg = 0.0105
	)

	newPricedManager := func(onMessage func(context.Context, Message) error) *SessionManager {
		return NewSessionManager(SessionManagerConfig{
			InputCostPer1M:  inputCost,
			OutputCostPer1M: outputCost,
			OnMessage:       onMessage,
		})
	}

	testUsage := &llm.Usage{PromptTokens: 1000, CompletionTokens: 500}

	t.Run("assistant message with usage gets cost calculated", func(t *testing.T) {
		t.Parallel()

		var recordedMessages []Message
		var mu sync.Mutex

		sm := newPricedManager(func(_ context.Context, msg Message) error {
			mu.Lock()
			recordedMessages = append(recordedMessages, msg)
			mu.Unlock()
			return nil
		})

		recordFunc := sm.createRecordMessageFunc()
		recordFunc(context.Background(), Message{Type: MessageTypeAssistant, Usage: testUsage})

		assert.InDelta(t, expectedCostPerMsg, sm.GetTotalCost(), 1e-9)

		mu.Lock()
		require.Len(t, recordedMessages, 1)
		require.NotNil(t, recordedMessages[0].Cost)
		assert.InDelta(t, expectedCostPerMsg, *recordedMessages[0].Cost, 1e-9)
		mu.Unlock()
	})

	t.Run("user message does not get cost calculated", func(t *testing.T) {
		t.Parallel()

		sm := newPricedManager(nil)
		recordFunc := sm.createRecordMessageFunc()

		recordFunc(context.Background(), Message{Type: MessageTypeUser, Content: "hello"})

		assert.Equal(t, 0.0, sm.GetTotalCost())
	})

	t.Run("multiple assistant messages accumulate cost", func(t *testing.T) {
		t.Parallel()

		sm := newPricedManager(nil)
		recordFunc := sm.createRecordMessageFunc()

		for range 3 {
			recordFunc(context.Background(), Message{
				Type:  MessageTypeAssistant,
				Usage: testUsage,
			})
		}

		assert.InDelta(t, expectedCostPerMsg*3, sm.GetTotalCost(), 1e-9)
	})
}

func TestSessionManager_SetWorkingBroadcastsCost(t *testing.T) {
	t.Parallel()

	t.Run("broadcast includes TotalCost", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "test"})
		sm.addCost(0.05)

		ctx := t.Context()

		next := sm.Subscribe(ctx)

		sm.SetWorking(true)

		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, ok := next()
			if ok && resp.SessionState != nil {
				assert.True(t, resp.SessionState.Working)
				assert.InDelta(t, 0.05, resp.SessionState.TotalCost, 1e-9)
			}
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for broadcast")
		}
	})
}

func TestSessionManager_SetSafeMode(t *testing.T) {
	t.Parallel()

	t.Run("SetSafeMode does not panic", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})

		assert.NotPanics(t, func() {
			sm.SetSafeMode(true)
		})
	})

	t.Run("SetSafeMode is thread-safe", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{})

		var wg sync.WaitGroup
		for i := range 10 {
			wg.Add(1)
			go func(val bool) {
				defer wg.Done()
				sm.SetSafeMode(val)
			}(i%2 == 0)
		}
		wg.Wait()
	})
}

func TestSessionManager_RecordMessage_PersistsViaCallback(t *testing.T) {
	t.Parallel()

	var persisted []Message
	var mu sync.Mutex

	provider := newStopProvider("persisted response")

	sm := NewSessionManager(SessionManagerConfig{
		OnMessage: func(_ context.Context, msg Message) error {
			mu.Lock()
			persisted = append(persisted, msg)
			mu.Unlock()
			return nil
		},
	})

	err := sm.AcceptUserMessage(context.Background(), provider, "m", "m", "hello")
	require.NoError(t, err)

	// Wait for the assistant message to be persisted (user message is persisted synchronously).
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, msg := range persisted {
			if msg.Type == MessageTypeAssistant {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "assistant message should be persisted via OnMessage callback")

	_ = sm.Cancel(context.Background())
}

func TestSessionManager_ConcurrentMessages(t *testing.T) {
	t.Parallel()

	provider := newStopProvider("ok")

	sm := NewSessionManager(SessionManagerConfig{})

	for i := range 3 {
		err := sm.AcceptUserMessage(context.Background(), provider, "m", "m",
			"message-"+string(rune('0'+i)))
		require.NoError(t, err)
		expected := i + 1
		require.Eventually(t, func() bool {
			return len(sm.GetMessages()) >= expected
		}, 5*time.Second, 10*time.Millisecond)
	}

	require.Eventually(t, func() bool {
		return len(sm.GetMessages()) >= 3
	}, 2*time.Second, 50*time.Millisecond, "should have at least 3 user messages")

	msgs := sm.GetMessages()
	assert.GreaterOrEqual(t, len(msgs), 3, "should have at least 3 user messages")

	_ = sm.Cancel(context.Background())
}

func TestSessionManager_RecordExternalMessage(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var persisted []Message

	sm := NewSessionManager(SessionManagerConfig{
		ID:   "ext-msg-test",
		User: UserIdentity{UserID: "user-1"},
		OnMessage: func(_ context.Context, msg Message) error {
			mu.Lock()
			persisted = append(persisted, msg)
			mu.Unlock()
			return nil
		},
	})

	ctx := context.Background()
	next := sm.Subscribe(ctx)

	msg := Message{
		Type:    MessageTypeAssistant,
		Content: "external message",
	}
	_, err := sm.RecordExternalMessage(ctx, msg)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, ok := next()
		if ok && len(resp.Messages) > 0 {
			assert.Equal(t, "external message", resp.Messages[0].Content)
			assert.Equal(t, "ext-msg-test", resp.Messages[0].SessionID)
			assert.NotEmpty(t, resp.Messages[0].ID)
		}
	}()

	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for SubPub message")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, persisted, 1)
	assert.Equal(t, "external message", persisted[0].Content)
	assert.Equal(t, "ext-msg-test", persisted[0].SessionID)
}

func TestSessionManager_RecordExternalMessage_UpdatesLastActivity(t *testing.T) {
	t.Parallel()

	initialActivity := time.Now().Add(-time.Second)
	sm := NewSessionManager(SessionManagerConfig{
		ID:           "activity-test",
		User:         UserIdentity{UserID: "user-1"},
		LastActivity: initialActivity,
	})

	_, err := sm.RecordExternalMessage(context.Background(), Message{
		Type:    MessageTypeAssistant,
		Content: "update activity",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return sm.LastActivity().After(initialActivity)
	}, 5*time.Second, 10*time.Millisecond, "LastActivity should be updated after RecordExternalMessage")
}

func TestSessionManager_RecordExternalMessage_AppendsToActiveLoopHistory(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(SessionManagerConfig{
		ID:   "loop-history-test",
		User: UserIdentity{UserID: "user-1"},
	})
	loop := &Loop{}
	sm.mu.Lock()
	sm.loop = loop
	sm.mu.Unlock()

	stored, err := sm.RecordExternalMessage(context.Background(), Message{
		Type:    MessageTypeAssistant,
		Content: "notification context",
		LLMData: &llm.Message{
			Role:    llm.RoleAssistant,
			Content: "notification context",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), stored.SequenceID)

	loop.mu.Lock()
	defer loop.mu.Unlock()
	require.Len(t, loop.history, 1)
	assert.Equal(t, llm.RoleAssistant, loop.history[0].Role)
	assert.Equal(t, "notification context", loop.history[0].Content)
}

func TestSessionManager_GetSession_IncludesDelegateFields(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(SessionManagerConfig{
		ID:              "delegate-sess",
		User:            UserIdentity{UserID: "user-1"},
		ParentSessionID: "parent-123",
		DelegateTask:    "analyze data",
	})

	sess := sm.GetSession()

	assert.Equal(t, "delegate-sess", sess.ID)
	assert.Equal(t, "user-1", sess.UserID)
	assert.Equal(t, "parent-123", sess.ParentSessionID)
	assert.Equal(t, "analyze data", sess.DelegateTask)
}

func TestSessionManager_DelegateSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("GetDelegates returns nil initially", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "snap-test"})
		assert.Nil(t, sm.GetDelegates())
	})

	t.Run("tracks started delegate", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "snap-test"})
		sm.SetDelegateStarted("del-1", "task one")

		delegates := sm.GetDelegates()
		require.Len(t, delegates, 1)
		assert.Equal(t, "del-1", delegates[0].ID)
		assert.Equal(t, "task one", delegates[0].Task)
		assert.Equal(t, DelegateStatusRunning, delegates[0].Status)
	})

	t.Run("tracks started then completed", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "snap-test"})
		sm.SetDelegateStarted("del-1", "task one")
		sm.SetDelegateCompleted("del-1", 0.05)

		delegates := sm.GetDelegates()
		require.Len(t, delegates, 1)
		assert.Equal(t, DelegateStatusCompleted, delegates[0].Status)
		assert.InDelta(t, 0.05, delegates[0].Cost, 1e-9)
	})

	t.Run("tracks multiple delegates", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "snap-test"})
		sm.SetDelegateStarted("del-1", "task one")
		sm.SetDelegateStarted("del-2", "task two")
		sm.SetDelegateCompleted("del-1", 0.01)

		delegates := sm.GetDelegates()
		require.Len(t, delegates, 2)

		byID := make(map[string]DelegateSnapshot)
		for _, d := range delegates {
			byID[d.ID] = d
		}
		assert.Equal(t, DelegateStatusCompleted, byID["del-1"].Status)
		assert.Equal(t, DelegateStatusRunning, byID["del-2"].Status)
	})
}

func TestSessionManager_SubscribeWithSnapshot_IncludesDelegates(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(SessionManagerConfig{
		ID:   "snap-sub-test",
		User: UserIdentity{UserID: "user1"},
	})

	sm.SetDelegateStarted("del-a", "analyze")
	sm.SetDelegateCompleted("del-a", 0.02)
	sm.SetDelegateStarted("del-b", "summarize")

	ctx := t.Context()
	snapshot, _ := sm.SubscribeWithSnapshot(ctx)

	require.Len(t, snapshot.Delegates, 2)
	byID := make(map[string]DelegateSnapshot)
	for _, d := range snapshot.Delegates {
		byID[d.ID] = d
	}
	assert.Equal(t, DelegateStatusCompleted, byID["del-a"].Status)
	assert.Equal(t, DelegateStatusRunning, byID["del-b"].Status)
}

func TestSessionManager_HasPendingPrompt(t *testing.T) {
	t.Parallel()

	t.Run("returns false when no prompts pending", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "prompt-test"})
		require.False(t, sm.HasPendingPrompt())
	})

	t.Run("returns true when prompts are pending", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "prompt-test"})

		// Simulate a pending prompt by adding to the map directly
		sm.promptsMu.Lock()
		sm.pendingPrompts["test-prompt"] = make(chan UserPromptResponse, 1)
		sm.promptsMu.Unlock()

		require.True(t, sm.HasPendingPrompt())
	})

	t.Run("returns false after prompt is removed", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "prompt-test"})

		sm.promptsMu.Lock()
		sm.pendingPrompts["test-prompt"] = make(chan UserPromptResponse, 1)
		sm.promptsMu.Unlock()

		require.True(t, sm.HasPendingPrompt())

		sm.promptsMu.Lock()
		delete(sm.pendingPrompts, "test-prompt")
		sm.promptsMu.Unlock()

		require.False(t, sm.HasPendingPrompt())
	})
}

func TestSessionManager_RecordHeartbeat(t *testing.T) {
	t.Parallel()

	t.Run("updates lastHeartbeat", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "hb-test"})
		assert.True(t, sm.LastHeartbeat().IsZero())

		sm.RecordHeartbeat()

		hb := sm.LastHeartbeat()
		assert.False(t, hb.IsZero())
		assert.WithinDuration(t, time.Now(), hb, time.Second)
	})

	t.Run("updates lastActivity", func(t *testing.T) {
		t.Parallel()

		initialActivity := time.Now().Add(-time.Second)
		sm := NewSessionManager(SessionManagerConfig{
			ID:           "hb-test",
			LastActivity: initialActivity,
		})

		sm.RecordHeartbeat()

		require.Eventually(t, func() bool {
			return sm.LastActivity().After(initialActivity)
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("is thread-safe", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "hb-test"})

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				sm.RecordHeartbeat()
			})
		}
		wg.Wait()

		assert.False(t, sm.LastHeartbeat().IsZero())
	})
}

func TestSessionManager_CreateWaitUserResponseFunc(t *testing.T) {
	t.Parallel()

	type waitResult struct {
		resp UserPromptResponse
		err  error
	}

	t.Run("keeps heartbeat fresh while waiting and accepts delayed approval", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			ID:                 "wait-heartbeat",
			PromptWaitInterval: 5 * time.Millisecond,
		})
		emit := sm.createEmitUserPromptFunc()
		wait := sm.createWaitUserResponseFunc()

		emit(UserPrompt{
			PromptID:   "approval-1",
			PromptType: PromptTypeCommandApproval,
			Question:   "Approve command?",
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		resultCh := make(chan waitResult, 1)
		go func() {
			resp, err := wait(ctx, "approval-1")
			resultCh <- waitResult{resp: resp, err: err}
		}()

		require.Eventually(t, sm.HasPendingPrompt, time.Second, 5*time.Millisecond)

		firstHB := sm.LastHeartbeat()
		require.False(t, firstHB.IsZero())
		require.Eventually(t, func() bool {
			return sm.LastHeartbeat().After(firstHB)
		}, time.Second, 5*time.Millisecond)

		require.True(t, sm.SubmitUserResponse(UserPromptResponse{
			PromptID:          "approval-1",
			SelectedOptionIDs: []string{"approve"},
		}))

		select {
		case result := <-resultCh:
			require.NoError(t, result.err)
			assert.Equal(t, []string{"approve"}, result.resp.SelectedOptionIDs)
			assert.False(t, result.resp.Cancelled)
		case <-time.After(time.Second):
			t.Fatal("wait did not return after approval")
		}

		require.Eventually(t, func() bool { return !sm.HasPendingPrompt() }, time.Second, 5*time.Millisecond)
	})

	t.Run("session cancel unblocks approval wait cleanly", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			ID:                 "cancel-approval",
			PromptWaitInterval: 5 * time.Millisecond,
		})
		emit := sm.createEmitUserPromptFunc()
		wait := sm.createWaitUserResponseFunc()

		emit(UserPrompt{
			PromptID:   "approval-cancel",
			PromptType: PromptTypeCommandApproval,
			Question:   "Approve command?",
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		resultCh := make(chan waitResult, 1)
		go func() {
			resp, err := wait(ctx, "approval-cancel")
			resultCh <- waitResult{resp: resp, err: err}
		}()

		require.Eventually(t, sm.HasPendingPrompt, time.Second, 5*time.Millisecond)
		require.NoError(t, sm.Cancel(context.Background()))

		select {
		case result := <-resultCh:
			require.NoError(t, result.err)
			assert.True(t, result.resp.Cancelled)
			assert.Equal(t, "approval-cancel", result.resp.PromptID)
		case <-time.After(time.Second):
			t.Fatal("wait did not return after session cancel")
		}

		require.Eventually(t, func() bool { return !sm.HasPendingPrompt() }, time.Second, 5*time.Millisecond)
	})

	t.Run("general prompt timeout returns deadline exceeded and heartbeat stops", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{
			ID:                 "general-timeout",
			PromptWaitInterval: 5 * time.Millisecond,
		})
		emit := sm.createEmitUserPromptFunc()
		wait := sm.createWaitUserResponseFunc()

		emit(UserPrompt{
			PromptID:   "general-timeout-1",
			PromptType: PromptTypeGeneral,
			Question:   "Need more details?",
		})

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		resultCh := make(chan waitResult, 1)
		go func() {
			resp, err := wait(ctx, "general-timeout-1")
			resultCh <- waitResult{resp: resp, err: err}
		}()

		require.Eventually(t, sm.HasPendingPrompt, time.Second, 5*time.Millisecond)
		firstHB := sm.LastHeartbeat()
		require.False(t, firstHB.IsZero())
		require.Eventually(t, func() bool {
			return sm.LastHeartbeat().After(firstHB)
		}, time.Second, 5*time.Millisecond)

		select {
		case result := <-resultCh:
			require.ErrorIs(t, result.err, context.DeadlineExceeded)
			assert.Empty(t, result.resp.PromptID)
			assert.False(t, result.resp.Cancelled)
		case <-time.After(time.Second):
			t.Fatal("wait did not time out")
		}

		require.Eventually(t, func() bool { return !sm.HasPendingPrompt() }, time.Second, 5*time.Millisecond)
		// Wait for the heartbeat goroutine to fully stop, then verify it remains stable.
		require.Eventually(t, func() bool {
			hb1 := sm.LastHeartbeat()
			for range 5 {
				<-time.After(10 * time.Millisecond)
				if sm.LastHeartbeat() != hb1 {
					return false
				}
			}
			return true
		}, 5*time.Second, 10*time.Millisecond, "heartbeat should stop after wait timeout")
	})
}

func TestSessionManager_CancelPendingPrompts(t *testing.T) {
	t.Parallel()

	t.Run("cancels multiple pending general prompts", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "cancel-test"})

		ch1 := make(chan UserPromptResponse, 1)
		ch2 := make(chan UserPromptResponse, 1)

		sm.promptsMu.Lock()
		sm.pendingPrompts["p1"] = ch1
		sm.pendingPrompts["p2"] = ch2
		sm.promptTypes["p1"] = PromptTypeGeneral
		sm.promptTypes["p2"] = PromptTypeGeneral
		sm.promptsMu.Unlock()

		sm.CancelPendingPrompts()

		select {
		case resp := <-ch1:
			assert.True(t, resp.Cancelled)
			assert.Equal(t, "p1", resp.PromptID)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("ch1 did not receive cancellation")
		}

		select {
		case resp := <-ch2:
			assert.True(t, resp.Cancelled)
			assert.Equal(t, "p2", resp.PromptID)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("ch2 did not receive cancellation")
		}
	})

	t.Run("skips command approval prompts", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "skip-approval"})

		generalCh := make(chan UserPromptResponse, 1)
		approvalCh := make(chan UserPromptResponse, 1)

		sm.promptsMu.Lock()
		sm.pendingPrompts["general"] = generalCh
		sm.pendingPrompts["approval"] = approvalCh
		sm.promptTypes["general"] = PromptTypeGeneral
		sm.promptTypes["approval"] = PromptTypeCommandApproval
		sm.promptsMu.Unlock()

		sm.CancelPendingPrompts()

		// General prompt should be cancelled.
		select {
		case resp := <-generalCh:
			assert.True(t, resp.Cancelled)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("general prompt not cancelled")
		}

		// Approval prompt should NOT be cancelled.
		select {
		case <-approvalCh:
			t.Fatal("approval prompt should not be cancelled")
		case <-time.After(50 * time.Millisecond):
			// Expected: no cancellation.
		}
	})

	t.Run("no panic on empty map", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "empty-cancel"})
		assert.NotPanics(t, func() {
			sm.CancelPendingPrompts()
		})
	})

	t.Run("does not block on already responded channel", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "full-chan"})

		ch := make(chan UserPromptResponse, 1)
		// Pre-fill the channel.
		ch <- UserPromptResponse{PromptID: "p1", FreeTextResponse: "answer"}

		sm.promptsMu.Lock()
		sm.pendingPrompts["p1"] = ch
		sm.promptTypes["p1"] = PromptTypeGeneral
		sm.promptsMu.Unlock()

		done := make(chan struct{})
		go func() {
			defer close(done)
			sm.CancelPendingPrompts()
		}()

		select {
		case <-done:
			// Expected: did not block.
		case <-time.After(100 * time.Millisecond):
			t.Fatal("CancelPendingPrompts blocked on full channel")
		}

		// The original response should still be in the channel (not the cancellation).
		resp := <-ch
		assert.Equal(t, "answer", resp.FreeTextResponse)
		assert.False(t, resp.Cancelled)
	})
}

func TestSessionManager_CancelAllPendingPrompts(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(SessionManagerConfig{ID: "cancel-all"})

	generalCh := make(chan UserPromptResponse, 1)
	approvalCh := make(chan UserPromptResponse, 1)

	sm.promptsMu.Lock()
	sm.pendingPrompts["general"] = generalCh
	sm.pendingPrompts["approval"] = approvalCh
	sm.promptTypes["general"] = PromptTypeGeneral
	sm.promptTypes["approval"] = PromptTypeCommandApproval
	sm.promptsMu.Unlock()

	sm.CancelAllPendingPrompts()

	select {
	case resp := <-generalCh:
		assert.True(t, resp.Cancelled)
		assert.Equal(t, "general", resp.PromptID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("general prompt not cancelled")
	}

	select {
	case resp := <-approvalCh:
		assert.True(t, resp.Cancelled)
		assert.Equal(t, "approval", resp.PromptID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("approval prompt not cancelled")
	}
}

func TestRepairOrphanedToolCalls(t *testing.T) {
	t.Parallel()

	t.Run("no-op on empty history", func(t *testing.T) {
		t.Parallel()
		result := repairOrphanedToolCalls(nil)
		assert.Nil(t, result)
	})

	t.Run("no-op when all tool calls have results", func(t *testing.T) {
		t.Parallel()
		history := []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "tc1"}}},
			{Role: llm.RoleTool, ToolCallID: "tc1", Content: "done"},
		}
		result := repairOrphanedToolCalls(history)
		assert.Len(t, result, 3)
	})

	t.Run("adds synthetic result for orphaned tool call", func(t *testing.T) {
		t.Parallel()
		history := []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "tc1"}, {ID: "tc2"}}},
			{Role: llm.RoleTool, ToolCallID: "tc1", Content: "done"},
			// tc2 is missing — orphaned
		}
		result := repairOrphanedToolCalls(history)
		require.Len(t, result, 4)
		assert.Equal(t, llm.RoleTool, result[3].Role)
		assert.Equal(t, "tc2", result[3].ToolCallID)
		assert.Contains(t, result[3].Content, "cancelled")
	})

	t.Run("adds results for all missing tool calls", func(t *testing.T) {
		t.Parallel()
		history := []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}, {ID: "c"}}},
			// All missing
		}
		result := repairOrphanedToolCalls(history)
		require.Len(t, result, 4) // 1 assistant + 3 synthetic
		for _, msg := range result[1:] {
			assert.Equal(t, llm.RoleTool, msg.Role)
			assert.Contains(t, msg.Content, "cancelled")
		}
	})

	t.Run("ignores earlier paired tool calls", func(t *testing.T) {
		t.Parallel()
		history := []llm.Message{
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old"}}},
			{Role: llm.RoleTool, ToolCallID: "old", Content: "result"},
			{Role: llm.RoleUser, Content: "second"},
			// No orphaned calls — last non-tool message is a user message
		}
		result := repairOrphanedToolCalls(history)
		assert.Len(t, result, 4) // unchanged
	})
}

func TestSessionManager_TryRouteToGeneralPrompt(t *testing.T) {
	t.Parallel()

	t.Run("routes text to pending general prompt", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "route-test"})

		ch := make(chan UserPromptResponse, 1)
		sm.promptsMu.Lock()
		sm.pendingPrompts["p1"] = ch
		sm.promptTypes["p1"] = PromptTypeGeneral
		sm.promptsMu.Unlock()

		id, routed := sm.tryRouteToGeneralPrompt("main.go")
		require.True(t, routed)
		assert.Equal(t, "p1", id)

		resp := <-ch
		assert.Equal(t, "p1", resp.PromptID)
		assert.Equal(t, "main.go", resp.FreeTextResponse)
		assert.False(t, resp.Cancelled)
	})

	t.Run("skips command approval prompts", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "route-skip-approval"})

		ch := make(chan UserPromptResponse, 1)
		sm.promptsMu.Lock()
		sm.pendingPrompts["approval-1"] = ch
		sm.promptTypes["approval-1"] = PromptTypeCommandApproval
		sm.promptsMu.Unlock()

		id, routed := sm.tryRouteToGeneralPrompt("yes")
		assert.False(t, routed)
		assert.Empty(t, id)

		// Channel should be empty — nothing was sent.
		select {
		case <-ch:
			t.Fatal("should not have sent to approval channel")
		default:
			// Expected.
		}
	})

	t.Run("returns false when channel is full", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "route-full"})

		ch := make(chan UserPromptResponse, 1)
		// Pre-fill the channel.
		ch <- UserPromptResponse{PromptID: "p1", FreeTextResponse: "previous"}

		sm.promptsMu.Lock()
		sm.pendingPrompts["p1"] = ch
		sm.promptTypes["p1"] = PromptTypeGeneral
		sm.promptsMu.Unlock()

		done := make(chan struct{})
		go func() {
			defer close(done)
			id, routed := sm.tryRouteToGeneralPrompt("new text")
			assert.False(t, routed)
			assert.Empty(t, id)
		}()

		select {
		case <-done:
			// Expected: did not block.
		case <-time.After(100 * time.Millisecond):
			t.Fatal("tryRouteToGeneralPrompt blocked on full channel")
		}

		// Original response is still there.
		resp := <-ch
		assert.Equal(t, "previous", resp.FreeTextResponse)
	})

	t.Run("returns false when no prompts pending", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "route-empty"})
		id, routed := sm.tryRouteToGeneralPrompt("hello")
		assert.False(t, routed)
		assert.Empty(t, id)
	})
}

func TestSessionManager_AcceptUserMessage_RoutesToPendingPrompt(t *testing.T) {
	t.Parallel()

	t.Run("routes to pending prompt and records with nil LLMData", func(t *testing.T) {
		t.Parallel()

		provider := newStopProvider("hi")

		var persisted []Message
		var mu sync.Mutex

		sm := NewSessionManager(SessionManagerConfig{
			ID: "route-accept",
			OnMessage: func(_ context.Context, msg Message) error {
				mu.Lock()
				persisted = append(persisted, msg)
				mu.Unlock()
				return nil
			},
		})

		// Start the loop first so ensureLoop is a no-op.
		err := sm.AcceptUserMessage(context.Background(), provider, "m", "m", "setup")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return len(sm.GetMessages()) > 0
		}, 5*time.Second, 10*time.Millisecond)

		// Register a pending prompt.
		ch := make(chan UserPromptResponse, 1)
		sm.promptsMu.Lock()
		sm.pendingPrompts["ask-1"] = ch
		sm.promptTypes["ask-1"] = PromptTypeGeneral
		sm.promptsMu.Unlock()

		// Send message while prompt is pending.
		err = sm.AcceptUserMessage(context.Background(), provider, "m", "m", "main.go")
		require.NoError(t, err)

		// Prompt should have received the response.
		select {
		case resp := <-ch:
			assert.Equal(t, "ask-1", resp.PromptID)
			assert.Equal(t, "main.go", resp.FreeTextResponse)
			assert.False(t, resp.Cancelled)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("prompt did not receive response")
		}

		// Check that the message was recorded with nil LLMData.
		msgs := sm.GetMessages()
		var routedMsg *Message
		for i := range msgs {
			if msgs[i].Content == "main.go" {
				routedMsg = &msgs[i]
				break
			}
		}
		require.NotNil(t, routedMsg, "routed message should be recorded")
		assert.Nil(t, routedMsg.LLMData, "routed message should have nil LLMData")

		_ = sm.Cancel(context.Background())
	})

	t.Run("falls back to cancel+queue when channel full", func(t *testing.T) {
		t.Parallel()

		provider := newStopProvider("hi")

		sm := NewSessionManager(SessionManagerConfig{ID: "route-fallback"})

		// Start the loop.
		err := sm.AcceptUserMessage(context.Background(), provider, "m", "m", "setup")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return len(sm.GetMessages()) > 0
		}, 5*time.Second, 10*time.Millisecond)

		// Register a pending prompt with a full channel.
		ch := make(chan UserPromptResponse, 1)
		ch <- UserPromptResponse{PromptID: "ask-1", FreeTextResponse: "previous"}

		sm.promptsMu.Lock()
		sm.pendingPrompts["ask-1"] = ch
		sm.promptTypes["ask-1"] = PromptTypeGeneral
		sm.promptsMu.Unlock()

		// Send message — routing should fail, fall back to normal flow.
		err = sm.AcceptUserMessage(context.Background(), provider, "m", "m", "fallback text")
		require.NoError(t, err)

		// Check that the message was recorded with non-nil LLMData (normal path).
		msgs := sm.GetMessages()
		var fallbackMsg *Message
		for i := range msgs {
			if msgs[i].Content == "fallback text" {
				fallbackMsg = &msgs[i]
				break
			}
		}
		require.NotNil(t, fallbackMsg, "fallback message should be recorded")
		assert.NotNil(t, fallbackMsg.LLMData, "fallback message should have LLMData")

		_ = sm.Cancel(context.Background())
	})
}

func TestSessionManager_EmitUserPrompt_ForcesFreeText(t *testing.T) {
	t.Parallel()

	t.Run("forces AllowFreeText for general prompts", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "freetext-test"})
		emitFn := sm.createEmitUserPromptFunc()

		emitFn(UserPrompt{
			PromptID:      "test-prompt",
			Question:      "Pick one",
			AllowFreeText: false, // LLM says no free text.
		})

		msgs := sm.GetMessages()
		require.NotEmpty(t, msgs)
		require.NotNil(t, msgs[0].UserPrompt)
		assert.True(t, msgs[0].UserPrompt.AllowFreeText, "free text should be forced to true")
		assert.NotEmpty(t, msgs[0].UserPrompt.FreeTextPlaceholder)
	})

	t.Run("does not force AllowFreeText for command approval", func(t *testing.T) {
		t.Parallel()

		sm := NewSessionManager(SessionManagerConfig{ID: "approval-freetext"})
		emitFn := sm.createEmitUserPromptFunc()

		emitFn(UserPrompt{
			PromptID:      "approval-prompt",
			PromptType:    PromptTypeCommandApproval,
			Question:      "Approve?",
			AllowFreeText: false,
		})

		msgs := sm.GetMessages()
		require.NotEmpty(t, msgs)
		require.NotNil(t, msgs[0].UserPrompt)
		assert.False(t, msgs[0].UserPrompt.AllowFreeText, "approval prompts should not force free text")
	})
}

func TestSessionManager_EmitUIAction_PersistsInHistory(t *testing.T) {
	t.Parallel()

	var persisted []Message
	sm := NewSessionManager(SessionManagerConfig{
		ID: "ui-action-test",
		OnMessage: func(_ context.Context, msg Message) error {
			persisted = append(persisted, msg)
			return nil
		},
	})

	emitFn := sm.createEmitUIActionFunc()
	emitFn(UIAction{
		Type: UIActionNavigate,
		Path: "/dags/github_webhook_codex_analysis",
	})

	msgs := sm.GetMessages()
	require.Len(t, msgs, 1)
	assert.Equal(t, MessageTypeUIAction, msgs[0].Type)
	require.NotNil(t, msgs[0].UIAction)
	assert.Equal(t, UIActionNavigate, msgs[0].UIAction.Type)
	assert.Equal(t, "/dags/github_webhook_codex_analysis", msgs[0].UIAction.Path)

	require.Len(t, persisted, 1)
	assert.Equal(t, MessageTypeUIAction, persisted[0].Type)
	require.NotNil(t, persisted[0].UIAction)
	assert.Equal(t, "/dags/github_webhook_codex_analysis", persisted[0].UIAction.Path)
}
