// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec093_ai_endpoints_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	keyOne = "sk-ant-api03-0123456789abcdefghij"
	keyTwo = "sk-ant-api03-ZYXWVUTSRQPONMLKJIHG"
)

// Spec 093, "The key": the API never shows the key. A response, the output of the
// server and each file of the data directory hold no key. The key is a managed
// secret with the ref ai/endpoint/<name>, and the secret routes show its metadata
// only.
func TestTheKeyIsNeverShown(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)

	status, body := srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyOne, "fix-bug", "review"))
	require.Equal(t, http.StatusCreated, status, body)
	var created endpointJSON
	aitest.Decode(t, body, &created)
	assert.Equal(t, "test-endpoint", created.Name)
	assert.Equal(t, "https://api.anthropic.com", created.BaseURL, "the preset gives the base URL")
	assert.Equal(t, "x-api-key", created.Auth, "the preset gives the authentication form")
	assert.Equal(t, "global", created.Scope)
	assert.Equal(t, []string{"fix-bug", "review"}, created.Allow.DAGs)
	assert.NotEmpty(t, created.KeyFingerprint)
	assert.NotEmpty(t, created.KeyUpdatedAt)

	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	var list struct {
		Endpoints []endpointJSON `json:"endpoints"`
	}
	aitest.Decode(t, body, &list)
	require.Len(t, list.Endpoints, 1)
	assert.Equal(t, created.KeyFingerprint, list.Endpoints[0].KeyFingerprint)

	// Another write path never returns the key either: a rotation, a change.
	status, body = srv.Call(http.MethodPut, "/ai/endpoints/test-endpoint/key", map[string]string{"key": keyTwo})
	require.Equal(t, http.StatusOK, status, body)
	status, body = srv.Call(http.MethodPatch, "/ai/endpoints/test-endpoint", map[string]any{"description": "Main key"})
	require.Equal(t, http.StatusOK, status, body)

	// The key is a managed secret of the global scope. The secret routes show no value.
	status, body = srv.Call(http.MethodGet, "/secrets?workspace=global", nil)
	require.Equal(t, http.StatusOK, status, body)
	var secrets struct {
		Secrets []struct {
			Ref          string `json:"ref"`
			Workspace    string `json:"workspace"`
			ProviderType string `json:"providerType"`
			HasValue     bool   `json:"hasValue"`
		} `json:"secrets"`
	}
	aitest.Decode(t, body, &secrets)
	require.Len(t, secrets.Secrets, 1)
	assert.Equal(t, "ai/endpoint/test-endpoint", secrets.Secrets[0].Ref)
	assert.Equal(t, "dagu-managed", secrets.Secrets[0].ProviderType)
	assert.True(t, secrets.Secrets[0].HasValue)

	srv.AssertNoKey(keyOne, keyTwo)
}

// Spec 093, "The key": the prefix is the first characters of the key, and its
// length is the smaller of 6 and 10 percent of the key length, rounded down. A key
// of fewer than 10 characters has no prefix. The fingerprint is the first 12
// hexadecimal digits of the SHA-256 of the key.
func TestKeyDisplayRule(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	for _, tc := range []struct {
		length int
		prefix int
	}{
		{5, 0}, {9, 0}, {10, 1}, {19, 1}, {20, 2}, {35, 3}, {59, 5}, {60, 6}, {100, 6},
	} {
		name := fmt.Sprintf("k%d", tc.length)
		key := ("sk-ant-Qx" + strings.Repeat("7", 100))[:tc.length]
		status, body := srv.Call(http.MethodPost, "/ai/endpoints", endpointBody(name, key, "*"))
		require.Equal(t, http.StatusCreated, status, "length %d: %s", tc.length, body)
		var ep endpointJSON
		aitest.Decode(t, body, &ep)

		assert.Equal(t, key[:tc.prefix], ep.KeyPrefix, "length %d", tc.length)
		sum := sha256.Sum256([]byte(key))
		assert.Equal(t, hex.EncodeToString(sum[:])[:12], ep.KeyFingerprint, "length %d", tc.length)
		if tc.prefix == 0 {
			assert.NotContains(t, body, "keyPrefix", "length %d: a short key has no prefix", tc.length)
		}
	}
}

