// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSeedRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, logDir := newSeedRepository(t)
	dag := seedTestDAG()

	_, seeded, err := intake.SeedRun(ctx, intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         "run-1",
		Nodes:            transform.SeedNodes(dag, nil, []string{"build"}),
		Params:           "ENV=dev",
		TriggerType:      ir.TriggerTypeManual,
		TriggerActor:     "alice",
		ProfileName:      "dev",
		NoReuse:          true,
		LogBaseDir:       logDir,
	})
	require.NoError(t, err)

	status := readLatestStatus(t, repo, dag.Name, "run-1")
	require.Equal(t, seeded.AttemptID, status.AttemptID)
	require.Equal(t, ir.Queued, status.Status)
	require.Equal(t, ir.TriggerTypeManual, status.TriggerType)
	require.Equal(t, "alice", status.TriggerActor)
	require.Equal(t, "dev", status.ProfileName)
	require.True(t, status.NoReuse)
	require.Equal(t, "ENV=dev", status.Params)
	require.True(t, strings.HasPrefix(status.Log, logDir))
	require.Len(t, status.Nodes, 2)
	require.Equal(t, ir.NodeSkipped, status.Nodes[0].Status)
	require.True(t, status.Nodes[0].SkippedByRetry)
	require.Equal(t, ir.NodeNotStarted, status.Nodes[1].Status)
}

// A source run's work directory is copied into the seeded run, and carried
// output variables that point into it are rewritten to the copy.
func TestSeedRunCopiesWorkDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, logDir := newSeedRepository(t)
	dag := seedTestDAG()

	sourceRef := ir.NewDAGRunRef(dag.Name, "source")
	sourceAttempt, err := repo.CreateAttempt(ctx, dag, time.Now(), sourceRef.ID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	sourceWorkDir, err := repo.MaterializeWorkDir(ctx, dagrun.WorkDirRef{DAGRun: sourceRef})
	require.NoError(t, err)
	sourceFile := filepath.Join(sourceWorkDir, "result.txt")
	require.NoError(t, os.WriteFile(sourceFile, []byte("from-source"), 0o600))
	source := ir.NewStatusBuilder(dag).Create(sourceRef.ID, ir.Succeeded, 0, time.Now(), ir.WithAttemptID(sourceAttempt.ID()))
	source.Nodes[0].Status = ir.NodeSucceeded
	source.Nodes[0].OutputVariables = &collections.SyncMap{}
	source.Nodes[0].OutputVariables.Store("RESULT", "RESULT="+sourceFile)
	require.NoError(t, sourceAttempt.Open(ctx))
	require.NoError(t, sourceAttempt.Write(ctx, source))
	require.NoError(t, sourceAttempt.Close(ctx))
	require.NoError(t, repo.SnapshotWorkDir(ctx, dagrun.WorkDirRef{DAGRun: sourceRef}, sourceWorkDir))

	_, _, err = intake.SeedRun(ctx, intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         "run-1",
		Nodes:            transform.SeedNodes(dag, &source, []string{"build"}),
		Source:           &source,
		TriggerType:      ir.TriggerTypeManual,
		LogBaseDir:       logDir,
	})
	require.NoError(t, err)

	workDir, err := repo.MaterializeWorkDir(ctx, dagrun.WorkDirRef{DAGRun: ir.NewDAGRunRef(dag.Name, "run-1")})
	require.NoError(t, err)
	copied := filepath.Join(workDir, "result.txt")
	content, err := os.ReadFile(copied) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, "from-source", string(content))

	status := readLatestStatus(t, repo, dag.Name, "run-1")
	raw, ok := status.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT="+copied, raw)
}

