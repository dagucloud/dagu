// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreWritesCurrentDAGRunFileCompatibilityLayout(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	store := NewStore(baseDir, WithArtifactDir(filepath.Join(baseDir, "artifacts")))
	repository := persis.NewDAGRunRepository(store, NewDAGRunWorkspaceStore(baseDir), persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	parentDAG := &ir.DAG{
		Name:     "compat-dag",
		Location: filepath.Join(baseDir, "compat-dag.yaml"),
	}
	parentTS := time.Date(2026, 5, 27, 1, 2, 3, 456_000_000, time.UTC)
	parentAttempt, err := repository.CreateAttempt(ctx, parentDAG, parentTS, "run-compat", persis.DAGRunCreateAttemptOptions{
		AttemptID: "attempt-compat",
	})
	require.NoError(t, err)
	rootRef := ir.NewDAGRunRef(parentDAG.Name, "run-compat")
	parentWorkDir, err := repository.MaterializeWorkspace(ctx, dagrun.DAGRunWorkspaceRef{DAGRun: rootRef})
	require.NoError(t, err)
	require.NoError(t, parentAttempt.Open(ctx))

	parentStatus := ir.InitialStatus(parentDAG)
	parentStatus.DAGRunID = "run-compat"
	parentStatus.AttemptID = parentAttempt.ID()
	parentStatus.Status = ir.Succeeded
	require.NoError(t, parentAttempt.Write(ctx, parentStatus))

	parentOutputs := &ir.DAGRunOutputs{
		Metadata: ir.OutputsMetadata{
			DAGName:     parentDAG.Name,
			DAGRunID:    parentStatus.DAGRunID,
			AttemptID:   parentStatus.AttemptID,
			Status:      parentStatus.Status.String(),
			CompletedAt: "2026-05-27T01:02:03Z",
		},
		Outputs: map[string]string{"step-one": "ok"},
	}
	require.NoError(t, parentAttempt.WriteOutputs(ctx, parentOutputs))
	require.NoError(t, parentAttempt.WriteStepMessages(ctx, "step-one", []ir.LLMMessage{
		{Role: ir.LLMRoleUser, Content: "hello"},
	}))
	require.NoError(t, parentAttempt.Close(ctx))

	childDAG := &ir.DAG{
		Name:     "child-dag",
		Location: filepath.Join(baseDir, "child-dag.yaml"),
	}
	childTS := time.Date(2026, 5, 27, 1, 2, 4, 789_000_000, time.UTC)
	childAttempt, err := repository.CreateAttempt(ctx, childDAG, childTS, "child-run", persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	childWorkDir, err := repository.MaterializeWorkspace(ctx, dagrun.DAGRunWorkspaceRef{
		RootDAGRun: rootRef,
		DAGRun:     ir.NewDAGRunRef(childDAG.Name, "child-run"),
	})
	require.NoError(t, err)
	require.NoError(t, childAttempt.Open(ctx))

	childStatus := ir.InitialStatus(childDAG)
	childStatus.Root = rootRef
	childStatus.DAGRunID = "child-run"
	childStatus.AttemptID = childAttempt.ID()
	childStatus.Status = ir.Succeeded
	require.NoError(t, childAttempt.Write(ctx, childStatus))
	require.NoError(t, childAttempt.Close(ctx))

	runDir := filepath.Join(baseDir, "compat-dag", "dag-runs", "2026", "05", "27", "dag-run_20260527_010203Z_run-compat")
	attemptDir := filepath.Join(runDir, "a_20260527_010203_456Z_attempt-compat")
	statusFile := filepath.Join(attemptDir, JSONLStatusFile)
	assert.Equal(t, statusFile, parentAttempt.(*Attempt).file)
	assert.Equal(t, filepath.Join(runDir, "work"), parentWorkDir)
	require.DirExists(t, runDir)
	require.DirExists(t, attemptDir)
	require.FileExists(t, statusFile)
	require.FileExists(t, filepath.Join(attemptDir, DAGDefinition))
	require.FileExists(t, filepath.Join(attemptDir, OutputsFile))
	require.DirExists(t, filepath.Join(runDir, "work"))
	require.FileExists(t, filepath.Join(runDir, MessagesDir, "step-one.json"))

	childAttemptDir := filepath.Join(runDir, SubDAGRunsDir, "child-run", "a_20260527_010204_789Z_child-attempt")
	require.DirExists(t, childAttemptDir)
	require.FileExists(t, filepath.Join(childAttemptDir, JSONLStatusFile))
	require.FileExists(t, filepath.Join(childAttemptDir, DAGDefinition))
	require.NoDirExists(t, filepath.Join(runDir, SubDAGRunsDir, "child-run", "work"))
	assert.Equal(t, filepath.Join(runDir, subDAGWorkDirName("child-run")), childWorkDir)
	require.DirExists(t, childWorkDir)

	assert.NoDirExists(t, filepath.Join(baseDir, "dag_runs"))
	assert.NoDirExists(t, filepath.Join(baseDir, "dagruns"))

	rawStatus, err := os.ReadFile(statusFile)
	require.NoError(t, err)
	assert.Contains(t, string(rawStatus), `"dagRunId"`)
	assert.Contains(t, string(rawStatus), `"attemptId"`)
	assert.NotContains(t, string(rawStatus), `"dag_run_id"`)
	assert.NotContains(t, string(rawStatus), `"attempt_id"`)

	var statusShape map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawStatus, &statusShape))
	assert.Contains(t, statusShape, "dagRunId")
	assert.Contains(t, statusShape, "attemptId")
	assert.Contains(t, statusShape, "status")

	foundParent, err := repository.FindAttempt(ctx, rootRef)
	require.NoError(t, err)
	foundStatus, err := foundParent.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, parentStatus.DAGRunID, foundStatus.DAGRunID)
	assert.Equal(t, parentStatus.AttemptID, foundStatus.AttemptID)

	foundOutputs, err := foundParent.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, foundOutputs)
	assert.Equal(t, parentOutputs, foundOutputs)

	foundMessages, err := foundParent.ReadStepMessages(ctx, "step-one")
	require.NoError(t, err)
	assert.Equal(t, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "hello"}}, foundMessages)

	foundChild, err := repository.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	foundChildStatus, err := foundChild.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, childStatus.DAGRunID, foundChildStatus.DAGRunID)
	assert.Equal(t, childStatus.AttemptID, foundChildStatus.AttemptID)
}

