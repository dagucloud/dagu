// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec031_human_task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/conformance/harness"
)

func sharedEnv(t *testing.T) []string {
	t.Helper()
	return []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
}

func startWaiting(t *testing.T, dagu *harness.Runner, env []string, runID, file string, extraArgs ...string) {
	t.Helper()
	queueProcessor := dagu.StartWithEnv(env, "scheduler", "--dags="+dagu.ProjectPath("."))
	select {
	case <-queueProcessor.Done():
		t.Fatalf("scheduler exited during startup: %s", queueProcessor.FailureOutput())
	case <-time.After(100 * time.Millisecond):
	}

	args := []string{"start", "--run-id=" + runID}
	args = append(args, extraArgs...)
	args = append(args, file)
	result := dagu.RunWithEnv(env, args...)
	result.ExpectExitCode(0)

	status := waitForStatus(t, dagu, env, runID, file, "Waiting")
	status.ExpectStderr("")
}

func complete(t *testing.T, dagu *harness.Runner, env []string, runID, step, file string, inputs ...string) *harness.Result {
	t.Helper()
	args := []string{"human-task", "complete", "--run-id=" + runID, "--step=" + step}
	args = append(args, inputs...)
	args = append(args, fixtureDAGName(file))
	return dagu.RunWithEnv(env, args...)
}

func fixtureDAGName(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	return "spec031_" + base
}

func waitForStatus(
	t *testing.T,
	dagu *harness.Runner,
	env []string,
	runID string,
	file string,
	status string,
) *harness.Result {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var result *harness.Result
	for time.Now().Before(deadline) {
		result = dagu.RunWithEnv(env, "status", "--run-id="+runID, file)
		if result.ExitCode() == 0 && strings.Contains(result.Stdout(), status) {
			return result
		}
		time.Sleep(50 * time.Millisecond)
	}
	if result == nil {
		t.Fatal("status command was not run")
	}
	t.Fatalf("DAG-run %s did not reach %s\nstdout:\n%s\nstderr:\n%s", runID, status, result.Stdout(), result.Stderr())
	return nil
}

func waitForFileContent(t *testing.T, path, expected string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		actual, err := os.ReadFile(path) // #nosec G304 -- the caller supplies a test-project path.
		if err == nil && string(actual) == expected {
			return
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("reading %s: %v", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	actual, err := os.ReadFile(path) // #nosec G304 -- the caller supplies a test-project path.
	t.Fatalf("file %s did not contain %q: content=%q err=%v", path, expected, actual, err)
}
