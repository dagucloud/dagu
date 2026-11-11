// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"sort"
	"testing"

	dagschema "github.com/dagucloud/dagu/v2/internal/cmn/schema"
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

// The list form of tools is deprecated: the warning says so for a with block
// and for an llm block. The key dags gives no warning.
func TestDeprecatedSyntaxWarningsToolsListForm(t *testing.T) {
	t.Parallel()

	warnings := DeprecatedSyntaxWarnings([]byte(`
llm:
  provider: local
  model: m
  tools: [lookup]
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
      tools:
        - lookup
  - id: mapped
    action: chat.completion
    with:
      prompt: hi
      dags:
        - name: lookup
`))

	require.Equal(t, []string{
		"Deprecated DAG syntax: llm.tools: The list form of `tools` is deprecated. Use the key dags.",
		"Deprecated DAG syntax: steps[0].with.tools: The list form of `tools` is deprecated. Use the key dags.",
	}, warnings)
}

// The old provider method of chat.completion stays valid and gives one validate-only
// warning for each step. It names each key of the old method that the step uses,
// in the with and in the rows of the model list. The block endpoint gives none.
func TestDeprecatedSyntaxWarningsChatProvider(t *testing.T) {
	t.Parallel()

	warnings := DeprecatedSyntaxWarnings([]byte(`
steps:
  - id: old
    action: chat.completion
    with:
      provider: openai
      model: gpt-4o
      api_key_name: OPENAI_API_KEY
      prompt: hi
  - id: rows
    action: chat.completion
    with:
      model:
        - name: claude-sonnet-4-6
          endpoint: cle1
        - name: gpt-4o
          provider: openai
          api_key_name: OPENAI_API_KEY
      prompt: hi
  - id: new
    action: chat.completion
    with:
      endpoint:
        name: cle1
        model: claude-sonnet-4-6
      prompt: hi
  - id: other
    action: http.request
    with:
      provider: x
`))

	require.Equal(t, []string{
		"Deprecated DAG syntax: steps[0].with.provider, steps[0].with.api_key_name: use with.endpoint, an endpoint of the AI page",
		"Deprecated DAG syntax: steps[1].with.model[1].provider, steps[1].with.model[1].api_key_name: use with.endpoint, an endpoint of the AI page",
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