func TestStoreRetriesLegacySubDAGRunInSameDirectory(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	store := NewStore(baseDir, WithArtifactDir(filepath.Join(baseDir, "artifacts")))
	repository := persis.NewDAGRunRepository(store, NewDAGRunWorkspaceStore(baseDir), persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	parentDAG := &ir.DAG{
		Name:     "compat-dag",
		Location: filepath.Join(baseDir, "compat-dag.yaml"),
	}
	parentTS := time.Date(2026, 5, 27, 1, 2, 3, 456_000_000, time.UTC)
	_, err := repository.CreateAttempt(ctx, parentDAG, parentTS, "run-compat", persis.DAGRunCreateAttemptOptions{
		AttemptID: "attempt-compat",
	})
	require.NoError(t, err)

	runDir := filepath.Join(baseDir, "compat-dag", "dag-runs", "2026", "05", "27", "dag-run_20260527_010203Z_run-compat")
	legacyChildDir := filepath.Join(runDir, LegacySubDAGRunsDir, LegacySubDAGRunDirPrefix+"child-run")
	require.NoError(t, os.MkdirAll(legacyChildDir, 0750))

	rootRef := ir.NewDAGRunRef(parentDAG.Name, "run-compat")
	childDAG := &ir.DAG{
		Name:     "child-dag",
		Location: filepath.Join(baseDir, "child-dag.yaml"),
	}
	childTS := time.Date(2026, 5, 27, 1, 2, 4, 789_000_000, time.UTC)
	childAttempt, err := repository.CreateAttempt(ctx, childDAG, childTS, "child-run", persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
		Retry:      true,
		AttemptID:  "child-retry",
	})
	require.NoError(t, err)

	expectedAttemptDir := filepath.Join(legacyChildDir, "a_20260527_010204_789Z_child-retry")
	assert.Equal(t, filepath.Join(expectedAttemptDir, JSONLStatusFile), childAttempt.(*Attempt).file)
	require.DirExists(t, expectedAttemptDir)
	require.NoDirExists(t, filepath.Join(runDir, SubDAGRunsDir, "child-run"))
}

