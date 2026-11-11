// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentCoordinator hands out a list of tasks, one for each poll, and records the
// polls and the acknowledgments.
type agentCoordinator struct {
	*mockCoordinatorCli

	mu        sync.Mutex
	tasks     []*coordinatorv1.Task
	polls     int
	acks      []string
	rejectKey string
}

func newAgentCoordinator(tasks ...*coordinatorv1.Task) *agentCoordinator {
	c := &agentCoordinator{mockCoordinatorCli: newMockCoordinatorCli(), tasks: tasks}
	c.SetPollFunc(func(ctx context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
		c.mu.Lock()
		c.polls++
		if len(c.tasks) == 0 {
			c.mu.Unlock()
			<-ctx.Done()
			return nil, ctx.Err()
		}
		task := c.tasks[0]
		c.tasks = c.tasks[1:]
		c.mu.Unlock()
		return task, nil
	})
	c.RunHeartbeatFunc = func(_ context.Context, _ serviceregistry.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, task := range req.RunningTasks {
			if task.AttemptKey == c.rejectKey {
				return &coordinatorv1.RunHeartbeatResponse{CancelledRuns: []*coordinatorv1.CancelledRun{{AttemptKey: task.AttemptKey}}}, nil
			}
		}
		return &coordinatorv1.RunHeartbeatResponse{}, nil
	}
	return c
}

func (c *agentCoordinator) AckTaskClaimTo(_ context.Context, _ serviceregistry.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acks = append(c.acks, req.AttemptKey)
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (c *agentCoordinator) counts() (int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.polls, append([]string(nil), c.acks...)
}

func agentTask(id string, selector map[string]string) *coordinatorv1.Task {
	return &coordinatorv1.Task{
		DagRunId: id, Target: "dag-" + id, AttemptId: "attempt-" + id, AttemptKey: "key-" + id, ClaimToken: "claim-" + id,
		OwnerCoordinatorId: "coord", OwnerCoordinatorHost: "127.0.0.1", OwnerCoordinatorPort: 50055,
		Operation: coordinatorv1.Operation_OPERATION_START, WorkerSelector: selector,
	}
}

var claudeSelector = map[string]string{"agent.provider.claude": "true"}

type statusPushFunc func(ir.DAGRunStatus) error

func (f statusPushFunc) Push(_ context.Context, status ir.DAGRunStatus) error { return f(status) }

// reportingHandler runs a task: it pushes the final status of the run through
// the final report of the context, and records the runs.
type reportingHandler struct {
	mu      sync.Mutex
	runs    []string
	pushErr error
}

func (h *reportingHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
	h.mu.Lock()
	h.runs = append(h.runs, task.DagRunId)
	pushErr := h.pushErr
	h.mu.Unlock()
	pusher := worker.FinalReportPusherForTest(ctx, statusPushFunc(func(ir.DAGRunStatus) error { return pushErr }))
	// The status of the run is failed: it does not change the result of the worker.
	return pusher.Push(ctx, ir.DAGRunStatus{DAGRunID: task.DagRunId, Status: ir.Failed})
}

func (h *reportingHandler) ran() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.runs...)
}

func startAgentWorker(t *testing.T, coord *agentCoordinator, handler worker.TaskHandler) *worker.Worker {
	t.Helper()
	w := worker.NewWorker("agent-1", 1, coord, nil, nil)
	require.NoError(t, w.EnableAgentMode("claude"))
	w.SetHandler(handler)
	go func() { _ = w.Start(context.Background()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	})
	return w
}

func waitAgentRun(t *testing.T, w *worker.Worker) {
	t.Helper()
	select {
	case <-w.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the agent worker did not end its run")
	}
}

// Spec 092, "Rules that the option sets" and "End": the agent worker takes one
// run, polls no more, and its result is the result of the final report.
func TestAgentWorkerTakesOneRun(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(agentTask("run-1", claudeSelector), agentTask("run-2", claudeSelector))
	handler := &reportingHandler{}
	w := startAgentWorker(t, coord, handler)

	waitAgentRun(t, w)
	require.NoError(t, w.AgentRunErr(), "the final status reached the coordinator, so the result is a success")
	// The worker polls no more after the run: the second task stays with the coordinator.
	time.Sleep(200 * time.Millisecond)
	polls, acks := coord.counts()
	assert.Equal(t, 1, polls)
	assert.Equal(t, []string{"key-run-1"}, acks)
	assert.Equal(t, []string{"run-1"}, handler.ran())
}

// heartbeatRecorder records the heartbeats that a worker sends.
type heartbeatRecorder struct {
	mu   sync.Mutex
	reqs []*coordinatorv1.HeartbeatRequest
}

