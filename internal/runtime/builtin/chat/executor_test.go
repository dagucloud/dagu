// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	llmpkg "github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_MessageSaving(t *testing.T) {
	t.Parallel()

	t.Run("SavesAllMessagesWithInherited", func(t *testing.T) {
		t.Parallel()

		executor := &Executor{
			step: core.Step{
				LLM: &core.LLMConfig{
					Provider: "openai",
					Model:    "gpt-4o",
				},
			},
			messages: []exec.LLMMessage{
				{Role: exec.RoleUser, Content: "Hello"},
			},
			contextMessages: []exec.LLMMessage{
				{Role: exec.RoleSystem, Content: "You are helpful"},
				{Role: exec.RoleUser, Content: "Previous question"},
				{Role: exec.RoleAssistant, Content: "Previous answer"},
			},
		}

		// Simulate what happens after Run() completes
		allMessages := append(executor.contextMessages, executor.messages...)
		metadata := &exec.LLMMessageMetadata{
			Provider:         "openai",
			Model:            "gpt-4o",
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		}
		executor.savedMessages = append(allMessages, exec.LLMMessage{
			Role:     exec.RoleAssistant,
			Content:  "Hello there!",
			Metadata: metadata,
		})

		saved := executor.GetMessages()
		assert.Len(t, saved, 5) // 3 inherited + 1 user + 1 assistant
		assert.Equal(t, exec.RoleSystem, saved[0].Role)
		assert.Equal(t, exec.RoleAssistant, saved[4].Role)
		assert.NotNil(t, saved[4].Metadata)
		assert.Equal(t, 15, saved[4].Metadata.TotalTokens)
	})

	t.Run("MetadataAlwaysSaved", func(t *testing.T) {
		t.Parallel()

		executor := &Executor{
			step: core.Step{
				LLM: &core.LLMConfig{
					Provider: "gemini",
					Model:    "gemini-pro",
				},
			},
			messages: []exec.LLMMessage{
				{Role: exec.RoleUser, Content: "Test"},
			},
		}

		metadata := &exec.LLMMessageMetadata{
			Provider:         "gemini",
			Model:            "gemini-pro",
			PromptTokens:     5,
			CompletionTokens: 3,
			TotalTokens:      8,
		}
		executor.savedMessages = append(executor.messages, exec.LLMMessage{
			Role:     exec.RoleAssistant,
			Content:  "Response",
			Metadata: metadata,
		})

		saved := executor.GetMessages()
		assert.Len(t, saved, 2)

		assistantMsg := saved[1]
		assert.Equal(t, exec.RoleAssistant, assistantMsg.Role)
		assert.NotNil(t, assistantMsg.Metadata)
		assert.Equal(t, "gemini", assistantMsg.Metadata.Provider)
		assert.Equal(t, "gemini-pro", assistantMsg.Metadata.Model)
		assert.Equal(t, 5, assistantMsg.Metadata.PromptTokens)
		assert.Equal(t, 3, assistantMsg.Metadata.CompletionTokens)
		assert.Equal(t, 8, assistantMsg.Metadata.TotalTokens)
	})
}

func TestExecutor_SetContext(t *testing.T) {
	t.Parallel()

	executor := &Executor{}

	messages := []exec.LLMMessage{
		{Role: exec.RoleSystem, Content: "System prompt"},
		{Role: exec.RoleUser, Content: "User message"},
		{Role: exec.RoleAssistant, Content: "Assistant response"},
	}

	executor.SetContext(messages)

	assert.Equal(t, messages, executor.contextMessages)
}

func TestExecutor_PushBackExecutionMessagesUsePreviousConversationAndFeedback(t *testing.T) {
	t.Parallel()

	executor := &Executor{
		messages: []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "current system"},
			{Role: exec.RoleUser, Content: "original YAML prompt should not repeat"},
		},
		contextMessages: []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "previous system"},
			{Role: exec.RoleUser, Content: "original prompt"},
			{Role: exec.RoleAssistant, Content: "previous answer"},
		},
		step: core.Step{
			Approval: &core.ApprovalConfig{Input: []string{"FEEDBACK"}},
		},
	}
	executor.SetPushBackContext(map[string]string{
		"FEEDBACK": "revise with more detail",
		"IGNORED":  "do not include",
	}, 2)

	messages, err := executor.executionMessages(context.Background())
	require.NoError(t, err)

	require.Len(t, messages, 4)
	assert.Equal(t, exec.RoleSystem, messages[0].Role)
	assert.Equal(t, "current system", messages[0].Content)
	assert.Equal(t, "original prompt", messages[1].Content)
	assert.Equal(t, "previous answer", messages[2].Content)
	assert.Contains(t, messages[3].Content, "iteration 2")
	assert.Contains(t, messages[3].Content, "FEEDBACK: revise with more detail")
	assert.NotContains(t, messages[3].Content, "IGNORED")
	assert.NotContains(t, messages[3].Content, "original YAML prompt should not repeat")
}

