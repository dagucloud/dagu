// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec095_mcp_servers_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests of Spec 095, "MCP servers with credentials": the MCP tab (the
// routes), the named form of tools.mcp, the MCP relay and the check of the key allow.

const (
	mcpTokenTwo = "mcp-token-SECOND-9876543210zyxwvutsrqponm"
	mcpHeaderA  = "header-value-A-0123456789abcdefghij"
)

// mcpBody returns the create request of a global MCP server entry with a token.
func mcpBody(name, url, token string, dags ...string) map[string]any {
	body := map[string]any{
		"name": name, "url": url, "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": dags},
	}
	if token != "" {
		body["token"] = token
	}
	return body
}

// mcpJSON is the part of the response of a MCP server entry that the tests read.
type mcpJSON struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Scope string `json:"scope"`
	Token *struct {
		KeyPrefix      string `json:"keyPrefix"`
		KeyFingerprint string `json:"keyFingerprint"`
		KeyUpdatedAt   string `json:"keyUpdatedAt"`
	} `json:"token"`
	Headers []struct {
		Name           string `json:"name"`
		KeyPrefix      string `json:"keyPrefix"`
		KeyFingerprint string `json:"keyFingerprint"`
	} `json:"headers"`
}

// Spec 095, "The MCP tab": the API never shows a token or a header value. It shows
// the prefix and the fingerprint with the display rule of the key. A response, the
// output of the server and each file of the data directory hold no value. Each
// value is a managed secret with the ref ai/mcp/<name>/<header>.
func TestMCPCredentialsAreNeverShown(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)

	status, body := srv.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", "https://docs.example.com/mcp", mcpToken, "fix-bug"))
	require.Equal(t, http.StatusCreated, status, body)
	var created mcpJSON
	aitest.Decode(t, body, &created)
	require.NotNil(t, created.Token)
	// The prefix is the smaller of 6 and 10 percent of the length, rounded down.
	assert.Equal(t, mcpToken[:min(6, len(mcpToken)/10)], created.Token.KeyPrefix)
	assert.Equal(t, aitest.Fingerprint(mcpToken), created.Token.KeyFingerprint)
	assert.NotEmpty(t, created.Token.KeyUpdatedAt)

	headers := map[string]any{
		"name": "wiki", "url": "https://wiki.example.com/mcp", "scope": "global",
		"allow":   map[string]any{"workspaces": []string{"*"}, "dags": []string{"*"}},
		"headers": map[string]string{"X-Api-Key": mcpHeaderA, "X-Team": "short"},
	}
	status, body = srv.Call(http.MethodPost, "/ai/mcp-servers", headers)
	require.Equal(t, http.StatusCreated, status, body)
	var wiki mcpJSON
	aitest.Decode(t, body, &wiki)
	require.Len(t, wiki.Headers, 2)
	assert.Equal(t, "x-api-key", wiki.Headers[0].Name, "the names are in lower case, sorted")
	assert.Equal(t, aitest.Fingerprint(mcpHeaderA), wiki.Headers[0].KeyFingerprint)
	assert.Equal(t, "x-team", wiki.Headers[1].Name)
	assert.Empty(t, wiki.Headers[1].KeyPrefix, "a value of fewer than 10 characters has no prefix")

	status, body = srv.Call(http.MethodGet, "/ai/mcp-servers", nil)
	require.Equal(t, http.StatusOK, status, body)
	var list struct {
		Servers []mcpJSON `json:"servers"`
	}
	aitest.Decode(t, body, &list)
	require.Len(t, list.Servers, 2)

	// A rotation replaces the whole set. The fingerprint changes.
	status, body = srv.Call(http.MethodPut, "/ai/mcp-servers/docs/credentials", map[string]string{"token": mcpTokenTwo})
	require.Equal(t, http.StatusOK, status, body)
	var rotated mcpJSON
	aitest.Decode(t, body, &rotated)
	require.NotNil(t, rotated.Token)
	assert.Equal(t, aitest.Fingerprint(mcpTokenTwo), rotated.Token.KeyFingerprint)
	assert.NotEqual(t, created.Token.KeyFingerprint, rotated.Token.KeyFingerprint)
	status, body = srv.Call(http.MethodPatch, "/ai/mcp-servers/docs", map[string]any{"url": "https://docs2.example.com/mcp"})
	require.Equal(t, http.StatusOK, status, body)

	// Each value is a managed secret of the global scope, and the secret routes show no value.
	status, body = srv.Call(http.MethodGet, "/secrets?workspace=global", nil)
	require.Equal(t, http.StatusOK, status, body)
	var secrets struct {
		Secrets []struct {
			Ref          string `json:"ref"`
			ProviderType string `json:"providerType"`
			HasValue     bool   `json:"hasValue"`
		} `json:"secrets"`
	}
	aitest.Decode(t, body, &secrets)
	var refs []string
	for _, secret := range secrets.Secrets {
		refs = append(refs, secret.Ref)
		assert.Equal(t, "dagu-managed", secret.ProviderType)
		assert.True(t, secret.HasValue)
	}
	assert.ElementsMatch(t, []string{"ai/mcp/docs/authorization", "ai/mcp/wiki/x-api-key", "ai/mcp/wiki/x-team"}, refs)

	srv.AssertNoKey(mcpToken, mcpTokenTwo, mcpHeaderA)

	// A delete removes the entry and its secrets. The name is free again.
	status, body = srv.Call(http.MethodDelete, "/ai/mcp-servers/wiki", nil)
	require.Equal(t, http.StatusNoContent, status, body)
	status, body = srv.Call(http.MethodGet, "/secrets?workspace=global", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "ai/mcp/wiki/")
	status, _ = srv.Call(http.MethodDelete, "/ai/mcp-servers/wiki", nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, body = srv.Call(http.MethodPost, "/ai/mcp-servers", headers)
	assert.Equal(t, http.StatusCreated, status, body)
}

