// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// TestHooks exposes selected internal scheduler hooks to external tests only.
type TestHooks struct {
	OnLockWait func()
}

func NewWithHooksForTest(
	cfg *config.Config,
	deps Dependencies,
	hooks TestHooks,
) (*Scheduler, error) {
	return newScheduler(
		cfg,
		deps.EntryReader,
		deps.DAGRunManager,
		deps.DAGRepository,
		deps.DAGRunRepository,
		deps.QueueStore,
		deps.ProcRepository,
		deps.ServiceRegistry,
		deps.CoordinatorClient,
		deps.SchedulerStateStore,
		deps.SchedulerPauseStore,
		schedulerHooks{onLockWait: hooks.OnLockWait},
		nil,
	)
}

func (s *RetryScanner) ScanForTest(ctx context.Context) error {
	return s.scan(ctx)
}

func UnlaunchableExecutableForTest(t *testing.T) string {
	return unlaunchableExecutable(t)
}

func LocalLaunchFailedForTest(err error) bool {
	return localLaunchFailed(err)
}

func NewStartupExecutionErrorForTest(err error) error {
	return newStartupExecutionError(err)
}
