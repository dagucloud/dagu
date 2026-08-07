// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package materialization

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/incremental"
	"github.com/stretchr/testify/require"
)

func TestAcquirePathsAllowsReadersAndExcludesWriter(t *testing.T) {
	t.Parallel()

	store := New(t.TempDir())
	request := []exec.PathLockRequest{{Key: "/data/input.txt", Mode: exec.PathLockShared}}
	first, err := store.AcquirePaths(context.Background(), request)
	require.NoError(t, err)
	second, err := store.AcquirePaths(context.Background(), request)
	require.NoError(t, err)

	blockedCtx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_, err = store.AcquirePaths(blockedCtx, []exec.PathLockRequest{{
		Key:  "/data/input.txt",
		Mode: exec.PathLockExclusive,
	}})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, second.Release())
	require.NoError(t, first.Release())
	writer, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key:  "/data/input.txt",
		Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	require.NoError(t, writer.Release())
}

func TestAcquirePathsRejectsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(t.TempDir()).AcquirePaths(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCommitPublishesOutputAndManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, "output.txt")
	stagingPath := filepath.Join(dir, ".output.tmp")
	require.NoError(t, os.WriteFile(stagingPath, []byte("new"), fileMode))
	manifest := testManifest(t, "materialization", "commit", stagingPath, finalPath)
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(finalPath), Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	require.NoError(t, store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath: stagingPath,
		FinalPath:   finalPath,
		Manifest:    manifest,
	}))
	require.NoFileExists(t, stagingPath)
	require.FileExists(t, finalPath)
	content, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	require.Equal(t, "new", string(content))
	stored, err := store.Get(context.Background(), manifest.MaterializationKey)
	require.NoError(t, err)
	require.Equal(t, manifest.CommitID, stored.CommitID)
}

func TestCommitCanPublishWithoutReplacingManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, "output.txt")
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(finalPath), Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	firstStage := filepath.Join(dir, ".first.tmp")
	require.NoError(t, os.WriteFile(firstStage, []byte("first"), fileMode))
	firstManifest := testManifest(t, "materialization", "commit-1", firstStage, finalPath)
	require.NoError(t, store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath: firstStage,
		FinalPath:   finalPath,
		Manifest:    firstManifest,
	}))

	secondStage := filepath.Join(dir, ".second.tmp")
	require.NoError(t, os.WriteFile(secondStage, []byte("second"), fileMode))
	require.NoError(t, store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath:      secondStage,
		FinalPath:        finalPath,
		Manifest:         testManifest(t, "materialization", "commit-2", secondStage, finalPath),
		PreserveManifest: true,
	}))

	content, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	require.Equal(t, "second", string(content))
	stored, err := store.Get(context.Background(), firstManifest.MaterializationKey)
	require.NoError(t, err)
	require.Equal(t, firstManifest.CommitID, stored.CommitID)
}

func TestGetTreatsUnsupportedSchemaVersionAsMissing(t *testing.T) {
	t.Parallel()

	store := New(t.TempDir())
	require.NoError(t, store.ensureDirs())
	manifest := exec.Materialization{
		SchemaVersion:      exec.MaterializationSchemaVersion + 1,
		MaterializationKey: "materialization",
	}
	require.NoError(t, fileutil.WriteJSONAtomic(store.manifestPath(manifest.MaterializationKey), manifest, fileMode))

	_, err := store.Get(context.Background(), manifest.MaterializationKey)
	require.ErrorIs(t, err, exec.ErrMaterializationNotFound)
	require.ErrorContains(t, err, "unsupported materialization schema version")
}

func TestCommitReplacesLongNamedOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, strings.Repeat("x", 240))
	if err := os.WriteFile(finalPath, []byte("probe"), fileMode); err != nil {
		t.Skipf("filesystem does not support long basenames: %v", err)
	}
	require.NoError(t, os.Remove(finalPath))
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(finalPath), Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	for _, commit := range []struct {
		id      string
		content string
	}{
		{id: "commit-1", content: "first"},
		{id: "commit-2", content: "second"},
	} {
		stagingPath := filepath.Join(dir, "."+commit.id+".tmp")
		require.NoError(t, os.WriteFile(stagingPath, []byte(commit.content), fileMode))
		require.NoError(t, store.Commit(context.Background(), lock, exec.MaterializationCommit{
			StagingPath: stagingPath,
			FinalPath:   finalPath,
			Manifest:    testManifest(t, "materialization", commit.id, stagingPath, finalPath),
		}))
	}

	content, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	require.Equal(t, "second", string(content))
}

func TestCommitRequiresMatchingExclusiveOutputLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, "output.txt")
	stagingPath := filepath.Join(dir, ".output.tmp")
	require.NoError(t, os.WriteFile(stagingPath, []byte("new"), fileMode))
	manifest := testManifest(t, "materialization", "commit", stagingPath, finalPath)
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(filepath.Join(dir, "other.txt")), Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	err = store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath: stagingPath,
		FinalPath:   finalPath,
		Manifest:    manifest,
	})
	require.ErrorContains(t, err, "exclusive lock for the final output")
	require.FileExists(t, stagingPath)
	require.NoFileExists(t, finalPath)
}

func TestCommitRejectsSharedOutputLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, "output.txt")
	stagingPath := filepath.Join(dir, ".output.tmp")
	require.NoError(t, os.WriteFile(stagingPath, []byte("new"), fileMode))
	manifest := testManifest(t, "materialization", "commit", stagingPath, finalPath)
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(finalPath), Mode: exec.PathLockShared,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	err = store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath: stagingPath,
		FinalPath:   finalPath,
		Manifest:    manifest,
	})
	require.ErrorContains(t, err, "exclusive lock for the final output")
}

func TestCommitRequiresSiblingStagingPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, "output.txt")
	stagingPath := filepath.Join(t.TempDir(), "output.tmp")
	require.NoError(t, os.WriteFile(stagingPath, []byte("new"), fileMode))
	manifest := testManifest(t, "materialization", "commit", stagingPath, finalPath)
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(finalPath), Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	err = store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath: stagingPath,
		FinalPath:   finalPath,
		Manifest:    manifest,
	})
	require.ErrorContains(t, err, "same filesystem directory")
}

func TestCommitRollsBackOutputWhenManifestWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions")
	}

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	finalPath := filepath.Join(dir, "output.txt")
	stagingPath := filepath.Join(dir, ".output.tmp")
	require.NoError(t, os.WriteFile(finalPath, []byte("old"), fileMode))
	require.NoError(t, os.WriteFile(stagingPath, []byte("new"), fileMode))
	manifest := testManifest(t, "materialization", "commit", stagingPath, finalPath)
	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: incremental.ComparisonKey(finalPath), Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Release()) })

	manifestsDir := filepath.Join(store.root, "manifests")
	require.NoError(t, os.Chmod(manifestsDir, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(manifestsDir, 0o750)) })
	err = store.Commit(context.Background(), lock, exec.MaterializationCommit{
		StagingPath: stagingPath,
		FinalPath:   finalPath,
		Manifest:    manifest,
	})
	require.ErrorContains(t, err, "write materialization manifest")
	content, readErr := os.ReadFile(finalPath)
	require.NoError(t, readErr)
	require.Equal(t, "old", string(content))
	require.NoFileExists(t, stagingPath)
	require.NoFileExists(t, store.manifestPath(manifest.MaterializationKey))
	require.NoFileExists(t, store.journalPath(incremental.ComparisonKey(finalPath)))
}

func testManifest(t *testing.T, key, commitID, sourcePath, finalPath string) exec.Materialization {
	t.Helper()
	output, err := snapshotFile(sourcePath)
	require.NoError(t, err)
	output.Path = finalPath
	return exec.Materialization{
		SchemaVersion:      exec.MaterializationSchemaVersion,
		MaterializationKey: key,
		CommitID:           commitID,
		Output:             output,
	}
}

func TestRecoverIncompleteCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		previous         bool
		backupContent    string
		finalContent     string
		manifest         string
		preserveManifest bool
		wantFinalContent string
		wantManifest     string
	}{
		{
			name:             "prepared journal before backup",
			previous:         true,
			finalContent:     "known-good",
			manifest:         "previous",
			wantFinalContent: "known-good",
			wantManifest:     "previous",
		},
		{
			name:             "final replaced before manifest",
			previous:         true,
			backupContent:    "known-good",
			finalContent:     "proposed",
			manifest:         "previous",
			wantFinalContent: "known-good",
			wantManifest:     "previous",
		},
		{
			name:             "manifest and final committed",
			previous:         true,
			backupContent:    "known-good",
			finalContent:     "proposed",
			manifest:         "proposed",
			wantFinalContent: "proposed",
			wantManifest:     "proposed",
		},
		{
			name:             "preserved manifest and final committed",
			previous:         true,
			backupContent:    "known-good",
			finalContent:     "proposed",
			manifest:         "previous",
			preserveManifest: true,
			wantFinalContent: "proposed",
			wantManifest:     "previous",
		},
		{
			name:             "first always-run publication committed",
			finalContent:     "proposed",
			preserveManifest: true,
			wantFinalContent: "proposed",
		},
		{
			name:         "first materialization before manifest",
			finalContent: "proposed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store := New(filepath.Join(dir, "store"))
			require.NoError(t, store.ensureDirs())
			finalPath := filepath.Join(dir, "output.txt")
			backupPath := filepath.Join(dir, "output.backup")
			manifestPath := store.manifestPath("materialization")
			journalPath := store.journalPath("output-key")

			previousSource := filepath.Join(dir, "previous.txt")
			require.NoError(t, os.WriteFile(previousSource, []byte("known-good"), fileMode))
			previousSnapshot, err := snapshotFile(previousSource)
			require.NoError(t, err)
			previousSnapshot.Path = finalPath

			proposedSource := filepath.Join(dir, "proposed.txt")
			require.NoError(t, os.WriteFile(proposedSource, []byte("proposed"), fileMode))
			proposedSnapshot, err := snapshotFile(proposedSource)
			require.NoError(t, err)
			proposedSnapshot.Path = finalPath
			proposed := exec.Materialization{CommitID: "proposed", Output: proposedSnapshot}

			if tt.finalContent != "" {
				require.NoError(t, os.WriteFile(finalPath, []byte(tt.finalContent), fileMode))
			}
			if tt.backupContent != "" {
				require.NoError(t, os.WriteFile(backupPath, []byte(tt.backupContent), fileMode))
			}
			previousManifest := json.RawMessage(nil)
			if tt.previous {
				previousManifest = json.RawMessage(`{"commitId":"previous"}`)
			}
			switch tt.manifest {
			case "previous":
				require.NoError(t, fileutil.WriteFileAtomic(manifestPath, previousManifest, fileMode))
			case "proposed":
				require.NoError(t, fileutil.WriteJSONAtomic(manifestPath, proposed, fileMode))
			}

			journal := commitJournal{
				FinalPath:        finalPath,
				BackupPath:       backupPath,
				ManifestPath:     manifestPath,
				PreviousManifest: previousManifest,
				Proposed:         proposed,
				PreserveManifest: tt.preserveManifest,
			}
			if tt.previous {
				journal.PreviousFinal = &previousSnapshot
			}
			require.NoError(t, fileutil.WriteJSONAtomic(journalPath, journal, fileMode))

			require.NoError(t, store.recover("output-key"))
			require.NoFileExists(t, journalPath)
			require.NoFileExists(t, backupPath)
			if tt.wantFinalContent == "" {
				require.NoFileExists(t, finalPath)
			} else {
				content, err := os.ReadFile(finalPath)
				require.NoError(t, err)
				require.Equal(t, tt.wantFinalContent, string(content))
			}
			switch tt.wantManifest {
			case "previous":
				manifest, err := os.ReadFile(manifestPath)
				require.NoError(t, err)
				require.JSONEq(t, string(previousManifest), string(manifest))
			case "proposed":
				manifest, err := os.ReadFile(manifestPath)
				require.NoError(t, err)
				var recovered exec.Materialization
				require.NoError(t, json.Unmarshal(manifest, &recovered))
				require.Equal(t, proposed.CommitID, recovered.CommitID)
			default:
				require.NoFileExists(t, manifestPath)
			}
		})
	}
}

