// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomStepTypes_DAGLocalExecTemplate(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-local
step_types:
  greet:
    type: command
    description: Send a greeting
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
        repeat:
          type: integer
          default: 3
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
          - {$input: repeat}
steps:
  - type: greet
    with:
      message: hello
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "greet_1", step.Name)
	assert.Equal(t, "direct", step.Shell)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "/bin/echo", step.Commands[0].Command)
	assert.Equal(t, []string{"hello", "3"}, step.Commands[0].Args)
	assert.Equal(t, "greet", step.ExecutorConfig.Metadata["custom_type"])
	assert.Equal(t, "Send a greeting", step.Description)
}

func TestCustomStepTypes_OutputSchemaIsAttachedToExpandedStep(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-output-schema
step_types:
  classify:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties:
        text:
          type: string
    output_schema:
      type: object
      additionalProperties: false
      required: [category, confidence]
      properties:
        category:
          type: string
        confidence:
          type: number
          minimum: 0
          maximum: 1
    template:
      command: echo '{"category":"bug","confidence":0.9}'
steps:
  - id: classify
    type: classify
    with:
      text: crash on startup
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	require.NotNil(t, step.OutputSchema)
	assert.Equal(t, "object", step.OutputSchema["type"])
	assert.Contains(t, step.OutputSchema, "required")
}

func TestCustomStepTypes_RejectInvalidOutputSchema(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-invalid-output-schema
step_types:
  classify:
    type: command
    input_schema:
      type: object
    output_schema:
      type: string
    template:
      command: echo '{}'
steps:
  - type: classify
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output_schema must resolve to an object schema")
}

func TestCustomStepTypes_AllowsRefObjectOutputSchema(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-ref-output-schema
step_types:
  classify:
    type: command
    input_schema:
      type: object
    output_schema:
      $ref: '#/$defs/result'
      $defs:
        result:
          type: object
          additionalProperties: false
          properties:
            category:
              type: string
    template:
      command: echo '{"category":"bug"}'
steps:
  - type: classify
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	require.NotNil(t, dag.Steps[0].OutputSchema)
	assert.Equal(t, "#/$defs/result", dag.Steps[0].OutputSchema["$ref"])
}

func TestCustomStepTypes_AllowsComposedObjectOutputSchema(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-composed-output-schema
step_types:
  classify:
    type: command
    input_schema:
      type: object
    output_schema:
      anyOf:
        - type: object
          additionalProperties: false
          properties:
            category:
              type: string
        - type: object
          additionalProperties: false
          properties:
            priority:
              type: string
    template:
      command: echo '{"category":"bug"}'
steps:
  - type: classify
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	require.NotNil(t, dag.Steps[0].OutputSchema)
}

func TestCustomStepTypes_RejectsMixedComposedOutputSchema(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-mixed-composed-output-schema
step_types:
  classify:
    type: command
    input_schema:
      type: object
    output_schema:
      anyOf:
        - type: object
        - type: string
    template:
      command: echo '{}'
steps:
  - type: classify
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output_schema must resolve to an object schema")
}

func TestCustomStepTypes_AllowsUnconstrainedOutputSchema(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-unconstrained-output-schema
step_types:
  classify:
    type: command
    input_schema:
      type: object
    output_schema: {}
    template:
      command: echo '{}'
steps:
  - type: classify
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	assert.NotNil(t, dag.Steps[0].OutputSchema)
}

func TestCustomStepTypes_LegacyConfigAlias(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-legacy-config
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
steps:
  - type: greet
    config:
      message: hello
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	require.Len(t, dag.Steps[0].Commands, 1)
	assert.Equal(t, []string{"hello"}, dag.Steps[0].Commands[0].Args)
}

func TestCustomStepTypes_RejectWithAndLegacyConfig(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-mixed-config
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties:
        message:
          type: string
    template:
      command: echo {{ .input.message }}
steps:
  - type: greet
    with:
      message: hello
    config:
      message: goodbye
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `fields "with" and "config" cannot be used together`)
}

