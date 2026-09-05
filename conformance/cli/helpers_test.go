// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli holds black-box conformance tests for CLI commands that are
// not owned by any single numbered spec: general dag-run lifecycle
// management (ls, rm, ps, dequeue, retry, restart, stop) and standalone
// utility commands (profile, config, context, version, cleanup, schema,
// example, sync). Flag-level depth for commands whose core behavior already
// has a natural home in a numbered spec's conformance package belongs there
// instead of here.
package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// resultLinePattern matches the "Result: <status>" line `dagu status` and
// `dagu start` print at the end of their tree render, the one place the
// root DAG-run's own terminal status appears verbatim.
var resultLinePattern = regexp.MustCompile(`(?m)^Result: (\S+)`)

// sharedEnv returns an env override giving every command invoked with it the
// same DAGU_HOME (so commands that must see each other's state, such as
// `start` followed by `stop`, actually do -- the harness gives each call a
// fresh isolated home by default) and pointing the configured DAGs directory
// at the isolated project root, so plain filenames placed there by
// dagu.WriteFile are name-resolvable by catalog-based commands such as `ls`,
// `rm -d`, `stop`, and `retry`, exactly the way `start`/`validate`/`dry`
// already resolve a bare relative file path without any such override.
func sharedEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"),
		"DAGU_DAGS_DIR=.",
	}
}

// waitForStatus polls `dagu status --run-id=<runID> <file>` until its
// "Result: <status>" line names the given status, or fails the test after
// harness.WaitTimeout. It compares the normalized root result rather than
// searching the whole output, since the tree render also prints DAG and
// step names that could otherwise coincidentally contain the status word.
func waitForStatus(t *testing.T, dagu *harness.Runner, env []string, runID, file, status string) *harness.Result {
	t.Helper()

	deadline := time.Now().Add(harness.WaitTimeout(t))
	var result *harness.Result
	for time.Now().Before(deadline) {
		result = dagu.RunWithEnv(env, "status", "--run-id="+runID, file)
		if result.ExitCode() == 0 && resultStatus(result.Stdout()) == status {
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

// resultStatus extracts the status named on the "Result: <status>" line, or
// "" if no such line is present.
func resultStatus(stdout string) string {
	match := resultLinePattern.FindStringSubmatch(stdout)
	if match == nil {
		return ""
	}
	return match[1]
}

// startBackgroundAndWaitFor starts file in the background and polls until
// markerFile exists in the project directory, confirming the run has
// actually reached a running, in-flight state before the caller acts on it
// (e.g. `dagu stop`, `dagu ps`, `dagu dequeue`). This avoids racing an
// arbitrary wall-clock delay against process-launch overhead, the same
// pattern used for the parallel-fan-out abort test in
// conformance/spec018_parallel_foreach.
func startBackgroundAndWaitFor(t *testing.T, dagu *harness.Runner, env []string, markerFile string, args ...string) *harness.Process {
	t.Helper()

	proc := dagu.StartWithEnv(env, args...)

	deadline := time.Now().Add(harness.WaitTimeout(t))
	for {
		if _, err := os.Stat(dagu.ProjectPath(markerFile)); err == nil {
			return proc
		}
		if time.Now().After(deadline) {
			proc.Stop()
			t.Fatalf("%s never appeared: %s", markerFile, proc.FailureOutput())
		}
		select {
		case <-proc.Done():
			t.Fatalf("process exited before %s appeared: %s", markerFile, proc.FailureOutput())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// waitForProcessDone blocks until proc exits, failing the test if it takes
// longer than harness.WaitTimeout.
func waitForProcessDone(t *testing.T, proc *harness.Process) {
	t.Helper()

	select {
	case <-proc.Done():
	case <-time.After(harness.WaitTimeout(t)):
		proc.Stop()
		t.Fatal("process did not exit in time")
	}
}
