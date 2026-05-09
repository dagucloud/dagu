// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestCustomStepTypes_DAGLocalExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct exec integration uses /bin/echo")
	}
	t.Parallel()

	th := test.Setup(t)

	dag := th.DAG(t, `
name: custom-step-local
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
        repeat:
          type: integer
          default: 2
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
          - {$input: repeat}
steps:
  - name: greet-user
    type: greet
    with:
      message: "*.go"
    output: OUT
`)
	dag.Agent().RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"OUT": "*.go 2",
	})
}

func TestCustomStepTypes_RuntimeVariableInputExpandsAtExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct exec integration uses /bin/echo")
	}
	t.Parallel()

	th := test.Setup(t)

	dag := th.DAG(t, `
name: custom-step-runtime-variable-input
type: graph
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
  - id: produce
    action: exec
    with:
      command: /bin/echo
      args: [3]
    output: COUNT
  - id: consume
    depends: [produce]
    type: repeat
    with:
      count: ${COUNT}
    output: OUT
`)
	dag.Agent().RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"OUT": "3",
	})
}

func TestCustomStepTypes_TemplateRuntimeVariableExpandsAtExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct exec integration uses /bin/echo")
	}
	t.Parallel()

	th := test.Setup(t)

	dag := th.DAG(t, `
name: custom-step-template-runtime-variable
type: graph
step_types:
  echo_count:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/echo
        args:
          - ${COUNT}
steps:
  - id: produce
    action: exec
    with:
      command: /bin/echo
      args: [7]
    output: COUNT
  - id: consume
    depends: [produce]
    type: echo_count
    output: OUT
`)
	dag.Agent().RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"OUT": "7",
	})
}

func TestCustomStepTypes_DAGLocalScriptShebang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script shebang integration uses /bin/sh and /bin/bash")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash is required for the shebang integration test")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is required for the shebang integration test")
	}
	t.Parallel()

	th := test.Setup(t)

	dag := th.DAG(t, `
name: custom-step-script-shebang
shell: /bin/sh
step_types:
  bash_snippet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      script: |
        #!/bin/bash
        msg={{ json .input.message }}
        parts=("ignored" "$msg")
        printf '%s\n' "${parts[1]}"
steps:
  - name: run-bash-template
    type: bash_snippet
    with:
      message: xxx
    output: OUT
`)
	dag.Agent().RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"OUT": "xxx",
	})
}

func TestCustomStepTypes_BaseConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("direct exec integration uses /bin/echo")
	}
	t.Parallel()

	th := test.Setup(t)
	require.NoError(t, os.WriteFile(th.Config.Paths.BaseConfig, []byte(`
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
`), 0600))

	dag := th.DAG(t, `
name: custom-step-base
steps:
  - name: greet-user
    type: greet
    with:
      message: "hello from base"
    output: OUT
`)
	dag.Agent().RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"OUT": "hello from base",
	})
}

func TestCustomStepTypes_DefaultPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration uses /bin/sh")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is required for the env precedence integration test")
	}
	t.Parallel()

	th := test.Setup(t)

	dag := th.DAG(t, `
name: custom-step-default-precedence
defaults:
  env:
    - LAYERED: default
    - DEFAULT_ONLY: default-only
step_types:
  show_env:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      properties: {}
    template:
      exec:
        command: /bin/sh
        args:
          - -c
          - printf '%s|%s|%s' "$LAYERED" "$DEFAULT_ONLY" "$TEMPLATE_ONLY"
      env:
        - LAYERED: template
        - TEMPLATE_ONLY: template-only
steps:
  - name: show-layered-env
    type: show_env
    env:
      - LAYERED: call
    output: OUT
`)
	dag.Agent().RunSuccess(t)

	dag.AssertOutputs(t, map[string]any{
		"OUT": "call|default-only|template-only",
	})
}
