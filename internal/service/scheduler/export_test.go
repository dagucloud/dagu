// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	procdomain "github.com/dagucloud/dagu/v2/internal/proc"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// TestHooks exposes selected internal scheduler hooks to external tests only.
type TestHooks struct {
	OnLockWait func()
}

func NewWithHooksForTest(
	cfg *config.Config,
	er EntryReader,
	drm runtime.Manager,
	dagRunRepository *dagrun.Repository,
	queueStore queuedomain.QueueStore,
	procStore procdomain.ProcStore,
	reg serviceregistry.ServiceRegistry,
	coordinatorCli dispatch.Dispatcher,
	watermarkStore WatermarkStore,
	hooks TestHooks,
) (*Scheduler, error) {
	return newScheduler(
		cfg,
		er,
		drm,
		dagRunRepository,
		queueStore,
		procStore,
		reg,
		coordinatorCli,
		watermarkStore,
		schedulerHooks{onLockWait: hooks.OnLockWait},
		schedulerOptions{},
	)
}

func (s *RetryScanner) ScanForTest(ctx context.Context) error {
	return s.scan(ctx)
}

func LocalLaunchFailedForTest(err error) bool {
	return localLaunchFailed(err)
}

func NewStartupExecutionErrorForTest(err error) error {
	return newStartupExecutionError(err)
}