// Spec 095, "The MCP tab": the scope is fixed at creation, a name is unique in its
// scope, and the entry has rules for its fields.
func TestMCPScopeAndInvalidRequests(t *testing.T) {
	t.Parallel()

	srv := aitest.StartServer(t, harness.NewRunner(t), nil)
	status, body := srv.Call(http.MethodPost, "/workspaces", map[string]string{"name": "acme"})
	require.Equal(t, http.StatusCreated, status, body)

	status, body = srv.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", "https://docs.example.com/mcp", mcpToken, "*"))
	require.Equal(t, http.StatusCreated, status, body)
	status, _ = srv.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", "https://docs.example.com/mcp", mcpToken, "*"))
	assert.Equal(t, http.StatusConflict, status)
	workspace := map[string]any{
		"name": "docs", "url": "https://docs.example.com/mcp", "scope": "workspace:acme", "token": mcpToken,
		"allow": map[string]any{"dags": []string{"*"}},
	}
	status, body = srv.Call(http.MethodPost, "/ai/mcp-servers", workspace)
	require.Equal(t, http.StatusCreated, status, body)
	workspace["scope"] = "workspace:nope"
	status, _ = srv.Call(http.MethodPost, "/ai/mcp-servers", workspace)
	assert.Equal(t, http.StatusNotFound, status)

	// The routes that name an entry read the scope from the query workspace.
	status, body = srv.Call(http.MethodPatch, "/ai/mcp-servers/docs?workspace=acme", map[string]any{"url": "https://acme.example.com/mcp"})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"workspace:acme"`)
	status, body = srv.Call(http.MethodGet, "/ai/mcp-servers", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "https://docs.example.com/mcp")

	// Choices: a DAG sees the entries that it may use.
	status, body = srv.Call(http.MethodGet, "/ai/mcp-server-choices?dag=fix-bug&workspace=acme", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"workspace:acme"`)
	assert.NotContains(t, body, "example.com", "the choices have no URL")
	assert.NotContains(t, body, "keyFingerprint")

	for name, mutate := range map[string]func(map[string]any){
		"name dagu":         func(b map[string]any) { b["name"] = "dagu" },
		"bad name":          func(b map[string]any) { b["name"] = "Docs_1" },
		"ftp url":           func(b map[string]any) { b["url"] = "ftp://x.example.com" },
		"url with user":     func(b map[string]any) { b["url"] = "https://u:p@x.example.com/mcp" },
		"url with fragment": func(b map[string]any) { b["url"] = "https://x.example.com/mcp#x" },
		"token and header":  func(b map[string]any) { b["headers"] = map[string]string{"Authorization": "x"} },
		"reserved header":   func(b map[string]any) { b["token"] = nil; b["headers"] = map[string]string{"Content-Type": "x"} },
		"empty allow":       func(b map[string]any) { b["allow"] = map[string]any{"workspaces": []string{"*"}, "dags": []string{}} },
	} {
		in := mcpBody("fresh", "https://x.example.com/mcp", mcpToken, "*")
		mutate(in)
		status, body := srv.Call(http.MethodPost, "/ai/mcp-servers", in)
		assert.Equal(t, http.StatusBadRequest, status, "%s: %s", name, body)
		assert.NotContains(t, body, mcpToken, name)
	}
	srv.AssertNoKey(mcpToken)
}

