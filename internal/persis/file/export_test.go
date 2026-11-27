// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"testing"
	"time"
)

// SetReleaseWait sets how long and how often a delete waits for processes
// to release a project, until t ends.
func SetReleaseWait(t *testing.T, timeout, poll time.Duration) {
	t.Helper()
	prevTimeout, prevPoll := releaseTimeout, releasePollInterval
	releaseTimeout, releasePollInterval = timeout, poll
	t.Cleanup(func() { releaseTimeout, releasePollInterval = prevTimeout, prevPoll })
}

// SetLifecycleLockWait sets how long a create or delete waits for another
// one, until t ends.
func SetLifecycleLockWait(t *testing.T, wait time.Duration) {
	t.Helper()
	prev := lifecycleLockWait
	lifecycleLockWait = wait
	t.Cleanup(func() { lifecycleLockWait = prev })
}
