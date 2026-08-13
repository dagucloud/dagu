// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis"
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
	dagRepository *persis.DAGRepository,
	dagRunRepository *persis.DAGRunRepository,
	queueStore queuedomain.QueueStore,
	procRepository processRepository,
	reg serviceregistry.ServiceRegistry,
	coordinatorCli dispatch.Dispatcher,
	watermarkStore WatermarkStore,
	hooks TestHooks,
) (*Scheduler, error) {
	return newScheduler(
		cfg,
		er,
		drm,
		dagRepository,
		dagRunRepository,
		queueStore,
		procRepository,
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
