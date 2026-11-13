// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092, "Routing" and "Reserved labels": the loader rejects a DAG that needs
// two providers, a key of worker_selector that belongs to Dagu, and a DAG that
// needs a provider but must stay on the host.
func TestAgentRoutingValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "TwoProviders",
			yaml: `
steps:
  - id: a
    action: agent.run
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
  - id: b
    action: agent.run
    with: { provider: codex, prompt: hi }
`,
			want: []string{`the DAG needs the providers "claude" and "codex": a run goes to one agent worker`},
		},
		{
			name: "TwoProvidersWithAHandler",
			yaml: `
handler_on:
  failure:
    action: agent.run
    with: { provider: codex, prompt: hi }
steps:
  - id: a
    action: agent.run
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
`,
			want: []string{`the DAG needs the providers "claude" and "codex"`},
		},
		{
			name: "AgentKeyInTheDAGSelector",
			yaml: `
worker_selector:
  agent.provider.claude: "true"
steps:
  - id: a
    run: echo hi
`,
			want: []string{`worker selector "agent.provider.claude"`, "belong to Dagu"},
		},
		{
			name: "AgentKeyInAStepSelector",
			yaml: `
steps:
  - id: child
    action: dag.run
    worker_selector:
      agent.provider.claude: "true"
    with:
      dag: child
`,
			want: []string{`worker selector "agent.provider.claude"`},
		},
		{
			name: "LocalSelector",
			yaml: `
worker_selector: local
steps:
  - id: a
    action: agent.run
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
`,
			want: []string{`worker_selector "local" is not valid`, `the DAG needs the provider "claude"`},
		},
		{
			name: "ApprovalStep",
			yaml: `
steps:
  - id: a
    action: agent.run
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
  - id: gate
    depends: [a]
    run: echo ok
    approval:
      prompt: Approve
`,
			want: []string{`the DAG needs the provider "claude"`, "approval steps cannot be dispatched to workers"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadAgentRun(t, t.TempDir(), tc.yaml)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// One provider and a sub-DAG step need one agent worker: the steps of the
// sub-DAG do not count.
func TestAgentRoutingAcceptsOneProvider(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
worker_selector:
  zone: eu
steps:
  - id: code
    action: agent.run
    with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: hi }
  - id: child
    action: dag.run
    with:
      dag: child
`)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude"}, dag.AgentProviders())
	assert.Equal(t, map[string]string{"zone": "eu", "agent.provider.claude": "true"}, dag.DispatchWorkerSelector())
}

// An agent.run step in the body of a foreach step makes the DAG an agent DAG:
// the run goes to an agent worker, and a step that runs a process is untrusted
// by default.
func TestAgentRoutingOfAForeachBody(t *testing.T) {
	t.Parallel()

	dag, err := loadAgentRun(t, t.TempDir(), `
steps:
  - id: prepare
    run: echo prepare
  - id: judges
    depends: [prepare]
    foreach:
      items: [a, b]
      steps:
        - id: judge
          action: agent.run
          with: { provider: claude, endpoint: { name: test-endpoint, model: claude-test }, prompt: "judge ${foreach.item}" }
`)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude"}, dag.AgentProviders())
	assert.Equal(t, map[string]string{"agent.provider.claude": "true"}, dag.DispatchWorkerSelector())
	assert.True(t, dag.Steps[0].Untrusted, "a step that runs a process is untrusted by default")
	assert.Equal(t, []string{"test-endpoint"}, dag.Steps[1].Foreach.Steps[0].AIEndpoints())
	assert.NotEmpty(t, dag.RequiredCapabilities())
}
