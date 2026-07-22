// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec031_human_task_test

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcknowledgementCheckpointAndResume(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "spec031-ack"

	startWaiting(t, dagu, env, runID, "acknowledgement.yaml")
	status := waitForStatus(t, dagu, env, runID, "acknowledgement.yaml", "Waiting")
	status.ExpectStdoutNotContains("form:")
	require.Contains(t, status.Stdout(), "maintenance_started")
	require.Contains(t, status.Stdout(), "Confirm that maintenance has started")

	result := complete(t, dagu, env, runID, "maintenance_started", "acknowledgement.yaml")
	result.ExpectExitCode(0)
	result.ExpectStdout("Completed human task maintenance_started; DAG-run queued for resume.\n")
	result.ExpectStderr("")

	waitForStatus(t, dagu, env, runID, "acknowledgement.yaml", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("continued.txt"), "continued\n")

	repeat := complete(t, dagu, env, runID, "maintenance_started", "acknowledgement.yaml")
	repeat.ExpectExitCode(0)
	repeat.ExpectStdout("Human task maintenance_started was already completed.\n")
	repeat.ExpectStderr("")
}

func TestTypedFormDefaultsAndOutputs(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "spec031-form"

	startWaiting(t, dagu, env, runID, "typed_form.yaml")
	status := waitForStatus(t, dagu, env, runID, "typed_form.yaml", "Waiting")
	require.Contains(t, status.Stdout(), "release_review")
	require.Contains(t, status.Stdout(), "Choose the release target")
	normalizedStatus := collapseStatusLines(status.Stdout())
	require.Contains(t, normalizedStatus, `{"type":"object"`)
	require.Contains(t, normalizedStatus, `"additionalProperties":false`)
	require.Contains(t, normalizedStatus, `"title":"Release review"`)
	require.Contains(t, normalizedStatus, `"environment":{"type":"string","title":"Environment","enum":["staging","production"]}`)
	require.Contains(t, normalizedStatus, `"notify":{"type":"boolean","default":true}`)
	require.Contains(t, normalizedStatus, `"replicas":{"type":"integer","default":2,"minimum":1}`)

	result := complete(t, dagu, env, runID, "release_review", "typed_form.yaml", "--input=environment=production")
	result.ExpectExitCode(0)
	result.ExpectStdout("Completed human task release_review; DAG-run queued for resume.\n")
	result.ExpectStderr("")

	waitForStatus(t, dagu, env, runID, "typed_form.yaml", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("release.txt"), "production,2,true\n")

	repeat := complete(t, dagu, env, runID, "release_review", "typed_form.yaml", "--inputs-json", `{"notify":true,"environment":"production","replicas":2}`)
	repeat.ExpectExitCode(0)
	repeat.ExpectStdout("Human task release_review was already completed.\n")
	repeat.ExpectStderr("")
}

func TestMultipleAndSequentialCheckpoints(t *testing.T) {
	t.Run("independent ordinary branch", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-independent"

		startWaiting(t, dagu, env, runID, "independent_branch.yaml")
		waitForFileContent(t, dagu.ProjectPath("independent.txt"), "independent\n")
		dagu.ExpectNoFile("resumed.txt")

		result := complete(t, dagu, env, runID, "review", "independent_branch.yaml")
		result.ExpectExitCode(0)
		result.ExpectStdout("Completed human task review; DAG-run queued for resume.\n")
		result.ExpectStderr("")
		waitForStatus(t, dagu, env, runID, "independent_branch.yaml", "Succeeded")
		waitForFileContent(t, dagu.ProjectPath("resumed.txt"), "resumed\n")
	})

	t.Run("independent tasks", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-multiple"

		startWaiting(t, dagu, env, runID, "multiple_waiting.yaml")
		first := complete(t, dagu, env, runID, "review_a", "multiple_waiting.yaml")
		first.ExpectExitCode(0)
		first.ExpectStdout("Completed human task review_a; DAG-run remains waiting.\n")
		first.ExpectStderr("")
		dagu.ExpectNoFile("deployed.txt")

		second := complete(t, dagu, env, runID, "review_b", "multiple_waiting.yaml")
		second.ExpectExitCode(0)
		second.ExpectStdout("Completed human task review_b; DAG-run queued for resume.\n")
		second.ExpectStderr("")
		waitForStatus(t, dagu, env, runID, "multiple_waiting.yaml", "Succeeded")
		waitForFileContent(t, dagu.ProjectPath("deployed.txt"), "deployed\n")
	})

	t.Run("sequential tasks", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-sequential"

		startWaiting(t, dagu, env, runID, "sequential_waiting.yaml")
		first := complete(t, dagu, env, runID, "first_review", "sequential_waiting.yaml")
		first.ExpectExitCode(0)
		first.ExpectStdout("Completed human task first_review; DAG-run queued for resume.\n")
		first.ExpectStderr("")
		status := waitForStatus(t, dagu, env, runID, "sequential_waiting.yaml", "Waiting")
		require.Contains(t, status.Stdout(), "second_review")
		waitForFileContent(t, dagu.ProjectPath("between.txt"), "between\n")

		second := complete(t, dagu, env, runID, "second_review", "sequential_waiting.yaml")
		second.ExpectExitCode(0)
		second.ExpectStdout("Completed human task second_review; DAG-run queued for resume.\n")
		second.ExpectStderr("")
		waitForStatus(t, dagu, env, runID, "sequential_waiting.yaml", "Succeeded")
		waitForFileContent(t, dagu.ProjectPath("finished.txt"), "finished\n")
	})
}

