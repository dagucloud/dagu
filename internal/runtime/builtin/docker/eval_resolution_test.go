// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerExecutorCommandResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		step            core.Step
		wantShellConfig bool
	}{
		{
			name: "StepContainerShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "docker"},
				Container: &core.Container{
					Image: "alpine",
					Shell: []string{"/bin/sh", "-c"},
				},
			},
			wantShellConfig: true,
		},
		{
			name: "ExecutorConfigShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{
					Type:   "docker",
					Config: map[string]any{"image": "alpine", "shell": "/bin/bash"},
				},
			},
			wantShellConfig: true,
		},
		{
			name: "NoShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "docker"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			command := tt.step.CommandResolution(context.Background())
			assert.Equal(t, cmnvalue.CommandTargetDocker, command.Target)
			assert.Equal(t, tt.wantShellConfig, command.ShellConfigured)
		})
	}
}

func TestEvalContainerFieldsUsesDockerCommandSemantics(t *testing.T) {
	t.Setenv("DOCKER_TARGET_HOME", "/host/home")

	step := core.Step{
		Name:           "docker-step",
		ExecutorConfig: core.ExecutorConfig{Type: "docker"},
	}
	ctx := runtime.NewContextForTest(context.Background(), &core.DAG{Name: "test-dag"}, "run-1", "test.log")
	env := runtime.NewEnv(ctx, step)
	env.Scope = env.Scope.WithEntry("COMMAND_NAME", "printf", cmnvalue.EnvSourceStepEnv)
	ctx = runtime.WithEnv(ctx, env)

	got, err := EvalContainerFields(ctx, core.Container{
		Command: []string{"$COMMAND_NAME", "$DOCKER_TARGET_HOME"},
		Shell:   []string{"/bin/sh", "-c", "echo \\$DOCKER_TARGET_HOME"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"printf", "$DOCKER_TARGET_HOME"}, got.Command)
	assert.Equal(t, []string{"/bin/sh", "-c", "echo \\$DOCKER_TARGET_HOME"}, got.Shell)
}
