// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const (
	tokenOne  = "mcp-token-0123456789abcdefghij"
	tokenTwo  = "mcp-token-ZYXWVUTSRQPONMLKJIHG"
	headerVal = "header-value-0123456789abcdef"
)

type mcpFixture struct {
	svc     *aiendpoint.MCPService
	secrets *store.SecretStore
	backend *testutil.MemoryBackend
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	backend := testutil.NewMemoryBackend()
	secrets, err := store.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)
	servers, err := store.NewAIMCPServerStore(backend.Collection("ai_mcp_servers"))
	require.NoError(t, err)
	svc, err := aiendpoint.NewMCPService(servers, secrets)
	require.NoError(t, err)
	return &mcpFixture{svc: svc, secrets: secrets, backend: backend}
}

func mcpInput(name, token string, workspaces, dags []string) aiendpoint.MCPCreateInput {
	return aiendpoint.MCPCreateInput{
		Name:        name,
		URL:         "https://docs.example.com/mcp",
		Scope:       aiendpoint.GlobalScope(),
		Allow:       aiendpoint.Allow{Workspaces: workspaces, DAGs: dags},
		Credentials: aiendpoint.MCPCredentialsInput{Token: token},
		Actor:       "admin",
	}
}

func TestMCPCreateStoresTheCredentialsAsManagedSecrets(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)

	in := mcpInput("docs", "", []string{"*"}, []string{"fix-bug"})
	in.Credentials.Headers = map[string]string{"X-Api-Key": tokenOne, "X-Team": headerVal}
	server, err := f.svc.Create(ctx, in)
	require.NoError(t, err)

	// The display values follow the rule of the endpoint key, for each value.
	require.Len(t, server.Headers, 2)
	assert.Equal(t, "x-api-key", server.Headers[0].Header, "the headers are sorted and in lower case")
	assert.Equal(t, "x-team", server.Headers[1].Header)
	prefix, fingerprint := aiendpoint.KeyDisplay(tokenOne)
	assert.Equal(t, prefix, server.Headers[0].Prefix)
	assert.Equal(t, fingerprint, server.Headers[0].Fingerprint)
	assert.Nil(t, server.Token)

	// Each value is a managed secret with the ref of the spec.
	sec, err := f.secrets.GetByRef(ctx, "", "ai/mcp/docs/x-api-key")
	require.NoError(t, err)
	assert.Equal(t, secret.ProviderDaguManaged, sec.ProviderType)
	assert.Equal(t, sec.ID, server.Headers[0].SecretID)
	_, err = f.secrets.GetByRef(ctx, "", "ai/mcp/docs/x-team")
	require.NoError(t, err)

	// No record of the metadata store, and no JSON of the entry, holds a value.
	page, err := f.backend.Collection("ai_mcp_servers").List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	for _, rec := range page.Records {
		assert.NotContains(t, string(rec.Data), tokenOne)
		assert.NotContains(t, string(rec.Data), headerVal)
	}
	data, err := json.Marshal(server)
	require.NoError(t, err)
	assert.NotContains(t, string(data), tokenOne)
	secretPage, err := f.backend.Collection("secrets").List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	for _, rec := range secretPage.Records {
		assert.NotContains(t, string(rec.Data), tokenOne, "the secret store encrypts the value")
	}
}

func TestMCPTokenIsTheBearerHeader(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)
	_, err := f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	server, err := f.svc.ResolveMCPServer(ctx, "docs", "any-dag", "")
	require.NoError(t, err)
	creds, err := f.svc.ReadCredentials(ctx, server)
	require.NoError(t, err)
	assert.Equal(t, tokenOne, creds.Token)

	// The credentials replace the credentials of the client.
	h := http.Header{}
	h.Set("Authorization", "Bearer relay-secret")
	h.Set("Proxy-Authorization", "x")
	creds.Apply(h)
	assert.Equal(t, "Bearer "+tokenOne, h.Get("Authorization"))
	assert.Empty(t, h.Get("Proxy-Authorization"))

	// A format verb or a log line never shows a value.
	assert.NotContains(t, fmt.Sprintf("%v %+v %s", creds, creds, creds), tokenOne)
	var line bytes.Buffer
	slog.New(slog.NewJSONHandler(&line, nil)).Info("x", slog.Any("creds", creds))
	assert.NotContains(t, line.String(), tokenOne)
}

