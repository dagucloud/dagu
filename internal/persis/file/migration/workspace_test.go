// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dagState lists, relative to a project's state directory, where the stores
// keep each kind of state of the DAG invoices once migration is done.
var dagState = []string{
	"runs/invoices/dag-runs/2026/01/02/dag-run_1/status.jsonl",
	"state/dag-runs-ephemeral/invoices/dag-runs/2026/01/02/dag-run_2/status.jsonl",
	"state/dag-run-work/invoices/run1/root/work.txt",
	"logs/invoices/dag-run_1/run.log",
	"artifacts/invoices/dag-run_0/old.md",
	"artifacts/2026/01/02/" + invoicesArtifactDir + "/report.md",
	"artifacts/2026/01/02/" + invoicesArtifactDir + artifactpath.MetaSuffix,
	"state/queue/invoices/item_low_20260102_000000_000000000Z_run_9.json",
	"state/suspend/invoices.suspend",
	"state/dag-settings/invoices.json",
	"state/scheduler/dags/invoices.json",
	"state/slot-index/" + hex.EncodeToString([]byte("invoices")) + "/2026-01-02.jsonl",
	"state/dag-state/v1/dag/" + hex.EncodeToString([]byte("invoices")) + "/6b.json",
	"settings/webhook-dag-claims/invoices.json",
	"settings/webhooks/w1.json",
}

var invoicesArtifactDir = "120000_invoices_" + artifactpath.RunSuffix("run_1")

// workspaceInstall is a v2 install with the workspaces billing and con, a
// Windows device name. The DAG invoices is billing's, deploy is con's, and
// cleanup and typo, whose label names no workspace, are the default
// project's. Every kind of state of invoices, and the runs of cleanup and
// deploy, are in v2's directories.
type workspaceInstall struct {
	root string
	cfg  *config.Config
}

func newWorkspaceInstall(t *testing.T) *workspaceInstall {
	t.Helper()
	in := &workspaceInstall{root: t.TempDir()}
	in.cfg = v2Install(in.root)
	ctx := context.Background()

	workspaces, err := persisfile.NewServerStores(in.cfg).WorkspaceStore()
	require.NoError(t, err)
	for _, name := range []string{"billing", "con"} {
		require.NoError(t, workspaces.Create(ctx, workspace.NewWorkspace(name, "")))
	}
	in.writeDAG(t, "invoices", "billing")
	in.writeDAG(t, "deploy", "con")
	in.writeDAG(t, "typo", "nowhere")
	in.writeDAG(t, "cleanup", "")

	// v2 keeps the state of invoices in these places, relative to the
	// install; the artifact sidecar and the collections are written below.
	v2 := []string{
		"data/dag-runs/invoices/dag-runs/2026/01/02/dag-run_1/status.jsonl",
		"data/dag-runs-ephemeral/invoices/dag-runs/2026/01/02/dag-run_2/status.jsonl",
		"data/dag-run-work/invoices/run1/root/work.txt",
		"logs/invoices/dag-run_1/run.log",
		"data/artifacts/invoices/dag-run_0/old.md",
		"data/artifacts/2026/01/02/" + invoicesArtifactDir + "/report.md",
		"data/queue/invoices/item_low_20260102_000000_000000000Z_run_9.json",
		"data/suspend/invoices.suspend",
		"data/dag-settings/invoices.json",
		"data/scheduler/dags/invoices.json",
		"data/slot-index/" + hex.EncodeToString([]byte("invoices")) + "/2026-01-02.jsonl",
		"data/dag-state/v1/dag/" + hex.EncodeToString([]byte("invoices")) + "/6b.json",
	}
	for _, rel := range v2 {
		writeFile(t, filepath.Join(in.root, rel))
	}
	require.NoError(t, fileartifact.WriteRecord(
		filepath.Join(in.root, "data/artifacts/2026/01/02", invoicesArtifactDir+artifactpath.MetaSuffix),
		fileartifact.Record{Version: fileartifact.RecordVersion, Name: "invoices", DAGRunID: "run_1"}))
	put := func(dir, id, data string) {
		now := time.Now()
		require.NoError(t, persisfile.NewCollection(filepath.Join(in.root, "data", dir)).Create(ctx,
			&persis.Record{ID: id, Data: []byte(data), CreatedAt: now, UpdatedAt: now}))
	}
	put("webhook-dag-claims", "invoices", `{"webhookId":"w1"}`)
	put("webhooks", "w1", `{"dagName":"invoices"}`)
	writeFile(t, filepath.Join(in.root, "data/dag-runs/cleanup/dag-runs/2026/01/02/dag-run_3/status.jsonl"))
	writeFile(t, filepath.Join(in.root, "data/dag-runs/deploy/dag-runs/2026/01/02/dag-run_4/status.jsonl"))
	return in
}

