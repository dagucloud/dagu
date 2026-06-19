// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec017_built_in_run_context_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestValidateBuiltInRunContextNotices(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "validation_notices.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("")
	result.ExpectStderrContains(
		"${run.id}",
		"${attempt.id}",
		"${run.status}",
		"${trigger.actor}",
		"${profile.name}",
		"${pushback.iteration}",
		"was left unchanged",
	)
	result.ExpectStderrNotContains(
		"${paths.context}",
		"${trigger.payload}",
		"${run.foo}",
		"${run.id.extra}",
	)
	dagu.ExpectNoFile("validation-notices.txt")
}

func TestRuntimeBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run(
		"start",
		"--run-id=spec017-run",
		"--trigger-type=scheduler",
		"--schedule-time=2026-03-13T10:00:00+09:00",
		"runtime_context.yaml",
	)
	result.ExpectExitCode(0)
	dagu.ExpectFileContains(
		"run-context.txt",
		"dag=builtin_runtime_context",
		"run=spec017-run",
		"root_name=${run.root_name}",
		"root_id=${run.root_id}",
		"root_env=spec017-run",
		"trigger=scheduler",
		"scheduled=2026-03-13T01:00:00Z",
		"attempt=",
		"step_id=capture",
		"step_name=capture",
		"env_run=spec017-run",
		"env_step=capture",
		"paths_ready=yes",
	)
	dagu.ExpectFileNotContains(
		"run-context.txt",
		"${attempt.id}",
		"${run.started_at}",
		"${paths.step_stdout_file}",
		"${paths.step_stderr_file}",
		"${paths.step_output_file}",
	)
}

func TestHandlerBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--run-id=spec017-handler", "handler_context.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains(
		"handler-context.txt",
		"status=succeeded",
		"env_status=succeeded",
		"run=spec017-handler",
		"stream_paths_match=yes",
	)
	dagu.ExpectFileNotContains(
		"handler-context.txt",
		"${run.status}",
		"${paths.step_stdout_file}",
		"${paths.step_stderr_file}",
	)
}

func TestUnsupportedContextLookingTextStaysSilent(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "unsupported_context_text.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrNotContains("was left unchanged")
	dagu.ExpectFileContent(
		"unsupported-context.txt",
		"${paths.context}\n${trigger.payload}\n${run.foo}\n${run.id.extra}\n",
	)
}
