// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec022_mcp_change_tool_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChangePreviewValidDoesNotWrite(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_preview_valid"
	spec := fixtureSpec(t, "valid_initial.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	result := callChange(t, fixture.session, changeArguments("preview", "upsert_dag", dagName, spec))
	output := requireChangeSuccess(t, result, "DAG spec is valid. Re-run with mode=apply to write it.", dagURI)
	require.Equal(t, "preview", requireString(t, output, "mode"))
	require.Equal(t, dagName, requireString(t, output, "dagName"))
	require.True(t, requireBool(t, output, "valid"))
	require.False(t, requireBool(t, output, "applied"))
	require.Empty(t, requireArray(t, output, "errors"))
	require.Contains(t, output, "dag")
	require.NotContains(t, output, "created")
	require.NotContains(t, output, "updated")

	requireDAGSpecNotFound(t, fixture.session, dagName)
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangePreviewExistingDAGDoesNotUpdate(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_preview_existing"
	initialSpec := fixtureSpec(t, "valid_initial.yaml")
	updatedSpec := fixtureSpec(t, "valid_updated.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"
	fixture.server.CreateDAG(t, dagName, initialSpec)

	result := callChange(t, fixture.session, changeArguments("preview", "upsert_dag", dagName, updatedSpec))
	output := requireChangeSuccess(t, result, "DAG spec is valid. Re-run with mode=apply to write it.", dagURI)
	require.Equal(t, "preview", requireString(t, output, "mode"))
	require.Equal(t, dagName, requireString(t, output, "dagName"))
	require.True(t, requireBool(t, output, "valid"))
	require.False(t, requireBool(t, output, "applied"))
	require.NotContains(t, output, "created")
	require.NotContains(t, output, "updated")

	require.Equal(t, initialSpec, requireReadDAGSpec(t, fixture.session, dagName))
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangePreviewInvalidReturnsValidationResult(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_preview_invalid"
	spec := fixtureSpec(t, "invalid_malformed_yaml.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	result := callChange(t, fixture.session, map[string]any{
		"name": dagName,
		"spec": spec,
	})
	output := requireChangeSuccess(t, result, "DAG spec is not valid; no changes were applied.", dagURI)
	require.Equal(t, "preview", requireString(t, output, "mode"))
	require.Equal(t, "upsert_dag", requireString(t, output, "type"))
	require.Equal(t, dagName, requireString(t, output, "dagName"))
	require.False(t, requireBool(t, output, "valid"))
	require.False(t, requireBool(t, output, "applied"))
	require.NotEmpty(t, requireArray(t, output, "errors"))
	require.NotContains(t, output, "created")
	require.NotContains(t, output, "updated")

	requireDAGSpecNotFound(t, fixture.session, dagName)
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangeDefaultsNullAndTrimmedFields(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_defaults"
	spec := fixtureSpec(t, "valid_initial.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	result := callChange(t, fixture.session, map[string]any{
		"mode": nil,
		"type": nil,
		"name": "  " + dagName + "  ",
		"spec": spec,
	})
	output := requireChangeSuccess(t, result, "DAG spec is valid. Re-run with mode=apply to write it.", dagURI)
	require.Equal(t, "preview", requireString(t, output, "mode"))
	require.Equal(t, "upsert_dag", requireString(t, output, "type"))
	require.Equal(t, dagName, requireString(t, output, "dagName"))
	require.True(t, requireBool(t, output, "valid"))
	require.False(t, requireBool(t, output, "applied"))

	requireDAGSpecNotFound(t, fixture.session, dagName)
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangeTrimsModeTypeAndName(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_trimmed_fields"
	spec := fixtureSpec(t, "valid_initial.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	result := callChange(t, fixture.session, map[string]any{
		"mode": "  preview  ",
		"type": "  upsert_dag  ",
		"name": "  " + dagName + "  ",
		"spec": spec,
	})
	output := requireChangeSuccess(t, result, "DAG spec is valid. Re-run with mode=apply to write it.", dagURI)
	require.Equal(t, "preview", requireString(t, output, "mode"))
	require.Equal(t, "upsert_dag", requireString(t, output, "type"))
	require.Equal(t, dagName, requireString(t, output, "dagName"))
	require.True(t, requireBool(t, output, "valid"))
	require.False(t, requireBool(t, output, "applied"))

	requireDAGSpecNotFound(t, fixture.session, dagName)
	requireNoDAGRuns(t, fixture.session, dagName)
}