// Spec 093, "The key": a rotation replaces the value. The fingerprint changes, the
// prefix follows the new key, and the old value stays in the store as an old
// version that no route reads.
func TestRotationChangesTheFingerprint(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyOne, "*"))
	require.Equal(t, http.StatusCreated, status, body)
	var before endpointJSON
	aitest.Decode(t, body, &before)

	status, body = srv.Call(http.MethodPut, "/ai/endpoints/test-endpoint/key", map[string]string{"key": keyTwo})
	require.Equal(t, http.StatusOK, status, body)
	var after endpointJSON
	aitest.Decode(t, body, &after)

	assert.NotEqual(t, before.KeyFingerprint, after.KeyFingerprint)
	sum := sha256.Sum256([]byte(keyTwo))
	assert.Equal(t, hex.EncodeToString(sum[:])[:12], after.KeyFingerprint)
	assert.Equal(t, keyTwo[:3], after.KeyPrefix)

	// The managed secret has a second version.
	status, body = srv.Call(http.MethodGet, "/secrets?workspace=global", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"currentVersion":2`)

	// An invalid key and an unknown endpoint change nothing.
	status, _ = srv.Call(http.MethodPut, "/ai/endpoints/test-endpoint/key", map[string]string{"key": "a\nb"})
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = srv.Call(http.MethodPut, "/ai/endpoints/nope/key", map[string]string{"key": keyTwo})
	assert.Equal(t, http.StatusNotFound, status)
	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, after.KeyFingerprint)

	srv.AssertNoKey(keyOne, keyTwo)
}

// Spec 093, "Scope and name": the scope is fixed at creation. A name is unique in a
// scope. A workspace endpoint has no allow.workspaces, and a global endpoint needs
// it. A workspace that does not exist is refused.
func TestScopeAndUniqueness(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	status, body := srv.Call(http.MethodPost, "/workspaces", map[string]string{"name": "acme"})
	require.Equal(t, http.StatusCreated, status, body)

	status, body = srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyOne, "*"))
	require.Equal(t, http.StatusCreated, status, body)
	status, _ = srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyTwo, "*"))
	assert.Equal(t, http.StatusConflict, status, "a name is unique in its scope")

	workspaceEndpoint := map[string]any{
		"name": "test-endpoint", "preset": "anthropic", "protocol": "anthropic",
		"scope": "workspace:acme", "allow": map[string]any{"dags": []string{"fix-bug"}}, "key": keyTwo,
	}
	status, body = srv.Call(http.MethodPost, "/ai/endpoints", workspaceEndpoint)
	require.Equal(t, http.StatusCreated, status, "the same name in a workspace is another endpoint: %s", body)
	var ws endpointJSON
	aitest.Decode(t, body, &ws)
	assert.Equal(t, "workspace:acme", ws.Scope)
	assert.Empty(t, ws.Allow.Workspaces)
	status, _ = srv.Call(http.MethodPost, "/ai/endpoints", workspaceEndpoint)
	assert.Equal(t, http.StatusConflict, status)

	// Each endpoint has its own managed secret.
	status, body = srv.Call(http.MethodGet, "/secrets?workspace=acme", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"ref":"ai/endpoint/test-endpoint"`)

	withWorkspaces := map[string]any{
		"name": "test-endpoint-2", "preset": "anthropic", "protocol": "anthropic",
		"scope": "workspace:acme", "allow": map[string]any{"workspaces": []string{"acme"}, "dags": []string{"*"}}, "key": keyTwo,
	}
	status, _ = srv.Call(http.MethodPost, "/ai/endpoints", withWorkspaces)
	assert.Equal(t, http.StatusBadRequest, status, "allow.workspaces is not valid in a workspace scope")

	noWorkspaces := endpointBody("test-endpoint-3", keyTwo, "*")
	noWorkspaces["allow"] = map[string]any{"dags": []string{"*"}}
	status, _ = srv.Call(http.MethodPost, "/ai/endpoints", noWorkspaces)
	assert.Equal(t, http.StatusBadRequest, status, "allow.workspaces is required for the scope global")

	unknown := map[string]any{
		"name": "test-endpoint-4", "preset": "anthropic", "protocol": "anthropic",
		"scope": "workspace:nowhere", "allow": map[string]any{"dags": []string{"*"}}, "key": keyTwo,
	}
	status, _ = srv.Call(http.MethodPost, "/ai/endpoints", unknown)
	assert.Equal(t, http.StatusNotFound, status, "the workspace of the scope must exist")

	// The routes with a name read the scope from the query workspace.
	status, _ = srv.Call(http.MethodPatch, "/ai/endpoints/test-endpoint?workspace=acme", map[string]any{"description": "Workspace key"})
	assert.Equal(t, http.StatusOK, status)
	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, 1, strings.Count(body, "Workspace key"), "the change reached the workspace endpoint only")

	srv.AssertNoKey(keyOne, keyTwo)
}

