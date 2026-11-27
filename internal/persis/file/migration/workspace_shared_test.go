// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG name that DAGs of two projects share splits its runs by the
// workspace each run's labels name, as v2 showed them. Each run takes every
// attempt's logs and its queue item along, while a log kept directly in the
// name's directory moves on its own. The rest of the name's state stays in
// the default project, which the migration records for the report. The
// state v2 keeps by file name follows each file's project, and two files of
// one workspace with one name move that name's state once.
func TestRunSplitsSharedDAGName(t *testing.T) {
	t.Parallel()
	in := newWorkspaceInstall(t)
	in.writeFile(t, "dags/report.yaml", "labels:\n  - workspace: billing\nsteps:\n  - run: echo billing\n")
	in.writeFile(t, "dags/report-default.yaml", "name: report\nsteps:\n  - run: echo default\n")
	in.writeFile(t, "dags/dup-a.yaml", "name: dup\nlabels:\n  - workspace: billing\nsteps:\n  - run: echo a\n")
	in.writeFile(t, "dags/dup-b.yaml", "name: dup\nlabels:\n  - workspace: billing\nsteps:\n  - run: echo b\n")
	runs := filepath.Join(in.root, "data", "dag-runs")
	billingRun := in.writeRun(t, runs, "report", "run_b", []string{"workspace=billing"}, 2)
	defaultRun := in.writeRun(t, runs, "report", "run_d", nil, 1)
	// A run whose ID ends in another run's ID keeps its own queue item.
	in.writeRun(t, runs, "report", "x_run_b", nil, 1)
	in.writeRun(t, runs, "dup", "run_x", []string{"workspace=billing"}, 1)
	for _, rel := range []string{
		"data/suspend/report.suspend",
		"data/suspend/report-default.suspend",
		"data/suspend/dup-a.suspend",
		"data/suspend/dup-b.suspend",
		"data/queue/report/item_low_20260102_000000_000000000Z_run_b.json",
		"data/queue/report/item_low_20260102_000000_000000000Z_run_d.json",
		"data/queue/report/item_low_20260102_000000_000000000Z_x_run_b.json",
		"data/slot-index/7265706f7274/2026-01-02.jsonl",
	} {
		writeFile(t, filepath.Join(in.root, rel))
	}

	require.NoError(t, newStateMigrator(t, in.cfg).Run(context.Background()))

	state := filepath.Join(in.cfg.Paths.DataDir, "projects")
	runDir := func(project, run string) string {
		return filepath.Join(state, project, "runs", "report", "dag-runs", "2026", "01", "02", run)
	}
	assert.DirExists(t, runDir("billing", billingRun))
	assert.NoDirExists(t, runDir("default", billingRun))
	assert.DirExists(t, runDir("default", defaultRun))
	for _, attempt := range []string{"1", "2"} {
		assert.FileExists(t, filepath.Join(state, "billing", "logs", "report", billingRun+"-"+attempt, "run.log"))
	}
	assert.FileExists(t, filepath.Join(state, "billing", "logs", "report", "flat_run_b.log"))
	assert.FileExists(t, filepath.Join(state, "default", "logs", "report", defaultRun+"-1", "run.log"))
	assert.FileExists(t, filepath.Join(state, "default", "logs", "report", "flat_run_d.log"))
	assert.FileExists(t, filepath.Join(state, "billing", "state", "queue", "report", "item_low_20260102_000000_000000000Z_run_b.json"))
	assert.FileExists(t, filepath.Join(state, "default", "state", "queue", "report", "item_low_20260102_000000_000000000Z_run_d.json"))
	assert.FileExists(t, filepath.Join(state, "default", "state", "queue", "report", "item_low_20260102_000000_000000000Z_x_run_b.json"))
	assert.FileExists(t, filepath.Join(state, "default", "state", "slot-index", "7265706f7274", "2026-01-02.jsonl"))
	for _, flag := range []string{"report", "dup-a", "dup-b"} {
		assert.FileExists(t, filepath.Join(state, "billing", "state", "suspend", flag+".suspend"))
	}
	assert.FileExists(t, filepath.Join(state, "default", "state", "suspend", "report-default.suspend"))
	assert.DirExists(t, filepath.Join(state, "billing", "runs", "dup"))

	assert.Equal(t, []attentionItem{{Kind: attentionSharedName, DAG: "report", Projects: []string{"billing", "default"}}},
		readAttention(t, in))
}

// Files of one name in two projects, which recursive discovery finds, keep
// the state v2 keeps by that name in the default project, where v2's
// webhook URL of the name keeps reaching a DAG. The suspend flag is copied,
// so the workspace's DAG stays suspended too.
func TestRunKeepsSharedFileNameState(t *testing.T) {
	t.Parallel()
	in := newWorkspaceInstall(t)
	in.cfg.DAGDiscovery.Recursive = true
	in.writeFile(t, "dags/a/job.yaml", "labels:\n  - workspace: billing\nsteps:\n  - run: echo billing\n")
	in.writeFile(t, "dags/b/job.yaml", "steps:\n  - run: echo default\n")
	writeFile(t, filepath.Join(in.root, "data", "suspend", "job.suspend"))
	writeFile(t, filepath.Join(in.root, "data", "dag-settings", "job.json"))

	require.NoError(t, newStateMigrator(t, in.cfg).Run(context.Background()))

	state := filepath.Join(in.cfg.Paths.DataDir, "projects")
	assert.FileExists(t, filepath.Join(state, "default", "state", "suspend", "job.suspend"))
	assert.FileExists(t, filepath.Join(state, "billing", "state", "suspend", "job.suspend"))
	assert.FileExists(t, filepath.Join(state, "default", "state", "dag-settings", "job.json"))
	billing, err := projectregistry.New(in.cfg.Paths.DataDir).Get(context.Background(), "billing")
	require.NoError(t, err)
	assert.NotContains(t, billing.MigratedDAGs, "job")
	assert.Contains(t, readAttention(t, in),
		attentionItem{Kind: attentionSharedFileName, DAG: "job", Projects: []string{"billing", "default"}})
}

