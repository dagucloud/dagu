// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestReadToolIdentityIsReadOnly(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")
	ctx := mcptest.Context(t)

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	var tool *mcpsdk.Tool
	for _, candidate := range result.Tools {
		if candidate.Name == "dagu_read" {
			tool = candidate
			break
		}
	}
	require.NotNil(t, tool)
	require.NotNil(t, tool.Annotations)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.NotNil(t, tool.Annotations.OpenWorldHint)
	require.False(t, *tool.Annotations.OpenWorldHint)
	if tool.Annotations.DestructiveHint != nil {
		require.False(t, *tool.Annotations.DestructiveHint)
	}
}

func TestReadSuccessfulCallsDoNotMutateDAGState(t *testing.T) {
	fixture := newReadFixture(t)

	before := callRead(t, fixture.session, map[string]any{
		"target": "dags",
		"query":  "name=" + fixture.dagName,
	})
	require.False(t, before.IsError)
	beforeItem := requireItem(t, requireItems(t, requireData(t, mcptest.StructuredMap(t, before))), "name", fixture.dagName)

	read := callRead(t, fixture.session, map[string]any{
		"target": "dag_spec",
		"name":   fixture.dagName,
	})
	requireReadSuccess(t, read, "dag_spec", dagSpecURI(fixture.dagName), "dag_spec", "application/yaml")

	after := callRead(t, fixture.session, map[string]any{
		"target": "dags",
		"query":  "name=" + fixture.dagName,
	})
	require.False(t, after.IsError)
	afterItem := requireItem(t, requireItems(t, requireData(t, mcptest.StructuredMap(t, after))), "name", fixture.dagName)
	require.Equal(t, beforeItem["uri"], afterItem["uri"])
}