func TestRepository(t *testing.T) {
	t.Run("RecentStatuses", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create timestamps for the attempts
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		ts3 := time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC)

		// Create attempts with different statuses
		th.CreateAttempt(t, ts1, "dagrun-id-1", ir.Running)
		unreadable := th.CreateAttempt(t, ts2, "dagrun-id-2", ir.Failed)
		th.CreateAttempt(t, ts3, "dagrun-id-3", ir.Succeeded)

		// Request 2 most recent statuses
		statuses := th.Repository.RecentStatuses(th.Context, "test_DAG", 2)
		require.Len(t, statuses, 2)
		assert.Equal(t, "dagrun-id-3", statuses[0].DAGRunID)
		assert.Equal(t, "dagrun-id-2", statuses[1].DAGRunID)

		// Verify all attempts are returned if the number requested is equal to the number of attempts
		statuses = th.Repository.RecentStatuses(th.Context, "test_DAG", 3)
		require.Len(t, statuses, 3)

		// Verify all attempts are returned if the number requested is greater than the number of attempts
		statuses = th.Repository.RecentStatuses(th.Context, "test_DAG", 4)
		require.Len(t, statuses, 3)

		require.NoError(t, os.WriteFile(unreadable.file, []byte("{"), 0o600))
		statuses = th.Repository.RecentStatuses(th.Context, "test_DAG", 4)
		require.Len(t, statuses, 2)
		assert.Equal(t, []string{"dagrun-id-3", "dagrun-id-1"}, []string{
			statuses[0].DAGRunID,
			statuses[1].DAGRunID,
		})
	})
	t.Run("LatestAttempt", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create timestamps for the attempts
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		ts3 := time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC)

		// Create attempts with different statuses
		th.CreateAttempt(t, ts1, "dagrun-id-1", ir.Running)
		th.CreateAttempt(t, ts2, "dagrun-id-2", ir.Failed)
		th.CreateAttempt(t, ts3, "dagrun-id-3", ir.Succeeded)

		repository := persis.NewDAGRunRepository(th.Backend, nil, persis.DAGRunRepositoryOptions{
			LatestStatusToday: false,
			Location:          time.Local,
		})
		attempt, err := repository.LatestAttempt(th.Context, "test_DAG", persis.DAGRunLatestAttemptOptions{

			// Verify the attempt is the most recent
		})
		require.NoError(t, err)

		dagRunStatus, err := attempt.ReadStatus(th.Context)
		require.NoError(t, err)

		assert.Equal(t, "dagrun-id-3", dagRunStatus.DAGRunID)
	})
	t.Run("FindByDAGRunID", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create timestamps for the attempts
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		ts3 := time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC)

		// Create attempts with different statuses
		th.CreateAttempt(t, ts1, "dagrun-id-1", ir.Running)
		th.CreateAttempt(t, ts2, "dagrun-id-2", ir.Failed)
		th.CreateAttempt(t, ts3, "dagrun-id-3", ir.Succeeded)

		// Find the attempt with dag-run ID "dagrun-id-2"
		ref := ir.NewDAGRunRef("test_DAG", "dagrun-id-2")
		attempt, err := th.Repository.FindAttempt(th.Context, ref)
		require.NoError(t, err)

		// Verify the attempt is the correct one
		dagRunStatus, err := attempt.ReadStatus(th.Context)
		require.NoError(t, err)
		assert.Equal(t, "dagrun-id-2", dagRunStatus.DAGRunID)

		// Verify an error is returned if the dag-run ID does not exist
		refNonExist := ir.NewDAGRunRef("test_DAG", "nonexistent-id")
		_, err = th.Repository.FindAttempt(th.Context, refNonExist)
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})
	t.Run("RemoveOld", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create timestamps for the attempts
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		ts3 := time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC)

		// Create attempts with different statuses
		th.CreateAttempt(t, ts1, "dagrun-id-1", ir.Running)
		th.CreateAttempt(t, ts2, "dagrun-id-2", ir.Failed)
		th.CreateAttempt(t, ts3, "dagrun-id-3", ir.Succeeded)

		// Verify attempts are present
		statuses := th.Repository.RecentStatuses(th.Context, "test_DAG", 3)
		require.Len(t, statuses, 3)

		// Remove attempts older than 0 days
		// It should remove all attempts
		removedIDs, err := th.Repository.RemoveOldDAGRuns(th.Context, "test_DAG", 0, persis.DAGRunRetentionOptions{})
		require.NoError(t, err)
		assert.Len(t, removedIDs, 2) // 2 non-active runs should be removed

		// Verify non active attempts are removed
		statuses = th.Repository.RecentStatuses(th.Context, "test_DAG", 3)
		require.Len(t, statuses, 1)

		// Verify the remaining status is the active one
		assert.Equal(t, "dagrun-id-1", statuses[0].DAGRunID)
		assert.Equal(t, ir.Running, statuses[0].Status)
	})
	t.Run("RemoveOldWithOlderThanCutoff", func(t *testing.T) {
		th := setupTestRepository(t)

		tsOld := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		tsRecent := time.Date(2021, 1, 3, 12, 0, 0, 0, time.UTC)
		cutoff := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)

		oldAttempt := th.CreateAttempt(t, tsOld, "old-run", ir.Succeeded)
		recentAttempt := th.CreateAttempt(t, tsRecent, "recent-run", ir.Succeeded)
		// canRemoveDAGRun gates on status-file mtime as well as recorded run time.
		require.NoError(t, os.Chtimes(oldAttempt.file, tsOld, tsOld))
		require.NoError(t, os.Chtimes(recentAttempt.file, tsRecent, tsRecent))

		removedIDs, err := th.Repository.RemoveOldDAGRuns(
			th.Context,
			"test_DAG",
			30, // ignored when OlderThan is set
			persis.DAGRunRetentionOptions{OlderThan: &cutoff},
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"old-run"}, removedIDs)

		statuses := th.Repository.RecentStatuses(th.Context, "test_DAG", 3)
		require.Len(t, statuses, 1)
		assert.Equal(t, "recent-run", statuses[0].DAGRunID)
	})
	t.Run("RemoveOldWithZeroOlderThanCutoff", func(t *testing.T) {
		th := setupTestRepository(t)

		th.CreateAttempt(t, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), "completed-run", ir.Succeeded)

		zeroCutoff := time.Time{}
		removedIDs, err := th.Repository.RemoveOldDAGRuns(
			th.Context,
			"test_DAG",
			30,
			persis.DAGRunRetentionOptions{OlderThan: &zeroCutoff},
		)
		require.NoError(t, err)
		assert.Empty(t, removedIDs)

		statuses := th.Repository.RecentStatuses(th.Context, "test_DAG", 1)
		require.Len(t, statuses, 1)
		assert.Equal(t, "completed-run", statuses[0].DAGRunID)
	})
	t.Run("RemoveDAGRunRejectsActiveWhenRequested", func(t *testing.T) {
		th := setupTestRepository(t)
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ref := ir.NewDAGRunRef("test_DAG", "active-id")

		th.CreateAttempt(t, ts, ref.ID, ir.Running)

		err := th.Repository.RemoveDAGRun(th.Context, ref, persis.DAGRunRemoveOptions{RejectActive: true})
		require.ErrorIs(t, err, dagrun.ErrDAGRunActive)

		attempt, err := th.Repository.FindAttempt(th.Context, ref)
		require.NoError(t, err)
		status, err := attempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.Equal(t, ir.Running, status.Status)

		err = th.Repository.RemoveDAGRun(th.Context, ref, persis.DAGRunRemoveOptions{})
		require.NoError(t, err)

		_, err = th.Repository.FindAttempt(th.Context, ref)
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})
	t.Run("RemoveDAGRunRemovesArtifactDirsIncludingSubDAGRuns", func(t *testing.T) {
		th := setupTestRepository(t)
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

		artifactRoot := filepath.Join(th.TmpDir, "artifacts")
		parentArtifactDir := filepath.Join(artifactRoot, "parent-artifacts")
		subArtifactDir := filepath.Join(artifactRoot, "sub-artifacts")
		require.NoError(t, os.MkdirAll(parentArtifactDir, 0o750))
		require.NoError(t, os.MkdirAll(subArtifactDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(parentArtifactDir, "summary.md"), []byte("parent"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(subArtifactDir, "summary.md"), []byte("child"), 0o600))

		dag := th.DAG("test_DAG")
		parentAttempt, err := th.Repository.CreateAttempt(th.Context, dag.DAG, ts, "parent-id", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, parentAttempt.Open(th.Context))

		parentStatus := ir.InitialStatus(dag.DAG)
		parentStatus.DAGRunID = "parent-id"
		parentStatus.Status = ir.Succeeded
		parentStatus.ArchiveDir = parentArtifactDir
		require.NoError(t, parentAttempt.Write(th.Context, parentStatus))
		require.NoError(t, parentAttempt.Close(th.Context))

		rootRef := ir.NewDAGRunRef("test_DAG", "parent-id")
		subDAG := th.DAG("child")
		subAttempt, err := th.Repository.CreateAttempt(th.Context, subDAG.DAG, ts, "sub-id", persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootRef,
		})
		require.NoError(t, err)
		require.NoError(t, subAttempt.Open(th.Context))

		subStatus := ir.InitialStatus(subDAG.DAG)
		subStatus.DAGRunID = "sub-id"
		subStatus.Status = ir.Succeeded
		subStatus.ArchiveDir = subArtifactDir
		require.NoError(t, subAttempt.Write(th.Context, subStatus))
		require.NoError(t, subAttempt.Close(th.Context))

		require.DirExists(t, parentArtifactDir)
		require.DirExists(t, subArtifactDir)

		err = th.Repository.RemoveDAGRun(th.Context, rootRef, persis.DAGRunRemoveOptions{})
		require.NoError(t, err)

		assert.NoDirExists(t, parentArtifactDir)
		assert.NoDirExists(t, subArtifactDir)

		_, err = th.Repository.FindAttempt(th.Context, rootRef)
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})
	t.Run("RemoveDAGRunSkipsArtifactDirsOutsideTrustedRoot", func(t *testing.T) {
		th := setupTestRepository(t)
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

		outsideArtifactDir := filepath.Join(t.TempDir(), "outside-artifacts")
		require.NoError(t, os.MkdirAll(outsideArtifactDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(outsideArtifactDir, "summary.md"), []byte("outside"), 0o600))

		dag := th.DAG("test_DAG")
		attempt, err := th.Repository.CreateAttempt(th.Context, dag.DAG, ts, "outside-id", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(th.Context))

		status := ir.InitialStatus(dag.DAG)
		status.DAGRunID = "outside-id"
		status.Status = ir.Succeeded
		status.ArchiveDir = outsideArtifactDir
		require.NoError(t, attempt.Write(th.Context, status))
		require.NoError(t, attempt.Close(th.Context))

		rootRef := ir.NewDAGRunRef("test_DAG", "outside-id")
		require.DirExists(t, outsideArtifactDir)

		err = th.Repository.RemoveDAGRun(th.Context, rootRef, persis.DAGRunRemoveOptions{})
		require.NoError(t, err)

		require.DirExists(t, outsideArtifactDir)
		_, err = th.Repository.FindAttempt(th.Context, rootRef)
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})
	t.Run("SubDAGRun", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create a timestamp for the parent attempt
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

		// Create a parent attempt
		_ = th.CreateAttempt(t, ts, "parent-id", ir.Running)

		// Create a child attempt
		rootDAGRun := ir.NewDAGRunRef("test_DAG", "parent-id")
		subDAG := th.DAG("child")
		subAttempt, err := th.Repository.CreateAttempt(th.Context, subDAG.DAG, ts, "sub-id", persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootDAGRun,
		})
		require.NoError(t, err)

		// Write the status
		err = subAttempt.Open(th.Context)
		require.NoError(t, err)
		defer func() {
			_ = subAttempt.Close(th.Context)
		}()

		statusToWrite := ir.InitialStatus(subDAG.DAG)
		statusToWrite.DAGRunID = "sub-id"
		err = subAttempt.Write(th.Context, statusToWrite)
		require.NoError(t, err)

		// Verify attempt is created
		dagRunRef := ir.NewDAGRunRef("test_DAG", "parent-id")
		existingAttempt, err := th.Repository.FindSubAttempt(th.Context, dagRunRef, "sub-id")
		require.NoError(t, err)

		dagRunStatus, err := existingAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		assert.Equal(t, "sub-id", dagRunStatus.DAGRunID)
	})
	t.Run("SubDAGRunRetry", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create a timestamp for the parent attempt
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

		// Create a parent attempt
		_ = th.CreateAttempt(t, ts, "parent-id", ir.Running)

		// Create a sub dag-run
		const subDAGRunID = "sub-dagrun-id"
		const parentDAGRunID = "parent-id"

		rootDAGRun := ir.NewDAGRunRef("test_DAG", parentDAGRunID)
		subDAG := th.DAG("child")
		attempt, err := th.Repository.CreateAttempt(th.Context, subDAG.DAG, ts, subDAGRunID, persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootDAGRun,
		})
		require.NoError(t, err)

		// Write the status
		err = attempt.Open(th.Context)
		require.NoError(t, err)
		defer func() {
			_ = attempt.Close(th.Context)
		}()

		statusToWrite := ir.InitialStatus(subDAG.DAG)
		statusToWrite.DAGRunID = subDAGRunID
		statusToWrite.Status = ir.Running
		err = attempt.Write(th.Context, statusToWrite)
		require.NoError(t, err)

		// Find the sub dag-run attempt
		ts = time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		dagRunRef := ir.NewDAGRunRef("test_DAG", parentDAGRunID)
		existingAttempt, err := th.Repository.FindSubAttempt(th.Context, dagRunRef, subDAGRunID)
		require.NoError(t, err)
		existingAttemptStatus, err := existingAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		assert.Equal(t, subDAGRunID, existingAttemptStatus.DAGRunID)
		assert.Equal(t, ir.Running.String(), existingAttemptStatus.Status.String())

		// Create a retry attempt and write different status
		retryAttempt, err := th.Repository.CreateAttempt(th.Context, subDAG.DAG, ts, subDAGRunID, persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootDAGRun,
			Retry:      true,
		})
		require.NoError(t, err)
		statusToWrite.Status = ir.Succeeded
		_ = retryAttempt.Open(th.Context)
		_ = retryAttempt.Write(th.Context, statusToWrite)
		_ = retryAttempt.Close(th.Context)

		// Verify the retry attempt is created
		existingAttempt, err = th.Repository.FindSubAttempt(th.Context, dagRunRef, subDAGRunID)
		require.NoError(t, err)
		existingAttemptStatus, err = existingAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		assert.Equal(t, subDAGRunID, existingAttemptStatus.DAGRunID)
		assert.Equal(t, ir.Succeeded.String(), existingAttemptStatus.Status.String())
	})
	t.Run("CreateChildAttempt", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create a parent attempt first
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, ts, "parent-id", ir.Running)

		rootRef := ir.NewDAGRunRef("test_DAG", "parent-id")
		subDAG := th.DAG("child")
		subAttempt, err := th.Repository.CreateAttempt(th.Context, subDAG.DAG, ts, "sub-id", persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootRef,
		})
		require.NoError(t, err)

		// Write status to the sub-attempt
		err = subAttempt.Open(th.Context)
		require.NoError(t, err)
		defer func() { _ = subAttempt.Close(th.Context) }()

		statusToWrite := ir.InitialStatus(subDAG.DAG)
		statusToWrite.DAGRunID = "sub-id"
		err = subAttempt.Write(th.Context, statusToWrite)
		require.NoError(t, err)

		// Verify sub-attempt can be found
		foundAttempt, err := th.Repository.FindSubAttempt(th.Context, rootRef, "sub-id")
		require.NoError(t, err)

		status, err := foundAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		assert.Equal(t, "sub-id", status.DAGRunID)
	})
	t.Run("CompareAndSwapLatestAttemptStatusUpdatesSubAttempt", func(t *testing.T) {
		th := setupTestRepository(t)

		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, ts, "parent-id", ir.Running)

		rootRef := ir.NewDAGRunRef("test_DAG", "parent-id")
		subRef := ir.NewDAGRunRef("child", "parent-id")
		subDAG := th.DAG(subRef.Name)
		subAttempt, err := th.Repository.CreateAttempt(th.Context, subDAG.DAG, ts, subRef.ID, persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootRef,
		})
		require.NoError(t, err)

		require.NoError(t, subAttempt.Open(th.Context))
		statusToWrite := ir.InitialStatus(subDAG.DAG)
		statusToWrite.DAGRunID = subRef.ID
		statusToWrite.Root = rootRef
		statusToWrite.AttemptID = subAttempt.ID()
		statusToWrite.AttemptKey = ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, subRef.Name, subRef.ID, subAttempt.ID())
		statusToWrite.Status = ir.Running
		statusToWrite.Nodes = []*ir.Node{{Status: ir.NodeRunning}}
		require.NoError(t, subAttempt.Write(th.Context, statusToWrite))
		require.NoError(t, subAttempt.Close(th.Context))

		updated, swapped, err := th.Repository.CompareAndSwapLatestAttemptStatus(
			th.Context,
			subRef,
			subAttempt.ID(),
			ir.Running,
			func(status *ir.DAGRunStatus) error {
				status.Status = ir.Failed
				status.Error = "lease expired"
				status.Nodes[0].Status = ir.NodeFailed
				return nil
			}, persis.DAGRunCompareAndSwapOptions{RootDAGRun: rootRef, ExpectedAttemptKey: statusToWrite.AttemptKey},
		)
		require.NoError(t, err)
		require.True(t, swapped)
		require.Equal(t, ir.Failed, updated.Status)

		foundAttempt, err := th.Repository.FindSubAttempt(th.Context, rootRef, subRef.ID)
		require.NoError(t, err)
		foundStatus, err := foundAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, foundStatus.Status)
		require.Equal(t, "lease expired", foundStatus.Error)
		require.Equal(t, ir.NodeFailed, foundStatus.Nodes[0].Status)
	})
	t.Run("CreateChildAttemptEmptyRootID", func(t *testing.T) {
		th := setupTestRepository(t)

		rootRef := ir.NewDAGRunRef("test_DAG", "")
		_, err := th.Repository.CreateAttempt(th.Context, th.DAG("child").DAG, time.Now(), "sub-id", persis.DAGRunCreateAttemptOptions{
			RootDAGRun: rootRef,
		})
		require.ErrorIs(t, err, dagrun.ErrDAGRunIDEmpty)
	})
	t.Run("ReadDAG", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create a timestamp for the parent attempt
		ts := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)

		// Create a parent attempt
		rec := th.CreateAttempt(t, ts, "parent-id", ir.Running)

		// Write the status
		err := rec.Open(th.Context)
		require.NoError(t, err)
		defer func() {
			_ = rec.Close(th.Context)
		}()

		statusToWrite := ir.InitialStatus(rec.dag)
		statusToWrite.DAGRunID = "parent-id"

		err = rec.Write(th.Context, statusToWrite)
		require.NoError(t, err)

		// Read the DAG and verify it matches the original
		dag, err := rec.ReadDAG(th.Context)
		require.NoError(t, err)

		require.NotNil(t, dag)
		// Compare key fields instead of full struct (DAG contains sync.Once which cannot be copied)
		require.Equal(t, rec.dag.Name, dag.Name)
		require.Equal(t, rec.dag.Location, dag.Location)
	})
}

