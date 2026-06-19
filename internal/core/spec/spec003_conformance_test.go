// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpec003ConformanceValueResolvedFieldSurfaces(t *testing.T) {
	t.Parallel()

	dag := loadSpec003DAG(t, `
name: spec003
description: literal description
params:
  - name: environment
    default: prod
  - name: retry_limit
    default: "3"
  - name: retry_interval
    default: "5"
  - name: repeat_limit
    default: "2"
  - name: repeat_interval
    default: "7"
  - name: repeat_max_interval
    default: "11"
  - name: repeat_condition
    default: "keep-going"
env:
  - ROOT_VALUE=${params.environment}
dotenv:
  - .env.${params.environment}
shell: bash
shell_args:
  - -e
  - ${params.environment}
working_dir: /tmp/${params.environment}
preconditions:
  - condition: ready-${params.environment}
container:
  image: repo/${params.environment}:latest
  env:
    - CONTAINER_VALUE=${params.environment}
steps:
  - id: publish
    run: echo ${params.environment}
    working_dir: /work/${params.environment}
    env:
      - STEP_VALUE=${params.environment}
    preconditions:
      - condition: check-${params.environment}
    retry_policy:
      limit: ${params.retry_limit}
      interval_sec: ${params.retry_interval}
    repeat_policy:
      repeat: while
      condition: ${params.repeat_condition}
      limit: ${params.repeat_limit}
      interval_sec: ${params.repeat_interval}
      max_interval_sec: ${params.repeat_max_interval}
    stdout:
      artifact: stdout/${params.environment}.log
      outputs:
        fields:
          image:
            value: repo:${params.environment}
    stderr:
      artifact: stderr/${params.environment}.log
    output:
      digest:
        value: sha:${params.environment}
      report:
        from: file
        path: outputs/${params.environment}.txt
    outputs:
      - name: literal_name
        type: string

  - id: container_step
    action: harness.run
    container:
      image: step:${params.environment}
    with:
      prompt: review ${params.environment}
      provider: codex

  - id: log_message
    action: log.write
    with:
      message: deploy ${params.environment}

  - id: child
    action: dag.run
    with:
      dag: child-${params.environment}
      params: ENV=${params.environment}

  - id: fanout
    parallel:
      items:
        - ${params.environment}
        - target: ${params.environment}
    action: dag.run
    with:
      dag: fanout-${params.environment}
      params:
        env: ${params.environment}

  - id: chat
    action: chat.completion
    with:
      model:
        - provider: openai
          name: gpt-4o
          base_url: https://${params.environment}.example/v1
          api_key_name: OPENAI_API_KEY
      system: system ${params.environment}
      base_url: https://fallback-${params.environment}.example/v1
      tools:
        - literal_tool
      messages:
        - role: user
          content: hello ${params.environment}

  - id: render
    action: template.render
    with:
      template: literal ${params.environment}
      data:
        message: resolved ${params.environment}
`)

	resolved := resolveSpec003Fields(t, dag, cmnvalue.Values{
		"environment":         "prod",
		"retry_limit":         "3",
		"retry_interval":      "5",
		"repeat_limit":        "2",
		"repeat_interval":     "7",
		"repeat_max_interval": "11",
		"repeat_condition":    "keep-going",
	})

	assert.Equal(t, "prod", resolved["env[0]"])
	assert.Equal(t, ".env.prod", resolved["dotenv[0]"])
	assert.Equal(t, "prod", resolved["shell_args[1]"])
	assert.Equal(t, "/tmp/prod", resolved["working_dir"])
	assert.Equal(t, "ready-prod", resolved["preconditions[0].condition"])
	assert.Equal(t, "repo/prod:latest", resolved["container.image"])
	assert.Equal(t, "prod", resolved["container.env[0]"])

	assert.Equal(t, "echo prod", resolved["steps[0].run[0].cmd_with_args"])
	assert.Equal(t, "/work/prod", resolved["steps[0].working_dir"])
	assert.Equal(t, "prod", resolved["steps[0].env[0]"])
	assert.Equal(t, "check-prod", resolved["steps[0].preconditions[0].condition"])
	assert.Equal(t, "3", resolved["steps[0].retry_policy.limit"])
	assert.Equal(t, "5", resolved["steps[0].retry_policy.interval_sec"])
	assert.Equal(t, "keep-going", resolved["steps[0].repeat_policy.condition"])
	assert.Equal(t, "2", resolved["steps[0].repeat_policy.limit"])
	assert.Equal(t, "7", resolved["steps[0].repeat_policy.interval_sec"])
	assert.Equal(t, "11", resolved["steps[0].repeat_policy.max_interval_sec"])
	assert.Equal(t, "stdout/prod.log", resolved["steps[0].stdout.artifact"])
	assert.Equal(t, "stderr/prod.log", resolved["steps[0].stderr.artifact"])
	assert.Equal(t, "repo:prod", resolved["steps[0].stdout.outputs.fields.image.value"])
	assert.Equal(t, "sha:prod", resolved["steps[0].output.digest.value"])
	assert.Equal(t, "outputs/prod.txt", resolved["steps[0].output.report.path"])

	assert.Equal(t, "review prod", resolved["steps[1].run[0].cmd_with_args"])
	assert.Equal(t, "step:prod", resolved["steps[1].container.image"])
	assert.Equal(t, "deploy prod", resolved["steps[2].with.message"])

	assert.Equal(t, "child-prod", resolved["steps[3].child_dag.name"])
	assert.Equal(t, `ENV="prod"`, resolved["steps[3].child_dag.params"])
	assert.Equal(t, "prod", resolved["steps[4].parallel.items[0].value"])
	assert.Equal(t, "prod", resolved["steps[4].parallel.items[1].params.target"])
	assert.Equal(t, "fanout-prod", resolved["steps[4].child_dag.name"])
	assert.Contains(t, resolved["steps[4].child_dag.params"], `env="prod"`)

	assert.Equal(t, "system prod", resolved["steps[5].llm.system"])
	assert.Equal(t, "https://fallback-prod.example/v1", resolved["steps[5].llm.base_url"])
	assert.Equal(t, "https://prod.example/v1", resolved["steps[5].llm.model[0].base_url"])
	assert.Equal(t, "hello prod", resolved["steps[5].messages[0].content"])

	assert.Equal(t, "literal ${params.environment}", resolved["steps[6].run"])
	assert.Equal(t, "resolved prod", resolved["steps[6].with.data.message"])

	assertSpec003FieldAbsent(t, resolved, "name")
	assertSpec003FieldAbsent(t, resolved, "description")
	assertSpec003FieldAbsent(t, resolved, "steps[0].id")
	assertSpec003FieldAbsent(t, resolved, "steps[0].outputs[0].name")
	assertSpec003FieldAbsent(t, resolved, "steps[0].outputs[0].type")
	assertSpec003FieldAbsent(t, resolved, "steps[5].llm.model[0].provider")
	assertSpec003FieldAbsent(t, resolved, "steps[5].llm.model[0].name")
	assertSpec003FieldAbsent(t, resolved, "steps[5].llm.model[0].api_key_name")
	assertSpec003FieldAbsent(t, resolved, "steps[5].llm.tools[0]")
}

