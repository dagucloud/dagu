// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestReferenceFieldsEmitsValidationPathSet(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Env:        []string{"ROOT=${consts.root}"},
		Dotenv:     []string{"${consts.env_file}"},
		Shell:      "${consts.shell}",
		ShellArgs:  []string{"${consts.shell_arg}"},
		WorkingDir: "${consts.workdir}",
		Preconditions: []*core.Condition{
			{Condition: "${env.READY}"},
		},
		Container: &core.Container{
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
		Steps: []core.Step{
			{
				ID:     "build",
				Name:   "build",
				Script: "${consts.script}",
				Commands: []core.CommandEntry{
					{
						Command:     "${consts.command}",
						CmdWithArgs: "${consts.cmd_with_args}",
						Args:        []string{"${consts.arg}"},
					},
				},
				ExecutorConfig: core.ExecutorConfig{
					Config: map[string]any{
						"endpoint": "${consts.endpoint}",
						"headers": map[string]any{
							"authorization": "${env.TOKEN}",
						},
					},
				},
				Dir: "${consts.step_dir}",
				Env: []string{"STEP=${env.STEP}"},
				Preconditions: []*core.Condition{
					{Condition: "${env.STEP_READY}"},
				},
				RetryPolicy: core.RetryPolicy{
					LimitStr:       "${consts.retry_limit}",
					IntervalSecStr: "${consts.retry_interval}",
				},
				RepeatPolicy: core.RepeatPolicy{
					LimitStr:       "${consts.repeat_limit}",
					IntervalStr:    "${consts.repeat_interval}",
					MaxIntervalStr: "${consts.repeat_max_interval}",
					Condition:      &core.Condition{Condition: "${env.REPEAT}"},
				},
				SubDAG: &core.SubDAG{
					Name:   "${consts.child_dag}",
					Params: "${env.CHILD_PARAMS}",
				},
				Parallel: &core.ParallelConfig{
					Variable: "${params.items}",
					Items: []core.ParallelItem{
						{
							Value:  "${consts.item}",
							Params: collections.DeterministicMap{"target": "${env.TARGET}"},
						},
					},
				},
				Stdout:         "${consts.stdout}",
				StdoutArtifact: "${consts.stdout_artifact}",
				Stderr:         "${consts.stderr}",
				StderrArtifact: "${consts.stderr_artifact}",
				StdoutOutputs: &core.StepOutputsConfig{Fields: map[string]core.StepOutputEntry{
					"image": {
						HasValue: true,
						Value:    map[string]string{"tag": "${params.tag}"},
						Path:     "${consts.output_path}",
						Select:   "${consts.unsupported_select}",
					},
				}},
				StructuredOutput: map[string]core.StepOutputEntry{
					"digest": {
						HasValue: true,
						Value:    []any{"${steps.build.outputs.image}"},
						Path:     "${consts.digest_path}",
						Select:   "${consts.unsupported_structured_select}",
					},
				},
				Container: &core.Container{
					Image:      "${consts.step_image}",
					WorkingDir: "${consts.step_container_dir}",
				},
				LLM: &core.LLMConfig{
					Provider:   "${consts.llm_provider}",
					Model:      "${consts.llm_model}",
					System:     "${env.LLM_SYSTEM}",
					BaseURL:    "${env.LLM_BASE_URL}",
					APIKeyName: "${env.LLM_API_KEY}",
					Models: []core.ModelEntry{
						{
							Provider:   "${consts.model_provider}",
							Name:       "${consts.model_name}",
							BaseURL:    "${env.MODEL_BASE_URL}",
							APIKeyName: "${env.MODEL_API_KEY}",
						},
					},
					Tools: []string{"${consts.llm_tool}"},
				},
				Messages: []core.LLMMessage{
					{Content: "${env.MESSAGE_CONTENT}"},
				},
			},
		},
		HandlerOn: core.HandlerOn{
			Init: &core.Step{Name: "init", Script: "${consts.init_script}"},
		},
	}

	fields := core.ReferenceFields(dag)
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
		"steps[0].retry_policy.limit",
		"steps[0].retry_policy.interval_sec",
		"steps[0].repeat_policy.limit",
		"steps[0].repeat_policy.interval",
		"steps[0].repeat_policy.max_interval",
		"steps[0].repeat_policy.condition",
		"steps[0].child_dag.name",
		"steps[0].child_dag.params",
		"steps[0].parallel.variable",
		"steps[0].parallel.items[0].value",
		"steps[0].parallel.items[0].params.target",
		"steps[0].stdout",
		"steps[0].stdout.artifact",
		"steps[0].stderr",
		"steps[0].stderr.artifact",
		"steps[0].stdout.outputs.fields.image.value.tag",
		"steps[0].stdout.outputs.fields.image.path",
		"steps[0].output.digest.value[0]",
		"steps[0].output.digest.path",
		"steps[0].container.image",
		"steps[0].container.working_dir",
		"steps[0].llm.provider",
		"steps[0].llm.model",
		"steps[0].llm.system",
		"steps[0].llm.base_url",
		"steps[0].llm.api_key_name",
		"steps[0].llm.models[0].provider",
		"steps[0].llm.models[0].name",
		"steps[0].llm.models[0].base_url",
		"steps[0].llm.models[0].api_key_name",
		"steps[0].llm.tools[0]",
		"steps[0].messages[0].content",
		"handler_on.init.run",
	}, got)
	assert.NotContains(t, got, "steps[0].stdout.outputs.fields.image.select")
	assert.NotContains(t, got, "steps[0].output.digest.select")
}
