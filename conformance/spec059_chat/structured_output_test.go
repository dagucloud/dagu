// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec059_chat_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// A chat step with output_schema asks for the answer through a forced
// respond tool, publishes the listed properties as step outputs, and corrects
// an unusable answer once before failing without exposing it.
func TestStructuredOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		replies []string
		// answer is the step's stdout; empty when the step fails.
		answer string
		// result is what a dependent step prints from the outputs.
		result string
	}{
		{
			name:    "RespondTool",
			replies: []string{respondReply(`{"category":"refund","amount":12.5,"note":"dropped"}`)},
			answer:  `{"amount":12.5,"category":"refund"}`,
			result:  "refund 12.5",
		},
		{
			name:    "TextAnswer",
			replies: []string{textReply("```json\n{\"category\":\"question\",\"amount\":0}\n```")},
			answer:  `{"amount":0,"category":"question"}`,
			result:  "question 0",
		},
		{
			name: "Correction",
			replies: []string{
				respondReply(`{"category":"praise","amount":1}`),
				respondReply(`{"category":"complaint","amount":3}`),
			},
			answer: `{"amount":3,"category":"complaint"}`,
			result: "complaint 3",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			model := newScriptedModel(t, map[string][]string{"test-model": tc.replies})
			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + model.URL()}, "start", "structured_output.yaml")
			requests := model.Close()
			result.ExpectExitCode(0)

			dagu.ExpectFileContent("answer.out", tc.answer+"\n")
			dagu.ExpectTextFileContent("result.out", tc.result+"\n")

			require.Len(t, requests, len(tc.replies))
			first := requests[0]
			require.False(t, first.Stream)
			require.Equal(t, "required", first.ToolChoice)
			require.Len(t, first.Tools, 1)
			require.Equal(t, "respond", first.Tools[0].Function.Name)
			require.JSONEq(t, `{
				"type":"object",
				"properties":{
					"category":{"type":"string","enum":["refund","complaint","question"]},
					"amount":{"type":"number"}
				},
				"required":["category","amount"]
			}`, string(first.Tools[0].Function.Parameters))
			require.Equal(t, "system", first.Messages[0].Role)
			require.Contains(t, first.Messages[0].Content, "respond tool")

			if len(requests) > 1 {
				// The rejected answer returns as plain text, then the reason.
				second := requests[1].Messages
				require.Len(t, second, 4)
				require.Equal(t, "assistant", second[2].Role)
				require.JSONEq(t, `{"category":"praise","amount":1}`, second[2].Content)
				require.Empty(t, second[2].ToolCalls)
				require.Equal(t, "user", second[3].Role)
				require.Contains(t, second[3].Content, "respond tool again")
			}
		})
	}
}

// An answer that is still unusable after the correction fails the step with
// an error that names no part of the answer; the step's stderr holds the
// rejected answers and the reasons.
func TestStructuredOutputRejected(t *testing.T) {
	t.Parallel()

	invalid := respondReply(`{"category":"secret-praise","amount":1}`)
	model := newScriptedModel(t, map[string][]string{"test-model": {invalid, invalid}})
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + model.URL()}, "start", "structured_output.yaml")
	requests := model.Close()
	result.ExpectNonZeroExitCode()
	require.Len(t, requests, 2)
	// The run summary wraps long lines inside a tree drawn with box characters.
	summary := strings.Join(strings.Fields(strings.ReplaceAll(result.Stdout(), "│", " ")), " ")
	require.Contains(t, summary, "error: local/test-model: the model gave no answer that matches output_schema")

	dagu.ExpectFileContains("rejected.err", "answer rejected", "enum", `{"category":"secret-praise","amount":1}`)
	dagu.ExpectNoFile("result.out")
}

// A model that cannot answer after its correction gives way to the next
// model in the list.
func TestStructuredOutputFallback(t *testing.T) {
	t.Parallel()

	model := newScriptedModel(t, map[string][]string{
		"weak-model":   {textReply("no idea"), textReply("still no idea")},
		"strong-model": {respondReply(`{"category":"refund"}`)},
	})
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + model.URL()}, "start", "structured_output_fallback.yaml")
	requests := model.Close()
	result.ExpectExitCode(0)

	dagu.ExpectFileContent("answer.out", `{"category":"refund"}`+"\n")
	var models []string
	for _, req := range requests {
		models = append(models, req.Model)
	}
	require.Equal(t, []string{"weak-model", "weak-model", "strong-model"}, models)
}

