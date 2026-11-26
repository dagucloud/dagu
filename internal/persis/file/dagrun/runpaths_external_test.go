// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dagrun/dagrunindex"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runPathsDAG = "etl"

var runPathsStartedAt = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

// runPathsFixture is a v2 install and the state directory its default
// project uses once projects are on.
type runPathsFixture struct {
	runsDir string
	project runpath.Dirs
	legacy  runpath.Dirs
	// outside is a log file a DAG placed outside every directory above.
	outside string
}

func newRunPathsFixture(t *testing.T) runPathsFixture {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, "data", "projects", "default")
	return runPathsFixture{
		runsDir: filepath.Join(home, "data", "dag-runs"),
		project: runpath.Dirs{Log: filepath.Join(state, "logs"), Artifact: filepath.Join(state, "artifacts")},
		legacy:  runpath.Dirs{Log: filepath.Join(home, "logs"), Artifact: filepath.Join(home, "data", "artifacts")},
		outside: filepath.Join(t.TempDir(), "custom", "load.out"),
	}
}

// repository opens the runs with paths. The trusted artifact root is the
// directory paths resolves artifacts in.
func (f runPathsFixture) repository(paths runpath.Mapper, artifactDir string) *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(
		filedagrun.NewStore(f.runsDir, filedagrun.WithArtifactDir(artifactDir), filedagrun.WithRunPaths(paths)),
		filedagrun.NewWorkDirStore(filepath.Join(f.runsDir, ".dag-run-work"), f.runsDir),
		persis.DAGRunRepositoryOptions{},
	)
}

// status returns a finished run whose log, step output, and artifacts lie in
// dirs, apart from one step that writes to the fixture's outside path.
func (f runPathsFixture) status(dirs runpath.Dirs, dagRunID string) ir.DAGRunStatus {
	logDir := filepath.Join(dirs.Log, runPathsDAG, "dag-run_"+dagRunID)
	return ir.DAGRunStatus{
		Name:       runPathsDAG,
		DAGRunID:   dagRunID,
		AttemptID:  "attempt-" + dagRunID,
		Status:     ir.Succeeded,
		Log:        filepath.Join(logDir, "dag-run.log"),
		ArchiveDir: filepath.Join(dirs.Artifact, "2026", "11", "25", "120000_etl_"+artifactpath.RunSuffix(dagRunID)),
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "extract"},
				Stdout: filepath.Join(logDir, "extract.out"), Stderr: filepath.Join(logDir, "extract.err"),
				PushBackPreviousStdout: filepath.Join(logDir, "extract.1.out"),
				RetryNodeRunsDir:       logpath.NodeRunsDir(logDir, "extract"),
			},
			{Step: ir.Step{Name: "load"}, Stdout: f.outside, Stderr: f.outside},
		},
		OnExit: &ir.Node{Step: ir.Step{Name: "onExit"}, Stdout: filepath.Join(logDir, "on_exit.log"), Stderr: filepath.Join(logDir, "on_exit.log")},
	}
}

