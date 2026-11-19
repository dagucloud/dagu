// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

// Spec 092, "Match": a worker with the label of a provider claims only a task of
// that provider, and a general worker claims no agent task.
func TestDispatchTaskStore_ClaimAppliesTheAgentRule(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	require.NoError(t, s.Enqueue(ctx, &dispatch.DispatchTask{
		DAGRunID: "run-agent", Target: "agent", AttemptID: "attempt-agent", AttemptKey: "key-agent",
		WorkerSelector: map[string]string{"agent.provider.claude": "true"},
	}))
	require.NoError(t, s.Enqueue(ctx, &dispatch.DispatchTask{
		DAGRunID: "run-plain", Target: "plain", AttemptID: "attempt-plain", AttemptKey: "key-plain",
	}))

	codex, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "codex-1", PollerID: "p", Labels: map[string]string{"agent.provider.codex": "true"},
	})
	require.NoError(t, err)
	assert.Nil(t, codex, "an agent worker of another provider claims nothing")

	agent, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "claude-1", PollerID: "p", WorkerPool: "agent-claude",
		Labels: map[string]string{"agent.provider.claude": "true", "os": "linux"},
	})
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "run-agent", agent.Task.DAGRunID)
	assert.Equal(t, "agent-claude", agent.WorkerPool)

	again, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "claude-2", PollerID: "p", Labels: map[string]string{"agent.provider.claude": "true"},
	})
	require.NoError(t, err)
	assert.Nil(t, again, "an agent worker claims no task that needs no provider")

	general, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "general-1", PollerID: "p", WorkerPool: "general", Labels: map[string]string{"os": "linux"},
	})
	require.NoError(t, err)
	require.NotNil(t, general)
	assert.Equal(t, "run-plain", general.Task.DAGRunID)

	stored, err := s.GetClaim(ctx, agent.ClaimToken)
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", stored.WorkerPool, "the claim keeps the pool of the poller")

	// A released claim forgets the pool.
	require.NoError(t, s.ReleaseClaim(ctx, agent.ClaimToken))
	reclaimed, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{
		WorkerID: "claude-3", PollerID: "p", Labels: map[string]string{"agent.provider.claude": "true"},
	})
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	assert.Empty(t, reclaimed.WorkerPool)
}

// The pool of a lease does not change once it is set.
func TestDAGRunLeaseStore_RejectsPoolTransfer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("dag_run_leases"))
	lease := dispatch.DAGRunLease{
		AttemptKey: "key-1", DAGRun: ir.NewDAGRunRef("dag", "run-1"), AttemptID: "attempt-1",
		WorkerID: "worker-1", WorkerPool: "agent-claude", LastHeartbeatAt: time.Now().UnixMilli(),
	}
	require.NoError(t, s.Upsert(ctx, lease))

	other := lease
	other.WorkerPool = "general"
	require.ErrorIs(t, s.Upsert(ctx, other), dispatch.ErrDAGRunLeaseConflict)

	// An update without a pool, as a status report makes, keeps the pool.
	refresh := lease
	refresh.WorkerPool = ""
	require.NoError(t, s.Upsert(ctx, refresh))
	stored, err := s.Get(ctx, "key-1")
	require.NoError(t, err)
	assert.Equal(t, "agent-claude", stored.WorkerPool)
}
