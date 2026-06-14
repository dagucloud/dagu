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

func TestEvalContainerFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(ctx context.Context) context.Context
		input    core.Container
		expected core.Container
	}{
		{
			name: "NoVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image:      "alpine:latest",
				Name:       "my-container",
				WorkingDir: "/app",
			},
			expected: core.Container{
				Image:      "alpine:latest",
				Name:       "my-container",
				WorkingDir: "/app",
			},
		},
		{
			name: "ImageVariable",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("IMAGE", "myimage:v1.0", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image: "${IMAGE}",
			},
			expected: core.Container{
				Image: "myimage:v1.0",
			},
		},
		{
			name: "MultipleVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"IMAGE":          "nginx:latest",
					"CONTAINER_NAME": "web-server",
					"WORK_DIR":       "/var/www",
					"NET":            "my-network",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image:      "${IMAGE}",
				Name:       "${CONTAINER_NAME}",
				WorkingDir: "${WORK_DIR}",
				Network:    "${NET}",
			},
			expected: core.Container{
				Image:      "nginx:latest",
				Name:       "web-server",
				WorkingDir: "/var/www",
				Network:    "my-network",
			},
		},
		{
			name: "VolumesWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"HOST_PATH":      "/host/data",
					"CONTAINER_PATH": "/container/data",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image:   "alpine",
				Volumes: []string{"${HOST_PATH}:${CONTAINER_PATH}:ro"},
			},
			expected: core.Container{
				Image:   "alpine",
				Volumes: []string{"/host/data:/container/data:ro"},
			},
		},
		{
			name: "PortsWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"HOST_PORT":      "8080",
					"CONTAINER_PORT": "80",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image: "nginx",
				Ports: []string{"${HOST_PORT}:${CONTAINER_PORT}"},
			},
			expected: core.Container{
				Image: "nginx",
				Ports: []string{"8080:80"},
			},
		},
		{
			name: "EnvWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"DB_HOST": "localhost",
					"DB_PORT": "5432",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image: "myapp",
				Env:   []string{"DATABASE_URL=postgres://${DB_HOST}:${DB_PORT}/db"},
			},
			expected: core.Container{
				Image: "myapp",
				Env:   []string{"DATABASE_URL=postgres://localhost:5432/db"},
			},
		},
		{
			name: "CommandWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"SCRIPT": "run.sh",
					"ARG1":   "value1",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image:   "alpine",
				Command: []string{"/bin/sh", "${SCRIPT}", "${ARG1}"},
			},
			expected: core.Container{
				Image:   "alpine",
				Command: []string{"/bin/sh", "run.sh", "value1"},
			},
		},
		{
			name: "UserWithVariable",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("UID", "1000", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image: "alpine",
				User:  "${UID}",
			},
			expected: core.Container{
				Image: "alpine",
				User:  "1000",
			},
		},
		{
			name: "NonEvaluatedFieldsRemainUnchanged",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("POLICY", "always", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image:         "alpine",
				PullPolicy:    core.PullPolicyAlways,
				KeepContainer: true,
				Startup:       core.StartupCommand,
				WaitFor:       core.WaitForHealthy,
				Platform:      "linux/amd64",
				LogPattern:    "ready.*started",
				RestartPolicy: "on-failure",
			},
			expected: core.Container{
				Image:         "alpine",
				PullPolicy:    core.PullPolicyAlways,
				KeepContainer: true,
				Startup:       core.StartupCommand,
				WaitFor:       core.WaitForHealthy,
				Platform:      "linux/amd64",
				LogPattern:    "ready.*started",
				RestartPolicy: "on-failure",
			},
		},
		{
			name: "OutputFromPreviousStep",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("IMAGE_TAG", "v2.0.0", cmnvalue.EnvSourceOutput)
				return runtime.WithEnv(ctx, env)
			},
			input: core.Container{
				Image: "myapp:${IMAGE_TAG}",
			},
			expected: core.Container{
				Image: "myapp:v2.0.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setup(context.Background())

			result, err := EvalContainerFields(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvalStringSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(ctx context.Context) context.Context
		input    []string
		expected []string
	}{
		{
			name: "EmptySlice",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input:    []string{},
			expected: []string{},
		},
		{
			name: "NilSlice",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input:    nil,
			expected: nil,
		},
		{
			name: "NoVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input:    []string{"hello", "world"},
			expected: []string{"hello", "world"},
		},
		{
			name: "WithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, core.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"VAR1": "value1",
					"VAR2": "value2",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input:    []string{"${VAR1}", "${VAR2}", "static"},
			expected: []string{"value1", "value2", "static"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setup(context.Background())

			result, err := evalStringSlice(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