func (r *heartbeatRecorder) record(_ context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return &coordinatorv1.HeartbeatResponse{}, nil
}

func (r *heartbeatRecorder) all() []*coordinatorv1.HeartbeatRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*coordinatorv1.HeartbeatRequest(nil), r.reqs...)
}

// Spec 092, "End": before it exits, the agent worker tells the coordinator that
// it is gone. The offline heartbeat is its last heartbeat.
func TestAgentWorkerSendsOfflineHeartbeatBeforeExit(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(agentTask("run-1", claudeSelector))
	heartbeats := &heartbeatRecorder{}
	coord.SetHeartbeatFunc(heartbeats.record)
	w := startAgentWorker(t, coord, &reportingHandler{})

	waitAgentRun(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.Stop(ctx))

	sent := heartbeats.all()
	require.NotEmpty(t, sent)
	last := sent[len(sent)-1]
	assert.True(t, last.Offline, "the last heartbeat marks the worker offline")
	assert.Equal(t, "agent-1", last.WorkerId)
	assert.Equal(t, "true", last.Labels["agent.provider.claude"], "the offline heartbeat has the labels of the pool")
	for _, req := range sent[:len(sent)-1] {
		assert.False(t, req.Offline, "only the last heartbeat is offline")
	}
	time.Sleep(1500 * time.Millisecond)
	assert.Len(t, heartbeats.all(), len(sent), "the worker sends no heartbeat after the offline heartbeat")
}

// A failure to send the final status is the result of the run.
func TestAgentWorkerFinalReportFailure(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(agentTask("run-1", claudeSelector))
	w := startAgentWorker(t, coord, &reportingHandler{pushErr: errors.New("coordinator unavailable")})

	waitAgentRun(t, w)
	err := w.AgentRunErr()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "coordinator unavailable")
}

// A claim that the owner rejects before execution does not count: the worker
// takes the next claim.
func TestAgentWorkerRejectedClaimDoesNotCount(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(agentTask("run-1", claudeSelector), agentTask("run-2", claudeSelector))
	coord.rejectKey = "key-run-1"
	handler := &reportingHandler{}
	w := startAgentWorker(t, coord, handler)

	waitAgentRun(t, w)
	require.NoError(t, w.AgentRunErr())
	assert.Equal(t, []string{"run-2"}, handler.ran())
}

// A DAG that the routing check of the worker refuses after the acknowledgment
// runs no step, so it does not count as the one run: a forged label cannot
// spend the run of an agent worker.
func TestAgentWorkerRoutingRefusalDoesNotCount(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(agentTask("forged", claudeSelector), agentTask("real", claudeSelector))
	handler := &reportingHandler{}
	refusing := &mockHandler{ExecuteFunc: func(ctx context.Context, task *coordinatorv1.Task) error {
		if task.DagRunId == "forged" {
			return worker.RefusedByRoutingForTest(errors.New("the DAG needs no provider"))
		}
		return handler.Handle(ctx, task)
	}}
	w := startAgentWorker(t, coord, refusing)

	waitAgentRun(t, w)
	require.NoError(t, w.AgentRunErr())
	assert.Equal(t, []string{"real"}, handler.ran())
}

// The worker refuses a task whose selector does not have its provider label. It
// does not acknowledge the claim, so the task returns to the coordinator.
func TestAgentWorkerRefusesTaskOfAnotherProvider(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(
		agentTask("plain", nil),
		agentTask("codex", map[string]string{"agent.provider.codex": "true"}),
		agentTask("claude", claudeSelector),
	)
	handler := &reportingHandler{}
	w := startAgentWorker(t, coord, handler)

	waitAgentRun(t, w)
	_, acks := coord.counts()
	assert.Equal(t, []string{"key-claude"}, acks)
	assert.Equal(t, []string{"claude"}, handler.ran())
}

// A general worker refuses a task of an agent run in the same way.
func TestGeneralWorkerRefusesAgentTask(t *testing.T) {
	t.Parallel()

	coord := newAgentCoordinator(agentTask("agent", claudeSelector), agentTask("plain", nil))
	ran := make(chan string, 2)
	w := worker.NewWorker("general-1", 1, coord, nil, nil)
	w.SetHandler(&mockHandler{ExecuteFunc: func(_ context.Context, task *coordinatorv1.Task) error {
		ran <- task.DagRunId
		return nil
	}})
	go func() { _ = w.Start(context.Background()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	})

	select {
	case id := <-ran:
		assert.Equal(t, "plain", id)
	case <-time.After(10 * time.Second):
		t.Fatal("the general worker took no task")
	}
	_, acks := coord.counts()
	assert.Equal(t, []string{"key-plain"}, acks, fmt.Sprintf("acks: %v", acks))
}
