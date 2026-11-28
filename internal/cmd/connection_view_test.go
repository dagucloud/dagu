// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

// list prints each connection of the workspace with its account and
// status, or says that the workspace has none.
func TestConnectionList(t *testing.T) {
	t.Parallel()
	pending := testConnection(api.ConnectionStatusPending, "")
	pending.Name = "team"
	pending.Services = append(pending.Services, api.ConnectionService{Name: "calendar", Access: api.ConnectionAccessWrite})
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /connections": api.ConnectionListResponse{Connections: []api.Connection{
			testConnection(api.ConnectionStatusConnected, "ops@example.com"), pending,
		}},
	})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	require.NoError(t, c.list(context.Background()))

	assert.Contains(t, fake.sent(), "GET /connections?workspace=ops")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"NAME", "PROVIDER", "ACCOUNT", "STATUS", "SERVICES"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"work", "google", "ops@example.com", "connected", "gmail"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"team", "google", "-", "pending", "gmail,calendar:write"}, strings.Fields(lines[2]))

	fake.set("GET /connections", api.ConnectionListResponse{Connections: []api.Connection{}})
	c, out = testConnectionCLI(client, strings.NewReader(""), nil)
	c.workspace = ""
	require.NoError(t, c.list(context.Background()))
	assert.Equal(t, "No connections in workspace global. Add one with 'dagu connection add google'.\n", out.String())
}

// show prints the connection with its app's name when the caller may read
// the app, its ID when the API refuses the app, and the error beside the
// ID otherwise. It says how to grant a missing scope, and drops the
// control characters of a provider's reason.
func TestConnectionShow(t *testing.T) {
	t.Parallel()
	conn := testConnection(api.ConnectionStatusNeedsReconnect, "ops@example.com")
	conn.StatusReason = new("invalid_grant: Token has been \x1b[2Jexpired or revoked.")
	conn.Services[0].MissingScopes = []string{"https://www.googleapis.com/auth/gmail.readonly"}
	tests := []struct {
		name    string
		app     any
		wantApp string
	}{
		{name: "AppReadable", app: testApp(api.OAuthClientTypeDesktop), wantApp: `App:\s+google-agent \(app-1\)\n`},
		{name: "AppHidden", app: http.StatusForbidden, wantApp: `App:\s+app-1\n`},
		{name: "AppFailed", app: http.StatusInternalServerError, wantApp: `App:\s+app-1 \(not read: Internal Server Error\)\n`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, client := newFakeConnectionAPI(t, map[string]any{"GET /connections/work": conn, "GET /oauth-apps/app-1": tt.app})
			c, out := testConnectionCLI(client, strings.NewReader(""), nil)

			require.NoError(t, c.show(context.Background(), "work"))

			printed := out.String()
			assert.Regexp(t, tt.wantApp, printed)
			assert.Contains(t, printed, "needs_reconnect: invalid_grant: Token has been [2Jexpired or revoked.")
			assert.NotContains(t, printed, "\x1b")
			assert.Contains(t, printed, "Service gmail lacks https://www.googleapis.com/auth/gmail.readonly. Run 'dagu connection reconnect work --workspace ops' to grant it.")
		})
	}
}

// tools prints each service's tools as a table without control
// characters, and fails when a service's tools could not be listed.
func TestConnectionTools(t *testing.T) {
	t.Parallel()
	listed := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	resp := api.ConnectionToolsResponse{Servers: []api.ConnectionToolServer{
		{Server: "gmail", ListedAt: &listed, Tools: []api.ConnectionTool{
			{Name: "search_threads", Description: new("Search threads.\nMore text."), Annotations: &map[string]any{"readOnlyHint": true}},
			{Name: "send_message", Description: new("Send a \x1b[31mmessage\x1b[0m.")},
		}},
		{Server: "calendar", Tools: []api.ConnectionTool{}, Error: new("the token lacks a scope")},
	}}
	_, client := newFakeConnectionAPI(t, map[string]any{"GET /connections/work/tools": resp})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	err := c.tools(context.Background(), "work", "", false)

	require.ErrorContains(t, err, "the tools of calendar could not be listed")
	printed := out.String()
	assert.Contains(t, printed, "gmail: 2 tools, listed "+formatConnectionTime(&listed))
	assert.Contains(t, printed, "search_threads  read-only  Search threads.")
	assert.NotContains(t, printed, "More text.")
	assert.Contains(t, printed, "send_message    writes     Send a [31mmessage[0m.")
	assert.NotContains(t, printed, "\x1b")
	assert.Contains(t, printed, "calendar: the token lacks a scope")
}

