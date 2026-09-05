// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec043_sftp_transfer holds black-box conformance tests for
// Spec 043: SFTP Transfer.
package spec043_sftp_transfer_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// lastStepStdout reads the exact bytes the last step in the run wrote to
// stdout, by locating that step's captured-output log file from dagu
// start's own tree render (the last "stdout:" line in a multi-step run)
// and reading it directly. Fixtures that chain an sftp step with an
// ssh.run readback step use this to inspect remote state through dagu's
// own observable output, rather than by reaching into the container
// directly from Go test code.
func lastStepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	matches := stdoutLogPattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a stdout log path in output:\n%s", daguStartOutput)
	path := strings.TrimSpace(matches[len(matches)-1][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// TestSFTPTransferLive proves sftp.upload/sftp.download's core contract
// against a real SFTP server: single-file transfer in both directions,
// permission preservation, atomic replacement of an existing destination
// on upload, recursive directory transfer, and that with.direction set to
// the value the action name already implies has no effect.
func TestSFTPTransferLive(t *testing.T) {
	dockerClient := requireDockerDaemon(t)
	port, containerID := startSSHDContainer(t, dockerClient)
	env := []string{"SSH_PORT=" + strconv.Itoa(port)}

	t.Run("uploads a file and preserves its permissions", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("upload_source.txt", "hello upload")
		require.NoError(t, os.Chmod(dagu.ProjectPath("upload_source.txt"), 0o640))

		result := dagu.RunWithEnv(env, "start", "upload_file.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "640\nhello upload", lastStepStdout(t, result.Stdout()))
	})

	t.Run("downloads a file and preserves its permissions", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		putRemoteFile(t, dockerClient, containerID, "/home/sshtest/download_source.txt", "hello download", 0o640)

		result := dagu.RunWithEnv(env, "start", "download_file.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("downloaded_single.txt", "hello download")

		info, err := os.Stat(dagu.ProjectPath("downloaded_single.txt"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	})

	t.Run("uploading replaces an existing destination file atomically", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("overwrite_source.txt", "NEW-CONTENT")
		putRemoteFile(t, dockerClient, containerID, "/home/sshtest/overwrite_target.txt", "OLD-CONTENT", 0o644)

		result := dagu.RunWithEnv(env, "start", "upload_overwrite.yaml")
		result.ExpectExitCode(0)
		// The readback step prints the destination's final content
		// (unterminated, so it runs straight into the "---" separator),
		// then a count of any leftover ".dagu-tmp-*" file under the same
		// remote directory: 0 proves the upload's temp file was renamed
		// away, not merely that a second file with new content happens to
		// also exist.
		require.Equal(t, "NEW-CONTENT---\n0\n", lastStepStdout(t, result.Stdout()))
	})

	t.Run("uploads a directory recursively", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("upload_tree/root.txt", "root-file")
		dagu.WriteFile("upload_tree/sub/nested.txt", "nested-file")

		result := dagu.RunWithEnv(env, "start", "upload_dir.yaml")
		result.ExpectExitCode(0)
		// Neither fixture file has a trailing newline, so the first cat's
		// output runs straight into the "---" separator.
		require.Equal(t, "root-file---\nnested-file", lastStepStdout(t, result.Stdout()))
	})

	t.Run("downloads a directory recursively", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		putRemoteFile(t, dockerClient, containerID, "/home/sshtest/download_tree/root.txt", "root-remote", 0o644)
		putRemoteFile(t, dockerClient, containerID, "/home/sshtest/download_tree/sub/nested.txt", "nested-remote", 0o644)

		result := dagu.RunWithEnv(env, "start", "download_dir.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("downloaded_tree/root.txt", "root-remote")
		dagu.ExpectFileContent("downloaded_tree/sub/nested.txt", "nested-remote")
	})

	t.Run("upload fails when the local source does not exist", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "upload_missing_source.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("failed to stat source")
	})

	t.Run("download fails when the remote source does not exist", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "download_missing_source.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("failed to stat remote")
	})

	t.Run("with.direction matching the action name has no effect", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("upload_source.txt", "hello upload")

		result := dagu.RunWithEnv(env, "start", "direction_explicit_match.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "hello upload", lastStepStdout(t, result.Stdout()))
	})
}

// TestSFTPDirectionMismatchFailsValidate proves with.direction is rejected
// at DAG-build time when it names the other direction from the one the
// action itself already implies.
func TestSFTPDirectionMismatchFailsValidate(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "direction_mismatch.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("direction must be")
}

// TestSFTPNoServer proves the failure modes that do not need a real SFTP
// server: with.source and with.destination are validated only at runtime,
// not at DAG-build time (unlike ssh.run's with.command), and a connection
// that fails outright (nothing listening) is a runtime error shared with
// ssh.run's client.
func TestSFTPNoServer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "missing with.source fails only when the step runs",
			file:        "missing_source_no_server.yaml",
			stderrParts: []string{"source path is required"},
		},
		{
			name:        "missing with.destination fails only when the step runs",
			file:        "missing_destination_no_server.yaml",
			stderrParts: []string{"destination path is required"},
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
			validate := dagu.Run("validate", tc.file)
			validate.ExpectExitCode(0)

			result := dagu.Run("start", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}
