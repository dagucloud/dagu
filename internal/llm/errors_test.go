// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIError(t *testing.T) {
	t.Parallel()

	t.Run("ErrorMessage", func(t *testing.T) {
		t.Parallel()
		err := &APIError{Provider: "test", StatusCode: 500, Message: "failed"}
		assert.Contains(t, err.Error(), "test API error")
		assert.Contains(t, err.Error(), "500")
	})

	t.Run("ErrorMessageWithWrapped", func(t *testing.T) {
		t.Parallel()
		inner := errors.New("inner error")
		err := &APIError{Provider: "test", StatusCode: 500, Message: "failed", Err: inner}
		assert.Contains(t, err.Error(), "inner error")
	})

	t.Run("Unwrap", func(t *testing.T) {
		t.Parallel()
		inner := errors.New("inner")
		err := &APIError{Err: inner}
		assert.Equal(t, inner, err.Unwrap())
	})
}

func TestNewAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status    int
		retryable bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
	}

	for _, tc := range tests {
		err := NewAPIError("test", tc.status, "msg")
		assert.Equal(t, tc.retryable, err.Retryable, "status %d", tc.status)
	}
}

func TestNewAPIError_ClassifiesContextOverflow(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"error":{"code":"context_length_exceeded","message":"request is too large"}}`,
		`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 120000 tokens > 100000 maximum"}}`,
		`{"error":{"code":400,"message":"The input token count exceeds the maximum number of tokens allowed."}}`,
		`{"error":"request exceeds the available context size"}`,
	}
	for _, body := range tests {
		err := NewAPIError("test", 400, body)
		require.ErrorIs(t, err, ErrContextTooLong)
		assert.False(t, err.Retryable)
	}
}

func TestNewAPIError_DoesNotClassifyOtherInvalidRequestsAsContextOverflow(t *testing.T) {
	t.Parallel()

	err := NewAPIError("test", 400,
		`{"error":{"type":"invalid_request_error","message":"temperature must be between zero and two"}}`)
	assert.NotErrorIs(t, err, ErrContextTooLong)
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	assert.False(t, IsRetryable(nil))
	assert.True(t, IsRetryable(ErrRateLimited))
	assert.True(t, IsRetryable(ErrServerError))
	assert.True(t, IsRetryable(ErrTimeout))
	assert.False(t, IsRetryable(ErrNoAPIKey))

	apiErr := &APIError{Retryable: true}
	assert.True(t, IsRetryable(apiErr))
}

func TestIsAuthError(t *testing.T) {
	t.Parallel()

	assert.False(t, IsAuthError(nil))
	assert.True(t, IsAuthError(ErrUnauthorized))
	assert.True(t, IsAuthError(ErrNoAPIKey))
	assert.False(t, IsAuthError(ErrRateLimited))

	assert.True(t, IsAuthError(&APIError{StatusCode: 401}))
	assert.True(t, IsAuthError(&APIError{StatusCode: 403}))
	assert.False(t, IsAuthError(&APIError{StatusCode: 500}))
}

func TestIsRateLimitError(t *testing.T) {
	t.Parallel()

	assert.False(t, IsRateLimitError(nil))
	assert.True(t, IsRateLimitError(ErrRateLimited))
	assert.True(t, IsRateLimitError(&APIError{StatusCode: 429}))
	assert.False(t, IsRateLimitError(&APIError{StatusCode: 500}))
}

func TestWrapError(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsNilForNil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, WrapError("test", nil))
	})

	t.Run("DoesNotDoubleWrap", func(t *testing.T) {
		t.Parallel()
		orig := &APIError{Provider: "orig", Message: "err"}
		wrapped := WrapError("new", orig)
		assert.Equal(t, orig, wrapped)
	})

	t.Run("WrapsRegularError", func(t *testing.T) {
		t.Parallel()
		err := errors.New("regular error")
		wrapped := WrapError("test", err)
		var apiErr *APIError
		assert.True(t, errors.As(wrapped, &apiErr))
		assert.Equal(t, "test", apiErr.Provider)
	})

	t.Run("MarksWrappedContextCanceledAsRetryable", func(t *testing.T) {
		t.Parallel()

		wrapped := WrapError("openrouter", fmt.Errorf("failed to decode response: %w", context.Canceled))
		var apiErr *APIError
		require.True(t, errors.As(wrapped, &apiErr))
		assert.True(t, apiErr.Retryable)
	})
}
