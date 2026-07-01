// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec022_mcp_change_tool_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChangeApplyCreatesDAG(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_apply_create"
	spec := fixtureSpec(t, "valid_initial.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	result := callChange(t, fixture.session, changeArguments("apply", "upsert_dag", dagName, spec))
	output := requireChangeSuccess(t, result, "DAG change applied.", dagURI)
	require.Equal(t, "apply", requireString(t, output, "mode"))
	require.Equal(t, dagName, requireString(t, output, "dagName"))
	require.True(t, requireBool(t, output, "valid"))
	require.True(t, requireBool(t, output, "applied"))
	require.True(t, requireBool(t, output, "created"))
	require.False(t, requireBool(t, output, "updated"))
	require.Empty(t, requireArray(t, output, "errors"))
	require.Contains(t, output, "dag")

	require.Equal(t, spec, requireReadDAGSpec(t, fixture.session, dagName))
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangeApplyUpdatesExistingDAG(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_apply_update"
	initialSpec := fixtureSpec(t, "valid_initial.yaml")
	updatedSpec := fixtureSpec(t, "valid_updated.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	createResult := callChange(t, fixture.session, changeArguments("apply", "upsert_dag", dagName, initialSpec))
	createOutput := requireChangeSuccess(t, createResult, "DAG change applied.", dagURI)
	require.True(t, requireBool(t, createOutput, "created"))
	require.False(t, requireBool(t, createOutput, "updated"))

	updateResult := callChange(t, fixture.session, changeArguments("apply", "upsert_dag", dagName, updatedSpec))
	updateOutput := requireChangeSuccess(t, updateResult, "DAG change applied.", dagURI)
	require.Equal(t, "apply", requireString(t, updateOutput, "mode"))
	require.Equal(t, dagName, requireString(t, updateOutput, "dagName"))
	require.True(t, requireBool(t, updateOutput, "valid"))
	require.True(t, requireBool(t, updateOutput, "applied"))
	require.False(t, requireBool(t, updateOutput, "created"))
	require.True(t, requireBool(t, updateOutput, "updated"))
	require.Contains(t, updateOutput, "dag")

	require.Equal(t, updatedSpec, requireReadDAGSpec(t, fixture.session, dagName))
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangeApplyInvalidDoesNotOverwriteExistingDAG(t *testing.T) {
	fixture := newChangeFixture(t)
	dagName := "mcp_change_apply_invalid"
	initialSpec := fixtureSpec(t, "valid_initial.yaml")
	invalidSpec := fixtureSpec(t, "invalid_malformed_yaml.yaml")
	dagURI := "dagu://dags/" + dagName + "/spec"

	createResult := callChange(t, fixture.session, changeArguments("apply", "upsert_dag", dagName, initialSpec))
	createOutput := requireChangeSuccess(t, createResult, "DAG change applied.", dagURI)
	require.True(t, requireBool(t, createOutput, "created"))

	invalidResult := callChange(t, fixture.session, changeArguments("apply", "upsert_dag", dagName, invalidSpec))
	invalidOutput := requireChangeSuccess(t, invalidResult, "DAG spec is not valid; no changes were applied.", dagURI)
	require.Equal(t, "apply", requireString(t, invalidOutput, "mode"))
	require.False(t, requireBool(t, invalidOutput, "valid"))
	require.False(t, requireBool(t, invalidOutput, "applied"))
	require.NotEmpty(t, requireArray(t, invalidOutput, "errors"))
	require.NotContains(t, invalidOutput, "created")
	require.NotContains(t, invalidOutput, "updated")

	require.Equal(t, initialSpec, requireReadDAGSpec(t, fixture.session, dagName))
	requireNoDAGRuns(t, fixture.session, dagName)
}

func TestChangeApplyAffectsOnlyNamedDAG(t *testing.T) {
	fixture := newChangeFixture(t)
	targetName := "mcp_change_apply_target"
	otherName := "mcp_change_apply_other"
	targetSpec := fixtureSpec(t, "valid_initial.yaml")
	otherSpec := fixtureSpec(t, "valid_updated.yaml")
	dagURI := "dagu://dags/" + targetName + "/spec"
	fixture.server.CreateDAG(t, otherName, otherSpec)

	result := callChange(t, fixture.session, changeArguments("apply", "upsert_dag", targetName, targetSpec))
	output := requireChangeSuccess(t, result, "DAG change applied.", dagURI)
	require.Equal(t, "apply", requireString(t, output, "mode"))
	require.True(t, requireBool(t, output, "valid"))
	require.True(t, requireBool(t, output, "applied"))
	require.True(t, requireBool(t, output, "created"))
	require.False(t, requireBool(t, output, "updated"))
	require.Contains(t, output, "dag")

	require.Equal(t, targetSpec, requireReadDAGSpec(t, fixture.session, targetName))
	require.Equal(t, otherSpec, requireReadDAGSpec(t, fixture.session, otherName))
	requireNoDAGRuns(t, fixture.session, targetName)
	requireNoDAGRuns(t, fixture.session, otherName)
}
