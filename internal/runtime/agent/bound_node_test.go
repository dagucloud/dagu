// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// nodeInventory is a fixed inventory, matched as the store matches.
type nodeInventory []inventory.ResolvedNode

func (n nodeInventory) Select(_ context.Context, selector string) ([]inventory.ResolvedNode, error) {
	filters, err := inventory.ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	return inventory.Select(n, filters), nil
}

func (n nodeInventory) Named(_ context.Context, names []string) ([]inventory.ResolvedNode, error) {
	return inventory.Named(n, names), nil
}

const (
	nodeUser     = "tester"
	nodePassword = "node-password-4k9q"
	passwordRef  = "ssh/web-password"
)

// startNode starts an SSH server that accepts nodePassword for nodeUser, and
// returns a node that reaches it, with its host key pinned and its password
// given as passwordRef.
func startNode(t *testing.T, name string, labels ...string) (inventory.ResolvedNode, *harness.SSHServer) {
	t.Helper()

	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: nodeUser, Password: nodePassword})
	port, err := strconv.Atoi(server.Port)
	require.NoError(t, err)
	return inventory.ResolvedNode{
		Name:          name,
		Host:          "127.0.0.1",
		Port:          port,
		User:          nodeUser,
		Auth:          ir.SSHAuth{PasswordSecret: passwordRef},
		HostKey:       strings.TrimSpace(string(ssh.MarshalAuthorizedKey(server.HostKey))),
		StrictHostKey: true,
		Labels:        ir.NewLabels(labels),
		Description:   "Tokyo web",
	}, server
}

// runBound runs yaml bound to node web01 of nodes.
func runBound(t *testing.T, nodes nodeInventory, yaml string, resolver *countingSecretResolver) (*test.Agent, *test.DAG) {
	t.Helper()

	th := test.Setup(t)
	dag := th.DAG(t, yaml)
	return dag.Agent(test.WithAgentOptions(agent.Options{
		BoundNode:               "web01",
		NodeSelector:            nodes,
		SecretReferenceResolver: resolver,
	})), &dag
}

// A bound run reaches its node as DAG-level ssh reaches its host: steps with
// no action run there, with the node's credentials and fields. The password
// resolves once and is masked.
func TestAgent_BoundRunReachesNode(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01", "role=web", "gpu.type=a100")
	resolver := newCountingSecretResolver(map[string]string{passwordRef: nodePassword})
	dagAgent, dag := runBound(t, nodeInventory{node}, `
node: required
env:
  - ROLE: ${node.labels.role}
steps:
  - id: where
    run: echo ${node.name} $ROLE ${node.labels."gpu.type"} ${node.labels}
    output: WHERE
  - id: leak
    run: echo `+nodePassword+`
    output: LEAK
`, resolver)
	dagAgent.RunSuccess(t)

	require.Equal(t, 1, resolver.callCount(passwordRef))
	require.Len(t, server.Commands(), 2, "each step runs on the node")
	outputs := dag.ReadOutputs(t)
	require.Equal(t, "web01 web a100 gpu.type=a100,role=web", outputs["where"])
	require.NotContains(t, outputs["leak"], nodePassword)

	statusJSON, err := json.Marshal(dagAgent.Status(context.Background()))
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), nodePassword)
	require.Empty(t, dag.Steps[0].ExecutorConfig.Type, "the run routes its own copy of the DAG")
}

// nodeSecretResolver resolves secret refs only by the node whose connection
// uses them, as the coordinator serves node credentials to a worker.
type nodeSecretResolver struct {
	values map[string]string
	mu     sync.Mutex
	nodes  []string
}

func (r *nodeSecretResolver) ResolveReference(_ context.Context, ref secretref.Ref) (string, error) {
	return "", fmt.Errorf("secret ref %q resolved without its node", ref.Ref)
}

func (r *nodeSecretResolver) CheckReferenceAccessibility(ctx context.Context, ref secretref.Ref) error {
	_, err := r.ResolveReference(ctx, ref)
	return err
}

