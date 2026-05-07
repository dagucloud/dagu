// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepSchemaV2_Run(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: hello
    run: echo hello
    with:
      shell: bash -e
      shell_args: [-c]
      shell_packages: [curl]
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "hello", step.ID)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "echo", step.Commands[0].Command)
	assert.Equal(t, []string{"hello"}, step.Commands[0].Args)
	assert.Equal(t, "bash", step.Shell)
	assert.Equal(t, []string{"-e", "-c"}, step.ShellArgs)
	assert.Equal(t, []string{"curl"}, step.ShellPackages)
}

func TestStepSchemaV2_RunRejectsMixedExecutionFields(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
steps:
  - run: echo hello
    command: echo legacy
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `run cannot be used together with command`)
}

func TestStepSchemaV2_ActionDagRunParallel(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: fanout
    parallel:
      items:
        - id: a
          region: us
      max_concurrent: 3
    action: dag.run
    with:
      dag: account_workflow
      params:
        account_id: ${ITEM.id}
        region: ${ITEM.region}
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, core.ExecutorTypeParallel, step.ExecutorConfig.Type)
	require.NotNil(t, step.SubDAG)
	assert.Equal(t, "account_workflow", step.SubDAG.Name)
	assert.Contains(t, step.SubDAG.Params, `${ITEM.id}`)
	assert.Contains(t, step.SubDAG.Params, `${ITEM.region}`)
	require.NotNil(t, step.Parallel)
	assert.Equal(t, 3, step.Parallel.MaxConcurrent)
	require.Len(t, step.Parallel.Items, 1)
	assert.Equal(t, "a", step.Parallel.Items[0].Params["id"])
}

func TestStepSchemaV2_ActionParallelRejectsNonDAG(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: invalid
    parallel: [a, b]
    action: http.request
    with:
      method: GET
      url: https://example.com
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `parallel currently requires action: dag.run`)
}

func TestStepSchemaV2_ActionHTTPRequest(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: request
    action: http.request
    with:
      method: POST
      url: https://example.com/api
      headers:
        X-Test: ok
      body: '{"ok":true}'
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "http", step.ExecutorConfig.Type)
	assert.Empty(t, step.Commands)
	assert.Equal(t, "POST", step.ExecutorConfig.Config["method"])
	assert.Equal(t, "https://example.com/api", step.ExecutorConfig.Config["url"])
	assert.Equal(t, `{"ok":true}`, step.ExecutorConfig.Config["body"])
}

func TestStepSchemaV2_CustomActions(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
actions:
  slack.notify:
    description: Send Slack notification
    input_schema:
      type: object
      additionalProperties: false
      required: [text]
      properties:
        text:
          type: string
    template:
      action: http.request
      with:
        method: POST
        url: ${SLACK_WEBHOOK_URL}
        body: {$input: text}
steps:
  - action: slack.notify
    with:
      text: hello
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "slack.notify_1", step.Name)
	assert.Equal(t, "http", step.ExecutorConfig.Type)
	assert.Equal(t, "POST", step.ExecutorConfig.Config["method"])
	assert.Equal(t, "hello", step.ExecutorConfig.Config["body"])
	assert.Equal(t, "slack.notify", step.ExecutorConfig.Metadata["custom_type"])
	assert.Equal(t, "Send Slack notification", step.Description)
}

func TestStepSchemaV2_CustomActionsFromBaseConfig(t *testing.T) {
	t.Parallel()

	baseYAML := []byte(`
actions:
  greet:
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      run: echo {{ .input.message }}
`)

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - action: greet
    with:
      message: hello
`), WithBaseConfigContent(baseYAML))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "greet_1", step.Name)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "echo", step.Commands[0].Command)
	assert.Equal(t, []string{"hello"}, step.Commands[0].Args)
	assert.Equal(t, "greet", step.ExecutorConfig.Metadata["custom_type"])
}

func TestStepSchemaV2_HandlerSupportsRun(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - run: echo main
handler_on:
  success:
    run: echo success
`))
	require.NoError(t, err)
	require.NotNil(t, dag.HandlerOn.Success)
	require.Len(t, dag.HandlerOn.Success.Commands, 1)
	assert.Equal(t, "echo", dag.HandlerOn.Success.Commands[0].Command)
	assert.Equal(t, []string{"success"}, dag.HandlerOn.Success.Commands[0].Args)
}
