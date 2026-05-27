// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
)

// TestHooks exposes selected internal scheduler hooks to external tests only.
type TestHooks struct {
	OnLockWait func()
}

func NewWithHooksForTest(
	cfg *config.Config,
	er EntryReader,
	drm runtime.Manager,
	dagRunStore exec.DAGRunStore,
	queueStore exec.QueueStore,
	procStore exec.ProcStore,
	reg exec.ServiceRegistry,
	coordinatorCli exec.Dispatcher,
	watermarkStore WatermarkStore,
	hooks TestHooks,
) (*Scheduler, error) {
	return newScheduler(
		cfg,
		er,
		drm,
		dagRunStore,
		queueStore,
		procStore,
		reg,
		coordinatorCli,
		watermarkStore,
		schedulerHooks{onLockWait: hooks.OnLockWait},
		schedulerOptions{},
	)
}