func writeRun(t *testing.T, repo *persis.DAGRunRepository, status ir.DAGRunStatus) {
	t.Helper()
	ctx := context.Background()
	attempt, err := repo.CreateAttempt(ctx, &ir.DAG{Name: status.Name}, runPathsStartedAt, status.DAGRunID,
		persis.DAGRunCreateAttemptOptions{AttemptID: status.AttemptID})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func readRun(t *testing.T, repo *persis.DAGRunRepository, dagRunID string) *ir.DAGRunStatus {
	t.Helper()
	ctx := context.Background()
	attempt, err := repo.FindAttempt(ctx, ir.NewDAGRunRef(runPathsDAG, dagRunID))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	return status
}

func assertRunPaths(t *testing.T, want ir.DAGRunStatus, got *ir.DAGRunStatus) {
	t.Helper()
	assert.Equal(t, want.Log, got.Log)
	assert.Equal(t, want.ArchiveDir, got.ArchiveDir)
	require.Len(t, got.Nodes, len(want.Nodes))
	for i := range want.Nodes {
		assert.Equal(t, want.Nodes[i].Stdout, got.Nodes[i].Stdout)
		assert.Equal(t, want.Nodes[i].Stderr, got.Nodes[i].Stderr)
		assert.Equal(t, want.Nodes[i].PushBackPreviousStdout, got.Nodes[i].PushBackPreviousStdout)
		assert.Equal(t, want.Nodes[i].RetryNodeRunsDir, got.Nodes[i].RetryNodeRunsDir)
	}
	require.NotNil(t, got.OnExit)
	assert.Equal(t, want.OnExit.Stdout, got.OnExit.Stdout)
}

func TestRunPathsStoredRelative(t *testing.T) {
	f := newRunPathsFixture(t)
	repo := f.repository(runpath.New(f.project, f.project, true), f.project.Artifact)
	status := f.status(f.project, "run-1")

	writeRun(t, repo, status)

	stored, err := filedagrun.ParseStatusFile(findOnlyStatusFile(t, f.runsDir))
	require.NoError(t, err)
	assert.Equal(t, "./etl/dag-run_run-1/dag-run.log", stored.Log)
	assert.Equal(t, "./2026/11/25/120000_etl_"+artifactpath.RunSuffix("run-1"), stored.ArchiveDir)
	assert.Equal(t, "./etl/dag-run_run-1/extract.out", stored.Nodes[0].Stdout)
	assert.Equal(t, "./etl/dag-run_run-1/extract.err", stored.Nodes[0].Stderr)
	assert.Equal(t, "./etl/dag-run_run-1/extract.1.out", stored.Nodes[0].PushBackPreviousStdout)
	assert.Equal(t, "./etl/dag-run_run-1/node-runs/extract", stored.Nodes[0].RetryNodeRunsDir)
	assert.Equal(t, f.outside, stored.Nodes[1].Stdout)
	assert.Equal(t, "./etl/dag-run_run-1/on_exit.log", stored.OnExit.Stdout)

	assertRunPaths(t, status, readRun(t, repo, "run-1"))
	assert.Equal(t, filepath.Join(f.project.Log, "etl", "dag-run_run-1", "extract.out"), status.Nodes[0].Stdout,
		"the status written is left as it is")
}

// With projects off, a record is the one an install without projects
// writes, so an older binary reads it.
func TestRunPathsStoredAsGivenWithProjectsOff(t *testing.T) {
	v2 := newRunPathsFixture(t)
	off := v2
	off.runsDir = filepath.Join(t.TempDir(), "dag-runs")
	status := v2.status(v2.legacy, "run-1")

	writeRun(t, v2.repository(runpath.Mapper{}, v2.legacy.Artifact), status)
	writeRun(t, off.repository(runpath.New(v2.legacy, v2.legacy, false), v2.legacy.Artifact), status)

	want, err := os.ReadFile(findOnlyStatusFile(t, v2.runsDir))
	require.NoError(t, err)
	got, err := os.ReadFile(findOnlyStatusFile(t, off.runsDir))
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

// A record written before projects maps its paths under the v2 directories
// to the project's, and is not rewritten.
func TestRunPathsReadV2Record(t *testing.T) {
	f := newRunPathsFixture(t)
	writeRun(t, f.repository(runpath.Mapper{}, f.legacy.Artifact), f.status(f.legacy, "run-1"))
	statusFile := findOnlyStatusFile(t, f.runsDir)
	before, err := os.ReadFile(statusFile)
	require.NoError(t, err)

	repo := f.repository(runpath.New(f.project, f.legacy, true), f.project.Artifact)
	assertRunPaths(t, f.status(f.project, "run-1"), readRun(t, repo, "run-1"))

	after, err := os.ReadFile(statusFile)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestRunPathsRemoveRun(t *testing.T) {
	f := newRunPathsFixture(t)
	repo := f.repository(runpath.New(f.project, f.project, true), f.project.Artifact)
	status := f.status(f.project, "run-1")
	files := []string{status.Log, status.Nodes[0].Stdout, status.Nodes[0].Stderr, filepath.Join(status.ArchiveDir, "report.csv")}
	for _, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	}
	writeRun(t, repo, status)

	require.NoError(t, repo.RemoveDAGRun(context.Background(), ir.NewDAGRunRef(runPathsDAG, "run-1"), persis.DAGRunRemoveOptions{}))

	for _, file := range files {
		assert.NoFileExists(t, file)
	}
	assert.NoDirExists(t, status.ArchiveDir)
}

// A listing built from the day index, which copies a record's stored
// artifact directory, still names the directory, and a listing of whole
// statuses names every path.
func TestRunPathsListing(t *testing.T) {
	f := newRunPathsFixture(t)
	repo := f.repository(runpath.New(f.project, f.project, true), f.project.Artifact)
	want := f.status(f.project, "run-0")
	for i := range dagrunindex.MinRunsForIndex {
		writeRun(t, repo, f.status(f.project, fmt.Sprintf("run-%d", i)))
	}

	opts := persis.DAGRunListOptions{AllHistory: true, DAGRunID: "run-0", Limit: 20}
	// The first listing builds the index; the second reads it.
	_, err := repo.ListStatusesPage(context.Background(), opts)
	require.NoError(t, err)
	page, err := repo.ListStatusesPage(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, want.ArchiveDir, page.Items[0].ArchiveDir)

	opts.FullStatus = true
	page, err = repo.ListStatusesPage(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assertRunPaths(t, want, page.Items[0])
}

func TestRunPathsArtifactIndexRecord(t *testing.T) {
	f := newRunPathsFixture(t)
	repo := f.repository(runpath.New(f.project, f.project, true), f.project.Artifact)
	status := f.status(f.project, "run-1")
	require.NoError(t, os.MkdirAll(status.ArchiveDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(status.ArchiveDir, "report.csv"), []byte("x"), 0o600))

	writeRun(t, repo, status)

	metaPath, ok := artifactpath.MetaPath(f.project.Artifact, status.ArchiveDir)
	require.True(t, ok)
	record, err := artifact.ReadRecord(metaPath)
	require.NoError(t, err)
	assert.Equal(t, "./2026/11/25/120000_etl_"+artifactpath.RunSuffix("run-1"), record.Dir)
}
