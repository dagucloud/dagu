// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProviderType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected ProviderType
		wantErr  bool
	}{
		{"openai", ProviderOpenAI, false},
		{"anthropic", ProviderAnthropic, false},
		{"gemini", ProviderGemini, false},
		{"google", ProviderGemini, false},
		{"openrouter", ProviderOpenRouter, false},
		{"local", ProviderLocal, false},
		{"ollama", ProviderLocal, false},
		{"vllm", ProviderLocal, false},
		{"llama", ProviderLocal, false},
		{"zai", ProviderZAI, false},
		{"zhipu", ProviderZAI, false},
		{"zhipuai", ProviderZAI, false},
		{"glm", ProviderZAI, false},
		{"unknown", "", true},
		{"", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			result, err := ParseProviderType(tc.input)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrInvalidProvider)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestDefaultAPIKeyEnvVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider ProviderType
		expected string
	}{
		{ProviderOpenAI, "OPENAI_API_KEY"},
		{ProviderAnthropic, "ANTHROPIC_API_KEY"},
		{ProviderGemini, "GOOGLE_API_KEY"},
		{ProviderOpenRouter, "OPENROUTER_API_KEY"},
		{ProviderLocal, ""},
		{ProviderZAI, "ZAI_API_KEY"},
		{ProviderType("unknown"), ""},
	}

	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, DefaultAPIKeyEnvVar(tc.provider))
		})
	}
}

func TestDefaultBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider ProviderType
		expected string
	}{
		{ProviderOpenAI, "https://api.openai.com/v1"},
		{ProviderAnthropic, "https://api.anthropic.com"},
		{ProviderGemini, "https://generativelanguage.googleapis.com/v1beta"},
		{ProviderOpenRouter, "https://openrouter.ai/api/v1"},
		{ProviderLocal, "http://localhost:11434/v1"},
		{ProviderZAI, "https://api.z.ai/api/paas/v4"},
		{ProviderType("unknown"), ""},
	}

	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, DefaultBaseURL(tc.provider))
		})
	}
}

func TestGetAPIKeyFromEnv(t *testing.T) {
	t.Run("ReturnsEmptyForLocal", func(t *testing.T) {
		assert.Empty(t, GetAPIKeyFromEnv(ProviderLocal))
	})

	t.Run("ReturnsEnvValue", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "test-key")
		assert.Equal(t, "test-key", GetAPIKeyFromEnv(ProviderOpenAI))
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, 2.0, cfg.Multiplier)
}

// mockProvider for testing provider registration.
type mockProvider struct{ name string }

func (m *mockProvider) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{Content: "mock"}, nil
}
func (m *mockProvider) ChatStream(context.Context, *ChatRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) Name() string { return m.name }

func TestNewProvider(t *testing.T) {
	orig := registry
	defer func() { registry = orig }()
	registry = make(map[ProviderType]ProviderFactory)

	testType := ProviderType("test")
	RegisterProvider(testType, func(_ Config) (Provider, error) {
		return &mockProvider{name: "test"}, nil
	})

	t.Run("CreatesRegisteredProvider", func(t *testing.T) {
		p, err := NewProvider(testType, Config{})
		require.NoError(t, err)
		assert.Equal(t, "test", p.Name())
	})

	t.Run("ErrorsOnUnregistered", func(t *testing.T) {
		_, err := NewProvider(ProviderType("missing"), Config{})
		assert.ErrorIs(t, err, ErrInvalidProvider)
	})
}

func TestNewProvider_DisableRequestTimeout(t *testing.T) {
	orig := registry
	defer func() { registry = orig }()
	registry = make(map[ProviderType]ProviderFactory)

	testType := ProviderType("test-no-timeout")
	var captured Config
	RegisterProvider(testType, func(cfg Config) (Provider, error) {
		captured = cfg
		return &mockProvider{name: "test-no-timeout"}, nil
	})

	_, err := NewProvider(testType, Config{DisableRequestTimeout: true})
	require.NoError(t, err)

	assert.Zero(t, captured.Timeout)
	assert.Equal(t, DefaultConfig().MaxRetries, captured.MaxRetries)
	assert.Equal(t, DefaultConfig().InitialInterval, captured.InitialInterval)
	assert.Equal(t, DefaultConfig().MaxInterval, captured.MaxInterval)
	assert.Equal(t, DefaultConfig().Multiplier, captured.Multiplier)
}
