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

// Spec 094, "Version check": a chat.completion step that names an endpoint, in
// the block endpoint or in a model entry, needs the capability ai_relay.v1 as an agent.run step
// does. The coordinator holds the run, and fails it with the reason and the step.
func TestDispatchOfAChatStepWithAnEndpointWaitsForTheCapability(t *testing.T) {
	for name, with := range map[string]string{
		"endpoint block": `      endpoint:
        name: cle1
        model: claude-haiku-4-5
`,
		"endpoint of a model entry": `      model:
        - name: gpt-4o
          provider: openai
        - name: claude-haiku-4-5
          endpoint: cle1
`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newHoldFixture(t, holdWait)
			f.heartbeat(t, "worker-old", map[string]string{"zone": "a"})

			task := &coordinatorv1.Task{
				Operation: coordinatorv1.Operation_OPERATION_START,
				DagRunId:  "run-chat",
				Target:    "chat-run",
				Definition: "name: chat-run\nsteps:\n  - name: classify\n    action: chat.completion\n    with:\n" +
					with + "      prompt: Classify this note.\n",
			}
			_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
			require.NoError(t, err, "the dispatch is held, not refused")
			assert.Equal(t, []string{ir.CapabilityAIRelay}, task.RequiredCapabilities)

			f.pollNone(t, "worker-old", map[string]string{"zone": "a"})
			ExpireHeldDispatchesForTest(f.h, time.Now().Add(2*holdWait))
			runStatus := f.status(t, "run-chat")
			assert.Equal(t, ir.Failed, runStatus.Status)
			assert.Contains(t, runStatus.Error, `no worker supports the AI relay: the step "classify" names the endpoint "cle1" (worker_too_old)`)
		})
	}
}

// A chat step on the old method needs no capability.
func TestDispatchOfAChatStepWithTheOldMethodNeedsNoCapability(t *testing.T) {
	f := newHoldFixture(t, holdWait)
	task := &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START,
		DagRunId:  "run-old",
		Target:    "chat-old",
		Definition: `name: chat-old
steps:
  - name: classify
    action: chat.completion
    with:
      provider: openai
      api_key_name: OPENAI_API_KEY
      model: gpt-4o
      prompt: Classify this note.
`,
	}
	f.heartbeat(t, "worker-plain", map[string]string{"zone": "a"})
	_, err := f.h.Dispatch(t.Context(), &coordinatorv1.DispatchRequest{Task: task})
	require.NoError(t, err)
	assert.Empty(t, task.RequiredCapabilities)
}
