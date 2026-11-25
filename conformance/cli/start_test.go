// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"strings"
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

// TestStartOnlyOutputFrom proves --output-from feeds the declared outputs of
// one finished run to the selected step.
func TestStartOnlyOutputFrom(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	source := dagu.RunWithEnv(env, "start", "--run-id=cli-start-output-from", "only_outputs.yaml")
	source.ExpectExitCode(0)
	dagu.WriteFile("consume.out", "")

	result := dagu.RunWithEnv(env, "start", "--only=consume", "--output-from=produce=cli-start-output-from", "only_outputs.yaml")
	result.ExpectExitCode(0)
	// Only --outputs-from carries the work directory of the source run.
	dagu.ExpectFileContent("consume.out", "from-source\nmissing\n")
}

// TestStartOnlyOutputFromMissingRun proves that a run that does not exist
// stops the command before any step runs.
func TestStartOnlyOutputFromMissingRun(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(sharedEnv(t), "start", "--only=consume", "--output-from=produce=cli-start-no-such-run", "only_outputs.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("step produce cannot be reused from run cli-start-no-such-run")
	dagu.ExpectNoFile("consume.out")
}

// TestStartOnlySetsOutput proves the deprecated --output still feeds a
// declared output to the selected step with no earlier run, through a strict
// step-output reference.
func TestStartOnlySetsOutput(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(sharedEnv(t), "start", "--only=consume", "--output=produce.value=given", "only_outputs.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("consume.out", "given\nmissing\n")
}

// TestStartOnlyOutputOverridesSource proves --output wins over the value
// --outputs-from carries, while the source run's work directory is still
// copied.
func TestStartOnlyOutputOverridesSource(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	source := dagu.RunWithEnv(env, "start", "--run-id=cli-start-override-source", "only_outputs.yaml")
	source.ExpectExitCode(0)
	dagu.WriteFile("consume.out", "")

	result := dagu.RunWithEnv(env, "start", "--only=consume", "--outputs-from=cli-start-override-source", "--output=produce.value=given", "only_outputs.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("consume.out", "given\nshared\n")
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

// changedOutputsYAML gives consume the output of produce; the edit of
// produce makes a run before it a run of another produce.
const changedOutputsYAML = `working_dir: .
steps:
  - id: produce
    run: printf 'value=from-source\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: value
  - id: consume
    depends: produce
    run: printf '%s\n' '${steps.produce.outputs.value}' > consume.out
`

// TestStartOnlyChangedStep proves that a skipped step that changed since the
// run it carries outputs from is refused with what changed and how to reuse
// it anyway, and that --allow-changed reuses its outputs.
func TestStartOnlyChangedStep(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	dagu.WriteFile("changed_outputs.yaml", changedOutputsYAML)
	source := dagu.RunWithEnv(env, "start", "--run-id=cli-start-changed-source", "changed_outputs.yaml")
	source.ExpectExitCode(0)
	dagu.WriteFile("changed_outputs.yaml", strings.Replace(changedOutputsYAML, "from-source", "from-edit", 1))
	dagu.WriteFile("consume.out", "")

	refused := dagu.RunWithEnv(env, "start", "--only=consume", "--outputs-from=cli-start-changed-source", "changed_outputs.yaml")
	refused.ExpectNonZeroExitCode()
	refused.ExpectStderrContains("step produce cannot be reused from run cli-start-changed-source: the step changed since that run; pass --allow-changed to reuse the changed steps anyway")
	// The refused run started no step.
	dagu.ExpectFileContent("consume.out", "")

	allowed := dagu.RunWithEnv(env, "start", "--only=consume", "--outputs-from=cli-start-changed-source", "--allow-changed", "changed_outputs.yaml")
	allowed.ExpectExitCode(0)
	allowed.ExpectStderrContains("reusing step produce from run cli-start-changed-source although the step changed since that run")
	dagu.ExpectFileContent("consume.out", "from-source\n")
}
