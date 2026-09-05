// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec059_chat holds black-box conformance tests for
// Spec 059: Chat Completion Action.
package spec059_chat_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionLive(t *testing.T) {
	t.Run("with.prompt sends a single user message and writes the response to stdout", func(t *testing.T) {
		t.Parallel()

		srv := newMockLLMServer(t, func(_ int, _ mockRequest) mockResponse {
			return mockResponse{Content: "Hello world"}
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{srv.BaseURLEnv()}, "start", "basic_prompt.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "Hello world\n", lastStepStdout(t, result.Stdout()))

		reqs := srv.Requests()
		require.Len(t, reqs, 1)
		require.Equal(t, []mockMessage{{Role: "user", Content: "hello"}}, reqs[0].Messages)
	})

	t.Run("with.system is sent as the first message, ahead of with.messages", func(t *testing.T) {
		t.Parallel()

		srv := newMockLLMServer(t, func(_ int, _ mockRequest) mockResponse {
			return mockResponse{Content: "ack"}
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{srv.BaseURLEnv()}, "start", "with_messages_and_system.yaml")
		result.ExpectExitCode(0)

		reqs := srv.Requests()
		require.Len(t, reqs, 1)
		require.Equal(t, []mockMessage{
			{Role: "system", Content: "You are a helpful assistant"},
			{Role: "user", Content: "hi there"},
		}, reqs[0].Messages)
	})

	t.Run("with.stream: false sends a non-streaming request and still writes the full response", func(t *testing.T) {
		t.Parallel()

		srv := newMockLLMServer(t, func(_ int, _ mockRequest) mockResponse {
			return mockResponse{Content: "Hello world"}
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{srv.BaseURLEnv()}, "start", "stream_false.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "Hello world\n", lastStepStdout(t, result.Stdout()))

		reqs := srv.Requests()
		require.Len(t, reqs, 1)
		require.False(t, reqs[0].Stream)
	})

	t.Run("with.temperature/with.max_tokens/with.top_p are forwarded on the request", func(t *testing.T) {
		t.Parallel()

		srv := newMockLLMServer(t, func(_ int, _ mockRequest) mockResponse {
			return mockResponse{Content: "ack"}
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{srv.BaseURLEnv()}, "start", "params_passthrough.yaml")
		result.ExpectExitCode(0)

		reqs := srv.Requests()
		require.Len(t, reqs, 1)
		require.NotNil(t, reqs[0].Temperature)
		require.InDelta(t, 0.25, *reqs[0].Temperature, 0.0001)
		require.NotNil(t, reqs[0].MaxTokens)
		require.Equal(t, 128, *reqs[0].MaxTokens)
		require.NotNil(t, reqs[0].TopP)
		require.InDelta(t, 0.5, *reqs[0].TopP, 0.0001)
	})

	// Fallback tries each configured model in order, disabling streaming
	// for the whole request (see runWithModel), until one succeeds.
	t.Run("with.model as an array falls back to the next model when the first fails", func(t *testing.T) {
		t.Parallel()

		srv := newMockLLMServer(t, func(_ int, req mockRequest) mockResponse {
			if req.Model == "bad-model" {
				return mockResponse{StatusCode: 400}
			}
			return mockResponse{Content: "from good model"}
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{srv.BaseURLEnv()}, "start", "model_fallback.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "from good model\n", lastStepStdout(t, result.Stdout()))

		reqs := srv.Requests()
		require.Len(t, reqs, 2)
		require.Equal(t, "bad-model", reqs[0].Model)
		require.Equal(t, "good-model", reqs[1].Model)
	})

	// Tool calling always uses a non-streaming request (executeToolStep
	// calls ChatWithRetry, never ChatStream), regardless of with.stream.
	//
	// The respond callback runs on the mock server's own per-request
	// goroutine, so it must not make test assertions there: a failed
	// require/assert call there would abort the goroutine mid-response
	// (net/http recovers the panic and the client sees a bare EOF),
	// which starves the real request of a response instead of failing
	// the test cleanly. All assertions happen below, against the
	// requests srv.Requests() captured.
	t.Run("with.tools runs the named tool DAG and feeds its output back for a final response", func(t *testing.T) {
		t.Parallel()

		srv := newMockLLMServer(t, func(n int, _ mockRequest) mockResponse {
			if n == 0 {
				return mockResponse{ToolCalls: []mockToolCall{
					{ID: "call-1", Name: "echo-tool", Arguments: `{"MESSAGE":"ping"}`},
				}}
			}
			return mockResponse{Content: "done"}
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{srv.BaseURLEnv()}, "start", "tool_call_roundtrip.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "done\n", lastStepStdout(t, result.Stdout()))

		reqs := srv.Requests()
		require.Len(t, reqs, 2)
		require.Len(t, reqs[0].Tools, 1)
		require.Equal(t, "echo-tool", reqs[0].Tools[0].Function.Name)

		require.False(t, reqs[1].Stream, "tool-calling requests are never streamed")
		last := reqs[1].Messages[len(reqs[1].Messages)-1]
		require.Equal(t, "tool", last.Role)
		require.JSONEq(t, `{"RESULT":"got ping"}`, last.Content)
	})
}

// TestChatCompletionValidation proves that chat.completion's step-level llm
// configuration is validated at DAG-build time by dedicated Go code (not
// just the registered JSON Schema) whenever any llm field is present --
// except omitting the llm configuration entirely, which passes validate and
// fails only when the step runs, since chat.completion has no registered
// step validator to catch it.
func TestChatCompletionValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		fixture  string
		contains string
	}{
		{"an unrecognized with.provider", "invalid_provider.yaml", "invalid provider"},
		{"with.provider set without with.model", "provider_without_model.yaml", "model must be specified when llm config is provided"},
		{"with.temperature out of range", "temperature_out_of_range.yaml", "temperature must be between 0.0 and 2.0"},
		{"with.max_tokens below 1", "max_tokens_less_than_one.yaml", "max_tokens must be at least 1"},
		{"with.top_p out of range", "top_p_out_of_range.yaml", "top_p must be between 0.0 and 1.0"},
		{"neither with.prompt nor with.messages", "neither_prompt_nor_messages.yaml", "chat.completion requires with.prompt or with.messages"},
	}

	for _, tc := range cases {
		t.Run(tc.name+" fails validate", func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.contains)
		})
	}

	t.Run("no llm configuration at all fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "missing_llm_config.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "missing_llm_config.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("llm configuration is required for chat step")
	})
}
