// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec090_agent_tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  struct {
				Properties map[string]struct {
					Description string   `json:"description"`
					Enum        []string `json:"enum"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

// toolNamesInOrder returns the names of the tools as the request lists them.
func (r chatRequest) toolNamesInOrder() []string {
	names := make([]string, 0, len(r.Tools))
	for _, tool := range r.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

func (r chatRequest) toolNames() []string {
	names := make([]string, 0, len(r.Tools))
	for _, tool := range r.Tools {
		names = append(names, tool.Function.Name)
	}
	slices.Sort(names)
	return names
}

// llm starts a local OpenAI-compatible server. The first request gets the tool
// call that call returns, when it is not empty; the next request gets a final
// answer. It records every request.
type llm struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []chatRequest
}

func newLLM(t *testing.T, call string) *llm {
	t.Helper()

	l := &llm{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		l.mu.Lock()
		l.requests = append(l.requests, req)
		count := len(l.requests)
		l.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if count == 1 && call != "" {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[` + call + `]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *llm) captured() []chatRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

func toolCall(name, arguments string) string {
	quoted, _ := json.Marshal(arguments)
	return `{"id":"call_1","type":"function","function":{"name":"` + name + `","arguments":` + string(quoted) + `}}`
}

func env(l *llm, extra ...string) []string {
	return append([]string{"LLM_BASE_URL=" + l.srv.URL, "TIER=free", "CUSTOMER=c-42", "MCP_URL=http://127.0.0.1:1"}, extra...)
}

// A DAG with no description gets the description "Run the DAG <name>.".
func TestDAGWithoutDescriptionGetsADefault(t *testing.T) {
	t.Parallel()

	l := newLLM(t, "")
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "tools_default_description.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 1)
	require.Equal(t, []string{"silent"}, requests[0].toolNames())
	require.Equal(t, "Run the DAG silent.", requests[0].Tools[0].Function.Description)
}

// An input that the workflow fixes is hidden from the model, and Dagu applies
// it. An input marked from_ai keeps the description that the author wrote.
func TestFixedInputIsHiddenAndApplied(t *testing.T) {
	t.Parallel()

	l := newLLM(t, toolCall("refund", `{"AMOUNT":"5"}`))
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "tools_params.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 2)
	require.Len(t, requests[0].Tools, 1)
	parameters := requests[0].Tools[0].Function.Parameters
	require.NotContains(t, parameters.Properties, "CUSTOMER_ID")
	require.Contains(t, parameters.Properties, "AMOUNT")
	require.Equal(t, "Amount to refund, in euros", parameters.Properties["AMOUNT"].Description)

	last := requests[1].Messages[len(requests[1].Messages)-1]
	require.Equal(t, "tool", last.Role)
	require.JSONEq(t, `{"RESULT":"refund 5 to c-42"}`, last.Content)
}

// A model argument that names an input that the tool does not show, such as a
// fixed input, gives an error result to the model. No child run starts, and the
// step goes on.
func TestHiddenInputArgumentIsAnErrorResult(t *testing.T) {
	t.Parallel()

	l := newLLM(t, toolCall("refund", `{"CUSTOMER_ID":"someone-else","AMOUNT":"5"}`))
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "tools_params.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 2)
	last := requests[1].Messages[len(requests[1].Messages)-1]
	require.Equal(t, "tool", last.Role)
	require.Contains(t, last.Content, `tool "refund" has no input "CUSTOMER_ID"`)
}

// An MCP entry offers the allowed tools of an MCP server under the names
// <name>_<tool>, and a call reaches the server with the configured header.
func TestMCPToolsAreOfferedAndCalled(t *testing.T) {
	t.Parallel()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "v0"}, nil)
	type echoIn struct {
		Text string `json:"text" jsonschema:"text to echo"`
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "echo", Description: "Echo the text"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoIn) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "secret", Description: "Not allowed"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ echoIn) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{}, nil, nil
		})
	var (
		mu      sync.Mutex
		headers []string
	)
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	l := newLLM(t, toolCall("docs_echo", `{"text":"hello"}`))
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l, "MCP_URL="+httpServer.URL), "start", "tools_mcp.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 2)
	require.Equal(t, []string{"docs_echo"}, requests[0].toolNames())
	last := requests[1].Messages[len(requests[1].Messages)-1]
	require.Equal(t, "tool", last.Role)
	require.Equal(t, "echo: hello", last.Content)

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, headers, "Bearer test-token")
}

// max_tools bounds the effective catalog. A larger catalog fails the step and
// the model receives no request.
func TestMaxToolsFailsTheStep(t *testing.T) {
	t.Parallel()

	l := newLLM(t, "")
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "tools_max_tools.yaml")
	require.NotEqual(t, 0, result.ExitCode())
	require.Contains(t, result.Stdout()+result.Stderr(), "2 tools, above max_tools 1")
	require.Empty(t, l.captured())
}

// tool_result_max_bytes bounds the result that goes back to the model.
func TestToolResultLimit(t *testing.T) {
	t.Parallel()

	l := newLLM(t, toolCall("lookup", `{"CUSTOMER_ID":"c-1"}`))
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "tools_result_limit.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 2)
	last := requests[1].Messages[len(requests[1].Messages)-1]
	require.LessOrEqual(t, len(last.Content), 60)
	require.Contains(t, last.Content, "[truncated: ")
}

// dagu validate rejects the entries that break the closed schema of the tool
// map, such as an unknown category or an unknown key on an entry. The error
// names the path of the key and the valid keys.
func TestValidateRejectsBadToolMaps(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture  string
		messages []string
	}{
		{"invalid_mcp_without_allow.yaml", []string{`llm.tools.mcp[0].allow`, `allow is required`}},
		{"invalid_tools_as_list.yaml", []string{`llm.tools[0]`, `must be the name of a DAG`}},
		{"invalid_tools_unknown_category.yaml", []string{`llm.tools.widgets`, `"widgets"`, `valid keys are mcp`}},
		{"invalid_dags_in_tool_map.yaml", []string{`llm.tools.dags`, `unknown key "dags" in the tool map; valid keys are mcp`}},
		{"invalid_dags_with_tools_list.yaml", []string{`llm.dags`, `key "dags" and key "tools" contradict each other`}},
		{"invalid_tool_entry_unknown_key.yaml", []string{`llm.dags[0].retries`, `"retries"`, `name, params`}},
		{"invalid_mcp_entry_unknown_key.yaml", []string{`llm.tools.mcp[0].retries`, `"retries"`, `name, url, headers, allow`}},
		{"invalid_tool_entry_name_missing.yaml", []string{`llm.dags[0].name`, `name is required`}},
		{"invalid_builtin_on_chat.yaml", []string{`llm.tools.builtin`, `valid only on an agent.run step`}},
		{"invalid_mcp_named_dagu.yaml", []string{`llm.tools.mcp[0].name`, `name of the tool bridge`}},
		{"invalid_mcp_duplicate_name.yaml", []string{`llm.tools.mcp[1].name`, `mcp name "docs" is also the name of llm.tools.mcp[0]`}},
		{"invalid_tool_name_too_long.yaml", []string{`llm.dags[0].name`, `above the limit of 64`}},
		{"invalid_tool_named_load_skill.yaml", []string{`llm.dags[0].name`, `reserved for the tool that loads a skill`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			require.NotEqual(t, 0, result.ExitCode())
			output := result.Stdout() + result.Stderr()
			for _, message := range tc.messages {
				require.Contains(t, output, message)
			}
		})
	}
}

// The list form, a list of DAG names, stays valid. It is deprecated:
// dagu validate accepts it with exit code 0 and says so in a warning.
func TestValidateAcceptsTheDeprecatedToolsList(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "tools_list_form.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout()+result.Stderr(), "The list form of `tools` is deprecated. Use the key dags.")
}

// Two entries that give the same tool name are a validation error that names
// both paths. A character outside [A-Za-z0-9_-] counts as an underscore.
func TestValidateRejectsADuplicateToolName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture  string
		messages []string
	}{
		{"invalid_duplicate_tool_name.yaml", []string{
			`llm.dags[1]`, `tool name "lookup" is also the tool name of llm.dags[0]`}},
		{"invalid_duplicate_tool_name_sanitized.yaml", []string{
			`llm.dags[1]`, `tool name "ops_notify" is also the tool name of llm.dags[0]`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			require.NotEqual(t, 0, result.ExitCode())
			output := result.Stdout() + result.Stderr()
			for _, message := range tc.messages {
				require.Contains(t, output, message)
			}
		})
	}
}

// The MCP tools of a server are known only when the step starts. A tool name
// that another tool has fails the step, and the error names both sources. The
// model receives no request.
func TestMCPToolNameClashFailsTheStep(t *testing.T) {
	t.Parallel()

	httpServer := newMCPEchoServer(t)
	l := newLLM(t, "")
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l, "MCP_URL="+httpServer.URL), "start", "tools_mcp_name_clash.yaml")
	require.NotEqual(t, 0, result.ExitCode())
	require.Contains(t, result.Stdout()+result.Stderr(), `tool name "docs_echo" of mcp:docs is also the tool name of dag:docs_echo`)
	require.Empty(t, l.captured())
}

// message returns the text of the first message that has the role, or "".
func (r chatRequest) message(role string) string {
	for _, m := range r.Messages {
		if m.Role == role {
			return m.Content
		}
	}
	return ""
}

// newMCPEchoServer starts an MCP server over HTTP with the tool echo.
func newMCPEchoServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "v0"}, nil)
	type echoIn struct {
		Text string `json:"text" jsonschema:"text to echo"`
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "echo", Description: "Echo the text"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoIn) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	httpServer := httptest.NewServer(mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil))
	t.Cleanup(httpServer.Close)
	return httpServer
}
