// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec098_mcp_call_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	token      = "conformance-token"
	chartImage = "fake png bytes"
)

type mcpServer struct {
	url     string
	explode atomic.Int32
}

// newMCPServer serves a Streamable HTTP MCP endpoint that requires the
// bearer token. The scrape tool echoes the token so masking is observable.
func newMCPServer(t *testing.T) *mcpServer {
	t.Helper()
	s := &mcpServer{}
	server := sdk.NewServer(&sdk.Implementation{Name: "conformance", Version: "1"}, nil)
	schema := map[string]any{"type": "object"}
	server.AddTool(&sdk.Tool{Name: "scrape", InputSchema: schema},
		func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{
				Content:           []sdk.Content{&sdk.TextContent{Text: "status of " + string(req.Params.Arguments) + " via " + token}},
				StructuredContent: map[string]any{"pages": 3},
			}, nil
		})
	server.AddTool(&sdk.Tool{Name: "chart", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: `{"title":"Weekly sales"}`},
				&sdk.ImageContent{Data: []byte(chartImage), MIMEType: "image/png"},
			}}, nil
		})
	server.AddTool(&sdk.Tool{Name: "explode", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			s.explode.Add(1)
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "bad input"}}}, nil
		})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// A url taken from a reference loads, the result is readable by a later
// step, and a credential echoed by the tool is masked while an ordinary
// header value is not.
func TestMCPCall(t *testing.T) {
	t.Parallel()
	server := newMCPServer(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"MCP_URL=" + server.url}, "start", "call.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("call.out", `"structured_content":{"pages":3}`, `"is_error":false`, "status of", "***")
	dagu.ExpectFileNotContains("call.out", token)
	dagu.ExpectFileContains("result.out", "pages=3 err=false")
}

// JSON a tool returns as text is readable by path, and save_binary writes
// image data to a run artifact that a later step reads by its path.
func TestMCPCallJSONAndBinary(t *testing.T) {
	t.Parallel()
	server := newMCPServer(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"MCP_URL=" + server.url}, "start", "json_binary.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("chart.out", `"path":"mcp/chart/`, `/content-1.png"`)
	dagu.ExpectFileNotContains("chart.out", `"data"`)
	dagu.ExpectFileContains("result.out", "title=Weekly sales")
	dagu.ExpectFileContains("image.out", chartImage)
}

// A tool that reports isError fails the step, writes the normal output, and
// runs once.
func TestMCPCallToolError(t *testing.T) {
	t.Parallel()
	server := newMCPServer(t)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"MCP_URL=" + server.url}, "start", "tool_error.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("mcp.call: tool error")
	dagu.ExpectFileContains("error.out", `"is_error":true`, "bad input")
	if got := server.explode.Load(); got != 1 {
		t.Fatalf("tool ran %d times, want 1", got)
	}
}

func TestMCPCallConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, message string }{
		{"unknown_field.yaml", "transport"},
		{"userinfo.yaml", "must not contain credentials"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
