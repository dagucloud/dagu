// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/dagucloud/dagu/internal/testutil"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTelegramAPI struct {
	mu           sync.Mutex
	sends        []tgbotapi.Chattable
	actionCount  int
	messageCount int
}

func (a *fakeTelegramAPI) GetUpdatesChan(tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	return nil
}

func (a *fakeTelegramAPI) StopReceivingUpdates() {}

func (a *fakeTelegramAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sends = append(a.sends, c)
	switch c.(type) {
	case tgbotapi.ChatActionConfig:
		a.actionCount++
	default:
		a.messageCount++
	}
	return tgbotapi.Message{}, nil
}

func (a *fakeTelegramAPI) sendCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sends)
}

func (a *fakeTelegramAPI) typingCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.actionCount
}

func (a *fakeTelegramAPI) textCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.messageCount
}

func assertTypingStops(t *testing.T, api *fakeTelegramAPI, msg string) int {
	t.Helper()

	stoppedAt := api.typingCount()
	assert.Never(t, func() bool {
		return api.typingCount() > stoppedAt
	}, 50*time.Millisecond, 5*time.Millisecond, msg)
	return stoppedAt
}

type fakeTelegramAgentService struct {
	mu                 sync.Mutex
	nextSessionID      int
	nextSequenceID     int64
	createEmptyCalls   int
	appendAttempts     []string
	appendSessionIDs   []string
	appendMessages     []agent.Message
	createMessages     []string
	sendMessages       []string
	flushCalls         int
	generateCalls      int
	generated          agent.Message
	generatedErr       error
	appendErrBySession map[string]error
	enqueueResult      agent.ChatQueueResult
	flushResult        agent.ChatQueueResult
	createErr          error
	cancelErr          error
}

func newFakeTelegramAgentService(content string) *fakeTelegramAgentService {
	return &fakeTelegramAgentService{
		nextSequenceID: 1,
		generated: agent.Message{
			Type:      agent.MessageTypeAssistant,
			Content:   content,
			CreatedAt: time.Now(),
			LLMData: &llm.Message{
				Role:    llm.RoleAssistant,
				Content: content,
			},
		},
	}
}

func (s *fakeTelegramAgentService) CreateSession(_ context.Context, _ agent.UserIdentity, req agent.ChatRequest) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createMessages = append(s.createMessages, req.Message)
	if s.createErr != nil {
		return "", "", s.createErr
	}
	s.nextSessionID++
	return fmt.Sprintf("created-%d", s.nextSessionID), "", nil
}

func (s *fakeTelegramAgentService) CreateEmptySession(_ context.Context, _ agent.UserIdentity, _ string, _ bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSessionID++
	s.createEmptyCalls++
	return fmt.Sprintf("sess-%d", s.nextSessionID), nil
}

func (s *fakeTelegramAgentService) SendMessage(_ context.Context, _ string, _ agent.UserIdentity, req agent.ChatRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendMessages = append(s.sendMessages, req.Message)
	return nil
}

func (s *fakeTelegramAgentService) EnqueueChatMessage(_ context.Context, sessionID string, _ agent.UserIdentity, req agent.ChatRequest) (agent.ChatQueueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendMessages = append(s.sendMessages, req.Message)
	result := s.enqueueResult
	if result.SessionID == "" {
		result.SessionID = sessionID
	}
	if !result.Queued {
		result.Started = true
	}
	return result, nil
}

func (s *fakeTelegramAgentService) FlushQueuedChatMessage(_ context.Context, sessionID string, _ agent.UserIdentity) (agent.ChatQueueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushCalls++
	result := s.flushResult
	if result.SessionID == "" {
		result.SessionID = sessionID
	}
	return result, nil
}

func (s *fakeTelegramAgentService) CancelSession(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelErr
}

func (s *fakeTelegramAgentService) SubmitUserResponse(context.Context, string, string, agent.UserPromptResponse) error {
	return nil
}

func (s *fakeTelegramAgentService) GenerateAssistantMessage(context.Context, string, agent.UserIdentity, string, string) (agent.Message, error) {
	s.mu.Lock()
	s.generateCalls++
	err := s.generatedErr
	msg := s.generated
	s.mu.Unlock()
	if err != nil {
		return agent.Message{}, err
	}
	return msg, nil
}

