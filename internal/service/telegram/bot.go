// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package telegram provides a Telegram bot service that bridges Telegram
// chats with the Dagu AI agent, allowing users to interact with the agent
// via Telegram messages.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// maxTelegramMessageLen is the maximum length for a single Telegram message.
const maxTelegramMessageLen = 4096
const defaultIncomingBatchDelay = 750 * time.Millisecond
const defaultTypingRefreshInterval = 4 * time.Second

// AgentService is the subset of the agent API that the Telegram bot requires.
// Defining this as an interface decouples the bot from the concrete agent.API
// implementation, making it testable in isolation.
type AgentService = chatbridge.AgentService

type telegramAPI interface {
	GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
	StopReceivingUpdates()
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

// Config holds configuration for the Telegram bot.
type Config struct {
	Token                 string
	AllowedChatIDs        []int64
	InterestedEventTypes  []string
	SafeMode              bool
	EventService          *eventstore.Service
	NotificationStateFile string
}

// chatState tracks the agent session state for a single Telegram chat.
type chatState struct {
	chatbridge.State

	typingMu      sync.Mutex
	typingCancel  context.CancelFunc
	typingDone    chan struct{}
	typingLoopGen uint64
}

// Bot is a Telegram bot that forwards messages to the Dagu agent API.
type Bot struct {
	cfg                   Config
	agentAPI              AgentService
	botAPI                telegramAPI
	selfUsername          string
	chats                 sync.Map // chatID (int64) -> *chatState
	allowedChats          map[int64]struct{}
	eventService          *eventstore.Service
	notificationStateFile string
	logger                *slog.Logger
	incomingDelay         time.Duration
	incomingAfterFunc     func(time.Duration, func())
	typingDelay           time.Duration
}

// New creates a new Telegram bot instance.
func New(cfg Config, agentAPI AgentService, logger *slog.Logger) (*Bot, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("telegram bot token is required (set DAGU_BOTS_TELEGRAM_TOKEN)")
	}
	if len(cfg.AllowedChatIDs) == 0 {
		return nil, fmt.Errorf("at least one allowed chat ID is required (set DAGU_BOTS_TELEGRAM_ALLOWED_CHAT_IDS)")
	}

	botAPI, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %w", err)
	}

	allowed := make(map[int64]struct{}, len(cfg.AllowedChatIDs))
	for _, id := range cfg.AllowedChatIDs {
		allowed[id] = struct{}{}
	}

	return &Bot{
		cfg:                   cfg,
		agentAPI:              agentAPI,
		botAPI:                botAPI,
		selfUsername:          botAPI.Self.UserName,
		allowedChats:          allowed,
		eventService:          cfg.EventService,
		notificationStateFile: cfg.NotificationStateFile,
		logger:                logger,
		incomingDelay:         defaultIncomingBatchDelay,
		incomingAfterFunc:     func(delay time.Duration, f func()) { time.AfterFunc(delay, f) },
		typingDelay:           defaultTypingRefreshInterval,
	}, nil
}

// Run starts the bot and blocks until the context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.logger.Info("Telegram bot started",
		slog.String("username", b.selfUsername),
		slog.Int("allowed_chats", len(b.allowedChats)),
	)

	// Start DAG run monitor if the event store is available.
	if b.eventService != nil {
		monitor := NewDAGRunMonitor(b.eventService, b.notificationStateFile, b.agentAPI, b, b.logger)
		go monitor.Run(ctx)
	} else {
		b.logger.Warn("Event store is not configured; DAG run notifications are disabled")
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := b.botAPI.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			b.botAPI.StopReceivingUpdates()
			b.logger.Info("Telegram bot stopped")
			return nil

		case update := <-updates:
			if update.CallbackQuery != nil {
				b.handleCallbackQuery(ctx, update.CallbackQuery)
				continue
			}
			if update.Message == nil {
				continue
			}
			b.handleMessage(ctx, update.Message)
		}
	}
}

