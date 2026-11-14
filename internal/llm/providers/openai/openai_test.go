// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A single SSE data line larger than bufio.Scanner's default 64 KiB limit must
// not terminate the stream.
func TestStreamResponse_LargeEvent(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("a", 200*1024)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"" + big + "\"}}]}\n\n" +
		"data: [DONE]\n\n"

	provider := &Provider{}
	events := make(chan llm.StreamEvent)
	go provider.streamResponse(context.Background(), io.NopCloser(strings.NewReader(stream)), events)

	var deltas []string
	done := false
	for event := range events {
		require.NoError(t, event.Error)
		if event.Done {
			done = true
			continue
		}
		deltas = append(deltas, event.Delta)
	}
	require.True(t, done)
	require.Equal(t, []string{big}, deltas)
}

// Reasoning effort passes through, except that an unset effort is medium
// and max becomes xhigh.
func TestBuildRequestBody_ReasoningEffort(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	reasoning := func(effort string) map[string]any { return map[string]any{"effort": effort} }

	tests := []struct {
		name     string
		thinking *llm.ThinkingRequest
		expected any
	}{
		{name: "Disabled", thinking: &llm.ThinkingRequest{Effort: llm.ThinkingEffortHigh}},
		{name: "Unset", thinking: &llm.ThinkingRequest{Enabled: true}, expected: reasoning("medium")},
		{name: "None", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone}, expected: reasoning("none")},
		{name: "Minimal", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal}, expected: reasoning("minimal")},
		{name: "XHigh", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortXHigh}, expected: reasoning("xhigh")},
		{name: "Max", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMax}, expected: reasoning("xhigh")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &llm.ChatRequest{
				Model:    "gpt-5.5",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
				Thinking: tt.thinking,
			}
			body, err := provider.buildRequestBody(req, false)
			require.NoError(t, err)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(body, &parsed))
			assert.Equal(t, tt.expected, parsed["reasoning"])
		})
	}
}
