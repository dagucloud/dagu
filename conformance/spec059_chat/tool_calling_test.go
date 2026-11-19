// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec059_chat_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// with.dags references another DAG as a callable function: the LLM is
// offered its params as a JSON schema, a requested call runs it as a real
// sub-DAG with the LLM's arguments as DAG params, and its declared outputs
// are JSON-encoded back to the LLM as the tool result.
func TestToolCallingBasic(t *testing.T) {
	t.Parallel()

	toolForms(t, "tool_calling_basic.yaml", func(t *testing.T, fixture string) {
		requests := make(chan toolChatRequest, 2)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req toolChatRequest
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

			w.Header().Set("Content-Type", "application/json")
			if len(req.Messages) == 1 {
				// First turn: request the tool call.
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"",
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get-weather","arguments":"{\"CITY\":\"Paris\"}"}}]},
					"finish_reason":"tool_calls"}]}`))
				return
			}
			// Second turn: final answer using the tool result.
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"It is 22C in Paris."},"finish_reason":"stop"}]}`))
		}))
		t.Cleanup(srv.Close)

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + srv.URL}, "start", fixture)
		srv.Close()
		close(requests)
		result.ExpectExitCode(0)

		data, err := os.ReadFile(dagu.ProjectPath("result.out"))
		require.NoError(t, err)
		require.Equal(t, "It is 22C in Paris.\n", string(data))

		var captured []toolChatRequest
		for req := range requests {
			captured = append(captured, req)
		}
		require.Len(t, captured, 2)

		// First request offers the tool DAG's params as a JSON schema.
		require.Len(t, captured[0].Tools, 1)
		require.Equal(t, "get-weather", captured[0].Tools[0].Function.Name)
		require.Equal(t, []string{"CITY"}, captured[0].Tools[0].Function.Parameters.Required)

		// Second request carries the assistant tool call and the tool's result,
		// which is the sub-DAG's declared output JSON-encoded as the tool content.
		require.Equal(t, captured[0].Tools, captured[1].Tools)
		require.Len(t, captured[1].Messages, 3)
		toolMsg := captured[1].Messages[2]
		require.Equal(t, "tool", toolMsg.Role)
		require.Equal(t, "call_1", toolMsg.ToolCallID)
		require.JSONEq(t, `{"WEATHER_JSON":"{\"temp_c\": 22, \"city\": \"Paris\"}"}`, toolMsg.Content)
	})
}

// A tool call naming a DAG outside with.dags is a non-fatal error: its
// result content tells the LLM the tool was not found, and the loop
// continues to a final response rather than failing the run.
func TestUnknownTool(t *testing.T) {
	t.Parallel()

	toolForms(t, "tool_calling_unknown_tool.yaml", func(t *testing.T, fixture string) {
		var requestCount atomic.Int32
		var secondRequest toolChatRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if count == 1 {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"",
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"not-a-real-tool","arguments":"{}"}}]},
					"finish_reason":"tool_calls"}]}`))
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&secondRequest); err != nil {
				t.Errorf("decode request: %v", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"tool was unavailable"},"finish_reason":"stop"}]}`))
		}))
		t.Cleanup(srv.Close)

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + srv.URL}, "start", fixture)
		srv.Close()
		result.ExpectExitCode(0)

		data, err := os.ReadFile(dagu.ProjectPath("result.out"))
		require.NoError(t, err)
		require.Equal(t, "tool was unavailable\n", string(data))

		require.EqualValues(t, 2, requestCount.Load())
		require.Len(t, secondRequest.Messages, 3)
		toolMsg := secondRequest.Messages[2]
		require.Equal(t, "tool", toolMsg.Role)
		require.Equal(t, `Error: tool "not-a-real-tool" not found`, toolMsg.Content)
	})
}

// A tool DAG that itself fails is also non-fatal: the failure is reported as
// the tool's result content, and the loop continues.
func TestFailedTool(t *testing.T) {
	t.Parallel()

	toolForms(t, "tool_calling_failed_tool.yaml", func(t *testing.T, fixture string) {
		var requestCount atomic.Int32
		var secondRequest toolChatRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if count == 1 {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"",
					"tool_calls":[{"id":"call_1","type":"function","function":{"name":"broken-tool","arguments":"{}"}}]},
					"finish_reason":"tool_calls"}]}`))
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&secondRequest); err != nil {
				t.Errorf("decode request: %v", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"the tool failed"},"finish_reason":"stop"}]}`))
		}))
		t.Cleanup(srv.Close)

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + srv.URL}, "start", fixture)
		srv.Close()
		result.ExpectExitCode(0)

		data, err := os.ReadFile(dagu.ProjectPath("result.out"))
		require.NoError(t, err)
		require.Equal(t, "the tool failed\n", string(data))

		require.EqualValues(t, 2, requestCount.Load())
		require.Len(t, secondRequest.Messages, 3)
		toolMsg := secondRequest.Messages[2]
		require.Equal(t, "tool", toolMsg.Role)
		require.Equal(t, "Error: execution failed: exit status 1", toolMsg.Content)
	})
}

// max_tool_iterations bounds the tool loop: reaching it still succeeds the
// step (it is not a failure), stopping after exactly that many requests.
// With no override, the default is 10. The subtest "list form" runs the
// fixtures of the released list form of tools, which is deprecated, with no
// change.
func TestToolIterationLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture       string
		maxIterations int
	}{
		{"tool_calling_default_max_iterations.yaml", 10},
		{"tool_calling_custom_max_iterations.yaml", 3},
	} {
		toolForms(t, tc.fixture, func(t *testing.T, fixture string) {
			var requestCount atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requestCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				// Unknown calls keep the request loop active without spawning tool DAGs.
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"still working",
					"tool_calls":[{"id":"call_X","type":"function","function":{"name":"unknown-tool","arguments":"{}"}}]},
					"finish_reason":"tool_calls"}]}`))
			}))
			t.Cleanup(srv.Close)

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + srv.URL}, "start", fixture)
			srv.Close()
			result.ExpectExitCode(0)

			require.EqualValues(t, tc.maxIterations, requestCount.Load())

			data, err := os.ReadFile(dagu.ProjectPath("result.out"))
			require.NoError(t, err)
			require.Equal(t, "still working\n", string(data))
		})
	}
}

// toolForms runs the body once for each way to list the DAG tools. The fixture
// with no suffix has the list form of with.tools, which is deprecated. The
// fixture with the suffix _dags has the key with.dags.
func toolForms(t *testing.T, fixture string, body func(t *testing.T, fixture string)) {
	t.Helper()
	for _, form := range []struct{ name, fixture string }{
		{"list form", fixture},
		{"dags key", strings.TrimSuffix(fixture, ".yaml") + "_dags.yaml"},
	} {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			body(t, form.fixture)
		})
	}
}

type toolChatRequest struct {
	Model      string             `json:"model"`
	Messages   []toolChatMessage  `json:"messages"`
	Tools      []toolChatToolSpec `json:"tools"`
	ToolChoice string             `json:"tool_choice"`
}

type toolChatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
}

type toolChatToolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name       string `json:"name"`
		Parameters struct {
			Required []string `json:"required"`
		} `json:"parameters"`
	} `json:"function"`
}