// handleMessage processes an incoming Telegram message.
func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID

	if !b.isChatAllowed(chatID) {
		b.sendText(chatID, "This bot is not authorized for this chat.")
		return
	}

	// Handle commands
	if msg.IsCommand() {
		b.handleCommand(ctx, msg)
		return
	}

	text := msg.Text
	if text == "" {
		return
	}

	// Check if this is a response to a pending prompt
	cs := b.getOrCreateChat(chatID)
	pendingPrompt := cs.PendingPromptID()

	if pendingPrompt != "" {
		b.submitPromptResponse(ctx, cs, chatID, pendingPrompt, text)
		return
	}

	// Send message to agent
	b.enqueueIncomingMessage(ctx, cs, chatID, text)
}

// handleCommand processes bot commands.
func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	cs := b.getOrCreateChat(chatID)

	switch msg.Command() {
	case "new":
		b.clearPendingMessages(cs)
		b.resetChat(cs)
		b.sendText(chatID, "Session cleared. Send a message to start a new conversation.")

	case "cancel":
		b.clearPendingMessages(cs)
		b.stopTypingLoop(cs)
		sid, ownerUID := cs.ActiveSession()

		if sid == "" {
			b.sendText(chatID, "No active session.")
			return
		}

		if err := b.agentAPI.CancelSession(ctx, sid, ownerUID); err != nil {
			b.sendText(chatID, "Failed to cancel session: "+err.Error())
			return
		}
		b.stopTypingLoop(cs)
		b.sendText(chatID, "Session cancelled.")

	case "start":
		b.sendText(chatID, "Welcome to Dagu AI Agent! Send any message to start chatting.\n\nCommands:\n/new - Start a new session\n/cancel - Cancel current session")

	default:
		b.sendText(chatID, "Unknown command. Use /new, /cancel, or just send a message.")
	}
}

func (b *Bot) enqueueIncomingMessage(ctx context.Context, cs *chatState, chatID int64, text string) {
	if text == "" {
		return
	}

	b.startTypingLoop(ctx, cs, chatID)
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
		b.flushIncomingMessages(ctx, cs, chatID, gen)
	})
}

func (b *Bot) flushIncomingMessages(ctx context.Context, cs *chatState, chatID int64, gen uint64) {
	text, ok := cs.TakePendingMessages(gen, "\n\n")
	if !ok {
		return
	}

	user := b.userIdentity(chatID)
	if cs.SessionID() == "" {
		b.createSession(ctx, cs, chatID, user, text)
		return
	}

	b.sendMessage(ctx, cs, chatID, user, text)
}

// handleCallbackQuery processes inline keyboard button presses.
func (b *Bot) handleCallbackQuery(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	chatID := cq.Message.Chat.ID

	// Parse callback data: "prompt:<promptID>:<optionID>"
	parts := strings.SplitN(cq.Data, ":", 3)
	if len(parts) != 3 || parts[0] != "prompt" {
		callback := tgbotapi.NewCallback(cq.ID, "Invalid action")
		_, _ = b.botAPI.Send(callback)
		return
	}

	promptID := parts[1]
	optionID := parts[2]

	cs := b.getOrCreateChat(chatID)
	sid, ownerUID := cs.ActiveSession()
	cs.ClearPendingPrompt()

	if sid == "" {
		callback := tgbotapi.NewCallback(cq.ID, "No active session")
		_, _ = b.botAPI.Send(callback)
		return
	}

	resp := agent.UserPromptResponse{
		PromptID:          promptID,
		SelectedOptionIDs: []string{optionID},
	}

	b.startTypingLoop(ctx, cs, chatID)
	if err := b.agentAPI.SubmitUserResponse(ctx, sid, ownerUID, resp); err != nil {
		b.stopTypingLoop(cs)
		b.logger.Warn("Failed to submit prompt response",
			slog.String("session", sid),
			slog.String("user", ownerUID),
			slog.String("prompt", promptID),
			slog.String("error", err.Error()),
		)
		// Send the actual error to Telegram so user can see what went wrong
		b.sendText(chatID, fmt.Sprintf("Failed to submit response: %s", err.Error()))
		callback := tgbotapi.NewCallback(cq.ID, "Error - see message")
		_, _ = b.botAPI.Send(callback)
		return
	}

	// Acknowledge and update the message
	callback := tgbotapi.NewCallback(cq.ID, "Response submitted")
	_, _ = b.botAPI.Send(callback)

	// Edit the original message to show the selection
	edit := tgbotapi.NewEditMessageText(chatID, cq.Message.MessageID,
		cq.Message.Text+"\n\nSelected: "+optionID)
	_, _ = b.botAPI.Send(edit)
}

