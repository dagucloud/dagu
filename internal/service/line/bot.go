// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package line provides a LINE Messaging API bot service that bridges LINE
// chats with the Dagu AI agent.
package line

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

const maxLineTextLen = 5000
const maxLineWebhookBodyBytes = 1 << 20
const defaultIncomingBatchDelay = 750 * time.Millisecond
const processedWebhookEventTTL = 24 * time.Hour
const maxProcessedWebhookEvents = 10000

// AgentService is the subset of the agent API that the LINE bot requires.
type AgentService = chatbridge.AgentService

// Config holds configuration for the LINE bot.
type Config struct {
	ChannelAccessToken    string
	ChannelSecret         string
	AllowedSourceIDs      []string
	InterestedEventTypes  []string
	SafeMode              bool
	RespondToAll          bool
	EventService          *eventstore.Service
	NotificationStateFile string
}

type chatState struct {
	chatbridge.State

	promptMu              sync.Mutex
	pendingPromptOptions  map[string]string
	pendingPromptFreeText bool
}

// Bot forwards LINE webhook events to the Dagu agent API.
type Bot struct {
	cfg                   Config
	agentAPI              AgentService
	client                lineClientAPI
	chats                 sync.Map // sourceID (string) -> *chatState
	allowedSources        map[string]struct{}
	processedEventMu      sync.Mutex
	processedEvents       map[string]time.Time
	eventService          *eventstore.Service
	notificationStateFile string
	logger                *slog.Logger
	incomingDelay         time.Duration
	incomingAfterFunc     func(time.Duration, func())
}

// New creates a new LINE bot instance.
func New(cfg Config, agentAPI AgentService, logger *slog.Logger) (*Bot, error) {
	if cfg.ChannelAccessToken == "" {
		return nil, fmt.Errorf("line channel access token is required (set DAGU_BOTS_LINE_CHANNEL_ACCESS_TOKEN)")
	}
	if cfg.ChannelSecret == "" {
		return nil, fmt.Errorf("line channel secret is required (set DAGU_BOTS_LINE_CHANNEL_SECRET)")
	}
	if len(cfg.AllowedSourceIDs) == 0 {
		return nil, fmt.Errorf("at least one allowed source ID is required (set DAGU_BOTS_LINE_ALLOWED_SOURCE_IDS)")
	}

	allowed := make(map[string]struct{}, len(cfg.AllowedSourceIDs))
	for _, id := range cfg.AllowedSourceIDs {
		allowed[id] = struct{}{}
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Bot{
		cfg:                   cfg,
		agentAPI:              agentAPI,
		client:                newHTTPLineClient(cfg.ChannelAccessToken),
		allowedSources:        allowed,
		processedEvents:       make(map[string]time.Time),
		eventService:          cfg.EventService,
		notificationStateFile: cfg.NotificationStateFile,
		logger:                logger,
		incomingDelay:         defaultIncomingBatchDelay,
		incomingAfterFunc:     func(delay time.Duration, f func()) { time.AfterFunc(delay, f) },
	}, nil
}

// ConfigureRoutes registers the public LINE webhook route on the Dagu server.
func (b *Bot) ConfigureRoutes(ctx context.Context, r chi.Router, apiV1BasePath string) {
	webhookPath := path.Join(apiV1BasePath, "bots/line/webhook")
	r.Post(webhookPath, b.ServeHTTP)
	b.logger.InfoContext(ctx, "LINE webhook route configured", slog.String("path", webhookPath))
}

// Run starts background LINE bot work and blocks until the context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.logger.Info("LINE bot started",
		slog.Int("allowed_sources", len(b.allowedSources)),
		slog.Bool("respond_to_all", b.cfg.RespondToAll),
	)

	if b.eventService != nil {
		monitor := NewDAGRunMonitor(b.eventService, b.notificationStateFile, b.agentAPI, b, b.logger)
		go monitor.Run(ctx)
	} else {
		b.logger.Warn("Event store is not configured; DAG run notifications are disabled")
	}

	<-ctx.Done()
	b.logger.Info("LINE bot stopped")
	return nil
}

