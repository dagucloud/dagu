// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package local

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

// Streaming requests must ask the server to include token usage in the final
// chunk, as the OpenAI provider does. Non-streaming requests must not send the
// option.
func TestBuildRequestBody_StreamOptions(t *testing.T) {
	t.Parallel()

	provider := &Provider{}
	req := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
	}

	tests := []struct {
		name     string
		stream   bool
		expected any
	}{
		{name: "Stream", stream: true, expected: map[string]any{"include_usage": true}},
		{name: "NoStream", stream: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := provider.buildRequestBody(req, tt.stream)
			require.NoError(t, err)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(body, &parsed))
			assert.Equal(t, tt.expected, parsed["stream_options"])
		})
	}
}

// An error sent after the 200 status must fail the stream, even when text has
// already arrived.
func TestStreamResponse_Error(t *testing.T) {
	t.Parallel()

	stream := `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n" +
		`data: {"error":{"code":503,"message":"upstream overloaded"}}` + "\n\n"

	provider := &Provider{}
	events := make(chan llm.StreamEvent)
	go provider.streamResponse(context.Background(), io.NopCloser(strings.NewReader(stream)), events)

	var got []llm.StreamEvent
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 2)
	assert.Equal(t, "partial", got[0].Delta)
	assert.True(t, got[1].Done)
	require.ErrorContains(t, got[1].Error, "upstream overloaded")
	assert.True(t, llm.IsRetryable(got[1].Error))
}