func TestListRoot(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	// Create test directories
	testDirs := []string{
		"dag1",
		"dag2",
		"dag3",
	}

	for _, dir := range testDirs {
		dirPath := filepath.Join(tmpDir, dir)
		err := os.MkdirAll(dirPath, 0750)
		require.NoError(t, err, "Failed to create test directory")
	}

	// Create a file (should be ignored by listRoot)
	filePath := filepath.Join(tmpDir, "not-a-dir.txt")
	err := os.WriteFile(filePath, []byte("test"), 0600)
	require.NoError(t, err, "Failed to create test file")

	// Create localStore instance
	store := &Store{baseDir: tmpDir}

	// Call listRoot
	ctx := context.Background()
	roots, err := store.listRoot(ctx, "")
	require.NoError(t, err, "listRoot should not return an error")

	// Verify results
	assert.Len(t, roots, len(testDirs), "listRoot should return the correct number of directories")

	// Verify each directory is in the results
	foundDirs := make(map[string]bool)
	for _, root := range roots {
		foundDirs[root.prefix] = true
	}

	for _, dir := range testDirs {
		assert.True(t, foundDirs[dir], "listRoot should include directory %s", dir)
	}
}

// TestListRootExactMatch verifies that listRoot does exact matching, not substring matching.
// Regression test for issue #1473.
func TestListRootExactMatch(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "go"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "go_fasthttp"), 0750))

	store := &Store{baseDir: tmpDir}
	roots, err := store.listRoot(context.Background(), "go")
	require.NoError(t, err)
	require.Len(t, roots, 1, "should only match 'go', not 'go_fasthttp'")
	assert.Equal(t, "go", roots[0].prefix)
}