// Tool DAGs are offered next to the respond tool, and the loop ends when
// the model answers through respond.
func TestStructuredOutputWithTools(t *testing.T) {
	t.Parallel()

	model := newScriptedModel(t, map[string][]string{"test-model": {
		toolCallReply("get-weather", `{"CITY":"Paris"}`),
		respondReply(`{"city":"Paris","celsius":22}`),
	}})
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + model.URL()}, "start", "structured_output_tools.yaml")
	requests := model.Close()
	result.ExpectExitCode(0)

	dagu.ExpectFileContent("answer.out", `{"celsius":22,"city":"Paris"}`+"\n")
	dagu.ExpectTextFileContent("result.out", "Paris 22\n")

	require.Len(t, requests, 2)
	var tools []string
	for _, tool := range requests[0].Tools {
		tools = append(tools, tool.Function.Name)
	}
	require.Equal(t, []string{"get-weather", "respond"}, tools)
	require.Equal(t, "required", requests[0].ToolChoice)
	toolResult := requests[1].Messages[len(requests[1].Messages)-1]
	require.Equal(t, "tool", toolResult.Role)
	require.Contains(t, toolResult.Content, "Paris")
}

// Without an answer through respond, a tool loop that reaches its limit
// fails the step instead of ending with the last text.
func TestStructuredOutputToolLimit(t *testing.T) {
	t.Parallel()

	call := toolCallReply("loop-tool", `{}`)
	model := newScriptedModel(t, map[string][]string{"test-model": {call, call, call}})
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + model.URL()}, "start", "structured_output_tool_limit.yaml")
	requests := model.Close()
	result.ExpectNonZeroExitCode()
	require.Len(t, requests, 2)
}

// scriptedModel serves an OpenAI-compatible chat completions endpoint that
// answers each model's requests with its scripted replies in order.
type scriptedModel struct {
	server   *httptest.Server
	mu       sync.Mutex
	replies  map[string][]string
	requests []structuredRequest
}

func newScriptedModel(t *testing.T, replies map[string][]string) *scriptedModel {
	t.Helper()
	m := &scriptedModel{replies: replies}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		var req structuredRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.requests = append(m.requests, req)
		pending := m.replies[req.Model]
		if len(pending) == 0 {
			t.Errorf("unexpected request for %s", req.Model)
			http.Error(w, "no scripted reply left", http.StatusBadRequest)
			return
		}
		m.replies[req.Model] = pending[1:]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pending[0]))
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *scriptedModel) URL() string {
	return m.server.URL
}

// Close waits for in-flight requests and returns every request received.
func (m *scriptedModel) Close() []structuredRequest {
	m.server.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

type structuredRequest struct {
	Model      string              `json:"model"`
	Stream     bool                `json:"stream"`
	ToolChoice string              `json:"tool_choice"`
	Tools      []structuredTool    `json:"tools"`
	Messages   []structuredMessage `json:"messages"`
}

type structuredTool struct {
	Function struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type structuredMessage struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolCalls []json.RawMessage `json:"tool_calls"`
}

// respondReply is a model turn that answers through the respond tool.
func respondReply(arguments string) string {
	return toolCallReply("respond", arguments)
}

// toolCallReply is a model turn that calls one tool with arguments.
func toolCallReply(name, arguments string) string {
	return chatReply(map[string]any{
		"role":    "assistant",
		"content": "",
		"tool_calls": []any{map[string]any{
			"id":       "call_" + name,
			"type":     "function",
			"function": map[string]any{"name": name, "arguments": arguments},
		}},
	}, "tool_calls")
}

// textReply is a model turn that answers in text.
func textReply(content string) string {
	return chatReply(map[string]any{"role": "assistant", "content": content}, "stop")
}

func chatReply(message map[string]any, finishReason string) string {
	body, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": message, "finish_reason": finishReason}},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}
