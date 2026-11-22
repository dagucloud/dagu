// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The DAG writes state, a redirect file, an artifact, and logs to a DAG-level
// log directory, and the run emits events. None of it reaches the configured
// home.
func TestCaseLeavesHomeUnchanged(t *testing.T) {
	t.Parallel()

	ctx, home := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "isolated", fmt.Sprintf(`
log_dir: %q
artifacts:
  enabled: true
steps:
  - name: save
    action: state.set
    with:
      key: cursor
      value: 1
  - name: report
    run: echo report
    stdout: report.txt
  - name: archive
    run: echo archive
    stdout:
      artifact: archive.txt
`, filepath.Join(home, "dag-logs")))
	before := dirSnapshot(t, home)

	tc, err := openCase(ctx, caseOptions{tempDir: t.TempDir()})
	require.NoError(t, err)
	status, err := tc.run(dag)
	require.NoError(t, err)
	require.NoError(t, tc.close())

	assert.Equal(t, ir.Succeeded, status.Status, status.Error)
	assert.Equal(t, before, dirSnapshot(t, home))
}

func TestCaseClose(t *testing.T) {
	t.Parallel()

	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep=%t", keep), func(t *testing.T) {
			t.Parallel()

			ctx, _ := newCaseTestContext(t)
			dag := writeCaseDAG(t, ctx, "close", "steps:\n  - run: echo ok\n")
			tempDir := t.TempDir()

			tc, err := openCase(ctx, caseOptions{tempDir: tempDir, keep: keep})
			require.NoError(t, err)
			_, err = tc.run(dag)
			require.NoError(t, err)
			require.NoError(t, tc.close())

			entries, err := os.ReadDir(tempDir)
			require.NoError(t, err)
			if keep {
				require.Len(t, entries, 1)
				assert.Equal(t, tc.dir, filepath.Join(tempDir, entries[0].Name()))
			} else {
				assert.Empty(t, entries)
			}
		})
	}
}

// A step outside real writes its redirect under the case directory, and a
// step in real writes it at its configured path.
func TestCaseRedirects(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	workDir := t.TempDir()
	dag := writeCaseDAG(t, ctx, "redirects", fmt.Sprintf(`
working_dir: %q
steps:
  - name: moved
    run: echo moved
    stdout: out/result.txt
  - name: kept
    run: echo kept
    stdout: kept.txt
`, workDir))

	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), real: []string{"kept"}})
	status, err := tc.run(dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	entries, err := os.ReadDir(workDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "kept.txt", entries[0].Name())

	moved := findFile(t, tc.dir, "result.txt")
	require.NotEmpty(t, moved, "redirect file is not in the case directory")
	data, err := os.ReadFile(moved)
	require.NoError(t, err)
	assert.Equal(t, "moved\n", string(data))
}

// The env provider would fail on the unset variables, so a passing run
// resolved its secrets from the test values.
func TestCaseSecrets(t *testing.T) {
	t.Parallel()

	const yaml = `
secrets:
  - name: API_TOKEN
    provider: env
    key: DAGU_CASE_TEST_UNSET_TOKEN
  - name: DB_PASSWORD
    provider: env
    key: DAGU_CASE_TEST_UNSET_PASSWORD
steps:
  - name: use
    run: echo token=$API_TOKEN
`

	t.Run("Missing", func(t *testing.T) {
		t.Parallel()

		ctx, _ := newCaseTestContext(t)
		dag := writeCaseDAG(t, ctx, "secrets", yaml)
		tc := openTestCase(t, ctx, caseOptions{
			tempDir: t.TempDir(),
			secrets: map[string]string{"API_TOKEN": "case-token"},
		})

		_, err := tc.run(dag)
		require.ErrorContains(t, err, "DB_PASSWORD")
		runs, err := tc.ctx.Persistence.DAGRunRepository.RecentStatuses(tc.ctx, dag.Name, 1)
		require.NoError(t, err)
		assert.Empty(t, runs, "the case started a run")
	})

	t.Run("Masked", func(t *testing.T) {
		t.Parallel()

		ctx, _ := newCaseTestContext(t)
		dag := writeCaseDAG(t, ctx, "secrets", yaml)
		tc := openTestCase(t, ctx, caseOptions{
			tempDir: t.TempDir(),
			secrets: map[string]string{"API_TOKEN": "case-token", "DB_PASSWORD": "case-password"},
		})

		status, err := tc.run(dag)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status, status.Error)
		stdout, err := os.ReadFile(status.Nodes[0].Stdout)
		require.NoError(t, err)
		assert.Equal(t, "token=*******\n", string(stdout))
	})
}