func (s *fakeTelegramAgentService) AppendExternalMessage(_ context.Context, sessionID string, _ agent.UserIdentity, msg agent.Message) (agent.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAttempts = append(s.appendAttempts, sessionID)
	if err := s.appendErrBySession[sessionID]; err != nil {
		return agent.Message{}, err
	}
	msg.SessionID = sessionID
	msg.SequenceID = s.nextSequenceID
	s.nextSequenceID++
	s.appendSessionIDs = append(s.appendSessionIDs, sessionID)
	s.appendMessages = append(s.appendMessages, msg)
	return msg, nil
}

func (s *fakeTelegramAgentService) CompactSessionIfNeeded(_ context.Context, sessionID string, _ agent.UserIdentity) (string, bool, error) {
	return sessionID, false, nil
}

func (s *fakeTelegramAgentService) GetSessionDetail(context.Context, string, string) (*agent.StreamResponse, error) {
	return &agent.StreamResponse{}, nil
}

func (s *fakeTelegramAgentService) SubscribeSession(context.Context, string, agent.UserIdentity) (agent.StreamResponse, func() (agent.StreamResponse, bool), error) {
	return agent.StreamResponse{}, func() (agent.StreamResponse, bool) { return agent.StreamResponse{}, false }, nil
}

func TestDAGRunMonitor_SuppressesSuccessDigestAndMarksRunsDelivered(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	service := newFakeTelegramAgentService("telegram notification")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		cfg:          Config{SafeMode: true},
		agentAPI:     service,
		botAPI:       api,
		allowedChats: map[int64]struct{}{123: {}},
		logger:       logger,
	}
	monitor := newDAGRunMonitorWithWindows(nil, "", service, bot, logger, 10*time.Millisecond, 20*time.Millisecond)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	cs := bot.getOrCreateChat(123)
	bot.setActiveSession(cs, "existing-session", "telegram:123")

	first := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Succeeded,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
	}
	second := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Succeeded,
		DAGRunID:  "run-2",
		AttemptID: "attempt-2",
	}
	require.True(t, monitor.notifyCompletion(context.Background(), first))
	require.True(t, monitor.notifyCompletion(context.Background(), second))

	require.Eventually(t, func() bool {
		return monitor.isSeen("123", first) && monitor.isSeen("123", second)
	}, time.Second, 10*time.Millisecond)

	service.mu.Lock()
	assert.Zero(t, service.createEmptyCalls)
	assert.Empty(t, service.appendSessionIDs)
	assert.Empty(t, service.appendMessages)
	service.mu.Unlock()
	assert.Equal(t, int64(0), bot.lastDeliveredSeq(cs))
	assert.Zero(t, api.sendCount(), "successful completions should not send a Telegram digest")
}

func TestDAGRunMonitor_FlushesUrgentSingleCreatesSessionWhenMissing(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	service := newFakeTelegramAgentService("fresh notification")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		cfg:          Config{SafeMode: true},
		agentAPI:     service,
		botAPI:       api,
		allowedChats: map[int64]struct{}{456: {}},
		logger:       logger,
	}
	monitor := newDAGRunMonitorWithWindows(nil, "", service, bot, logger, 10*time.Millisecond, 20*time.Millisecond)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	ok := monitor.notifyCompletion(context.Background(), &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Failed,
		DAGRunID:  "run-2",
		AttemptID: "attempt-2",
		Error:     "boom",
	})
	require.True(t, ok)

	require.Eventually(t, func() bool {
		return api.sendCount() == 1
	}, time.Second, 10*time.Millisecond)

	cs := bot.getOrCreateChat(456)
	sessionID, ownerUserID := cs.ActiveSession()
	assert.Equal(t, "sess-1", sessionID)
	assert.Equal(t, "telegram:456", ownerUserID)
	assert.Equal(t, int64(1), bot.lastDeliveredSeq(cs))
	assert.Equal(t, 1, service.createEmptyCalls)
	assert.Equal(t, 1, api.sendCount())
	service.mu.Lock()
	require.Len(t, service.appendMessages, 1)
	assert.Equal(t, "fresh notification", service.appendMessages[0].Content)
	service.mu.Unlock()
}