func TestMCPEntryWithNoCredentialIsValid(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)
	server, err := f.svc.Create(ctx, mcpInput("open", "", []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	assert.Nil(t, server.Token)

	resolved, err := f.svc.ResolveMCPServer(ctx, "open", "d", "")
	require.NoError(t, err)
	creds, err := f.svc.ReadCredentials(ctx, resolved)
	require.NoError(t, err)
	assert.Empty(t, creds.Token)
	assert.Empty(t, creds.Headers)
}

func TestMCPCreateRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)

	valid := func() aiendpoint.MCPCreateInput {
		return mcpInput("docs", tokenOne, []string{"*"}, []string{"*"})
	}
	many := map[string]string{}
	for i := range aiendpoint.MaxMCPHeaders + 1 {
		many["X-H"+string(rune('a'+i))] = "v"
	}
	for name, mutate := range map[string]func(*aiendpoint.MCPCreateInput){
		"bad name":                func(in *aiendpoint.MCPCreateInput) { in.Name = "Docs_1" },
		"name dagu":               func(in *aiendpoint.MCPCreateInput) { in.Name = "dagu" },
		"long name":               func(in *aiendpoint.MCPCreateInput) { in.Name = "a" + strings.Repeat("b", 64) },
		"relative url":            func(in *aiendpoint.MCPCreateInput) { in.URL = "docs.example.com" },
		"ftp url":                 func(in *aiendpoint.MCPCreateInput) { in.URL = "ftp://docs.example.com" },
		"url with user":           func(in *aiendpoint.MCPCreateInput) { in.URL = "https://u:p@docs.example.com" },
		"url with fragment":       func(in *aiendpoint.MCPCreateInput) { in.URL = "https://docs.example.com/#x" },
		"empty token header":      func(in *aiendpoint.MCPCreateInput) { in.Credentials.Headers = map[string]string{"X-K": ""} },
		"control character":       func(in *aiendpoint.MCPCreateInput) { in.Credentials.Token = "a\nb" },
		"long value":              func(in *aiendpoint.MCPCreateInput) { in.Credentials.Token = strings.Repeat("a", 8193) },
		"token and authorization": func(in *aiendpoint.MCPCreateInput) { in.Credentials.Headers = map[string]string{"Authorization": "x"} },
		"too many headers":        func(in *aiendpoint.MCPCreateInput) { in.Credentials.Token, in.Credentials.Headers = "", many },
		"header host": func(in *aiendpoint.MCPCreateInput) {
			in.Credentials.Token, in.Credentials.Headers = "", map[string]string{"Host": "x"}
		},
		"header session": func(in *aiendpoint.MCPCreateInput) {
			in.Credentials.Token, in.Credentials.Headers = "", map[string]string{"Mcp-Session-Id": "x"}
		},
		"header hop by hop": func(in *aiendpoint.MCPCreateInput) {
			in.Credentials.Token, in.Credentials.Headers = "", map[string]string{"Upgrade": "x"}
		},
		"header bad characters": func(in *aiendpoint.MCPCreateInput) {
			in.Credentials.Token, in.Credentials.Headers = "", map[string]string{"X_K": "x"}
		},
		"header twice": func(in *aiendpoint.MCPCreateInput) {
			in.Credentials.Token, in.Credentials.Headers = "", map[string]string{"X-K": "x", "x-k": "y"}
		},
		"workspaces missing":      func(in *aiendpoint.MCPCreateInput) { in.Allow.Workspaces = nil },
		"dags missing":            func(in *aiendpoint.MCPCreateInput) { in.Allow.DAGs = nil },
		"wildcard with a name":    func(in *aiendpoint.MCPCreateInput) { in.Allow.DAGs = []string{"*", "a"} },
		"workspaces in workspace": func(in *aiendpoint.MCPCreateInput) { in.Scope = aiendpoint.WorkspaceScope("team") },
	} {
		in := valid()
		mutate(&in)
		_, err := f.svc.Create(ctx, in)
		require.ErrorIs(t, err, aiendpoint.ErrInvalid, name)
		assert.NotContains(t, err.Error(), tokenOne, name)
	}

	// None of the failed calls left a record or a secret.
	list, err := f.svc.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)
	secrets, err := f.secrets.List(ctx, secret.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, secrets)

	// A URL can have a query: it is not a base URL of a model API.
	in := valid()
	in.URL = "https://docs.example.com/mcp?workspace=main"
	_, err = f.svc.Create(ctx, in)
	require.NoError(t, err)
	in.Name = "plain"
	in.URL = "http://localhost:8080/mcp"
	_, err = f.svc.Create(ctx, in)
	require.NoError(t, err, "the scheme http is valid")
}