func (b *Bot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLineWebhookBodyBytes))
	if err != nil {
		http.Error(w, "invalid webhook body", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("x-line-signature")
	if !verifySignature(b.cfg.ChannelSecret, body, signature) {
		http.Error(w, "invalid LINE signature", http.StatusUnauthorized)
		return
	}

	var payload webhookRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid webhook json", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	for _, event := range payload.Events {
		b.handleWebhookEvent(r.Context(), event)
	}
}

func verifySignature(channelSecret string, body []byte, signature string) bool {
	if channelSecret == "" || signature == "" {
		return false
	}
	got, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(channelSecret))
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func (b *Bot) handleWebhookEvent(ctx context.Context, event webhookEvent) {
	if !b.claimWebhookEvent(event) {
		b.logger.Info("Skipped duplicate LINE webhook event",
			slog.String("webhook_event_id", event.WebhookEventID),
			slog.Bool("is_redelivery", event.DeliveryContext.IsRedelivery),
		)
		return
	}
	if event.Type != "message" || event.Message.Type != "text" {
		return
	}
	sourceID := event.Source.ID()
	if sourceID == "" {
		return
	}
	if !b.isSourceAllowed(sourceID) {
		b.logger.Warn("Rejected LINE webhook from unauthorized source",
			slog.String("source_id", sourceID),
			slog.String("source_type", event.Source.Type),
		)
		if event.ReplyToken != "" {
			b.replyText(ctx, event.ReplyToken, "This bot is not authorized for this chat.")
		}
		return
	}

	text := strings.TrimSpace(event.Message.Text)
	if text == "" {
		return
	}

	cs := b.getOrCreateChat(sourceID)
	pendingPrompt := cs.PendingPromptID()
	mentioned := event.Message.SelfMentioned()
	if event.Source.Type != "user" && !b.cfg.RespondToAll && !mentioned && pendingPrompt == "" {
		return
	}
	if mentioned {
		text = stripSelfMention(text, event.Message.Mention)
		if text == "" {
			return
		}
	}

	if pendingPrompt != "" {
		b.submitPromptResponse(ctx, cs, sourceID, pendingPrompt, text)
		return
	}

	if fields := strings.Fields(text); len(fields) > 0 {
		cmd := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
		switch cmd {
		case "new", "cancel", "start":
			b.clearPendingMessages(cs)
			b.handleTextCommand(ctx, cs, sourceID, cmd)
			return
		}
	}

	b.enqueueIncomingMessage(ctx, cs, sourceID, text)
}

func (b *Bot) handleTextCommand(ctx context.Context, cs *chatState, sourceID, cmd string) {
	switch cmd {
	case "new":
		b.resetChat(cs)
		b.sendText(ctx, sourceID, "Session cleared. Send a message to start a new conversation.")
	case "cancel":
		sid, ownerUID := cs.ActiveSession()
		if sid == "" {
			b.sendText(ctx, sourceID, "No active session.")
			return
		}
		if err := b.agentAPI.CancelSession(ctx, sid, ownerUID); err != nil {
			b.sendText(ctx, sourceID, "Failed to cancel session: "+err.Error())
			return
		}
		b.sendText(ctx, sourceID, "Session cancelled.")
	case "start":
		b.sendText(ctx, sourceID, "Welcome to Dagu AI Agent! Send any message to start chatting.\n\nCommands:\n/new - Start a new session\n/cancel - Cancel current session")
	}
}