func TestSpec003ConformanceConstsResolveBeforeLiteralParamDefaults(t *testing.T) {
	t.Parallel()

	dag := loadSpec003DAG(t, `
name: consts-and-defaults
consts:
  - region: us-east-1
  - endpoint: https://${consts.region}.example
params:
  - name: target
    default: ${consts.region}
steps:
  - id: show
    run: echo ${consts.endpoint} ${params.target}
`)

	require.Equal(t, "https://us-east-1.example", dag.Consts["endpoint"])
	require.Equal(t, "${consts.region}", dag.ParamValues()["target"])

	resolved := resolveSpec003Fields(t, dag, dag.ParamValues())
	assert.Equal(t, "echo https://us-east-1.example ${consts.region}", resolved["steps[0].run[0].cmd_with_args"])
}

func TestSpec003ConformanceInheritedRootLLMTextFields(t *testing.T) {
	t.Parallel()

	dag := loadSpec003DAG(t, `
name: root-llm
params:
  - name: environment
    default: prod
llm:
  model:
    - provider: openai
      name: gpt-4o
      base_url: https://${params.environment}.example/v1
  system: root system ${params.environment}
  base_url: https://fallback-${params.environment}.example/v1
steps:
  - id: chat
    action: chat.completion
    with:
      messages:
        - role: user
          content: hello ${params.environment}
`)

	resolved := resolveSpec003Fields(t, dag, cmnvalue.Values{"environment": "prod"})

	assert.Equal(t, "root system prod", resolved["steps[0].llm.system"])
	assert.Equal(t, "https://fallback-prod.example/v1", resolved["steps[0].llm.base_url"])
	assert.Equal(t, "https://prod.example/v1", resolved["steps[0].llm.model[0].base_url"])
	assert.Equal(t, "hello prod", resolved["steps[0].messages[0].content"])
	assertSpec003FieldAbsent(t, resolved, "steps[0].llm.model[0].provider")
	assertSpec003FieldAbsent(t, resolved, "steps[0].llm.model[0].name")
}

