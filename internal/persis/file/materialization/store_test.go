// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package materialization

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
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

func TestRecoverIncompleteCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		previous         bool
		backupContent    string
		finalContent     string
		manifest         string
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

	dir := t.TempDir()
	finalPath := filepath.Join(dir, "output.txt")
	require.NoError(t, os.WriteFile(finalPath, []byte("user-data"), 0o600))
	proposedPath := filepath.Join(dir, "proposed.txt")
	require.NoError(t, os.WriteFile(proposedPath, []byte("proposed"), 0o600))
	proposed, err := snapshotFile(proposedPath)
	require.NoError(t, err)
	proposed.Path = finalPath

	err = restorePrevious(commitJournal{
		FinalPath: finalPath,
		Proposed:  exec.Materialization{Output: proposed},
	})
	require.Error(t, err)
	content, readErr := os.ReadFile(finalPath)
	require.NoError(t, readErr)
	require.Equal(t, "user-data", string(content))
}