func TestStoredPromptFormAndDAGSnapshot(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "spec031-snapshot"

	startWaiting(t, dagu, env, runID, "prompt_snapshot.yaml")
	status := waitForStatus(t, dagu, env, runID, "prompt_snapshot.yaml", "Waiting")
	require.Contains(t, status.Stdout(), "Review release for production as operator in "+runID)
	require.Contains(t, status.Stdout(), `${REVIEWER}`)
	require.Contains(t, status.Stdout(), `${params.target}`)

	changed, err := os.ReadFile(dagu.ProjectPath("prompt_snapshot_changed.yaml")) // #nosec G304 -- fixed test fixture path.
	require.NoError(t, err)
	dagu.WriteFile("prompt_snapshot.yaml", string(changed))

	stored := waitForStatus(t, dagu, env, runID, "prompt_snapshot.yaml", "Waiting")
	require.Contains(t, stored.Stdout(), "Review release for production as operator in "+runID)
	require.NotContains(t, stored.Stdout(), "This prompt must not replace")
	require.NoError(t, os.Remove(dagu.ProjectPath("prompt_snapshot.yaml")))

	result := complete(t, dagu, env, runID, "review", "prompt_snapshot.yaml", "--input=environment=production")
	result.ExpectExitCode(0)
	result.ExpectStdout("Completed human task review; DAG-run queued for resume.\n")
	result.ExpectStderr("")
	waitForStatus(t, dagu, env, runID, "spec031_prompt_snapshot", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("snapshot.txt"), "production\n")
}

func TestAdditionalPropertiesDoNotCreateOutputs(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "spec031-additional"

	startWaiting(t, dagu, env, runID, "additional_properties.yaml")
	input := `{"metadata":{"team":"platform","flags":[true,null]},"environment":"staging"}`
	result := complete(t, dagu, env, runID, "collect", "additional_properties.yaml", "--inputs-json", input)
	result.ExpectExitCode(0)
	result.ExpectStdout("Completed human task collect; DAG-run queued for resume.\n")
	result.ExpectStderr("")
	waitForStatus(t, dagu, env, runID, "additional_properties.yaml", "Succeeded")
	waitForFileContent(t, dagu.ProjectPath("additional.txt"), "staging\n")

	repeat := complete(t, dagu, env, runID, "collect", "additional_properties.yaml", "--inputs-json", `{"environment":"staging","metadata":{"flags":[true,null],"team":"platform"}}`)
	repeat.ExpectExitCode(0)
	repeat.ExpectStdout("Human task collect was already completed.\n")
	repeat.ExpectStderr("")
}

func TestConcurrentCompletion(t *testing.T) {
	t.Run("same canonical input", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-concurrent-same"
		startWaiting(t, dagu, env, runID, "concurrent.yaml")

		results := runConcurrentCompletions(t, dagu, env, runID, "blue", "blue")
		for _, result := range results {
			assert.Equal(t, 0, result.ExitCode(), "stdout:\n%s\nstderr:\n%s", result.Stdout(), result.Stderr())
			assert.Empty(t, result.Stderr())
		}
		waitForStatus(t, dagu, env, runID, "concurrent.yaml", "Succeeded")
		waitForFileContent(t, dagu.ProjectPath("concurrent.txt"), "blue\n")
	})

	t.Run("different canonical input", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-concurrent-different"
		startWaiting(t, dagu, env, runID, "concurrent.yaml")

		results := runConcurrentCompletions(t, dagu, env, runID, "blue", "green")
		successes := 0
		failures := 0
		for _, result := range results {
			if result.ExitCode() == 0 {
				successes++
				assert.Empty(t, result.Stderr())
				continue
			}
			failures++
			assert.Empty(t, result.Stdout())
			assert.Contains(t, result.Stderr(), "different input")
		}
		assert.Equal(t, 1, successes)
		assert.Equal(t, 1, failures)
		waitForStatus(t, dagu, env, runID, "concurrent.yaml", "Succeeded")
	})
}

func TestPreconditionSkipAndDryRun(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	start := dagu.RunWithEnv(env, "start", "--run-id=spec031-skipped", "precondition_skipped.yaml")
	start.ExpectExitCode(0)
	waitForStatus(t, dagu, env, "spec031-skipped", "precondition_skipped.yaml", "Succeeded")
	dagu.ExpectNoFile("precondition.txt")
	rejected := complete(t, dagu, env, "spec031-skipped", "skipped_review", "precondition_skipped.yaml")
	rejected.ExpectNonZeroExitCode()
	rejected.ExpectStdout("")
	rejected.ExpectStderrContains("spec031-skipped", "not waiting")
	rejected.ExpectStderrNotContains("Usage:")

	dry := dagu.RunWithEnv(env, "dry", "prompt_snapshot.yaml")
	dry.ExpectExitCode(0)
	dagu.ExpectNoFile("snapshot.txt")
}

func collapseStatusLines(stdout string) string {
	var result strings.Builder
	for line := range strings.SplitSeq(stdout, "\n") {
		if index := strings.Index(line, "│     "); index >= 0 {
			line = line[index+len("│     "):]
		}
		result.WriteString(strings.TrimSpace(line))
	}
	return result.String()
}

func runConcurrentCompletions(
	t *testing.T,
	dagu *harness.Runner,
	env []string,
	runID string,
	first string,
	second string,
) []*harness.Result {
	t.Helper()
	values := []string{first, second}
	results := make([]*harness.Result, len(values))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index, value := range values {
		wg.Go(func() {
			<-start
			results[index] = complete(t, dagu, env, runID, "choose", "concurrent.yaml", "--input=lane="+value)
		})
	}
	close(start)
	wg.Wait()
	return results
}
