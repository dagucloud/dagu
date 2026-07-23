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

func TestStartControllerRunnerRunsUntilCanceled(t *testing.T) {
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

	wait := s.startControllerRunner(context.Background())
	require.NotNil(t, wait)
	// The runner starts independently of the wait closure returned to Start.
	<-started

	s.lock.Lock()
	cancel := s.controllerCancel
	s.lock.Unlock()
	require.NotNil(t, cancel)
	cancel()
	wait()
	<-finished
}

func TestStartControllerRunnerDoesNotStartAfterStop(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	s := &Scheduler{
		quit: make(chan any),
		controllerRunner: backgroundRunnerFunc(func(context.Context) {
			close(started)
		}),
	}
	close(s.quit)

	require.Nil(t, s.startControllerRunner(context.Background()))
	select {
	case <-started:
		t.Fatal("controller runner started after shutdown")
	default:
	}
}
