// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestStartOnlyRunsSelectedStep proves --only executes just the named step:
// the steps before and after it in the chain never run, and the run still
// succeeds because they are recorded as skipped.
func TestStartOnlyRunsSelectedStep(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-start-only"

	result := dagu.RunWithEnv(env, "start", "--run-id="+runID, "--only=second", "only_steps.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("only.out", "second\n")

	status := dagu.RunWithEnv(env, "status", "--run-id="+runID, "only_steps.yaml")
	status.ExpectExitCode(0)
	require.Equal(t, "Succeeded", resultStatus(status.Stdout()))
	require.Contains(t, status.Stdout(), "first [skipped]")
	require.Contains(t, status.Stdout(), "third [skipped]")
}

// TestStartOnlyReusesOutputs proves --outputs-from feeds a finished run's
// declared outputs and work-directory files to the selected step.
func TestStartOnlyReusesOutputs(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	source := dagu.RunWithEnv(env, "start", "--run-id=cli-start-source", "only_outputs.yaml")
	source.ExpectExitCode(0)
	dagu.WriteFile("consume.out", "")

	result := dagu.RunWithEnv(env, "start", "--run-id=cli-start-reuse", "--only=consume", "--outputs-from=cli-start-source", "only_outputs.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("consume.out", "from-source\nshared\n")
}

// TestStartOnlyKeepsReference proves that without --outputs-from a reference
// to a skipped step's output stays unresolved, as Spec 007 requires for an
// output that was never published.
func TestStartOnlyKeepsReference(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "start", "--only=consume", "only_outputs.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("consume.out", "${steps.produce.outputs.value}\nmissing\n")
}

func TestStartOutputsFromNeedsOnly(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(sharedEnv(t), "start", "--outputs-from=cli-start-source", "only_steps.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("--outputs-from requires --only")
	dagu.ExpectNoFile("only.out")
}

func TestStartOnlyUnknownStep(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(sharedEnv(t), "start", "--only=missing", "only_steps.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`unknown step "missing"`)
	dagu.ExpectNoFile("only.out")
}
