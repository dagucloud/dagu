// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanTaskBuildsFormOutputs(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: review
    action: human.task
    with:
      prompt: "Deploy ${ENVIRONMENT}?"
      form:
        type: object
        properties:
          window:
            type: string
          retries:
            type: integer
            default: 0
          confirmed:
            type: boolean
            default: false
          note:
            type: string
          decision:
            oneOf:
              - const: approve
                title: Approve
              - const: cancel
                title: Cancel
        required: [window, decision]
  - id: deploy
    depends: review
    run: echo "${steps.review.outputs.window}"
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 2)
	assert.True(t, dag.ForceLocal)

	step := dag.Steps[0]
	require.NotNil(t, step.HumanTask)
	assert.Equal(t, "Deploy ${ENVIRONMENT}?", step.HumanTask.Prompt)
	assert.Empty(t, step.ExecutorConfig.Type)
	assert.Empty(t, step.ExecutorConfig.Config)
	assert.ElementsMatch(t, []core.StepOutputDeclaration{
		{Name: "window", Type: core.StepDeclaredOutputTypeString},
		{Name: "retries", Type: core.StepDeclaredOutputTypeJSON},
		{Name: "confirmed", Type: core.StepDeclaredOutputTypeJSON},
		{Name: "note", Type: core.StepDeclaredOutputTypeString},
		{Name: "decision", Type: core.StepDeclaredOutputTypeString},
	}, step.Outputs)

	var form map[string]any
	require.NoError(t, json.Unmarshal(step.HumanTask.Form, &form))
	assert.Equal(t, false, form["additionalProperties"])
}

func TestHumanTaskInLocalDAGForcesParentLocal(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: parent
steps:
  - id: child
    action: dag.run
    with:
      dag: child
---
name: child
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
`))
	require.NoError(t, err)
	assert.True(t, dag.ForceLocal)
	require.Contains(t, dag.LocalDAGs, "child")
	assert.True(t, dag.LocalDAGs["child"].ForceLocal)
}

func TestHumanTaskMetadataForcesScheduledDAGLocal(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"root step": `
name: review-flow
schedule: "0 * * * *"
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
`,
		"local DAG step": `
name: parent
schedule: "0 * * * *"
steps:
  - id: child
    action: dag.run
    with:
      dag: child
---
name: child
steps:
  review:
    id: review
    action: human.task
    with:
      prompt: Review
`,
	}

	for name, yamlData := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dag, err := LoadYAML(
				context.Background(),
				[]byte(yamlData),
				OnlyMetadata(),
				WithoutEval(),
				SkipSchemaValidation(),
			)
			require.NoError(t, err)
			assert.True(t, dag.ForceLocal)
		})
	}
}

func TestHumanTaskAllowsAcknowledgementWithoutForm(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: acknowledge
    action: human.task
    with:
      prompt: Confirm the maintenance notice was read
`))
	require.NoError(t, err)
	require.NotNil(t, dag.Steps[0].HumanTask)
	assert.Nil(t, dag.Steps[0].HumanTask.Form)
	assert.Empty(t, dag.Steps[0].Outputs)
}

func TestHumanTaskDoesNotInheritExecutionDefaults(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
defaults:
  retry_policy:
    limit: 3
  timeout_sec: 30
  mail_on_error: true
  signal_on_stop: SIGKILL
steps:
  - id: acknowledge
    action: human.task
    with:
      prompt: Confirm the maintenance notice was read
`))
	require.NoError(t, err)
	step := dag.Steps[0]
	assert.Zero(t, step.RetryPolicy.Limit)
	assert.Zero(t, step.Timeout)
	assert.False(t, step.MailOnError)
	assert.Empty(t, step.SignalOnStop)
}

func TestHumanTaskFormAllowsAdditionalPropertiesExplicitly(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      form:
        type: object
        properties: {}
        additionalProperties: true
`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"object","properties":{},"additionalProperties":true}`, string(dag.Steps[0].HumanTask.Form))
}

func TestHumanTaskFormPreservesOneOfConstraints(t *testing.T) {
	t.Parallel()

	form, _, err := buildHumanTaskForm(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decision": map[string]any{
				"type":    "string",
				"pattern": "^approve$",
				"enum":    []any{"approve"},
				"oneOf": []any{
					map[string]any{"type": "string", "const": "approve"},
					map[string]any{"type": "string", "const": "reject"},
				},
			},
		},
		"required": []any{"decision"},
	})
	require.NoError(t, err)

	result, err := ValidateHumanTaskInputs(form, map[string]any{"decision": "approve"}, false)
	require.NoError(t, err)
	assert.Equal(t, "approve", result.Outputs["decision"])

	_, err = ValidateHumanTaskInputs(form, map[string]any{"decision": "reject"}, false)
	require.Error(t, err)
}

func TestHumanTaskRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		message string
	}{
		{
			name: "MissingID",
			yaml: `
