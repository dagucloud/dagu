// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"testing"
	"time"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Spec 094, "Version check": a run with a step that names an endpoint waits for a
// worker that announces ai_relay.v1. A worker with no capability does not take it.
// After the wait, the run fails with an error that names the reason and the step.
func TestDispatchOfAnEndpointStepWaitsForTheCapability(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	ctx := t.Context()
	agentLabels := map[string]string{"agent.provider.claude": "true"}
	f.heartbeat(t, "agent-old", agentLabels)

	task := &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START,
		DagRunId:  "run-ai",
		Target:    "ai-run",
		Definition: `name: ai-run
steps:
  - name: code_1
    action: agent.run
    with:
      provider: claude
      endpoint:
        name: cle1
        model: claude-test
      prompt: Fix it.
`,
	}
	_, err := f.h.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
	require.NoError(t, err, "the dispatch is held, not refused")
	assert.Equal(t, []string{ir.CapabilityAIRelay}, task.RequiredCapabilities, "the capability comes from the DAG")
	assert.Equal(t, ir.Queued, f.status(t, "run-ai").Status)

	// The worker with no capability is not handed the task.
	f.pollNone(t, "agent-old", agentLabels)

	// After the wait, the run fails with the reason and the step.
	ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))
	runStatus := f.status(t, "run-ai")
	assert.Equal(t, ir.Failed, runStatus.Status)
	assert.Contains(t, runStatus.Error, `no worker supports the AI relay: the step "code_1" names the endpoint "cle1" (worker_too_old)`)
}

// A caller cannot set the required capabilities: the coordinator takes them from
// the DAG. A task with no endpoint step needs none.
func TestDispatchTakesTheCapabilitiesFromTheDAG(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	task := &coordinatorv1.Task{
		Operation:            coordinatorv1.Operation_OPERATION_START,
		DagRunId:             "run-plain",
		Target:               "plain",
		Definition:           "steps:\n  - name: s\n    run: echo hi\n",
		RequiredCapabilities: []string{ir.CapabilityAIRelay},
	}
	f.heartbeat(t, "worker-plain", map[string]string{"type": "gpu"})
	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	require.NoError(t, err)
	assert.Empty(t, task.RequiredCapabilities, "the coordinator drops a capability that the DAG does not need")
}
