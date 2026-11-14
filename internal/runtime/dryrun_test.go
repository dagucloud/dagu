// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestCheckDryRunStep(t *testing.T) {
	missing := "dagu-test-missing-9f3c2b1a"

	t.Run("MissingShell", func(t *testing.T) {
		step := newStep("1", withCommand("true"), withShell(missing+"-shell"))

		err := checkDryRun(t, t.TempDir(), step, nil)
		require.ErrorContains(t, err, missing+"-shell")
	})

	t.Run("MissingCommandNamesFieldPath", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("lookup fixtures rely on POSIX paths")
		}
		// The test binary is an executable that exists on every platform.
		self, err := os.Executable()
		require.NoError(t, err)
		step := newStep("1", withShell("direct"))
		step.Commands = []ir.CommandEntry{{Command: self}, parseCommand(missing + "-second")}

		err = checkDryRun(t, t.TempDir(), step, nil)
		require.ErrorContains(t, err, "run[1]")
		require.ErrorContains(t, err, missing+"-second")
	})

	t.Run("NonExecutableCommandPath", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("executable permission bits are not meaningful on Windows")
		}
		workDir := t.TempDir()
		script := filepath.Join(workDir, "no-exec.sh")
		require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o644))

		err := checkDryRun(t, workDir, newStep("1", withCommand("./no-exec.sh")), nil)
		require.ErrorContains(t, err, "not an executable file")
	})

	t.Run("ShellBuiltin", func(t *testing.T) {
		if windowsShellTest() {
			t.Skip("Windows default shells are not checked for command names")
		}

		err := checkDryRun(t, t.TempDir(), newStep("1", withCommand("exit 0")), nil)
		require.NoError(t, err)
	})

	t.Run("ZshBuiltin", func(t *testing.T) {
		if _, err := exec.LookPath("zsh"); err != nil {
			t.Skip("zsh is not installed")
		}
		step := newStep("1", withCommand("setopt extendedglob"), withShell("zsh"))

		err := checkDryRun(t, t.TempDir(), step, nil)
		require.NoError(t, err)
	})

	// A command exec'd without a shell resolves through the dagu process PATH,
	// so a directory that only the step PATH lists does not satisfy it.
	t.Run("DirectCommandIgnoresStepPATH", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("lookup fixtures rely on POSIX executables")
		}
		binDir := t.TempDir()
		writeExecutable(t, filepath.Join(binDir, "dagu-test-step-path-tool"))
		step := newStep("1", withCommand("dagu-test-step-path-tool"), withShell("direct"))

		err := checkDryRun(t, t.TempDir(), step, map[string]string{"PATH": binDir})
		require.ErrorContains(t, err, "dagu-test-step-path-tool")
	})

	// A shell resolves command names through the step PATH, with relative
	// entries anchored to the step working directory.
	t.Run("ShellCommandUsesStepPATH", func(t *testing.T) {
		if windowsShellTest() {
			t.Skip("Windows default shells are not checked for command names")
		}
		workDir := t.TempDir()
		writeExecutable(t, filepath.Join(workDir, "bin", "dagu-test-relpath-tool"))
		step := newStep("1", withCommand("dagu-test-relpath-tool"))

		err := checkDryRun(t, workDir, step, map[string]string{"PATH": "./bin"})
		require.NoError(t, err)
	})

	t.Run("NonLocalExecutor", func(t *testing.T) {
		step := newStep("1", withCommand(missing+"-command"), withShell(missing+"-shell"))
		step.ExecutorConfig.Type = "docker"

		// Docker executors resolve the shell inside the image, not on the host.
		err := checkDryRun(t, t.TempDir(), step, nil)
		require.NoError(t, err)
	})
}

// checkDryRun runs the dry-run executable check for step with workDir as the
// DAG working directory and envs layered over the step environment.
func checkDryRun(t *testing.T, workDir string, step ir.Step, envs map[string]string) error {
	t.Helper()

	dag := &ir.DAG{Name: "dry-run-check", WorkingDir: workDir}
	ctx := runctx.NewContext(context.Background(), dag, "", "")
	env := runtime.NewEnv(ctx, step)
	if len(envs) > 0 {
		env.Scope = env.Scope.WithEntries(envs, cmnvalue.EnvSourceStepEnv)
	}
	return runtime.CheckDryRunStep(runtime.WithEnv(ctx, env), step)
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

func TestCheckDryRunStepRunsTheExecutorHook(t *testing.T) {
	const executorType = "dry-hook-test-9f3c2b1a"
	var seen map[string]any
	registry.RegisterExecutorCapabilities(executorType, registry.ExecutorCapabilities{
		DryRunCheck: func(_ context.Context, step ir.Step) error {
			seen = step.ExecutorConfig.Config
			if path, _ := step.ExecutorConfig.Config["path"].(string); path == "missing.xlsx" {
				return errors.New("field 'with.path': missing.xlsx: workbook not found")
			}
			return nil
		},
	})
	t.Cleanup(func() { registry.UnregisterExecutorCapabilities(executorType) })

	step := newStep("hook")
	step.ExecutorConfig = ir.ExecutorConfig{Type: executorType, Config: map[string]any{
		"path":  "missing.xlsx",
		"later": "${steps.find.outputs.path}",
	}}
	err := checkDryRun(t, t.TempDir(), step, nil)
	require.ErrorContains(t, err, "field 'with.path': missing.xlsx: workbook not found")
	require.Equal(t, "${steps.find.outputs.path}", seen["later"], "a step output reference is left as written for the hook to skip")

	step.ExecutorConfig.Config["path"] = "present.xlsx"
	require.NoError(t, checkDryRun(t, t.TempDir(), step, nil))
	require.NoError(t, checkDryRun(t, t.TempDir(), newStep("plain", withCommand("true")), nil),
		"an executor without a hook is checked as before")
}

func TestDryRunChecksForSplitsProblems(t *testing.T) {
	err := errors.Join(
		errors.New(`field 'with.key': column "Nope" not found`),
		errors.New(`field 'with.set': column "Also" not found`),
	)
	require.Equal(t, []runtime.DryRunCheck{
		{Step: "mark", Field: "with.key", Message: `column "Nope" not found`},
		{Step: "mark", Field: "with.set", Message: `column "Also" not found`},
	}, runtime.DryRunChecksFor("mark", err))

	plain := errors.New("shell missing")
	require.Equal(t, []runtime.DryRunCheck{{Step: "check", Message: "shell missing"}}, runtime.DryRunChecksFor("check", plain))
}

func TestSortDryRunChecksFollowsSteps(t *testing.T) {
	checks := []runtime.DryRunCheck{
		{Step: "load", Field: "with.path", Message: "missing"},
		{Step: "read", Field: "with.sheet", Message: "nope"},
		{Step: "load", Field: "with.source", Message: "also"},
	}
	runtime.SortDryRunChecks([]string{"read", "load"}, checks)
	require.Equal(t, []runtime.DryRunCheck{
		{Step: "read", Field: "with.sheet", Message: "nope"},
		{Step: "load", Field: "with.path", Message: "missing"},
		{Step: "load", Field: "with.source", Message: "also"},
	}, checks)
}