// A read-only directory in the source work directory, such as a Go module
// cache, is copied with its files and keeps its permissions.
func TestSeedRunCopiesReadOnlyDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("directory permission bits are not enforced on Windows")
	}
	t.Parallel()

	ctx := context.Background()
	repo, logDir := newSeedRepository(t)
	dag := seedTestDAG()

	sourceRef := ir.NewDAGRunRef(dag.Name, "source")
	sourceAttempt, err := repo.CreateAttempt(ctx, dag, time.Now(), sourceRef.ID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	sourceWorkDir, err := repo.MaterializeWorkDir(ctx, dagrun.WorkDirRef{DAGRun: sourceRef})
	require.NoError(t, err)
	readOnlyDir := filepath.Join(sourceWorkDir, "modcache")
	require.NoError(t, os.Mkdir(readOnlyDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(readOnlyDir, "go.mod"), []byte("module x\n"), 0o400))
	require.NoError(t, os.Chmod(readOnlyDir, 0o500))
	source := ir.NewStatusBuilder(dag).Create(sourceRef.ID, ir.Succeeded, 0, time.Now(), ir.WithAttemptID(sourceAttempt.ID()))
	source.Nodes[0].Status = ir.NodeSucceeded
	require.NoError(t, sourceAttempt.Open(ctx))
	require.NoError(t, sourceAttempt.Write(ctx, source))
	require.NoError(t, sourceAttempt.Close(ctx))

	workDir, err := repo.MaterializeWorkDir(ctx, dagrun.WorkDirRef{DAGRun: ir.NewDAGRunRef(dag.Name, "run-1")})
	require.NoError(t, err)
	copiedDir := filepath.Join(workDir, "modcache")
	// Read-only directories would make the temporary directory cleanup fail.
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o750)
		_ = os.Chmod(copiedDir, 0o750)
	})

	_, _, err = intake.SeedRun(ctx, intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         "run-1",
		Nodes:            transform.SeedNodes(dag, &source, []string{"build"}),
		Source:           &source,
		TriggerType:      ir.TriggerTypeManual,
		LogBaseDir:       logDir,
	})
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(copiedDir, "go.mod")) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, "module x\n", string(content))
	info, err := os.Stat(copiedDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o500), info.Mode().Perm())
}

func TestSeedRunExistingRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, logDir := newSeedRepository(t)
	dag := seedTestDAG()
	existing, err := repo.CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, existing.Open(ctx))
	require.NoError(t, existing.Write(ctx, ir.NewStatusBuilder(dag).Create("run-1", ir.Succeeded, 0, time.Now(), ir.WithAttemptID(existing.ID()))))
	require.NoError(t, existing.Close(ctx))

	_, _, err = intake.SeedRun(ctx, intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         "run-1",
		Nodes:            transform.SeedNodes(dag, nil, nil),
		LogBaseDir:       logDir,
	})
	require.Error(t, err)

	status := readLatestStatus(t, repo, dag.Name, "run-1")
	require.Equal(t, ir.Succeeded, status.Status)
}

func TestMarkSeedFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, logDir := newSeedRepository(t)
	dag := seedTestDAG()
	_, seeded, err := intake.SeedRun(ctx, intake.SeedRequest{
		DAGRunRepository: repo,
		DAG:              dag,
		DAGRunID:         "run-1",
		Nodes:            transform.SeedNodes(dag, nil, nil),
		TriggerType:      ir.TriggerTypeManual,
		LogBaseDir:       logDir,
	})
	require.NoError(t, err)

	intake.MarkSeedFailed(ctx, repo, seeded, errors.New("launch failed"))

	status := readLatestStatus(t, repo, dag.Name, "run-1")
	require.Equal(t, ir.Failed, status.Status)
	require.Equal(t, "launch failed", status.Error)

	// A run that already left the queued state keeps its status.
	intake.MarkSeedFailed(ctx, repo, seeded, errors.New("second failure"))
	status = readLatestStatus(t, repo, dag.Name, "run-1")
	require.Equal(t, "launch failed", status.Error)
}

func newSeedRepository(t *testing.T) (*persis.DAGRunRepository, string) {
	t.Helper()
	tmp := t.TempDir()
	repo := testutil.NewFileDAGRunRepository(filepath.Join(tmp, "dag-runs"), persis.DAGRunRepositoryOptions{})
	return repo, filepath.Join(tmp, "logs")
}

func seedTestDAG() *ir.DAG {
	dag := &ir.DAG{
		Name: "seeded",
		Steps: []ir.Step{
			{Name: "build"},
			{Name: "publish", Depends: []string{"build"}},
		},
	}
	ir.InitializeDefaults(dag)
	return dag
}

func readLatestStatus(t *testing.T, repo *persis.DAGRunRepository, dagName, dagRunID string) *ir.DAGRunStatus {
	t.Helper()
	attempt, err := repo.FindAttempt(context.Background(), ir.NewDAGRunRef(dagName, dagRunID))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(context.Background())
	require.NoError(t, err)
	return status
}
