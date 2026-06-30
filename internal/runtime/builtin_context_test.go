// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"path/filepath"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStringBuiltInRunContext(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	artifactDir := filepath.Join(tmpDir, "artifacts")
	logFile := filepath.Join(tmpDir, "dag.log")
	startedAt := "2026-03-13T10:00:01Z"
	scheduledAt := "2026-03-13T10:00:00Z"
	profileResolvedAt := "2026-03-13T09:59:00Z"

	dag := &core.DAG{Name: "child"}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", logFile,
		runtime.WithAttemptID("attempt-1"),
		runtime.WithRootDAGRun(exec.NewDAGRunRef("root", "root-run-1")),
		runtime.WithTriggerType(core.TriggerTypeScheduler),
		runtime.WithRunStartedAt(startedAt),
		runtime.WithScheduleTime(scheduledAt),
		runtime.WithWorkDir(workDir),
		runtime.WithArtifactDir(artifactDir),
		runtime.WithRuntimeProfile("prod", profileResolvedAt, nil),
	)

	env := runtime.NewEnv(ctx, core.Step{ID: "build-id", Name: "build"})
	env.Scope = env.Scope.WithEntries(map[string]string{
		exec.EnvKeyDAGRunStatus:                  core.Succeeded.String(),
		exec.EnvKeyDAGRunStepStdoutFile:          filepath.Join(tmpDir, "stdout.log"),
		exec.EnvKeyDAGRunStepStderrFile:          filepath.Join(tmpDir, "stderr.log"),
		exec.EnvKeyDAGUOutputFile:                filepath.Join(tmpDir, "output.json"),
		exec.EnvKeyDAGPushBackIteration:          "2",
		exec.EnvKeyDAGPushBackPreviousStdoutFile: filepath.Join(tmpDir, "previous.log"),
	}, cmnvalue.EnvSourceStepEnv)
	ctx = runtime.WithEnv(ctx, env)

	got, err := runtime.ResolveString(ctx, "${context.dag.name}|${context.run.id}|${context.run.status}|${context.attempt.started_at}|${context.run.scheduled_at}|${context.run.root_name}|${context.run.root_id}|${context.attempt.id}|${context.step.id}|${context.step.name}|${context.trigger.type}|${context.paths.log_file}|${context.paths.work_dir}|${context.paths.artifacts_dir}|${context.paths.step_stdout_file}|${context.paths.step_stderr_file}|${context.paths.step_output_file}|${context.profile.name}|${context.profile.resolved_at}|${context.pushback.iteration}|${context.pushback.previous_stdout_file}", cmnvalue.WorkflowField("run"))
	require.NoError(t, err)

	expected := "child|run-1|succeeded|" + startedAt + "|" + scheduledAt + "|root|root-run-1|attempt-1|build-id|build|scheduler|" +
		logFile + "|" + workDir + "|" + artifactDir + "|" +
		filepath.Join(tmpDir, "stdout.log") + "|" + filepath.Join(tmpDir, "stderr.log") + "|" +
		filepath.Join(tmpDir, "output.json") + "|prod|" + profileResolvedAt + "|2|" + filepath.Join(tmpDir, "previous.log")
	assert.Equal(t, expected, got)
}

func TestResolveStringUnavailableBuiltInRunContextStaysLiteral(t *testing.T) {
	t.Parallel()

	ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "run-1", "dag.log",
		runtime.WithWorkDir(t.TempDir()),
	)
	env := runtime.NewEnv(ctx, core.Step{Name: "step"})
	ctx = runtime.WithEnv(ctx, env)

	input := "${context.run.status} ${context.run.root_name} ${context.run.root_id} ${context.trigger.actor} ${context.profile.name} ${context.pushback.iteration}"
	got, err := runtime.ResolveString(ctx, input, cmnvalue.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, input, got)
}

func TestResolveStringLegacyBuiltInRunContextAliases(t *testing.T) {
	t.Parallel()

	ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "run-1", "dag.log",
		runtime.WithAttemptID("attempt-1"),
		runtime.WithRunStartedAt("2026-03-13T10:00:01Z"),
		runtime.WithWorkDir(t.TempDir()),
	)
	env := runtime.NewEnv(ctx, core.Step{Name: "step"})
	ctx = runtime.WithEnv(ctx, env)

	got, err := runtime.ResolveString(ctx, "${dag.name}|${run.id}|${run.started_at}|${attempt.id}|${step.name}", cmnvalue.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, "test|run-1|2026-03-13T10:00:01Z|attempt-1|step", got)
}
