// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	"github.com/dagucloud/dagu/v2/internal/ir"
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
		runenv.EnvKeyDAGName:                       "workflow",
		runenv.EnvKeyDAGDocsDir:                    "/host/docs/workflow",
		runenv.EnvKeyDAGRunID:                      "run-1",
		runenv.EnvKeyDAGRunWorkDir:                 "/host/work",
		runenv.EnvKeyDAGRunLogFile:                 "/host/log/main.log",
		runenv.EnvKeyDAGRunArtifactsDir:            "/host/artifacts",
		runenv.EnvKeyDAGRunStepStdoutFile:          "/host/log/stdout.log",
		runenv.EnvKeyDAGRunStepStderrFile:          "/host/log/stderr.log",
		runenv.EnvKeyDAGPushBackPreviousStdoutFile: "/host/log/previous.log",
		"PWD": "/host/work",
	})

	assert.Equal(t, []string{
		"API_TOKEN=secret",
		runenv.EnvKeyDAGName + "=workflow",
		runenv.EnvKeyDAGRunID + "=run-1",
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