// sendTyping sends a "typing..." indicator to the Telegram chat.
func (b *Bot) sendTyping(chatID int64) {
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	_, _ = b.botAPI.Send(action)
}

func (b *Bot) startTypingLoop(ctx context.Context, cs *chatState, chatID int64) {
	cs.typingMu.Lock()
	if cs.typingCancel != nil {
		cs.typingMu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	cs.typingLoopGen++
	gen := cs.typingLoopGen
	cs.typingCancel = cancel
	cs.typingDone = done
	cs.typingMu.Unlock()

	if !b.sendTypingIfCurrent(cs, gen, chatID) {
		close(done)
		b.finishTypingLoop(cs, gen)
		return
	}

	refresh := b.typingDelay
	if refresh <= 0 {
		refresh = defaultTypingRefreshInterval
	}

	go func() {
		ticker := time.NewTicker(refresh)
		defer ticker.Stop()
		defer close(done)
		defer b.finishTypingLoop(cs, gen)

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				select {
				case <-loopCtx.Done():
					return
				default:
				}
				if !b.sendTypingIfCurrent(cs, gen, chatID) {
					return
				}
			}
		}
	}()
}

func (b *Bot) stopTypingLoop(cs *chatState) {
	cs.typingMu.Lock()
	cancel := cs.typingCancel
	done := cs.typingDone
	cs.typingCancel = nil
	cs.typingDone = nil
	cs.typingMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (b *Bot) finishTypingLoop(cs *chatState, gen uint64) {
	cs.typingMu.Lock()
	defer cs.typingMu.Unlock()
	if cs.typingLoopGen == gen {
		cs.typingCancel = nil
		cs.typingDone = nil
	}
}

func (b *Bot) sendTypingIfCurrent(cs *chatState, gen uint64, chatID int64) bool {
	cs.typingMu.Lock()
	defer cs.typingMu.Unlock()
	if cs.typingLoopGen != gen || cs.typingCancel == nil {
		return false
	}
	b.sendTyping(chatID)
	return true
}

// createSession creates a new agent session and starts listening for responses.
func (b *Bot) createSession(ctx context.Context, cs *chatState, chatID int64, user agent.UserIdentity, text string) {
	b.startTypingLoop(ctx, cs, chatID)

	req := agent.ChatRequest{
		Message:  text,
		SafeMode: b.cfg.SafeMode,
	}

	sessionID, _, err := b.agentAPI.CreateSession(ctx, user, req)
	if err != nil {
		b.stopTypingLoop(cs)
		b.logger.Error("Failed to create session", slog.String("error", err.Error()))
		b.sendText(chatID, "Failed to start session: "+err.Error())
		return
	}

	b.setActiveSession(cs, sessionID, user.UserID)
	b.startSubscription(ctx, cs, chatID, user.UserID, sessionID)
}

// sendMessage sends a message to an existing session.
func (b *Bot) sendMessage(ctx context.Context, cs *chatState, chatID int64, user agent.UserIdentity, text string) {
	b.startTypingLoop(ctx, cs, chatID)

	req := agent.ChatRequest{
		Message:  text,
		SafeMode: b.cfg.SafeMode,
	}

	result, err := chatbridge.EnqueueMessage(ctx, b.agentAPI, &cs.State, user, req)
	if err != nil {
		b.stopTypingLoop(cs)
		b.logger.Error("Failed to enqueue message", slog.String("error", err.Error()))
		b.sendText(chatID, "Failed to send message: "+err.Error())
		return
	}
	if result.Missing {
		b.logger.Warn("Session missing during Telegram send, recreating chat",
			slog.String("session", result.SessionID),
			slog.String("user", user.UserID),
		)
		b.resetChat(cs)
		b.createSession(ctx, cs, chatID, user, text)
		return
	}
	sid := result.SessionID
	if sid == "" {
		sid = cs.SessionID()
	}
	if result.Queued {
		b.startTypingLoop(ctx, cs, chatID)
	}

	// Ensure a subscription is running (but don't restart if already active for this session)
	if sid != "" {
		b.ensureSubscription(ctx, cs, chatID, user.UserID, sid)
	}
}

// submitPromptResponse submits a text response to a pending agent prompt.
func (b *Bot) submitPromptResponse(ctx context.Context, cs *chatState, chatID int64, promptID, text string) {
	sid, ownerUserID := cs.ActiveSession()
	cs.ClearPendingPrompt()

	if sid == "" {
		return
	}

	resp := agent.UserPromptResponse{
		PromptID:         promptID,
		FreeTextResponse: text,
	}

	b.startTypingLoop(ctx, cs, chatID)
	if err := b.agentAPI.SubmitUserResponse(ctx, sid, ownerUserID, resp); err != nil {
		b.stopTypingLoop(cs)
		b.sendText(chatID, "Failed to submit response: "+err.Error())
	}
}

// ensureSubscription starts a subscription only if one isn't already running
// for the given session. Safe to call on every message.
func (b *Bot) ensureSubscription(ctx context.Context, cs *chatState, chatID int64, userID, sessionID string) {
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

	go b.subscribeLoop(subCtx, cs, chatID, userID, sessionID)
}

// startSubscription cancels any existing subscription and starts a fresh one.
// Use this when creating a new session or adopting a notification session.
func (b *Bot) startSubscription(ctx context.Context, cs *chatState, chatID int64, userID, sessionID string) {
	subCtx, cancel := context.WithCancel(ctx)
	cleanup, _ := cs.PrepareSubscription(sessionID, cancel, true)
	if cleanup != nil {
		cleanup()
	}

	go b.subscribeLoop(subCtx, cs, chatID, userID, sessionID)
}

// subscribeLoop uses the agent's built-in pub-sub to receive session updates
// in real time and forward them to Telegram.
func (b *Bot) subscribeLoop(ctx context.Context, cs *chatState, chatID int64, userID, sessionID string) {
	user := agent.UserIdentity{
		UserID:   userID,
		Username: "telegram",
		Role:     auth.RoleAdmin,
	}

	snapshot, next, err := b.agentAPI.SubscribeSession(ctx, sessionID, user)
	if err != nil {
		b.logger.Warn("Failed to subscribe to session",
			slog.String("session", sessionID),
			slog.String("error", err.Error()),
		)
		return
	}

	// Process the initial snapshot — skip user messages (we sent those),
	// but forward any assistant/error/prompt messages that arrived before
	// the subscription started.
	b.processStreamResponse(ctx, cs, chatID, snapshot)

	// Listen for real-time updates via the pub-sub channel.
	for {
		resp, ok := next()
		if !ok {
			b.stopTypingLoop(cs)
			// Session ended — clear the session ID so the next message
			// creates a fresh session instead of reusing a dead one.
			cs.ClearSessionIfActive(sessionID)
			return
		}
		b.processStreamResponse(ctx, cs, chatID, resp)
	}
}

// processStreamResponse handles a stream response and sends relevant content to Telegram.
func (b *Bot) processStreamResponse(ctx context.Context, cs *chatState, chatID int64, resp agent.StreamResponse) {
	var shouldFlushQueued bool
	chatbridge.ProcessStreamResponse(&cs.State, resp, chatbridge.StreamHandlers{
		OnSessionState: func(state agent.SessionState) {
			if !state.Working && state.HasQueuedUserInput {
				shouldFlushQueued = true
				b.startTypingLoop(ctx, cs, chatID)
			}
		},
		OnWorking: func(working bool) {
			if working {
				b.startTypingLoop(ctx, cs, chatID)
			} else if !cs.HasQueuedUserInput() {
				b.stopTypingLoop(cs)
			}
		},
		OnAssistant: func(msg agent.Message) {
			b.stopTypingLoop(cs)
			b.sendLongText(chatID, msg.Content)
		},
		OnError: func(msg agent.Message) {
			b.stopTypingLoop(cs)
			b.sendText(chatID, "Error: "+msg.Content)
		},
		OnPrompt: func(prompt *agent.UserPrompt) {
			b.stopTypingLoop(cs)
			b.sendPrompt(cs, chatID, prompt)
		},
	})
	if shouldFlushQueued {
		_, ownerUserID := cs.ActiveSession()
		if ownerUserID == "" {
			return
		}
		user := agent.UserIdentity{
			UserID:   ownerUserID,
			Username: "telegram",
			Role:     auth.RoleAdmin,
		}
		result, err := chatbridge.FlushQueuedMessage(ctx, b.agentAPI, &cs.State, user)
		if err != nil {
			b.logger.Warn("Failed to flush queued Telegram message",
				slog.String("session", result.SessionID),
				slog.String("user", user.UserID),
				slog.String("error", err.Error()),
			)
			return
		}
		if result.Missing {
			b.logger.Warn("Session missing during queued Telegram flush",
				slog.String("session", result.SessionID),
				slog.String("user", user.UserID),
			)
			b.resetChat(cs)
			return
		}
		if result.SessionID != "" {
			b.ensureSubscription(ctx, cs, chatID, user.UserID, result.SessionID)
		}
	}
}

// sendPrompt sends a user prompt with inline keyboard buttons.
func (b *Bot) sendPrompt(cs *chatState, chatID int64, prompt *agent.UserPrompt) {
	cs.SetPendingPrompt(prompt.PromptID)

	text := prompt.Question
	if prompt.Command != "" {
		text += "\n\nCommand: " + prompt.Command
	}
	if prompt.AllowFreeText {
		text += "\n\n(You can also reply with text)"
	}

	if len(prompt.Options) > 0 {
		var rows [][]tgbotapi.InlineKeyboardButton
		for _, opt := range prompt.Options {
			callbackData := fmt.Sprintf("prompt:%s:%s", prompt.PromptID, opt.ID)
			label := opt.Label
			if opt.Description != "" {
				label += " - " + opt.Description
			}
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(label, callbackData),
			))
		}

		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
		if _, err := b.botAPI.Send(msg); err != nil {
			b.logger.Warn("Failed to send prompt", slog.String("error", err.Error()))
		}
	} else {
		b.sendText(chatID, text)
	}
}