func TestSpec003ConformanceEscapedDaguReferencesUseBackslashParity(t *testing.T) {
	t.Parallel()

	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{Params: cmnvalue.Values{"name": nil}},
		cmnvalue.RuntimeScope{Params: cmnvalue.Values{"name": "prod"}},
	)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "odd single backslash escapes",
			raw:  `\${params.name}`,
			want: `${params.name}`,
		},
		{
			name: "even backslashes do not escape",
			raw:  `\\${params.name}`,
			want: `\\prod`,
		},
		{
			name: "odd run keeps preceding pairs",
			raw:  `\\\${params.name}`,
			want: `\\${params.name}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolver.String(context.Background(), tt.raw, cmnvalue.WorkflowField("test"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSpec003ConformanceStringInsertionIsOnePass(t *testing.T) {
	t.Parallel()

	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{
			Params: cmnvalue.Values{
				"nested": nil,
				"name":   nil,
			},
		},
		cmnvalue.RuntimeScope{
			Params: cmnvalue.Values{
				"nested": "${params.name}",
				"name":   "prod",
			},
		},
	)

	got, err := resolver.String(context.Background(), "${params.nested}", cmnvalue.WorkflowField("test"))
	require.NoError(t, err)
	assert.Equal(t, "${params.name}", got)
}

func loadSpec003DAG(t *testing.T, yaml string) *core.DAG {
	t.Helper()

	registerSpec003ExecutorCapabilities()

	dag, err := spec.LoadYAML(
		context.Background(),
		[]byte(strings.TrimSpace(yaml)),
		spec.WithoutEval(),
	)
	require.NoError(t, err)
	return dag
}

var registerSpec003ExecutorCapabilitiesOnce sync.Once

func registerSpec003ExecutorCapabilities() {
	registerSpec003ExecutorCapabilitiesOnce.Do(func() {
		for _, typ := range []string{"", "shell", "command"} {
			core.RegisterExecutorCapabilities(typ, core.ExecutorCapabilities{
				Command: true, MultipleCommands: true, Script: true, Shell: true,
			})
		}
		for _, typ := range []string{"docker", "container"} {
			core.RegisterExecutorCapabilities(typ, core.ExecutorCapabilities{
				Command: true, MultipleCommands: true, Container: true,
			})
		}
		for _, typ := range []string{"dag", "subworkflow", "parallel", core.ExecutorTypeDAGEnqueue} {
			core.RegisterExecutorCapabilities(typ, core.ExecutorCapabilities{
				SubDAG: true, WorkerSelector: true,
			})
		}
		core.RegisterExecutorCapabilities("chat", core.ExecutorCapabilities{LLM: true})
		core.RegisterExecutorCapabilities("harness", core.ExecutorCapabilities{
			Command: true, Script: true, Container: true,
		})
		core.RegisterExecutorCapabilities("log", core.ExecutorCapabilities{})
		core.RegisterExecutorCapabilities("template", core.ExecutorCapabilities{Script: true})
	})
}

func resolveSpec003Fields(t *testing.T, dag *core.DAG, params cmnvalue.Values) map[string]string {
	t.Helper()

	resolver := cmnvalue.NewResolver(
		cmnvalue.StaticScope{
			Consts: cmnvalue.Values(dag.Consts),
			Params: dag.ParamDeclarations(),
		},
		cmnvalue.RuntimeScope{
			Consts: cmnvalue.Values(dag.Consts),
			Params: params,
			Env:    cmnvalue.NewEnvScope(nil, false),
		},
	)

	resolved := make(map[string]string)
	for _, field := range core.ReferenceFields(dag) {
		got, err := resolver.String(context.Background(), field.Value, field.Field)
		require.NoError(t, err, "field %s", field.Path)
		resolved[field.Path] = got
	}
	return resolved
}

func assertSpec003FieldAbsent(t *testing.T, resolved map[string]string, path string) {
	t.Helper()

	_, ok := resolved[path]
	require.False(t, ok, "field %s must not opt into Spec 003 value resolution", path)
}
