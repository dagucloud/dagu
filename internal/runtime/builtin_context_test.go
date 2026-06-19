// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
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
	docsDir := filepath.Join(tmpDir, "docs")
	workDir := filepath.Join(tmpDir, "work")
	artifactDir := filepath.Join(tmpDir, "artifacts")
	logFile := filepath.Join(tmpDir, "dag.log")
	startedAt := "2026-03-13T10:00:01Z"
	scheduledAt := "2026-03-13T10:00:00Z"
	profileResolvedAt := "2026-03-13T09:59:00Z"

	cfg := &config.Config{}
	cfg.Paths.DocsDir = docsDir
	ctx := config.WithConfig(context.Background(), cfg)
	dag := &core.DAG{Name: "child"}
	ctx = runtime.NewContext(ctx, dag, "run-1", logFile,
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

	got, err := runtime.ResolveString(ctx, "${dag.name}|${run.id}|${run.status}|${run.started_at}|${run.scheduled_at}|${run.root_name}|${run.root_id}|${attempt.id}|${step.id}|${step.name}|${trigger.type}|${paths.log_file}|${paths.work_dir}|${paths.artifacts_dir}|${paths.docs_dir}|${paths.step_stdout_file}|${paths.step_stderr_file}|${paths.step_output_file}|${profile.name}|${profile.resolved_at}|${pushback.iteration}|${pushback.previous_stdout_file}", cmnvalue.WorkflowField("run"))
	require.NoError(t, err)

	expected := "child|run-1|succeeded|" + startedAt + "|" + scheduledAt + "|root|root-run-1|attempt-1|build-id|build|scheduler|" +
		logFile + "|" + workDir + "|" + artifactDir + "|" + filepath.Join(docsDir, "child") + "|" +
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

	input := "${run.status} ${run.root_name} ${run.root_id} ${trigger.actor} ${profile.name} ${pushback.iteration}"
	got, err := runtime.ResolveString(ctx, input, cmnvalue.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, input, got)
}
