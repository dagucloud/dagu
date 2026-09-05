// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec060_node_script_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches a per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
// One match appears per step that wrote anything to stdout, in the order
// dagu's tree render lists them (which, for both the sequential and the
// independent-but-declared-in-order fixtures this package uses, matches
// declaration order).
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stepStdout reads the exact bytes the (0-indexed) nth step with logged
// stdout wrote, by locating its captured-output log file from dagu start's
// own tree render and reading it directly, since the tree render re-wraps
// long lines with its own indentation, which would corrupt a strict content
// or JSON-parse match.
func stepStdout(t *testing.T, daguStartOutput string, n int) string {
	t.Helper()

	matches := stdoutLogPattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.Greaterf(t, len(matches), n, "expected at least %d stdout log paths in output:\n%s", n+1, daguStartOutput)
	path := strings.TrimSpace(matches[n][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// lastStepStdout is stepStdout for the last step with logged stdout.
func lastStepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	matches := stdoutLogPattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a stdout log path in output:\n%s", daguStartOutput)
	return stepStdout(t, daguStartOutput, len(matches)-1)
}
