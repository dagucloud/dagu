// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

// The fixture follows Spec 107's examples: workspaces ops and sales, a
// Google app with the agent flag and one without, and a service account.
func TestAdmit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := admissionStore(t)

	tests := []struct {
		name      string
		workspace string
		conn      string
		servers   []string
		// wantErr is empty when the run is admitted.
		wantErr  string
		notFound bool
	}{
		{name: "AgentApp", workspace: "ops", conn: "work-google", servers: []string{"gmail"}},
		{name: "SameNameOtherWorkspace", workspace: "sales", conn: "work-google", servers: []string{"gmail"}},
		{name: "NoServer", workspace: "ops", conn: "ops-mail"},
		{name: "ServiceWithoutMCPServer", workspace: "ops", conn: "ops-mail", servers: []string{"mailbox"}},
		{name: "ServiceAccountNeedsNoAgent", workspace: "ops", conn: "ops-robot", servers: []string{"gmail"}},
		{name: "NoWorkspaceLabel", conn: "shared"},
		// Admission reads no token state, so a broken connection runs its
		// other steps and fails only where a step asks for a token.
		{name: "NeedsReconnectOnInvalidApp", workspace: "ops", conn: "broken", servers: []string{"gmail"}},
		{
			name: "Missing", workspace: "ops", conn: "missing", notFound: true,
			wantErr: `connection not found: "missing" in workspace "ops"`,
		},
		{
			name: "GlobalIsNoFallback", workspace: "ops", conn: "shared", notFound: true,
			wantErr: `"shared" in workspace "ops"`,
		},
		{
			name: "OtherWorkspaceIsNoFallback", conn: "work-google", notFound: true,
			wantErr: `"work-google" in the connections of DAGs without a workspace label`,
		},
		{
			name: "ServerNotAService", workspace: "ops", conn: "work-google", servers: []string{"drive"},
			wantErr: `connection "work-google": server "drive" is not one of its services (gmail)`,
		},
		{
			name: "AppLacksAgent", workspace: "ops", conn: "ops-mail", servers: []string{"mailbox", "gmail"},
			wantErr: `connection "ops-mail": app "google-std" lacks the agent flag, which Google MCP server "gmail" needs`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := connection.Admit(ctx, store, tt.workspace, tt.conn, tt.servers)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, tt.notFound, errors.Is(err, connection.ErrNotFound))
		})
	}
}

func admissionStore(t *testing.T) connection.Store {
	t.Helper()
	ctx := context.Background()
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}}
	store, err := file.NewServerStores(cfg).ConnectionStore()
	require.NoError(t, err)

	newApp := func(app connection.App) *connection.App {
		app.Provider = connection.ProviderGoogle
		app.Kind = connection.AppKindBYO
		app.ClientID = app.Name + "-client"
		created, err := store.CreateApp(ctx, &app)
		require.NoError(t, err)
		return created
	}
	agentApp := newApp(connection.App{Name: "google-agent", ClientType: connection.ClientTypeWeb, Agent: true})
	stdApp := newApp(connection.App{Name: "google-std", ClientType: connection.ClientTypeWeb})
	robotApp := newApp(connection.App{Name: "google-robot", ClientType: connection.ClientTypeServiceAccount, ClientSecret: "key"})
	invalidApp := newApp(connection.App{Name: "google-invalid", ClientType: connection.ClientTypeWeb, Agent: true})
	_, err = store.UpdateApp(ctx, invalidApp.ID, func(app *connection.App) error {
		app.Status = connection.AppStatusInvalid
		return nil
	})
	require.NoError(t, err)

	gmailRead := []connection.Service{{Name: "gmail", Access: connection.AccessRead}}
	for _, conn := range []connection.Connection{
		{Workspace: "ops", Name: "work-google", App: agentApp.ID, Services: gmailRead},
		{Workspace: "sales", Name: "work-google", App: agentApp.ID, Services: gmailRead},
		{Workspace: "ops", Name: "ops-mail", App: stdApp.ID, Services: []connection.Service{
			{Name: "gmail", Access: connection.AccessWrite},
			{Name: "mailbox", Access: connection.AccessWrite},
		}},
		{
			Workspace: "ops", Name: "ops-robot", App: robotApp.ID, Services: gmailRead,
			Grant: connection.GrantServiceAccount, Subject: "ops@example.com",
		},
		{Workspace: "ops", Name: "broken", App: invalidApp.ID, Services: gmailRead},
		{Workspace: secret.GlobalWorkspace, Name: "shared", App: stdApp.ID, Services: gmailRead},
	} {
		conn.Provider = connection.ProviderGoogle
		if conn.Grant == "" {
			conn.Grant = connection.GrantAuthorizationCode
		}
		created, err := store.Create(ctx, &conn)
		require.NoError(t, err)
		if conn.Name == "broken" {
			_, err = store.UpdateTokenState(ctx, created, func(st *connection.TokenState) error {
				st.Status = connection.TokenStatusNeedsReconnect
				return nil
			})
			require.NoError(t, err)
		}
	}
	return store
}
