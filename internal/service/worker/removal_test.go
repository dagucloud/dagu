// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// removalClient is a coordinator client whose first poll returns task, if
// any, and whose later polls wait until the poller stops. Heartbeats pass.
// poll and preflight, when set, answer the polls after the first and the
// owner checks of a task.
type removalClient struct {
	coordinator.Client
	task      *coordinatorv1.Task
	polled    atomic.Bool
	poll      func(context.Context) error
	preflight func(context.Context) error
	// beforeTask runs before the first poll returns task, acks counts the
	// acknowledgments of its claim, and ack, when set, answers them.
	beforeTask func()
	acks       atomic.Int32
	ack        func() error
	// ackHeld, when set, holds each acknowledgment with its context.
	ackHeld func(context.Context)
}

func (c *removalClient) Poll(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	if !c.polled.Swap(true) && c.task != nil {
		if c.beforeTask != nil {
			c.beforeTask()
		}
		return c.task, nil
	}
	if c.poll != nil {
		if err := c.poll(ctx); err != nil {
			return nil, err
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *removalClient) Heartbeat(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	return &coordinatorv1.HeartbeatResponse{}, nil
}

func (c *removalClient) RunHeartbeatTo(ctx context.Context, _ serviceregistry.HostInfo, _ *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	if c.preflight != nil {
		return nil, c.preflight(ctx)
	}
	return &coordinatorv1.RunHeartbeatResponse{}, nil
}

func (c *removalClient) AckTaskClaimTo(ctx context.Context, _ serviceregistry.HostInfo, _ *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	c.acks.Add(1)
	if c.ackHeld != nil {
		c.ackHeld(ctx)
		return nil, ctx.Err()
	}
	if c.ack != nil {
		if err := c.ack(); err != nil {
			return nil, err
		}
	}
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (c *removalClient) Metrics() coordinator.Metrics { return coordinator.Metrics{IsConnected: true} }

func (c *removalClient) Cleanup(context.Context) error { return nil }

// runHandler runs each task with run.
type runHandler func(context.Context, *coordinatorv1.Task) error

func (h runHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error { return h(ctx, task) }

// errRemovedForTest is the failure a removed worker's membership returns.
var errRemovedForTest = removedFailure{failure{cause: "This worker was removed from the coordinator", next: "test"}}

// refusal is a coordinator's refusal of the worker's certificate as revoked,
// which a membership passes to its worker the moment it arrives.
type refusal struct {
	w *Worker
}

func (r *refusal) refuse() { r.w.publishRemoval() }

// newRemovableWorker returns a worker of client with up to maxActiveRuns runs
// that r removes, as a worker of a membership is.
func newRemovableWorker(client coordinator.Client, maxActiveRuns int, cfg *config.Config, r *refusal, run runHandler) *Worker {
	w := NewWorker("test-worker", maxActiveRuns, client, map[string]string{}, cfg, nil)
	w.removalExit = func(context.Context) error { return errRemovedForTest }
	w.SetHandler(run)
	r.w = w
	return w
}

// startRemovable starts w and returns what Start returned, failing the test
// when the worker does not stop.
func startRemovable(t *testing.T, w *Worker) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- w.Start(t.Context()) }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the worker kept running after its removal")
		return nil
	}
}

// A refusal as revoked on any call of the worker's client stops the worker,
// also while its heartbeats still pass: the run it holds is canceled at once,
// and Start returns the membership's failure once the run has exited (Spec
// 108 §Removed Worker).
func TestWorkerRemoved(t *testing.T) {
	r := new(refusal)
	started := make(chan struct{})
	client := &removalClient{
		task: &coordinatorv1.Task{DagRunId: "run-1", AttemptKey: "attempt-1"},
		poll: func(context.Context) error {
			// A poll refused as revoked: the client reports it first.
			<-started
			r.refuse()
			return status.Error(codes.Unauthenticated, "this worker was removed from the coordinator")
		},
	}
	runErr := make(chan error, 1)
	w := newRemovableWorker(client, 2, &config.Config{}, r, func(ctx context.Context, _ *coordinatorv1.Task) error {
		close(started)
		<-ctx.Done()
		runErr <- ctx.Err()
		return ctx.Err()
	})

	assert.Equal(t, errRemovedForTest, startRemovable(t, w))
	select {
	case err := <-runErr:
		assert.ErrorIs(t, err, context.Canceled)
	default:
		t.Fatal("Start returned before the run was stopped")
	}
	assert.Equal(t, errRemovedForTest, w.Stop(t.Context()), "a later Stop reports the removal")
}

// ownedTask is a task whose owner coordinator the worker checks with before
// it runs the task.
func ownedTask() *coordinatorv1.Task {
	return &coordinatorv1.Task{
		DagRunId: "run-1", AttemptKey: "attempt-1",
		OwnerCoordinatorId: "coordinator-1", OwnerCoordinatorHost: "127.0.0.1", OwnerCoordinatorPort: 50055,
	}
}

// A run that the worker accepted and was still checking with its owner
// coordinator when the worker was removed never reaches the executor: not
// when the removal cancels the check, nor when the owner refuses the check
// as revoked (Spec 108 §Removed Worker).
func TestWorkerRemovedDuringPreflight(t *testing.T) {
	for name, refuseCheck := range map[string]bool{"SignalDuringCheck": false, "CheckRefused": true} {
		t.Run(name, func(t *testing.T) {
			r := new(refusal)
			var once sync.Once
			client := &removalClient{task: ownedTask(), preflight: func(ctx context.Context) error {
				once.Do(func() { r.refuse() })
				if refuseCheck {
					// The client reported the refusal before it returns.
					return status.Error(codes.Unauthenticated, "this worker was removed from the coordinator")
				}
				// The removal lands while the check is under way, which it
				// cancels.
				<-ctx.Done()
				return ctx.Err()
			}}
			var executed atomic.Bool
			w := newRemovableWorker(client, 1, &config.Config{}, r, func(context.Context, *coordinatorv1.Task) error {
				executed.Store(true)
				return nil
			})

			assert.Equal(t, errRemovedForTest, startRemovable(t, w))
			assert.False(t, executed.Load(), "the run reached the executor after the removal")
		})
	}
}

// A removal while a stop drains the runs ends the drain: the runs stop at
// once, and Stop and Start end as removed (Spec 108 §Removed Worker).
func TestWorkerRemovedDuringDrain(t *testing.T) {
	// pollStopped is closed once the stop ends polling, which it does
	// before it drains.
	pollStopped := make(chan struct{})
	var stopPoll sync.Once
	client := &removalClient{
		task: &coordinatorv1.Task{DagRunId: "run-1", AttemptKey: "attempt-1"},
		poll: func(ctx context.Context) error {
			<-ctx.Done()
			stopPoll.Do(func() { close(pollStopped) })
			return ctx.Err()
		},
	}
	r := new(refusal)
	started := make(chan struct{})
	runErr := make(chan error, 1)
	cfg := &config.Config{}
	cfg.SignalHandling.DrainTimeout = time.Hour
	w := newRemovableWorker(client, 2, cfg, r, func(ctx context.Context, _ *coordinatorv1.Task) error {
		close(started)
		<-ctx.Done()
		runErr <- ctx.Err()
		return ctx.Err()
	})
	startErr := make(chan error, 1)
	go func() { startErr <- w.Start(t.Context()) }()
	<-started

	stopErr := make(chan error, 1)
	go func() { stopErr <- w.Stop(t.Context()) }()
	<-pollStopped
	r.refuse()
	select {
	case err := <-stopErr:
		assert.Equal(t, errRemovedForTest, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the drain went on after the removal")
	}
	select {
	case err := <-runErr:
		assert.ErrorIs(t, err, context.Canceled)
	default:
		t.Fatal("Stop returned before the run was stopped")
	}
	assert.Equal(t, errRemovedForTest, <-startErr)
}

// A worker without a membership ends as removed with errWorkerRemoved.
func TestWorkerRemovedWithoutMembership(t *testing.T) {
	w := NewWorker("test-worker", 1, &removalClient{}, map[string]string{}, &config.Config{}, nil)
	w.SetHandler(runHandler(func(context.Context, *coordinatorv1.Task) error { return nil }))
	w.publishRemoval()
	err := startRemovable(t, w)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errWorkerRemoved))
}