// Spec 093, "Use rule" and "Scope and name": the route of choices lists the
// endpoints that a DAG may use. A workspace endpoint hides the global endpoint of
// the same name for the DAGs of its workspace. The response has no base URL and
// no key data.
func TestChoicesFollowTheUseRule(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	status, body := srv.Call(http.MethodPost, "/workspaces", map[string]string{"name": "acme"})
	require.Equal(t, http.StatusCreated, status, body)

	for _, in := range []map[string]any{
		endpointBody("listed", keyOne, "fix-bug"),
		endpointBody("unlisted", keyOne, "review"),
		endpointBody("open", keyOne, "*"),
		endpointBody("hidden", keyOne, "*"),
		{"name": "hidden", "preset": "anthropic", "protocol": "anthropic", "scope": "workspace:acme", "allow": map[string]any{"dags": []string{"*"}}, "key": keyTwo},
		{"name": "only-acme", "preset": "anthropic", "protocol": "anthropic", "scope": "workspace:acme", "allow": map[string]any{"dags": []string{"fix-bug"}}, "key": keyTwo},
	} {
		status, body = srv.Call(http.MethodPost, "/ai/endpoints", in)
		require.Equal(t, http.StatusCreated, status, body)
	}
	global := endpointBody("acme-only", keyOne, "*")
	global["allow"] = map[string]any{"workspaces": []string{"acme"}, "dags": []string{"*"}}
	status, body = srv.Call(http.MethodPost, "/ai/endpoints", global)
	require.Equal(t, http.StatusCreated, status, body)

	choices := func(query string) []string {
		status, body := srv.Call(http.MethodGet, "/ai/endpoint-choices?"+query, nil)
		require.Equal(t, http.StatusOK, status, body)
		assert.NotContains(t, body, "baseUrl")
		assert.NotContains(t, body, "keyFingerprint")
		assert.NotContains(t, body, "keyPrefix")
		var out struct {
			Choices []struct {
				Name     string `json:"name"`
				Protocol string `json:"protocol"`
				Preset   string `json:"preset"`
				Scope    string `json:"scope"`
			} `json:"choices"`
		}
		aitest.Decode(t, body, &out)
		var names []string
		for _, c := range out.Choices {
			assert.Equal(t, "anthropic", c.Protocol)
			assert.Equal(t, "anthropic", c.Preset)
			names = append(names, c.Name+"@"+c.Scope)
		}
		return names
	}

	// A DAG with no workspace sees the global endpoints that list it.
	assert.Equal(t, []string{"hidden@global", "listed@global", "open@global"}, choices("dag=fix-bug"))
	assert.Equal(t, []string{"hidden@global", "open@global", "unlisted@global"}, choices("dag=review"))
	// A DAG of the workspace sees its own endpoints and the global endpoints that allow its workspace.
	assert.Equal(t, []string{"acme-only@global", "hidden@workspace:acme", "listed@global", "only-acme@workspace:acme", "open@global"}, choices("dag=fix-bug&workspace=acme"))
	assert.Equal(t, []string{"acme-only@global", "hidden@workspace:acme", "open@global", "unlisted@global"}, choices("dag=review&workspace=acme"))

	status, _ = srv.Call(http.MethodGet, "/ai/endpoint-choices", nil)
	assert.Equal(t, http.StatusBadRequest, status, "the query dag is required")
}

