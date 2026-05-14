// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryTestProvider struct {
	chatFunc func(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}

func (p *retryTestProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.chatFunc(ctx, req)
}

func (p *retryTestProvider) ChatStream(context.Context, *ChatRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}

func (p *retryTestProvider) Name() string {
	return "retry-test"
}

func TestChatWithRetry(t *testing.T) {
	t.Parallel()

	t.Run("retries transient decode failure", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		provider := &retryTestProvider{
			chatFunc: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
				if calls.Add(1) == 1 {
					return nil, WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF))
				}
				return &ChatResponse{Content: "ok", FinishReason: "stop"}, nil
			},
		}

		resp, err := ChatWithRetry(context.Background(), provider, &ChatRequest{Model: "test"}, DefaultLogicalRetryConfig())
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "ok", resp.Content)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("does not retry non retryable errors", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		provider := &retryTestProvider{
			chatFunc: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
				calls.Add(1)
				return nil, ErrInvalidRequest
			},
		}

		_, err := ChatWithRetry(context.Background(), provider, &ChatRequest{Model: "test"}, DefaultLogicalRetryConfig())
		require.ErrorIs(t, err, ErrInvalidRequest)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("does not retry when parent context is canceled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var calls atomic.Int32
		provider := &retryTestProvider{
			chatFunc: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
				calls.Add(1)
				return nil, WrapError("openrouter", fmt.Errorf("failed to decode response: %w", context.Canceled))
			},
		}

		_, err := ChatWithRetry(ctx, provider, &ChatRequest{Model: "test"}, DefaultLogicalRetryConfig())
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("normalizes request before provider call", func(t *testing.T) {
		t.Parallel()

		var captured *ChatRequest
		provider := &retryTestProvider{
			chatFunc: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
				captured = req
				return &ChatResponse{Content: "ok", FinishReason: "stop"}, nil
			},
		}
		req := &ChatRequest{
			Model: "test",
			Tools: []Tool{
				testTool(WebSearchToolName, "first"),
				testTool(WebSearchToolName, "second"),
			},
			WebSearch: &WebSearchRequest{Enabled: true},
		}

		_, err := ChatWithRetry(context.Background(), provider, req, DefaultLogicalRetryConfig())
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.Len(t, captured.Tools, 1)
		assert.False(t, captured.WebSearch.Enabled)
		assert.True(t, req.WebSearch.Enabled, "normalization must not mutate caller request")
	})
}

func TestShouldRetryRequest(t *testing.T) {
	t.Parallel()

	t.Run("retries wrapped context canceled when parent is alive", func(t *testing.T) {
		t.Parallel()

		err := WrapError("openrouter", fmt.Errorf("failed to decode response: %w", context.Canceled))
		assert.True(t, ShouldRetryRequest(context.Background(), err))
	})

	t.Run("does not retry auth errors", func(t *testing.T) {
		t.Parallel()

		assert.False(t, ShouldRetryRequest(context.Background(), ErrUnauthorized))
	})

	t.Run("does not retry after caller cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF))
		assert.False(t, ShouldRetryRequest(ctx, err))
	})
}

func TestIsRetryableTransportError(t *testing.T) {
	t.Parallel()

	assert.True(t, isRetryableTransportError(io.ErrUnexpectedEOF))
	assert.True(t, isRetryableTransportError(context.DeadlineExceeded))
	assert.False(t, isRetryableTransportError(errors.New("permanent")))
}
