// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec058_jq_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
// The tree render omits this line entirely when the step wrote only a
// single trailing newline (or nothing) to stdout.
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stderrLogPattern matches the per-step captured-stderr log path the same
// way stdoutLogPattern matches stdout.
var stderrLogPattern = regexp.MustCompile(`stderr: (.+)`)

// lastStepStdout reads the exact bytes the last step in the run wrote to
// stdout, by locating that step's captured-output log file from dagu
// start's own tree render (the last "stdout:" line in a multi-step run)
// and reading it directly, since the tree render re-wraps long lines with
// its own indentation, which would corrupt a strict content match.
func lastStepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	return lastLoggedFile(t, daguStartOutput, stdoutLogPattern)
}

// lastStepStderr is lastStepStdout's counterpart for a step's captured
// stderr log file.
func lastStepStderr(t *testing.T, daguStartOutput string) string {
	t.Helper()

	return lastLoggedFile(t, daguStartOutput, stderrLogPattern)
}

func lastLoggedFile(t *testing.T, daguStartOutput string, pattern *regexp.Regexp) string {
	t.Helper()

	matches := pattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a logged file path matching %s in output:\n%s", pattern, daguStartOutput)
	path := strings.TrimSpace(matches[len(matches)-1][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// hasNoStepStdout reports whether the run's tree render recorded no
// "stdout:" line at all -- the tree omits that line when a step wrote
// only a single trailing newline (or nothing) to stdout.
func hasNoStepStdout(daguStartOutput string) bool {
	return !stdoutLogPattern.MatchString(daguStartOutput)
}
