// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// Spec 093, "Use in a step". These tests make the model check over gRPC with mutual
// TLS, as a worker does. The coordinator holds the models list and refreshes it.

const modelsTwo = `{"data":[{"id":"claude-a"},{"id":"claude-b"}]}`

func (f *relayFixture) setModels(body string) {
	f.modelsMu.Lock()
	defer f.modelsMu.Unlock()
	f.models = body
}

func (f *relayFixture) modelsCallCount() int {
	f.modelsMu.Lock()
	defer f.modelsMu.Unlock()
	return f.modelsCalls
}

// startModelsFixture is a fixture whose list was read: claude-a and claude-b.
func startModelsFixture(t *testing.T, opts relayOptions) *relayFixture {
	t.Helper()
	opts.withModels = true
	opts.modelsBody = modelsTwo
	f := startRelayFixture(t, opts)
	_, err := f.service.RefreshModels(context.Background(), aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	return f
}

func (f *relayFixture) checkModel(provider, model string) error {
	_, err := aigateway.CheckStart(context.Background(), f.good(), relayStep, aigateway.EndpointTarget("cle1"), provider, model)
	return err
}

func TestAIRelay_ModelCheckOverGRPC(t *testing.T) {
	t.Parallel()
	f := startModelsFixture(t, relayOptions{})
	before := f.modelsCallCount()

	require.NoError(t, f.checkModel("claude", "claude-a"))
	assert.Equal(t, before, f.modelsCallCount(), "a listed model makes no call to the provider")

	err := f.checkModel("claude", "claude-ghost")
	failure := asFailure(t, err)
	assert.Equal(t, aigateway.ReasonModelUnknown, failure.Reason)
	assert.Contains(t, failure.Message, `the model "claude-ghost" is not in the models list of the endpoint (read at `)
	assert.Contains(t, failure.Message, "reload the list on the AI page, or fix the model")
	assert.Equal(t, before+1, f.modelsCallCount(), "a miss refreshes the list once")
	assert.Empty(t, f.upstreamHeaders(), "no request reaches the chat route")
}

// A model that the provider released after the last read passes after the refresh
// on demand, and the coordinator keeps the new list.
func TestAIRelay_ModelCheckRefreshesOnDemand(t *testing.T) {
	t.Parallel()
	f := startModelsFixture(t, relayOptions{})
	f.setModels(`{"data":[{"id":"claude-a"},{"id":"claude-b"},{"id":"claude-c"}]}`)
	require.NoError(t, f.checkModel("claude", "claude-c"))

	ep, err := f.service.Get(context.Background(), aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	list, err := f.service.ModelsOf(context.Background(), ep)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-a", "claude-b", "claude-c"}, list.Names)
}

// The CLI aliases pass with no call. A step with no model passes.
func TestAIRelay_ModelCheckAliasesAndNoModel(t *testing.T) {
	t.Parallel()
	f := startModelsFixture(t, relayOptions{})
	before := f.modelsCallCount()
	for _, model := range []string{"sonnet", "opus", "haiku", "opusplan", "default", "sonnet[1m]", ""} {
		require.NoError(t, f.checkModel("claude", model), model)
	}
	assert.Equal(t, before, f.modelsCallCount())
}

// With no list that a read made, the step goes on.
func TestAIRelay_ModelCheckSkippedWithoutList(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{})
	require.NoError(t, f.checkModel("claude", "anything"), "a service with no lists")

	g := startRelayFixture(t, relayOptions{withModels: true, modelsBody: "not json"})
	require.NoError(t, g.checkModel("claude", "anything"), "a list that no read made")
	h := startRelayFixture(t, relayOptions{withModels: true, modelsBody: `{"data":[]}`})
	require.NoError(t, h.checkModel("claude", "anything"), "an empty list")
}

// A chat step on a worker fails at start with model_unknown, before any request.
// Each entry of the model list is checked.
func TestAIRelay_ChatStepModelCheck(t *testing.T) {
	t.Parallel()
	f := startModelsFixture(t, relayOptions{chatStep: true})
	run := func(llm *ir.LLMConfig) error {
		exec, err := executor.NewExecutor(chatStepContext(t, f.good()), chatStep(llm))
		require.NoError(t, err)
		exec.SetStdout(io.Discard)
		exec.SetStderr(io.Discard)
		return exec.Run(chatStepContext(t, f.good()))
	}

	err := run(&ir.LLMConfig{Model: "claude-ghost", Endpoint: "cle1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "code": endpoint "cle1": model_unknown: the model "claude-ghost" is not in the models list of the endpoint (read at `)

	err = run(&ir.LLMConfig{Models: []ir.ModelEntry{{Name: "claude-a", Endpoint: "cle1"}, {Name: "claude-ghost", Endpoint: "cle1"}}})
	require.Error(t, err, "the second entry fails the step at start")
	assert.Contains(t, err.Error(), `model_unknown: the model "claude-ghost"`)
	assert.Empty(t, f.upstreamHeaders(), "no request reaches the chat route")
}

// An endpoint that the step names and that does not exist fails with
// endpoint_unknown, also when the step has a model, on a worker.
func TestAIRelay_UnknownEndpointFailsBeforeTheModelCheck(t *testing.T) {
	t.Parallel()
	f := startModelsFixture(t, relayOptions{endpointStep: "gone"})
	_, err := aigateway.CheckStart(context.Background(), f.good(), relayStep, aigateway.EndpointTarget("gone"), "claude", "claude-a")
	assert.Equal(t, aigateway.ReasonEndpointUnknown, asFailure(t, err).Reason)
}
