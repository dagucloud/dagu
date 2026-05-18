// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"sort"
	"testing"

	dagschema "github.com/dagucloud/dagu/internal/cmn/schema"
	"github.com/stretchr/testify/require"
)

func TestDeprecatedSyntaxWarningsDeterministic(t *testing.T) {
	t.Parallel()

	warnings := DeprecatedSyntaxWarnings([]byte(`
step_types:
  greet:
    type: command
steps:
  second:
    type: http
    with:
      url: https://example.com
  first:
    command: echo first
handler_on:
  success:
    script: echo success
  failure:
    command: echo failure
`))

	require.Equal(t, []string{
		"Deprecated DAG syntax: step_types is deprecated; use actions",
		"Deprecated DAG syntax: steps.first.command is deprecated; use run",
		"Deprecated DAG syntax: steps.second.type is deprecated; use action",
		"Deprecated DAG syntax: steps.second.with is deprecated with legacy execution syntax; use action with with",
		"Deprecated DAG syntax: handler_on.failure.command is deprecated; use run",
		"Deprecated DAG syntax: handler_on.success.script is deprecated; use run",
	}, warnings)
}

func TestDeprecatedSyntaxWarningsStringStepShorthand(t *testing.T) {
	t.Parallel()

	warnings := DeprecatedSyntaxWarnings([]byte(`
steps:
  - echo shorthand
`))

	require.Equal(t, []string{
		"Deprecated DAG syntax: steps[0] string shorthand is deprecated; use run",
	}, warnings)
}

func TestBuiltinActionNamesMatchDAGSchema(t *testing.T) {
	t.Parallel()

	var schema map[string]any
	require.NoError(t, json.Unmarshal(dagschema.DAGSchemaJSON, &schema))

	definitions, ok := schema["definitions"].(map[string]any)
	require.True(t, ok)
	actionName, ok := definitions["actionName"].(map[string]any)
	require.True(t, ok)
	anyOf, ok := actionName["anyOf"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, anyOf)
	builtin, ok := anyOf[0].(map[string]any)
	require.True(t, ok)
	enumValues, ok := builtin["enum"].([]any)
	require.True(t, ok)

	schemaActions := make([]string, 0, len(enumValues))
	for _, value := range enumValues {
		str, ok := value.(string)
		require.True(t, ok)
		schemaActions = append(schemaActions, str)
	}
	sort.Strings(schemaActions)

	codeActions := make([]string, 0, len(builtinActionNormalizers))
	for action := range builtinActionNormalizers {
		codeActions = append(codeActions, action)
	}
	sort.Strings(codeActions)

	require.Equal(t, codeActions, schemaActions)
}