func TestBot_HandleMessage_BatchesRapidMessagesIntoSingleCreate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := &fakeTelegramAPI{}
	service := newFakeTelegramAgentService("ignored")
	var flushes []func()
	bot := &Bot{
		cfg:               Config{SafeMode: true},
		agentAPI:          service,
		botAPI:            api,
		allowedChats:      map[int64]struct{}{123: {}},
		logger:            logger,
		incomingDelay:     time.Hour,
		incomingAfterFunc: func(_ time.Duration, f func()) { flushes = append(flushes, f) },
	}

	first := &tgbotapi.Message{Text: "first", Chat: &tgbotapi.Chat{ID: 123}}
	second := &tgbotapi.Message{Text: "second", Chat: &tgbotapi.Chat{ID: 123}}
	bot.handleMessage(context.Background(), first)
	assert.Equal(t, 1, api.typingCount(), "first inbound message should start typing immediately")
	bot.handleMessage(context.Background(), second)
	assert.Equal(t, 1, api.typingCount(), "batched messages should reuse the current typing loop")

	require.Len(t, flushes, 2)
	for _, flush := range flushes {
		flush()
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.createMessages, 1)
	assert.Equal(t, "first\n\nsecond", service.createMessages[0])
}

func TestBot_HandleCommand_CancelStopsPreFlushTypingWithoutSession(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		botAPI:       api,
		logger:       logger,
		allowedChats: map[int64]struct{}{123: {}},
	}

	cs := bot.getOrCreateChat(123)
	bot.startTypingLoop(context.Background(), cs, 123)
	require.Eventually(t, func() bool {
		return api.typingCount() >= 1
	}, time.Second, 10*time.Millisecond)

	bot.handleCommand(context.Background(), &tgbotapi.Message{
		Text: "/cancel",
		Chat: &tgbotapi.Chat{ID: 123},
		Entities: []tgbotapi.MessageEntity{{
			Type:   "bot_command",
			Offset: 0,
			Length: len("/cancel"),
		}},
	})

	assertTypingStops(t, api, "cancel without an active session should stop any pre-flush typing loop")
	assert.Equal(t, 1, api.textCount(), "cancel should still acknowledge the missing session")
}

func TestBot_ProcessStreamResponse_RefreshesTypingWhileWorking(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		botAPI:      api,
		logger:      logger,
		typingDelay: 10 * time.Millisecond,
	}
	cs := bot.getOrCreateChat(123)

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		SessionState: &agent.SessionState{Working: true},
	})

	require.Eventually(t, func() bool {
		return api.typingCount() >= 2
	}, time.Second, 10*time.Millisecond)

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		SessionState: &agent.SessionState{Working: false},
	})

	assertTypingStops(t, api, "typing loop should stop once working=false arrives")
}

func TestBot_ProcessStreamResponse_StopsTypingOnAssistantMessage(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		botAPI:      api,
		logger:      logger,
		typingDelay: 10 * time.Millisecond,
	}
	cs := bot.getOrCreateChat(123)

	bot.startTypingLoop(context.Background(), cs, 123)
	require.Eventually(t, func() bool {
		return api.typingCount() >= 1
	}, time.Second, 10*time.Millisecond)

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		Messages: []agent.Message{
			{Type: agent.MessageTypeAssistant, SequenceID: 1, Content: "done"},
		},
	})

	assertTypingStops(t, api, "assistant output should stop typing immediately")
	assert.Equal(t, 1, api.textCount())
}

func TestBot_ProcessStreamResponse_RestartsTypingWhenWorkContinues(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		botAPI:      api,
		logger:      logger,
		typingDelay: 10 * time.Millisecond,
	}
	cs := bot.getOrCreateChat(123)

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		SessionState: &agent.SessionState{Working: true},
	})
	require.Eventually(t, func() bool {
		return api.typingCount() >= 1
	}, time.Second, 10*time.Millisecond)

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		Messages: []agent.Message{
			{Type: agent.MessageTypeAssistant, SequenceID: 1, Content: "partial reply"},
		},
	})

	beforeRestart := assertTypingStops(t, api, "assistant output should stop the current typing loop")

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		SessionState: &agent.SessionState{Working: true},
	})
	require.Eventually(t, func() bool {
		return api.typingCount() > beforeRestart
	}, time.Second, 10*time.Millisecond)
}

func TestBot_ProcessStreamResponse_FlushesQueuedTurnWhenIdle(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	service := newFakeTelegramAgentService("ignored")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		agentAPI:    service,
		botAPI:      api,
		logger:      logger,
		typingDelay: 10 * time.Millisecond,
	}
	cs := bot.getOrCreateChat(123)
	bot.setActiveSession(cs, "existing-session", "telegram:123")

	bot.processStreamResponse(context.Background(), cs, 123, agent.StreamResponse{
		SessionState: &agent.SessionState{
			SessionID:          "existing-session",
			Working:            false,
			HasQueuedUserInput: true,
		},
	})

	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.flushCalls == 1
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		return api.typingCount() >= 1
	}, time.Second, 10*time.Millisecond)
}
