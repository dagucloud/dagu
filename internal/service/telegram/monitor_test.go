// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telegram

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGRunMonitor_RetriesOnlyUndeliveredTelegramChat(t *testing.T) {
	t.Parallel()

	api := &fakeTelegramAPI{}
	service := newFakeTelegramAgentService("ai notification")
	service.appendErrBySession = map[string]error{"session-2": assert.AnError}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &Bot{
		cfg:          Config{SafeMode: true},
		agentAPI:     service,
		botAPI:       api,
		allowedChats: map[int64]struct{}{1: {}, 2: {}},
		logger:       logger,
	}
	cs1 := bot.getOrCreateChat(1)
	cs2 := bot.getOrCreateChat(2)
	bot.setActiveSession(cs1, "session-1", "telegram:1")
	bot.setActiveSession(cs2, "session-2", "telegram:2")

	monitor := newDAGRunMonitorWithWindows(nil, "", service, bot, logger, 10*time.Millisecond, 20*time.Millisecond)
	stopMonitor := testutil.StartContextRunner(t, monitor)
	defer stopMonitor()

	status := &exec.DAGRunStatus{
		Name:      "briefing",
		Status:    core.Failed,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Error:     "boom",
	}

	require.True(t, monitor.notifyCompletion(context.Background(), status))
	require.Eventually(t, func() bool {
		service.mu.Lock()
		session1Attempts := countMatches(service.appendAttempts, "session-1")
		session2Attempts := countMatches(service.appendAttempts, "session-2")
		service.mu.Unlock()
		return session1Attempts == 1 &&
			session2Attempts == 1 &&
			monitor.isSeen("1", status) &&
			!monitor.isSeen("2", status)
	}, time.Second, 10*time.Millisecond)
	assert.True(t, monitor.isSeen("1", status))
	assert.False(t, monitor.isSeen("2", status))

	require.True(t, monitor.notifyCompletion(context.Background(), status))
	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return countMatches(service.appendAttempts, "session-2") == 2
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, api.textCount(), "only the delivered chat should get one direct notification")
	assert.True(t, monitor.isSeen("1", status))
	assert.False(t, monitor.isSeen("2", status))
}

func countMatches(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}
