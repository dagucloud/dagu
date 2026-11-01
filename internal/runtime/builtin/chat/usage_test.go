// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usageProviderType builds the scripted provider a test keys by base URL
// through llm.NewProvider, which wraps it as it wraps every real provider,
// so its calls are recorded the way a real provider's are.
const usageProviderType llmpkg.ProviderType = "chat-usage-test"

var usageProviders sync.Map // base URL -> *scriptedProvider

func init() {
	llmpkg.RegisterProvider(usageProviderType, func(cfg llmpkg.Config) (llmpkg.Provider, error) {
		provider, ok := usageProviders.Load(cfg.BaseURL)
		if !ok {
			return nil, fmt.Errorf("no scripted provider for %s", cfg.BaseURL)
		}
		return provider.(*scriptedProvider), nil
	})
}

// A chat step's model usage counts every call it made, by the model that
// was asked: a tool-call turn, answers the step had to correct, the model
// that failed, and the one that answered after it.
func TestChatStepRecordsEveryModelCall(t *testing.T) {
	t.Parallel()

	usage := func(prompt int) llmpkg.Usage {
		return llmpkg.Usage{PromptTokens: prompt, CompletionTokens: 1, TotalTokens: prompt + 1}
	}
	missing := llmpkg.ToolCall{ID: "call-missing", Type: "function", Function: llmpkg.ToolCallFunction{Name: "missing", Arguments: "{}"}}
	weakTurns := []*llmpkg.ChatResponse{
		{ToolCalls: []llmpkg.ToolCall{missing}, Usage: usage(10)},
		{Content: "no idea", Usage: usage(20)},
		{Content: "still no idea", Usage: usage(30)},
		{Content: "nothing", Usage: usage(40)},
	}
	weak := &scriptedProvider{responses: weakTurns}
	answer := respondWith(`{"category":"refund"}`)
	answer.Usage = usage(50)
	strong := &scriptedProvider{responses: []*llmpkg.ChatResponse{answer}}

	step := classifyStep()
	step.LLM = &ir.LLMConfig{Models: []ir.ModelEntry{{Provider: "openai", Name: "weak"}, {Provider: "openai", Name: "strong"}}}
	run := newStructuredRun(t, step, nil)
	run.executor.toolRegistry = toolRegistry()
	base := "scripted://" + t.Name() + "/"
	usageProviders.Store(base+"weak", weak)
	usageProviders.Store(base+"strong", strong)
	t.Cleanup(func() {
		usageProviders.Delete(base + "weak")
		usageProviders.Delete(base + "strong")
	})
	run.executor.newProvider = func(_ context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error) {
		return llmpkg.NewProvider(usageProviderType, llmpkg.Config{BaseURL: base + cfg.Model})
	}

	meter := modelusage.NewMeter(nil)
	require.NoError(t, run.executor.Run(modelusage.WithMeter(chatRuntimeContext(t, nil), meter)))

	asked := len(weak.Requests())
	require.GreaterOrEqual(t, asked, 2, "the weak model took a tool turn and an answer")
	var weakPrompt int64
	for _, turn := range weakTurns[:asked] {
		weakPrompt += int64(turn.Usage.PromptTokens)
	}
	recorded := meter.Snapshot()
	require.NotNil(t, recorded, "the step's calls were recorded")
	assert.Equal(t, []ir.ModelUsage{
		{Provider: "scripted", Model: "strong", Calls: 1, InputTokens: 50, OutputTokens: 1},
		{Provider: "scripted", Model: "weak", Calls: int64(asked), InputTokens: weakPrompt, OutputTokens: int64(asked)},
	}, recorded.Models)
}
