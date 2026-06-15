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
				RepeatPolicy: core.RepeatPolicy{
					Condition: &core.Condition{Condition: "${env.REPEAT}"},
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
		"steps[0].repeat_policy.condition",
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
		"handler_on.init.run",
	}, got)
	assert.NotContains(t, got, "steps[0].stdout.outputs.fields.image.select")
	assert.NotContains(t, got, "steps[0].output.digest.select")
}
