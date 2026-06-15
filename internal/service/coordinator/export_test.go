// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// RuntimeDispatcherConfigForTest exposes the coordinator client config for external tests.
func RuntimeDispatcherConfigForTest(dispatcher exec.Dispatcher) (*Config, bool) {
	client, ok := dispatcher.(*clientImpl)
	if !ok {
		return nil, false
	}
	return client.config, true
}

// SetDispatchPollWaitForTest overrides distributed poll wait timing.
func SetDispatchPollWaitForTest(t testing.TB, h *Handler, initial, maxWait time.Duration) {
	t.Helper()
	previousInitial := h.dispatchPollInitialWait
	previousMax := h.dispatchPollMaxWait
	h.dispatchPollInitialWait = initial
	h.dispatchPollMaxWait = maxWait
	t.Cleanup(func() {
		h.dispatchPollInitialWait = previousInitial
		h.dispatchPollMaxWait = previousMax
	})
}

// NotifyDispatchAvailableForTest wakes distributed pollers.
func NotifyDispatchAvailableForTest(h *Handler) {
	h.notifyDispatchAvailable()
}
