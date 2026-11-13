// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pools of the example of Spec 092 load as typed values, in order.
func TestWorkerPoolsLoad(t *testing.T) {
	cfg := loadFromYAML(t, `
coordinator:
  worker_pools:
    - name: agent-claude
      identities: [agent-claude]
      labels:
        agent.provider.claude: "true"
    - name: general
      identities: [general-worker, general-worker-2]
      labels:
        zone: "*"
  dispatchers: [server, scheduler]
`)
	assert.Equal(t, []WorkerPool{
		{Name: "agent-claude", Identities: []string{"agent-claude"}, Labels: map[string]string{"agent.provider.claude": "true"}},
		{Name: "general", Identities: []string{"general-worker", "general-worker-2"}, Labels: map[string]string{"zone": "*"}},
	}, cfg.Coordinator.WorkerPools)
	assert.Equal(t, []string{"server", "scheduler"}, cfg.Coordinator.Dispatchers)

	plain := loadFromYAML(t, "coordinator:\n  port: 50055\n").Coordinator
	assert.Empty(t, plain.WorkerPools)
	assert.Empty(t, plain.Dispatchers)
}

// coordinator.dispatchers is required with pools and invalid without them. An
// identity has one role.
func TestDispatchersErrors(t *testing.T) {
	const pools = "  worker_pools: [{name: general, identities: [general-worker]}]\n"
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"Missing", pools, []string{`key "coordinator.dispatchers" is not set`}},
		{"WithoutPools", "  dispatchers: [server]\n", []string{`key "coordinator.dispatchers" needs coordinator.worker_pools`}},
		{"Empty", pools + "  dispatchers: []\n", []string{`"coordinator.dispatchers"`, "a non-empty list"}},
		{"NotAString", pools + "  dispatchers: [1]\n", []string{`"coordinator.dispatchers[0]"`}},
		{"Duplicate", pools + "  dispatchers: [server, server]\n", []string{`"coordinator.dispatchers[1]"`, "names once"}},
		{"InAPool", pools + "  dispatchers: [general-worker]\n", []string{`identity "general-worker" is a dispatcher and a member of the pool "general"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithErrorFromYAML(t, "coordinator:\n"+tc.yaml)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The block has a closed schema and no coercion. Each error names the key, the
// expected value and the value.
func TestWorkerPoolsErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"NotAList", "worker_pools: {name: a}", []string{`"coordinator.worker_pools"`, "a non-empty list"}},
		{"EmptyList", "worker_pools: []", []string{`"coordinator.worker_pools"`, "a non-empty list"}},
		{"UnknownKey", "worker_pools: [{name: a, identities: [x], label: {}}]", []string{`unknown key "coordinator.worker_pools[0].label"`, "identities, labels, name"}},
		{"MissingName", "worker_pools: [{identities: [x]}]", []string{`"coordinator.worker_pools[0].name"`, "got null"}},
		{"BadName", "worker_pools: [{name: Agent, identities: [x]}]", []string{`"coordinator.worker_pools[0].name"`, `^[a-z][a-z0-9-]*$`, `"Agent"`}},
		{"DuplicateName", "worker_pools: [{name: a, identities: [x]}, {name: a, identities: [y]}]", []string{`"coordinator.worker_pools[1].name"`, "no other pool"}},
		{"NoIdentities", "worker_pools: [{name: a, identities: []}]", []string{`"coordinator.worker_pools[0].identities"`, "non-empty list"}},
		{"IdentityNotAString", "worker_pools: [{name: a, identities: [1]}]", []string{`"coordinator.worker_pools[0].identities[0]"`}},
		{"IdentityTwiceInAPool", "worker_pools: [{name: a, identities: [x, x]}]", []string{`"coordinator.worker_pools[0].identities[1]"`}},
		{"IdentityInTwoPools", "worker_pools: [{name: a, identities: [x]}, {name: b, identities: [x]}]", []string{`identity "x" is in the pools "a" and "b"`}},
		{"LabelNotAString", "worker_pools: [{name: a, identities: [x], labels: {agent.provider.claude: true}}]", []string{`"coordinator.worker_pools[0].labels.agent.provider.claude"`, "a non-empty string", "(bool)"}},
		{"ProviderLabelNotTrue", `worker_pools: [{name: a, identities: [x], labels: {agent.provider.claude: "*"}}]`, []string{`the value "true"`, `"*"`}},
		{"TwoProviderLabels", `worker_pools: [{name: a, identities: [x], labels: {agent.provider.claude: "true", agent.provider.codex: "true"}}]`, []string{"one label agent.provider.<provider>"}},
		{"OtherAgentLabel", `worker_pools: [{name: a, identities: [x], labels: {agent.zone: "x"}}]`, []string{"belong to Dagu"}},
		{"OSLabel", `worker_pools: [{name: a, identities: [x], labels: {os: linux}}]`, []string{`every pool allows the label "os"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithErrorFromYAML(t, "coordinator:\n  "+tc.yaml+"\n")
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The key has no environment variable: a value from the environment is a string,
// so it can never be a list of pools.
func TestWorkerPoolsIgnoreNoEnvironmentVariable(t *testing.T) {
	t.Setenv("DAGU_COORDINATOR_WORKER_POOLS", "[{name: a, identities: [x]}]")
	assert.Empty(t, testLoad(t).Coordinator.WorkerPools)
}

// The source of worker.max_active_runs tells an agent worker that a file or a
// variable sets the key.
func TestWorkerMaxActiveRunsSource(t *testing.T) {
	t.Setenv("DAGU_WORKER_MAX_ACTIVE_RUNS", "")
	assert.Empty(t, testLoad(t).Worker.MaxActiveRunsSource, "the default has no source")
	assert.Equal(t, "file", loadFromYAML(t, "worker:\n  max_active_runs: 4\n").Worker.MaxActiveRunsSource)

	t.Setenv("DAGU_WORKER_MAX_ACTIVE_RUNS", "4")
	cfg := testLoad(t)
	assert.Equal(t, "env:DAGU_WORKER_MAX_ACTIVE_RUNS", cfg.Worker.MaxActiveRunsSource)
	assert.Equal(t, 4, cfg.Worker.MaxActiveRuns)
}
