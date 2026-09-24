// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestRetryReexecutesStep proves the basic contract: retrying a previously
// executed run with the same run ID actually re-runs the step, not just
// re-reports the old status. The step appends a marker each time it runs, so
// two attempts leave two marker lines.
func TestRetryReexecutesStep(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-retry-run"

	first := dagu.RunWithEnv(env, "start", "--run-id="+runID, "failing.yaml")
	first.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("attempts.out", "attempt\n")

	retry := dagu.RunWithEnv(env, "retry", "--run-id="+runID, "failing")
	retry.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("attempts.out", "attempt\nattempt\n")
}

// TestRetryStepFlagReexecutesOnlyThatStep proves --step scopes the retry to
// one step: an earlier, already-succeeded step must not re-run.
func TestRetryStepFlagReexecutesOnlyThatStep(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-retry-step"

	first := dagu.RunWithEnv(env, "start", "--run-id="+runID, "multi_step_failing.yaml")
	first.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("steps.out", "first\nsecond\n")

	retry := dagu.RunWithEnv(env, "retry", "--run-id="+runID, "--step=second", "multi_step_failing")
	retry.ExpectNonZeroExitCode()
	dagu.ExpectFileContent("steps.out", "first\nsecond\nsecond\n")
}

// TestRetryReplacesStdoutArtifact proves a stdout artifact holds the output of
// the latest attempt, not the output of every attempt of the run.
func TestRetryReplacesStdoutArtifact(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-retry-artifact"
	const artifact = "artifacts/*/*/*/*_artifact_retry_*/report.txt"

	first := dagu.RunWithEnv(env, "start", "--run-id="+runID, "artifact_retry.yaml")
	first.ExpectNonZeroExitCode()
	dagu.ExpectGlobFileContent(artifact, "line\n")

	dagu.WriteFile("ok", "")
	retry := dagu.RunWithEnv(env, "retry", "--run-id="+runID, "artifact_retry")
	retry.ExpectExitCode(0)
	dagu.ExpectGlobFileContent(artifact, "line\n")
}

func TestRetryDownstreamRequiresStep(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-retry-downstream"

	first := dagu.RunWithEnv(env, "start", "--run-id="+runID, "failing.yaml")
	first.ExpectNonZeroExitCode()

	retry := dagu.RunWithEnv(env, "retry", "--run-id="+runID, "--downstream", "failing")
	retry.ExpectNonZeroExitCode()
	retry.ExpectStderrContains("--downstream requires --step")
}

func TestRetryMissingRunIDFails(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "retry", "failing")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("run-id")
}
