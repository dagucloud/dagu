// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package line

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifySignatureUsesExactRawBody(t *testing.T) {
	t.Parallel()

	body := []byte(`{"destination":"U8e742f61d673b39c7fff3cecb7536ef0","events":[]}`)
	signature := testLineSignature("line-channel-secret", body)

	assert.True(t, verifySignature("line-channel-secret", body, signature))
	assert.False(t, verifySignature("line-channel-secret", []byte("{\n  \"destination\": \"U8e742f61d673b39c7fff3cecb7536ef0\",\n  \"events\": []\n}"), signature))
}

func TestServeHTTPRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	service := newFakeLineAgentService()
	bot := newTestLineBot(service, &fakeLineClient{}, []string{"Uallowed"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/line/webhook", strings.NewReader(`{"destination":"Ubot","events":[]}`))
	req.Header.Set("x-line-signature", "invalid")
	rec := httptest.NewRecorder()

	bot.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, service.createMessages)
}

func TestServeHTTPHandlesConsoleVerificationWithoutEvents(t *testing.T) {
	t.Parallel()

	service := newFakeLineAgentService()
	client := &fakeLineClient{}
	bot := newTestLineBot(service, client, []string{"Uallowed"})
	body := `{"destination":"Ubot","events":[]}`

	rec := performSignedLineWebhook(t, bot, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, service.createMessages)
	assert.Empty(t, client.pushes)
	assert.Empty(t, client.replies)
}

func TestServeHTTPForwardsAllowedTextMessageToAgent(t *testing.T) {
	t.Parallel()

	service := newFakeLineAgentService()
	service.assistantContent = "assistant response"
	client := &fakeLineClient{}
	bot := newTestLineBot(service, client, []string{"Uallowed"})
	body := `{
  "destination": "Ubot",
  "events": [{
    "type": "message",
    "replyToken": "reply-token",
    "source": {"type": "user", "userId": "Uallowed"},
    "message": {"type": "text", "id": "msg-1", "text": "hello"}
  }]
}`

	rec := performSignedLineWebhook(t, bot, body)

	require.Equal(t, http.StatusOK, rec.Code)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		service.mu.Lock()
		defer service.mu.Unlock()
		assert.Equal(c, []string{"hello"}, service.createMessages)
		require.Len(c, service.createUsers, 1)
		assert.Equal(c, "line:Uallowed", service.createUsers[0].UserID)
	}, time.Second, 10*time.Millisecond)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		client.mu.Lock()
		defer client.mu.Unlock()
		require.Len(c, client.pushes, 1)
		assert.Equal(c, lineText{to: "Uallowed", text: "assistant response"}, client.pushes[0])
	}, time.Second, 10*time.Millisecond)
}

func TestServeHTTPSkipsDuplicateWebhookEventID(t *testing.T) {
	t.Parallel()

	service := newFakeLineAgentService()
	client := &fakeLineClient{}
	bot := newTestLineBot(service, client, []string{"Uallowed"})
	body := `{
  "destination": "Ubot",
  "events": [{
    "type": "message",
    "replyToken": "reply-token",
    "webhookEventId": "01JLINEEVENT000000000000000",
    "deliveryContext": {"isRedelivery": false},
    "source": {"type": "user", "userId": "Uallowed"},
    "message": {"type": "text", "id": "msg-1", "text": "hello"}
  }]
}`

	rec := performSignedLineWebhook(t, bot, body)
	require.Equal(t, http.StatusOK, rec.Code)
	rec = performSignedLineWebhook(t, bot, body)
	require.Equal(t, http.StatusOK, rec.Code)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		service.mu.Lock()
		defer service.mu.Unlock()
		assert.Equal(c, []string{"hello"}, service.createMessages)
	}, time.Second, 10*time.Millisecond)
}

func TestSplitMessagePreservesUTF8(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("日", 5) + strings.Repeat("🙂", 5) + "tail"
	chunks := splitMessage(text, 10)

	require.Greater(t, len(chunks), 1)
	assert.Equal(t, text, strings.Join(chunks, ""))
	for _, chunk := range chunks {
		assert.True(t, utf8.ValidString(chunk))
		assert.LessOrEqual(t, len(chunk), 10)
	}
}

func TestSplitMessagePrefersParagraphBoundary(t *testing.T) {
	t.Parallel()

	chunks := splitMessage("aaaaaaaa\n\nbbbbbbbb", 12)

	require.Len(t, chunks, 2)
	assert.Equal(t, "aaaaaaaa\n\n", chunks[0])
	assert.Equal(t, "bbbbbbbb", chunks[1])
}