func TestExecutor_GetMessages(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsNilWhenEmpty", func(t *testing.T) {
		t.Parallel()

		executor := &Executor{}
		assert.Nil(t, executor.GetMessages())
	})

	t.Run("ReturnsSavedMessages", func(t *testing.T) {
		t.Parallel()

		executor := &Executor{
			savedMessages: []exec.LLMMessage{
				{Role: exec.RoleUser, Content: "Hello"},
				{Role: exec.RoleAssistant, Content: "Hi"},
			},
		}

		saved := executor.GetMessages()
		assert.Len(t, saved, 2)
		assert.Equal(t, exec.RoleUser, saved[0].Role)
		assert.Equal(t, exec.RoleAssistant, saved[1].Role)
	})
}

func TestNormalizeEnvVarExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "PlainVariableName",
			input:    "OPENAI_API_KEY",
			expected: "${OPENAI_API_KEY}",
		},
		{
			name:     "DollarPrefix",
			input:    "$ANTHROPIC_KEY",
			expected: "${ANTHROPIC_KEY}",
		},
		{
			name:     "BracedFormat",
			input:    "${MY_API_KEY}",
			expected: "${MY_API_KEY}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := normalizeEnvVarExpr(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestNewChatExecutor(t *testing.T) {
	t.Parallel()

	t.Run("NilLLMConfig", func(t *testing.T) {
		t.Parallel()

		step := core.Step{Name: "test"}
		_, err := newChatExecutor(context.Background(), step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "llm configuration is required")
	})

	t.Run("InvalidProvider", func(t *testing.T) {
		t.Parallel()

		step := core.Step{
			Name: "test",
			LLM:  &core.LLMConfig{Provider: "invalid-provider"},
		}
		_, err := newChatExecutor(context.Background(), step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid provider")
	})

	t.Run("ValidConfigWithOpenAI", func(t *testing.T) {
		t.Parallel()

		step := core.Step{
			Name: "test",
			LLM: &core.LLMConfig{
				Provider: "openai",
				Model:    "gpt-4o",
			},
		}
		exec, err := newChatExecutor(context.Background(), step)
		require.NoError(t, err)
		assert.NotNil(t, exec)
	})

	t.Run("WithSystemMessage", func(t *testing.T) {
		t.Parallel()

		step := core.Step{
			Name: "test",
			LLM: &core.LLMConfig{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-20250514",
				System:   "You are a helpful assistant",
			},
		}
		exec, err := newChatExecutor(context.Background(), step)
		require.NoError(t, err)

		e := exec.(*Executor)
		assert.Len(t, e.messages, 1)
		assert.Equal(t, core.LLMRoleSystem, e.messages[0].Role)
		assert.Equal(t, "You are a helpful assistant", e.messages[0].Content)
	})

	t.Run("WithStepMessages", func(t *testing.T) {
		t.Parallel()

		step := core.Step{
			Name: "test",
			LLM: &core.LLMConfig{
				Provider: "openai",
				Model:    "gpt-4o",
			},
			Messages: []core.LLMMessage{
				{Role: core.LLMRoleUser, Content: "Hello"},
				{Role: core.LLMRoleAssistant, Content: "Hi there"},
			},
		}
		exec, err := newChatExecutor(context.Background(), step)
		require.NoError(t, err)

		e := exec.(*Executor)
		assert.Len(t, e.messages, 2)
		assert.Equal(t, core.LLMRoleUser, e.messages[0].Role)
		assert.Equal(t, core.LLMRoleAssistant, e.messages[1].Role)
	})

	t.Run("WithSystemAndStepMessages", func(t *testing.T) {
		t.Parallel()

		step := core.Step{
			Name: "test",
			LLM: &core.LLMConfig{
				Provider: "gemini",
				Model:    "gemini-pro",
				System:   "Be concise",
			},
			Messages: []core.LLMMessage{
				{Role: core.LLMRoleUser, Content: "What is 2+2?"},
			},
		}
		exec, err := newChatExecutor(context.Background(), step)
		require.NoError(t, err)

		e := exec.(*Executor)
		assert.Len(t, e.messages, 2)
		assert.Equal(t, core.LLMRoleSystem, e.messages[0].Role)
		assert.Equal(t, core.LLMRoleUser, e.messages[1].Role)
	})

	t.Run("CustomAPIKeyName", func(t *testing.T) {
		t.Parallel()

		step := core.Step{
			Name: "test",
			LLM: &core.LLMConfig{
				Provider:   "openai",
				Model:      "gpt-4o",
				APIKeyName: "MY_CUSTOM_KEY",
			},
		}
		exec, err := newChatExecutor(context.Background(), step)
		require.NoError(t, err)

		e := exec.(*Executor)
		assert.Equal(t, "MY_CUSTOM_KEY", e.apiKeyEnvVar)
	})
}

func TestExecutor_SetStdout(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	e := &Executor{}
	e.SetStdout(&buf)
	assert.Equal(t, &buf, e.stdout)
}

func TestExecutor_SetStderr(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	e := &Executor{}
	e.SetStderr(&buf)
	assert.Equal(t, &buf, e.stderr)
}

func TestExecutor_Kill(t *testing.T) {
	t.Parallel()

	e := &Executor{}
	err := e.Kill(os.Interrupt)
	assert.NoError(t, err)
}

func TestToLLMMessages(t *testing.T) {
	t.Parallel()

	t.Run("EmptySlice", func(t *testing.T) {
		t.Parallel()

		result := toLLMMessages(nil)
		assert.Empty(t, result)
	})

	t.Run("ConvertMessages", func(t *testing.T) {
		t.Parallel()

		msgs := []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "System prompt"},
			{Role: exec.RoleUser, Content: "User message"},
			{Role: exec.RoleAssistant, Content: "Assistant response"},
		}
		result := toLLMMessages(msgs)

		assert.Len(t, result, 3)
		assert.Equal(t, llmpkg.RoleSystem, result[0].Role)
		assert.Equal(t, "System prompt", result[0].Content)
		assert.Equal(t, llmpkg.RoleUser, result[1].Role)
		assert.Equal(t, "User message", result[1].Content)
		assert.Equal(t, llmpkg.RoleAssistant, result[2].Role)
		assert.Equal(t, "Assistant response", result[2].Content)
	})
}