func (in *workspaceInstall) writeDAG(t *testing.T, name, ws string) {
	t.Helper()
	content := "steps:\n  - run: echo " + name + "\n"
	if ws != "" {
		content = "labels:\n  - workspace: " + ws + "\n" + content
	}
	path := filepath.Join(in.cfg.Paths.DAGsDir, name+".yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// requireMigrated requires the projects that the workspaces become, the
// files that run in place, and the state of each DAG in its project.
func (in *workspaceInstall) requireMigrated(t *testing.T) []project.Project {
	t.Helper()
	ctx := context.Background()
	registry := projectregistry.New(in.cfg.Paths.DataDir)
	projects, err := registry.List(ctx)
	require.NoError(t, err)
	require.Len(t, projects, 2)
	billing, con := projects[0], projects[1]
	assert.Equal(t, "billing", billing.Name)
	assert.Equal(t, "billing", billing.DisplayName)
	assert.Equal(t, "billing", billing.MigratedFrom)
	assert.Equal(t, []string{"invoices"}, billing.MigratedDAGs)
	assert.Equal(t, in.cfg.Paths.DataDir, billing.SlotSeed)
	assert.Equal(t, "con-2", con.Name)
	assert.Equal(t, "con", con.DisplayName)
	assert.Equal(t, "con", con.MigratedFrom)

	records := registry.InPlace(in.cfg.Paths.ProjectsDir)
	files, err := records.InPlaceFiles(ctx, "billing")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, samePathOnDisk(t, filepath.Join(in.cfg.Paths.DAGsDir, "invoices.yaml"), files[0]))
	files, err = records.InPlaceFiles(ctx, "con-2")
	require.NoError(t, err)
	require.Len(t, files, 1)

	for _, p := range projects {
		record, err := projectrecord.Read(filepath.Join(in.cfg.Paths.DataDir, "projects", p.Name))
		require.NoError(t, err)
		assert.Equal(t, projectrecord.Record{ID: p.ID, Name: p.Name}, record)
	}
	state := filepath.Join(in.cfg.Paths.DataDir, "projects")
	for _, rel := range dagState {
		assert.FileExists(t, filepath.Join(state, "billing", filepath.FromSlash(rel)))
		assert.NoFileExists(t, filepath.Join(state, project.DefaultName, filepath.FromSlash(rel)))
	}
	assert.FileExists(t, filepath.Join(state, "con-2/runs/deploy/dag-runs/2026/01/02/dag-run_4/status.jsonl"))
	assert.FileExists(t, filepath.Join(state, "default/runs/cleanup/dag-runs/2026/01/02/dag-run_3/status.jsonl"))
	assert.NoDirExists(t, filepath.Join(state, "nowhere"))
	return projects
}

// Each workspace becomes a project, and each of its DAGs runs in place as
// the project's, with its state moved on from the default project.
func TestRunMakesWorkspacesProjects(t *testing.T) {
	t.Parallel()
	in := newWorkspaceInstall(t)

	require.NoError(t, newStateMigrator(t, in.cfg).Run(context.Background()))

	in.requireMigrated(t)
	assert.NoDirExists(t, filepath.Join(in.cfg.Paths.ProjectsDir, "billing"), "migration moves no DAG file")
}

// A data directory that an earlier release migrated, moving every DAG's
// state into the default project, migrates again: the state of workspace
// DAGs moves on, and a migration killed partway resumes with the projects it
// registered.
func TestRunMovesWorkspaceStateAfterEarlierRelease(t *testing.T) {
	t.Parallel()
	in := newWorkspaceInstall(t)
	errStop := errors.New("stopped after the earlier release's moves")
	earlier := newStateMigrator(t, in.cfg)
	planned := earlier.planMoves
	earlier.planMoves = func(ctx context.Context, version int) ([]move, error) {
		if version > firstPlanVersion {
			return nil, errStop
		}
		return planned(ctx, version)
	}
	require.ErrorIs(t, earlier.Run(context.Background()), errStop)
	require.NoError(t, earlier.journal.writeStatus(statusFile{Progress: Progress{Phase: phaseDone}, Version: firstPlanVersion}))
	assert.FileExists(t, filepath.Join(in.cfg.Paths.DataDir, "projects/default/runs/invoices/dag-runs/2026/01/02/dag-run_1/status.jsonl"))

	killed := newStateMigrator(t, in.cfg)
	killed.afterMove = func(index int) error {
		if index == 2 {
			return errKilled
		}
		return nil
	}
	require.ErrorIs(t, killed.Run(context.Background()), errKilled)
	before, err := projectregistry.New(in.cfg.Paths.DataDir).List(context.Background())
	require.NoError(t, err)

	resumed := newStateMigrator(t, in.cfg)
	resumed.planMoves = refusePlan(t)
	require.NoError(t, resumed.Run(context.Background()))

	assert.Equal(t, before, in.requireMigrated(t))
}

// samePathOnDisk reports whether a and b name one existing file.
func samePathOnDisk(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	require.NoError(t, err)
	bi, err := os.Stat(b)
	require.NoError(t, err)
	return os.SameFile(ai, bi)
}
