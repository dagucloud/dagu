// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalContainerFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(ctx context.Context) context.Context
		input    ir.Container
		expected ir.Container
	}{
		{
			name: "NoVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image:      "alpine:latest",
				Name:       "my-container",
				WorkingDir: "/app",
			},
			expected: ir.Container{
				Image:      "alpine:latest",
				Name:       "my-container",
				WorkingDir: "/app",
			},
		},
		{
			name: "ImageVariable",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("IMAGE", "myimage:v1.0", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image: "${IMAGE}",
			},
			expected: ir.Container{
				Image: "myimage:v1.0",
			},
		},
		{
			name: "MultipleVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"IMAGE":          "nginx:latest",
					"CONTAINER_NAME": "web-server",
					"WORK_DIR":       "/var/www",
					"NET":            "my-network",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image:      "${IMAGE}",
				Name:       "${CONTAINER_NAME}",
				WorkingDir: "${WORK_DIR}",
				Network:    "${NET}",
			},
			expected: ir.Container{
				Image:      "nginx:latest",
				Name:       "web-server",
				WorkingDir: "/var/www",
				Network:    "my-network",
			},
		},
		{
			name: "VolumesWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"HOST_PATH":      "/host/data",
					"CONTAINER_PATH": "/container/data",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image:   "alpine",
				Volumes: []string{"${HOST_PATH}:${CONTAINER_PATH}:ro"},
			},
			expected: ir.Container{
				Image:   "alpine",
				Volumes: []string{"/host/data:/container/data:ro"},
			},
		},
		{
			name: "PortsWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"HOST_PORT":      "8080",
					"CONTAINER_PORT": "80",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image: "nginx",
				Ports: []string{"${HOST_PORT}:${CONTAINER_PORT}"},
			},
			expected: ir.Container{
				Image: "nginx",
				Ports: []string{"8080:80"},
			},
		},
		{
			name: "EnvWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"DB_HOST": "localhost",
					"DB_PORT": "5432",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image: "myapp",
				Env:   []string{"DATABASE_URL=postgres://${DB_HOST}:${DB_PORT}/db"},
			},
			expected: ir.Container{
				Image: "myapp",
				Env:   []string{"DATABASE_URL=postgres://localhost:5432/db"},
			},
		},
		{
			name: "EnvEntriesEvaluateSequentially",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image: "myapp",
				Env: []string{
					"SERVICE=api",
					"HOST=${env.SERVICE}.internal",
					"SELF=${env.SELF}",
				},
			},
			expected: ir.Container{
				Image: "myapp",
				Env: []string{
					"SERVICE=api",
					"HOST=api.internal",
					"SELF=${env.SELF}",
				},
			},
		},
		{
			name: "CommandWithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntries(map[string]string{
					"SCRIPT": "run.sh",
					"ARG1":   "value1",
				}, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image:   "alpine",
				Command: []string{"/bin/sh", "${SCRIPT}", "${ARG1}"},
			},
			expected: ir.Container{
				Image:   "alpine",
				Command: []string{"/bin/sh", "run.sh", "value1"},
			},
		},
		{
			name: "UserWithVariable",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("UID", "1000", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image: "alpine",
				User:  "${UID}",
			},
			expected: ir.Container{
				Image: "alpine",
				User:  "1000",
			},
		},
		{
			name: "NonEvaluatedFieldsRemainUnchanged",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("POLICY", "always", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image:         "alpine",
				PullPolicy:    ir.PullPolicyAlways,
				KeepContainer: true,
				Startup:       ir.StartupCommand,
				WaitFor:       ir.WaitForHealthy,
				Platform:      "linux/amd64",
				LogPattern:    "ready.*started",
				RestartPolicy: "on-failure",
			},
			expected: ir.Container{
				Image:         "alpine",
				PullPolicy:    ir.PullPolicyAlways,
				KeepContainer: true,
				Startup:       ir.StartupCommand,
				WaitFor:       ir.WaitForHealthy,
				Platform:      "linux/amd64",
				LogPattern:    "ready.*started",
				RestartPolicy: "on-failure",
			},
		},
		{
			name: "OutputFromPreviousStep",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				env.Scope = env.Scope.WithEntry("IMAGE_TAG", "v2.0.0", cmnvalue.EnvSourceOutput)
				return runtime.WithEnv(ctx, env)
			},
			input: ir.Container{
				Image: "myapp:${IMAGE_TAG}",
			},
			expected: ir.Container{
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

func TestEvalContainerEnvFile(t *testing.T) {
	t.Parallel()

	newCtx := func(t *testing.T, workDir string) context.Context {
		t.Helper()
		env := runtime.NewEnv(context.Background(), ir.Step{Name: "test"})
		env.WorkingDir = workDir
		return runtime.WithEnv(context.Background(), env)
	}

	writeEnv := func(t *testing.T, dir, name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	t.Run("InjectsFileVars", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeEnv(t, dir, ".env", "FILE_A=1\nFILE_B=two\n")

		result, err := EvalContainerFields(newCtx(t, dir), ir.Container{
			Image:   "alpine",
			EnvFile: []string{".env"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{".env"}, result.EnvFile)
		assert.ElementsMatch(t, []string{"FILE_A=1", "FILE_B=two"}, result.Env)
	})

	t.Run("EnvOverridesFileVars", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeEnv(t, dir, ".env", "SHARED=from_file\nFILE_ONLY=yes\n")

		result, err := EvalContainerFields(newCtx(t, dir), ir.Container{
			Image:   "alpine",
			EnvFile: []string{".env"},
			Env:     []string{"SHARED=explicit"},
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"SHARED=explicit", "FILE_ONLY=yes"}, result.Env)
	})

	t.Run("LaterFilesOverrideEarlier", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeEnv(t, dir, ".env.base", "MULTI_A=base\nMULTI_B=base\n")
		writeEnv(t, dir, ".env.local", "MULTI_B=local\n")

		result, err := EvalContainerFields(newCtx(t, dir), ir.Container{
			Image:   "alpine",
			EnvFile: []string{".env.base", ".env.local"},
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"MULTI_A=base", "MULTI_B=local"}, result.Env)
	})

	t.Run("DotenvSyntax", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Quotes are stripped, single-quoted values stay literal, and ${VAR}
		// expands from entries defined earlier in the same file.
		writeEnv(t, dir, ".env", "QUOTED=\"a b\"\nLITERAL='x$y'\nREF=${QUOTED}-z\n")

		result, err := EvalContainerFields(newCtx(t, dir), ir.Container{
			Image:   "alpine",
			EnvFile: []string{".env"},
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"QUOTED=a b", "LITERAL=x$y", "REF=a b-z"}, result.Env)
	})

	t.Run("PathVariablesEvaluate", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeEnv(t, dir, "vars.env", "VAR_PATH_VAR=ok\n")

		env := runtime.NewEnv(context.Background(), ir.Step{Name: "test"})
		env.WorkingDir = dir
		env.Scope = env.Scope.WithEntry("ENV_NAME", "vars", cmnvalue.EnvSourceStepEnv)
		ctx := runtime.WithEnv(context.Background(), env)

		result, err := EvalContainerFields(ctx, ir.Container{
			Image:   "alpine",
			EnvFile: []string{"${ENV_NAME}.env"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"vars.env"}, result.EnvFile)
		assert.Contains(t, result.Env, "VAR_PATH_VAR=ok")
	})

	t.Run("ResolvesRelativeToDAGDir", func(t *testing.T) {
		t.Parallel()
		dagDir := t.TempDir()
		writeEnv(t, dagDir, ".env", "DAG_DIR_VAR=present\n")

		env := runtime.NewEnv(context.Background(), ir.Step{Name: "test"})
		env.WorkingDir = t.TempDir() // no .env here
		env.DAG = &ir.DAG{Location: filepath.Join(dagDir, "dag.yaml")}
		ctx := runtime.WithEnv(context.Background(), env)

		result, err := EvalContainerFields(ctx, ir.Container{
			Image:   "alpine",
			EnvFile: []string{".env"},
		})
		require.NoError(t, err)
		assert.Contains(t, result.Env, "DAG_DIR_VAR=present")
	})

	t.Run("MissingFileFails", func(t *testing.T) {
		t.Parallel()
		_, err := EvalContainerFields(newCtx(t, t.TempDir()), ir.Container{
			Image:   "alpine",
			EnvFile: []string{"nonexistent.env"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent.env")
	})

	// Spec 006 layers the container environment as
	// step env < env_file < container env.
	t.Run("StepEnvBelowFileVars", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeEnv(t, dir, ".env", "A=file\nB=file\n")

		exec, err := newDocker(newCtx(t, dir), ir.Step{
			Name: "test",
			Env:  []string{"A=step", "C=step"},
			Container: &ir.Container{
				Image:   "alpine",
				EnvFile: []string{".env"},
				Env:     []string{"B=container"},
			},
		})
		require.NoError(t, err)
		d, ok := exec.(*docker)
		require.True(t, ok, "executor is *docker")
		assert.ElementsMatch(t, []string{"A=file", "B=container", "C=step"}, d.cfg.Container.Env)
	})
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
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input:    []string{},
			expected: []string{},
		},
		{
			name: "NilSlice",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input:    nil,
			expected: nil,
		},
		{
			name: "NoVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
				return runtime.WithEnv(ctx, env)
			},
			input:    []string{"hello", "world"},
			expected: []string{"hello", "world"},
		},
		{
			name: "WithVariables",
			setup: func(ctx context.Context) context.Context {
				env := runtime.NewEnv(ctx, ir.Step{Name: "test"})
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

			result, err := evalStringSlice(ctx, tt.input, "container", func(path string) cmnvalue.Field {
				return cmnvalue.ContainerField(path)
			})
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
