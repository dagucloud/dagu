// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestExecRunsInlineCommand proves `dagu exec -- <command>` runs the command
// as a one-off DAG run without a workflow file.
func TestExecRunsInlineCommand(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "exec", "--", "sh", "-c", "printf hi > exec.out")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("exec.out", "hi")
}

func TestExecPropagatesCommandFailure(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "exec", "--", "sh", "-c", "exit 7")
	result.ExpectNonZeroExitCode()
}

func TestExecEnvFlagSetsVariable(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "exec", "--env", "MY_VAR=exec-value", "--", "sh", "-c", "printf '%s' \"$MY_VAR\" > exec_env.out")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("exec_env.out", "exec-value")
}

func TestExecWorkdirFlagSetsWorkingDirectory(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)
	dagu.Mkdir("exec-workdir")

	result := dagu.RunWithEnv(env, "exec", "--workdir", "exec-workdir", "--", "sh", "-c", "pwd > location.out")
	result.ExpectExitCode(0)

	// On Windows, MSYS `pwd` prints a hybrid path -- a literal backslash
	// after the drive letter with forward slashes elsewhere -- rather than
	// a pure POSIX path, so normalize both sides' separators before
	// comparing instead of relying on an exact string match.
	actual, err := os.ReadFile(dagu.ProjectPath(filepath.Join("exec-workdir", "location.out"))) // #nosec G304 -- path is confined to the test's isolated project dir.
	require.NoError(t, err)
	want := filepath.ToSlash(dagu.ProjectPath("exec-workdir")) + "\n"
	require.Equal(t, want, strings.ReplaceAll(string(actual), `\`, "/"))
}

func TestExecRequiresCommand(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "exec")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("command is required")
}
