// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec083_continuous_runs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// findFiles returns the files under root that have the given extension or name.
func findFiles(t *testing.T, root string, match func(name string) bool) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && match(d.Name()) {
			found = append(found, path)
		}
		return nil
	})
	require.NoError(t, err)
	return found
}

func runBeat(t *testing.T, file string) (home string, dagu *harness.Runner) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixtures use a POSIX shell")
	}
	home = filepath.Join(t.TempDir(), "dagu")
	dagu = harness.NewRunner(t)
	dagu.RunWithEnv([]string{"DAGU_HOME=" + home}, "start", file).ExpectExitCode(0)
	dagu.ExpectFileContent("ticks.txt", strings.Repeat("tick\n", 30))
	return home, dagu
}

// A step that repeats without a limit is marked continuous in its status,
// and its log keeps the output of the last execution only, because each
// execution succeeded.
func TestContinuousStepStatusAndLog(t *testing.T) {
	t.Parallel()
	home, _ := runBeat(t, "continuous_until_done.yaml")

	statuses := findFiles(t, home, func(n string) bool { return n == "status.jsonl" })
	require.Len(t, statuses, 1)
	status, err := os.ReadFile(statuses[0])
	require.NoError(t, err)
	require.Contains(t, string(status), `"continuous":true`)
	require.Contains(t, string(status), `"doneCount":30`)

	logs := findFiles(t, home, func(n string) bool { return strings.HasSuffix(n, ".out") && strings.HasPrefix(n, "beat.") })
	require.Len(t, logs, 1)
	out, err := os.ReadFile(logs[0])
	require.NoError(t, err)
	require.Equal(t, "beat\n", string(out), "the output of the executions that succeeded does not stay")
}

// A step with a limit keeps its status and its whole log.
func TestRepeatWithLimitKeepsLog(t *testing.T) {
	t.Parallel()
	home, _ := runBeat(t, "limited_until_done.yaml")

	statuses := findFiles(t, home, func(n string) bool { return n == "status.jsonl" })
	require.Len(t, statuses, 1)
	status, err := os.ReadFile(statuses[0])
	require.NoError(t, err)
	require.NotContains(t, string(status), `"continuous"`)

	logs := findFiles(t, home, func(n string) bool { return strings.HasSuffix(n, ".out") && strings.HasPrefix(n, "beat.") })
	require.Len(t, logs, 1)
	out, err := os.ReadFile(logs[0])
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("beat\n", 30), string(out))
}
