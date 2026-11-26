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

// With secret values, an SSH credential ref takes the value given for the
// ref, masked like a resolved credential, and the resolver is not asked.
func TestAgent_SSHCredentialRefsFromSecretValues(t *testing.T) {
	t.Parallel()

	const credential = "test-value-9q2m"
	const yaml = `
steps:
  - name: local
    run: echo ` + credential + `
    output: RESPONSE
  - name: remote
    action: ssh.run
    preconditions:
      - condition: "skip"
        expected: "run"
    with:
      host: app.invalid
      command: "true"
      password: {secret: ssh/password}
`

	t.Run("Given", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		resolver := newCountingSecretResolver(map[string]string{"ssh/password": "resolver-value"})
		dag := th.DAG(t, yaml)
		dag.Agent(test.WithAgentOptions(agent.Options{
			SecretReferenceResolver: resolver,
			SecretValues:            map[string]string{"ssh/password": credential},
		})).RunSuccess(t)

		require.Zero(t, resolver.callCount("ssh/password"))
		outputs := dag.ReadOutputs(t)
		require.NotContains(t, outputs["response"], credential)
		require.Contains(t, outputs["response"], "*******")
	})

	t.Run("Missing", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, yaml)
		dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{SecretValues: map[string]string{}}))
		dagAgent.RunCheckErr(t, `failed to resolve steps.remote.with.password from secret ref "ssh/password": no value given`)
	})
}

// A node: required DAG started without a node fails before any step, rather
// than running commands meant for a node somewhere else.
func TestAgent_NodeTargetingFailsBeforeAnyStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		yaml    string
		opts    agent.Options
		wantErr string
	}{
		{
			name:    "NodeRequiredUnbound",
			yaml:    "node: required\nsteps:\n  - id: local\n    action: noop\n",
			wantErr: "declares node: required, so it runs only bound to a node",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			dag := th.DAG(t, tc.yaml)
			dagAgent := dag.Agent(test.WithAgentOptions(tc.opts))
			dagAgent.RunCheckErr(t, tc.wantErr)
			for _, node := range dagAgent.Status(th.Context).Nodes {
				require.Equal(t, ir.NodeNotStarted, node.Status, "step %q ran", node.Step.Name)
			}
		})
	}
}

// A dry run executes nothing, so it succeeds, and reports that the real run
// would fail on its node.
func TestAgent_NodeTargetingDryRunReportsRefusal(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, "node: required\nsteps:\n  - id: restart\n    run: systemctl restart app\n")
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{Dry: true, BoundNode: "web01", NodeSelector: nodeInventory{}}))
	dagAgent.RunSuccess(t)

	require.Contains(t, dagAgent.DryRunChecks(), runtime.DryRunCheck{
		Field:   "node",
		Message: `node "web01" is not in the project's inventories`,
	})
}

// A retry keeps the node its run was bound to, so a node: required run can be
// retried.
func TestAgent_RetryKeepsBoundNode(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, "node: required\nsteps:\n  - id: check\n    action: noop\n")
	nodes := nodeInventory{{Name: "web01", Host: "10.0.1.11"}}
	first := dag.Agent(test.WithAgentOptions(agent.Options{BoundNode: "web01", NodeSelector: nodes}))
	first.RunSuccess(t)
	status := first.Status(th.Context)
	require.Equal(t, "web01", status.BoundNode)

	retry := dag.Agent(test.WithAgentOptions(agent.Options{RetryTarget: &status, NodeSelector: nodes}))
	retry.RunSuccess(t)
	require.Equal(t, "web01", retry.Status(th.Context).BoundNode)
}