func TestCaseTrigger(t *testing.T) {
	t.Parallel()

	const yaml = "steps:\n  - name: trigger\n    run: echo ${context.trigger.type}\n"

	for _, tt := range []struct {
		name    string
		trigger ir.TriggerType
		want    string
	}{
		{name: "Default", want: "manual"},
		{name: "Restart", trigger: ir.TriggerTypeRestart, want: "restart"},
		{name: "Retry", trigger: ir.TriggerTypeRetry, want: "retry"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, _ := newCaseTestContext(t)
			dag := writeCaseDAG(t, ctx, "trigger", yaml)
			tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), trigger: tt.trigger})

			status, err := tc.run(dag)
			require.NoError(t, err)
			require.Equal(t, ir.Succeeded, status.Status, status.Error)
			stdout, err := os.ReadFile(status.Nodes[0].Stdout)
			require.NoError(t, err)
			assert.Equal(t, tt.want+"\n", string(stdout))
		})
	}

	t.Run("SubDAG", func(t *testing.T) {
		t.Parallel()

		ctx, _ := newCaseTestContext(t)
		dag := writeCaseDAG(t, ctx, "trigger", yaml)
		tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), trigger: ir.TriggerTypeSubDAG})

		_, err := tc.run(dag)
		require.ErrorContains(t, err, "subdag")
	})
}

// A service DAG has one run at a time. The live run is recorded in the
// configured data directory, which the case's checks do not read, and the
// case's run is recorded only in the case directory.
func TestCaseServiceBesideLiveRun(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "service", `
restart_policy: always
restart_wait_sec: 1
steps:
  - run: echo ok
`)

	const liveRunID = "live"
	live := ir.NewDAGRunRef(dag.Name, liveRunID)
	attempt, err := ctx.Persistence.DAGRunRepository.CreateAttempt(ctx, dag, time.Now(), liveRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(liveRunID, ir.Running, os.Getpid(), time.Now(),
		ir.WithAttemptID(attempt.ID()),
		ir.WithWorkerID("local"),
	)))
	require.NoError(t, attempt.Close(ctx))
	handle, err := ctx.Persistence.ProcRepository.Acquire(ctx, dag.ProcGroup(), proc.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         dag.Name,
		DAGRunID:     liveRunID,
		AttemptID:    attempt.ID(),
		RootName:     dag.Name,
		RootDAGRunID: liveRunID,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })
	require.ErrorIs(t, ctx.DAGRunMgr.CheckServiceRun(ctx, dag, "manual"), runtime.ErrServiceRunActive)

	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir()})
	status, err := tc.run(dag)
	require.NoError(t, err)
	assert.Equal(t, ir.Succeeded, status.Status, status.Error)

	alive, err := ctx.Persistence.ProcRepository.ListAlive(ctx, dag.ProcGroup())
	require.NoError(t, err)
	assert.Equal(t, []ir.DAGRunRef{live}, alive)
	runs, err := ctx.Persistence.DAGRunRepository.RecentStatuses(ctx, dag.Name, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, liveRunID, runs[0].DAGRunID)
	assert.ErrorIs(t, ctx.DAGRunMgr.CheckServiceRun(ctx, dag, "manual"), runtime.ErrServiceRunActive)
}

// newCaseTestContext returns the context of dagu start with every configured
// path under the returned home directory.
func newCaseTestContext(t *testing.T) (*Context, string) {
	t.Helper()

	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("debug: false\n"), 0o600))

	command := &cobra.Command{Use: "start"}
	initFlags(command, startFlags...)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	return ctx, home
}

func writeCaseDAG(t *testing.T, ctx *Context, name, yaml string) *ir.DAG {
	t.Helper()

	path := filepath.Join(ctx.Config.Paths.DAGsDir, name+".yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	dag, err := spec.Load(ctx, path, spec.WithBaseConfig(ctx.Config.Paths.BaseConfig))
	require.NoError(t, err)
	return dag
}

func openTestCase(t *testing.T, ctx *Context, opts caseOptions) *testCase {
	t.Helper()

	tc, err := openCase(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tc.close()) })
	return tc
}

// dirSnapshot maps each path under root to the content of its file, or to
// "dir" for a directory.
func dirSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	snapshot := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[rel] = string(data)
		return nil
	}))
	return snapshot
}

// findFile returns the path of the file named name under root, or "".
func findFile(t *testing.T, root, name string) string {
	t.Helper()

	var found string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == name {
			found = path
		}
		return nil
	}))
	return found
}
