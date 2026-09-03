// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec041_log_write holds black-box conformance tests for
// Spec 041: Log Write Action.
package spec041_log_write_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
// Captures through the line ending rather than \S+, so a project path
// containing spaces isn't truncated.
var stdoutLogPattern = regexp.MustCompile(`stdout: ([^\r\n]+)`)

// stepStdout reads the exact bytes a step wrote to stdout, by locating its
// captured-output log file from dagu start's own tree render and reading it
// directly. Routing the message through a step output and a second step's
// run: command instead would not prove this: capturing an output strips a
// trailing newline, and re-interpolating a captured value containing a
// newline into another step's command text escapes it to a literal \n --
// both would mask whether log.write itself appended an extra newline.
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	match := stdoutLogPattern.FindStringSubmatch(daguStartOutput)
	require.Lenf(t, match, 2, "expected a stdout log path in output:\n%s", daguStartOutput)
	data, err := os.ReadFile(match[1]) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// TestLogWriteRuntime proves log.write's message and trailing-newline
// contract: a message resolved with the standard value substitution, with
// exactly one newline appended when it lacks one, and no extra newline when
// it already ends with one.
func TestLogWriteRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		file    string
		content string
	}{
		{
			name:    "resolved message gets exactly one trailing newline",
			file:    "basic.yaml",
			content: "hello, world\n",
		},
		{
			name:    "message already ending in a newline is not given a second one",
			file:    "trailing_newline_preserved.yaml",
			content: "line one\nline two\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			require.Equal(t, tc.content, stepStdout(t, result.Stdout()))
		})
	}
}

// TestLogWriteValidation proves the errors DAG-build-time validation
// rejects before the DAG ever runs.
func TestLogWriteValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "missing with.message",
			file:        "missing_message.yaml",
			stderrParts: []string{"with.message is required"},
		},
		{
			name:        "with.message is not a string",
			file:        "non_string_message.yaml",
			stderrParts: []string{"with.message must be a non-empty string"},
		},
		{
			name:        "with.message is an empty string",
			file:        "empty_message.yaml",
			stderrParts: []string{"with.message must be a non-empty string"},
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
