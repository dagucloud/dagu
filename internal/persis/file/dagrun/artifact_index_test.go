// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// artifactIndexFixture drives an attempt through Attempt.Write so the index
// side effect is exercised the way production reaches it.
type artifactIndexFixture struct {
	th           RepositoryTest
	artifactRoot string
	dag          *ir.DAG

	// root is the run the artifacts belong to, left zero for a root run.
	root ir.DAGRunRef
}

func newArtifactIndexFixture(t *testing.T) artifactIndexFixture {
	t.Helper()

	th := setupTestRepository(t)
	return artifactIndexFixture{
		th:           th,
		artifactRoot: filepath.Join(th.TmpDir, "artifacts"),
		dag: &ir.DAG{
			Name:      "index-dag",
			Location:  filepath.Join(th.TmpDir, "index-dag.yaml"),
			Artifacts: &ir.ArtifactsConfig{Enabled: true},
		},
	}
}

// runDir creates an artifact directory under root and optionally puts a file in
// it, mirroring what a run that wrote an artifact leaves behind.
func (f artifactIndexFixture) runDir(t *testing.T, root, dagRunID string, withFile bool) string {
	t.Helper()

	dir, err := artifactpath.NewRunDir(
		context.Background(), root, "", f.dag.Name, dagRunID, time.Date(2026, 9, 15, 14, 32, 7, 0, time.UTC))
	require.NoError(t, err)
	if withFile {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "report.md"), []byte("ok"), 0o600))
	}
	return dir
}

// write records one attempt's status. retry asks for an additional attempt on
// an existing run, as an auto-retry does.
func (f artifactIndexFixture) write(t *testing.T, dagRunID string, status ir.Status, archiveDir string, retry bool) dagrun.Attempt {
	t.Helper()

	attempt, err := f.th.Backend.CreateAttempt(f.th.Context, persis.DAGRunCreateAttemptRequest{
		DAG:       f.dag,
		Timestamp: time.Date(2026, 9, 15, 14, 32, 7, 0, time.UTC),
		DAGRunID:  dagRunID,
		Retry:     retry,
	})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(f.th.Context))
	defer func() { _ = attempt.Close(f.th.Context) }()

	f.writeTo(t, attempt, dagRunID, status, archiveDir)
	return attempt
}

func (f artifactIndexFixture) writeTo(
	t *testing.T, attempt dagrun.Attempt, dagRunID string, status ir.Status, archiveDir string,
) {
	t.Helper()

	st := ir.InitialStatus(f.dag)
	st.DAGRunID = dagRunID
	st.Status = status
	st.ArchiveDir = archiveDir
	st.AttemptID = attempt.ID()
	st.Root = f.root
	require.NoError(t, attempt.Write(f.th.Context, st))
}