func (r *nodeSecretResolver) ResolveNodeReference(_ context.Context, node string, ref secretref.Ref) (string, error) {
	r.mu.Lock()
	r.nodes = append(r.nodes, node)
	r.mu.Unlock()
	return r.values[ref.Ref], nil
}

// A bound run on a worker reaches its node, and reads the node's credentials
// by the node, which is how a worker asks the coordinator for them.
func TestAgent_BoundRunOnWorker(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01")
	resolver := &nodeSecretResolver{values: map[string]string{passwordRef: nodePassword}}
	th := test.Setup(t)
	dag := th.DAG(t, "node: required\nsteps:\n  - id: where\n    run: echo ${node.name}\n    output: WHERE\n")
	dag.Agent(test.WithAgentOptions(agent.Options{
		BoundNode:               "web01",
		WorkerID:                "worker-1",
		NodeSelector:            nodeInventory{node},
		SecretReferenceResolver: resolver,
	})).RunSuccess(t)

	require.Len(t, server.Commands(), 1)
	require.Equal(t, "web01", dag.ReadOutputs(t)["where"])
	require.Equal(t, []string{"web01"}, resolver.nodes)
}

// Fields under an ssh.run step's with override the node's. A password
// replaces the node's key, which the server here does not accept, so the
// step signs in only if the password is the credential it sends.
func TestAgent_BoundRunWithOverridesNode(t *testing.T) {
	t.Parallel()

	const keyRef = "ssh/web-key"
	key, _ := harness.NewSSHKey(t)
	for _, tc := range []struct {
		name string
		node func(*inventory.ResolvedNode)
		with string
	}{
		{name: "User", node: func(n *inventory.ResolvedNode) { n.User = "nobody" }, with: "user: " + nodeUser},
		{name: "PasswordOverKey", node: func(n *inventory.ResolvedNode) { n.Auth = ir.SSHAuth{KeySecret: keyRef} }, with: "password: " + nodePassword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node, server := startNode(t, "web01")
			tc.node(&node)
			dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: connect
    action: ssh.run
    with:
      `+tc.with+`
      command: "true"
`, newCountingSecretResolver(map[string]string{passwordRef: nodePassword, keyRef: string(key)}))
			dagAgent.RunSuccess(t)

			require.Len(t, server.Commands(), 1)
		})
	}
}

// A bound run fails before any step starts, and sends no credential, when
// it cannot run on its node as the node is.
func TestAgent_BoundRunFailsBeforeAnyStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		yaml    string
		node    func(*inventory.ResolvedNode)
		wantErr string
	}{
		{
			name:    "MissingLabel",
			yaml:    "node: required\nsteps:\n  - id: drain\n    action: noop\n  - id: restart\n    run: systemctl restart ${node.labels.zone}\n",
			wantErr: `node "web01" has no label "zone"`,
		},
		{
			name:    "MissingLabelInEnv",
			yaml:    "node: required\nenv:\n  - ZONE: ${node.labels.zone}\nsteps:\n  - id: drain\n    action: noop\n",
			wantErr: `node "web01" has no label "zone"`,
		},
		{
			// A backslash in a path field separates the path, so the
			// reference resolves and is checked.
			name:    "MissingLabelInPath",
			yaml:    "node: required\nsteps:\n  - id: restart\n    run: systemctl restart app\n    stdout: 'C:\\logs\\${node.labels.zone}.log'\n",
			wantErr: `node "web01" has no label "zone"`,
		},
		{
			name:    "UnknownField",
			yaml:    "node: required\nsteps:\n  - id: restart\n    run: systemctl restart ${node.nmae}\n",
			wantErr: `${node.nmae} names no node field`,
		},
		{
			name:    "NotInInventory",
			yaml:    "node: required\nsteps:\n  - id: restart\n    run: systemctl restart app\n",
			node:    func(n *inventory.ResolvedNode) { n.Name = "web02" },
			wantErr: `node "web01" is not in the project's inventories`,
		},
		{
			name:    "Disabled",
			yaml:    "node: required\nsteps:\n  - id: restart\n    run: systemctl restart app\n",
			node:    func(n *inventory.ResolvedNode) { n.Disabled = true },
			wantErr: `node "web01" is disabled`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node, server := startNode(t, "web01")
			if tc.node != nil {
				tc.node(&node)
			}
			resolver := newCountingSecretResolver(map[string]string{passwordRef: nodePassword})
			dagAgent, _ := runBound(t, nodeInventory{node}, tc.yaml, resolver)
			dagAgent.RunCheckErr(t, tc.wantErr)

			for _, n := range dagAgent.Status(context.Background()).Nodes {
				require.Equal(t, ir.NodeNotStarted, n.Status, "step %q ran", n.Step.Name)
			}
			require.Zero(t, server.AuthAttempts(), "no credential reaches the node")
		})
	}
}

