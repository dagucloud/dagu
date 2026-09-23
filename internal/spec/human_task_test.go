// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
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
	assert.False(t, dag.ForceLocal)

	step := dag.Steps[0]
	require.NotNil(t, step.HumanTask)
	assert.Equal(t, "Deploy ${ENVIRONMENT}?", step.HumanTask.Prompt)
	assert.Empty(t, step.ExecutorConfig.Type)
	assert.Empty(t, step.ExecutorConfig.Config)
	assert.ElementsMatch(t, []ir.StepOutputDeclaration{
		{Name: "window", Type: ir.StepDeclaredOutputTypeString},
		{Name: "retries", Type: ir.StepDeclaredOutputTypeJSON},
		{Name: "confirmed", Type: ir.StepDeclaredOutputTypeJSON},
		{Name: "note", Type: ir.StepDeclaredOutputTypeString},
		{Name: "decision", Type: ir.StepDeclaredOutputTypeString},
	}, step.Outputs)

	var form map[string]any
	require.NoError(t, json.Unmarshal(step.HumanTask.Form, &form))
	assert.Equal(t, false, form["additionalProperties"])
}

// A push-back target written as a step ID resolves to the step name, and the
// feedback form adds no outputs next to the completion form.
func TestHumanTaskBuildsPushBack(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: implement
    name: Implement change
    run: echo implement
  - id: test
    depends: implement
    run: echo test
  - id: review
    depends: test
    action: human.task
    with:
      prompt: Review
      form:
        type: object
        properties:
          note:
            type: string
      push_back:
        rewind_to: " implement "
        form:
          type: object
          required: [feedback]
          properties:
            feedback:
              type: string
            severity:
              type: string
              enum: [minor, major]
`))
	require.NoError(t, err)

	task := dag.Steps[2].HumanTask
	require.NotNil(t, task)
	require.NotNil(t, task.PushBack)
	assert.Equal(t, "Implement change", task.PushBack.RewindTo)
	assert.JSONEq(t, `{
		"type": "object",
		"properties": {
			"feedback": {"type": "string"},
			"severity": {"type": "string", "enum": ["minor", "major"]}
		},
		"required": ["feedback"],
		"additionalProperties": false
	}`, string(task.PushBack.Form))
	assert.Equal(t, []ir.StepOutputDeclaration{
		{Name: "note", Type: ir.StepDeclaredOutputTypeString},
	}, dag.Steps[2].Outputs)
}

func TestHumanTaskBuildsPushBackWithoutForm(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: implement
`))
	require.NoError(t, err)

	pushBack := dag.Steps[1].HumanTask.PushBack
	require.NotNil(t, pushBack)
	assert.Equal(t, "implement", pushBack.RewindTo)
	assert.Empty(t, pushBack.Form)
}

func TestHumanTaskAllowsDAGWorkerSelector(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
worker_selector:
  region: remote
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
`))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"region": "remote"}, dag.WorkerSelector)
	assert.False(t, dag.ForceLocal)
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

func TestHumanTaskBuildsArtifactReferences(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts:
        - changes.diff
        - reports\\test-report.html
`))
	require.NoError(t, err)
	require.NotNil(t, dag.Steps[0].HumanTask)
	assert.Equal(t, []string{"changes.diff", "reports/test-report.html"}, dag.Steps[0].HumanTask.Artifacts)
}

// The build normalizes artifact paths but never resolves their references;
// resolution happens when the task opens. The first entry is deliberately not
// a path.Clean fixed point, so a normalization change cannot pass unnoticed.
func TestHumanTaskDoesNotResolveArtifactReferencesAtBuild(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
params:
  - OUT: reports
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts:
        - "./${params.OUT}//report.html"
        - "${OUT}/summary.txt"
`))
	require.NoError(t, err)
	require.NotNil(t, dag.Steps[0].HumanTask)
	assert.Equal(t,
		[]string{"${params.OUT}/report.html", "${OUT}/summary.txt"},
		dag.Steps[0].HumanTask.Artifacts)
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
			name: "ArtifactsMustBeArray",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: changes.diff
`,
			message: "with.artifacts must be an array",
		},
		{
			name: "ArtifactsMustNotBeNull",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: null
`,
			message: "with.artifacts must be an array",
		},
		{
			name: "ArtifactsMustContainStrings",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: [changes.diff, 1]
`,
			message: "entries must be strings",
		},
		{
			name: "ArtifactsRejectEmptyPath",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: [""]
`,
			message: "path must not be empty",
		},
		{
			name: "ArtifactsRejectAbsolutePath",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: [/etc/passwd]
`,
			message: "artifact path must be relative",
		},
		{
			name: "ArtifactsRejectHomeRelativePath",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: [~/secret]
`,
			message: "artifact path must be relative",
		},
		{
			name: "ArtifactsRejectUnsafePaths",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: [../../secret]
`,
			message: "must not contain parent directory segments",
		},
		{
			name: "ArtifactsRejectParentSegmentsBesideReferences",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: ["${params.OUT}/../secret"]
`,
			message: "must not contain parent directory segments",
		},
		{
			name: "ArtifactsRejectDuplicatePaths",
			yaml: `
steps:
  - id: review
    action: human.task
    with:
      prompt: Review
      artifacts: [changes.diff, changes.diff]
`,
			message: "contains duplicate path",
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
			name: "PushBackMustBeObject",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back: implement
`,
			message: "with.push_back must be an object",
		},
		{
			name: "PushBackMustNotBeNull",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back: null
`,
			message: "with.push_back must be an object",
		},
		{
			name: "PushBackUnknownField",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: implement
        limit: 3
`,
			message: "with.push_back does not support limit",
		},
		{
			name: "PushBackRequiresRewindTo",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back: {}
`,
			message: "with.push_back.rewind_to must be a non-empty step id or name",
		},
		{
			name: "PushBackRejectsBlankRewindTo",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: " "
`,
			message: "with.push_back.rewind_to must be a non-empty step id or name",
		},
		{
			name: "PushBackRejectsMissingTarget",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: build
`,
			message: "with.push_back.rewind_to references non-existent step build",
		},
		{
			name: "PushBackRejectsSelf",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: review
`,
			message: "not the task itself",
		},
		{
			name: "PushBackRejectsNonUpstreamTarget",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: publish
  - id: publish
    depends: review
    run: echo publish
`,
			message: "with.push_back.rewind_to must reference an upstream dependency",
		},
		{
			name: "PushBackFormMustNotBeNull",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: implement
        form: null
`,
			message: "with.push_back.form must be an object schema",
		},
		{
			name: "PushBackFormRejectsAdditionalProperties",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: implement
        form:
          type: object
          additionalProperties: true
`,
			message: "with.push_back.form additionalProperties must be false",
		},
		{
			name: "PushBackFormUsesFormRules",
			yaml: `
steps:
  - id: implement
    run: echo implement
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: implement
        form:
          type: object
          properties:
            details:
              type: object
`,
			message: "with.push_back.form",
		},
		{
			name: "PushBackInAgentDAG",
			yaml: `
type: agent
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - id: implement
    run: echo implement
  - id: review
    action: human.task
    with:
      prompt: Review
      push_back:
        rewind_to: implement
tasks:
  - name: ship
    description: done when reviewed
`,
			message: "with.push_back is not allowed in type: agent",
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
