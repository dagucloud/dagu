// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

// connectionDAG runs, on a worker, a child in process that calls Gmail
// through the connection work-google of workspace ops. Only the child
// declares the connection, so the child's own check decides.
const connectionDAG = `
worker_selector:
  test: "true"
labels:
  - workspace=ops
steps:
  - name: child
    action: dag.run
    with:
      dag: mail-child
---
name: mail-child
labels:
  - workspace=ops
steps:
  - name: read
    action: mcp.call
    with: {connection: work-google, server: gmail, tool: search_threads}
`

// An in-process child of a run on a worker checks its connections through
// the coordinator before its first step. A connection its workspace lacks
// fails the child with no step started. Once the workspace has it, the step
// starts, and fails without a token: the connection was never connected.
func TestConnections_CheckedOnWorker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		create      bool
		wantStarted bool
	}{
		{name: "Missing"},
		{name: "Present", create: true, wantStarted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newTestFixture(t, connectionDAG)
			if tt.create {
				createWorkGoogle(t, f)
			}
			_, err := f.dispatchDirect()
			require.NoError(t, err)
			status := f.waitForStatus(ir.Failed, executionStatusTimeout())
			require.Len(t, status.Nodes, 1)
			require.Len(t, status.Nodes[0].SubRuns, 1)

			rootRef := ir.NewDAGRunRef(status.Name, status.DAGRunID)
			child := readDistributedSubAttemptStatus(t, f, rootRef, status.Nodes[0].SubRuns[0].DAGRunID)
			require.Equal(t, ir.Failed, child.Status)
			started := len(child.Nodes) == 1 && child.Nodes[0].Status != ir.NodeNotStarted
			require.Equal(t, tt.wantStarted, started, "child nodes: %+v", child.Nodes)
		})
	}
}

// createWorkGoogle adds work-google to workspace ops on the coordinator,
// with an app set up for Google's MCP servers.
func createWorkGoogle(t *testing.T, f *testFixture) {
	t.Helper()
	ctx := f.coord.Context
	store, err := persisfile.NewServerStores(f.coord.Config).ConnectionStore()
	require.NoError(t, err)
	app, err := store.CreateApp(ctx, &connection.App{
		Name: "Google", Provider: connection.ProviderGoogle, Kind: connection.AppKindBYO,
		ClientID: "client", ClientSecret: "secret", ClientType: connection.ClientTypeWeb, Agent: true,
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, &connection.Connection{
		Workspace: "ops", Name: "work-google", Provider: connection.ProviderGoogle, App: app.ID,
		Grant:    connection.GrantAuthorizationCode,
		Services: []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
	})
	require.NoError(t, err)
}
