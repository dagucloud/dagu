// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

type blockingStatusPusher struct {
	calls atomic.Int32
	errCh chan error
}

func (p *blockingStatusPusher) Push(ctx context.Context, _ ir.DAGRunStatus) error {
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
		a.pushStatus(parentCtx, ir.DAGRunStatus{})
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

type orderedStatusPusher struct {
	calls        atomic.Int32
	firstStarted chan struct{}
	releaseFirst chan struct{}

	mu       sync.Mutex
	statuses []ir.DAGRunStatus
}

func (p *orderedStatusPusher) Push(ctx context.Context, status ir.DAGRunStatus) error {
	if p.calls.Add(1) == 1 {
		close(p.firstStarted)
		select {
		case <-p.releaseFirst:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.mu.Lock()
	p.statuses = append(p.statuses, status)
	p.mu.Unlock()
	return nil
}

func (p *orderedStatusPusher) recordedStatuses() []ir.DAGRunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ir.DAGRunStatus(nil), p.statuses...)
}

func TestRecordCurrentStatusPreservesSnapshotOrder(t *testing.T) {
	step := ir.Step{Name: "run-child"}
	plan, err := runtime.NewPlan(step)
	require.NoError(t, err)
	node := plan.Nodes()[0]
	node.SetStatus(ir.NodeRunning)

	pusher := &orderedStatusPusher{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	a := &Agent{
		dag:          &ir.DAG{Name: "parent", Steps: []ir.Step{step}},
		dagRunID:     "parent-run",
		rootDAGRun:   ir.NewDAGRunRef("parent", "parent-run"),
		plan:         plan,
		runner:       runtime.New(&runtime.Config{}),
		statusPusher: pusher,
	}

	firstDone := make(chan struct{})
	go func() {
		a.recordCurrentStatus(t.Context(), nil)
		close(firstDone)
	}()
	<-pusher.firstStarted

	node.SetSubRuns([]runtime.SubDAGRun{{DAGRunID: "child-run", DAGName: "child"}})
	secondStarted := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		close(secondStarted)
		a.recordCurrentStatus(t.Context(), nil)
		close(secondDone)
	}()

	<-secondStarted
	close(pusher.releaseFirst)
	<-firstDone
	<-secondDone

	statuses := pusher.recordedStatuses()
	require.Len(t, statuses, 2)
	require.Len(t, statuses[0].Nodes, 1)
	require.Len(t, statuses[1].Nodes, 1)
	require.Empty(t, statuses[0].Nodes[0].SubRuns)
	require.Equal(t, "child-run", statuses[1].Nodes[0].SubRuns[0].DAGRunID)
}
