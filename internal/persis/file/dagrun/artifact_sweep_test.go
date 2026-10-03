// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	// artifactSweepOld is past every cutoff a test uses.
	artifactSweepOld = time.Now().UTC().Add(-72 * time.Hour)
	// artifactSweepFresh is inside the enforced minimum age.
	artifactSweepFresh = time.Now().UTC().Add(-30 * time.Minute)
)

// artifactSweepRunDir creates a run artifact directory holding one file, as a
// run that wrote an artifact leaves behind.
func artifactSweepRunDir(t *testing.T, root, dagName, dagRunID string, at time.Time) string {
	t.Helper()

	dir, err := artifactpath.NewRunDir(context.Background(), root, "", dagName, dagRunID, at)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o600))
	return dir
}

// artifactSweepFill creates dir holding one file, as a run that wrote an
// artifact leaves behind.
func artifactSweepFill(t *testing.T, dir string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o600))
}

// artifactSweepLegacyName returns the pre-date run directory name of a run
// started at artifactSweepOld.
func artifactSweepLegacyName(dagRunID string) string {
	return DAGRunDirPrefix + formatDAGRunTimestamp(persis.NewUTC(artifactSweepOld)) + "_" + dagRunID
}

// resolvedDir evaluates symlinks in dir, the form the sweep reports paths in.
// TempDir itself can sit behind a link (macOS /var -> /private/var), so
// expectations must be built from the resolved base.
func resolvedDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

func artifactSweepRecord(t *testing.T, root, runDir, dagName, dagRunID string) string {
	t.Helper()

	metaPath, ok := artifactpath.MetaPath(root, runDir)
	require.True(t, ok)
	require.NoError(t, artifact.WriteRecord(metaPath, artifact.Record{
		Version:  artifact.RecordVersion,
		Name:     dagName,
		DAGRunID: dagRunID,
		Status:   ir.Succeeded,
		Dir:      runDir,
	}))
	return metaPath
}

func sweepArtifacts(t *testing.T, th RepositoryTest, req persis.ArtifactPruneRequest) *persis.ArtifactPruneResult {
	t.Helper()

	result, err := th.Backend.PruneArtifacts(th.Context, req)
	require.NoError(t, err)
	return result
}

