// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type backgroundRunnerFunc func(context.Context)

func (f backgroundRunnerFunc) Run(ctx context.Context) {
	f(ctx)
}

func TestPrepareControllerRunnerRunsUntilCanceled(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	finished := make(chan struct{})
	s := &Scheduler{
		quit: make(chan any),
		controllerRunner: backgroundRunnerFunc(func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			close(finished)
		}),
	}

	run := s.prepareControllerRunner(context.Background())
	require.NotNil(t, run)
	go run()
	<-started

	s.lock.Lock()
	cancel := s.controllerCancel
	s.lock.Unlock()
	require.NotNil(t, cancel)
	cancel()
	s.controllerWG.Wait()

	<-finished
}

func TestPrepareControllerRunnerDoesNotStartAfterStop(t *testing.T) {
	t.Parallel()

	s := &Scheduler{
		quit:             make(chan any),
		controllerRunner: backgroundRunnerFunc(func(context.Context) {}),
	}
	close(s.quit)

	require.Nil(t, s.prepareControllerRunner(context.Background()))
}