func TestBuildMessageList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		stepMsgs        []exec.LLMMessage
		contextMsgs     []exec.LLMMessage
		wantFirstSystem string
		wantLen         int
	}{
		{
			name: "step system takes precedence",
			stepMsgs: []exec.LLMMessage{
				{Role: exec.RoleSystem, Content: "step system"},
				{Role: exec.RoleUser, Content: "user"},
			},
			contextMsgs: []exec.LLMMessage{
				{Role: exec.RoleSystem, Content: "context system"},
			},
			wantFirstSystem: "step system",
			wantLen:         2,
		},
		{
			name:     "context system used when step has none",
			stepMsgs: []exec.LLMMessage{{Role: exec.RoleUser, Content: "user"}},
			contextMsgs: []exec.LLMMessage{
				{Role: exec.RoleSystem, Content: "context system"},
			},
			wantFirstSystem: "context system",
			wantLen:         2,
		},
		{
			name: "no context",
			stepMsgs: []exec.LLMMessage{
				{Role: exec.RoleSystem, Content: "step system"},
			},
			contextMsgs:     nil,
			wantFirstSystem: "step system",
			wantLen:         1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := buildMessageList(tt.stepMsgs, tt.contextMsgs)

			require.Len(t, result, tt.wantLen)
			assert.Equal(t, exec.RoleSystem, result[0].Role)
			assert.Equal(t, tt.wantFirstSystem, result[0].Content)
		})
	}
}

