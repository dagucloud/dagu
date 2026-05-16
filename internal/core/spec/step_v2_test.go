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

func TestStepSchemaV2_ActionDagEnqueue(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: enqueue_child
    action: dag.enqueue
    with:
      dag: account_workflow
      params:
        account_id: "42"
      queue: background
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, core.ExecutorTypeDAGEnqueue, step.ExecutorConfig.Type)
	require.NotNil(t, step.SubDAG)
	assert.Equal(t, "account_workflow", step.SubDAG.Name)
	assert.Contains(t, step.SubDAG.Params, `account_id="42"`)
	assert.Equal(t, "background", step.ExecutorConfig.Config["queue"])
}

func TestStepSchemaV2_ActionDagEnqueueParallel(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: enqueue_fanout
    parallel:
      items:
        - id: a
          region: us
        - id: b
          region: eu
      max_concurrent: 2
    action: dag.enqueue
    with:
      dag: account_workflow
      params:
        account_id: ${ITEM.id}
        region: ${ITEM.region}
      queue: background
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, core.ExecutorTypeDAGEnqueue, step.ExecutorConfig.Type)
	require.NotNil(t, step.SubDAG)
	assert.Equal(t, "account_workflow", step.SubDAG.Name)
	assert.Contains(t, step.SubDAG.Params, `${ITEM.id}`)
	assert.Contains(t, step.SubDAG.Params, `${ITEM.region}`)
	assert.Equal(t, "background", step.ExecutorConfig.Config["queue"])
	require.NotNil(t, step.Parallel)
	assert.Equal(t, 2, step.Parallel.MaxConcurrent)
	require.Len(t, step.Parallel.Items, 2)
	assert.Equal(t, "a", step.Parallel.Items[0].Params["id"])
	assert.Equal(t, "eu", step.Parallel.Items[1].Params["region"])
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
	assert.Contains(t, err.Error(), `parallel currently requires action: dag.run or dag.enqueue`)
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

func TestStepSchemaV2_ActionDockerRunAllowsImageDefaultCommand(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: run_image_default
    action: docker.run
    with:
      image: alpine:3.20
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "docker", step.ExecutorConfig.Type)
	assert.Empty(t, step.Commands)
	assert.Equal(t, "alpine:3.20", step.ExecutorConfig.Config["image"])
}

func TestStepSchemaV2_ActionGitCheckout(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: checkout
    action: git.checkout
    with:
      repository: https://example.com/acme/app.git
      ref: main
      path: ./workspace/app
      depth: 1
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "git", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "checkout", step.Commands[0].Command)
	assert.Equal(t, "https://example.com/acme/app.git", step.ExecutorConfig.Config["repository"])
	assert.Equal(t, "main", step.ExecutorConfig.Config["ref"])
	assert.Equal(t, "./workspace/app", step.ExecutorConfig.Config["path"])
	assert.Equal(t, uint64(1), step.ExecutorConfig.Config["depth"])
}

func TestStepSchemaV2_ActionJQFilterData(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: pick_name
    action: jq.filter
    with:
      filter: .name
      data:
        name: Alice
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "jq", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, ".name", step.Commands[0].CmdWithArgs)
	assert.JSONEq(t, `{"name":"Alice"}`, step.Script)
}

func TestStepSchemaV2_ActionJQFilterRejectsDataAndInput(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: pick_name
    action: jq.filter
    with:
      filter: .name
      data:
        name: Alice
      input: /tmp/input.json
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `jq.filter does not allow both with.data and with.input`)
}

func TestStepSchemaV2_ActionDataConvert(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: convert_users
    action: data.convert
    with:
      from: csv
      to: json
      data: |
        name,age
        Alice,30
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "data", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "convert", step.Commands[0].Command)
	require.NotNil(t, step.ExecutorConfig.Config)
	assert.Equal(t, "csv", step.ExecutorConfig.Config["from"])
	assert.Equal(t, "json", step.ExecutorConfig.Config["to"])
	assert.Contains(t, step.ExecutorConfig.Config, "data")
}

func TestStepSchemaV2_ActionDataPick(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: pick_image
    action: data.pick
    with:
      from: yaml
      select: .spec.containers[0].image
      raw: true
      data:
        spec:
          containers:
            - image: nginx:1.27
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "data", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "pick", step.Commands[0].Command)
	require.NotNil(t, step.ExecutorConfig.Config)
	assert.Equal(t, "yaml", step.ExecutorConfig.Config["from"])
	assert.Equal(t, ".spec.containers[0].image", step.ExecutorConfig.Config["select"])
	assert.Equal(t, true, step.ExecutorConfig.Config["raw"])
}

