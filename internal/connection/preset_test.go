// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/connection"
)

const scope = "https://www.googleapis.com/auth/"

func TestRequestedScopes(t *testing.T) {
	t.Parallel()
	conn := &connection.Connection{
		Provider: connection.ProviderGoogle,
		Services: []connection.Service{
			{Name: "gmail", Access: connection.AccessWrite},
			{Name: "drive", Access: connection.AccessRead},
		},
		Scopes: []string{scope + "drive.readonly", scope + "tasks"},
	}
	assert.Equal(t, []string{
		"openid",
		"email",
		scope + "gmail.readonly",
		scope + "gmail.compose",
		scope + "gmail.modify",
		scope + "drive.readonly",
		scope + "tasks",
	}, conn.RequestedScopes())
}

func TestAllBoundHosts(t *testing.T) {
	t.Parallel()
	t.Run("Google", func(t *testing.T) {
		t.Parallel()
		conn := &connection.Connection{
			Provider: connection.ProviderGoogle,
			Services: []connection.Service{
				{Name: "calendar", Access: connection.AccessRead},
				{Name: "drive", Access: connection.AccessRead},
				{Name: "search", Access: connection.AccessRead},
			},
			BoundHosts: []string{"tasks.googleapis.com"},
		}
		assert.Equal(t, []string{
			"calendarmcp.googleapis.com",
			"www.googleapis.com",
			"drivemcp.googleapis.com",
			"workspacemcp.googleapis.com",
			"tasks.googleapis.com",
		}, conn.AllBoundHosts())
	})
	t.Run("MCPResourceWithPort", func(t *testing.T) {
		t.Parallel()
		conn := &connection.Connection{
			Provider: connection.ProviderMCP,
			Resource: "https://mcp.example.com:8443/mcp",
		}
		assert.Equal(t, []string{"mcp.example.com:8443"}, conn.AllBoundHosts())
	})
}

// A preset that gains a scope in an upgrade reports it as missing for a
// connection whose grant predates the upgrade.
func TestMissingScopes(t *testing.T) {
	t.Parallel()
	conn := &connection.Connection{Provider: connection.ProviderGoogle}
	granted := []string{"openid", "email", scope + "gmail.readonly"}

	read := connection.Service{Name: "gmail", Access: connection.AccessRead}
	assert.Empty(t, conn.MissingScopes(read, granted))

	write := connection.Service{Name: "gmail", Access: connection.AccessWrite}
	assert.Equal(t, []string{scope + "gmail.compose", scope + "gmail.modify"}, conn.MissingScopes(write, granted))
}

func TestLookupPreset(t *testing.T) {
	t.Parallel()
	people, ok := connection.LookupPreset(connection.ProviderGoogle, "people")
	assert.True(t, ok)
	assert.Equal(t, "https://people.googleapis.com/mcp/v1", people.MCPURL)
	assert.Equal(t, []string{"people.googleapis.com"}, people.Hosts())
	assert.Equal(t, people.Scopes(connection.AccessRead), people.Scopes(connection.AccessWrite))

	_, ok = connection.LookupPreset(connection.ProviderMicrosoft, "gmail")
	assert.False(t, ok)
}
