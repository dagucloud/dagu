// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestDequeueFirstFromQueue proves the bare `dagu dequeue <queue>` form
// removes the head of the queue and aborts it. Dequeuing hides the attempt
// entirely (its stored attempt is renamed to a hidden entry) rather than
// leaving it visible with status Aborted, so the observable proof is that
// `dagu status` for that run ID no longer finds any record at all.
func TestDequeueFirstFromQueue(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const runID = "cli-dequeue-first"

	dagu.RunWithEnv(env, "enqueue", "--run-id="+runID, "simple.yaml").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "dequeue", "simple")
	result.ExpectExitCode(0)

	status := dagu.RunWithEnv(env, "status", "--run-id="+runID, "simple.yaml")
	status.ExpectNonZeroExitCode()
	status.ExpectStderrContains("dag-run ID not found")
}

// TestDequeueSpecificDAGRun proves `--dag-run` selects exactly one queued
// item to remove, leaving the rest of the queue untouched.
func TestDequeueSpecificDAGRun(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	const keepRunID = "cli-dequeue-keep"
	const removeRunID = "cli-dequeue-remove"

	dagu.RunWithEnv(env, "enqueue", "--run-id="+keepRunID, "simple.yaml").ExpectExitCode(0)
	dagu.RunWithEnv(env, "enqueue", "--run-id="+removeRunID, "simple.yaml").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "dequeue", "simple", "--dag-run=simple:"+removeRunID)
	result.ExpectExitCode(0)

	removedStatus := dagu.RunWithEnv(env, "status", "--run-id="+removeRunID, "simple.yaml")
	removedStatus.ExpectNonZeroExitCode()
	removedStatus.ExpectStderrContains("dag-run ID not found")

	keptStatus := dagu.RunWithEnv(env, "status", "--run-id="+keepRunID, "simple.yaml")
	keptStatus.ExpectExitCode(0)
	require.Contains(t, keptStatus.Stdout(), "Queued")
}

func TestDequeueEmptyQueueFails(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "dequeue", "simple")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("no dag-run found in queue")
}
