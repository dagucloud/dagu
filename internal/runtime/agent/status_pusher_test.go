// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

type blockingStatusPusher struct {
	calls atomic.Int32
	errCh chan error
}

func (p *blockingStatusPusher) Push(ctx context.Context, _ exec.DAGRunStatus) error {
	p.calls.Add(1)
	<-ctx.Done()
	err := ctx.Err()
	p.errCh <- err
	return err
}

func TestPushStatusUsesBoundedContext(t *testing.T) {
	oldTimeout := remoteStatusPushTimeout
	remoteStatusPushTimeout = 25 * time.Millisecond
	t.Cleanup(func() {
		remoteStatusPushTimeout = oldTimeout
	})

	parentCtx, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	pusher := &blockingStatusPusher{errCh: make(chan error, 1)}
	a := &Agent{statusPusher: pusher}

	done := make(chan struct{})
	startedAt := time.Now()
	go func() {
		a.pushStatus(parentCtx, exec.DAGRunStatus{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pushStatus did not return after its timeout")
	}

	require.Less(t, time.Since(startedAt), time.Second)
	require.Equal(t, int32(1), pusher.calls.Load())
	require.ErrorIs(t, <-pusher.errCh, context.DeadlineExceeded)
}
