// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec014_step_run_command_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestCommandFormRuntimeUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	cases := []struct {
		name    string
		file    string
		output  string
		content string
	}{
		{
			name:    "single command captures stdout through configured stdout file",
			file:    "single_command_success.yaml",
			output:  "single-command.out",
			content: "hello\n",
		},
		{
			name:    "shell operators stay inside one shell invocation",
			file:    "shell_operators.yaml",
			output:  "operators.txt",
			content: "beta\n",
		},
		{
			name:    "explicitly waited background work contributes output",
			file:    "background_wait.yaml",
			output:  "background-wait.txt",
			content: "done\n",
		},
		{
			name:    "array streams aggregate in entry order",
			file:    "array_streams.yaml",
			output:  "array-stdout.txt",
			content: "out1\nout2\n",
		},
		{
			name:    "array entries use fresh shell state",
			file:    "array_fresh_state.yaml",
			output:  "array-fresh.txt",
			content: "fresh\n",
		},
		{
			name:    "array success publishes shared output file values",
			file:    "array_outputs_success.yaml",
			output:  "array-outputs.txt",
			content: "one:two\n",
		},
		{
			name:    "step working directory is selected shell working directory",
			file:    "working_directory.yaml",
			output:  "workspace/cwd.txt",
			content: "workspace\n",
		},
		{
			name:    "explicit relative path runs from working directory",
			file:    "relative_path_explicit.yaml",
			output:  "relative-path.txt",
			content: "relative-ok\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

func TestCommandFormFailuresUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use POSIX shell snippets")
	}

	t.Run("default Unix shell fails fast", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "default_errexit.yaml")
		result.ExpectExitCode(1)
		dagu.ExpectFileContent("default-errexit.txt", "before\n")
	})

	t.Run("array stops on first failed entry", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "array_stop_on_failure.yaml")
		result.ExpectExitCode(1)
		dagu.ExpectFileContent("array-stop.txt", "first\n")
		dagu.ExpectNoFile("array-third.txt")
	})

	t.Run("array failure publishes no output file values", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "array_outputs_failure.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrNotContains("value")
		dagu.ExpectFileNotContains("array-output-failure.txt", "value")
	})

	t.Run("resolved command text with line break fails before shell starts", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "resolved_line_break.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("line break")
		result.ExpectStderrNotContains("line-break-ran")
		dagu.ExpectNoFile("line-break-ran.txt")
	})

	t.Run("unsupported shell packages fail before shell starts", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "unsupported_shell_packages.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("shell_packages")
		dagu.ExpectNoFile("unsupported-packages.txt")
	})

	t.Run("invalid Unix command carrier fails before shell starts", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "invalid_unix_carrier.yaml")
		result.ExpectExitCode(1)
		result.ExpectStderrContains("-cecho")
		dagu.ExpectNoFile("invalid-carrier.txt")
	})

	t.Run("bare local command lookup remains shell owned", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "bare_lookup_not_added.yaml")
		result.ExpectExitCode(1)
		dagu.ExpectNoFile("bare-lookup-ran.txt")
	})
}

func TestCommandDiagnosticsDoNotDumpResolvedSecret(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell snippets")
	}

	const runID = "spec014-secret-diagnostics"
	const file = "secret_diagnostics.yaml"
	const secret = "SPEC014_SECRET_SENTINEL"

	dagu := harness.NewRunner(t)
	sharedEnv := []string{
		"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"),
		"SPEC014_SECRET_VALUE=" + secret,
	}

	result := dagu.RunWithEnv(sharedEnv, "start", "--run-id", runID, file)
	result.ExpectExitCode(1)
	result.ExpectStderrContains("-cecho")
	result.ExpectStdoutNotContains(secret)
	result.ExpectStderrNotContains(secret)

	status := dagu.RunWithEnv(sharedEnv, "status", "--run-id", runID, file)
	status.ExpectExitCode(0)
	status.ExpectStdoutNotContains(secret)
	status.ExpectStderrNotContains(secret)

	history := dagu.RunWithEnv(sharedEnv, "history", "--run-id", runID, "--format", "json")
	history.ExpectExitCode(0)
	history.ExpectStdoutNotContains(secret)
	history.ExpectStderrNotContains(secret)
}

func TestCommandFormOtherShellFallbackUnix(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell snippets")
	}

	dagu := harness.NewRunner(t)
	dagu.WriteExecutable("other-shell-shim", `#!/bin/sh
printf '%s\n' "$@" > other-shell-args.txt
if [ "$1" = "-c" ]; then
  shift
  exec sh -c "$1"
fi
exit 64
`)

	result := dagu.Run("start", "other_shell_fallback.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("other-shell-args.txt", "-c\nprintf 'other-ok\\n' > other-shell.txt\n")
	dagu.ExpectFileContent("other-shell.txt", "other-ok\n")
}

func TestCommandFormPowerShellWhenAvailable(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh is not available")
	}

	t.Run("UTF-8 output is stable", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "powershell_utf8.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("pwsh-utf8.txt", "東京")
	})

	t.Run("Write-Error fails command-form step", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "powershell_error_fails.yaml")
		result.ExpectExitCode(1)
		dagu.ExpectFileContent("pwsh-error.txt", "before")
	})
}

func TestCommandFormCmdWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("cmd shell is Windows-specific")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "cmd_comspec.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("cmd-comspec.txt", "cmd-ok\r\n")
}