// Spec 095, "The MCP tab": the routes need the role admin. A request with no token
// is refused with 401.
func TestMCPRoutesNeedAuthentication(t *testing.T) {
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
		{http.MethodGet, "/ai/mcp-servers", nil},
		{http.MethodPost, "/ai/mcp-servers", mcpBody("docs", "https://docs.example.com/mcp", mcpToken, "*")},
		{http.MethodPatch, "/ai/mcp-servers/docs", map[string]any{"url": "https://x.example.com"}},
		{http.MethodPut, "/ai/mcp-servers/docs/credentials", map[string]string{"token": mcpTokenTwo}},
		{http.MethodDelete, "/ai/mcp-servers/docs", nil},
		{http.MethodGet, "/ai/mcp-server-choices?dag=fix-bug", nil},
	} {
		status, body := srv.Call(route.method, route.path, route.body)
		assert.Equal(t, http.StatusUnauthorized, status, "%s %s: %s", route.method, route.path, body)
	}
	status, body := srv.CallWithToken("not-a-token", http.MethodGet, "/ai/mcp-servers", nil)
	assert.Equal(t, http.StatusUnauthorized, status, body)

	srv.Login("admin", "conformance-admin-password")
	status, body = srv.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", "https://docs.example.com/mcp", mcpToken, "*"))
	require.Equal(t, http.StatusCreated, status, body)
	srv.AssertNoKey(mcpToken)
}

// createMCPServer creates the global entry docs with the token mcpToken. The URL is
// the path /docs of the fake MCP server.
func createMCPServer(t *testing.T, st *aitest.Stack, url string, dags ...string) {
	t.Helper()
	status, body := st.Server.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", url, mcpToken, dags...))
	require.Equal(t, http.StatusCreated, status, body)
}

// mcpResult is one answer that the fake CLI recorded.
type mcpResult struct {
	Status  int    `json:"status"`
	Body    string `json:"body"`
	Session string `json:"session"`
	Error   string `json:"error"`
}

func readMCPResults(t *testing.T, text string) map[string]mcpResult {
	t.Helper()
	var results map[string]mcpResult
	require.NoError(t, json.Unmarshal([]byte(text), &results))
	return results
}

