// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newAIMCPServerStore(t *testing.T) (*store.AIMCPServerStore, persis.Collection) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("ai_mcp_servers")
	s, err := store.NewAIMCPServerStore(col)
	require.NoError(t, err)
	return s, col
}

func aiMCPServer(scope aiendpoint.Scope, name string) *aiendpoint.MCPServer {
	now := time.Now().UTC()
	return &aiendpoint.MCPServer{
		Name:  name,
		URL:   "https://docs.example.com/mcp",
		Scope: scope,
		Allow: aiendpoint.Allow{DAGs: []string{"*"}},
		Token: &aiendpoint.Credential{
			Header: "authorization", SecretID: "secret-" + name, Prefix: "tok", Fingerprint: "0123456789ab", UpdatedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestAIMCPServerStoreCreateGetListDelete(t *testing.T) {
	ctx := context.Background()
	s, col := newAIMCPServerStore(t)

	require.NoError(t, s.Create(ctx, aiMCPServer(aiendpoint.GlobalScope(), "cle1")))
	require.NoError(t, s.Create(ctx, aiMCPServer(aiendpoint.WorkspaceScope("team"), "cle1")))
	require.NoError(t, s.Create(ctx, aiMCPServer(aiendpoint.GlobalScope(), "alpha")))

	// The record IDs have the scope, so a name is unique in its scope only.
	_, err := col.Get(ctx, "global/cle1")
	require.NoError(t, err)
	_, err = col.Get(ctx, "workspaces/team/cle1")
	require.NoError(t, err)
	require.ErrorIs(t, s.Create(ctx, aiMCPServer(aiendpoint.GlobalScope(), "cle1")), aiendpoint.ErrAlreadyExists)

	got, err := s.Get(ctx, aiendpoint.WorkspaceScope("team"), "cle1")
	require.NoError(t, err)
	assert.Equal(t, "secret-cle1", got.Token.SecretID)
	assert.Equal(t, "team", got.Scope.Workspace)

	// The global scope comes first, then the names in order.
	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "alpha", all[0].Name)
	assert.True(t, all[0].Scope.IsGlobal())
	assert.Equal(t, "cle1", all[1].Name)
	assert.True(t, all[1].Scope.IsGlobal())
	assert.Equal(t, "team", all[2].Scope.Workspace)

	require.NoError(t, s.Delete(ctx, aiendpoint.GlobalScope(), "cle1"))
	_, err = s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
	require.ErrorIs(t, s.Delete(ctx, aiendpoint.GlobalScope(), "cle1"), aiendpoint.ErrNotFound)
	_, err = s.Get(ctx, aiendpoint.WorkspaceScope("team"), "cle1")
	require.NoError(t, err, "the workspace entry stays")
}

func TestAIMCPServerStoreUpdate(t *testing.T) {
	ctx := context.Background()
	s, _ := newAIMCPServerStore(t)
	ep := aiMCPServer(aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, s.Create(ctx, ep))

	_, err := s.Update(ctx, ep.Scope, "cle1", func(server *aiendpoint.MCPServer) error {
		server.URL = "https://changed.example.com/mcp"
		return nil
	})
	require.NoError(t, err)
	got, err := s.Get(ctx, ep.Scope, "cle1")
	require.NoError(t, err)
	assert.Equal(t, "https://changed.example.com/mcp", got.URL)

	_, err = s.Update(ctx, aiendpoint.GlobalScope(), "nope", func(*aiendpoint.MCPServer) error { return nil })
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
}

func TestAIMCPServerStoreRejectsInvalidNames(t *testing.T) {
	ctx := context.Background()
	s, _ := newAIMCPServerStore(t)

	// A name with a slash cannot reach another record.
	require.ErrorIs(t, s.Create(ctx, aiMCPServer(aiendpoint.GlobalScope(), "../x")), aiendpoint.ErrInvalid)
	_, err := s.Get(ctx, aiendpoint.GlobalScope(), "a/b")
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
}