func TestToThinkingRequest(t *testing.T) {
	t.Parallel()

	t.Run("NilConfig", func(t *testing.T) {
		t.Parallel()

		result := toThinkingRequest(nil)
		assert.Nil(t, result)
	})

	t.Run("WithConfig", func(t *testing.T) {
		t.Parallel()

		budget := 1024
		cfg := &core.ThinkingConfig{
			Enabled:         true,
			Effort:          core.ThinkingEffortHigh,
			BudgetTokens:    &budget,
			IncludeInOutput: true,
		}
		result := toThinkingRequest(cfg)

		require.NotNil(t, result)
		assert.True(t, result.Enabled)
		assert.Equal(t, llmpkg.ThinkingEffort("high"), result.Effort)
		assert.Equal(t, &budget, result.BudgetTokens)
		assert.True(t, result.IncludeInOutput)
	})

	t.Run("DefaultValues", func(t *testing.T) {
		t.Parallel()

		cfg := &core.ThinkingConfig{}
		result := toThinkingRequest(cfg)

		require.NotNil(t, result)
		assert.False(t, result.Enabled)
		assert.Empty(t, result.Effort)
		assert.Nil(t, result.BudgetTokens)
		assert.False(t, result.IncludeInOutput)
	})
}

type mockProvider struct {
	chatFunc       func(ctx context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error)
	chatStreamFunc func(ctx context.Context, req *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error)
}

func (m *mockProvider) Chat(ctx context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
	return m.chatFunc(ctx, req)
}

func (m *mockProvider) ChatStream(ctx context.Context, req *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
	return m.chatStreamFunc(ctx, req)
}

func (m *mockProvider) Name() string {
	return "mock"
}

func TestExecutor_RunSimpleForModel_RetriesTransientChatFailure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	stream := false
	cfg := &core.LLMConfig{Provider: "openai", Model: "gpt-4o", Stream: &stream}
	var stdout bytes.Buffer
	executor := &Executor{
		stdout: stdoutWriter(&stdout),
		step:   core.Step{LLM: cfg},
	}

	provider := &mockProvider{
		chatFunc: func(_ context.Context, _ *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
			if calls.Add(1) == 1 {
				return nil, llmpkg.WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF))
			}
			return &llmpkg.ChatResponse{
				Content:      "done",
				FinishReason: "stop",
				Usage:        llmpkg.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
			}, nil
		},
		chatStreamFunc: func(context.Context, *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
			ch := make(chan llmpkg.StreamEvent)
			close(ch)
			return ch, nil
		},
	}

	err := executor.runSimpleForModel(context.Background(), provider, []exec.LLMMessage{{Role: exec.RoleUser, Content: "hello"}}, cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())
	assert.Contains(t, stdout.String(), "done")
	require.Len(t, executor.savedMessages, 2)
	assert.Equal(t, "done", executor.savedMessages[1].Content)
}

func TestExecutor_RunSimpleForModel_RetriesStreamBeforeFirstDelta(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	stream := true
	cfg := &core.LLMConfig{Provider: "openai", Model: "gpt-4o", Stream: &stream}
	var stdout bytes.Buffer
	executor := &Executor{
		stdout: stdoutWriter(&stdout),
		step:   core.Step{LLM: cfg},
	}

	provider := &mockProvider{
		chatFunc: func(context.Context, *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
			return nil, fmt.Errorf("unexpected Chat call")
		},
		chatStreamFunc: func(_ context.Context, _ *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
			if calls.Add(1) == 1 {
				return nil, llmpkg.WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF))
			}
			ch := make(chan llmpkg.StreamEvent, 2)
			ch <- llmpkg.StreamEvent{Delta: "done"}
			ch <- llmpkg.StreamEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}

	err := executor.runSimpleForModel(context.Background(), provider, []exec.LLMMessage{{Role: exec.RoleUser, Content: "hello"}}, cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, "done\n", stdout.String())
}

func TestExecutor_RunSimpleForModel_DoesNotRetryStreamAfterDelta(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	stream := true
	cfg := &core.LLMConfig{Provider: "openai", Model: "gpt-4o", Stream: &stream}
	var stdout bytes.Buffer
	executor := &Executor{
		stdout: stdoutWriter(&stdout),
		step:   core.Step{LLM: cfg},
	}

	provider := &mockProvider{
		chatFunc: func(context.Context, *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
			return nil, fmt.Errorf("unexpected Chat call")
		},
		chatStreamFunc: func(_ context.Context, _ *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
			calls.Add(1)
			ch := make(chan llmpkg.StreamEvent, 2)
			ch <- llmpkg.StreamEvent{Delta: "partial"}
			ch <- llmpkg.StreamEvent{Error: llmpkg.WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF)), Done: true}
			close(ch)
			return ch, nil
		},
	}

	err := executor.runSimpleForModel(context.Background(), provider, []exec.LLMMessage{{Role: exec.RoleUser, Content: "hello"}}, cfg)
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, "partial", stdout.String())
}

