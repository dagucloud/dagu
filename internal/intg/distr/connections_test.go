// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

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
// starts, and fails without a token, which no step on a worker asks for
// yet.
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

// exportDAG puts work-google's token in the environment of a step on a
// worker. The step prints the token in hex, which no mask hides, and as
// it is.
const exportDAG = `
worker_selector:
  test: "true"
labels:
  - workspace=ops
secrets:
  - name: GOOGLE_TOKEN
    provider: connection
    key: work-google
steps:
  - name: export
    command: |
      printf %s "$GOOGLE_TOKEN" | od -An -tx1 | tr -d ' \n'
      echo
      echo "token=$GOOGLE_TOKEN"
`

// A connection secret on a worker takes its token from the coordinator
// when the connection allows export, and the step's log streamed to the
// coordinator masks it. Without export the run fails at secret
// resolution, naming the connection.
func TestConnections_ExportOnWorker(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == "windows" {
		t.Skip("uses POSIX shell steps")
	}
	const token = "worker-export-token"
	tests := []struct {
		name   string
		export bool
		want   ir.Status
	}{
		{name: "Allowed", export: true, want: ir.Succeeded},
		{name: "NotAllowed", want: ir.Failed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newTestFixture(t, exportDAG, withLogPersistence())
			store, conn := createWorkGoogle(t, f)
			ctx := f.coord.Context
			_, err := store.Update(ctx, connection.Key{Workspace: "ops", Name: "work-google"}, func(c *connection.Connection) error {
				c.AllowTokenExport = tt.export
				return nil
			})
			require.NoError(t, err)
			_, err = store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
				now := time.Now()
				st.Status = connection.TokenStatusConnected
				st.RefreshToken = "worker-refresh-token"
				st.AccessTokens = map[string]connection.AccessToken{
					"": {Token: token, IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
				}
				return nil
			})
			require.NoError(t, err)

			_, err = f.dispatchDirect()
			require.NoError(t, err)
			status := f.waitForStatus(tt.want, executionStatusTimeout())
			if !tt.export {
				// The run reports its startup failure in the scheduler log it
				// streams to the coordinator, which escapes the quotes.
				runLog := getLogContent(t, status.Log)
				require.Contains(t, runLog, "does not allow token export")
				require.Contains(t, runLog, "work-google")
				require.Equal(t, ir.NodeNotStarted, status.Nodes[0].Status)
				return
			}
			stdout := getLogContent(t, status.Nodes[0].Stdout)
			require.Contains(t, stdout, hex.EncodeToString([]byte(token)))
			require.Contains(t, stdout, "token=*******")
			for _, dir := range []string{f.coord.Config.Paths.DataDir, f.coord.Config.Paths.LogDir} {
				require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					data, err := os.ReadFile(path)
					if err == nil && strings.Contains(string(data), token) {
						t.Errorf("%s holds the token", path)
					}
					return err
				}))
			}
		})
	}
}

// createWorkGoogle adds work-google to workspace ops on the coordinator,
// with an app set up for Google's MCP servers, and returns the
// coordinator's store and the connection.
func createWorkGoogle(t *testing.T, f *testFixture) (connection.Store, *connection.Connection) {
	t.Helper()
	ctx := f.coord.Context
	store, err := persisfile.NewServerStores(f.coord.Config).ConnectionStore()
	require.NoError(t, err)
	app, err := store.CreateApp(ctx, &connection.App{
		Name: "Google", Provider: connection.ProviderGoogle, Kind: connection.AppKindBYO,
		ClientID: "client", ClientSecret: "secret", ClientType: connection.ClientTypeWeb, Agent: true,
	})
	require.NoError(t, err)
	conn, err := store.Create(ctx, &connection.Connection{
		Workspace: "ops", Name: "work-google", Provider: connection.ProviderGoogle, App: app.ID,
		Grant:    connection.GrantAuthorizationCode,
		Services: []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
	})
	require.NoError(t, err)
	return store, conn
}
