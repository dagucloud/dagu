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
	// References to fields the spec does not define are reported by default.
	result.ExpectStderrContains(
		"${context.paths.context}",
		"${context.trigger.payload}",
		"${context.run.foo}",
		"${context.run.id.extra}",
		"reason=unknown_context_field",
		"was left unchanged",
	)
	// Supported fields carry a value only during a run, so validation stays
	// quiet about them unless the operator asks.
	result.ExpectStderrNotContains(
		"${unrelated.context}",
		"reason=namespace_unavailable",
	)

	verbose := dagu.Run("validate", "--show-unresolved", "validation_notices.yaml")
	verbose.ExpectExitCode(0)
	verbose.ExpectStderrContains(
		"${context.run.id}",
		"${context.attempt.id}",
		"${context.run.status}",
		"${context.trigger.actor}",
		"${context.profile.name}",
		"${context.pushback.iteration}",
		"reason=namespace_unavailable",
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
		"root_name=${context.run.root_name}",
		"root_id=${context.run.root_id}",
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
		"${context.attempt.id}",
		"${context.attempt.started_at}",
		"${context.paths.step_stdout_file}",
		"${context.paths.step_stderr_file}",
		"${context.paths.step_output_file}",
	)
}

func TestLegacyBuiltInRunContextAliases(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "--run-id=spec017-legacy", "legacy_aliases.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains(
		"legacy-context-aliases.txt",
		"dag=builtin_legacy_context_aliases",
		"run=spec017-legacy",
		"started=",
		"attempt=",
		"step=capture",
	)
	dagu.ExpectFileNotContains(
		"legacy-context-aliases.txt",
		"${dag.name}",
		"${run.id}",
		"${run.started_at}",
		"${attempt.id}",
		"${step.name}",
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
		"stdout=${context.paths.step_stdout_file}",
		"stderr=${context.paths.step_stderr_file}",
		"env_stdout=UNSET",
		"env_stderr=UNSET",
	)
	dagu.ExpectFileNotContains(
		"handler-context.txt",
		"${context.run.status}",
	)
}

func TestUnknownContextFieldsStaySilentAtRuntime(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "unsupported_context_text.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrNotContains("was left unchanged")
	dagu.ExpectFileContent(
		"unsupported-context.txt",
		"${context.paths.context}\n${context.trigger.payload}\n${context.run.foo}\n${context.run.id.extra}\n${unrelated.context}\n",
	)
}