func TestMCPDuplicateNameInAScopeOnly(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)

	_, err := f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"*"}, []string{"*"}))
	require.ErrorIs(t, err, aiendpoint.ErrAlreadyExists)

	in := mcpInput("docs", tokenTwo, nil, []string{"*"})
	in.Scope = aiendpoint.WorkspaceScope("team")
	_, err = f.svc.Create(ctx, in)
	require.NoError(t, err, "the name is unique in its scope")
	_, err = f.secrets.GetByRef(ctx, "team", "ai/mcp/docs/authorization")
	require.NoError(t, err, "the secret of a workspace entry is in the workspace of the scope")
}

func TestMCPSetCredentialsReplacesTheWholeSet(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)
	in := mcpInput("docs", "", []string{"*"}, []string{"*"})
	in.Credentials.Headers = map[string]string{"X-Api-Key": tokenOne, "X-Team": headerVal}
	created, err := f.svc.Create(ctx, in)
	require.NoError(t, err)

	// A rotation of one header keeps its secret, and writes a new version.
	rotated, err := f.svc.SetCredentials(ctx, aiendpoint.GlobalScope(), "docs", aiendpoint.MCPCredentialsInput{
		Headers: map[string]string{"X-Api-Key": tokenTwo},
	}, "admin")
	require.NoError(t, err)
	require.Len(t, rotated.Headers, 1)
	assert.Equal(t, created.Headers[0].SecretID, rotated.Headers[0].SecretID)
	assert.NotEqual(t, created.Headers[0].Fingerprint, rotated.Headers[0].Fingerprint)
	_, err = f.secrets.GetByRef(ctx, "", "ai/mcp/docs/x-team")
	require.ErrorIs(t, err, secret.ErrNotFound, "the header that the new set lacks loses its secret")

	server, err := f.svc.ResolveMCPServer(ctx, "docs", "d", "")
	require.NoError(t, err)
	creds, err := f.svc.ReadCredentials(ctx, server)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"x-api-key": tokenTwo}, creds.Headers)

	// A token replaces the headers.
	withToken, err := f.svc.SetCredentials(ctx, aiendpoint.GlobalScope(), "docs", aiendpoint.MCPCredentialsInput{Token: tokenOne}, "admin")
	require.NoError(t, err)
	require.NotNil(t, withToken.Token)
	assert.Empty(t, withToken.Headers)
	_, err = f.secrets.GetByRef(ctx, "", "ai/mcp/docs/x-api-key")
	require.ErrorIs(t, err, secret.ErrNotFound)

	// An empty set removes the credentials.
	cleared, err := f.svc.SetCredentials(ctx, aiendpoint.GlobalScope(), "docs", aiendpoint.MCPCredentialsInput{}, "admin")
	require.NoError(t, err)
	assert.Nil(t, cleared.Token)
	assert.Empty(t, cleared.Headers)
	_, err = f.secrets.GetByRef(ctx, "", "ai/mcp/docs/authorization")
	require.ErrorIs(t, err, secret.ErrNotFound)

	_, err = f.svc.SetCredentials(ctx, aiendpoint.GlobalScope(), "nope", aiendpoint.MCPCredentialsInput{Token: tokenOne}, "admin")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
}

func TestMCPUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)
	_, err := f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)

	url := "https://docs2.example.com/mcp"
	allow := aiendpoint.Allow{Workspaces: []string{"team"}, DAGs: []string{"fix-bug"}}
	updated, err := f.svc.Update(ctx, aiendpoint.GlobalScope(), "docs", aiendpoint.MCPUpdateInput{URL: &url, Allow: &allow, Actor: "admin"})
	require.NoError(t, err)
	assert.Equal(t, url, updated.URL)
	assert.Equal(t, allow, updated.Allow)
	require.NotNil(t, updated.Token, "a change keeps the credentials")

	bad := "ftp://x"
	_, err = f.svc.Update(ctx, aiendpoint.GlobalScope(), "docs", aiendpoint.MCPUpdateInput{URL: &bad})
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
	_, err = f.svc.Update(ctx, aiendpoint.GlobalScope(), "nope", aiendpoint.MCPUpdateInput{URL: &url})
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)

	require.NoError(t, f.svc.Delete(ctx, aiendpoint.GlobalScope(), "docs"))
	_, err = f.svc.ResolveMCPServer(ctx, "docs", "fix-bug", "team")
	assert.Equal(t, aiendpoint.ReasonMCPUnknown, aiendpoint.ReasonOf(err))
	_, err = f.secrets.GetByRef(ctx, "", "ai/mcp/docs/authorization")
	require.ErrorIs(t, err, secret.ErrNotFound, "the delete removes the credentials")
	require.ErrorIs(t, f.svc.Delete(ctx, aiendpoint.GlobalScope(), "docs"), aiendpoint.ErrNotFound)
}