// Spec 093, "Routes and audit": a delete removes the endpoint and its key. The name
// is free again, and a second delete finds nothing.
func TestDeleteRemovesTheEndpointAndItsKey(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyOne, "*"))
	require.Equal(t, http.StatusCreated, status, body)

	status, body = srv.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
	require.Equal(t, http.StatusNoContent, status, body)
	status, _ = srv.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
	assert.Equal(t, http.StatusNotFound, status)

	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"endpoints":[]`)
	status, body = srv.Call(http.MethodGet, "/secrets?workspace=global", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "ai/endpoint/test-endpoint", "the delete removed the managed secret")

	status, body = srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyTwo, "*"))
	assert.Equal(t, http.StatusCreated, status, body)
	srv.AssertNoKey(keyOne, keyTwo)
}

// Spec 093, "The endpoint": an invalid field is a 400 error that names the field.
// The message never holds the key.
func TestInvalidRequests(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	for name, mutate := range map[string]func(map[string]any){
		"name":             func(b map[string]any) { b["name"] = "Cle_1" },
		"preset":           func(b map[string]any) { b["preset"] = "nowhere" },
		"protocol of row":  func(b map[string]any) { b["preset"] = "openai" },
		"http base url":    func(b map[string]any) { b["baseUrl"] = "http://api.example.com" },
		"user in base url": func(b map[string]any) { b["baseUrl"] = "https://user:pass@api.example.com" },
		"query in url":     func(b map[string]any) { b["baseUrl"] = "https://api.example.com/?a=b" },
		"auth":             func(b map[string]any) { b["auth"] = "basic" },
		"models url":       func(b map[string]any) { b["modelsUrl"] = "https://api.example.com/models" },
		"no key":           func(b map[string]any) { delete(b, "key") },
		"key with newline": func(b map[string]any) { b["key"] = keyOne + "\n" },
		"key too long":     func(b map[string]any) { b["key"] = strings.Repeat("k", 8193) },
		"scope":            func(b map[string]any) { b["scope"] = "acme" },
		"wildcard mixed": func(b map[string]any) {
			b["allow"] = map[string]any{"workspaces": []string{"*", "a"}, "dags": []string{"*"}}
		},
		"no dags": func(b map[string]any) { b["allow"] = map[string]any{"workspaces": []string{"*"}} },
	} {
		body := endpointBody("test-endpoint", keyOne, "*")
		mutate(body)
		status, response := srv.Call(http.MethodPost, "/ai/endpoints", body)
		assert.Equal(t, http.StatusBadRequest, status, "%s: %s", name, response)
		assert.Contains(t, response, `"bad_request"`, name)
	}

	// A local model accepts http and no key.
	local := map[string]any{
		"name": "local", "preset": "ollama", "protocol": "openai", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": []string{"*"}},
	}
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", local)
	require.Equal(t, http.StatusCreated, status, body)
	var ep endpointJSON
	aitest.Decode(t, body, &ep)
	assert.Equal(t, "http://localhost:11434/v1", ep.BaseURL)
	assert.Empty(t, ep.KeyFingerprint)

	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, 1, strings.Count(body, `"name":`), "no invalid request created an endpoint")
	srv.AssertNoKey(keyOne)
}

// Spec 093, "Routes and audit": the routes need the role admin. A request with no
// token is refused with 401, and the admin can use each route.
func TestRoutesNeedAuthentication(t *testing.T) {
	t.Parallel()

	env := []string{
		"DAGU_AUTH_MODE=builtin",
		"DAGU_AUTH_BUILTIN_TOKEN_SECRET=conformance-jwt-secret",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=admin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=conformance-admin-password",
	}
	srv := aitest.StartServer(t, harness.NewRunner(t), env)

	for _, route := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/ai/endpoints", nil},
		{http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyOne, "*")},
		{http.MethodPatch, "/ai/endpoints/test-endpoint", map[string]any{"description": "x"}},
		{http.MethodPut, "/ai/endpoints/test-endpoint/key", map[string]string{"key": keyTwo}},
		{http.MethodDelete, "/ai/endpoints/test-endpoint", nil},
		{http.MethodGet, "/ai/endpoint-choices?dag=fix-bug", nil},
	} {
		status, body := srv.Call(route.method, route.path, route.body)
		assert.Equal(t, http.StatusUnauthorized, status, "%s %s: %s", route.method, route.path, body)
	}
	status, body := srv.CallWithToken("not-a-token", http.MethodGet, "/ai/endpoints", nil)
	assert.Equal(t, http.StatusUnauthorized, status, body)

	srv.Login("admin", "conformance-admin-password")
	status, body = srv.Call(http.MethodPost, "/ai/endpoints", endpointBody("test-endpoint", keyOne, "*"))
	require.Equal(t, http.StatusCreated, status, body)
	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"test-endpoint"`)
	srv.AssertNoKey(keyOne)
}

// endpointBody returns the body of the create request of a global endpoint of the
// preset anthropic.
func endpointBody(name, key string, dags ...string) map[string]any {
	return map[string]any{
		"name":     name,
		"preset":   "anthropic",
		"protocol": "anthropic",
		"scope":    "global",
		"allow":    map[string]any{"workspaces": []string{"*"}, "dags": dags},
		"key":      key,
	}
}

// endpointJSON is the part of the response of an endpoint that the tests read.
type endpointJSON struct {
	Name           string `json:"name"`
	Preset         string `json:"preset"`
	Protocol       string `json:"protocol"`
	BaseURL        string `json:"baseUrl"`
	Auth           string `json:"auth"`
	Scope          string `json:"scope"`
	KeyPrefix      string `json:"keyPrefix"`
	KeyFingerprint string `json:"keyFingerprint"`
	KeyUpdatedAt   string `json:"keyUpdatedAt"`
	Allow          struct {
		Workspaces []string `json:"workspaces"`
		DAGs       []string `json:"dags"`
	} `json:"allow"`
}