// expectMCPFindings checks what the fake CLI saw and what the fake MCP server saw. It
// holds for a run on an agent worker and for a local run.
func expectMCPFindings(t *testing.T, cliJSON, configJSON, envJSON, argvJSON string, mcp *fakeMCP) (relaySecret string) {
	t.Helper()
	results := readMCPResults(t, cliJSON)

	// The relay forwards the allowed requests, and the answer is the answer of the server.
	assert.Equal(t, 200, results["initialize"].Status)
	assert.Contains(t, results["initialize"].Body, "fake-mcp")
	assert.Equal(t, 200, results["list"].Status)
	assert.Contains(t, results["list"].Body, "drop_tables", "the coordinator does not change tools/list")
	assert.Equal(t, 200, results["allowed"].Status)
	assert.Contains(t, results["allowed"].Body, "search result for dagu")
	assert.Equal(t, 204, results["delete"].Status)
	assert.Contains(t, results["batch_ok"].Body, "search result for dagu")

	// A tool that is not in allow gets a JSON-RPC error and no call to the server.
	assert.Equal(t, 200, results["refused"].Status)
	assert.Contains(t, results["refused"].Body, "mcp_tool_not_allowed")
	assert.Contains(t, results["refused"].Body, `"id":4`)
	assert.Equal(t, 200, results["batch_refused"].Status)
	assert.Contains(t, results["batch_refused"].Body, "mcp_tool_not_allowed")
	assert.Contains(t, results["batch_refused"].Body, `"id":5`, "each request of the batch gets an error")
	assert.Contains(t, results["batch_refused"].Body, `"id":6`)

	// A body that is not JSON-RPC is refused.
	assert.Equal(t, 400, results["invalid_json"].Status)
	assert.Contains(t, results["invalid_json"].Body, "mcp_request_invalid")
	assert.Equal(t, 400, results["invalid_rpc"].Status)

	// A body that has a key twice, or a key that differs only by case, is refused: the
	// relay and the server could read two messages. The server never gets the call.
	for _, name := range []string{"ambiguous_method_case", "ambiguous_name_case", "ambiguous_duplicate", "ambiguous_batch"} {
		assert.Equal(t, 400, results[name].Status, name)
		assert.Contains(t, results[name].Body, "mcp_request_invalid", name)
	}

	// The relay refuses a path, a query, a method and a server that the step does not name.
	for name, want := range map[string]int{
		"other_path": 404, "query": 404, "put": 405,
		"other_server": 403, "no_secret": 401, "wrong_secret": 401, "two_secrets": 401, "host_header": 403,
	} {
		assert.Equal(t, want, results[name].Status, name)
	}
	assert.Contains(t, results["other_server"].Body, "mcp_server_forbidden")
	assert.Contains(t, results["other_path"].Body, "path_not_allowed")
	// The agent step names an endpoint (Spec 091), so its relay serves the model path of
	// the endpoint. The fake model API of the endpoint is a closed port: the relay
	// answers upstream_error, and not path_not_allowed.
	assert.Equal(t, 502, results["model_path"].Status, "model_path")

	// The inline form goes straight to its server, with its own headers and no relay.
	assert.Equal(t, 200, results["inline"].Status)
	assert.Equal(t, 200, results["x_headers"].Status)

	// The config of the CLI: the named server is the relay, with the secret of the relay.
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(configJSON), &config))
	docs := config.MCPServers["docs"]
	assert.Regexp(t, `^http://127\.0\.0\.1:\d+/mcp/docs$`, docs.URL, "the CLI connects to the relay")
	relaySecret = strings.TrimPrefix(docs.Headers["Authorization"], "Bearer ")
	assert.GreaterOrEqual(t, len(relaySecret), 43)
	assert.NotContains(t, configJSON, mcpToken, "the config of the CLI has no credential")
	assert.Equal(t, mcp.URL()+"/inline", config.MCPServers["inline"].URL, "the inline server is not on the relay")
	assert.Equal(t, map[string]string{"X-Team": "t1"}, config.MCPServers["inline"].Headers)

	// The CLI never sees the token.
	assert.NotContains(t, envJSON+argvJSON, mcpToken)

	// What the fake MCP server saw: the token on each call of the named server, and no
	// call of the refused tool, no header of the client, no secret of the relay.
	docsRequests := mcp.onPath("/docs")
	require.NotEmpty(t, docsRequests)
	for _, request := range docsRequests {
		assert.Equal(t, "Bearer "+mcpToken, request.Header.Get("Authorization"), "the coordinator adds the token")
		assert.NotContains(t, strings.Join(request.Header.Values("Authorization"), ","), relaySecret)
		assert.Empty(t, request.Header.Get("X-Evil"))
		assert.Empty(t, request.Header.Get("Cookie"))
		assert.Empty(t, request.Header.Get("X-Api-Key"))
		assert.Equal(t, "identity", request.Header.Get("Accept-Encoding"))
	}
	assert.NotContains(t, toolsCalled(docsRequests), "drop_tables", "a tool outside allow never reaches the server")
	for _, request := range docsRequests {
		assert.NotContains(t, request.Body, "drop_tables", "no body with the forbidden tool, in any form, reaches the server")
		assert.NotContains(t, request.Body, `"METHOD"`)
		assert.NotContains(t, request.Body, `"NAME"`)
	}
	assert.Equal(t, []string{"search", "search", "search", "search"}, toolsCalled(docsRequests))
	var methods []string
	for _, request := range docsRequests {
		methods = append(methods, request.Method)
	}
	assert.Contains(t, methods, http.MethodDelete)
	assert.Empty(t, mcp.onPath("/wiki"), "the step does not name the server wiki")
	assert.Empty(t, mcp.onPath("/docs/extra"))

	inline := mcp.onPath("/inline")
	require.Len(t, inline, 1)
	assert.Equal(t, "t1", inline[0].Header.Get("X-Team"))
	assert.Empty(t, inline[0].Header.Get("Authorization"), "the inline server gets no token")
	return relaySecret
}

