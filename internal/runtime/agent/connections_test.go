// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/runtime"
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

func (c *missingConnections) Connections(string) runtime.Connections { return nil }

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
	dagAgent.RunCheckErr(t, `connection "work-google": the DAG's workspace labels name no single valid workspace`)
	require.Empty(t, checker.checks)
}

// A run's steps get the connections of the DAG's workspace, and the run
// keeps each token it receives, so a later change of the stored token does
// not reach it.
func TestStoreConnections(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := file.NewServerStores(&config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}})
	store, err := stores.ConnectionStore()
	require.NoError(t, err)
	app, err := store.CreateApp(ctx, &connection.App{
		Name: "Example", Provider: connection.ProviderOAuth2, Kind: connection.AppKindBYO,
		ClientID: "client", ClientType: connection.ClientTypeWeb,
		Endpoints: &connection.Endpoints{Token: "https://auth.example.com/token"},
	})
	require.NoError(t, err)
	stored, err := store.Create(ctx, &connection.Connection{
		Workspace: "ops", Name: "work", Provider: connection.ProviderOAuth2,
		App: app.ID, Grant: connection.GrantAuthorizationCode,
	})
	require.NoError(t, err)
	setToken := func(token string) {
		_, err := store.UpdateTokenState(ctx, stored, func(st *connection.TokenState) error {
			st.Status = connection.TokenStatusConnected
			st.Account = "ops@example.com"
			st.AccessTokens = map[string]connection.AccessToken{"": {Token: token, ExpiresAt: time.Now().Add(time.Hour)}}
			return nil
		})
		require.NoError(t, err)
	}
	setToken("at-1")

	checker := agent.NewStoreConnectionChecker(stores.ConnectionStore, stores.ConnectionLocks(), nil)
	conns := checker.Connections("ops")
	conn, err := conns.Connection(ctx, "work")
	require.NoError(t, err)
	require.Equal(t, stored.ID, conn.ID)

	for range 2 {
		token, _, err := conns.Token(ctx, conn, "", broker.StepMinTTL)
		require.NoError(t, err)
		require.Equal(t, "at-1", token)
		setToken("at-2")
	}
	account, err := conns.Account(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, "ops@example.com", account)

	_, err = checker.Connections("sales").Connection(ctx, "work")
	require.ErrorIs(t, err, connection.ErrNotFound)
}
