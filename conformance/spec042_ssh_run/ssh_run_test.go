// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec042_ssh_run holds black-box conformance tests for
// Spec 042: SSH Run Action.
package spec042_ssh_run_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
// Captures the rest of the line rather than \S+, so a project path
// containing spaces isn't truncated.
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stepStdout reads the exact bytes a step wrote to stdout, by locating its
// captured-output log file from dagu start's own tree render and reading it
// directly. This avoids round-tripping through output capture, which would
// not distinguish "the whole multi-command script ran as one remote session"
// from "each command ran separately" the way reading the raw stream does.
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	match := stdoutLogPattern.FindStringSubmatch(daguStartOutput)
	require.Lenf(t, match, 2, "expected a stdout log path in output:\n%s", daguStartOutput)
	path := strings.TrimSpace(match[1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// TestSSHRunLive proves ssh.run's core execution contract against a real
// SSH server: command execution and value resolution, the working_dir
// contract (explicit dir, or the SSH user's home when unset -- never the
// local DAG's own working directory), that a command list runs as a single
// remote script sharing shell state across entries and stopping at the
// first failing entry, and that an authentication failure is a runtime
// error (ssh.run has no build-time validator for connection fields; only
// with.command is checked before the DAG runs), and that a local timeout
// ends the step promptly without stopping the remote command it started.
func TestSSHRunLive(t *testing.T) {
	dockerClient := requireDockerDaemon(t)
	port := startSSHDContainer(t, dockerClient)
	env := []string{"SSH_PORT=" + strconv.Itoa(port)}

	t.Run("resolved command runs and its output is capturable", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "basic.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "hello from remote\n", stepStdout(t, result.Stdout()))
	})

	t.Run("output captures via output: the same as any other step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "output_capture.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("out.txt", "captured")
	})

	t.Run("explicit working_dir sets the remote command's directory", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "working_dir_set.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "/tmp\n", stepStdout(t, result.Stdout()))
	})

	t.Run("no working_dir runs in the SSH user's home directory", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "working_dir_unset.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, sshTestHomeDir+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("a command list shares shell state across entries", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "multi_command_shared_state.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "read: set-by-first-command\n", stepStdout(t, result.Stdout()))
	})

	t.Run("a command list stops at the first failing entry", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "multi_command_stop_on_failure.yaml")
		result.ExpectNonZeroExitCode()
		require.Equal(t, "first\n", stepStdout(t, result.Stdout()))
		result.ExpectStderrContains("Process exited with status 1")
	})

	t.Run("the command's exit code is reported in the error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "exit_code.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("Process exited with status 7")
	})

	t.Run("a rejected password is a runtime authentication failure", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "wrong_password.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("unable to authenticate")
	})

	// A local timeout closes dagu's local SSH session promptly, but sends no
	// signal to the remote command: the remote sleep-then-touch keeps running
	// on the server after the local step has already been reported timed out.
	// Proven black-box, through a second ssh.run step, not by inspecting the
	// container directly. Deliberately not t.Parallel(): its assertion on how
	// quickly the step fails is sensitive to connection contention against
	// the shared test sshd from the other subtests running at the same time.
	t.Run("a local timeout does not stop the remote command", func(t *testing.T) {
		dagu := harness.NewRunner(t)
		start := time.Now()
		result := dagu.RunWithEnv(env, "start", "timeout_leaves_remote_process_running.yaml")
		elapsed := time.Since(start)
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("step timed out")
		require.Lessf(t, elapsed, 2500*time.Millisecond,
			"expected the step to time out quickly rather than waiting for the remote sleep, took %s", elapsed)

		// Poll for the orphaned remote sleep to finish rather than sleeping a
		// fixed duration matching the fixture's own "sleep 3": a fixed wait
		// with no margin would flake under any timing jitter (CI load,
		// connection overhead) that pushes the remote command past it.
		checkDagu := harness.NewRunner(t)
		deadline := time.Now().Add(harness.WaitTimeout(t))
		var check *harness.Result
		for time.Now().Before(deadline) {
			check = checkDagu.RunWithEnv(env, "start", "check_remote_marker_exists.yaml")
			if check.ExitCode() == 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if check == nil || check.ExitCode() != 0 {
			t.Fatalf("remote marker never appeared within %s; last attempt:\nstdout:\n%s\nstderr:\n%s",
				harness.WaitTimeout(t), check.Stdout(), check.Stderr())
		}
	})
}

// TestSSHRunEmptyHostValidates proves the positive side of "connection
// fields are validated only at runtime, not at DAG-build time": a step
// with with.command set but with.host entirely empty still passes
// dagu validate, since only with.command is checked at build time.
func TestSSHRunEmptyHostValidates(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "empty_host.yaml")
	result.ExpectExitCode(0)
}

// TestSSHRunMissingCommandFailsValidate proves with.command is rejected at
// DAG-build time itself, via dagu validate, not merely inferred from dagu
// start failing before it would otherwise attempt a connection.
func TestSSHRunMissingCommandFailsValidate(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "missing_command.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("with.command is required")
}

// TestSSHRunNoServer proves the two failure modes that do not need a real
// SSH server: with.command validated at DAG-build time, and a connection
// that fails outright (nothing listening) at runtime.
func TestSSHRunNoServer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "missing with.command is rejected before the DAG runs",
			file:        "missing_command.yaml",
			stderrParts: []string{"with.command is required"},
		},
		{
			name:        "nothing listening on the target port fails to connect",
			file:        "connection_refused.yaml",
			stderrParts: []string{"connection refused"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}
