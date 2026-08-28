// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec038_dry_run_step_checks_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestDryRunAcceptsResolvableShellAndCommand(t *testing.T) {
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
			// Dry-run must still perform no real actions: the step's own output
			// file must not be created even though the check now succeeds.
			dagu.ExpectNoFile(tc.output)
		})
	}
}

func TestDryRunRejectsUnresolvableCommand(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("dry", "missing_command.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("run")
	dagu.ExpectNoFile("missing-command.out")
}

func TestDryRunRejectsUnresolvableShell(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("dry", "missing_shell.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("shell")
	dagu.ExpectNoFile("missing-shell.out")
}

func TestDryRunChecksScriptExecutePermission(t *testing.T) {
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
		dagu.ExpectNoFile("script-ran.out")
	})

	t.Run("non-executable script is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("scripts/step.sh", script)

		result := dagu.Run("dry", "script_permission.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("run")
		dagu.ExpectNoFile("script-ran.out")
	})
}
