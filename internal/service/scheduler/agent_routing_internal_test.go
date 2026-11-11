// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The check of the available workers of the queue uses the implied label of the
// DAG and the match rule of Spec 092, and the condition names the provider.
func TestWorkerAvailableInSnapshotAppliesTheAgentRule(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fresh := now.UnixMilli()
	agentDAG := &ir.DAG{Name: "agent", Steps: []ir.Step{{Name: "code", ID: "code", Agent: &ir.AgentRun{Provider: "claude"}}}}
	plainDAG := &ir.DAG{Name: "plain"}
	general := dispatch.WorkerHeartbeatRecord{WorkerID: "general", Labels: map[string]string{"os": "linux"}, LastHeartbeatAt: fresh}
	claude := dispatch.WorkerHeartbeatRecord{WorkerID: "claude", Labels: map[string]string{"agent.provider.claude": "true"}, LastHeartbeatAt: fresh}

	available, defs := workerAvailableInSnapshot([]dispatch.WorkerHeartbeatRecord{general}, now, time.Minute, agentDAG, &ir.DAGRunStatus{})
	assert.False(t, available, "a general worker does not serve an agent run")
	require.Len(t, defs, 2)
	for _, def := range defs {
		assert.Equal(t, queuedConditionReasonNoMatchingWorker, def.reason)
		assert.Contains(t, def.message, `no agent worker serves the provider "claude": the selector is {agent.provider.claude: "true"}`)
	}

	available, _ = workerAvailableInSnapshot([]dispatch.WorkerHeartbeatRecord{claude}, now, time.Minute, agentDAG, &ir.DAGRunStatus{})
	assert.True(t, available)

	available, defs = workerAvailableInSnapshot([]dispatch.WorkerHeartbeatRecord{claude}, now, time.Minute, plainDAG, &ir.DAGRunStatus{})
	assert.False(t, available, "an agent worker does not serve a run that needs no provider")
	assert.Equal(t, noMatchingWorkerConditionDefs, defs)
}
