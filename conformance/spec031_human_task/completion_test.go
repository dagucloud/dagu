// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec031_human_task_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestCompletionCommandInputErrorsDoNotMutateWaitingRun(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "spec031-input-errors"
	startWaiting(t, dagu, env, runID, "typed_form.yaml")

	cases := []struct {
		name  string
		args  []string
		parts []string
	}{
		{
			name:  "missing run id",
			args:  []string{"human-task", "complete", "--step=release_review", "typed_form.yaml"},
			parts: []string{"run-id"},
		},
		{
			name:  "missing step",
			args:  []string{"human-task", "complete", "--run-id=" + runID, "typed_form.yaml"},
			parts: []string{"step"},
		},
		{
			name: "mutually exclusive input flags",
			args: []string{
				"human-task", "complete", "--run-id=" + runID, "--step=release_review",
				"--input=environment=production", `--inputs-json={"environment":"production"}`, "typed_form.yaml",
			},
			parts: []string{"--input", "--inputs-json"},
		},
		{
			name:  "invalid pair",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--input=environment"),
			parts: []string{"--input", "key=value"},
		},
		{
			name:  "duplicate pair",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--input=environment=staging", "--input=environment=production"),
			parts: []string{"environment", "duplicate"},
		},
		{
			name:  "invalid json",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--inputs-json", `{`),
			parts: []string{"--inputs-json", "JSON"},
		},
		{
			name:  "trailing json",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--inputs-json", `{} {}`),
			parts: []string{"--inputs-json", "JSON"},
		},
		{
			name:  "null json",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--inputs-json", `null`),
			parts: []string{"--inputs-json", "JSON object"},
		},
		{
			name:  "duplicate json member",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--inputs-json", `{"environment":"staging","environment":"production"}`),
			parts: []string{"environment", "duplicate", "JSON"},
		},
		{
			name:  "wrong json scalar type",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--inputs-json", `{"environment":"production","replicas":"3"}`),
			parts: []string{"release_review", "replicas", "integer"},
		},
		{
			name:  "missing required property",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--inputs-json", `{}`),
			parts: []string{"release_review", "environment", "required"},
		},
		{
			name:  "undeclared property",
			args:  completionArgs(runID, "release_review", "typed_form.yaml", "--input=region=us"),
			parts: []string{"release_review", "region", "additional properties"},
		},
		{
			name:  "unknown step",
			args:  completionArgs(runID, "missing_review", "typed_form.yaml"),
			parts: []string{"missing_review", "human task"},
		},
	}
	for _, tc := range cases {
		t.Log(tc.name)
		result := dagu.RunWithEnv(env, tc.args...)
		assertCompletionError(t, result, tc.parts...)
		waitForStatus(t, dagu, env, runID, "typed_form.yaml", "Waiting")
		dagu.ExpectNoFile("release.txt")
	}
}

func TestNestedDuplicateJSONMemberIsRejected(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "spec031-nested-duplicate"
	startWaiting(t, dagu, env, runID, "additional_properties.yaml")

	result := complete(
		t, dagu, env, runID, "collect", "additional_properties.yaml",
		"--inputs-json", `{"environment":"staging","metadata":{"team":"one","team":"two"}}`,
	)
	assertCompletionError(t, result, "team", "duplicate", "JSON")
	waitForStatus(t, dagu, env, runID, "additional_properties.yaml", "Waiting")
	dagu.ExpectNoFile("additional.txt")
}

func TestAcknowledgementSizeConflictAndTerminalErrors(t *testing.T) {
	t.Run("acknowledgement rejects input", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-ack-input"
		startWaiting(t, dagu, env, runID, "acknowledgement.yaml")
		result := complete(t, dagu, env, runID, "maintenance_started", "acknowledgement.yaml", "--input=value=yes")
		assertCompletionError(t, result, "maintenance_started", "does not accept input")
		waitForStatus(t, dagu, env, runID, "acknowledgement.yaml", "Waiting")
	})

	t.Run("maximum size", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-size"
		startWaiting(t, dagu, env, runID, "max_output_size.yaml")
		result := complete(t, dagu, env, runID, "size_review", "max_output_size.yaml", "--input=value=this-is-too-large")
		assertCompletionError(t, result, "size_review", "maximum size")
		waitForStatus(t, dagu, env, runID, "max_output_size.yaml", "Waiting")
	})

	t.Run("different prior input", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-conflict"
		startWaiting(t, dagu, env, runID, "concurrent.yaml")
		first := complete(t, dagu, env, runID, "choose", "concurrent.yaml", "--input=lane=blue")
		first.ExpectExitCode(0)
		waitForStatus(t, dagu, env, runID, "concurrent.yaml", "Succeeded")
		conflict := complete(t, dagu, env, runID, "choose", "concurrent.yaml", "--input=lane=green")
		assertCompletionError(t, conflict, "choose", "different input")
	})

	t.Run("terminal run", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		const runID = "spec031-terminal"
		start := dagu.RunWithEnv(env, "start", "--run-id="+runID, "precondition_skipped.yaml")
		start.ExpectExitCode(0)
		waitForStatus(t, dagu, env, runID, "precondition_skipped.yaml", "Succeeded")
		result := complete(t, dagu, env, runID, "skipped_review", "precondition_skipped.yaml")
		assertCompletionError(t, result, runID, "not waiting")
	})

	t.Run("missing run", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		env := sharedEnv(t)
		result := complete(t, dagu, env, "missing-run", "maintenance_started", "acknowledgement.yaml")
		assertCompletionError(t, result, "spec031_acknowledgement", "missing-run")
	})
}

func TestCompletionRequiresLocalCLIContext(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	added := dagu.RunWithEnv(env, "context", "add", "remote", "--server=http://127.0.0.1:1", "--api-key=dagu_test")
	added.ExpectExitCode(0)
	used := dagu.RunWithEnv(env, "context", "use", "remote")
	used.ExpectExitCode(0)

	result := dagu.RunWithEnv(
		env,
		"human-task", "complete", "--run-id=any-run", "--step=any-step", "acknowledgement.yaml",
	)
	assertCompletionError(t, result, "human-task complete", "local context")
}

func completionArgs(runID, step, file string, inputArgs ...string) []string {
	args := []string{"human-task", "complete", "--run-id=" + runID, "--step=" + step}
	args = append(args, inputArgs...)
	return append(args, file)
}

func assertCompletionError(t *testing.T, result *harness.Result, parts ...string) {
	t.Helper()
	result.ExpectNonZeroExitCode()
	result.ExpectStdout("")
	result.ExpectStderrContains(parts...)
	result.ExpectStderrNotContains("Usage:")
	require.NotEmpty(t, result.Stderr())
}
