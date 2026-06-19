// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/builtin/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOnce_RootContainerWithoutSharedClientFails(t *testing.T) {
	dag := testRootContainerDAG(t)
	step := core.Step{Name: "review"}
	ctx := testHarnessContext(t, dag, step)
	exec := harness.NewTestExecutorForTest(step, "inspect repo", "", dag.WorkingDir)
	cfg := harness.NewTestProviderConfigForTest("agent", core.HarnessDefinition{
		Binary:     "agent",
		PromptMode: core.HarnessPromptModeArg,
	}, map[string]any{"provider": "agent"})

	_, err := exec.RunOnceForTest(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "root-level container is configured")
	assert.Contains(t, err.Error(), "no shared container client")
	assert.Equal(t, 1, exec.ExitCode())
}

func TestRunOnce_BuiltinProviderWithRootContainerRejected(t *testing.T) {
	dag := testRootContainerDAG(t)
	step := core.Step{Name: "review"}
	ctx := testHarnessContext(t, dag, step)
	exec := harness.NewTestExecutorForTest(step, "inspect repo", "", dag.WorkingDir)

	_, err := exec.RunOnceForTest(ctx, harness.NewTestBuiltinProviderConfigForTest("builtin"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "builtin provider does not support container execution")
	assert.Equal(t, 1, exec.ExitCode())
}

func TestRun_RootContainerBuiltinFallbackContinuesToCLI(t *testing.T) {
	dag := testRootContainerDAG(t)
	step := core.Step{Name: "review"}
	ctx := testHarnessContext(t, dag, step)
	exec := harness.NewTestExecutorWithProviderConfigsForTest(
		step,
		"inspect repo",
		"",
		dag.WorkingDir,
		harness.NewTestBuiltinProviderConfigForTest("builtin"),
		harness.NewTestProviderConfigForTest("agent", core.HarnessDefinition{
			Binary:     "agent",
			PromptMode: core.HarnessPromptModeArg,
		}, map[string]any{"provider": "agent"}),
	)
	var stderr strings.Builder
	exec.SetStderr(&stderr)

	err := exec.Run(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no shared container client")
	assert.Contains(t, stderr.String(), "with builtin failed; trying fallback")
}

func TestRunOnce_RootContainerStdinProviderRejectedBeforeSharedClientLookup(t *testing.T) {
	dag := testRootContainerDAG(t)
	step := core.Step{Name: "review"}
	ctx := testHarnessContext(t, dag, step)
	exec := harness.NewTestExecutorForTest(step, "inspect repo", "stdin context", dag.WorkingDir)
	cfg := harness.NewTestProviderConfigForTest("stdin-agent", core.HarnessDefinition{
		Binary:     "stdin-agent",
		PromptMode: core.HarnessPromptModeStdin,
	}, map[string]any{"provider": "stdin-agent"})

	_, err := exec.RunOnceForTest(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support stdin")
	assert.NotContains(t, err.Error(), "no shared container client")
	assert.Equal(t, 1, exec.ExitCode())
}

func TestSharedContainerHarnessEnvForTest_FiltersHostPathRuntimeVariables(t *testing.T) {
	got := harness.SharedContainerHarnessEnvForTest(map[string]string{
		"API_TOKEN":                                  "secret",
		coreexec.EnvKeyDAGName:                       "workflow",
		coreexec.EnvKeyDAGRunID:                      "run-1",
		coreexec.EnvKeyDAGRunWorkDir:                 "/host/work",
		coreexec.EnvKeyDAGRunLogFile:                 "/host/log/main.log",
		coreexec.EnvKeyDAGRunArtifactsDir:            "/host/artifacts",
		coreexec.EnvKeyDAGRunStepStdoutFile:          "/host/log/stdout.log",
		coreexec.EnvKeyDAGRunStepStderrFile:          "/host/log/stderr.log",
		coreexec.EnvKeyDAGPushBackPreviousStdoutFile: "/host/log/previous.log",
		coreexec.EnvKeyDAGDocsDir:                    "/host/docs/workflow",
		"PWD":                                        "/host/work",
	})

	assert.Equal(t, []string{
		"API_TOKEN=secret",
		coreexec.EnvKeyDAGName + "=workflow",
		coreexec.EnvKeyDAGRunID + "=run-1",
	}, got)
}

func testRootContainerDAG(t *testing.T) *core.DAG {
	t.Helper()
	return &core.DAG{
		Name:       "harness-root-container-test",
		WorkingDir: t.TempDir(),
		Container:  &core.Container{Image: "alpine:latest"},
	}
}

func testHarnessContext(t *testing.T, dag *core.DAG, step core.Step, envs ...string) context.Context {
	t.Helper()
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "", runtime.WithEnvVars(envs...))
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
}