func (b *Bot) enqueueIncomingMessage(ctx context.Context, cs *chatState, sourceID, text string) {
	if text == "" {
		return
	}

	gen := cs.EnqueuePendingMessage(text)
	delay := b.incomingDelay
	if delay <= 0 {
		delay = defaultIncomingBatchDelay
	}
	afterFunc := b.incomingAfterFunc
	if afterFunc == nil {
		afterFunc = func(delay time.Duration, f func()) { time.AfterFunc(delay, f) }
	}
	afterFunc(delay, func() {
		b.flushIncomingMessages(ctx, cs, sourceID, gen)
	})
}

func (b *Bot) flushIncomingMessages(ctx context.Context, cs *chatState, sourceID string, gen uint64) {
	text, ok := cs.TakePendingMessages(gen, "\n\n")
	if !ok {
		return
	}

	user := b.userIdentity(sourceID)
	if cs.SessionID() == "" {
		b.createSession(ctx, cs, sourceID, user, text)
		return
	}

	b.sendMessage(ctx, cs, sourceID, user, text)
}

func (b *Bot) createSession(ctx context.Context, cs *chatState, sourceID string, user agent.UserIdentity, text string) {
	req := agent.ChatRequest{
		Message:  text,
		SafeMode: b.cfg.SafeMode,
	}

	sessionID, _, err := b.agentAPI.CreateSession(ctx, user, req)
	if err != nil {
		b.logger.Error("Failed to create LINE session", slog.String("error", err.Error()))
		b.sendText(ctx, sourceID, "Failed to start session: "+err.Error())
		return
	}

	b.setActiveSession(cs, sessionID, user.UserID)
	b.startSubscription(ctx, cs, sourceID, user.UserID, sessionID)
}

func (b *Bot) sendMessage(ctx context.Context, cs *chatState, sourceID string, user agent.UserIdentity, text string) {
	req := agent.ChatRequest{
		Message:  text,
		SafeMode: b.cfg.SafeMode,
	}

	result, err := chatbridge.EnqueueMessage(ctx, b.agentAPI, &cs.State, user, req)
	if err != nil {
		b.logger.Error("Failed to enqueue LINE message", slog.String("error", err.Error()))
		b.sendText(ctx, sourceID, "Failed to send message: "+err.Error())
		return
	}
	if result.Missing {
		b.logger.Warn("Session missing during LINE send, recreating chat",
			slog.String("session", result.SessionID),
			slog.String("user", user.UserID),
		)
		b.resetChat(cs)
		b.createSession(ctx, cs, sourceID, user, text)
		return
	}
	sid := result.SessionID
	if sid == "" {
		sid = cs.SessionID()
	}
	if sid != "" {
		b.ensureSubscription(ctx, cs, sourceID, user.UserID, sid)
	}
}

func (b *Bot) submitPromptResponse(ctx context.Context, cs *chatState, sourceID, promptID, text string) {
	sid, ownerUserID := cs.ActiveSession()
	resp, ok := cs.responseForPrompt(promptID, text)
	cs.ClearPendingPrompt()
	if !ok {
		b.sendText(ctx, sourceID, "Please choose one of the listed options.")
		return
	}
	if sid == "" {
		return
	}

	if err := b.agentAPI.SubmitUserResponse(ctx, sid, ownerUserID, resp); err != nil {
		b.sendText(ctx, sourceID, "Failed to submit response: "+err.Error())
	}
}

func (b *Bot) ensureSubscription(ctx context.Context, cs *chatState, sourceID, userID, sessionID string) {
	subCtx, cancel := context.WithCancel(ctx)
	cleanup, started := cs.PrepareSubscription(sessionID, cancel, false)
	if !started {
		if cleanup != nil {
			cleanup()
		}
		return
	}
	if cleanup != nil {
		cleanup()
	}

	go b.subscribeLoop(subCtx, cs, sourceID, userID, sessionID)
}

func (b *Bot) startSubscription(ctx context.Context, cs *chatState, sourceID, userID, sessionID string) {
	subCtx, cancel := context.WithCancel(ctx)
	cleanup, _ := cs.PrepareSubscription(sessionID, cancel, true)
	if cleanup != nil {
		cleanup()
	}

	go b.subscribeLoop(subCtx, cs, sourceID, userID, sessionID)
}