func TestListRootEmptyDirectory(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	// Create localStore instance
	store := &Store{baseDir: tmpDir}

	// Call listRoot
	ctx := context.Background()
	roots, err := store.listRoot(ctx, "")
	require.NoError(t, err, "listRoot should not return an error")

	// Verify results
	assert.Len(t, roots, 0, "listRoot should return an empty slice for an empty directory")
}

func TestListRootNonExistentDirectory(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	nonExistentDir := filepath.Join(tmpDir, "non-existent")

	// Create localStore instance
	store := &Store{baseDir: nonExistentDir}

	// Call listRoot
	ctx := context.Background()
	roots, err := store.listRoot(ctx, "")
	require.NoError(t, err, "listRoot should not return an error for non-existent directory")

	// Verify results
	assert.Len(t, roots, 0, "listRoot should return an empty slice for a non-existent directory")
}

func TestListRootCanceledContext(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	// Create localStore instance
	store := &Store{baseDir: tmpDir}

	// Create a canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	// Call listRoot with canceled context
	roots, err := store.listRoot(ctx, "")

	// The function doesn't check for context cancellation, so it should still succeed
	require.NoError(t, err, "listRoot should not return an error for canceled context")
	assert.Len(t, roots, 0, "listRoot should return an empty slice for an empty directory")
}

