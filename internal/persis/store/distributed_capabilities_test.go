// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

// Spec 094, "Version check": a task with a required capability goes only to a
// worker that announces it. A task with none goes to each worker.
func TestDispatchTaskStore_ClaimNeedsTheCapability(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	require.NoError(t, s.Enqueue(ctx, &dispatch.DispatchTask{
		DAGRunID: "run-ai", Target: "ai", AttemptID: "attempt-ai", AttemptKey: "key-ai",
		RequiredCapabilities: []string{ir.CapabilityAIRelay},
	}))

	old, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "old", PollerID: "p"})
	require.NoError(t, err)
	assert.Nil(t, old, "a worker with no capability claims nothing")

	other, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "old", PollerID: "p", Capabilities: []string{"other.v1"}})
	require.NoError(t, err)
	assert.Nil(t, other, "the no-match cache keeps the capabilities apart")

	current, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "old", PollerID: "p", Capabilities: []string{ir.CapabilityAIRelay}})
	require.NoError(t, err)
	require.NotNil(t, current)
	assert.Equal(t, "run-ai", current.Task.DAGRunID)
	assert.Equal(t, []string{ir.CapabilityAIRelay}, current.Task.RequiredCapabilities)
}