func TestStepSchemaV2_ActionSQLQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action       string
		executorType string
		dsn          string
	}{
		{
			action:       "postgres.query",
			executorType: "postgres",
			dsn:          "${DATABASE_URL}",
		},
		{
			action:       "sqlite.query",
			executorType: "sqlite",
			dsn:          ":memory:",
		},
		{
			action:       "duckdb.query",
			executorType: "duckdb",
			dsn:          ":memory:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: query_users
    action: `+tt.action+`
    with:
      dsn: "`+tt.dsn+`"
      query: SELECT 1 AS ok
      output_format: jsonl
`))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			step := dag.Steps[0]
			assert.Equal(t, tt.executorType, step.ExecutorConfig.Type)
			require.Len(t, step.Commands, 1)
			assert.Equal(t, "SELECT", step.Commands[0].Command)
			assert.Equal(t, "SELECT 1 AS ok", step.Commands[0].CmdWithArgs)
			require.NotNil(t, step.ExecutorConfig.Config)
			assert.Equal(t, tt.dsn, step.ExecutorConfig.Config["dsn"])
			assert.Equal(t, "jsonl", step.ExecutorConfig.Config["output_format"])
			assert.NotContains(t, step.ExecutorConfig.Config, "query")
		})
	}
}

func TestStepSchemaV2_ActionSQLImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action       string
		executorType string
		dsn          string
	}{
		{
			action:       "postgres.import",
			executorType: "postgres",
			dsn:          "${DATABASE_URL}",
		},
		{
			action:       "sqlite.import",
			executorType: "sqlite",
			dsn:          "/data/users.sqlite",
		},
		{
			action:       "duckdb.import",
			executorType: "duckdb",
			dsn:          "/data/users.duckdb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: import_users
    action: `+tt.action+`
    with:
      dsn: "`+tt.dsn+`"
      import:
        input_file: /data/users.csv
        table: users
`))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			step := dag.Steps[0]
			assert.Equal(t, tt.executorType, step.ExecutorConfig.Type)
			assert.Empty(t, step.Commands)
			require.NotNil(t, step.ExecutorConfig.Config)
			assert.Equal(t, tt.dsn, step.ExecutorConfig.Config["dsn"])
			assert.Contains(t, step.ExecutorConfig.Config, "import")
		})
	}
}

func TestStepSchemaV2_ActionFileOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action string
		op     string
		with   string
	}{
		{
			action: "file.stat",
			op:     "stat",
			with:   "path: ./input.txt",
		},
		{
			action: "file.read",
			op:     "read",
			with:   "path: ./input.txt",
		},
		{
			action: "file.write",
			op:     "write",
			with: `path: ./output.txt
      content: hello`,
		},
		{
			action: "file.copy",
			op:     "copy",
			with: `source: ./input.txt
      destination: ./output.txt`,
		},
		{
			action: "file.move",
			op:     "move",
			with: `source: ./input.txt
      destination: ./output.txt`,
		},
		{
			action: "file.delete",
			op:     "delete",
			with:   "path: ./output.txt",
		},
		{
			action: "file.mkdir",
			op:     "mkdir",
			with:   "path: ./out",
		},
		{
			action: "file.list",
			op:     "list",
			with:   "path: ./out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: file_step
    action: `+tt.action+`
    with:
      `+tt.with+`
`))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			step := dag.Steps[0]
			assert.Equal(t, "file", step.ExecutorConfig.Type)
			require.Len(t, step.Commands, 1)
			assert.Equal(t, tt.op, step.Commands[0].Command)
			assert.NotEmpty(t, step.ExecutorConfig.Config)
		})
	}
}

func TestStepSchemaV2_ActionWaitOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action string
		op     string
		with   string
	}{
		{
			action: "wait.duration",
			op:     "duration",
			with:   "duration: 10s",
		},
		{
			action: "wait.until",
			op:     "until",
			with:   "until: 2026-01-02T03:04:05Z",
		},
		{
			action: "wait.file",
			op:     "file",
			with: `path: ./ready.flag
      state: exists`,
		},
		{
			action: "wait.http",
			op:     "http",
			with: `url: https://example.com/health
      status: 204`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: wait_step
    action: `+tt.action+`
    with:
      `+tt.with+`
`))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			step := dag.Steps[0]
			assert.Equal(t, "wait", step.ExecutorConfig.Type)
			require.Len(t, step.Commands, 1)
			assert.Equal(t, tt.op, step.Commands[0].Command)
			assert.NotEmpty(t, step.ExecutorConfig.Config)
		})
	}
}

