// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec064_dry_run_step_checks_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

const dryRunWarning = "Dry run: step may fail on this host"

func TestDryAcceptsCommandAndShell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		output string
	}{
		{name: "command resolvable on PATH", file: "valid_command.yaml", output: "valid-command.out"},
		{name: "step-level shell resolvable on PATH", file: "valid_shell.yaml", output: "valid-shell.out"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("dry", tc.file)
			result.ExpectExitCode(0)
			// A resolvable command or shell produces no warning.
			result.ExpectStderrNotContains(dryRunWarning)
			// Dry validation must not create step output files.
			dagu.ExpectNoFile(tc.output)
		})
	}
}

// A missing executable is a warning, not a failure: the real run may execute
// on another host, or an upstream step may install the executable first.
func TestDryWarnsMissingExecutable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		file    string
		missing string
		output  string
	}{
		{
			name:    "command",
			file:    "missing_command.yaml",
			missing: "dagu-conformance-missing-command-9f3c2b1a",
			output:  "missing-command.out",
		},
		{
			name:    "shell",
			file:    "missing_shell.yaml",
			missing: "dagu-conformance-missing-shell-9f3c2b1a",
			output:  "missing-shell.out",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("dry", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStderrContains(dryRunWarning, tc.missing)
			dagu.ExpectNoFile(tc.output)
		})
	}
}

func TestDryChecksExecutePermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute permission bits are not meaningful on Windows")
	}
	t.Parallel()

	const script = "#!/bin/sh\nprintf 'ran\\n' > script-ran.out\n"

	t.Run("executable script is accepted", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteExecutable("scripts/step.sh", script)

		result := dagu.Run("dry", "script_permission.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrNotContains(dryRunWarning)
		dagu.ExpectNoFile("script-ran.out")
	})

	t.Run("non-executable script warns", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("scripts/step.sh", script)

		result := dagu.Run("dry", "script_permission.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains(dryRunWarning, "step.sh")
		dagu.ExpectNoFile("script-ran.out")
	})
}