// tools --json prints the server's body as it was sent, so an input
// schema keeps an integer bound that a float would round, and text keeps
// its angle brackets. It asks only for the service --server names.
func TestConnectionToolsJSON(t *testing.T) {
	t.Parallel()
	body := `{"servers":[{"server":"gmail","tools":[{"name":"search_threads","description":"a <b> & c",` +
		`"inputSchema":{"type":"object","properties":{"limit":{"type":"integer","maximum":9223372036854775807}}}}]}]}`
	fake, client := newFakeConnectionAPI(t, map[string]any{"GET /connections/work/tools": rawReply(body)})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	require.NoError(t, c.tools(context.Background(), "work", "gmail", true))

	assert.Contains(t, fake.sent(), "GET /connections/work/tools?server=gmail&workspace=ops")
	assert.Contains(t, out.String(), `"maximum": 9223372036854775807`)
	assert.Contains(t, out.String(), `"description": "a <b> & c"`)
	assert.True(t, json.Valid([]byte(out.String())))
}

// providers prints the redirect modes with the URI to register, and each
// provider's services and setup pages.
func TestConnectionProviders(t *testing.T) {
	t.Parallel()
	_, client := newFakeConnectionAPI(t, map[string]any{"GET /connection-providers": api.ConnectionProviderListResponse{
		PublicUrl: new("https://dagu.example.com"),
		Redirects: []api.ConnectionRedirect{
			{Mode: api.ConnectionRedirectModeServerCallback, ClientType: api.OAuthClientTypeWeb, RedirectUri: new("https://dagu.example.com/oauth/callback")},
			{Mode: api.ConnectionRedirectModeLoopback, ClientType: api.OAuthClientTypeDesktop},
		},
		Providers: []api.ConnectionProvider{{
			Name: api.ConnectionProviderNameGoogle,
			Services: []api.ConnectionPreset{
				{Name: "gmail", McpUrl: new("https://gmailmcp.googleapis.com/mcp/v1"), Apis: []string{"gmail.googleapis.com"}},
				{Name: "chat", Preview: true, Apis: []string{"chat.googleapis.com"}},
			},
			SetupLinks: &api.ConnectionProviderSetupLinks{AdminTrust: new("https://admin.google.com/ac/owl")},
		}},
	}})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	require.NoError(t, c.providers(context.Background()))

	printed := out.String()
	assert.Contains(t, printed, "Public URL: https://dagu.example.com")
	assert.Contains(t, printed, "server_callback  web          https://dagu.example.com/oauth/callback")
	assert.Contains(t, printed, "loopback         desktop      http://127.0.0.1 on a port the CLI picks")
	assert.Contains(t, printed, "gmail    yes                     gmail.googleapis.com")
	assert.Contains(t, printed, "chat     no (developer preview)")
	assert.Contains(t, printed, "Admin trust:  https://admin.google.com/ac/owl")
}

// apps list prints each app, apps remove names one by ID or a unique
// name, and an unknown name points at apps list on the same server.
func TestConnectionApps(t *testing.T) {
	t.Parallel()
	twin := testApp(api.OAuthClientTypeWeb)
	twin.Id, twin.Name = "app-3", "twin"
	twin2 := twin
	twin2.Id = "app-4"
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /oauth-apps":          api.OAuthAppListResponse{Apps: []api.OAuthApp{testApp(api.OAuthClientTypeDesktop), twin, twin2}},
		"DELETE /oauth-apps/app-1": nil,
	})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	require.NoError(t, c.listApps(context.Background()))
	assert.Contains(t, out.String(), "app-1  google-agent  google    desktop      true   ok      123-abc.apps.googleusercontent.com")

	require.NoError(t, c.removeApp(context.Background(), "google-agent"))
	assert.Contains(t, fake.sent(), "DELETE /oauth-apps/app-1?")
	assert.Contains(t, out.String(), "OAuth app google-agent (app-1) was removed.")

	require.ErrorContains(t, c.removeApp(context.Background(), "twin"), `2 OAuth apps are named "twin"; name one by its ID`)
	c.remoteContext = "production"
	require.ErrorContains(t, c.removeApp(context.Background(), "gone"), "'dagu connection apps list --context production' lists them")
}