func TestRestorePreviousPreservesUnknownFinal(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		withPrevious bool
	}{
		{name: "without previous output"},
		{name: "with previous output", withPrevious: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			finalPath := filepath.Join(dir, "output.txt")
			backupPath := filepath.Join(dir, "output.backup")
			require.NoError(t, os.WriteFile(finalPath, []byte("user-data"), 0o600))
			proposedPath := filepath.Join(dir, "proposed.txt")
			require.NoError(t, os.WriteFile(proposedPath, []byte("proposed"), 0o600))
			proposed, err := snapshotFile(proposedPath)
			require.NoError(t, err)
			proposed.Path = finalPath

			journal := commitJournal{
				FinalPath:  finalPath,
				BackupPath: backupPath,
				Proposed:   exec.Materialization{Output: proposed},
			}
			if tt.withPrevious {
				previousPath := filepath.Join(dir, "previous.txt")
				require.NoError(t, os.WriteFile(previousPath, []byte("previous"), 0o600))
				previous, err := snapshotFile(previousPath)
				require.NoError(t, err)
				previous.Path = finalPath
				journal.PreviousFinal = &previous
				require.NoError(t, os.WriteFile(backupPath, []byte("previous"), 0o600))
			}

			err = restorePrevious(journal)
			require.Error(t, err)
			content, readErr := os.ReadFile(finalPath)
			require.NoError(t, readErr)
			require.Equal(t, "user-data", string(content))
			if tt.withPrevious {
				backup, readErr := os.ReadFile(backupPath)
				require.NoError(t, readErr)
				require.Equal(t, "previous", string(backup))
			}
		})
	}
}

func TestAcquirePathsClearsUnrecoverableJournal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := New(filepath.Join(dir, "store"))
	require.NoError(t, store.ensureDirs())
	finalPath := filepath.Join(dir, "output.txt")
	require.NoError(t, os.WriteFile(finalPath, []byte("user-data"), fileMode))

	previousSource := filepath.Join(dir, "previous.txt")
	require.NoError(t, os.WriteFile(previousSource, []byte("previous"), fileMode))
	previous, err := snapshotFile(previousSource)
	require.NoError(t, err)
	previous.Path = finalPath

	proposedSource := filepath.Join(dir, "proposed.txt")
	require.NoError(t, os.WriteFile(proposedSource, []byte("proposed"), fileMode))
	proposed, err := snapshotFile(proposedSource)
	require.NoError(t, err)
	proposed.Path = finalPath

	pathKey := incremental.ComparisonKey(finalPath)
	journalPath := store.journalPath(pathKey)
	require.NoError(t, fileutil.WriteJSONAtomic(journalPath, commitJournal{
		FinalPath:     finalPath,
		BackupPath:    filepath.Join(dir, "missing-backup"),
		ManifestPath:  store.manifestPath("materialization"),
		PreviousFinal: &previous,
		Proposed: exec.Materialization{
			SchemaVersion:      exec.MaterializationSchemaVersion,
			MaterializationKey: "materialization",
			CommitID:           "proposed",
			Output:             proposed,
		},
	}, fileMode))

	_, err = store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: pathKey, Mode: exec.PathLockExclusive,
	}})
	require.ErrorIs(t, err, exec.ErrMaterializationRecovery)
	require.NoFileExists(t, journalPath)

	lock, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key: pathKey, Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	content, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	require.Equal(t, "user-data", string(content))
}
