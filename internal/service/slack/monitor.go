// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package slack

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/eventstore"
)

// DAGRunMonitor watches for DAG run completions and sends notifications via Slack.
type DAGRunMonitor struct {
	core     *chatbridge.NotificationMonitor
	agentAPI AgentService
	bot      *Bot
	logger   *slog.Logger
}

// NewDAGRunMonitor creates a new monitor instance.
func NewDAGRunMonitor(eventService *eventstore.Service, stateFile string, agentAPI AgentService, bot *Bot, logger *slog.Logger) *DAGRunMonitor {
	return newDAGRunMonitorWithWindows(
		eventService,
		stateFile,
		agentAPI,
		bot,
		logger,
		chatbridge.DefaultUrgentNotificationWindow,
		chatbridge.DefaultSuccessNotificationWindow,
	)
}

func newDAGRunMonitorWithWindows(
	eventService *eventstore.Service,
	stateFile string,
	agentAPI AgentService,
	bot *Bot,
	logger *slog.Logger,
	urgentWindow, successWindow time.Duration,
) *DAGRunMonitor {
	monitor := &DAGRunMonitor{
		agentAPI: agentAPI,
		bot:      bot,
		logger:   logger,
	}
	cfg := chatbridge.DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = urgentWindow
	cfg.SuccessWindow = successWindow
	cfg.InterestedEventTypes = slackInterestedEventTypes(bot.cfg.InterestedEventTypes)
	monitor.core = chatbridge.NewNotificationMonitor(eventService, stateFile, monitor, logger, cfg)
	return monitor
}

func slackInterestedEventTypes(values []string) []eventstore.EventType {
	if values == nil {
		return nil
	}
	result := make([]eventstore.EventType, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		result = append(result, eventstore.EventType(value))
	}
	return result
}

// Run starts the monitor loop.
func (m *DAGRunMonitor) Run(ctx context.Context) {
	m.core.Run(ctx)
}

func (m *DAGRunMonitor) notifyCompletion(_ context.Context, s *exec.DAGRunStatus) bool {
	return m.core.NotifyCompletion(s)
}

func (m *DAGRunMonitor) isSeen(destination string, s *exec.DAGRunStatus) bool {
	return m.core.IsDelivered(destination, s)
}

// NotificationDestinations returns the configured Slack channels and DMs.
func (m *DAGRunMonitor) NotificationDestinations() []string {
	destinations := make([]string, 0, len(m.bot.allowedChannels))
	for channelID := range m.bot.allowedChannels {
		destinations = append(destinations, channelID)
	}
	return destinations
}

// FlushNotificationBatch delivers a single notification batch to Slack.
func (m *DAGRunMonitor) FlushNotificationBatch(ctx context.Context, channelID string, batch chatbridge.NotificationBatch, allowLLM bool) bool {
	if strings.HasPrefix(channelID, "D") {
		return m.flushDirectMessage(ctx, channelID, batch, allowLLM)
	}
	return m.flushChannelThread(ctx, channelID, batch, allowLLM)
}

func (m *DAGRunMonitor) flushDirectMessage(ctx context.Context, channelID string, batch chatbridge.NotificationBatch, allowLLM bool) bool {
	convKey := channelID
	user := m.bot.userIdentity(convKey)
	cs := m.bot.getOrCreateChat(convKey, channelID, "")
	sessionID := m.currentSessionID(cs)

	msg := m.buildNotificationMessage(ctx, sessionID, user, batch, allowLLM)
	sessionID, stored, ok := m.appendNotification(ctx, cs, sessionID, user, chatbridge.NotificationBatchDAGName(batch), msg)
	if !ok {
		return false
	}
	if m.bot.subscriptionActive(cs, sessionID) {
		return true
	}

	m.bot.sendLongText(channelID, msg.Content)
	m.bot.markDelivered(cs, stored.SequenceID)
	return true
}

func (m *DAGRunMonitor) flushChannelThread(ctx context.Context, channelID string, batch chatbridge.NotificationBatch, allowLLM bool) bool {
	msg := m.buildNotificationMessage(ctx, "", m.bot.userIdentity(channelID), batch, allowLLM)
	threadTS := m.bot.sendLongRootThread(channelID, msg.Content)
	if threadTS == "" {
		return false
	}
	m.bot.recordRecentGatewayEvents(channelID, batch)

	threadKey := channelID + ":" + threadTS
	m.bot.activeThreads.Store(threadKey, true)

	cs := m.bot.getOrCreateChat(threadKey, channelID, threadTS)
	user := m.bot.userIdentity(threadKey)

	sessionID, stored, err := chatbridge.AppendNotification(ctx, m.agentAPI, &cs.State, user, chatbridge.NotificationBatchDAGName(batch), m.bot.cfg.SafeMode, msg)
	if err != nil {
		m.logger.Warn("Failed to append threaded notification message",
			slog.String("session", sessionID),
			slog.String("channel_id", channelID),
			slog.String("error", err.Error()),
		)
		return true
	}
	m.bot.markDelivered(cs, stored.SequenceID)
	return true
}

func (m *DAGRunMonitor) currentSessionID(cs *chatState) string {
	return cs.SessionID()
}

func (m *DAGRunMonitor) appendNotification(ctx context.Context, cs *chatState, sessionID string, user agent.UserIdentity, dagName string, msg agent.Message) (string, agent.Message, bool) {
	newSessionID, stored, err := chatbridge.AppendNotification(ctx, m.agentAPI, &cs.State, user, dagName, m.bot.cfg.SafeMode, msg)
	if err != nil {
		m.logger.Warn("Failed to append notification message",
			slog.String("session", sessionID),
			slog.String("error", err.Error()),
		)
		return "", agent.Message{}, false
	}
	return newSessionID, stored, true
}

func (m *DAGRunMonitor) buildNotificationMessage(ctx context.Context, sessionID string, user agent.UserIdentity, batch chatbridge.NotificationBatch, allowLLM bool) agent.Message {
	service := m.agentAPI
	if !allowLLM {
		service = nil
	}

	msg, err := chatbridge.GenerateNotificationMessage(ctx, service, sessionID, user, batch)
	if err != nil && len(batch.Events) > 0 && batch.Events[0].Status != nil {
		m.logger.Warn("Failed to generate AI notification, using deterministic fallback",
			slog.String("dag", batch.Events[0].Status.Name),
			slog.String("status", batch.Events[0].Status.Status.String()),
			slog.String("error", err.Error()),
		)
	}
	return msg
}