func TestListStatuses(t *testing.T) {
	t.Run("FilterByTimeRange", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create attempts with different timestamps
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		ts3 := time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC)

		th.CreateAttempt(t, ts1, "dagrun-id-1", ir.Succeeded)
		th.CreateAttempt(t, ts2, "dagrun-id-2", ir.Succeeded)
		th.CreateAttempt(t, ts3, "dagrun-id-3", ir.Succeeded)

		// Filter by time range (only ts2 should be included)
		from := persis.NewUTC(time.Date(2021, 1, 1, 12, 0, 0, 0, time.UTC))
		to := persis.NewUTC(time.Date(2021, 1, 2, 12, 0, 0, 0, time.UTC))

		statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{From: from, To: to})

		require.NoError(t, err)
		require.Len(t, statuses, 1)
		assert.Equal(t, "dagrun-id-2", statuses[0].DAGRunID)
	})

	t.Run("FilterByStatus", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create attempts with different statuses
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, ts, "dagrun-id-1", ir.Running)
		th.CreateAttempt(t, ts, "dagrun-id-2", ir.Failed)
		th.CreateAttempt(t, ts, "dagrun-id-3", ir.Succeeded)

		// Filter by status (only StatusError should be included)
		statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Statuses: []ir.Status{ir.Failed}, From: persis.NewUTC(ts)})

		require.NoError(t, err)
		require.Len(t, statuses, 1)
		assert.Equal(t, "dagrun-id-2", statuses[0].DAGRunID)
		assert.Equal(t, ir.Failed, statuses[0].Status)
	})

	t.Run("LimitResults", func(t *testing.T) {
		th := setupTestRepository(t)

		// Create multiple attempts
		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 1; i <= 5; i++ {
			th.CreateAttempt(t, ts, fmt.Sprintf("dagrun-id-%d", i), ir.Succeeded)
		}

		// Limit to 3 results
		statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Limit: 3, From: persis.NewUTC(ts)})

		require.NoError(t, err)
		require.Len(t, statuses, 3)
	})

	t.Run("SortByCreatedAt", func(t *testing.T) {
		th := setupTestRepository(t)

		// Use different timestamps to ensure deterministic sort order
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 1, 0, 0, 1, 0, time.UTC) // 1 second later
		ts3 := time.Date(2021, 1, 1, 0, 0, 2, 0, time.UTC) // 2 seconds later

		th.CreateAttempt(t, ts1, "dagrun-id-1", ir.Succeeded)
		th.CreateAttempt(t, ts2, "dagrun-id-2", ir.Succeeded)
		th.CreateAttempt(t, ts3, "dagrun-id-3", ir.Succeeded)

		// Get all statuses
		statuses, err := th.Repository.ListStatuses(
			th.Context, persis.DAGRunListOptions{From: persis.NewUTC(ts1)},
		)

		require.NoError(t, err)
		require.Len(t, statuses, 3)

		// Verify they are sorted by StartedAt in descending order (newest first)
		assert.Equal(t, "dagrun-id-3", statuses[0].DAGRunID)
		assert.Equal(t, "dagrun-id-2", statuses[1].DAGRunID)
		assert.Equal(t, "dagrun-id-1", statuses[2].DAGRunID)
	})

	t.Run("FilterByLabels", func(t *testing.T) {
		th := setupTestRepository(t)

		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

		// Create runs with different labels
		run1 := th.DAG("dag1")
		run1.Labels = ir.NewLabels([]string{"prod", "batch"})
		th.CreateAttemptWithDAG(t, ts, "run-1", ir.Succeeded, run1.DAG)

		run2 := th.DAG("dag2")
		run2.Labels = ir.NewLabels([]string{"prod", "api"})
		th.CreateAttemptWithDAG(t, ts, "run-2", ir.Succeeded, run2.DAG)

		run3 := th.DAG("dag3")
		run3.Labels = ir.NewLabels([]string{"dev"})
		th.CreateAttemptWithDAG(t, ts, "run-3", ir.Succeeded, run3.DAG)

		// Filter by label "prod" (should match run-1 and run-2)
		statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Labels: []string{"prod"}, From: persis.NewUTC(ts)})
		require.NoError(t, err)
		assert.Len(t, statuses, 2)

		// Filter by labels "prod" AND "batch" (should match only run-1)
		statuses, err = th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Labels: []string{"prod", "batch"}, From: persis.NewUTC(ts)})
		require.NoError(t, err)
		assert.Len(t, statuses, 1)
		assert.Equal(t, "run-1", statuses[0].DAGRunID)

		// Filter by label "dev" (should match only run-3)
		statuses, err = th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Labels: []string{"dev"}, From: persis.NewUTC(ts)})
		require.NoError(t, err)
		assert.Len(t, statuses, 1)
		assert.Equal(t, "run-3", statuses[0].DAGRunID)

		// Filter by label "nonexistent" (should match nothing)
		statuses, err = th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Labels: []string{"nonexistent"}, From: persis.NewUTC(ts)})
		require.NoError(t, err)
		assert.Empty(t, statuses)
	})

	t.Run("IncludesAutoRetryLimit", func(t *testing.T) {
		th := setupTestRepository(t)

		ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		dag := th.DAG("retry_dag")
		dag.RetryPolicy = &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     2.0,
			MaxInterval: 10 * time.Minute,
		}

		th.CreateAttemptWithDAG(t, ts, "retry-run", ir.Failed, dag.DAG)

		statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{From: persis.NewUTC(ts)})
		require.NoError(t, err)
		require.Len(t, statuses, 1)
		assert.Equal(t, 3, statuses[0].AutoRetryLimit)
		assert.Equal(t, dag.ProcGroup(), statuses[0].ProcGroup)
	})
}

func TestLatestStatusTimezone(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)

	now := time.Date(2025, 6, 8, 10, 0, 0, 0, time.UTC)
	backend := NewStore(t.TempDir())
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		LatestStatusToday: true,
		Location:          paris,
		Now:               func() time.Time { return now },
	})
	th := RepositoryTest{
		Context:    context.Background(),
		Repository: repository,
		Backend:    backend,
		TmpDir:     backend.baseDir,
	}

	startOfDay := time.Date(2025, 6, 8, 0, 0, 0, 0, paris)
	th.CreateAttempt(t, startOfDay, "midnight-run", ir.Succeeded)

	attempt, err := repository.LatestAttempt(th.Context, "test_DAG", persis.DAGRunLatestAttemptOptions{})
	require.NoError(t, err)
	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	assert.Equal(t, "midnight-run", status.DAGRunID)
}

func TestListStatuses_RemainingCountWithFilters(t *testing.T) {
	th := setupTestRepository(t)

	ts := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create 10 runs: 5 succeeded, 5 failed.
	for i := range 5 {
		th.CreateAttempt(t, ts.Add(time.Duration(i)*time.Second), fmt.Sprintf("success-%d", i), ir.Succeeded)
	}
	for i := range 5 {
		th.CreateAttempt(t, ts.Add(time.Duration(i+5)*time.Second), fmt.Sprintf("failed-%d", i), ir.Failed)
	}

	// Filter by Succeeded status with limit 10.
	// Before the fix, len(dagRuns) would consume the budget even for filtered-out runs,
	// potentially returning fewer results than expected.
	statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Statuses: []ir.Status{ir.Succeeded}, From: persis.NewUTC(ts), Limit: 10})
	require.NoError(t, err)
	// Should return all 5 succeeded runs, not fewer.
	assert.Len(t, statuses, 5)

	for _, s := range statuses {
		assert.Equal(t, ir.Succeeded, s.Status)
	}
}

func TestFormatUnixToRFC3339(t *testing.T) {
	assert.Equal(t, "", formatUnixToRFC3339(0))
	assert.Equal(t, "2024-01-15T12:00:00Z", formatUnixToRFC3339(1705320000))
}

