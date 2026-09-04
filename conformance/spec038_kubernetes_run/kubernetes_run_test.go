// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec038_kubernetes_run_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestKubernetesRunCreateBasic proves the core contract: a Job is created
// from with.image, its Pod runs the command, and the merged stdout/stderr
// is captured as a step output. With cleanup_policy left at its default
// ("delete"), the Job is gone once the step finishes.
func TestKubernetesRunCreateBasic(t *testing.T) {
	clientset, extraEnv := requireKubernetesCluster(t)

	const label = "dagu-conformance=kubernetes-run-basic"
	deleteJobsByLabel(t, clientset, label)
	t.Cleanup(func() { cleanupJobsByLabel(t, clientset, label) })

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(extraEnv, "start", "create_basic.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("out.txt", "hello\n")

	if job := findJobByLabel(t, clientset, label); job != nil {
		t.Fatalf("job matching %q should have been deleted by the default cleanup_policy", label)
	}
}

// TestKubernetesRunCleanupPolicyKeep proves with.cleanup_policy: keep leaves
// the Job in place instead of deleting it once the step finishes.
func TestKubernetesRunCleanupPolicyKeep(t *testing.T) {
	clientset, extraEnv := requireKubernetesCluster(t)

	const label = "dagu-conformance=kubernetes-run-cleanup-keep"
	deleteJobsByLabel(t, clientset, label)
	t.Cleanup(func() { cleanupJobsByLabel(t, clientset, label) })

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(extraEnv, "start", "cleanup_keep.yaml")
	result.ExpectExitCode(0)

	if job := findJobByLabel(t, clientset, label); job == nil {
		t.Fatalf("job matching %q should still exist with cleanup_policy: keep", label)
	}
}

// TestKubernetesRunCommandFails proves a non-zero command exit fails the
// step. With the default backoff_limit: 0, the Job is marked failed after
// a single Pod attempt rather than being retried, and the error describes
// the Job's own failure condition rather than a bare exit code.
func TestKubernetesRunCommandFails(t *testing.T) {
	clientset, extraEnv := requireKubernetesCluster(t)

	const label = "dagu-conformance=kubernetes-run-command-fails"
	deleteJobsByLabel(t, clientset, label)
	t.Cleanup(func() { cleanupJobsByLabel(t, clientset, label) })

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(extraEnv, "start", "command_fails.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("backoff limit")
}

// TestKubernetesRunWorkingDirAndEnv proves with.working_dir sets the
// container's working directory and with.env entries become real container
// environment variables, visible to a shell running inside the Pod.
func TestKubernetesRunWorkingDirAndEnv(t *testing.T) {
	clientset, extraEnv := requireKubernetesCluster(t)

	const label = "dagu-conformance=kubernetes-run-working-dir-env"
	deleteJobsByLabel(t, clientset, label)
	t.Cleanup(func() { cleanupJobsByLabel(t, clientset, label) })

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(extraEnv, "start", "working_dir_env.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("out.txt", "/tmp", "hello-env")
}

// TestKubernetesRunMissingImageBare proves a narrower validation case: when
// with contains only command, command is extracted into the step's own
// field, with is left empty, and the step fails before contacting the
// cluster with an error naming with.image specifically, from the
// executor's own Go-level check rather than config-schema validation.
func TestKubernetesRunMissingImageBare(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "missing_target_bare.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("with.image", "image is required")
}

// TestKubernetesRunMissingImageWithOther proves the config-schema path:
// when with has a field other than command but no image, DAG build fails
// schema validation before the DAG starts running.
func TestKubernetesRunMissingImageWithOther(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "missing_target_with_other.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("image")
}

// TestKubernetesRunInvalidCleanupPolicy proves with.cleanup_policy rejects
// any value other than delete or keep before the DAG starts running.
func TestKubernetesRunInvalidCleanupPolicy(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "invalid_cleanup_policy.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("cleanup_policy must be either delete or keep")
}

// TestKubernetesRunNegativeActiveDeadline proves a negative
// with.active_deadline is rejected by config-schema validation before the
// DAG starts running.
func TestKubernetesRunNegativeActiveDeadline(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "negative_active_deadline.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("active_deadline")
}
