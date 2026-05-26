// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"testing"
	"time"

	testutil "github.com/dagucloud/dagu/internal/test"
)

// Harness provides semantic integration-test helpers with platform details hidden behind adapters.
type Harness struct {
	t      *testing.T
	Helper testutil.Helper

	Commands Commands
	Wait     Waiter
}

// New creates a portable integration harness around an existing test helper.
func New(t *testing.T, helper testutil.Helper) Harness {
	t.Helper()

	return Harness{
		t:        t,
		Helper:   helper,
		Commands: defaultCommands(),
		Wait:     Waiter{t: t},
	}
}

// Timeout scales timeout for the current test platform.
func (h Harness) Timeout(timeout time.Duration) time.Duration {
	return h.Wait.Timeout(timeout)
}
