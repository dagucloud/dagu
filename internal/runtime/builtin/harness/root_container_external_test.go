// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOnce_RootContainerWithoutSharedClientFails(t *testing.T) {
	dag := testRootContainerDAG(t)
	step := ir.Step{Name: "review"}
	ctx := testHarnessContext(t, dag, step)
	exec := harness.NewTestExecutorForTest(step, "inspect repo", "", dag.WorkingDir)
	cfg := harness.NewTestProviderConfigForTest("agent", ir.HarnessDefinition{
		Binary:     "agent",
		PromptMode: ir.HarnessPromptModeArg,
	}, map[string]any{"provider": "agent"})

	_, err := exec.RunOnceForTest(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "root-level container is configured")
	assert.Contains(t, err.Error(), "no shared container client")
	assert.Equal(t, 1, exec.ExitCode())
}

func TestRunOnce_RootContainerStdinProviderRejectedBeforeSharedClientLookup(t *testing.T) {
	dag := testRootContainerDAG(t)
	step := ir.Step{Name: "review"}
	ctx := testHarnessContext(t, dag, step)
	exec := harness.NewTestExecutorForTest(step, "inspect repo", "stdin context", dag.WorkingDir)
	cfg := harness.NewTestProviderConfigForTest("stdin-agent", ir.HarnessDefinition{
		Binary:     "stdin-agent",
		PromptMode: ir.HarnessPromptModeStdin,
	}, map[string]any{"provider": "stdin-agent"})

	_, err := exec.RunOnceForTest(ctx, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support stdin")
	assert.NotContains(t, err.Error(), "no shared container client")
	assert.Equal(t, 1, exec.ExitCode())
}

func TestSharedContainerHarnessEnvForTest_FiltersHostPathRuntimeVariables(t *testing.T) {
	got := harness.SharedContainerHarnessEnvForTest(map[string]string{
		"API_TOKEN":                                "secret",
		runctx.EnvKeyDAGName:                       "workflow",
		runctx.EnvKeyDAGDocsDir:                    "/host/docs/workflow",
		runctx.EnvKeyDAGRunID:                      "run-1",
		runctx.EnvKeyDAGRunWorkDir:                 "/host/work",
		runctx.EnvKeyDAGRunLogFile:                 "/host/log/main.log",
		runctx.EnvKeyDAGRunArtifactsDir:            "/host/artifacts",
		runctx.EnvKeyDAGRunStepStdoutFile:          "/host/log/stdout.log",
		runctx.EnvKeyDAGRunStepStderrFile:          "/host/log/stderr.log",
		runctx.EnvKeyDAGPushBackPreviousStdoutFile: "/host/log/previous.log",
		"PWD": "/host/work",
	})

	assert.Equal(t, []string{
		"API_TOKEN=secret",
		runctx.EnvKeyDAGName + "=workflow",
		runctx.EnvKeyDAGRunID + "=run-1",
	}, got)
}

func testRootContainerDAG(t *testing.T) *ir.DAG {
	t.Helper()
	return &ir.DAG{
		Name:       "harness-root-container-test",
		WorkingDir: t.TempDir(),
		Container:  &ir.Container{Image: "alpine:latest"},
	}
}

func testHarnessContext(t *testing.T, dag *ir.DAG, step ir.Step, envs ...string) context.Context {
	t.Helper()
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "", runtime.WithEnvVars(envs...))
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
}