func (b *Bot) subscribeLoop(ctx context.Context, cs *chatState, sourceID, userID, sessionID string) {
	user := agent.UserIdentity{
		UserID:   userID,
		Username: "line",
		Role:     auth.RoleAdmin,
	}

	snapshot, next, err := b.agentAPI.SubscribeSession(ctx, sessionID, user)
	if err != nil {
		b.logger.Warn("Failed to subscribe to LINE session",
			slog.String("session", sessionID),
			slog.String("error", err.Error()),
		)
		return
	}

	b.processStreamResponse(ctx, cs, sourceID, snapshot)
	for {
		resp, ok := next()
		if !ok {
			cs.ClearSessionIfActive(sessionID)
			return
		}
		b.processStreamResponse(ctx, cs, sourceID, resp)
	}
}

func (b *Bot) processStreamResponse(ctx context.Context, cs *chatState, sourceID string, resp agent.StreamResponse) {
	var shouldFlushQueued bool
	chatbridge.ProcessStreamResponse(&cs.State, resp, chatbridge.StreamHandlers{
		OnSessionState: func(state agent.SessionState) {
			if !state.Working && state.HasQueuedUserInput {
				shouldFlushQueued = true
			}
		},
		OnAssistant: func(msg agent.Message) {
			b.sendLongText(ctx, sourceID, msg.Content)
		},
		OnError: func(msg agent.Message) {
			b.sendText(ctx, sourceID, "Error: "+msg.Content)
		},
		OnPrompt: func(prompt *agent.UserPrompt) {
			b.sendPrompt(ctx, cs, sourceID, prompt)
		},
	})
	if shouldFlushQueued {
		_, ownerUserID := cs.ActiveSession()
		if ownerUserID == "" {
			return
		}
		user := agent.UserIdentity{
			UserID:   ownerUserID,
			Username: "line",
			Role:     auth.RoleAdmin,
		}
		result, err := chatbridge.FlushQueuedMessage(ctx, b.agentAPI, &cs.State, user)
		if err != nil {
			b.logger.Warn("Failed to flush queued LINE message",
				slog.String("session", result.SessionID),
				slog.String("user", user.UserID),
				slog.String("error", err.Error()),
			)
			return
		}
		if result.Missing {
			b.logger.Warn("Session missing during queued LINE flush",
				slog.String("session", result.SessionID),
				slog.String("user", user.UserID),
			)
			b.resetChat(cs)
			return
		}
		if result.SessionID != "" {
			b.ensureSubscription(ctx, cs, sourceID, user.UserID, result.SessionID)
		}
	}
}