func TestCustomStepTypes_TemplateSupportsHermeticFunctions(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-template-functions
step_types:
  format_message:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
        fallback:
          type: string
          default: ""
    template:
      exec:
        command: /bin/echo
        args:
          - '{{ .input.message | trim | upper | replace "HELLO" "HI" }}'
          - '{{ list "b" "a" "b" | uniq | sortAlpha | join "," }}'
          - '{{ .input.fallback | default "fallback" }}'
steps:
  - type: format_message
    config:
      message: " hello "
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	require.Len(t, step.Commands, 1)
	assert.Equal(t, []string{"HI", "a,b", "fallback"}, step.Commands[0].Args)
	assert.Equal(t, "format_message", step.ExecutorConfig.Metadata["custom_type"])
}

func TestCustomStepTypes_TemplateKeepsJSONHelper(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-json-helper
step_types:
  emit:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - '{{ json .input.message }}'
steps:
  - type: emit
    config:
      message: 'hello "quoted" world'
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	require.Len(t, step.Commands, 1)
	assert.Equal(t, []string{`"hello \"quoted\" world"`}, step.Commands[0].Args)
}

func TestCustomStepTypes_TemplateRejectsBlockedFunctions(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-template-blocked-functions
step_types:
  stamp:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
        args:
          - '{{ now }}'
steps:
  - type: stamp
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `function "now" not defined`)
}

func TestCustomStepTypes_HarnessCommandCanUseTypedInput(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-harness-typed-input
step_types:
  codex_task:
    type: harness
    input_schema:
      type: object
      additionalProperties: false
      required: [prompt]
      properties:
        prompt:
          type: string
    template:
      command:
        $input: prompt
      config:
        provider: codex
steps:
  - type: codex_task
    config:
      prompt: 'Review "quoted" text'
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "harness", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, `Review "quoted" text`, step.Commands[0].CmdWithArgs)
	assert.Equal(t, "codex_task", step.ExecutorConfig.Metadata["custom_type"])
}

func TestCustomStepTypes_RuntimeVariableInputsDeferSchemaValidation(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-runtime-inputs
step_types:
  run_with_inputs:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message, count, enabled, mode]
      properties:
        message:
          type: string
        count:
          type: integer
          minimum: 1
        enabled:
          type: boolean
        mode:
          enum: [fast, slow]
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
          - {$input: count}
          - {$input: enabled}
          - {$input: mode}
steps:
  - type: run_with_inputs
    with:
      message: hello-${SUFFIX}
      count: ${COUNT}
      enabled: ${ENABLED}
      mode: ${MODE}
`), WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	require.Len(t, step.Commands, 1)
	assert.Equal(t, []string{"hello-${SUFFIX}", "${COUNT}", "${ENABLED}", "${MODE}"}, step.Commands[0].Args)
}

func TestCustomStepTypes_RuntimeVariableInputMustBeWholeValueForNonStringTypes(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-runtime-input-invalid
step_types:
  repeat:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [count]
      properties:
        count:
          type: integer
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: count}
steps:
  - type: repeat
    with:
      count: count-${COUNT}
`), WithoutEval())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid "repeat" input`)
}

func TestCustomStepTypes_CommandTargetInheritsDAGLevelContainer(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-inherit-container
container:
  exec: shared-runner
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      command: echo {{ json .input.message }}
steps:
  - type: greet
    with:
      message: hello-from-container
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "container", step.ExecutorConfig.Type)
	assert.Equal(t, "greet", step.ExecutorConfig.Metadata["custom_type"])
	require.Len(t, step.Commands, 1)
	assert.Equal(t, []string{"hello-from-container"}, step.Commands[0].Args)
}

func TestCustomStepTypes_CommandTargetWithoutExecUsesImplicitCommandExecutor(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-implicit-command
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      command: echo {{ json .input.message }}
steps:
  - type: greet
    with:
      message: hello-inline
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Empty(t, step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "echo", step.Commands[0].Command)
	assert.Equal(t, []string{"hello-inline"}, step.Commands[0].Args)
}

func TestCustomStepTypes_BaseConfigRegistry(t *testing.T) {
	t.Parallel()

	baseYAML := []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
`)

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-base
steps:
  - type: greet
    with:
      message: hello-from-base
