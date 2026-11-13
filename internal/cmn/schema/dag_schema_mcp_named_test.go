// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An mcp entry has a url, or it has no url and names a MCP server entry of the AI
// page (Spec 095). A named entry has no headers.
func TestDAGSchemaMCPNamedEntry(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	valid := []string{
		"tools:\n        mcp:\n          - { name: docs, allow: [search] }",
		"tools:\n        mcp:\n          - { name: docs, url: \"https://mcp.example.com/mcp\", allow: [search] }",
		"tools:\n        mcp:\n          - { name: docs, url: \"https://x\", headers: { Authorization: x }, allow: [search] }",
	}
	invalid := []string{
		"tools:\n        mcp:\n          - { name: docs, headers: { Authorization: x }, allow: [search] }",
		"tools:\n        mcp:\n          - { name: docs }",
		"tools:\n        mcp:\n          - { name: docs, allow: [] }",
		"tools:\n        mcp:\n          - { name: dagu, allow: [search] }",
		"tools:\n        mcp:\n          - { name: Docs, allow: [search] }",
		"tools:\n        mcp:\n          - { name: a" + strings.Repeat("b", 64) + ", allow: [search] }",
		"tools:\n        mcp:\n          - { name: docs, url: \"\", allow: [search] }",
		"tools:\n        mcp:\n          - { name: docs, allow: [search], bogus: 1 }",
	}
	agentHead := agentRunDAGHead + "      provider: claude\n      endpoint: { name: cle1, model: m }\n"
	for _, head := range []string{toolsDAGHead, agentHead} {
		for _, tools := range valid {
			require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, head+"      "+tools+"\n")), tools)
		}
		for _, tools := range invalid {
			require.Error(t, resolved.Validate(mustParseYAMLDocument(t, head+"      "+tools+"\n")), tools)
		}
	}
}
