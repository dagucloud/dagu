// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testControllerID = "ctrl_aaaaaaaaaaaaaaaa"

func validCreateYAML() []byte {
	return []byte(`type: controller
version: 1
name: Incident flow
description: Route incident work safely.
labels:
  - workspace=ops
llm:
  provider: openai
  model: gpt-4o
dags: []
states:
  default:
    description: Initial routing state.
    transitions:
      - to: completed
        when: Work is complete.
  completed:
    description: Work completed successfully.
    terminal: succeeded
`)
}

func validPersistedYAML(id string) []byte {
	return []byte(strings.Replace(string(validCreateYAML()), "version: 1\n", "version: 1\nid: "+id+"\n", 1))
}

func TestParseCreateDefinitionAppliesEffectiveDefaults(t *testing.T) {
	t.Parallel()

	definition, err := ParseCreateDefinition(validCreateYAML())
	require.NoError(t, err)
	assert.Empty(t, definition.ID)
	assert.Equal(t, DefaultMaxTurns, definition.EffectiveMaxTurns())
	assert.Equal(t, RouterInstructionPattern, definition.LLM.EffectiveSystem())
	assert.Equal(t, "ops", definition.Workspace())
}

func TestParseCreateDefinitionAcceptsOptionalDescriptionsAndChatProviders(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{
		"openai",
		"openai-codex",
		"anthropic",
		"gemini",
		"openrouter",
		"local",
		"zai",
		"opencode",
		"ollama",
	} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()

			definition, err := ParseCreateDefinition([]byte(`type: controller
version: 1
name: Router
llm:
  provider: ` + provider + `
  model: model
dags: []
states:
  default:
    terminal: succeeded
`))
			require.NoError(t, err)
			assert.Empty(t, definition.Description)
			assert.Empty(t, definition.States["default"].Description)
		})
	}
}

func TestParseCreateDefinitionRejectsGeneratedIDAndUnknownFields(t *testing.T) {
	t.Parallel()

	for _, value := range []string{testControllerID, `""`, "null"} {
		withID := strings.Replace(string(validCreateYAML()), "version: 1\n", "version: 1\nid: "+value+"\n", 1)
		_, err := ParseCreateDefinition([]byte(withID))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDefinition)
	}

	withUnknown := strings.Replace(string(validCreateYAML()), "name: Incident flow\n", "name: Incident flow\ndisplay_name: wrong\n", 1)
	_, err := ParseCreateDefinition([]byte(withUnknown))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDefinition)

	withExplicitZero := strings.Replace(string(validCreateYAML()), "description: Route incident work safely.\n", "description: Route incident work safely.\nmaxTurns: 0\n", 1)
	_, err = ParseCreateDefinition([]byte(withExplicitZero))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDefinition)
}

func TestParseDefinitionRejectsInvalidGraphAndSystemTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		path string
	}{
		{
			name: "structural dead end",
			yaml: strings.Replace(string(validPersistedYAML(testControllerID)), "    transitions:\n      - to: completed\n        when: Work is complete.\n", "", 1),
			path: "states.default",
		},
		{
			name: "terminal has transition",
			yaml: strings.Replace(string(validPersistedYAML(testControllerID)), "    terminal: succeeded\n", "    terminal: succeeded\n    transitions:\n      - to: default\n        when: Continue.\n", 1),
			path: "states.completed",
		},
		{
			name: "reserved template not first",
			yaml: strings.Replace(string(validPersistedYAML(testControllerID)), "  model: gpt-4o\n", "  model: gpt-4o\n  system: policy ${{.RouterInstruction}}\n", 1),
			path: "llm.system",
		},
		{
			name: "max turns too small",
			yaml: strings.Replace(string(validPersistedYAML(testControllerID)), "description: Route incident work safely.\n", "description: Route incident work safely.\nmaxTurns: 1\n", 1),
			path: "maxTurns",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDefinition([]byte(test.yaml))
			require.Error(t, err)
			var validationErr *ValidationError
			require.True(t, errors.As(err, &validationErr))
			assert.Contains(t, validationErr.Error(), test.path)
		})
	}
}

