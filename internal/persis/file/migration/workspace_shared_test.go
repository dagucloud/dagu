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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG name that DAGs of two projects share splits its runs by the
// workspace each run's labels name, as v2 showed them, and keeps the rest of
// its state in the default project, which the migration records for the
// report. Two files of one workspace with one name move that name's state
// once, so the migration does not refuse its own plan.
func TestRunSplitsSharedDAGName(t *testing.T) {
	t.Parallel()
	in := newWorkspaceInstall(t)
	in.writeFile(t, "dags/report.yaml", "labels:\n  - workspace: billing\nsteps:\n  - run: echo billing\n")
	in.writeFile(t, "dags/report-default.yaml", "name: report\nsteps:\n  - run: echo default\n")
	in.writeFile(t, "dags/dup-a.yaml", "name: dup\nlabels:\n  - workspace: billing\nsteps:\n  - run: echo a\n")
	in.writeFile(t, "dags/dup-b.yaml", "name: dup\nlabels:\n  - workspace: billing\nsteps:\n  - run: echo b\n")
	runs := filepath.Join(in.root, "data", "dag-runs")
	billingRun := in.writeRun(t, runs, "report", "run_b", []string{"workspace=billing"})
	defaultRun := in.writeRun(t, runs, "report", "run_d", nil)
	in.writeRun(t, runs, "dup", "run_x", []string{"workspace=billing"})
	writeFile(t, filepath.Join(in.root, "data", "suspend", "report.suspend"))

	require.NoError(t, newStateMigrator(t, in.cfg).Run(context.Background()))

	state := filepath.Join(in.cfg.Paths.DataDir, "projects")
	runDir := func(project, run string) string {
		return filepath.Join(state, project, "runs", "report", "dag-runs", "2026", "01", "02", run)
	}
	assert.DirExists(t, runDir("billing", billingRun))
	assert.NoDirExists(t, runDir("default", billingRun))
	assert.DirExists(t, runDir("default", defaultRun))
	assert.FileExists(t, filepath.Join(state, "billing", "logs", "report", billingRun, "run.log"))
	assert.FileExists(t, filepath.Join(state, "default", "logs", "report", defaultRun, "run.log"))
	assert.FileExists(t, filepath.Join(state, "default", "state", "suspend", "report.suspend"))
	assert.DirExists(t, filepath.Join(state, "billing", "runs", "dup"))

	var attention attentionFile
	data, err := os.ReadFile(filepath.Join(in.cfg.Paths.DataDir, dirName, attentionFileName))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &attention))
	assert.Equal(t, []attentionItem{{Kind: attentionSharedName, DAG: "report", Projects: []string{"billing", "default"}}},
		attention.Items)
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

// The logs of a workspace DAG that v2 kept on another disk through a link
// stay on that disk: the project's directory of logs links beside the
// default project's.
func TestRunKeepsWorkspaceLogsOnLinkedDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
	t.Parallel()
	in := newWorkspaceInstall(t)
	logs := in.cfg.Paths.LogDir
	disk := filepath.Join(in.root, "disk", "logs")
	require.NoError(t, os.MkdirAll(filepath.Dir(disk), 0o750))
	require.NoError(t, os.Rename(logs, disk))
	writeFile(t, filepath.Join(disk, "admin", "audit", "a.jsonl"))
	require.NoError(t, os.Symlink(disk, logs))

	require.NoError(t, newStateMigrator(t, in.cfg).Run(context.Background()))

	billing := filepath.Join(in.cfg.Paths.DataDir, "projects", "billing", "logs")
	assert.FileExists(t, filepath.Join(billing, "invoices", "dag-run_1", "run.log"))
	assert.FileExists(t, filepath.Join(disk, ".projects", "billing", "invoices", "dag-run_1", "run.log"),
		"the logs stay on their disk")
}

func (in *workspaceInstall) writeFile(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(in.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// writeRun records a finished run of the DAG called name in the v2 run
// directory runs, with its log in v2's log directory, and returns the run's
// directory name.
func (in *workspaceInstall) writeRun(t *testing.T, runs, name, runID string, labels []string) string {
	t.Helper()
	ctx := context.Background()
	at := persis.NewUTC(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	root := filedagrun.NewDataRoot(runs, name)
	run, err := root.CreateDAGRun(at, runID)
	require.NoError(t, err)
	attempt, err := run.CreateAttempt(ctx, at, nil, "a1")
	require.NoError(t, err)
	matches, err := filepath.Glob(filepath.Join(runs, name, "dag-runs", "2026", "01", "02", filedagrun.DAGRunDirPrefix+"*_"+runID))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	runDir := filepath.Base(matches[0])
	log := filepath.Join(in.cfg.Paths.LogDir, name, runDir, "run.log")
	writeFile(t, log)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{Name: name, DAGRunID: runID, Status: ir.Succeeded, Labels: labels, Log: log}))
	require.NoError(t, attempt.Close(ctx))
	return runDir
}