func TestResolveStatus_FastPath(t *testing.T) {
	store := &Store{}
	ctx := context.Background()

	dagRun := &DAGRun{
		dagRunID: "test-run",
		summary: &DAGRunSummary{
			Name:           "test-dag",
			DagRunID:       "test-run",
			Status:         ir.Succeeded,
			StartedAtUnix:  1705320000,
			FinishedAtUnix: 1705320060,
			Labels:         []string{"env=prod"},
			WorkerID:       "worker-1",
			Params:         "key=val",
			QueuedAt:       "2024-01-15T12:00:00Z",
			ScheduleTime:   "2024-01-15T11:55:00Z",
			TriggerType:    ir.TriggerType(1),
			CreatedAt:      1705320000000,
			LeaseAt:        1705320030000,
		},
	}

	status := store.resolveStatus(ctx, dagRun, nil, nil, nil, false)
	require.NotNil(t, status)
	assert.Equal(t, "test-dag", status.Name)
	assert.Equal(t, "test-run", status.DAGRunID)
	assert.Equal(t, ir.Succeeded, status.Status)
	assert.Equal(t, []string{"env=prod"}, status.Labels)
	assert.Equal(t, "2024-01-15T12:00:00Z", status.StartedAt)
	assert.Equal(t, "2024-01-15T12:01:00Z", status.FinishedAt)
	assert.Equal(t, "worker-1", status.WorkerID)
	assert.Equal(t, "key=val", status.Params)
	assert.Equal(t, "2024-01-15T12:00:00Z", status.QueuedAt)
	assert.Equal(t, "2024-01-15T11:55:00Z", status.ScheduleTime)
	assert.Equal(t, int64(1705320000000), status.CreatedAt)
	assert.Equal(t, int64(1705320030000), status.LeaseAt)
}

func TestResolveStatus_FastPath_StatusFilterReject(t *testing.T) {
	store := &Store{}
	ctx := context.Background()

	dagRun := &DAGRun{
		summary: &DAGRunSummary{
			Status: ir.Succeeded,
		},
	}

	// Filter only for Failed — should reject Succeeded.
	statusesFilter := map[ir.Status]struct{}{ir.Failed: {}}
	status := store.resolveStatus(ctx, dagRun, nil, nil, statusesFilter, true)
	assert.Nil(t, status)
}

func TestResolveStatus_FastPath_LabelFilterReject(t *testing.T) {
	store := &Store{}
	ctx := context.Background()

	dagRun := &DAGRun{
		summary: &DAGRunSummary{
			Status: ir.Succeeded,
			Labels: []string{"env=dev"},
		},
	}

	labelFilters := []ir.LabelFilter{ir.ParseLabelFilter("env=prod")}
	status := store.resolveStatus(ctx, dagRun, labelFilters, nil, nil, false)
	assert.Nil(t, status)
}

func TestResolveStatus_StandardPath(t *testing.T) {
	th := setupTestRepository(t)

	ts := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	dag := th.DAG("std-path-dag")
	dag.Labels = ir.NewLabels([]string{"env=prod"})
	th.CreateAttemptWithDAG(t, ts, "std-run-1", ir.Succeeded, dag.DAG)

	store := th.Backend
	ctx := context.Background()

	root := NewDataRoot(th.TmpDir, "std-path-dag")
	start := persis.NewUTC(ts)
	end := persis.NewUTC(ts.Add(24 * time.Hour))
	dagRuns := root.listDAGRunsInRange(ctx, start, end, nil)
	require.NotEmpty(t, dagRuns)

	// Standard path (no summary) with matching label filter.
	labelFilters := []ir.LabelFilter{ir.ParseLabelFilter("env=prod")}
	status := store.resolveStatus(ctx, dagRuns[0], labelFilters, nil, nil, false)
	require.NotNil(t, status, "should resolve status via standard path with matching label")

	// Standard path with non-matching label filter.
	labelFilters = []ir.LabelFilter{ir.ParseLabelFilter("env=staging")}
	status = store.resolveStatus(ctx, dagRuns[0], labelFilters, nil, nil, false)
	assert.Nil(t, status, "should reject via standard path when label doesn't match")

	// Standard path with matching status filter.
	statusFilter := map[ir.Status]struct{}{ir.Succeeded: {}}
	status = store.resolveStatus(ctx, dagRuns[0], nil, nil, statusFilter, true)
	require.NotNil(t, status, "should resolve via standard path with matching status")

	// Standard path with non-matching status filter.
	statusFilter = map[ir.Status]struct{}{ir.Failed: {}}
	status = store.resolveStatus(ctx, dagRuns[0], nil, nil, statusFilter, true)
	assert.Nil(t, status, "should reject via standard path when status doesn't match")
}

func TestResolveStatus_StandardPath_NoAttempt(t *testing.T) {
	store := &Store{}
	ctx := context.Background()

	// Create a DAGRun directory with correct naming but no attempt files.
	dir := t.TempDir()
	dagRunDir := filepath.Join(dir, "dag-run_20240115_120000Z_no-attempt")
	require.NoError(t, os.MkdirAll(dagRunDir, 0750))

	dagRun, err := NewDAGRun(dagRunDir)
	require.NoError(t, err)

	status := store.resolveStatus(ctx, dagRun, nil, nil, nil, false)
	assert.Nil(t, status, "should return nil when no attempts exist")
}

func TestListStatuses_WithAllHistoryBypassesDefaultTodayWindow(t *testing.T) {
	th := setupTestRepository(t)

	oldTs := time.Now().UTC().Add(-48 * time.Hour)
	th.CreateAttempt(t, oldTs, "old-run", ir.Running)

	statuses, err := th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Statuses: []ir.Status{ir.Running}, Unbounded: true})
	require.NoError(t, err)
	require.Empty(t, statuses)

	statuses, err = th.Repository.ListStatuses(th.Context, persis.DAGRunListOptions{Statuses: []ir.Status{ir.Running}, Unbounded: true, AllHistory: true})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "old-run", statuses[0].DAGRunID)
}

