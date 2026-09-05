// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec047_sqlite_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stderrLogPattern matches the per-step captured-stderr log path the same
// way, e.g. "├─stderr: /path/to/step.<ts>.<run>.err".
var stderrLogPattern = regexp.MustCompile(`stderr: (.+)`)

// stepStdout reads the exact bytes the LAST step in the run wrote to
// stdout, by locating that step's captured-output log file from dagu
// start's own tree render (the last "stdout:" line in a multi-step run)
// and reading it directly. The tree render re-wraps long lines with its
// own indentation, which would corrupt a strict JSONL/CSV parse, so
// assertions on precise query output read this file instead of
// result.Stdout().
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()
	return lastLoggedOutput(t, stdoutLogPattern, daguStartOutput)
}

// stepStderr is stepStdout's counterpart for the last step's captured
// stderr, where sqlite.query/sqlite.import write per-statement/import JSON
// execution metrics -- also re-wrapped by the tree render, so this reads
// the raw log file the same way. Every step (not only SELECT-shaped ones)
// writes its own metrics line, so a two-step DAG has one "stderr:" line
// per step; this always reads the last one.
func stepStderr(t *testing.T, daguStartOutput string) string {
	t.Helper()
	return lastLoggedOutput(t, stderrLogPattern, daguStartOutput)
}

func lastLoggedOutput(t *testing.T, pattern *regexp.Regexp, daguStartOutput string) string {
	t.Helper()

	matches := pattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a %q log path in output:\n%s", pattern, daguStartOutput)
	path := strings.TrimSpace(matches[len(matches)-1][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// lastJSONLine returns the last non-empty line of s, since execution/import
// metrics are written as one JSON object per line.
func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
