// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// missingConnections fails the check of every connection, recording what
// each check was asked.
type missingConnections struct {
	mu     sync.Mutex
	checks []string
}

func (c *missingConnections) CheckConnection(_ context.Context, workspace string, ref ir.ConnectionRef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks = append(c.checks, fmt.Sprintf("%s/%s%v", workspace, ref.Name, ref.Servers))
	return fmt.Errorf("connection %q not found", ref.Name)
}

// Every declared connection is checked in the run's workspace, and a failed
// check fails the run before its first step, naming each connection.
func TestAgent_ConnectionCheckFailsBeforeAnyStep(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `
labels:
  - workspace=ops
steps:
  - name: local
    run: echo started
  - name: unread
    action: mcp.call
    with: {connection: work-google, server: gmail, tool: search_threads}
  - name: send
    action: mcp.call
    with: {connection: ops-mail, tool: send}
`)
	checker := &missingConnections{}
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{ConnectionChecker: checker}))
	err := dagAgent.Run(dagAgent.Context)
	require.ErrorContains(t, err, `connection "work-google" not found`)
	require.ErrorContains(t, err, `connection "ops-mail" not found`)
	require.Equal(t, ir.Failed, dagAgent.Status(th.Context).Status)

	require.Equal(t, []string{"ops/work-google[gmail]", "ops/ops-mail[]"}, checker.checks)
	for _, node := range dagAgent.Status(th.Context).Nodes {
		require.Equal(t, ir.NodeNotStarted, node.Status, "step %q ran", node.Step.Name)
	}
}

// Two workspace labels name no workspace, so the run is not checked against
// the .global connections, which serve only DAGs without the label.
func TestAgent_ConnectionCheckRejectsInvalidWorkspaceLabel(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `
labels:
  - workspace=ops
  - workspace=sales
steps:
  - name: unread
    action: mcp.call
    with: {connection: work-google, server: gmail, tool: search_threads}
`)
	checker := &missingConnections{}
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{ConnectionChecker: checker}))
	dagAgent.RunCheckErr(t, `connection "work-google": the DAG's workspace label`)
	require.Empty(t, checker.checks)
}