// Spec 095, "The MCP relay", end to end: a coordinator with mutual TLS, an agent worker
// and a fake MCP server. The fake CLI reads the MCP config of its attempt, as the real
// CLI does, and calls the servers of the config. The test needs the user ID 0, like
// the other tests with an agent worker.
func TestMCPRelayOnAnAgentWorker(t *testing.T) {
	st := aitest.StartStack(t, "relay_coordinator_config.yaml")
	mcp := startFakeMCP(t, "sse")
	createMCPServer(t, st, mcp.URL()+"/docs", "mcp-run")
	// The agent step names an endpoint (Spec 091). The fake CLI never calls its model API.
	st.CreateEndpoint("http://127.0.0.1:9", aitest.RealKey, "*")

	runIt := func(t *testing.T, dag, id string, params ...string) aitest.RunDetails {
		t.Helper()
		worker := st.StartAgentWorker("agent-" + id)
		st.EnqueueWith(dag+".yaml", id, params...)
		details := st.WaitRun(dag, id)
		st.ExpectWorkerExit(worker)
		return details
	}

	t.Run("NamedServerGetsItsCredentialsFromTheCoordinator", func(t *testing.T) {
		st.Scenario("mcp", nil)
		details := runIt(t, "mcp-run", "calls", "MCP_INLINE_URL="+mcp.URL()+"/inline")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())

		secret := expectMCPFindings(t, st.Read("mcp.json"), st.Read("mcp_config.json"),
			st.Read("cli_env.json"), st.Read("cli_argv.json"), mcp)

		// The token is in no log, no output, no file of any home and no record of the
		// worker. The secret of the relay is in no log either.
		st.AssertKeyNowhere(mcpToken, secret)

		// The audit line has the target, the route, the method and the tool, and no token.
		out := st.Services.Output()
		assert.Contains(t, out, "action=ai_relay_request")
		assert.Contains(t, out, "target_kind=mcp")
		assert.Contains(t, out, "target=docs")
		assert.Contains(t, out, "route=mcp")
		assert.Contains(t, out, "rpc_method=tools/call")
		assert.Contains(t, out, "tool=search")
		assert.Contains(t, out, "tool=drop_tables", "a refused call has its line too")
		assert.Contains(t, out, "reason=mcp_tool_not_allowed")
		assert.Contains(t, out, "key_fingerprint="+aitest.Fingerprint(mcpToken))
		assert.Contains(t, out, "dag=mcp-run")
	})

	t.Run("ForbiddenDAGFailsBeforeTheCLIStarts", func(t *testing.T) {
		st.Scenario("ok", nil)
		before, ran := len(mcp.all()), st.RanCount()
		details := runIt(t, "mcp-forbidden", "forbidden")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `MCP server "docs": mcp_server_forbidden`)
		assert.Contains(t, details.ErrorText(), `the DAG "mcp-forbidden" is not in the allow list`)
		assert.Equal(t, ran, st.RanCount(), "the CLI did not start")
		assert.Len(t, mcp.all(), before, "no request went to the MCP server")
	})

	t.Run("UnknownServerFailsBeforeTheCLIStarts", func(t *testing.T) {
		st.Scenario("ok", nil)
		ran := st.RanCount()
		details := runIt(t, "mcp-unknown", "unknown")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `MCP server "ghost": mcp_server_unknown`)
		assert.Equal(t, ran, st.RanCount(), "the CLI did not start")
	})

	// The first event reaches the CLI before the server ends its stream.
	t.Run("StreamsChunkByChunk", func(t *testing.T) {
		st.Scenario("mcp_stream", nil)
		worker := st.StartAgentWorker("agent-stream")
		st.EnqueueWith("mcp-run.yaml", "stream", "MCP_INLINE_URL="+mcp.URL()+"/inline")
		st.WaitFile("first_chunk")
		firstAt := aitest.UnixNanoFile(t, filepath.Join(st.Dir, "first_chunk"))
		releasedAt := time.Now()
		details, ok := st.Details("mcp-run", "stream")
		require.True(t, ok)
		assert.False(t, details.Finished(), "the run is still open while the server streams")
		assert.True(t, firstAt.Before(releasedAt), "the CLI had its first event before the server ended")
		mcp.releaseNow()

		final := st.WaitRun("mcp-run", "stream")
		st.ExpectWorkerExit(worker)
		require.Equal(t, "succeeded", final.DAGRunDetails.StatusLabel, final.ErrorText())
		var stream struct {
			Status int `json:"status"`
			Chunks int `json:"chunks"`
		}
		require.NoError(t, json.Unmarshal([]byte(st.Read("mcp_stream.json")), &stream))
		assert.Equal(t, 200, stream.Status)
		assert.GreaterOrEqual(t, stream.Chunks, 2, "the events arrive as separate chunks")
	})

	// The stop of the run cancels the call to the MCP server.
	t.Run("StopCancelsTheServerCall", func(t *testing.T) {
		mcp.mu.Lock()
		mcp.mode = "block"
		mcp.mu.Unlock()
		st.Scenario("mcp_block", nil)
		worker := st.StartAgentWorker("agent-block")
		st.EnqueueWith("mcp-run.yaml", "block", "MCP_INLINE_URL="+mcp.URL()+"/inline")
		st.WaitFile("first_chunk")
		status, body := st.Server.Call(http.MethodPost, "/dag-runs/mcp-run/block/stop", nil)
		require.Equal(t, http.StatusOK, status, body)
		select {
		case <-mcp.cancelled:
		case <-time.After(60 * time.Second):
			t.Fatalf("the call to the MCP server stayed open after the stop\n%s", st.Services.Output())
		}
		st.WaitRun("mcp-run", "block")
		select {
		case <-worker.Done():
		case <-time.After(harness.WaitTimeout(t)):
			t.Fatalf("the agent worker did not exit\n%s", worker.Output())
		}
	})

	t.Run("RotationAppliesToTheNextRun", func(t *testing.T) {
		mcp.mu.Lock()
		mcp.mode = "sse"
		mcp.mu.Unlock()
		status, body := st.Server.Call(http.MethodPut, "/ai/mcp-servers/docs/credentials", map[string]string{"token": mcpTokenTwo})
		require.Equal(t, http.StatusOK, status, body)
		st.Scenario("mcp", nil)
		before := len(mcp.onPath("/docs"))
		details := runIt(t, "mcp-run", "rotated", "MCP_INLINE_URL="+mcp.URL()+"/inline")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		for _, request := range mcp.onPath("/docs")[before:] {
			assert.Equal(t, "Bearer "+mcpTokenTwo, request.Header.Get("Authorization"), "the next run uses the new token")
		}
		st.AssertKeyNowhere(mcpTokenTwo)
	})

	t.Run("DeletedServerFailsTheNextRun", func(t *testing.T) {
		status, body := st.Server.Call(http.MethodDelete, "/ai/mcp-servers/docs", nil)
		require.Equal(t, http.StatusNoContent, status, body)
		st.Scenario("ok", nil)
		ran := st.RanCount()
		details := runIt(t, "mcp-run", "deleted", "MCP_INLINE_URL="+mcp.URL()+"/inline")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `MCP server "docs": mcp_server_unknown`)
		assert.Equal(t, ran, st.RanCount(), "the CLI did not start")
	})
}