// A directory that a released version named for two DAGs, or that holds the
// artifacts kept by date, stays with the default project.
func TestRunLeavesDirectoriesOfOtherDAGs(t *testing.T) {
	t.Parallel()
	in := newWorkspaceInstall(t)
	in.writeFile(t, "dags/my.dag.yaml", "labels:\n  - workspace: billing\nsteps:\n  - run: echo a\n")
	in.writeFile(t, "dags/my_dag.yaml", "steps:\n  - run: echo b\n")
	in.writeFile(t, "dags/2026.yaml", "labels:\n  - workspace: billing\nsteps:\n  - run: echo c\n")
	logs := filepath.Join("logs", "my_dag", "dag-run_1", "run.log")
	dated := filepath.Join("data", "artifacts", "2026", "01", "02", "120000_other_0000000000000000", "a.md")
	writeFile(t, filepath.Join(in.root, logs))
	writeFile(t, filepath.Join(in.root, dated))

	require.NoError(t, newStateMigrator(t, in.cfg).Run(context.Background()))

	state := filepath.Join(in.cfg.Paths.DataDir, "projects", "default")
	assert.FileExists(t, filepath.Join(state, "logs", "my_dag", "dag-run_1", "run.log"))
	assert.FileExists(t, filepath.Join(state, "artifacts", "2026", "01", "02", "120000_other_0000000000000000", "a.md"))
}

// The logs of a workspace DAG that v2 kept on another file system through a
// link stay there: the project's directory of logs links beside the default
// project's. A link that a killed attempt left leading nowhere is completed
// or made again.
func TestRunKeepsWorkspaceLogsOnLinkedDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
	t.Parallel()
	for _, tc := range []struct {
		name string
		// left returns the target of the link that a killed attempt left as
		// the project's directory of logs, or "" for none, from the real
		// path of the disk.
		left func(disk string) string
	}{
		{name: "First", left: func(string) string { return "" }},
		{name: "KilledBeforeTarget", left: func(disk string) string { return filepath.Join(disk, ".projects", "billing") }},
		{name: "LeadingNowhere", left: func(disk string) string { return filepath.Join(disk, "gone") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := newWorkspaceInstall(t)
			logs := in.cfg.Paths.LogDir
			disk := filepath.Join(in.root, "disk", "logs")
			require.NoError(t, os.MkdirAll(filepath.Dir(disk), 0o750))
			require.NoError(t, os.Rename(logs, disk))
			writeFile(t, filepath.Join(disk, "admin", "audit", "a.jsonl"))
			require.NoError(t, os.Symlink(disk, logs))
			billing := filepath.Join(in.cfg.Paths.DataDir, "projects", "billing", "logs")
			real, err := filepath.EvalSymlinks(disk)
			require.NoError(t, err)
			if target := tc.left(real); target != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(billing), 0o750))
				require.NoError(t, os.Symlink(target, billing))
			}
			m := newStateMigrator(t, in.cfg)
			m.mover.sameFileSystem = otherFileSystem

			require.NoError(t, m.Run(context.Background()))

			assert.FileExists(t, filepath.Join(billing, "invoices", "dag-run_1", "run.log"))
			assert.FileExists(t, filepath.Join(disk, ".projects", "billing", "invoices", "dag-run_1", "run.log"),
				"the logs stay on their disk")
		})
	}
}

func readAttention(t *testing.T, in *workspaceInstall) []attentionItem {
	t.Helper()
	var attention attentionFile
	data, err := os.ReadFile(filepath.Join(in.cfg.Paths.DataDir, dirName, attentionFileName))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &attention))
	return attention.Items
}

func (in *workspaceInstall) writeFile(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(in.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// writeRun records a finished run of the DAG called name, with attempts
// attempts, in the v2 run directory runs, and returns the run's directory
// name. Each attempt's log lies in a directory of its own in v2's log
// directory, and its step output directly in the name's directory, as an
// older layout kept it.
func (in *workspaceInstall) writeRun(t *testing.T, runs, name, runID string, labels []string, attempts int) string {
	t.Helper()
	ctx := context.Background()
	at := persis.NewUTC(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	root := filedagrun.NewDataRoot(runs, name)
	run, err := root.CreateDAGRun(at, runID)
	require.NoError(t, err)
	matches, err := filepath.Glob(filepath.Join(runs, name, "dag-runs", "2026", "01", "02", filedagrun.DAGRunDirPrefix+"*_"+runID))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	runDir := filepath.Base(matches[0])
	flat := filepath.Join(in.cfg.Paths.LogDir, name, "flat_"+runID+".log")
	writeFile(t, flat)
	for i := 1; i <= attempts; i++ {
		attemptAt := persis.NewUTC(at.Add(time.Duration(i) * time.Minute))
		attempt, err := run.CreateAttempt(ctx, attemptAt, nil, "a"+string(rune('0'+i)))
		require.NoError(t, err)
		log := filepath.Join(in.cfg.Paths.LogDir, name, runDir+"-"+string(rune('0'+i)), "run.log")
		writeFile(t, log)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
			Name: name, DAGRunID: runID, Status: ir.Succeeded, Labels: labels, Log: log,
			Nodes: []*ir.Node{{Stdout: flat}},
		}))
		require.NoError(t, attempt.Close(ctx))
	}
	return runDir
}
