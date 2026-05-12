// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeChatRequest(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates tools by name and disables native web search on collision", func(t *testing.T) {
		t.Parallel()

		req := &ChatRequest{
			Model: "test",
			Tools: []Tool{
				testTool(WebSearchToolName, "first"),
				testTool(WebSearchToolName, "second"),
				testTool("read", "read"),
			},
			WebSearch: &WebSearchRequest{Enabled: true},
		}

		normalized := NormalizeChatRequest(req)

		require.NotSame(t, req, normalized)
		require.Len(t, normalized.Tools, 2)
		assert.Equal(t, "first", normalized.Tools[0].Function.Description)
		assert.Equal(t, "read", normalized.Tools[1].Function.Name)
		require.NotNil(t, normalized.WebSearch)
		assert.False(t, normalized.WebSearch.Enabled)

		require.Len(t, req.Tools, 3, "normalization must not mutate caller tools")
		assert.True(t, req.WebSearch.Enabled, "normalization must not mutate caller web search config")
	})

	t.Run("keeps native web search when no explicit web_search tool exists", func(t *testing.T) {
		t.Parallel()

		req := &ChatRequest{
			Model:     "test",
			Tools:     []Tool{testTool("read", "read")},
			WebSearch: &WebSearchRequest{Enabled: true},
		}

		normalized := NormalizeChatRequest(req)

		require.NotNil(t, normalized.WebSearch)
		assert.True(t, normalized.WebSearch.Enabled)
	})
}

func TestNewProviderNormalizesRequests(t *testing.T) {
	orig := registry
	defer func() { registry = orig }()
	registry = make(map[ProviderType]ProviderFactory)

	capturedChat := make(chan *ChatRequest, 1)
	capturedStream := make(chan *ChatRequest, 1)
	RegisterProvider(ProviderType("normalize-test"), func(_ Config) (Provider, error) {
		return &normalizingTestProvider{
			chat:   capturedChat,
			stream: capturedStream,
		}, nil
	})

	provider, err := NewProvider(ProviderType("normalize-test"), Config{})
	require.NoError(t, err)

	req := &ChatRequest{
		Model: "test",
		Tools: []Tool{
			testTool(WebSearchToolName, "first"),
			testTool(WebSearchToolName, "second"),
		},
		WebSearch: &WebSearchRequest{Enabled: true},
	}

	_, err = provider.Chat(context.Background(), req)
	require.NoError(t, err)
	chatReq := <-capturedChat
	require.Len(t, chatReq.Tools, 1)
	assert.False(t, chatReq.WebSearch.Enabled)

	events, err := provider.ChatStream(context.Background(), req)
	require.NoError(t, err)
	for range events {
	}
	streamReq := <-capturedStream
	require.Len(t, streamReq.Tools, 1)
	assert.False(t, streamReq.WebSearch.Enabled)
}

func testTool(name, description string) Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  map[string]any{"type": "object"},
		},
	}
}

type normalizingTestProvider struct {
	chat   chan<- *ChatRequest
	stream chan<- *ChatRequest
}

func (p *normalizingTestProvider) Chat(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
	p.chat <- req
	return &ChatResponse{Content: "ok"}, nil
}

func (p *normalizingTestProvider) ChatStream(_ context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	p.stream <- req
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func (p *normalizingTestProvider) Name() string {
	return "normalize-test"
}
