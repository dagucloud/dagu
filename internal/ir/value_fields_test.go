// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestReferenceFieldsEmitsValidationPathSet(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Env:        []string{"ROOT=${consts.root}"},
		Dotenv:     []string{"${consts.env_file}"},
		Shell:      "${consts.shell}",
		ShellArgs:  []string{"${consts.shell_arg}"},
		WorkingDir: "${consts.workdir}",
		Preconditions: []*ir.Condition{
			{Condition: "${env.READY}"},
			{Eval: "${params.root_eval}", Expected: "ready"},
		},
		Container: &ir.Container{
			Exec:       "${consts.exec}",
			Image:      "${consts.image}",
			Name:       "${consts.name}",
			User:       "${consts.user}",
			WorkingDir: "${consts.container_dir}",
			Network:    "${consts.network}",
			Volumes:    []string{"${consts.volume}"},
			Ports:      []string{"${consts.port}"},
			Env:        []string{"ROOT=${env.ROOT}"},
			Command:    []string{"${consts.command}"},
			Shell:      []string{"${consts.shell}"},
		},
		Steps: []ir.Step{
			{
				ID:     "build",
				Name:   "build",
				Script: "${consts.script}",
				Commands: []ir.CommandEntry{
					{
						Command:     "${consts.command}",
						CmdWithArgs: "${consts.cmd_with_args}",
						Args:        []string{"${consts.arg}"},
					},
				},
				ExecutorConfig: ir.ExecutorConfig{
					Config: map[string]any{
						"endpoint": "${consts.endpoint}",
						"headers": map[string]any{
							"authorization": "${env.TOKEN}",
						},
					},
				},
				Dir: "${consts.step_dir}",
				Env: []string{"STEP=${env.STEP}"},
				Preconditions: []*ir.Condition{
					{Condition: "${env.STEP_READY}"},
					{Eval: "${params.step_eval}", Expected: "ready"},
				},
				RetryPolicy: ir.RetryPolicy{
					LimitStr:       "${consts.retry_limit}",
					IntervalSecStr: "${consts.retry_interval}",
				},
				RepeatPolicy: ir.RepeatPolicy{
					LimitStr:       "${consts.repeat_limit}",
					IntervalStr:    "${consts.repeat_interval}",
					MaxIntervalStr: "${consts.repeat_max_interval}",
					Condition:      &ir.Condition{Condition: "${env.REPEAT}"},
				},
				SubDAG: &ir.SubDAG{
					Name:   "${consts.child_dag}",
					Params: "${env.CHILD_PARAMS}",
				},
				Parallel: &ir.ParallelConfig{
					Variable: "${params.items}",
					Items: []ir.ParallelItem{
						{
							Value:  "${consts.item}",
							Params: collections.DeterministicMap{"target": "${env.TARGET}"},
						},
					},
				},
				Foreach: &ir.ForeachConfig{
					Items: []any{
						map[string]any{"source": "${params.foreach_source}"},
					},
					Key: "${foreach.item.source}",
					Steps: []ir.Step{
						{
							Name:   "summarize",
							Script: "echo ${foreach.item.source}",
							Env:    []string{"ITEM=${foreach.item.source}"},
						},
					},
					Collect: map[string]string{
						"summary": "${steps.summarize.stdout}",
					},
				},
				Stdout:         "${consts.stdout}",
				StdoutArtifact: "${consts.stdout_artifact}",
				Stderr:         "${consts.stderr}",
				StderrArtifact: "${consts.stderr_artifact}",
				StdoutOutputs: &ir.StepOutputsConfig{Fields: map[string]ir.StepOutputEntry{
					"image": {
						HasValue: true,
						Value:    map[string]string{"tag": "${params.tag}"},
						Path:     "${consts.output_path}",
						Select:   "${consts.unsupported_select}",
					},
				}},
				StructuredOutput: map[string]ir.StepOutputEntry{
					"digest": {
						HasValue: true,
						Value:    []any{"${steps.build.outputs.image}"},
						Path:     "${consts.digest_path}",
						Select:   "${consts.unsupported_structured_select}",
					},
				},
				Container: &ir.Container{
					Image:      "${consts.step_image}",
					WorkingDir: "${consts.step_container_dir}",
				},
				LLM: &ir.LLMConfig{
					Provider:   "${consts.llm_provider}",
					Model:      "${consts.llm_model}",
					System:     "${env.LLM_SYSTEM}",
					BaseURL:    "${env.LLM_BASE_URL}",
					APIKeyName: "${env.LLM_API_KEY}",
					Models: []ir.ModelEntry{
						{
							Provider:   "${consts.model_provider}",
							Name:       "${consts.model_name}",
							BaseURL:    "${env.MODEL_BASE_URL}",
							APIKeyName: "${env.MODEL_API_KEY}",
						},
					},
					Tools: []string{"${consts.llm_tool}"},
				},
				Messages: []ir.LLMMessage{
					{Content: "${env.MESSAGE_CONTENT}"},
				},
			},
		},
		HandlerOn: ir.HandlerOn{
			Init: &ir.Step{Name: "init", Script: "${consts.init_script}"},
		},
	}

	fields := ir.ReferenceFields(dag)
	got := make([]string, 0, len(fields))
	for _, field := range fields {
		got = append(got, field.Path)
	}

	assert.ElementsMatch(t, []string{
		"env[0]",
		"dotenv[0]",
		"shell",
		"shell_args[0]",
		"working_dir",
		"preconditions[0].condition",
		"preconditions[1].eval",
		"container.exec",
		"container.image",
		"container.name",
		"container.user",
		"container.working_dir",
		"container.network",
		"container.volumes[0]",
		"container.ports[0]",
		"container.env[0]",
		"container.command[0]",
		"container.shell[0]",
		"steps[0].run",
		"steps[0].run[0].command",
		"steps[0].run[0].cmd_with_args",
		"steps[0].run[0].args[0]",
		"steps[0].with.endpoint",
		"steps[0].with.headers.authorization",
		"steps[0].working_dir",
		"steps[0].env[0]",
		"steps[0].preconditions[0].condition",
		"steps[0].preconditions[1].eval",
		"steps[0].retry_policy.limit",
		"steps[0].retry_policy.interval_sec",
		"steps[0].repeat_policy.limit",
		"steps[0].repeat_policy.interval_sec",
		"steps[0].repeat_policy.max_interval_sec",
		"steps[0].repeat_policy.condition",
		"steps[0].child_dag.name",
		"steps[0].child_dag.params",
		"steps[0].parallel.variable",
		"steps[0].parallel.items[0].value",
		"steps[0].parallel.items[0].params.target",
		"steps[0].foreach.items[0].source",
		"steps[0].foreach.key",
		"steps[0].foreach.steps[0].run",
		"steps[0].foreach.steps[0].env[0]",
		"steps[0].foreach.collect.summary",
		"steps[0].stdout",
		"steps[0].stdout.artifact",
		"steps[0].stderr",
		"steps[0].stderr.artifact",
		"steps[0].stdout.outputs.fields.image.value.tag",
		"steps[0].output.digest.value[0]",
		"steps[0].output.digest.path",
		"steps[0].container.image",
		"steps[0].container.working_dir",
		"steps[0].llm.system",
		"steps[0].llm.provider",
		"steps[0].llm.model",
		"steps[0].llm.base_url",
		"steps[0].llm.model[0].provider",
		"steps[0].llm.model[0].name",
		"steps[0].llm.model[0].base_url",
		"steps[0].messages[0].content",
		"handler_on.init.run",
	}, got)
	assert.NotContains(t, got, "steps[0].stdout.outputs.fields.image.select")
	assert.NotContains(t, got, "steps[0].stdout.outputs.fields.image.path")
	assert.NotContains(t, got, "steps[0].output.digest.select")
	assert.NotContains(t, got, "steps[0].llm.api_key_name")
	assert.NotContains(t, got, "steps[0].llm.model[0].api_key_name")
	assert.NotContains(t, got, "steps[0].llm.tools[0]")
}