// sendLongText sends a message, splitting it if it exceeds Telegram's limit.
func (b *Bot) sendLongText(chatID int64, text string) {
	chunks := splitMessage(text, maxTelegramMessageLen)
	for _, chunk := range chunks {
		b.sendText(chatID, chunk)
	}
}

// sendText sends a simple text message to a Telegram chat.
func (b *Bot) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.botAPI.Send(msg); err != nil {
		b.logger.Warn("Failed to send Telegram message",
			slog.Int64("chat_id", chatID),
			slog.String("error", err.Error()),
		)
	}
}

// isChatAllowed checks if a chat ID is authorized to use the bot.
func (b *Bot) isChatAllowed(chatID int64) bool {
	_, ok := b.allowedChats[chatID]
	return ok
}

// getOrCreateChat returns or creates a chatState for the given chat ID.
func (b *Bot) getOrCreateChat(chatID int64) *chatState {
	val, _ := b.chats.LoadOrStore(chatID, &chatState{})
	return val.(*chatState)
}

// resetChat clears the session state for a chat.
func (b *Bot) resetChat(cs *chatState) {
	if cancel := cs.Reset(); cancel != nil {
		cancel()
	}
	b.stopTypingLoop(cs)
}

// userIdentity creates a chat-scoped UserIdentity so the entire chat shares one session.
func (b *Bot) userIdentity(chatID int64) agent.UserIdentity {
	return agent.UserIdentity{
		UserID:   fmt.Sprintf("telegram:%d", chatID),
		Username: "telegram",
		Role:     auth.RoleAdmin,
	}
}

func (b *Bot) setActiveSession(cs *chatState, sessionID, ownerUserID string) {
	cs.SetActiveSession(sessionID, ownerUserID)
}

func (b *Bot) lastDeliveredSeq(cs *chatState) int64 {
	return cs.LastDeliveredSeq()
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

// splitMessage splits text into chunks that fit within the Telegram message limit.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to split at a paragraph boundary
		cut := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n\n"); idx > maxLen/2 {
			cut = idx + 2
		} else if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			cut = idx + 1
		}

		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}

	return chunks
}