func TestExecutor_ExecuteToolStep_RetriesTransientChatFailure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var stdout bytes.Buffer
	cfg := &core.LLMConfig{Provider: "openai", Model: "gpt-4o"}
	executor := &Executor{
		stdout: stdoutWriter(&stdout),
		step:   core.Step{LLM: cfg},
	}

	provider := &mockProvider{
		chatFunc: func(_ context.Context, _ *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
			if calls.Add(1) == 1 {
				return nil, llmpkg.WrapError("openrouter", fmt.Errorf("failed to decode response: %w", io.ErrUnexpectedEOF))
			}
			return &llmpkg.ChatResponse{
				Content:      "tool loop done",
				FinishReason: "stop",
				Usage:        llmpkg.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
			}, nil
		},
		chatStreamFunc: func(context.Context, *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
			ch := make(chan llmpkg.StreamEvent)
			close(ch)
			return ch, nil
		},
	}

	msgs, done, err := executor.executeToolStep(context.Background(), provider, cfg, nil, []exec.LLMMessage{{Role: exec.RoleUser, Content: "hello"}}, 0)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, int32(2), calls.Load())
	require.Len(t, msgs, 2)
	assert.Equal(t, "tool loop done", msgs[1].Content)
	assert.Contains(t, stdout.String(), "tool loop done")
}

func stdoutWriter(buf *bytes.Buffer) *bytes.Buffer {
	return buf
}

// createContextWithSecrets creates a test context with the given secrets.
func createContextWithSecrets(secrets map[string]string) context.Context {
	if secrets == nil {
		return context.Background()
	}
	secretEnvs := make([]string, 0, len(secrets))
	for k, v := range secrets {
		secretEnvs = append(secretEnvs, k+"="+v)
	}
	return exec.NewContext(context.Background(), &core.DAG{Name: "test"}, "run-1", "/tmp/log",
		exec.WithSecrets(secretEnvs))
}

func TestMaskSecretsForProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		secrets    map[string]string
		messages   []exec.LLMMessage
		wantMasked []string
	}{
		{
			name:    "no secrets in context",
			secrets: nil,
			messages: []exec.LLMMessage{
				{Role: exec.RoleUser, Content: "Hello"},
			},
			wantMasked: []string{"Hello"},
		},
		{
			name:    "empty secrets",
			secrets: map[string]string{},
			messages: []exec.LLMMessage{
				{Role: exec.RoleUser, Content: "Hello"},
			},
			wantMasked: []string{"Hello"},
		},
		{
			name:    "masks secret in content",
			secrets: map[string]string{"API_KEY": "secret123"},
			messages: []exec.LLMMessage{
				{Role: exec.RoleUser, Content: "My key is secret123"},
			},
			wantMasked: []string{"My key is *******"},
		},
		{
			name: "masks multiple secrets",
			secrets: map[string]string{
				"DB_PASS": "dbpass",
				"API_KEY": "apikey",
			},
			messages: []exec.LLMMessage{
				{Role: exec.RoleSystem, Content: "Use dbpass for DB"},
				{Role: exec.RoleUser, Content: "Key is apikey"},
			},
			wantMasked: []string{
				"Use ******* for DB",
				"Key is *******",
			},
		},
		{
			name:    "preserves role and metadata for multiple messages",
			secrets: map[string]string{"SECRET": "xyz"},
			messages: []exec.LLMMessage{
				{
					Role:     exec.RoleUser,
					Content:  "Value: xyz",
					Metadata: nil,
				},
				{
					Role:     exec.RoleAssistant,
					Content:  "Response with xyz",
					Metadata: &exec.LLMMessageMetadata{Model: "gpt-4", PromptTokens: 10, CompletionTokens: 5},
				},
				{
					Role:     exec.RoleUser,
					Content:  "Another xyz message",
					Metadata: &exec.LLMMessageMetadata{Provider: "openai"},
				},
			},
			wantMasked: []string{"Value: *******", "Response with *******", "Another ******* message"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := createContextWithSecrets(tt.secrets)
			result := maskSecretsForProvider(ctx, tt.messages)

			require.Len(t, result, len(tt.wantMasked))
			for i, want := range tt.wantMasked {
				assert.Equal(t, want, result[i].Content)
				assert.Equal(t, tt.messages[i].Role, result[i].Role)
				// Verify metadata is preserved for each message
				assert.Equal(t, tt.messages[i].Metadata, result[i].Metadata)
			}
		})
	}
}
