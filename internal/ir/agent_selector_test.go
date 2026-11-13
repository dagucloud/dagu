// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cliStep(id, provider string) ir.Step {
	return ir.Step{Name: id, ID: id, Agent: &ir.AgentRun{Provider: provider}}
}

// The CLI provider of each agent.run step of the DAG and of its handlers counts.
// A step that calls a sub-DAG does not count.
func TestAgentProviders(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{
		cliStep("code", "claude"),
		{Name: "child", ID: "child", SubDAG: &ir.SubDAG{Name: "child-with-codex"}},
		cliStep("again", "claude"),
	}}
	assert.Equal(t, []string{"claude"}, dag.AgentProviders())
	assert.Equal(t, map[string]string{"agent.provider.claude": "true"}, dag.AgentWorkerSelector())

	handler := cliStep("on-failure", "codex")
	dag.HandlerOn.Failure = &handler
	assert.Equal(t, []string{"claude", "codex"}, dag.AgentProviders())

	assert.Nil(t, (&ir.DAG{Steps: []ir.Step{{Name: "plain", ID: "plain"}}}).AgentProviders())
	var none *ir.DAG
	assert.Nil(t, none.AgentProviders())
	assert.Nil(t, none.AgentWorkerSelector())
}

// An agent.run step in the body of a foreach step runs in the run of the DAG, so
// its provider counts as the provider of a step does. Without it, the run would
// not go to an agent worker.
func TestAgentProvidersOfAForeachBody(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{{
		Name: "each", ID: "each",
		Foreach: &ir.ForeachConfig{Steps: []ir.Step{cliStep("judge", "claude")}},
	}}}
	assert.Equal(t, []string{"claude"}, dag.AgentProviders())
	assert.Equal(t, map[string]string{"agent.provider.claude": "true"}, dag.DispatchWorkerSelector())
}

// The selector of a task has the worker_selector of the DAG and the implied label.
func TestDispatchWorkerSelectorAddsTheImpliedLabel(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{WorkerSelector: map[string]string{"zone": "eu"}, Steps: []ir.Step{cliStep("code", "claude")}}
	assert.Equal(t, map[string]string{"zone": "eu", "agent.provider.claude": "true"}, dag.DispatchWorkerSelector())
	assert.Equal(t, map[string]string{"zone": "eu"}, dag.WorkerSelector, "the DAG is not changed")

	plain := &ir.DAG{WorkerSelector: map[string]string{"zone": "eu"}}
	assert.Equal(t, map[string]string{"zone": "eu"}, plain.DispatchWorkerSelector())
}

// A worker with a label agent.provider.<P> matches only a selector with that label.
func TestWorkerLabelsMatchSelector(t *testing.T) {
	t.Parallel()

	general := map[string]string{"os": "linux", "zone": "eu"}
	claude := map[string]string{"os": "linux", "agent.provider.claude": "true"}
	agentTask := map[string]string{"agent.provider.claude": "true"}
	codexTask := map[string]string{"agent.provider.codex": "true"}

	for _, tc := range []struct {
		name     string
		labels   map[string]string
		selector map[string]string
		want     bool
	}{
		{"GeneralWorkerNoSelector", general, nil, true},
		{"GeneralWorkerOwnLabel", general, map[string]string{"zone": "eu"}, true},
		{"GeneralWorkerWrongValue", general, map[string]string{"zone": "us"}, false},
		{"GeneralWorkerAgentTask", general, agentTask, false},
		{"AgentWorkerNoSelector", claude, nil, false},
		{"AgentWorkerOtherLabelOnly", claude, map[string]string{"os": "linux"}, false},
		{"AgentWorkerOwnProvider", claude, agentTask, true},
		{"AgentWorkerOtherProvider", claude, codexTask, false},
		{"MissingLabelOnWorker", map[string]string{}, map[string]string{"gpu": ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ir.WorkerLabelsMatchSelector(tc.labels, tc.selector))
		})
	}
}

// Only a built-in CLI harness provider that is not a chat provider is a CLI provider.
func TestIsAgentCLIProvider(t *testing.T) {
	t.Parallel()

	assert.True(t, ir.IsAgentCLIProvider("claude"))
	assert.True(t, ir.IsAgentCLIProvider("codex"))
	assert.False(t, ir.IsAgentCLIProvider("gemini"), "gemini is also a chat provider, so it sets the engine api")
	assert.False(t, ir.IsAgentCLIProvider("opencode"), "opencode is also a chat provider")
	assert.False(t, ir.IsAgentCLIProvider("anthropic"))
	assert.False(t, ir.IsAgentCLIProvider(""))
	assert.Contains(t, ir.AgentCLIProviderNames(), "claude")
	assert.NotContains(t, ir.AgentCLIProviderNames(), "gemini")
}

// A run selector cannot use a key that starts with agent.
func TestValidateWorkerSelectorRejectsAgentKeys(t *testing.T) {
	t.Parallel()

	err := ir.ValidateWorkerSelector(map[string]string{"agent.provider.claude": "true"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `worker selector "agent.provider.claude"`)
	assert.Contains(t, err.Error(), "agent.")

	dag := &ir.DAG{Name: "d"}
	require.Error(t, dag.ApplyWorkerSelector(map[string]string{"agent.x": "y"}))
	assert.Nil(t, dag.WorkerSelector)
	require.NoError(t, ir.ValidateWorkerSelector(map[string]string{"zone": "eu"}))
}

// The condition and the final error of a run with no agent worker name the provider.
func TestDescribeAgentSelector(t *testing.T) {
	t.Parallel()

	text, ok := ir.DescribeAgentSelector(map[string]string{"agent.provider.claude": "true"})
	require.True(t, ok)
	assert.Equal(t, `no agent worker serves the provider "claude": the selector is {agent.provider.claude: "true"}`, text)

	_, ok = ir.DescribeAgentSelector(map[string]string{"zone": "eu"})
	assert.False(t, ok)

	conditions := ir.NoMatchingWorkerConditionsFor(map[string]string{"agent.provider.claude": "true"}, time.Now())
	require.Len(t, conditions, 2)
	for _, condition := range conditions {
		assert.Equal(t, ir.DAGRunReasonNoMatchingWorker, condition.Reason)
		assert.Contains(t, condition.Message, text)
	}
	assert.Equal(t, ir.NoMatchingWorkerConditions(time.Time{})[0].Message,
		ir.NoMatchingWorkerConditionsFor(map[string]string{"zone": "eu"}, time.Time{})[0].Message)

	assert.Equal(t, `the DAG needs the providers "claude" and "codex": a run goes to one agent worker`,
		ir.TwoProvidersError([]string{"claude", "codex"}).Error())
}
