// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec059_chat holds black-box conformance tests for chat.completion.
package spec059_chat_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestChatCompletion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture  string
		models   []string
		messages []chatMessage
		stream   bool
	}{
		{"basic_prompt.yaml", []string{"test-model"}, []chatMessage{{Role: "user", Content: "hello"}}, true},
		{"stream_false.yaml", []string{"test-model"}, []chatMessage{{Role: "system", Content: "Be concise"}, {Role: "user", Content: "hello"}}, false},
		{"model_fallback.yaml", []string{"bad-model", "good-model"}, []chatMessage{{Role: "user", Content: "hello"}}, false},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			requests := make(chan chatRequest, len(tc.models))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
					http.Error(w, "unexpected endpoint", http.StatusNotFound)
					return
				}

				var req chatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				select {
				case requests <- req:
				default:
					t.Error("unexpected extra request")
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}

				if req.Model == "bad-model" {
					http.Error(w, "model unavailable", http.StatusBadRequest)
					return
				}

				body := `{"choices":[{"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop"}]}`
				w.Header().Set("Content-Type", "application/json")
				if req.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					body = "data: {\"choices\":[{\"delta\":{\"content\":\"Hello world\"}}]}\n\ndata: [DONE]\n\n"
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			t.Cleanup(srv.Close)

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + srv.URL}, "start", tc.fixture)
			// Closing waits for handlers before inspecting their captured requests.
			srv.Close()
			close(requests)
			result.ExpectExitCode(0)

			data, err := os.ReadFile(dagu.ProjectPath("result.out"))
			require.NoError(t, err)
			require.Equal(t, "Hello world\n", string(data))

			var captured []chatRequest
			for req := range requests {
				captured = append(captured, req)
			}
			require.Len(t, captured, len(tc.models))
			for i, req := range captured {
				require.Equal(t, tc.models[i], req.Model)
				require.Equal(t, tc.messages, req.Messages)
				require.Equal(t, tc.stream, req.Stream)
			}
		})
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func TestChatConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		message string
	}{
		{"provider_without_model.yaml", "model must be specified"},
		{"neither_prompt_nor_messages.yaml", "requires with.prompt or with.messages"},
		{"structured_output_no_type.yaml", "must declare type: object"},
		{"structured_output_no_properties.yaml", "must list at least one property"},
		{"structured_output_required_unlisted.yaml", "must be listed in properties"},
		{"structured_output_web_search.yaml", "web search cannot be combined with output_schema"},
		{"structured_output_respond_tool.yaml", `"respond" is reserved`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