func TestArtifactIndexWrite(t *testing.T) {
	t.Run("RecordsFinishedRun", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		f.write(t, "run-1", ir.Succeeded, dir, false)

		rec, err := artifact.ReadRecord(dir + artifactpath.MetaSuffix)
		require.NoError(t, err)
		assert.Equal(t, "index-dag", rec.Name)
		assert.Equal(t, "run-1", rec.DAGRunID)
		assert.Equal(t, ir.Succeeded, rec.Status)
		assert.Equal(t, dir, rec.Dir)
	})

	t.Run("SkipsRunningStatus", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		f.write(t, "run-1", ir.Running, dir, false)

		assert.NoFileExists(t, dir+artifactpath.MetaSuffix)
	})

	// A run that wrote nothing has nothing to list, so it stays out of the index.
	// NotStarted is not "active", so a guard written as !IsActive() lets a
	// pre-execution status through. A retry reuses a directory that already
	// holds files, so nothing else stops it.
	t.Run("SkipsNotStartedStatus", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		f.write(t, "run-1", ir.NotStarted, dir, false)

		assert.NoFileExists(t, dir+artifactpath.MetaSuffix)
	})

	t.Run("TerminalWriteAfterNotStartedWins", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		attempt := f.write(t, "run-1", ir.NotStarted, dir, false)
		require.NoError(t, attempt.Open(f.th.Context))
		f.writeTo(t, attempt, "run-1", ir.Succeeded, dir)
		require.NoError(t, attempt.Close(f.th.Context))

		rec, err := artifact.ReadRecord(dir + artifactpath.MetaSuffix)
		require.NoError(t, err)
		assert.Equal(t, ir.Succeeded, rec.Status)
	})

	// The agent can turn a succeeded run into a failed one when uploading its
	// artifacts fails, on the same attempt.
	t.Run("ChangedStatusOnSameAttemptReplacesRecord", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		attempt := f.write(t, "run-1", ir.Succeeded, dir, false)
		require.NoError(t, attempt.Open(f.th.Context))
		f.writeTo(t, attempt, "run-1", ir.Failed, dir)
		require.NoError(t, attempt.Close(f.th.Context))

		rec, err := artifact.ReadRecord(dir + artifactpath.MetaSuffix)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, rec.Status)
	})

	t.Run("SkipsEmptyArtifactDir", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", false)

		f.write(t, "run-1", ir.Succeeded, dir, false)

		assert.NoFileExists(t, dir+artifactpath.MetaSuffix)
	})

	t.Run("SkipsLayoutsPredatingTheIndex", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		legacy := filepath.Join(f.artifactRoot, "index-dag", "dag-run_20260915_143207Z_run-1")
		require.NoError(t, os.MkdirAll(legacy, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "report.md"), []byte("ok"), 0o600))

		f.write(t, "run-1", ir.Succeeded, legacy, false)

		assert.NoFileExists(t, legacy+artifactpath.MetaSuffix)
	})

	// The terminal status of a run is persisted more than once.
	t.Run("RepeatedTerminalWriteIsStable", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		attempt := f.write(t, "run-1", ir.Succeeded, dir, false)
		first, err := os.ReadFile(dir + artifactpath.MetaSuffix)
		require.NoError(t, err)

		require.NoError(t, attempt.Open(f.th.Context))
		f.writeTo(t, attempt, "run-1", ir.Succeeded, dir)
		require.NoError(t, attempt.Close(f.th.Context))

		second, err := os.ReadFile(dir + artifactpath.MetaSuffix)
		require.NoError(t, err)
		assert.Equal(t, string(first), string(second))
	})

	// An auto-retried run reuses its directory, so the index must follow the
	// latest attempt rather than freeze on the first one's outcome.
	t.Run("LaterAttemptReplacesRecord", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		f.write(t, "run-1", ir.Failed, dir, false)
		f.write(t, "run-1", ir.Succeeded, dir, true)

		rec, err := artifact.ReadRecord(dir + artifactpath.MetaSuffix)
		require.NoError(t, err)
		assert.Equal(t, ir.Succeeded, rec.Status)
	})

	// artifacts.dir moves the files but not the index, so one date walk still
	// finds every run.
	t.Run("OverriddenDirIsIndexedInTheGlobalTree", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		override := filepath.Join(f.th.TmpDir, "elsewhere")
		dir := f.runDir(t, override, "run-1", true)

		f.write(t, "run-1", ir.Succeeded, dir, false)

		metaPath, ok := artifactpath.MetaPath(f.artifactRoot, dir)
		require.True(t, ok)
		assert.NoFileExists(t, dir+artifactpath.MetaSuffix)

		rec, err := artifact.ReadRecord(metaPath)
		require.NoError(t, err)
		assert.Equal(t, dir, rec.Dir)
	})
}

