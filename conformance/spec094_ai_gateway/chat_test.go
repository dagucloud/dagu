// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec094_ai_gateway_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests of Spec 094, "chat.completion": a chat.completion step with an endpoint
// calls the relay. The old method calls the provider with the key of the step.

// oldMethodKey is the key that the old method reads from the environment of the
// step. It goes from the step to the provider.
const oldMethodKey = "sk-old-method-KEY-0123456789"

// chatEnv is the environment of a local run of the old method.
func chatEnv(p *aitest.LocalProject, upstream *aitest.ChatUpstream) []string {
	return append(append([]string(nil), p.Env...), "LLM_BASE_URL="+upstream.URL(), "OLD_METHOD_KEY="+oldMethodKey)
}

// Spec 094, "chat.completion" and "Local mode": a chat step with an endpoint calls
// the relay of its process. The provider gets the real key. The old method calls the
// provider with the key of the environment, in the same suite.
func TestChatEndpointLocalMode(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(t)
	provider := aitest.StartChatUpstream(t)
	srv := aitest.StartServer(t, runner, nil)
	// The local runs use the data directory of the server: it holds the endpoints.
	dagu := aitest.NewLocalProject(t, runner, srv.Home)

	status, body := srv.Call(http.MethodPost, "/ai/endpoints", map[string]any{
		"name": "test-endpoint", "preset": "custom", "protocol": "anthropic", "baseUrl": provider.URL(),
		"auth": "x-api-key", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": []string{
			"chat-endpoint", "chat-endpoint-nostream", "chat-endpoint-list", "chat-endpoint-structured",
		}},
		"key": aitest.RealKey,
	})
	require.Equal(t, http.StatusCreated, status, body)
	env := chatEnv(dagu, provider)

	// The step streams by default. The provider gets the real key and the Anthropic form.
	t.Run("StreamsThroughTheRelay", func(t *testing.T) {
		result := runner.RunWithEnv(env, "start", "chat-endpoint.yaml")
		result.ExpectExitCode(0)
		assert.Equal(t, "Hello world\n", aitest.ReadResult(t, runner))
		requests := provider.All()
		require.Len(t, requests, 1)
		assert.Equal(t, "/v1/messages", requests[0].Path)
		assert.True(t, requests[0].Stream)
		assert.Equal(t, aitest.RealKey, requests[0].Header.Get("X-Api-Key"), "the provider gets the real key")
		assert.Equal(t, "claude-test", requests[0].Model)
		assert.NotContains(t, result.Stdout()+result.Stderr(), aitest.RealKey)
		srv.AssertNoKey(aitest.RealKey)
	})

	t.Run("DoesNotStream", func(t *testing.T) {
		before := len(provider.All())
		runner.RunWithEnv(env, "start", "chat-endpoint-nostream.yaml").ExpectExitCode(0)
		assert.Equal(t, "Hello world\n", aitest.ReadResult(t, runner))
		requests := provider.All()
		require.Len(t, requests, before+1)
		assert.False(t, requests[before].Stream)
		assert.Equal(t, aitest.RealKey, requests[before].Header.Get("X-Api-Key"))
	})

	// The step offers the tool respond, and the arguments of the call are the answer.
	t.Run("StructuredOutput", func(t *testing.T) {
		result := runner.RunWithEnv(env, "start", "chat-endpoint-structured.yaml")
		result.ExpectExitCode(0)
		assert.JSONEq(t, `{"amount":12.5,"category":"refund"}`, strings.TrimSpace(aitest.ReadResult(t, runner)))
		assert.Contains(t, result.Stdout(), "category=refund")
	})

	// The first model fails on the endpoint, and the second model is on the old
	// method: its call goes from the step to the provider with the key of the
	// environment, and the relay is not on its path.
	t.Run("FallsBackFromTheEndpointToTheOldMethod", func(t *testing.T) {
		before := len(provider.All())
		runner.RunWithEnv(env, "start", "chat-endpoint-list.yaml").ExpectExitCode(0)
		assert.Equal(t, "Hello from the old method\n", aitest.ReadResult(t, runner))
		requests := provider.All()[before:]
		require.Len(t, requests, 2)
		assert.Equal(t, "/v1/messages", requests[0].Path)
		assert.Equal(t, "bad-model", requests[0].Model)
		assert.Equal(t, aitest.RealKey, requests[0].Header.Get("X-Api-Key"), "the first entry went through the relay")
		assert.Equal(t, "/chat/completions", requests[1].Path)
		assert.Equal(t, "good-model", requests[1].Model)
		assert.Equal(t, "Bearer "+oldMethodKey, requests[1].Header.Get("Authorization"), "the old method sends the key of the environment")
		assert.Empty(t, requests[1].Header.Get("X-Api-Key"))
	})

	// The old method has no endpoint and no gateway: the key of the
	// environment of the step.
	t.Run("OldMethodIsUnchanged", func(t *testing.T) {
		before := len(provider.All())
		old := aitest.NewLocalProject(t, runner, filepath.Join(t.TempDir(), "empty-home"))
		runner.RunWithEnv(chatEnv(old, provider), "start", "chat-old-method.yaml").ExpectExitCode(0)
		assert.Equal(t, "Hello from the old method\n", aitest.ReadResult(t, runner))
		requests := provider.All()[before:]
		require.Len(t, requests, 1)
		assert.Equal(t, "Bearer "+oldMethodKey, requests[0].Header.Get("Authorization"))
	})

	// The DAG is not in the allow list: the step fails when it starts, and no
	// request goes to the provider.
	t.Run("ForbiddenDAGFailsAtTheStart", func(t *testing.T) {
		before := len(provider.All())
		result := runner.RunWithEnv(env, "start", "chat-endpoint-forbidden.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "ask": endpoint "test-endpoint": endpoint_forbidden`, `the DAG "chat-endpoint-forbidden" is not in the allow list`)
		assert.Len(t, provider.All(), before, "no request goes to the provider")
	})

	t.Run("DeletedEndpointFailsTheStart", func(t *testing.T) {
		status, body := srv.Call(http.MethodDelete, "/ai/endpoints/test-endpoint", nil)
		require.Equal(t, http.StatusNoContent, status, body)
		before := len(provider.All())
		result := runner.RunWithEnv(env, "start", "chat-endpoint.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`step "ask": endpoint "test-endpoint": endpoint_unknown`)
		assert.Len(t, provider.All(), before)
	})
}

// Spec 094, "chat.completion" and "Errors": the block endpoint with api_key_name,
// base_url or provider is a validation error that names both keys and the path.
// The same holds for the key endpoint of an entry of a model list. The keys model
// and thinking contradict the block too. A value that is not a block, an unknown
// key, a missing model and an effort out of the five levels are errors. A step with
// an endpoint block, with a list of models and with the old method is valid.
func TestValidateChatEndpointContradictions(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	for _, key := range []struct{ name, reason string }{
		{"api_key_name", "an endpoint holds its own key"},
		{"base_url", "an endpoint holds its own base URL"},
		{"provider", "an endpoint gives the provider type"},
	} {
		want := `key "endpoint" and key "` + key.name + `" contradict each other; ` + key.reason
		t.Run("with "+key.name, func(_ *testing.T) {
			result := dagu.Run("validate", "chat_contradiction_"+key.name+".yaml")
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(want, "field 'with.endpoint'")
		})
		t.Run("entry with "+key.name, func(_ *testing.T) {
			result := dagu.Run("validate", "chat_entry_contradiction_"+key.name+".yaml")
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(want, "with.model[1].endpoint")
		})
	}

	for _, tc := range []struct {
		fixture string
		want    []string
	}{
		{"chat_block_and_model.yaml", []string{"endpoint", "model"}},
		{"chat_block_and_thinking.yaml", []string{"endpoint", "thinking"}},
		{"chat_block_not_a_block.yaml", []string{"with.endpoint", "endpoint must be a block with name and model"}},
		{"chat_block_unknown_key.yaml", []string{"with.endpoint.temperature", `"temperature"`, "name, model, effort"}},
		{"chat_block_no_model.yaml", []string{"with.endpoint.model", "chat.completion needs a model; choose one that the endpoint lists"}},
		{"chat_block_bad_effort.yaml", []string{"with.endpoint.effort", "effort must be one of low, medium, high, xhigh, max"}},
		// A model list has no default endpoint: each entry names one, or a provider.
		{"chat_entry_bare.yaml", []string{"model[1].provider: required"}},
		// The endpoint is for chat.completion. The llm block of the DAG, which an agent
		// DAG uses, does not take it.
		{"chat_llm_block_endpoint.yaml", []string{"the key endpoint is valid only with the action chat.completion"}},
	} {
		t.Run(tc.fixture, func(_ *testing.T) {
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.want...)
		})
	}

	for _, file := range []string{
		"chat-endpoint.yaml", "chat-endpoint-nostream.yaml", "chat-endpoint-list.yaml",
		"chat-endpoint-structured.yaml", "chat-old-method.yaml", "chat-worker.yaml",
	} {
		t.Run("valid "+file, func(_ *testing.T) {
			dagu.Run("validate", file).ExpectExitCode(0)
		})
	}
}

