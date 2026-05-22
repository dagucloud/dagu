// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileaudit

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_New(t *testing.T) {
	t.Run("ValidDir", func(t *testing.T) {
		store, err := New(t.TempDir(), 0)
		require.NoError(t, err)
		assert.NotNil(t, store)
	})

	t.Run("EmptyDir", func(t *testing.T) {
		_, err := New("", 0)
		assert.Error(t, err)
	})
}

func TestStore_Append(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	entry := audit.NewEntry(audit.CategoryUser, "login", "user-123", "testuser").
		WithIPAddress("192.168.1.1")

	err = store.Append(context.Background(), entry)
	require.NoError(t, err)
}

func TestStore_AppendNilEntry(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	err = store.Append(context.Background(), nil)
	assert.Error(t, err)
}

func TestStore_Query(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	// Add entries
	for range 5 {
		entry := audit.NewEntry(audit.CategoryUser, "login", "user-123", "testuser")
		err = store.Append(context.Background(), entry)
		require.NoError(t, err)
	}

	result, err := store.Query(context.Background(), audit.QueryFilter{})
	require.NoError(t, err)
	assert.Equal(t, 5, result.Total)
	assert.Len(t, result.Entries, 5)
}

func TestStore_QueryWithCategory(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	// Add mixed entries
	err = store.Append(context.Background(), audit.NewEntry(audit.CategoryUser, "login", "u1", "user1"))
	require.NoError(t, err)
	err = store.Append(context.Background(), audit.NewEntry(audit.CategoryTerminal, "command", "u1", "user1"))
	require.NoError(t, err)
	err = store.Append(context.Background(), audit.NewEntry(audit.CategoryUser, "logout", "u1", "user1"))
	require.NoError(t, err)

	result, err := store.Query(context.Background(), audit.QueryFilter{Category: audit.CategoryUser})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)
}

func TestStore_QueryWithStructuredMCPFields(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	mcpEntry := audit.NewEntry(audit.CategoryMCP, "mcp.tool_call.denied", "apikey:key-1", "apikey:deploy").
		WithIPAddress("203.0.113.10")
	mcpEntry.Source = "mcp"
	mcpEntry.Surface = "mcp"
	mcpEntry.Result = "denied"
	mcpEntry.CorrelationID = "corr-1"
	mcpEntry.Workspace = "default"
	mcpEntry.CredentialID = "key-1"
	mcpEntry.CredentialType = "api_key"
	mcpEntry.ResourceType = "dag"
	mcpEntry.ResourceID = "deploy-prod"
	mcpEntry.MCPTool = "dagu_execute"

	restEntry := audit.NewEntry(audit.CategoryDAG, "dag_execute", "user-1", "alice")
	restEntry.Source = "rest"
	restEntry.Surface = "rest_api"
	restEntry.Result = "succeeded"

	require.NoError(t, store.Append(context.Background(), mcpEntry))
	require.NoError(t, store.Append(context.Background(), restEntry))

	result, err := store.Query(context.Background(), audit.QueryFilter{
		Source:        "mcp",
		Surface:       "mcp",
		Result:        "denied",
		Action:        "mcp.tool_call.denied",
		Workspace:     "default",
		CredentialID:  "key-1",
		CorrelationID: "corr-1",
		ResourceType:  "dag",
		ResourceID:    "deploy-prod",
		MCPTool:       "dagu_execute",
		IPAddress:     "203.0.113.10",
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	assert.Equal(t, "mcp.tool_call.denied", result.Entries[0].Action)
}

func TestStore_QueryWithPagination(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	// Add entries
	for range 10 {
		entry := audit.NewEntry(audit.CategoryUser, "login", "user-123", "testuser")
		err = store.Append(context.Background(), entry)
		require.NoError(t, err)
	}

	result, err := store.Query(context.Background(), audit.QueryFilter{Limit: 3, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, 10, result.Total)
	assert.Len(t, result.Entries, 3)
}

func TestStore_QueryWithTimeRange(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	now := time.Now().UTC()
	err = store.Append(context.Background(), audit.NewEntry(audit.CategoryUser, "login", "u1", "user1"))
	require.NoError(t, err)

	result, err := store.Query(context.Background(), audit.QueryFilter{
		StartTime: now.Add(-1 * time.Hour),
		EndTime:   now.Add(1 * time.Hour),
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.Total, 1)
}

func TestStore_QueryOffsetBeyondTotal(t *testing.T) {
	store, err := New(t.TempDir(), 0)
	require.NoError(t, err)

	err = store.Append(context.Background(), audit.NewEntry(audit.CategoryUser, "login", "u1", "user1"))
	require.NoError(t, err)

	result, err := store.Query(context.Background(), audit.QueryFilter{Offset: 100})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Len(t, result.Entries, 0)
}