func TestMCPResolveAppliesTheScopeAndUseRules(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)

	_, err := f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"team"}, []string{"fix-bug"}))
	require.NoError(t, err)
	ws := mcpInput("docs", tokenTwo, nil, []string{"review"})
	ws.Scope = aiendpoint.WorkspaceScope("team")
	_, err = f.svc.Create(ctx, ws)
	require.NoError(t, err)

	// The workspace entry hides the global entry of the same name.
	server, err := f.svc.ResolveMCPServer(ctx, "docs", "review", "team")
	require.NoError(t, err)
	assert.Equal(t, "team", server.Scope.Workspace)
	_, err = f.svc.ResolveMCPServer(ctx, "docs", "fix-bug", "team")
	require.Error(t, err)
	assert.Equal(t, aiendpoint.ReasonMCPForbidden, aiendpoint.ReasonOf(err), "the hidden global entry does not serve fix-bug")

	// A DAG of another workspace and a DAG with no workspace see the global entry.
	_, err = f.svc.ResolveMCPServer(ctx, "docs", "fix-bug", "other")
	assert.Equal(t, aiendpoint.ReasonMCPForbidden, aiendpoint.ReasonOf(err))
	_, err = f.svc.ResolveMCPServer(ctx, "docs", "fix-bug", "")
	assert.Equal(t, aiendpoint.ReasonMCPForbidden, aiendpoint.ReasonOf(err), "a list of names does not match a DAG with no workspace")

	// The error names the MCP server, and has no credential.
	assert.Contains(t, err.Error(), `MCP server "docs"`)
	assert.NotContains(t, err.Error(), tokenOne)

	_, err = f.svc.ResolveMCPServer(ctx, "other", "fix-bug", "team")
	assert.Equal(t, aiendpoint.ReasonMCPUnknown, aiendpoint.ReasonOf(err))
	_, err = f.svc.ResolveMCPServer(ctx, "Bad_Name", "fix-bug", "team")
	assert.Equal(t, aiendpoint.ReasonMCPUnknown, aiendpoint.ReasonOf(err))
}

func TestMCPReadCredentialsReportsAMissingSecret(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)
	created, err := f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"*"}, []string{"*"}))
	require.NoError(t, err)
	require.NoError(t, f.secrets.Delete(ctx, created.Token.SecretID))

	server, err := f.svc.ResolveMCPServer(ctx, "docs", "d", "")
	require.NoError(t, err)
	_, err = f.svc.ReadCredentials(ctx, server)
	require.Error(t, err)
	assert.Equal(t, aiendpoint.ReasonUnavailable, aiendpoint.ReasonOf(err))
	assert.NotContains(t, err.Error(), tokenOne)
}

func TestMCPChoicesFollowTheUseRule(t *testing.T) {
	ctx := context.Background()
	f := newMCPFixture(t)
	_, err := f.svc.Create(ctx, mcpInput("docs", tokenOne, []string{"*"}, []string{"fix-bug"}))
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, mcpInput("other", tokenOne, []string{"*"}, []string{"review"}))
	require.NoError(t, err)
	ws := mcpInput("wiki", tokenTwo, nil, []string{"fix-bug"})
	ws.Scope = aiendpoint.WorkspaceScope("team")
	_, err = f.svc.Create(ctx, ws)
	require.NoError(t, err)

	names := func(dag, workspace string) []string {
		choices, err := f.svc.Choices(ctx, dag, workspace)
		require.NoError(t, err)
		var out []string
		for _, c := range choices {
			out = append(out, c.Name)
		}
		return out
	}
	assert.Equal(t, []string{"docs"}, names("fix-bug", ""))
	assert.Equal(t, []string{"docs", "wiki"}, names("fix-bug", "team"))
	assert.Empty(t, names("none", "team"))
}
