// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const mcpToken = "mcp-intg-token"

func newMCPServer(t *testing.T) string {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "intg", Version: "1"}, nil)
	schema := map[string]any{"type": "object"}
	server.AddTool(&sdk.Tool{Name: "scrape", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{
				Content:           []sdk.Content{&sdk.TextContent{Text: "# Example"}},
				StructuredContent: map[string]any{"pages": 3},
			}, nil
		})
	server.AddTool(&sdk.Tool{Name: "explode", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "bad"}}}, nil
		})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mcpToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func mcpDAG(t *testing.T, th test.Helper, url, tool string) test.DAG {
	t.Helper()
	secretFile := th.TempFile(t, "mcp-token.txt", []byte(mcpToken))
	return th.DAG(t, `
secrets:
  - name: MCP_TOKEN
    provider: file
    key: `+secretFile+`

steps:
  - id: call
    action: mcp.call
    with:
      url: `+url+`
      tool: `+tool+`
      arguments:
        url: https://example.com
      headers:
        Authorization: Bearer ${MCP_TOKEN}
    timeout_sec: 30
    output: PAGE

  - id: consume
    depends: call
    run: echo "${PAGE.content} pages=${PAGE.structured_content.pages} err=${PAGE.is_error}"
    output: RESULT
`)
}

func TestMCPCall(t *testing.T) {
	t.Parallel()

	t.Run("OutputFeedsNextStep", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		dag := mcpDAG(t, th, newMCPServer(t), "scrape")

		dag.Agent().RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		log, err := os.ReadFile(status.Nodes[1].Stdout)
		require.NoError(t, err)
		require.Contains(t, string(log), "# Example")
		require.Contains(t, string(log), "pages=3 err=false")
		require.NotContains(t, string(log), mcpToken)
	})

	t.Run("ToolErrorFailsStep", func(t *testing.T) {
		t.Parallel()
		th := test.Setup(t)
		dag := mcpDAG(t, th, newMCPServer(t), "explode")

		dag.Agent().RunError(t)

		dag.AssertLatestStatus(t, ir.Failed)
	})
}