func newTestLineBot(service *fakeLineAgentService, client *fakeLineClient, allowed []string) *Bot {
	allowedSources := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSources[id] = struct{}{}
	}
	return &Bot{
		cfg: Config{
			ChannelAccessToken: "line-channel-token",
			ChannelSecret:      "line-channel-secret",
			SafeMode:           true,
			RespondToAll:       true,
		},
		agentAPI:          service,
		client:            client,
		allowedSources:    allowedSources,
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		incomingDelay:     time.Millisecond,
		incomingAfterFunc: func(_ time.Duration, f func()) { f() },
	}
}

func performSignedLineWebhook(t *testing.T, bot *Bot, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/bots/line/webhook", strings.NewReader(body))
	req.Header.Set("x-line-signature", testLineSignature("line-channel-secret", []byte(body)))
	rec := httptest.NewRecorder()
	bot.ServeHTTP(rec, req)
	return rec
}

func testLineSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type lineText struct {
	to   string
	text string
}

type fakeLineClient struct {
	mu      sync.Mutex
	pushes  []lineText
	replies []lineText
}

func (c *fakeLineClient) PushText(_ context.Context, to, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pushes = append(c.pushes, lineText{to: to, text: text})
	return nil
}

func (c *fakeLineClient) ReplyText(_ context.Context, replyToken, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.replies = append(c.replies, lineText{to: replyToken, text: text})
	return nil
}

type fakeLineAgentService struct {
	mu               sync.Mutex
	nextSessionID    int
	createMessages   []string
	createUsers      []agent.UserIdentity
	sendMessages     []string
	cancelErr        error
	submitResponses  []agent.UserPromptResponse
	assistantContent string
}

func newFakeLineAgentService() *fakeLineAgentService {
	return &fakeLineAgentService{}
}

func (s *fakeLineAgentService) CreateSession(_ context.Context, user agent.UserIdentity, req agent.ChatRequest) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSessionID++
	s.createMessages = append(s.createMessages, req.Message)
	s.createUsers = append(s.createUsers, user)
	return fmt.Sprintf("session-%d", s.nextSessionID), "", nil
}

func (s *fakeLineAgentService) CreateEmptySession(context.Context, agent.UserIdentity, string, bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSessionID++
	return fmt.Sprintf("session-%d", s.nextSessionID), nil
}

func (s *fakeLineAgentService) SendMessage(_ context.Context, _ string, _ agent.UserIdentity, req agent.ChatRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendMessages = append(s.sendMessages, req.Message)
	return nil
}

func (s *fakeLineAgentService) EnqueueChatMessage(_ context.Context, sessionID string, _ agent.UserIdentity, req agent.ChatRequest) (agent.ChatQueueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendMessages = append(s.sendMessages, req.Message)
	return agent.ChatQueueResult{SessionID: sessionID, Started: true}, nil
}

func (s *fakeLineAgentService) FlushQueuedChatMessage(_ context.Context, sessionID string, _ agent.UserIdentity) (agent.ChatQueueResult, error) {
	return agent.ChatQueueResult{SessionID: sessionID, Started: true}, nil
}

func (s *fakeLineAgentService) CancelSession(context.Context, string, string) error {
	return s.cancelErr
}

func (s *fakeLineAgentService) SubmitUserResponse(_ context.Context, _ string, _ string, resp agent.UserPromptResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submitResponses = append(s.submitResponses, resp)
	return nil
}

func (s *fakeLineAgentService) GenerateAssistantMessage(context.Context, string, agent.UserIdentity, string, string) (agent.Message, error) {
	return agent.Message{Type: agent.MessageTypeAssistant, Content: s.assistantContent}, nil
}

func (s *fakeLineAgentService) AppendExternalMessage(_ context.Context, sessionID string, _ agent.UserIdentity, msg agent.Message) (agent.Message, error) {
	msg.SessionID = sessionID
	msg.SequenceID = 1
	return msg, nil
}

func (s *fakeLineAgentService) CompactSessionIfNeeded(_ context.Context, sessionID string, _ agent.UserIdentity) (string, bool, error) {
	return sessionID, false, nil
}

func (s *fakeLineAgentService) GetSessionDetail(context.Context, string, string) (*agent.StreamResponse, error) {
	return &agent.StreamResponse{}, nil
}

func (s *fakeLineAgentService) SubscribeSession(context.Context, string, agent.UserIdentity) (agent.StreamResponse, func() (agent.StreamResponse, bool), error) {
	s.mu.Lock()
	content := s.assistantContent
	s.mu.Unlock()
	snapshot := agent.StreamResponse{}
	if content != "" {
		snapshot.Messages = []agent.Message{{
			Type:       agent.MessageTypeAssistant,
			SequenceID: 1,
			Content:    content,
			CreatedAt:  time.Now(),
		}}
	}
	return snapshot, func() (agent.StreamResponse, bool) { return agent.StreamResponse{}, false }, nil
}
