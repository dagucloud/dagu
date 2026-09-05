// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestConfigShowsResolvedPaths proves `dagu config` resolves DAGU_HOME and
// DAGU_DAGS_DIR into the actual paths in effect, not just that it prints
// labeled lines: it asserts the DAGs-directory line names the project
// directory itself, and the data-directory line names DAGU_HOME's own
// "data" subdirectory, so a build that resolved either to the wrong path
// (or to another DAG-run's home) would fail this test.
func TestConfigShowsResolvedPaths(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	env := []string{
		"DAGU_HOME=" + home,
		"DAGU_DAGS_DIR=.",
	}

	result := dagu.RunWithEnv(env, "config")
	result.ExpectExitCode(0)

	requireLineContains(t, result.Stdout(), "DAGs directory:", dagu.ProjectPath("."))
	requireLineContains(t, result.Stdout(), "Data directory:", filepath.Join(home, "data"))
}

// requireLineContains fails the test unless some line starts with label and
// its remainder, trimmed, equals value exactly. Matching the resolved path
// by exact equality (rather than substring) rejects a path that merely
// shares value as a prefix, such as a sibling directory named value+"-old".
func requireLineContains(t *testing.T, output, label, value string) {
	t.Helper()

	for line := range strings.SplitSeq(output, "\n") {
		rest, ok := strings.CutPrefix(line, label)
		if ok && strings.TrimSpace(rest) == value {
			return
		}
	}
	t.Fatalf("no line labeled %q has value %q; output:\n%s", label, value, output)
}