func (b *Bot) sendPrompt(ctx context.Context, cs *chatState, sourceID string, prompt *agent.UserPrompt) {
	cs.setPrompt(prompt)
	text := prompt.Question
	if prompt.Command != "" {
		text += "\n\nCommand: " + prompt.Command
	}
	if len(prompt.Options) > 0 {
		lines := make([]string, 0, len(prompt.Options)+1)
		lines = append(lines, "Options:")
		for _, opt := range prompt.Options {
			label := opt.Label
			if opt.Description != "" {
				label += " - " + opt.Description
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", opt.ID, label))
		}
		text += "\n\n" + strings.Join(lines, "\n")
	}
	if prompt.AllowFreeText {
		text += "\n\nYou can also reply with text."
	}
	b.sendLongText(ctx, sourceID, text)
}

func (b *Bot) sendLongText(ctx context.Context, sourceID, text string) {
	chunks := splitMessage(text, maxLineTextLen)
	for _, chunk := range chunks {
		b.sendText(ctx, sourceID, chunk)
	}
}

func (b *Bot) sendText(ctx context.Context, sourceID, text string) {
	if err := b.client.PushText(ctx, sourceID, text); err != nil {
		b.logger.Warn("Failed to send LINE message",
			slog.String("source_id", sourceID),
			slog.String("error", err.Error()),
		)
	}
}

func (b *Bot) replyText(ctx context.Context, replyToken, text string) {
	if err := b.client.ReplyText(ctx, replyToken, text); err != nil {
		b.logger.Warn("Failed to reply to LINE webhook",
			slog.String("error", err.Error()),
		)
	}
}

func (b *Bot) isSourceAllowed(sourceID string) bool {
	_, ok := b.allowedSources[sourceID]
	return ok
}

func (b *Bot) getOrCreateChat(sourceID string) *chatState {
	val, _ := b.chats.LoadOrStore(sourceID, &chatState{})
	return val.(*chatState)
}

func (b *Bot) getOrCreateNotificationChat(sourceID string) *chatState {
	return b.getOrCreateChat(sourceID)
}

func (b *Bot) resetChat(cs *chatState) {
	if cancel := cs.Reset(); cancel != nil {
		cancel()
	}
	cs.clearPrompt()
}

func (b *Bot) userIdentity(sourceID string) agent.UserIdentity {
	return agent.UserIdentity{
		UserID:   "line:" + sourceID,
		Username: "line",
		Role:     auth.RoleAdmin,
	}
}

func (b *Bot) setActiveSession(cs *chatState, sessionID, ownerUserID string) {
	cs.SetActiveSession(sessionID, ownerUserID)
}

func (b *Bot) markDelivered(cs *chatState, seq int64) {
	cs.MarkDelivered(seq)
}

func (b *Bot) clearPendingMessages(cs *chatState) {
	cs.ClearPendingMessages()
}

func (b *Bot) subscriptionActive(cs *chatState, sessionID string) bool {
	return cs.SubscriptionActive(sessionID)
}

func (b *Bot) claimWebhookEvent(event webhookEvent) bool {
	eventID := event.WebhookEventID
	if eventID == "" {
		return true
	}

	now := time.Now()
	b.processedEventMu.Lock()
	defer b.processedEventMu.Unlock()

	if b.processedEvents == nil {
		b.processedEvents = make(map[string]time.Time)
	}
	if _, ok := b.processedEvents[eventID]; ok {
		return false
	}
	b.processedEvents[eventID] = now
	if len(b.processedEvents) > maxProcessedWebhookEvents {
		b.pruneProcessedWebhookEventsLocked(now)
	}
	return true
}

func (b *Bot) pruneProcessedWebhookEventsLocked(now time.Time) {
	cutoff := now.Add(-processedWebhookEventTTL)
	for eventID, processedAt := range b.processedEvents {
		if processedAt.Before(cutoff) {
			delete(b.processedEvents, eventID)
		}
	}
	for eventID := range b.processedEvents {
		if len(b.processedEvents) <= maxProcessedWebhookEvents {
			return
		}
		delete(b.processedEvents, eventID)
	}
}

func (cs *chatState) setPrompt(prompt *agent.UserPrompt) {
	cs.SetPendingPrompt(prompt.PromptID)
	cs.promptMu.Lock()
	defer cs.promptMu.Unlock()
	cs.pendingPromptFreeText = prompt.AllowFreeText
	cs.pendingPromptOptions = make(map[string]string, len(prompt.Options)*2)
	for _, opt := range prompt.Options {
		cs.pendingPromptOptions[strings.ToLower(strings.TrimSpace(opt.ID))] = opt.ID
		cs.pendingPromptOptions[strings.ToLower(strings.TrimSpace(opt.Label))] = opt.ID
	}
}

func (cs *chatState) responseForPrompt(promptID, text string) (agent.UserPromptResponse, bool) {
	cs.promptMu.Lock()
	defer cs.promptMu.Unlock()
	normalized := strings.ToLower(strings.TrimSpace(text))
	if optionID, ok := cs.pendingPromptOptions[normalized]; ok {
		return agent.UserPromptResponse{
			PromptID:          promptID,
			SelectedOptionIDs: []string{optionID},
		}, true
	}
	if cs.pendingPromptFreeText || len(cs.pendingPromptOptions) == 0 {
		return agent.UserPromptResponse{
			PromptID:         promptID,
			FreeTextResponse: text,
		}, true
	}
	return agent.UserPromptResponse{}, false
}

func (cs *chatState) clearPrompt() {
	cs.promptMu.Lock()
	defer cs.promptMu.Unlock()
	cs.pendingPromptOptions = nil
	cs.pendingPromptFreeText = false
}

func splitMessage(text string, maxLen int) []string {
	if maxLen <= 0 || len(text) <= maxLen {
		return []string{text}
	}

	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		if len(string(runes)) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		cut := maxRunesWithinBytes(runes, maxLen)
		if cut == 0 {
			cut = 1
		}
		if idx := lastRuneSequenceIndex(runes[:cut], '\n', '\n'); idx > cut/2 {
			cut = idx + 2
		} else if idx := lastRuneSequenceIndex(runes[:cut], '\n'); idx > cut/2 {
			cut = idx + 1
		}

		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}

	return chunks
}

