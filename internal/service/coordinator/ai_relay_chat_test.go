// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 094, "chat.completion". The step code of the fixture is a chat.completion
// step. It names its endpoint through its models.

// chatStepContext is the context of a chat step that runs on a worker with the
// client of the relay.
func chatStepContext(t *testing.T, client aigateway.Client) context.Context {
	t.Helper()
	ctx := runctx.NewContext(context.Background(), &ir.DAG{Name: "fix-bug"}, "run-1", "/tmp/log")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{Name: "code"}))
	return aigateway.WithClient(ctx, client)
}

func chatStep(llm *ir.LLMConfig) ir.Step {
	return ir.Step{
		Name:           "code",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeChat},
		LLM:            llm,
		Messages:       []ir.PromptMessage{{Role: ir.LLMRoleUser, Content: "Say hello."}},
	}
}

// The check 5 reads the name of the endpoint from the models of the step. The
// start check gives the chat type, and a request cannot name another endpoint.
func TestAIRelay_ChatStepNamesTheEndpointThroughItsModels(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{chatStep: true})
	client := f.good()
	ctx := context.Background()

	result, err := client.Check(ctx, relayStep, aigateway.EndpointTarget("cle1"), "")
	require.NoError(t, err)
	assert.Equal(t, aiendpoint.ProtocolAnthropic, result.Protocol)
	assert.Equal(t, "anthropic", result.ChatType, "the type of the provider comes from the endpoint")

	body := `{"model":"m","messages":[]}`
	sink := newMemSink()
	require.NoError(t, client.Relay(ctx, relayStep, aigateway.EndpointTarget("cle1"), postMessages(body), strings.NewReader(body), sink))
	assert.Equal(t, http.StatusOK, sink.status)
	headers := f.upstreamHeaders()
	require.Len(t, headers, 1)
	assert.Equal(t, relayRealKey, headers[0].Get("X-Api-Key"))

	_, err = client.Check(ctx, relayStep, aigateway.EndpointTarget("cle2"), "")
	assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason, "the request cannot name another endpoint")
	_, err = client.Check(ctx, aigateway.StepRef{DAGName: "fix-bug", StepName: "plain"}, aigateway.EndpointTarget("cle1"), "")
	assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason, "a step that names no endpoint")
}

// A chat step on a worker calls the coordinator over gRPC with mutual TLS. The
// real key reaches the provider and no other place.
func TestAIRelay_ChatStepOverGRPC(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{
		chatStep: true,
		handler: func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/messages", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"hello from the provider"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
		},
	})

	stream := false
	exec, err := executor.NewExecutor(chatStepContext(t, f.good()), chatStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1", Stream: &stream}))
	require.NoError(t, err)
	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(io.Discard)
	require.NoError(t, exec.Run(chatStepContext(t, f.good())))

	assert.Equal(t, "hello from the provider\n", stdout.String())
	headers := f.upstreamHeaders()
	require.Len(t, headers, 1)
	assert.Equal(t, relayRealKey, headers[0].Get("X-Api-Key"), "the provider gets the real key")
	assert.NotEmpty(t, headers[0].Get("Anthropic-Version"))
	assert.NotContains(t, stdout.String(), relayRealKey)
	records := f.audit.all()
	require.Len(t, records, 1)
	assert.Equal(t, "messages", records[0].Route)
	assert.Equal(t, "code", records[0].StepName)
}

// A chat step on a worker that does not hold the lease is refused at the start
// check, before any request goes to the provider.
func TestAIRelay_ChatStepOfAnotherWorkerIsRefused(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{chatStep: true})
	other := f.relayClient("worker-cert", "worker-2", f.attemptK, f.attemptI)

	exec, err := executor.NewExecutor(chatStepContext(t, other), chatStep(&ir.LLMConfig{Model: "m", Endpoint: "cle1"}))
	require.NoError(t, err)
	exec.SetStdout(io.Discard)
	exec.SetStderr(io.Discard)
	err = exec.Run(chatStepContext(t, other))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "code": endpoint "cle1": endpoint_forbidden`)
	assert.Empty(t, f.upstreamHeaders())
}
