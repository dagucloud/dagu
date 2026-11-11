// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"io"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

// agentProbe is an executor that fails like the executor of an agent.run step:
// each execution returns an AgentError of one kind.
type agentProbe struct {
	kind  ir.AgentFailureKind
	calls *atomic.Int32
}

func (p *agentProbe) SetStdout(io.Writer)    {}
func (p *agentProbe) SetStderr(io.Writer)    {}
func (p *agentProbe) Kill(_ os.Signal) error { return nil }

func (p *agentProbe) Run(context.Context) error {
	p.calls.Add(1)
	return executor.NewAgentError(p.kind, 0, "", io.ErrUnexpectedEOF)
}

func registerAgentProbe(t *testing.T, kind ir.AgentFailureKind) (string, *atomic.Int32) {
	t.Helper()

	calls := &atomic.Int32{}
	executorType := "agent_probe_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	executor.RegisterExecutor(executorType, func(context.Context, ir.Step) (executor.Executor, error) {
		return &agentProbe{kind: kind, calls: calls}, nil
	}, nil, registry.ExecutorCapabilities{})
	t.Cleanup(func() {
		executor.UnregisterExecutor(executorType)
		registry.UnregisterExecutorCapabilities(executorType)
	})
	return executorType, calls
}

// A step ends with the exit code of the failure kind, retry_policy never
// retries auth_required, and it retries rate_limited and error as for any step.
func TestAgentRunFailureKindsAndRetryPolicy(t *testing.T) {
	for name, tc := range map[string]struct {
		kind     ir.AgentFailureKind
		exitCode int
		calls    int32
	}{
		"auth_required is not retried": {ir.AgentKindAuthRequired, 77, 1},
		"rate_limited is retried":      {ir.AgentKindRateLimited, 75, 3},
		"error is retried":             {ir.AgentKindError, 1, 3},
	} {
		t.Run(name, func(t *testing.T) {
			probeType, calls := registerAgentProbe(t, tc.kind)
			r := setupRunner(t)
			plan := r.newPlan(t, newStep("probe",
				withExecutorType(probeType),
				withRetryPolicy(2, 10*time.Millisecond),
			))

			result := plan.assertRun(t, ir.Failed)
			result.assertNodeStatus(t, "probe", ir.NodeFailed)

			node := result.nodeByName(t, "probe")
			assert.Equal(t, tc.exitCode, node.State().ExitCode)
			assert.Equal(t, tc.calls, calls.Load())
		})
	}
}

// retry_policy.exit_code can select the code 75 of a rate limit.
func TestAgentRunRetryPolicySelectsTheCodeOfAKind(t *testing.T) {
	probeType, calls := registerAgentProbe(t, ir.AgentKindRateLimited)
	r := setupRunner(t)
	plan := r.newPlan(t, newStep("probe",
		withExecutorType(probeType),
		withRetryPolicy(2, 10*time.Millisecond),
		func(step *ir.Step) { step.RetryPolicy.ExitCodes = []int{75} },
	))
	plan.assertRun(t, ir.Failed)
	assert.Equal(t, int32(3), calls.Load())

	// A policy that selects another code does not retry.
	probeType, calls = registerAgentProbe(t, ir.AgentKindRateLimited)
	plan = r.newPlan(t, newStep("probe",
		withExecutorType(probeType),
		withRetryPolicy(2, 10*time.Millisecond),
		func(step *ir.Step) { step.RetryPolicy.ExitCodes = []int{42} },
	))
	plan.assertRun(t, ir.Failed)
	assert.Equal(t, int32(1), calls.Load())
}