`), WithBaseConfigContent(baseYAML))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "greet_1", step.Name)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, []string{"hello-from-base"}, step.Commands[0].Args)
	assert.Equal(t, "greet", step.ExecutorConfig.Metadata["custom_type"])
}

func TestCustomStepTypes_BaseConfigRegistryNormalizesLookupKeys(t *testing.T) {
	t.Parallel()

	baseYAML := []byte(`
step_types:
  " greet ":
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
`)

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-base-normalized
steps:
  - type: greet
    with:
      message: hello-from-normalized-base
`), WithBaseConfigContent(baseYAML))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "greet_1", step.Name)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, []string{"hello-from-normalized-base"}, step.Commands[0].Args)
	assert.Equal(t, "greet", step.ExecutorConfig.Metadata["custom_type"])
}

func TestCustomStepTypes_BaseConfigDefaultsApplyBeforeTemplate(t *testing.T) {
	t.Parallel()

	baseYAML := []byte(`
defaults:
  env:
    - DEFAULT_ONLY: default-only
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
        args: [hello]
      env:
        - TEMPLATE_ONLY: template-only
`)

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-base-defaults
steps:
  - type: greet
`), WithBaseConfigContent(baseYAML))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, []string{"DEFAULT_ONLY=default-only", "TEMPLATE_ONLY=template-only"}, step.Env)
	assert.Equal(t, "greet", step.ExecutorConfig.Metadata["custom_type"])
}

func TestCustomStepTypes_DuplicateNameAcrossScopes(t *testing.T) {
	t.Parallel()

	baseYAML := []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
`)

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-duplicate
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
steps:
  - type: greet
`), WithBaseConfigContent(baseYAML))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate legacy step_types definition "greet"`)
}

func TestCustomStepTypes_DuplicateNameAcrossScopesAfterNormalization(t *testing.T) {
	t.Parallel()

	baseYAML := []byte(`
step_types:
  " greet ":
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
`)

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-duplicate-normalized
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
steps:
  - type: greet
`), WithBaseConfigContent(baseYAML))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate legacy step_types definition "greet"`)
}

func TestCustomStepTypes_RejectsForbiddenCallSiteFields(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-forbidden
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
steps:
  - type: greet
    command: echo should-fail
    with:
      message: hello
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `legacy step_types definition "greet"`)
	assert.Contains(t, err.Error(), `field "command" is not allowed`)
}

func TestCustomStepTypes_HandlerSupport(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-handler
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
handler_on:
  success:
    type: greet
    with:
      message: handler-ok
steps:
  - run: echo run
`))
	require.NoError(t, err)
	require.NotNil(t, dag.HandlerOn.Success)
	assert.Equal(t, "onSuccess", dag.HandlerOn.Success.Name)
	assert.Equal(t, "direct", dag.HandlerOn.Success.Shell)
	assert.Equal(t, "greet", dag.HandlerOn.Success.ExecutorConfig.Metadata["custom_type"])
	require.Len(t, dag.HandlerOn.Success.Commands, 1)
	assert.Equal(t, []string{"handler-ok"}, dag.HandlerOn.Success.Commands[0].Args)
}

func TestCustomStepTypes_HandlerRejectsWithAndLegacyConfig(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: custom-step-handler-mixed-config
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties:
        message:
          type: string
    template:
      command: echo {{ .input.message }}
handler_on:
  success:
    type: greet
    with:
      message: hello
    config:
      message: goodbye
steps:
  - run: echo run
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `fields "with" and "config" cannot be used together`)
}

func TestCustomStepTypes_HandlerAllowsExplicitZeroValueOverrides(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-handler-zero-overrides
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
      timeout_sec: 15
      mail_on_error: true
handler_on:
  success:
    type: greet
    with:
      message: handler-ok
    timeout_sec: 0
    mail_on_error: false
steps:
  - run: echo run
`))
	require.NoError(t, err)
	require.NotNil(t, dag.HandlerOn.Success)
	assert.Equal(t, "onSuccess", dag.HandlerOn.Success.Name)
	assert.Zero(t, dag.HandlerOn.Success.Timeout)
	assert.False(t, dag.HandlerOn.Success.MailOnError)
	assert.Equal(t, "greet", dag.HandlerOn.Success.ExecutorConfig.Metadata["custom_type"])
}