func TestStepSchemaV2_ActionArtifactOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action string
		op     string
		with   string
	}{
		{
			action: "artifact.write",
			op:     "write",
			with: `path: reports/summary.md
      content: hello`,
		},
		{
			action: "artifact.read",
			op:     "read",
			with:   "path: reports/summary.md",
		},
		{
			action: "artifact.list",
			op:     "list",
			with:   "path: reports",
		},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: artifact_step
    action: `+tt.action+`
    with:
      `+tt.with+`
`))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)

			step := dag.Steps[0]
			assert.Equal(t, "artifact", step.ExecutorConfig.Type)
			require.Len(t, step.Commands, 1)
			assert.Equal(t, tt.op, step.Commands[0].Command)
			assert.NotEmpty(t, step.ExecutorConfig.Config)
		})
	}
}

func TestStepSchemaV2_ActionArtifactListAllowsOmittedWith(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: list_artifacts
    action: artifact.list
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "artifact", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "list", step.Commands[0].Command)
	assert.Empty(t, step.ExecutorConfig.Config)
}

func TestStepSchemaV2_StdoutArtifact(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: report
    run: ./generate-report
    stdout:
      artifact: reports/report.md
    stderr:
      artifact: reports/report.err
`))
	require.NoError(t, err)
	require.NotNil(t, dag.Artifacts)
	assert.True(t, dag.Artifacts.Enabled)
	require.Len(t, dag.Steps, 1)
	assert.Empty(t, dag.Steps[0].Stdout)
	assert.Equal(t, "reports/report.md", dag.Steps[0].StdoutArtifact)
	assert.Empty(t, dag.Steps[0].Stderr)
	assert.Equal(t, "reports/report.err", dag.Steps[0].StderrArtifact)
}

func TestStepSchemaV2_StdoutArtifactRejectsDisabledArtifacts(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
artifacts:
  enabled: false
steps:
  - id: report
    run: ./generate-report
    stdout:
      artifact: reports/report.md
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artifact outputs require artifacts.enabled to be true")
}

func TestStepSchemaV2_ActionHarnessStdin(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
steps:
  - id: review
    action: harness.run
    with:
      provider: codex
      prompt: Review this patch
      stdin: |
        diff --git a/main.go b/main.go
        ...
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	assert.Equal(t, "harness", step.ExecutorConfig.Type)
	require.Len(t, step.Commands, 1)
	assert.Equal(t, "Review this patch", step.Commands[0].CmdWithArgs)
	assert.Contains(t, step.Script, "diff --git")
	require.NotNil(t, step.ExecutorConfig.Config)
	assert.NotContains(t, step.ExecutorConfig.Config, "stdin")
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
    output_schema:
      type: object
      additionalProperties: false
      required: [ok]
      properties:
        ok:
          type: boolean
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
	require.NotNil(t, step.OutputSchema)
	assert.Equal(t, "object", step.OutputSchema["type"])
}

func TestStepSchemaV2_CustomActionRejectsLegacyTemplateFields(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
actions:
  bad.notify:
    input_schema:
      type: object
    template:
      action: log.write
      command: echo legacy
      with:
        message: hello
steps:
  - action: bad.notify
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template contains deprecated execution keys: [command]")
}

func TestStepSchemaV2_CustomActionErrorContext(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
actions:
  bad.log:
    input_schema:
      type: object
    template:
      action: log.write
      with: {}
steps:
  - action: bad.log
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `custom action "bad.log": failed to normalize expanded template`)
	assert.Contains(t, err.Error(), "with.message is required")
}

func TestStepSchemaV2_CustomActionsCanComposeCustomActions(t *testing.T) {
	t.Parallel()

	dag, err := LoadYAML(context.Background(), []byte(`
actions:
  http.notify:
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
        url: ${WEBHOOK_URL}
        body: {$input: text}
  slack.notify:
    input_schema:
      type: object
      additionalProperties: false
      required: [text]
      properties:
        text:
          type: string
    template:
      action: http.notify
      with:
        text: {$input: text}
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
}

func TestStepSchemaV2_CustomActionsRejectRecursiveReferences(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(context.Background(), []byte(`
actions:
  loop.a:
    input_schema:
      type: object
    template:
      action: loop.b
  loop.b:
    input_schema:
      type: object
    template:
      action: loop.a
steps:
  - action: loop.a
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recursive custom action reference: loop.a -> loop.b -> loop.a")
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