steps:
  - action: human.task
    with:
      prompt: Review
`,
			message: "requires an explicit id",
		},
		{
			name: "MissingPrompt",
			yaml: `
steps:
  - id: review
    action: human.task
    with: {}
`,
			message: "with.prompt",
		},
		{
			name: "ExplicitOutputs",
			yaml: `
steps:
  - id: review
    action: human.task
    outputs:
      - name: result
    with:
      prompt: Review
`,
			message: "derives outputs from its form",
		},
		{
			name: "ApprovalIsSeparate",
			yaml: `
steps:
  - id: review
    action: human.task
    approval:
      prompt: Approve
    with:
      prompt: Review
`,
			message: "does not support approval",
		},
		{
			name: "OneOfConstDoesNotMatchType",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      form:
        type: object
        properties:
          decision:
            type: string
            oneOf:
              - type: string
                const: approve
              - type: integer
                const: reject
`,
			message: "does not match its type",
		},
		{
			name: "NestedProperty",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      form:
        type: object
        properties:
          nested:
            type: object
            properties:
              value: {type: string}
`,
			message: "unsupported schema field",
		},
		{
			name: "UnknownRequiredProperty",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      form:
        type: object
        properties: {}
        required: [missing]
`,
			message: "is not declared",
		},
		{
			name: "DAGWorkerSelector",
			yaml: `
worker_selector:
  region: remote
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
`,
			message: "cannot be dispatched to workers",
		},
		{
			name: "LifecycleHandler",
			yaml: `
handler_on:
  init:
    id: review
    action: human.task
    with:
      prompt: Review
steps:
  - run: echo ready
`,
			message: "cannot be used in handler_on.init",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadYAML(context.Background(), []byte(test.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.message)
		})
	}
}

func TestValidateHumanTaskInputs(t *testing.T) {
	t.Parallel()

	form, _, err := buildHumanTaskForm(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"window": map[string]any{"type": "string"},
			"count":  map[string]any{"type": "integer", "default": 2},
			"note":   map[string]any{"type": "string"},
		},
		"required": []any{"window"},
	})
	require.NoError(t, err)

	result, err := ValidateHumanTaskInputs(form, map[string]any{"window": "night", "count": "3", "note": "ready"}, true)
	require.NoError(t, err)
	assert.JSONEq(t, `{"window":"night","count":3,"note":"ready"}`, string(result.Canonical))
	assert.Equal(t, map[string]string{"window": "night", "count": "3", "note": "ready"}, result.Outputs)

	_, err = ValidateHumanTaskInputs(form, map[string]any{"count": 1}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "window")

	_, err = ValidateHumanTaskInputs(form, map[string]any{"window": "night", "extra": true}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extra")

	result, err = ValidateHumanTaskInputs(form, map[string]any{"window": "night", "count": json.Number("4")}, false)
	require.NoError(t, err)
	assert.Equal(t, "4", result.Outputs["count"])
	result, err = ValidateHumanTaskInputs(form, map[string]any{"window": "night", "count": json.Number("4.0")}, false)
	require.NoError(t, err)
	assert.Equal(t, "4", result.Outputs["count"])

	_, err = ValidateHumanTaskInputs(form, map[string]any{"window": json.Number("4")}, false)
	require.Error(t, err)
}