func TestCustomStepTypes_TemplateFieldsOverrideDefaults(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-template-overrides-defaults
defaults:
  continue_on: failed
  retry_policy:
    limit: 1
    interval_sec: 60
  repeat_policy:
    repeat: while
    condition: "true"
    interval_sec: 30
  timeout_sec: 600
  mail_on_error: true
  signal_on_stop: SIGTERM
  env:
    - DEFAULT_ONLY: default-only
  preconditions:
    - condition: "test -f /default"
step_types:
  layered:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
        args: [hello]
      continue_on: skipped
      retry_policy:
        limit: 5
        interval_sec: 2
      repeat_policy:
        repeat: until
        condition: "cat /tmp/status"
        expected: done
        interval_sec: 7
      timeout_sec: 9
      mail_on_error: false
      signal_on_stop: SIGINT
      env:
        - TEMPLATE_ONLY: template-only
      preconditions:
        - condition: "test -d /template"
steps:
  - type: layered
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.True(t, step.ContinueOn.Skipped)
	assert.False(t, step.ContinueOn.Failure)
	assert.Equal(t, 5, step.RetryPolicy.Limit)
	assert.Equal(t, 2*time.Second, step.RetryPolicy.Interval)
	assert.Equal(t, core.RepeatModeUntil, step.RepeatPolicy.RepeatMode)
	require.NotNil(t, step.RepeatPolicy.Condition)
	assert.Equal(t, "cat /tmp/status", step.RepeatPolicy.Condition.Condition)
	assert.Equal(t, "done", step.RepeatPolicy.Condition.Expected)
	assert.Equal(t, 7*time.Second, step.RepeatPolicy.Interval)
	assert.Equal(t, 9*time.Second, step.Timeout)
	assert.False(t, step.MailOnError)
	assert.Equal(t, "SIGINT", step.SignalOnStop)
	assert.Equal(t, []string{"DEFAULT_ONLY=default-only", "TEMPLATE_ONLY=template-only"}, step.Env)
	require.Len(t, step.Preconditions, 2)
	assert.Equal(t, "test -f /default", step.Preconditions[0].Condition)
	assert.Equal(t, "test -d /template", step.Preconditions[1].Condition)
}