func maxRunesWithinBytes(runes []rune, maxLen int) int {
	var size int
	for i, r := range runes {
		next := utf8.RuneLen(r)
		if next < 0 {
			next = len(string(r))
		}
		if size+next > maxLen {
			return i
		}
		size += next
	}
	return len(runes)
}

func lastRuneSequenceIndex(runes []rune, seq ...rune) int {
	if len(seq) == 0 || len(seq) > len(runes) {
		return -1
	}
	for i := len(runes) - len(seq); i >= 0; i-- {
		match := true
		for j, r := range seq {
			if runes[i+j] != r {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

type webhookRequest struct {
	Destination string         `json:"destination"`
	Events      []webhookEvent `json:"events"`
}

type webhookEvent struct {
	Type            string          `json:"type"`
	ReplyToken      string          `json:"replyToken"`
	WebhookEventID  string          `json:"webhookEventId"`
	DeliveryContext deliveryContext `json:"deliveryContext"`
	Source          eventSource     `json:"source"`
	Message         eventMessage    `json:"message"`
}

type deliveryContext struct {
	IsRedelivery bool `json:"isRedelivery"`
}

type eventSource struct {
	Type    string `json:"type"`
	UserID  string `json:"userId"`
	GroupID string `json:"groupId"`
	RoomID  string `json:"roomId"`
}

func (s eventSource) ID() string {
	switch s.Type {
	case "user":
		return s.UserID
	case "group":
		return s.GroupID
	case "room":
		return s.RoomID
	default:
		return ""
	}
}

type eventMessage struct {
	Type    string       `json:"type"`
	ID      string       `json:"id"`
	Text    string       `json:"text"`
	Mention *lineMention `json:"mention"`
}

func (m eventMessage) SelfMentioned() bool {
	if m.Mention == nil {
		return false
	}
	for _, mentionee := range m.Mention.Mentionees {
		if mentionee.IsSelf {
			return true
		}
	}
	return false
}

type lineMention struct {
	Mentionees []lineMentionee `json:"mentionees"`
}

type lineMentionee struct {
	Index  int  `json:"index"`
	Length int  `json:"length"`
	IsSelf bool `json:"isSelf"`
}

func stripSelfMention(text string, mention *lineMention) string {
	if mention == nil {
		return strings.TrimSpace(text)
	}
	for _, mentionee := range mention.Mentionees {
		if !mentionee.IsSelf || mentionee.Index != 0 || mentionee.Length <= 0 {
			continue
		}
		runes := []rune(text)
		if mentionee.Length >= len(runes) {
			return ""
		}
		return strings.TrimSpace(string(runes[mentionee.Length:]))
	}
	return strings.TrimSpace(text)
}