func TestArtifactSweep(t *testing.T) {
	oldEnough := persis.ArtifactPruneRequest{OlderThan: persis.NewUTC(time.Now().Add(-24 * time.Hour))}

	t.Run("RemovesOrphanedDirAndRecord", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		dir := artifactSweepRunDir(t, root, "gone-dag", "gone-run", artifactSweepOld)
		meta := artifactSweepRecord(t, root, dir, "gone-dag", "gone-run")

		result := sweepArtifacts(t, th, oldEnough)

		assert.Contains(t, result.Dirs, dir)
		assert.Contains(t, result.Records, meta)
		assert.NoDirExists(t, dir)
		assert.NoFileExists(t, meta)
		// The emptied date partitions are pruned, the root is not.
		day := artifactSweepOld.Format("2006/01/02")
		assert.NoDirExists(t, filepath.Join(root, filepath.FromSlash(day)))
		assert.NoDirExists(t, filepath.Join(root, artifactSweepOld.Format("2006")))
		assert.DirExists(t, root)
	})

	// A live run keeps its artifacts no matter how old the directory is: a
	// run still writing is a run whose record exists.
	t.Run("KeepsDirsClaimedByRuns", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		th.CreateAttempt(t, time.Now(), "live-run", ir.Running)
		liveDir := artifactSweepRunDir(t, root, "test_DAG", "live-run", artifactSweepOld)
		liveMeta := artifactSweepRecord(t, root, liveDir, "test_DAG", "live-run")

		deadDir := artifactSweepRunDir(t, root, "test_DAG", "dead-run", artifactSweepOld)

		result := sweepArtifacts(t, th, oldEnough)

		assert.DirExists(t, liveDir)
		assert.FileExists(t, liveMeta)
		assert.NoDirExists(t, deadDir)
		assert.NotContains(t, result.Dirs, liveDir)
		assert.Contains(t, result.Dirs, deadDir)
	})

	t.Run("KeepsSubRunArtifacts", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		dag := th.DAG("parent-dag")
		_, err := th.Backend.CreateAttempt(th.Context, persis.DAGRunCreateAttemptRequest{
			DAG:       dag.DAG,
			Timestamp: time.Now(),
			DAGRunID:  "parent-run",
		})
		require.NoError(t, err)
		_, err = th.Backend.CreateAttempt(th.Context, persis.DAGRunCreateAttemptRequest{
			DAG:        dag.DAG,
			Timestamp:  time.Now(),
			DAGRunID:   "child-run",
			RootDAGRun: ir.NewDAGRunRef(dag.Name, "parent-run"),
		})
		require.NoError(t, err)

		// The pre-"sub" layout kept child runs under children/child_<id>.
		dataRoot := NewDataRoot(th.TmpDir, dag.Name)
		parent, err := dataRoot.FindByDAGRunID(th.Context, "parent-run")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(
			filepath.Join(parent.baseDir, LegacySubDAGRunsDir, LegacySubDAGRunDirPrefix+"legacy-child"), 0o750))

		childDir := artifactSweepRunDir(t, root, "child-dag", "child-run", artifactSweepOld)
		legacyDir := artifactSweepRunDir(t, root, "child-dag", "legacy-child", artifactSweepOld)
		result := sweepArtifacts(t, th, oldEnough)

		assert.DirExists(t, childDir)
		assert.DirExists(t, legacyDir)
		assert.NotContains(t, result.Dirs, childDir)
		assert.NotContains(t, result.Dirs, legacyDir)
	})

	t.Run("KeepsFreshOrphansBelowMinAge", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		dir := artifactSweepRunDir(t, root, "gone-dag", "gone-run", artifactSweepFresh)
		meta := artifactSweepRecord(t, root, dir, "gone-dag", "gone-run")

		// A zero bound still applies the minimum age: a directory can exist
		// before the run record that names it.
		result := sweepArtifacts(t, th, persis.ArtifactPruneRequest{})

		assert.Empty(t, result.Dirs)
		assert.Empty(t, result.Records)
		assert.DirExists(t, dir)
		assert.FileExists(t, meta)
	})

	// A record always lands in the global tree, even when artifacts.dir put
	// the run's directory outside it. Pruning the stale record leaves the
	// relocated directory alone.
	t.Run("RemovesRecordForRelocatedDirOnly", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		outside := artifactSweepRunDir(t, resolvedDir(t, t.TempDir()), "gone-dag", "gone-run", artifactSweepOld)
		meta := artifactSweepRecord(t, root, outside, "gone-dag", "gone-run")

		result := sweepArtifacts(t, th, oldEnough)

		assert.Contains(t, result.Records, meta)
		assert.NoFileExists(t, meta)
		assert.DirExists(t, outside)
	})

	t.Run("DryRunReportsWithoutRemoving", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		dir := artifactSweepRunDir(t, root, "gone-dag", "gone-run", artifactSweepOld)
		meta := artifactSweepRecord(t, root, dir, "gone-dag", "gone-run")

		req := oldEnough
		req.DryRun = true
		result := sweepArtifacts(t, th, req)

		assert.Contains(t, result.Dirs, dir)
		assert.Contains(t, result.Records, meta)
		assert.DirExists(t, dir)
		assert.FileExists(t, meta)
	})

	t.Run("SkipsNonConformingEntries", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		day := filepath.Join(root, filepath.FromSlash(artifactSweepOld.Format("2006/01/02")))
		require.NoError(t, os.MkdirAll(day, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(day, "notes.txt"), []byte("x"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(day, "not-a-run-dir"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o600))

		result := sweepArtifacts(t, th, oldEnough)

		assert.Empty(t, result.Dirs)
		assert.Empty(t, result.Records)
		assert.FileExists(t, filepath.Join(day, "notes.txt"))
		assert.DirExists(t, filepath.Join(day, "not-a-run-dir"))
		assert.FileExists(t, filepath.Join(root, "README.md"))
	})

	// Run-shaped names count only at the artifact layout positions. Run
	// history, backups, and deeper trees reuse those names and survive.
	t.Run("IgnoresRunShapedDirsOutsideLayout", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		day := filepath.FromSlash(artifactSweepOld.Format("2006/01/02"))
		kept := []string{
			filepath.Join(root, "backup", "test_DAG", artifactSweepLegacyName("gone-run")),
			filepath.Join(root, "dag-runs", "test_DAG", "dag-runs", day, artifactSweepLegacyName("gone-run")),
			filepath.Join(root, "team", day, artifactSweepOld.Format("150405")+"_gone-dag_"+artifactpath.RunSuffix("gone-run")),
		}
		for _, dir := range kept {
			artifactSweepFill(t, dir)
		}

		result := sweepArtifacts(t, th, oldEnough)

		assert.Empty(t, result.Dirs)
		for _, dir := range kept {
			assert.DirExists(t, dir)
		}
	})

	t.Run("RemovesLegacyDirs", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		th.CreateAttempt(t, time.Now(), "live-run", ir.Running)

		stale := filepath.Join(root, "legacy-dag", artifactSweepLegacyName("dead-run"))
		live := filepath.Join(root, "legacy-dag", artifactSweepLegacyName("live-run"))
		artifactSweepFill(t, stale)
		artifactSweepFill(t, live)

		result := sweepArtifacts(t, th, oldEnough)

		assert.Contains(t, result.Dirs, stale)
		assert.NoDirExists(t, stale)
		assert.DirExists(t, live)
	})

	// A run ID shaped like a partitioned suffix — a fixed-length hexadecimal
	// string — must not shield a legacy orphan whose directory is keyed by it.
	t.Run("RemovesLegacyDirWithSuffixShapedID", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		th.CreateAttempt(t, time.Now(), "live-run", ir.Running)

		colliding := filepath.Join(root, "legacy-dag", artifactSweepLegacyName(artifactpath.RunSuffix("live-run")))
		artifactSweepFill(t, colliding)

		result := sweepArtifacts(t, th, oldEnough)

		assert.Contains(t, result.Dirs, colliding)
		assert.NoDirExists(t, colliding)
	})

	// The reverse direction: a partitioned orphan whose suffix equals a live
	// run's ID must not be kept by that run.
	t.Run("RemovesPartitionedDirWithIDShapedSuffix", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		th.CreateAttempt(t, time.Now(), "0123456789abcdef", ir.Running)

		day := filepath.Join(root, filepath.FromSlash(artifactSweepOld.Format("2006/01/02")))
		colliding := filepath.Join(day, artifactSweepOld.Format("150405")+"_gone-dag_0123456789abcdef")
		artifactSweepFill(t, colliding)

		result := sweepArtifacts(t, th, oldEnough)

		assert.Contains(t, result.Dirs, colliding)
		assert.NoDirExists(t, colliding)
	})

	// A root that is a symlink to the filesystem root must still trip the
	// filesystem-root guard: Abs and Clean do not resolve the link, so
	// sweeping it would roam the link target.
	t.Run("RejectsSymlinkedFilesystemRoot", func(t *testing.T) {
		th := setupTestRepository(t)

		link := filepath.Join(th.TmpDir, "root-link")
		if err := os.Symlink("/", link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("creating symlinks requires privilege on Windows: %v", err)
			}
			require.NoError(t, err)
		}

		_, err := th.Backend.PruneArtifacts(th.Context, persis.ArtifactPruneRequest{Root: link})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to sweep filesystem root")
	})

	// The run history is never an artifact tree, so a root holding it is
	// refused before anything is examined.
	t.Run("RejectsRootContainingRunHistory", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")
		orphan := artifactSweepRunDir(t, root, "gone-dag", "gone-run", artifactSweepOld)

		_, err := th.Backend.PruneArtifacts(th.Context, persis.ArtifactPruneRequest{Root: th.TmpDir})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to sweep")
		assert.DirExists(t, orphan)
	})

	// Logs share the pre-date artifact layout, so a root that is or holds a
	// protected log directory must not reach its run directories.
	t.Run("RejectsRootContainingProtectedDir", func(t *testing.T) {
		th := setupTestRepository(t)
		base := resolvedDir(t, t.TempDir())
		logDir := filepath.Join(base, "logs")
		logRunDir := filepath.Join(logDir, "test_DAG", artifactSweepLegacyName("gone-run"))
		artifactSweepFill(t, logRunDir)

		for _, root := range []string{logDir, base} {
			req := oldEnough
			req.Root = root
			req.ProtectedDirs = []string{logDir}
			_, err := th.Backend.PruneArtifacts(th.Context, req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refusing to sweep")
		}
		assert.DirExists(t, logRunDir)
	})

	// A symlinked root that stays inside the filesystem sweeps its target as
	// a plain root does.
	t.Run("SweepsThroughSymlinkedRoot", func(t *testing.T) {
		th := setupTestRepository(t)
		root := filepath.Join(resolvedDir(t, th.TmpDir), "artifacts")

		stale := artifactSweepRunDir(t, root, "gone-dag", "dead-run", artifactSweepOld)

		link := filepath.Join(th.TmpDir, "root-link")
		if err := os.Symlink(root, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("creating symlinks requires privilege on Windows: %v", err)
			}
			require.NoError(t, err)
		}

		req := oldEnough
		req.Root = link
		result := sweepArtifacts(t, th, req)

		assert.Contains(t, result.Dirs, stale)
		assert.NoDirExists(t, stale)
	})

	// Pointing the sweep at a root outside the configured one reclaims a tree
	// left at a previous data directory; liveness still applies there.
	t.Run("SweepsExternalRoot", func(t *testing.T) {
		th := setupTestRepository(t)

		th.CreateAttempt(t, time.Now(), "live-run", ir.Running)

		oldRoot := resolvedDir(t, t.TempDir())
		stale := artifactSweepRunDir(t, oldRoot, "gone-dag", "dead-run", artifactSweepOld)
		live := artifactSweepRunDir(t, oldRoot, "live-dag", "live-run", artifactSweepOld)
		meta := artifactSweepRecord(t, oldRoot, stale, "gone-dag", "dead-run")

		req := oldEnough
		req.Root = oldRoot
		result := sweepArtifacts(t, th, req)

		assert.NoDirExists(t, stale)
		assert.NoFileExists(t, meta)
		assert.DirExists(t, live)
		assert.Contains(t, result.Dirs, stale)
		assert.NotContains(t, result.Dirs, live)
	})
}