// A removal that arrives after a run registered but before it moved into
// execution stops the run: it never reaches the executor (Spec 108 §Removed
// Worker).
func TestWorkerRemovedBeforeExecution(t *testing.T) {
	r := new(refusal)
	client := &removalClient{task: &coordinatorv1.Task{DagRunId: "run-1", AttemptKey: "attempt-1"}}
	var executed atomic.Bool
	w := newRemovableWorker(client, 1, &config.Config{}, r, func(context.Context, *coordinatorv1.Task) error {
		executed.Store(true)
		return nil
	})
	w.beforeExecute = func() { r.refuse() }

	assert.Equal(t, errRemovedForTest, startRemovable(t, w))
	assert.False(t, executed.Load(), "the run reached the executor after the removal")
}

// A refusal that arrives once a run moved into execution, before the
// executor runs, stops the run the moment it arrives, with the removal as its
// cause, so the executor starts stopped, before the worker's own stop runs
// (Spec 108 §Removed Worker).
func TestWorkerRemovedAfterAdmission(t *testing.T) {
	r := new(refusal)
	client := &removalClient{task: &coordinatorv1.Task{DagRunId: "run-1", AttemptKey: "attempt-1"}}
	cause := make(chan error, 1)
	w := newRemovableWorker(client, 1, &config.Config{}, r, func(ctx context.Context, _ *coordinatorv1.Task) error {
		cause <- context.Cause(ctx)
		return ctx.Err()
	})
	// The refusal lands in the gap, and the run goes on at once, without
	// waiting for the worker to stop.
	w.afterAdmit = func(context.Context) { r.refuse() }

	assert.Equal(t, errRemovedForTest, startRemovable(t, w))
	assert.ErrorIs(t, <-cause, errWorkerRemoved, "the run entered the executor live")
}