// Only root runs are indexed. A child's artifacts are reached through its
// root, and listing them on their own would name a root the viewer may not
// see.
func TestArtifactIndexSkipsChildRuns(t *testing.T) {
	t.Run("ChildIsNotIndexed", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		f.root = ir.NewDAGRunRef("parent-dag", "parent-run")
		dir := f.runDir(t, f.artifactRoot, "child-run", true)

		f.write(t, "child-run", ir.Succeeded, dir, false)

		assert.NoFileExists(t, dir+artifactpath.MetaSuffix)
	})

	t.Run("RootIsIndexed", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		f.write(t, "run-1", ir.Succeeded, dir, false)

		assert.FileExists(t, dir+artifactpath.MetaSuffix)
	})

	// A run whose writer set the root to itself is a root run.
	t.Run("SelfRootedRunIsIndexed", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		f.root = ir.NewDAGRunRef(f.dag.Name, "run-1")
		dir := f.runDir(t, f.artifactRoot, "run-1", true)

		f.write(t, "run-1", ir.Succeeded, dir, false)

		assert.FileExists(t, dir+artifactpath.MetaSuffix)
	})
}

func TestArtifactIndexRemoval(t *testing.T) {
	t.Run("RemovesRecordAndEmptiedDateDirs", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", true)
		f.write(t, "run-1", ir.Succeeded, dir, false)
		require.FileExists(t, dir+artifactpath.MetaSuffix)

		require.NoError(t, f.th.Repository.RemoveDAGRun(
			f.th.Context, ir.NewDAGRunRef(f.dag.Name, "run-1"), persis.DAGRunRemoveOptions{}))

		assert.NoDirExists(t, dir)
		assert.NoFileExists(t, dir+artifactpath.MetaSuffix)
		assert.NoDirExists(t, filepath.Join(f.artifactRoot, "2026"))
		assert.DirExists(t, f.artifactRoot)
	})

	// A run that wrote nothing has no index record, so pruning cannot rely on
	// the record's own ancestors to clean up the date directories.
	t.Run("PrunesDateDirsForUnindexedRun", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		dir := f.runDir(t, f.artifactRoot, "run-1", false)
		f.write(t, "run-1", ir.Succeeded, dir, false)
		require.NoFileExists(t, dir+artifactpath.MetaSuffix)

		require.NoError(t, f.th.Repository.RemoveDAGRun(
			f.th.Context, ir.NewDAGRunRef(f.dag.Name, "run-1"), persis.DAGRunRemoveOptions{}))

		assert.NoDirExists(t, dir)
		assert.NoDirExists(t, filepath.Join(f.artifactRoot, "2026"))
		assert.DirExists(t, f.artifactRoot)
	})

	// Artifacts relocated outside the trusted root are left alone, but their
	// index record lives in the root and must go, or the listing keeps
	// reporting a run that was deleted.
	t.Run("RemovesRecordForRelocatedArtifacts", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		override := filepath.Join(f.th.TmpDir, "elsewhere")
		dir := f.runDir(t, override, "run-1", true)
		f.write(t, "run-1", ir.Succeeded, dir, false)

		metaPath, ok := artifactpath.MetaPath(f.artifactRoot, dir)
		require.True(t, ok)
		require.FileExists(t, metaPath)

		require.NoError(t, f.th.Repository.RemoveDAGRun(
			f.th.Context, ir.NewDAGRunRef(f.dag.Name, "run-1"), persis.DAGRunRemoveOptions{}))

		assert.NoFileExists(t, metaPath)
	})

	t.Run("KeepsDateDirsThatStillHoldRuns", func(t *testing.T) {
		f := newArtifactIndexFixture(t)
		kept := f.runDir(t, f.artifactRoot, "run-keep", true)
		removed := f.runDir(t, f.artifactRoot, "run-remove", true)
		f.write(t, "run-keep", ir.Succeeded, kept, false)
		f.write(t, "run-remove", ir.Succeeded, removed, false)

		require.NoError(t, f.th.Repository.RemoveDAGRun(
			f.th.Context, ir.NewDAGRunRef(f.dag.Name, "run-remove"), persis.DAGRunRemoveOptions{}))

		assert.NoDirExists(t, removed)
		assert.DirExists(t, kept)
		assert.FileExists(t, kept+artifactpath.MetaSuffix)
	})
}