// Spec 094, "Local mode": a run with no coordinator and no worker calls the gateway
// in its own process. The process reads the MCP server entries of its data directory.
func TestMCPRelayLocalMode(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(t)
	mcp := startFakeMCP(t, "sse")
	srv := aitest.StartServer(t, runner, nil)
	dagu := aitest.NewLocalProject(t, runner, srv.Home)
	status, body := srv.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", mcp.URL()+"/docs", mcpToken, "mcp-local"))
	require.Equal(t, http.StatusCreated, status, body)
	// The agent step names an endpoint (Spec 091). The fake CLI never calls its model API.
	status, body = srv.Call(http.MethodPost, "/ai/endpoints", map[string]any{
		"name": "test-endpoint", "preset": "custom", "protocol": "anthropic", "baseUrl": "http://127.0.0.1:9",
		"auth": "x-api-key", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": []string{"*"}}, "key": aitest.RealKey,
	})
	require.Equal(t, http.StatusCreated, status, body)

	t.Run("RunsThroughTheRelay", func(t *testing.T) {
		dagu.Scenario(t, "mcp")
		result := dagu.Runner.RunWithEnv(dagu.Env, "start", "mcp-local.yaml", "--", "MCP_INLINE_URL="+mcp.URL()+"/inline")
		result.ExpectExitCode(0)
		secret := expectMCPFindings(t, dagu.Read(t, "mcp.json"), dagu.Read(t, "mcp_config.json"),
			dagu.Read(t, "cli_env.json"), dagu.Read(t, "cli_argv.json"), mcp)
		srv.AssertNoKey(mcpToken)
		assert.NotContains(t, result.Stdout()+result.Stderr(), mcpToken)
		assert.NotContains(t, result.Stdout()+result.Stderr(), secret, "the relay secret is masked")
	})

	t.Run("ForbiddenDAG", func(t *testing.T) {
		dagu.Scenario(t, "ok")
		before := dagu.RanCount()
		result := dagu.Start("mcp-forbidden.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`MCP server "docs": mcp_server_forbidden`, `the DAG "mcp-forbidden" is not in the allow list`)
		assert.Equal(t, before, dagu.RanCount(), "the CLI did not start")
	})

	t.Run("UnknownServer", func(t *testing.T) {
		dagu.Scenario(t, "ok")
		before := dagu.RanCount()
		result := dagu.Start("mcp-unknown.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`MCP server "ghost": mcp_server_unknown`)
		assert.Equal(t, before, dagu.RanCount(), "the CLI did not start")
	})
}

// Spec 095, "The key allow": a chat step with a named MCP entry reads the list of the
// server through the relay, offers the tools of allow only, and calls a tool through
// the relay. The coordinator adds the token. The test runs in the local mode.
func TestChatNamedMCPLocalMode(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(t)
	mcp := startFakeMCP(t, "sse")
	model := startMCPChatModel(t)
	srv := aitest.StartServer(t, runner, nil)
	dagu := aitest.NewLocalProject(t, runner, srv.Home)
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", map[string]any{
		"name": "test-endpoint", "preset": "custom", "protocol": "anthropic", "baseUrl": model.URL(),
		"auth": "x-api-key", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": []string{"mcp-chat"}}, "key": aitest.RealKey,
	})
	require.Equal(t, http.StatusCreated, status, body)
	status, body = srv.Call(http.MethodPost, "/ai/mcp-servers", mcpBody("docs", mcp.URL()+"/docs", mcpToken, "mcp-chat"))
	require.Equal(t, http.StatusCreated, status, body)

	t.Run("CallsTheToolThroughTheRelay", func(t *testing.T) {
		result := runner.RunWithEnv(dagu.Env, "start", "mcp-chat.yaml")
		result.ExpectExitCode(0)
		assert.Equal(t, "The docs say: found.\n", aitest.ReadResult(t, runner))

		// The model got the tools of allow only, and the result of the call.
		requests := model.records()
		require.Len(t, requests, 2)
		assert.Equal(t, []string{"docs_search"}, requests[0].tools, "the model gets the tools of allow only")
		assert.Contains(t, requests[1].body, "search result for dagu")
		assert.NotContains(t, requests[0].body+requests[1].body, mcpToken, "the token never goes to the model")

		// The server got the token on each request, and the call of the allowed tool only.
		docs := mcp.onPath("/docs")
		require.NotEmpty(t, docs)
		for _, request := range docs {
			assert.Equal(t, "Bearer "+mcpToken, request.Header.Get("Authorization"))
		}
		assert.Equal(t, []string{"search"}, toolsCalled(docs))
		srv.AssertNoKey(mcpToken, aitest.RealKey)
		assert.NotContains(t, result.Stdout()+result.Stderr(), mcpToken)
	})

	t.Run("ForbiddenDAGFailsAtTheStart", func(t *testing.T) {
		before := len(mcp.all())
		result := runner.RunWithEnv(dagu.Env, "start", "mcp-chat-forbidden.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "ask": MCP server "docs": mcp_server_forbidden`)
		assert.Len(t, mcp.all(), before, "no request goes to the MCP server")
	})
}

// Spec 095, "The MCP relay": a chat step on a general worker reaches the MCP server
// through the gRPC stream. The coordinator adds the token and the worker never has it.
func TestChatNamedMCPOnAWorker(t *testing.T) {
	st := aitest.StartStackAs(t, "relay_coordinator_config.yaml", false)
	mcp := startFakeMCP(t, "sse")
	model := startMCPChatModel(t)
	st.CreateEndpoint(model.URL(), aitest.RealKey, "mcp-chat-worker")
	createMCPServer(t, st, mcp.URL()+"/docs", "mcp-chat-worker")
	worker := st.StartGeneralWorker("general-mcp")

	st.Enqueue("mcp-chat-worker.yaml", "mcp-chat-1")
	details := st.WaitRun("mcp-chat-worker", "mcp-chat-1")
	require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText()+"\n"+worker.Output())

	requests := model.records()
	require.Len(t, requests, 2)
	assert.Equal(t, []string{"docs_search"}, requests[0].tools)
	assert.Contains(t, requests[1].body, "search result for dagu")
	docs := mcp.onPath("/docs")
	require.NotEmpty(t, docs)
	for _, request := range docs {
		assert.Equal(t, "Bearer "+mcpToken, request.Header.Get("Authorization"))
	}
	assert.Equal(t, []string{"search"}, toolsCalled(docs))
	assert.Contains(t, aitest.StepLogs(t, st.Server.Home), "The docs say: found.")

	st.AssertKeyNowhere(mcpToken)
	st.AssertKeyNowhere(aitest.RealKey)
	assert.NotContains(t, worker.Output(), mcpToken)
}