// A node whose host key is neither pinned nor trusted fails the step that
// connects, before any credential is sent.
func TestAgent_BoundRunRefusesUnknownHostKey(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01")
	node.HostKey = ""
	dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: restart
    run: systemctl restart app
`, newCountingSecretResolver(map[string]string{passwordRef: nodePassword}))
	dagAgent.RunCheckErr(t, `host key of node "web01" is unknown`)

	require.Zero(t, server.AuthAttempts(), "no credential reaches the node")
	require.Empty(t, server.Commands())
}

// An ssh.run step that names another host connects there, so a run whose
// steps all do never reads the node's credentials.
func TestAgent_BoundRunWithHostReadsNoNodeCredential(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01")
	resolver := newCountingSecretResolver(nil)
	dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: elsewhere
    action: ssh.run
    preconditions:
      - condition: "skip"
        expected: "run"
    continue_on:
      skipped: true
    with:
      host: app.invalid
      command: "true"
`, resolver)
	dagAgent.RunSuccess(t)

	require.Zero(t, resolver.callCount(passwordRef))
	require.Zero(t, server.AuthAttempts())
}

// A run whose steps all run where the run runs never connects, so it reads
// none of the node's credentials.
func TestAgent_BoundRunWithoutCommandsReadsNoCredential(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01")
	resolver := newCountingSecretResolver(nil)
	dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: drain
    action: noop
  - id: enable
    action: noop
`, resolver)
	dagAgent.RunSuccess(t)

	require.Zero(t, resolver.callCount(passwordRef))
	require.Zero(t, server.AuthAttempts())
}

// A step typed command or shell runs on the command executor, as a step with
// no action does, so it runs on the node and never on the Dagu host.
func TestAgent_BoundRunTypedCommandReachesNode(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01")
	dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: typed_command
    type: command
    command: "true"
  - id: typed_shell
    type: shell
    command: "true"
`, newCountingSecretResolver(map[string]string{passwordRef: nodePassword}))
	dagAgent.RunSuccess(t)

	require.Len(t, server.Commands(), 2, "each step runs on the node")
}

// A bastion or host key setting under a bound step's with fails the step,
// since the node's inventory sets its bastion and decides which host keys it
// accepts. The node's key is not pinned here, so a step that turned the check
// off would send the node's password to a host no one verified.
func TestAgent_BoundRunRejectsWithConnectionFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		with    string
		wantErr string
	}{
		{name: "Bastion", with: "bastion:\n        host: 127.0.0.1\n        user: " + nodeUser, wantErr: "with.bastion does not apply to a node"},
		{name: "StrictHostKey", with: "strict_host_key: false", wantErr: "with.strict_host_key does not apply to a node"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node, server := startNode(t, "web01")
			node.HostKey = ""
			dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: connect
    action: ssh.run
    with:
      `+tc.with+`
      command: "true"
`, newCountingSecretResolver(map[string]string{passwordRef: nodePassword}))
			dagAgent.RunCheckErr(t, tc.wantErr)

			require.Zero(t, server.AuthAttempts(), "no credential reaches the node")
		})
	}
}

// The label check reads only the fields that resolve values, so a
// description naming a label the node lacks does not fail the run.
func TestAgent_BoundRunLabelCheckSkipsDescription(t *testing.T) {
	t.Parallel()

	node, _ := startNode(t, "web01")
	dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
description: Drains the app in ${node.labels.zone}
steps:
  - id: drain
    action: noop
`, newCountingSecretResolver(nil))
	dagAgent.RunSuccess(t)
}