func TestCustomStepTypes_CallSiteOverridesTemplateAndComposesAdditiveFields(t *testing.T) {
	t.Parallel()

	callSiteSignal := "SIGQUIT"
	if runtime.GOOS == "windows" {
		callSiteSignal = "SIGINT"
	}

	dag, err := LoadYAML(context.Background(), fmt.Appendf(nil, `
name: custom-step-callsite-overrides-template
defaults:
  continue_on: failed
  retry_policy:
    limit: 1
    interval_sec: 60
  repeat_policy:
    repeat: until
    condition: "cat /tmp/default"
    expected: ready
    interval_sec: 12
  timeout_sec: 600
  mail_on_error: true
  signal_on_stop: SIGTERM
  env:
    - LAYERED: default
    - DEFAULT_ONLY: default-only
  preconditions:
    - condition: "test -f /default"
step_types:
  layered:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
        args: [hello]
      continue_on: skipped
      retry_policy:
        limit: 5
        interval_sec: 2
      repeat_policy:
        repeat: while
        condition: "test -d /template"
        interval_sec: 7
      timeout_sec: 9
      mail_on_error: true
      signal_on_stop: SIGINT
      env:
        - LAYERED: template
        - TEMPLATE_ONLY: template-only
      preconditions:
        - condition: "test -d /template"
steps:
  - type: layered
    continue_on:
      failed: true
    retry_policy:
      limit: 7
      interval_sec: 3
    repeat_policy:
      repeat: until
      condition: "cat /tmp/call"
      expected: done
      interval_sec: 11
    timeout_sec: 0
    mail_on_error: false
    signal_on_stop: %s
    env:
      - LAYERED: call
      - CALL_ONLY: call-only
    preconditions:
      - condition: "test -x /call"
`, callSiteSignal))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.True(t, step.ContinueOn.Failure)
	assert.False(t, step.ContinueOn.Skipped)
	assert.Equal(t, 7, step.RetryPolicy.Limit)
	assert.Equal(t, 3*time.Second, step.RetryPolicy.Interval)
	assert.Equal(t, core.RepeatModeUntil, step.RepeatPolicy.RepeatMode)
	require.NotNil(t, step.RepeatPolicy.Condition)
	assert.Equal(t, "cat /tmp/call", step.RepeatPolicy.Condition.Condition)
	assert.Equal(t, "done", step.RepeatPolicy.Condition.Expected)
	assert.Equal(t, 11*time.Second, step.RepeatPolicy.Interval)
	assert.Zero(t, step.Timeout)
	assert.False(t, step.MailOnError)
	assert.Equal(t, callSiteSignal, step.SignalOnStop)
	assert.Equal(t, []string{
		"LAYERED=default",
		"DEFAULT_ONLY=default-only",
		"LAYERED=template",
		"TEMPLATE_ONLY=template-only",
		"LAYERED=call",
		"CALL_ONLY=call-only",
	}, step.Env)
	require.Len(t, step.Preconditions, 3)
	assert.Equal(t, "test -f /default", step.Preconditions[0].Condition)
	assert.Equal(t, "test -d /template", step.Preconditions[1].Condition)
	assert.Equal(t, "test -x /call", step.Preconditions[2].Condition)
}