// Spec 094, "chat.completion" and "Version check": a chat step with an endpoint runs
// on a worker through the gRPC stream to the coordinator. The coordinator reads the
// key and calls the provider. The worker never holds it. The test uses a general
// worker, so it needs no restricted user.
func TestChatEndpointOnAWorker(t *testing.T) {
	st := aitest.StartStackAs(t, "relay_coordinator_config.yaml", false)
	provider := aitest.StartChatUpstream(t)
	st.CreateEndpoint(provider.URL(), aitest.RealKey, "chat-worker")
	worker := st.StartGeneralWorker("general-chat")

	st.Enqueue("chat-worker.yaml", "chat-1")
	details := st.WaitRun("chat-worker", "chat-1")
	require.Equal(t, "succeeded", details.DAGRunDetails.StatusLabel, details.ErrorText()+"\n"+worker.Output())

	requests := provider.All()
	require.Len(t, requests, 1)
	assert.Equal(t, "/v1/messages", requests[0].Path)
	assert.Equal(t, aitest.RealKey, requests[0].Header.Get("X-Api-Key"), "the provider gets the real key")
	assert.True(t, requests[0].Stream, "the step streams by default")

	// The log of the step is on the coordinator, and holds the answer.
	assert.Contains(t, aitest.StepLogs(t, st.Server.Home), "Hello world")

	// The key is in no log, no output, no file of a home, and no response.
	st.AssertKeyNowhere(aitest.RealKey)
	st.Server.AssertNoKey(aitest.RealKey)
	assert.NotContains(t, worker.Output(), aitest.RealKey)
}

// A chat step on a worker, with an endpoint that the DAG may not use, fails when it
// starts with the reason endpoint_forbidden. No request goes to the provider.
func TestChatEndpointOnAWorkerForbiddenDAG(t *testing.T) {
	st := aitest.StartStackAs(t, "relay_coordinator_config.yaml", false)
	provider := aitest.StartChatUpstream(t)
	st.CreateEndpoint(provider.URL(), aitest.RealKey, "chat-worker")
	worker := st.StartGeneralWorker("general-chat")

	st.Enqueue("chat-worker-forbidden.yaml", "chat-2")
	details := st.WaitRun("chat-worker-forbidden", "chat-2")
	require.Equal(t, "failed", details.DAGRunDetails.StatusLabel, worker.Output())
	assert.Contains(t, details.ErrorText(), `endpoint "test-endpoint": endpoint_forbidden`)
	assert.Empty(t, provider.All(), "no request goes to the provider")
	st.AssertKeyNowhere(aitest.RealKey)
}
