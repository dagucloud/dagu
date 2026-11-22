// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
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

func (n nodeInventory) Nodes(context.Context) ([]inventory.ResolvedNode, error) {
	return n, nil
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
steps:
  - id: where
    run: echo ${node.name} ${node.labels.role} ${node.labels."gpu.type"} ${node.labels}
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
}

// Fields under an ssh.run step's with override the node's.
func TestAgent_BoundRunWithOverridesNode(t *testing.T) {
	t.Parallel()

	node, server := startNode(t, "web01")
	node.User = "nobody"
	dagAgent, _ := runBound(t, nodeInventory{node}, `
node: required
steps:
  - id: as_tester
    action: ssh.run
    with:
      user: `+nodeUser+`
      command: "true"
`, newCountingSecretResolver(map[string]string{passwordRef: nodePassword}))
	dagAgent.RunSuccess(t)

	require.Len(t, server.Commands(), 1)
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