func TestCustomStepTypes_HandlerTemplateFieldsOverrideDefaults(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: custom-step-handler-default-precedence
defaults:
  timeout_sec: 600
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
      timeout_sec: 15
handler_on:
  success:
    type: greet
    with:
      message: handler-ok
steps:
  - run: echo run
`))
	require.NoError(t, err)
	require.NotNil(t, dag.HandlerOn.Success)
	assert.Equal(t, "onSuccess", dag.HandlerOn.Success.Name)
	assert.Equal(t, 15*time.Second, dag.HandlerOn.Success.Timeout)
	assert.Equal(t, "greet", dag.HandlerOn.Success.ExecutorConfig.Metadata["custom_type"])
}

func TestStepExec_BuildsDirectCommand(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
name: exec-step
steps:
  - action: exec
    with:
      command: /bin/echo
      args: [hello, 3, true]
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "cmd_1", step.Name)
	assert.Equal(t, "direct", step.Shell)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "/bin/echo", step.Commands[0].Command)
	assert.Equal(t, []string{"hello", "3", "true"}, step.Commands[0].Args)
}

func TestLegacyStepExec_RejectsCommandConflict(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
name: exec-step-invalid
steps:
  - command: echo hello
    exec:
      command: /bin/echo
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec cannot be used together with command")
}

func TestValidateCustomStepInput_DefersRuntimeExpressionLeaves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		schema    map[string]any
		input     map[string]any
		assertErr assert.ErrorAssertionFunc
	}{
		{
			name: "IntegerWholeRuntimeExpression",
			schema: objectInputSchema(map[string]any{
				"count": map[string]any{"type": "integer"},
			}, "count"),
			input:     map[string]any{"count": "${COUNT}"},
			assertErr: assert.NoError,
		},
		{
			name: "BooleanWholeRuntimeExpression",
			schema: objectInputSchema(map[string]any{
				"enabled": map[string]any{"type": "boolean"},
			}, "enabled"),
			input:     map[string]any{"enabled": "$ENABLED"},
			assertErr: assert.NoError,
		},
		{
			name: "EnumWholeRuntimeExpression",
			schema: objectInputSchema(map[string]any{
				"mode": map[string]any{"enum": []any{"fast", "slow"}},
			}, "mode"),
			input:     map[string]any{"mode": "${MODE}"},
			assertErr: assert.NoError,
		},
		{
			name: "StringEnumEmbeddedRuntimeExpressionRejected",
			schema: objectInputSchema(map[string]any{
				"mode": map[string]any{"enum": []any{"fast", "slow"}},
			}, "mode"),
			input:     map[string]any{"mode": "fast-${MODE}"},
			assertErr: assert.Error,
		},
		{
			name: "StringEmbeddedRuntimeExpression",
			schema: objectInputSchema(map[string]any{
				"message": map[string]any{"type": "string"},
			}, "message"),
			input:     map[string]any{"message": "hello-${NAME}"},
			assertErr: assert.NoError,
		},
		{
			name: "NestedIntegerRuntimeExpression",
			schema: objectInputSchema(map[string]any{
				"limits": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count": map[string]any{"type": "integer"},
					},
					"required": []any{"count"},
				},
			}, "limits"),
			input:     map[string]any{"limits": map[string]any{"count": "${COUNT}"}},
			assertErr: assert.NoError,
		},
		{
			name: "ArrayIntegerRuntimeExpression",
			schema: objectInputSchema(map[string]any{
				"counts": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "integer"},
				},
			}, "counts"),
			input:     map[string]any{"counts": []any{"${COUNT}"}},
			assertErr: assert.NoError,
		},
		{
			name: "ArrayRuntimeExpressionDoesNotHideInvalidSibling",
			schema: objectInputSchema(map[string]any{
				"counts": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "integer"},
				},
			}, "counts"),
			input:     map[string]any{"counts": []any{"${COUNT}", "abc"}},
			assertErr: assert.Error,
		},
		{
			name: "AdditionalPropertiesRuntimeExpression",
			schema: map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "integer"},
			},
			input:     map[string]any{"us": "${US_COUNT}"},
			assertErr: assert.NoError,
		},
		{
			name: "AdditionalPropertiesRuntimeExpressionDoesNotHideInvalidSibling",
			schema: map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "integer"},
			},
			input:     map[string]any{"us": "${US_COUNT}", "eu": "abc"},
			assertErr: assert.Error,
		},
		{
			name: "RefRuntimeExpression",
			schema: map[string]any{
				"$defs": map[string]any{
					"count": map[string]any{"type": "integer", "minimum": 1},
				},
				"type":                 "object",
				"additionalProperties": false,
				"required":             []any{"count"},
				"properties": map[string]any{
					"count": map[string]any{"$ref": "#/$defs/count"},
				},
			},
			input:     map[string]any{"count": "${COUNT}"},
			assertErr: assert.NoError,
		},
		{
			name: "IntegerEmbeddedRuntimeExpressionRejected",
			schema: objectInputSchema(map[string]any{
				"count": map[string]any{"type": "integer"},
			}, "count"),
			input:     map[string]any{"count": "count-${COUNT}"},
			assertErr: assert.Error,
		},
		{
			name: "NonRuntimeInvalidStillRejected",
			schema: objectInputSchema(map[string]any{
				"count": map[string]any{"type": "integer"},
			}, "count"),
			input:     map[string]any{"count": "abc"},
			assertErr: assert.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			schema := mustResolveCustomStepInputSchema(t, tt.schema)
			_, err := validateCustomStepInput("test", schema, "with", tt.input)
			tt.assertErr(t, err)
		})
	}
}

func objectInputSchema(properties map[string]any, required ...string) map[string]any {
	requiredValues := make([]any, 0, len(required))
	for _, name := range required {
		requiredValues = append(requiredValues, name)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             requiredValues,
		"properties":           properties,
	}
}

func mustResolveCustomStepInputSchema(t *testing.T, schema map[string]any) *jsonschema.Resolved {
	t.Helper()

	resolved, err := resolveCustomStepTypeInputSchema("test", schema)
	require.NoError(t, err)
	return resolved
}
