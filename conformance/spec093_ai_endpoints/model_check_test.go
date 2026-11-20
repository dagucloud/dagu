// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec093_ai_endpoints_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests of the model check of Spec 093, "Use in a step": a step with an endpoint
// and a model that the models list of the endpoint lacks fails at start, before the
// CLI or the first request. The coordinator, or the process of a local run, refreshes
// the list once on demand before it fails.

// modelProvider is a provider with a models route and a chat route. The route of the
// models answers in the form of the protocol anthropic. A path that starts with
// /broken answers with the status 500. The chat route is the route of aitest.ChatUpstream.
type modelProvider struct {
	*aitest.ChatUpstream

	mu          sync.Mutex
	names       []string
	modelsCalls int
}

func startModelProvider(t *testing.T, names ...string) *modelProvider {
	t.Helper()
	p := &modelProvider{ChatUpstream: &aitest.ChatUpstream{}, names: names}
	// The coordinator reaches the fake on the loopback address of the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p.Server = httptest.NewUnstartedServer(http.HandlerFunc(p.handle))
	p.Server.Listener = listener
	p.Server.Start()
	t.Cleanup(p.Server.Close)
	return p
}

func (p *modelProvider) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/v1/models") {
		p.Serve(w, r)
		return
	}
	p.mu.Lock()
	p.modelsCalls++
	names := append([]string(nil), p.names...)
	p.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/broken") {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	items := make([]string, len(names))
	for i, name := range names {
		items[i] = fmt.Sprintf(`{"id":%q}`, name)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false}`, strings.Join(items, ","))
}

// release adds a model, as a provider does when it releases one.
func (p *modelProvider) release(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = append(p.names, name)
}

func (p *modelProvider) listCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.modelsCalls
}

// endpoint is the create request of a global endpoint of the preset custom, with the
// models URL on the path of the provider.
func (p *modelProvider) endpoint(name, modelsPath string, dags ...string) map[string]any {
	return map[string]any{
		"name": name, "preset": "custom", "protocol": "anthropic", "baseUrl": p.URL(),
		"auth": "x-api-key", "scope": "global", "modelsUrl": p.URL() + modelsPath,
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": dags}, "key": aitest.RealKey,
	}
}

const modelGhostMessage = `model_unknown: the model "claude-ghost" is not in the models list of the endpoint (read at `

// Spec 093, "Use in a step", in local mode: the process of the run holds the gateway.
func TestModelCheckLocalMode(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(t)
	provider := startModelProvider(t, "claude-a", "claude-b")
	srv := aitest.StartServerWithConfig(t, runner, modelsConfig, nil)
	dagu := aitest.NewLocalProject(t, runner, srv.Home)

	status, body := srv.Call(http.MethodPost, "/ai/endpoints", provider.endpoint("test-endpoint", "/v1/models",
		"model-chat", "model-chat-list", "model-agent", "model-agent-nomodel"))
	require.Equal(t, http.StatusCreated, status, body)
	status, body = srv.Call(http.MethodPost, "/ai/endpoints", provider.endpoint("test-endpoint-2", "/broken/v1/models", "model-chat-broken"))
	require.Equal(t, http.StatusCreated, status, body)
	require.Equal(t, []string{"claude-a", "claude-b"}, srv.WaitModels("test-endpoint").Models)
	assert.Contains(t, srv.WaitModels("test-endpoint-2").Error, "500", "the list of test-endpoint-2 cannot be read")

	withModel := func(model string) []string { return append(append([]string(nil), dagu.Env...), "MODEL="+model) }

	t.Run("ListedModelRuns", func(t *testing.T) {
		before, calls := len(provider.All()), provider.listCalls()
		runner.RunWithEnv(withModel("claude-b"), "start", "model-chat.yaml").ExpectExitCode(0)
		assert.Equal(t, "Hello world\n", aitest.ReadResult(t, runner))
		assert.Len(t, provider.All(), before+1)
		assert.Equal(t, calls, provider.listCalls(), "a listed model makes no call to the models route")
	})

	// The step fails before any request reaches the chat route. The check refreshes
	// the list once, and fails when the model is still missing.
	t.Run("UnlistedModelFailsAtStart", func(t *testing.T) {
		before, calls := len(provider.All()), provider.listCalls()
		result := runner.RunWithEnv(withModel("claude-ghost"), "start", "model-chat.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "ask": endpoint "test-endpoint": `+modelGhostMessage,
			"reload the list on the AI page, or fix the model")
		assert.Len(t, provider.All(), before, "no request reaches the chat route")
		assert.Equal(t, calls+1, provider.listCalls(), "the check refreshed the list once")
	})

	t.Run("EachEntryOfAListIsChecked", func(t *testing.T) {
		before := len(provider.All())
		result := runner.RunWithEnv(withModel("claude-ghost"), "start", "model-chat-list.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(modelGhostMessage)
		assert.Len(t, provider.All(), before, "the first entry is listed, and still no request goes out")
	})

	t.Run("AgentFailsBeforeTheCLI", func(t *testing.T) {
		dagu.Scenario(t, "ok")
		before := dagu.RanCount()
		result := runner.RunWithEnv(withModel("claude-ghost"), "start", "model-agent.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "code": endpoint "test-endpoint": ` + modelGhostMessage)
		assert.Equal(t, before, dagu.RanCount(), "the CLI did not start")
	})

	// The CLI accepts the aliases that the models API does not list.
	t.Run("CLIAliasPasses", func(t *testing.T) {
		dagu.Scenario(t, "ok")
		calls := provider.listCalls()
		for _, model := range []string{"sonnet", "opus", "haiku", "opusplan", "default", "sonnet[1m]"} {
			runner.RunWithEnv(withModel(model), "start", "model-agent.yaml").ExpectExitCode(0)
		}
		assert.Equal(t, 6, dagu.RanCount(), "the CLI ran for each step")
		assert.Equal(t, calls, provider.listCalls(), "no call to the models route")
	})

	// A step of the CLI claude with no model does not load: the model must be one
	// that the endpoint lists.
	t.Run("NoModelFailsAtLoad", func(t *testing.T) {
		before := dagu.RanCount()
		result := runner.RunWithEnv(dagu.Env, "start", "model-agent-nomodel.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.endpoint.model", "agent.run needs a model; choose one that the endpoint lists")
		assert.Equal(t, before, dagu.RanCount(), "the CLI did not start")
	})

	// A chat step names the model to the API, so it has no alias.
	t.Run("ChatHasNoAlias", func(_ *testing.T) {
		result := runner.RunWithEnv(withModel("sonnet"), "start", "model-chat.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`the model "sonnet" is not in the models list`)
	})

	// The provider releases a model after the list was read. The refresh of the check
	// finds it, and the step runs.
	t.Run("NewModelPassesAfterTheRefresh", func(t *testing.T) {
		provider.release("claude-brand-new")
		before, calls := len(provider.All()), provider.listCalls()
		runner.RunWithEnv(withModel("claude-brand-new"), "start", "model-chat.yaml").ExpectExitCode(0)
		assert.Equal(t, "Hello world\n", aitest.ReadResult(t, runner))
		assert.Len(t, provider.All(), before+1)
		assert.Equal(t, calls+1, provider.listCalls())
		assert.Contains(t, srv.Models("test-endpoint").Models, "claude-brand-new", "the refresh kept the new list")
	})

	// The list was never read, so the step goes on. The provider answers at the first
	// request, and its answer for an unknown model is an upstream_error.
	t.Run("UnavailableListLetsTheStepGoOn", func(t *testing.T) {
		before := len(provider.All())
		result := runner.RunWithEnv(dagu.Env, "start", "model-chat-broken.yaml")
		result.ExpectExitCode(0)
		assert.Equal(t, "Hello world\n", aitest.ReadResult(t, runner))
		assert.Len(t, provider.All(), before+1, "the request reached the provider")
	})

	t.Run("DeletedEndpointIsUnknown", func(t *testing.T) {
		status, body := srv.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
		require.Equal(t, http.StatusNoContent, status, body)
		before := len(provider.All())
		result := runner.RunWithEnv(withModel("claude-a"), "start", "model-chat.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "ask": endpoint "test-endpoint": endpoint_unknown`)
		dagu.Scenario(t, "ok")
		agentBefore := dagu.RanCount()
		result = runner.RunWithEnv(withModel("claude-a"), "start", "model-agent.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "code": endpoint "test-endpoint": endpoint_unknown`)
		assert.Equal(t, agentBefore, dagu.RanCount(), "the CLI did not start")
		assert.Len(t, provider.All(), before)
	})
}

// Spec 093, "Use in a step", on a worker: the coordinator checks the model of a chat
// step of a general worker. The coordinator refreshes the list once for a miss, and
// not again within 60 seconds.
func TestModelCheckOnAWorker(t *testing.T) {
	st := aitest.StartStackAs(t, "relay_coordinator_config.yaml", false)
	provider := startModelProvider(t, "claude-a", "claude-b")
	status, body := st.Server.Call(http.MethodPost, "/ai/endpoints",
		provider.endpoint("test-endpoint", "/v1/models", "model-worker-listed", "model-worker-new", "model-worker-ghost"))
	require.Equal(t, http.StatusCreated, status, body)
	require.Equal(t, []string{"claude-a", "claude-b"}, st.Server.WaitModels("test-endpoint").Models)
	st.StartGeneralWorker("general-model")

	runIt := func(dag, id string) aitest.RunDetails {
		t.Helper()
		st.Enqueue(dag+".yaml", id)
		return st.WaitRun(dag, id)
	}

	t.Run("ListedModelRuns", func(t *testing.T) {
		calls := provider.listCalls()
		details := runIt("model-worker-listed", "listed")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		assert.Equal(t, calls, provider.listCalls())
	})

	t.Run("NewModelPassesAfterTheRefresh", func(t *testing.T) {
		provider.release("claude-brand-new")
		calls := provider.listCalls()
		details := runIt("model-worker-new", "new")
		require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText())
		assert.Equal(t, calls+1, provider.listCalls(), "one refresh on demand")
	})

	// The refresh of the last subtest was less than 60 seconds ago, so this miss makes
	// no call to the models route.
	t.Run("UnlistedModelFailsAtStartWithoutASecondRefresh", func(t *testing.T) {
		before, calls := len(provider.All()), provider.listCalls()
		details := runIt("model-worker-ghost", "ghost")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `step "ask": endpoint "test-endpoint": `+modelGhostMessage)
		assert.Equal(t, calls, provider.listCalls(), "at most one refresh on demand in 60 seconds")
		assert.Len(t, provider.All(), before, "no request reaches the chat route")
	})

	t.Run("DeletedEndpointIsUnknown", func(t *testing.T) {
		status, body := st.Server.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
		require.Equal(t, http.StatusNoContent, status, body)
		details := runIt("model-worker-listed", "listed-2")
		assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
		assert.Contains(t, details.ErrorText(), `endpoint "test-endpoint": endpoint_unknown`)
	})
	st.AssertKeyNowhere(aitest.RealKey)
}

// Spec 093, "Use in a step", on an agent worker: the check fails an agent step before
// the CLI starts. The test needs the user ID 0, like the other tests of the agent worker.
func TestModelCheckOnAnAgentWorker(t *testing.T) {
	st := aitest.StartStack(t, "relay_coordinator_config.yaml")
	provider := startModelProvider(t, "claude-a", "claude-b")
	status, body := st.Server.Call(http.MethodPost, "/ai/endpoints", provider.endpoint("test-endpoint", "/v1/models", "model-agent-worker"))
	require.Equal(t, http.StatusCreated, status, body)
	require.Equal(t, []string{"claude-a", "claude-b"}, st.Server.WaitModels("test-endpoint").Models)

	st.Scenario("ok", nil)
	worker := st.StartAgentWorker("agent-model")
	st.Enqueue("model-agent-worker.yaml", "ghost")
	details := st.WaitRun("model-agent-worker", "ghost")
	st.ExpectWorkerExit(worker)
	assert.Equal(t, "failed", details.DAGRunDetails.StatusLabel)
	assert.Contains(t, details.ErrorText(), `endpoint "test-endpoint": `+modelGhostMessage)
	assert.Zero(t, st.RanCount(), "the CLI did not start")
	assert.Empty(t, provider.All(), "no request reaches the chat route")
}