func TestListStatusesPage(t *testing.T) {
	t.Run("IndexPathPreservesRunSummary", func(t *testing.T) {
		th := setupTestRepository(t)

		base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		dag := th.DAG("artifact-dag")
		artifactDir := filepath.Join(th.TmpDir, "artifacts", "artifact-dag", "artifact-run")

		attempt, err := th.Repository.CreateAttempt(th.Context, dag.DAG, base, "artifact-run", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(th.Context))
		defer func() {
			require.NoError(t, attempt.Close(th.Context))
		}()

		status := ir.InitialStatus(dag.DAG)
		status.DAGRunID = "artifact-run"
		status.Status = ir.Succeeded
		status.ArchiveDir = artifactDir
		status.TriggerActor = "alice"
		require.NoError(t, attempt.Write(th.Context, status))

		for i := range 9 {
			th.CreateAttemptWithDAG(t, base.Add(time.Duration(i+1)*time.Second), fmt.Sprintf("filler-run-%d", i), ir.Succeeded, dag.DAG)
		}

		_, err = th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, DAGRunID: "artifact-run", Limit: 20},
		)
		require.NoError(t, err)

		page, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, DAGRunID: "artifact-run", Limit: 20},
		)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.Equal(t, artifactDir, page.Items[0].ArchiveDir)
		assert.Equal(t, "alice", page.Items[0].TriggerActor)
	})

	t.Run("ForwardPaginationHasDeterministicOrderWithoutDuplicates", func(t *testing.T) {
		th := setupTestRepository(t)

		base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		alpha := th.DAG("alpha")
		beta := th.DAG("beta")

		th.CreateAttemptWithDAG(t, base.Add(3*time.Second), "run-4", ir.Succeeded, beta.DAG)
		th.CreateAttemptWithDAG(t, base.Add(2*time.Second), "run-3", ir.Succeeded, alpha.DAG)
		th.CreateAttemptWithDAG(t, base.Add(1*time.Second), "run-2", ir.Succeeded, beta.DAG)
		th.CreateAttemptWithDAG(t, base.Add(1*time.Second), "run-1", ir.Succeeded, alpha.DAG)
		th.CreateAttemptWithDAG(t, base, "run-0", ir.Succeeded, alpha.DAG)

		page1, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Limit: 2},
		)
		require.NoError(t, err)
		require.Len(t, page1.Items, 2)
		require.NotEmpty(t, page1.NextCursor)
		assert.Equal(t, []string{"beta/run-4", "alpha/run-3"}, []string{
			page1.Items[0].Name + "/" + page1.Items[0].DAGRunID,
			page1.Items[1].Name + "/" + page1.Items[1].DAGRunID,
		})

		page2, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Limit: 2, Cursor: page1.NextCursor},
		)
		require.NoError(t, err)
		require.Len(t, page2.Items, 2)
		require.NotEmpty(t, page2.NextCursor)
		assert.Equal(t, []string{"alpha/run-1", "beta/run-2"}, []string{
			page2.Items[0].Name + "/" + page2.Items[0].DAGRunID,
			page2.Items[1].Name + "/" + page2.Items[1].DAGRunID,
		})

		page3, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Limit: 2, Cursor: page2.NextCursor},
		)
		require.NoError(t, err)
		require.Len(t, page3.Items, 1)
		assert.Empty(t, page3.NextCursor)
		assert.Equal(t, "run-0", page3.Items[0].DAGRunID)

		seen := make(map[string]struct{})
		for _, page := range [][]*ir.DAGRunStatus{page1.Items, page2.Items, page3.Items} {
			for _, item := range page {
				key := item.Name + "/" + item.DAGRunID
				if _, ok := seen[key]; ok {
					t.Fatalf("duplicate DAG run in paged results: %s", key)
				}
				seen[key] = struct{}{}
			}
		}
	})

	t.Run("CursorRejectsChangedFilters", func(t *testing.T) {
		th := setupTestRepository(t)

		ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, ts, "run-1", ir.Succeeded)
		th.CreateAttempt(t, ts.Add(-time.Second), "run-0", ir.Succeeded)

		page, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Statuses: []ir.Status{ir.Succeeded}, Limit: 1},
		)
		require.NoError(t, err)
		require.NotEmpty(t, page.NextCursor)

		_, err = th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Statuses: []ir.Status{ir.Failed}, Limit: 1, Cursor: page.NextCursor},
		)
		require.ErrorIs(t, err, persis.ErrInvalidDAGRunQueryCursor)
	})

	t.Run("CursorRejectsVersionTwo", func(t *testing.T) {
		th := setupTestRepository(t)
		payload, err := json.Marshal(queryCursorPayload{
			Version:    2,
			FilterHash: "legacy",
			Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
			Name:       "daily",
			DAGRunID:   "run-1",
		})
		require.NoError(t, err)

		_, err = th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Cursor: base64.RawURLEncoding.EncodeToString(payload)},
		)
		require.ErrorIs(t, err, persis.ErrInvalidDAGRunQueryCursor)
	})

	t.Run("CursorTracksWorkspaceVisibility", func(t *testing.T) {
		th := setupTestRepository(t)

		ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, ts, "run-1", ir.Succeeded)
		th.CreateAttempt(t, ts.Add(-time.Second), "run-0", ir.Succeeded)

		page, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, WorkspaceFilter: &workspace.WorkspaceFilter{
				Enabled:           true,
				Workspaces:        []string{"ops", "dev"},
				IncludeUnlabelled: true,
			}, Limit: 1},
		)
		require.NoError(t, err)
		require.NotEmpty(t, page.NextCursor)

		continued, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, WorkspaceFilter: &workspace.WorkspaceFilter{
				Enabled:           true,
				Workspaces:        []string{"dev", "ops"},
				IncludeUnlabelled: true,
			}, Limit: 1, Cursor: page.NextCursor},
		)
		require.NoError(t, err)
		require.Len(t, continued.Items, 1)

		_, err = th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, WorkspaceFilter: &workspace.WorkspaceFilter{
				Enabled:    true,
				Workspaces: []string{"dev", "ops"},
			}, Limit: 1, Cursor: page.NextCursor},
		)
		require.ErrorIs(t, err, persis.ErrInvalidDAGRunQueryCursor)
	})

	t.Run("CursorTreatsDisabledWorkspaceFilterAsUnrestricted", func(t *testing.T) {
		th := setupTestRepository(t)

		ts := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, ts, "run-1", ir.Succeeded)
		th.CreateAttempt(t, ts.Add(-time.Second), "run-0", ir.Succeeded)

		page, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Limit: 1},
		)
		require.NoError(t, err)
		require.NotEmpty(t, page.NextCursor)

		continued, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, WorkspaceFilter: &workspace.WorkspaceFilter{
				Workspaces:        []string{"ignored"},
				IncludeUnlabelled: true,
			}, Limit: 1, Cursor: page.NextCursor},
		)
		require.NoError(t, err)
		require.Len(t, continued.Items, 1)
	})

	t.Run("NewerRunsAfterPageOneDoNotCorruptContinuation", func(t *testing.T) {
		th := setupTestRepository(t)

		base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		th.CreateAttempt(t, base.Add(2*time.Second), "run-2", ir.Succeeded)
		th.CreateAttempt(t, base.Add(1*time.Second), "run-1", ir.Succeeded)
		th.CreateAttempt(t, base, "run-0", ir.Succeeded)

		page1, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Limit: 2},
		)
		require.NoError(t, err)
		require.Len(t, page1.Items, 2)
		require.NotEmpty(t, page1.NextCursor)

		th.CreateAttempt(t, base.Add(3*time.Second), "run-3", ir.Succeeded)

		page2, err := th.Repository.ListStatusesPage(
			th.Context, persis.DAGRunListOptions{AllHistory: true, Limit: 2, Cursor: page1.NextCursor},
		)
		require.NoError(t, err)
		require.Len(t, page2.Items, 1)
		assert.Equal(t, "run-0", page2.Items[0].DAGRunID)
	})
}