func TestValidatorChecksCurrentDAGIdentityWorkspaceAndParams(t *testing.T) {
	t.Parallel()

	yaml := strings.Replace(string(validPersistedYAML(testControllerID)), "dags: []", "dags:\n  - inspect-alert", 1)
	yaml = strings.Replace(yaml, "    transitions:\n", "    dags:\n      - inspect-alert\n    transitions:\n", 1)
	definition, err := ParseDefinition([]byte(yaml))
	require.NoError(t, err)

	resolver := func(context.Context, string) (DAGMetadata, error) {
		return DAGMetadata{
			FileName:  "other-file",
			Name:      "inspect-alert",
			Workspace: "security",
			ParamDefs: []core.ParamDef{{}},
		}, nil
	}
	_, err = NewValidator(resolver).Validate(context.Background(), definition)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidDefinition)
	assert.Contains(t, err.Error(), "fileName and name")
	assert.Contains(t, err.Error(), "different workspace")
	assert.Contains(t, err.Error(), "positional parameters")
}

func TestValidatorReportsDefinitionWarnings(t *testing.T) {
	t.Parallel()

	yaml := strings.Replace(string(validPersistedYAML(testControllerID)), "dags: []", "dags:\n  - inspect-alert\n  - notify-team", 1)
	yaml = strings.Replace(yaml, `states:
  default:
    description: Initial routing state.
    transitions:
      - to: completed
        when: Work is complete.
  completed:
    description: Work completed successfully.
    terminal: succeeded
`, `states:
  default:
    description: Initial routing state.
    dags:
      - inspect-alert
  orphaned:
    description: Unreachable routing state.
    transitions:
      - to: orphaned
        when: Continue waiting.
`, 1)
	definition, err := ParseDefinition([]byte(yaml))
	require.NoError(t, err)

	resolver := func(_ context.Context, fileName string) (DAGMetadata, error) {
		description := "Notify the response team."
		if fileName == "inspect-alert" {
			description = ""
		}
		return DAGMetadata{
			FileName:    fileName,
			Name:        fileName,
			Workspace:   "ops",
			Description: description,
		}, nil
	}
	warnings, err := NewValidator(resolver).Validate(context.Background(), definition)
	require.NoError(t, err)

	assert.Equal(t, []DefinitionWarning{
		{
			Code:    "unreachable_state",
			Path:    "states.orphaned",
			Message: `State "orphaned" is unreachable from default`,
		},
		{
			Code:    "unused_dag",
			Path:    "dags[1]",
			Message: `DAG "notify-team" is not referenced by any State`,
		},
		{
			Code:    "dag_description_empty",
			Path:    "dags[0]",
			Message: `DAG "inspect-alert" has no description for the Router`,
		},
		{
			Code:    "no_terminal_state",
			Path:    "states",
			Message: "Controller has no terminal State and can only finish when stopped or failed",
		},
	}, warnings)
}

func TestValidatePromptPreservesInputAndRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidatePrompt("  keep surrounding spaces  "))
	assert.ErrorIs(t, ValidatePrompt(" \n\t"), ErrInvalidPrompt)
	assert.ErrorIs(t, ValidatePrompt(strings.Repeat("x", maxPromptBytes+1)), ErrInvalidPrompt)
}

func TestValidateNameRejectsLineBreaks(t *testing.T) {
	t.Parallel()

	for _, lineBreak := range []rune{'\n', '\v', '\f', '\r', '\u0085', '\u2028', '\u2029'} {
		issues := validateName("Incident" + string(lineBreak) + "flow")
		assert.Contains(t, issues, issue("single_line", "name", "must be a single line"))
	}
}

func TestValidateID(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateID(testControllerID))
	assert.ErrorIs(t, ValidateID("ctrl_invalid"), ErrInvalidDefinition)
}
