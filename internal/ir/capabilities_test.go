// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

func TestRequiredCapabilities(t *testing.T) {
	t.Parallel()

	plain := &ir.DAG{Steps: []ir.Step{{Name: "a", Agent: &ir.AgentRun{Provider: "claude"}}}}
	assert.Empty(t, plain.RequiredCapabilities())
	_, needs := plain.DescribeMissingAIRelay()
	assert.False(t, needs)

	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "a"},
		{Name: "code_1", Agent: &ir.AgentRun{Provider: "claude", Endpoint: "cle1"}},
	}}
	assert.Equal(t, []string{ir.CapabilityAIRelay}, dag.RequiredCapabilities())
	text, needs := dag.DescribeMissingAIRelay()
	assert.True(t, needs)
	assert.Equal(t, `no worker supports the AI relay: the step "code_1" names the endpoint "cle1" (worker_too_old)`, text)

	onFailure := &ir.DAG{HandlerOn: ir.HandlerOn{Failure: &ir.Step{Name: "h", Agent: &ir.AgentRun{Provider: "claude", Endpoint: "cle1"}}}}
	assert.Equal(t, []string{ir.CapabilityAIRelay}, onFailure.RequiredCapabilities())
}

// A step in the body of a foreach step that names an endpoint needs the
// capability too.
func TestRequiredCapabilitiesOfAForeachBody(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{{
		Name: "each",
		Foreach: &ir.ForeachConfig{Steps: []ir.Step{
			{Name: "judge", Agent: &ir.AgentRun{Provider: "claude", Endpoint: "cle1"}},
		}},
	}}}
	assert.Equal(t, []string{ir.CapabilityAIRelay}, dag.RequiredCapabilities())
}

// A chat.completion step needs the capability when one of its models names an
// endpoint. A step on the old method needs none. Another executor with an llm
// block needs none.
func TestRequiredCapabilitiesOfChatSteps(t *testing.T) {
	t.Parallel()

	chat := func(name string, cfg *ir.LLMConfig) ir.Step {
		return ir.Step{Name: name, ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeChat}, LLM: cfg}
	}

	old := &ir.DAG{Steps: []ir.Step{chat("a", &ir.LLMConfig{Provider: "openai", Model: "gpt-4o"})}}
	assert.Empty(t, old.RequiredCapabilities())

	single := &ir.DAG{Steps: []ir.Step{chat("a", &ir.LLMConfig{Model: "m", Endpoint: "cle1"})}}
	assert.Equal(t, []string{ir.CapabilityAIRelay}, single.RequiredCapabilities())

	// The first model uses the old method and the second one uses an endpoint.
	list := &ir.DAG{Steps: []ir.Step{{Name: "b"}, chat("a", &ir.LLMConfig{Models: []ir.ModelEntry{
		{Provider: "openai", Name: "gpt-4o"},
		{Name: "m", Endpoint: "cle2"},
	}})}}
	assert.Equal(t, []string{ir.CapabilityAIRelay}, list.RequiredCapabilities())
	text, needs := list.DescribeMissingAIRelay()
	assert.True(t, needs)
	assert.Equal(t, `no worker supports the AI relay: the step "a" names the endpoint "cle2" (worker_too_old)`, text)

	other := &ir.DAG{Steps: []ir.Step{{Name: "a", ExecutorConfig: ir.ExecutorConfig{Type: "browser"}, LLM: &ir.LLMConfig{Endpoint: "cle1"}}}}
	assert.Empty(t, other.RequiredCapabilities())
}

func TestLLMEndpoints(t *testing.T) {
	t.Parallel()

	cfg := &ir.LLMConfig{Models: []ir.ModelEntry{
		{Name: "a", Endpoint: "cle1"},
		{Provider: "openai", Name: "b"},
		{Name: "c", Endpoint: "cle2"},
		{Name: "d", Endpoint: "cle1"},
	}}
	assert.Equal(t, []string{"cle1", "cle2"}, cfg.Endpoints())
	assert.Nil(t, (&ir.LLMConfig{Provider: "openai", Model: "m"}).Endpoints())
	assert.Equal(t, []string{"cle1"}, (&ir.LLMConfig{Model: "m", Endpoint: "cle1"}).Endpoints())
}

func TestWorkerHasCapabilities(t *testing.T) {
	t.Parallel()

	assert.True(t, ir.WorkerHasCapabilities(nil, nil))
	assert.True(t, ir.WorkerHasCapabilities([]string{"a", ir.CapabilityAIRelay}, []string{ir.CapabilityAIRelay}))
	assert.False(t, ir.WorkerHasCapabilities(nil, []string{ir.CapabilityAIRelay}))
	assert.False(t, ir.WorkerHasCapabilities([]string{"a"}, []string{ir.CapabilityAIRelay}))
}
