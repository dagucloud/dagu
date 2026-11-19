// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// countingSecretResolver resolves registry refs from a map and counts the
// lookups of each ref.
type countingSecretResolver struct {
	mu     sync.Mutex
	values map[string]string
	calls  map[string]int
}

func newCountingSecretResolver(values map[string]string) *countingSecretResolver {
	return &countingSecretResolver{values: values, calls: make(map[string]int)}
}

func (r *countingSecretResolver) ResolveReference(_ context.Context, ref secretref.Ref) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[ref.Ref]++
	value, ok := r.values[ref.Ref]
	if !ok {
		return "", secretpkg.ErrNotFound
	}
	return value, nil
}

func (r *countingSecretResolver) CheckReferenceAccessibility(context.Context, secretref.Ref) error {
	return nil
}

func (r *countingSecretResolver) callCount(ref string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[ref]
}

// Two SSH steps share one password ref, and neither runs. The ref still
// resolves once at run start, and its value is masked wherever it appears.
// A step name containing "=" must not change what is masked.
func TestAgent_SSHCredentialRefsResolveOnceAndMask(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	const credential = "credential-value-7f3k"
	resolver := newCountingSecretResolver(map[string]string{"ssh/password": credential})

	dag := th.DAG(t, `
steps:
  - name: local
    run: echo `+credential+`
    output: RESPONSE
  - name: remote=prod
    action: ssh.run
    preconditions:
      - condition: "skip"
        expected: "run"
    continue_on:
      skipped: true
    with:
      host: app.invalid
      command: "true"
      password: {secret: ssh/password}
  - name: remote-again
    action: ssh.run
    preconditions:
      - condition: "skip"
        expected: "run"
    with:
      host: other.invalid
      command: "true"
      password: {secret: ssh/password}
`)
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{SecretReferenceResolver: resolver}))
	dagAgent.RunSuccess(t)

	require.Equal(t, 1, resolver.callCount("ssh/password"))

	outputs := dag.ReadOutputs(t)
	require.NotContains(t, outputs["response"], credential)
	require.Contains(t, outputs["response"], "*******")

	statusJSON, err := json.Marshal(dagAgent.Status(th.Context))
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), credential)
}

func TestAgent_SSHCredentialRefMissingFailsBeforeAnyStep(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `
steps:
  - name: local
    run: echo started
  - name: remote
    action: ssh.run
    with:
      host: app.invalid
      command: "true"
      key: {secret: ssh/missing}
`)
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
		SecretReferenceResolver: newCountingSecretResolver(nil),
	}))
	dagAgent.RunCheckErr(t, `failed to resolve steps.remote.with.key from secret ref "ssh/missing"`)

	for _, node := range dagAgent.Status(th.Context).Nodes {
		require.Equal(t, ir.NodeNotStarted, node.Status, "step %q ran", node.Step.Name)
	}
}

// Until node steps run on their nodes, a run that uses node targeting fails
// before any step, rather than running remote commands on the Dagu host.
func TestAgent_NodeTargetingFailsBeforeAnyStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "NodesStep",
			yaml:    "steps:\n  - id: local\n    run: echo started\n  - id: clean\n    run: rm -rf /tmp/app-cache\n    nodes: web\n",
			wantErr: `step "clean" sets nodes, and running a step on inventory nodes is not available yet`,
		},
		{
			name:    "NodeRequired",
			yaml:    "node: required\nsteps:\n  - id: local\n    run: echo started\n",
			wantErr: "declares node: required, and running a DAG bound to a node is not available yet",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			dag := th.DAG(t, tc.yaml)
			dagAgent := dag.Agent()
			dagAgent.RunCheckErr(t, tc.wantErr)
			for _, node := range dagAgent.Status(th.Context).Nodes {
				require.Equal(t, ir.NodeNotStarted, node.Status, "step %q ran", node.Step.Name)
			}
		})
	}
}

// A dry run executes nothing, so it succeeds, and reports that the real run
// would refuse node targeting.
func TestAgent_NodeTargetingDryRunReportsRefusal(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, "steps:\n  - id: clean\n    run: rm -rf /tmp/app-cache\n    nodes: web\n")
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{Dry: true}))
	dagAgent.RunSuccess(t)

	require.Contains(t, dagAgent.DryRunChecks(), runtime.DryRunCheck{
		Field:   "nodes",
		Message: `step "clean" sets nodes, and running a step on inventory nodes is not available yet`,
	})
}