// A worker that learns of its removal while a poll returns a task accepts
// no task: the claim expires and returns the task to the queue (Spec 108
// §Removal).
func TestWorkerRemovedBeforeAcknowledgment(t *testing.T) {
	r := new(refusal)
	task := ownedTask()
	task.ClaimToken = "claim-1"
	client := &removalClient{task: task, beforeTask: func() { r.refuse() }}
	var executed atomic.Bool
	w := newRemovableWorker(client, 1, &config.Config{}, r, func(context.Context, *coordinatorv1.Task) error {
		executed.Store(true)
		return nil
	})

	assert.Equal(t, errRemovedForTest, startRemovable(t, w))
	assert.Zero(t, client.acks.Load(), "the claim was acknowledged after the removal")
	assert.False(t, executed.Load())
}

// A refusal that arrives while the worker acknowledges a claim stops the
// acknowledgment: no further attempt is made, and the task is left to return
// to the queue (Spec 108 §Removal).
func TestWorkerRemovedDuringAcknowledgment(t *testing.T) {
	r := new(refusal)
	task := ownedTask()
	task.ClaimToken = "claim-1"
	client := &removalClient{task: task, ack: func() error {
		// The first attempt fails as the refusal arrives.
		r.refuse()
		return status.Error(codes.Unavailable, "coordinator restarting")
	}}
	var executed atomic.Bool
	w := newRemovableWorker(client, 1, &config.Config{}, r, func(context.Context, *coordinatorv1.Task) error {
		executed.Store(true)
		return nil
	})

	assert.Equal(t, errRemovedForTest, startRemovable(t, w))
	assert.Equal(t, int32(1), client.acks.Load(), "an acknowledgment was tried after the removal")
	assert.False(t, executed.Load())
}

// A refusal that arrives during a claim acknowledgment ends it: no attempt
// follows, whatever the worker's own stop does meanwhile (Spec 108 §Removal).
func TestAcknowledgmentStopsOnRemoval(t *testing.T) {
	removal, cancelRemoval := context.WithCancel(context.Background())
	defer cancelRemoval()
	task := ownedTask()
	task.ClaimToken = "claim-1"
	client := &removalClient{ack: func() error {
		cancelRemoval()
		return status.Error(codes.Unavailable, "coordinator restarting")
	}}
	p := NewPoller("test-worker", client, nil, 0, nil)
	p.removal = removal

	require.Error(t, p.ackTaskClaim(t.Context(), task))
	assert.Equal(t, int32(1), client.acks.Load())
}