// Spec 095, "The key tools.mcp": the loader accepts the named form and the inline form,
// and rejects an entry with headers and no url. The llm block of a DAG cannot have a
// named entry.
func TestValidateTheNamedFormOfTheMCPEntry(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	for _, file := range []string{
		"mcp-run.yaml", "mcp-forbidden.yaml", "mcp-unknown.yaml", "mcp-local.yaml",
		"mcp-chat.yaml", "mcp-chat-forbidden.yaml", "mcp-chat-worker.yaml",
	} {
		t.Run("valid "+file, func(_ *testing.T) {
			dagu.Run("validate", file).ExpectExitCode(0)
		})
	}

	for _, tc := range []struct {
		fixture string
		want    []string
	}{
		{"invalid_mcp_headers_without_url.yaml", []string{"with.tools.mcp[0].headers", "headers is not valid on an mcp entry with no url"}},
		{"invalid_mcp_no_allow.yaml", []string{"with.tools.mcp[0].allow", "allow is required"}},
		{"invalid_mcp_empty_url.yaml", []string{"with.tools.mcp[0].url", "url must be a non-empty string"}},
		{"invalid_mcp_llm_block.yaml", []string{"an mcp entry with no url names a MCP server entry of the AI page"}},
	} {
		t.Run(tc.fixture, func(_ *testing.T) {
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.want...)
		})
	}
}
