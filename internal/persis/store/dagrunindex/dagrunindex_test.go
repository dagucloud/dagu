// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrunindex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	indexv1 "github.com/dagucloud/dagu/proto/index/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// createDayDir creates a day directory with the given number of terminal runs.
func createDayDir(t *testing.T, dayDir string, numRuns int, status core.Status) {
	t.Helper()
	for i := range numRuns {
		runName := "dag-run_20240115_120000Z_run" + string(rune('A'+i))
		runDir := filepath.Join(dayDir, runName)
		attemptDir := filepath.Join(runDir, "attempt_20240115_120000_001Z_abc123")
		require.NoError(t, os.MkdirAll(attemptDir, 0750))

		st := exec.DAGRunStatus{
			Name:       "test",
			DAGRunID:   "run" + string(rune('A'+i)),
			AttemptID:  "abc123",
			Status:     status,
			StartedAt:  "2024-01-15T12:00:00Z",
			FinishedAt: "2024-01-15T12:01:00Z",
			Labels:     []string{"env=prod"},
		}
		data, err := json.Marshal(st)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "status.jsonl"), append(data, '\n'), 0600))
	}
}

func readDayDir(t *testing.T, dayDir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dayDir)
	require.NoError(t, err)
	return entries
}

func TestTryLoadForDay_FewRuns(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 5, core.Succeeded) // < 10

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Nil(t, entries)
	assert.False(t, fromIndex)
}

func TestTryLoadForDay_NoIndex_AllTerminal(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 12, core.Succeeded)

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	require.Len(t, entries, 12)
	assert.True(t, fromIndex)

	// Verify index file was created.
	_, statErr := os.Stat(filepath.Join(dayDir, IndexFileName))
	assert.NoError(t, statErr)
}

func TestTryLoadForDay_NoIndex_ActiveRun(t *testing.T) {
	dayDir := t.TempDir()

	// Create 9 terminal + 1 active = 10 total
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Override one run to be active.
	dirEntries := readDayDir(t, dayDir)
	// Find the first dag-run dir and replace its status.
	for _, de := range dirEntries {
		if de.IsDir() {
			attemptDir := filepath.Join(dayDir, de.Name(), "attempt_20240115_120000_001Z_abc123")
			st := exec.DAGRunStatus{
				Status:    core.Running,
				StartedAt: "2024-01-15T12:00:00Z",
				LeaseAt:   1705320030000,
			}
			data, err := json.Marshal(st)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "status.jsonl"), append(data, '\n'), 0600))
			break
		}
	}

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 10)
	assert.False(t, fromIndex) // No index written because one run is active.

	var runningEntry *Entry
	for i := range entries {
		if entries[i].Status == core.Running {
			runningEntry = &entries[i]
			break
		}
	}
	require.NotNil(t, runningEntry)
	assert.Equal(t, int64(1705320030000), runningEntry.LeaseAt)

	// No index file should exist.
	_, statErr := os.Stat(filepath.Join(dayDir, IndexFileName))
	assert.True(t, os.IsNotExist(statErr))
}

func TestTryLoadForDay_ValidIndex(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 10, core.Succeeded)

	// First call: builds index.
	entries1, fromIndex1, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	require.Len(t, entries1, 10)
	assert.True(t, fromIndex1)

	// Second call: loads from index.
	entries2, fromIndex2, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	require.Len(t, entries2, 10)
	assert.True(t, fromIndex2)
}

func TestTryLoadForDay_PreservesRetryMetadata(t *testing.T) {
	dayDir := t.TempDir()
	runDir := filepath.Join(dayDir, "dag-run_20240115_120000Z_retry-run")
	attemptDir := filepath.Join(runDir, "attempt_20240115_120000_001Z_abc123")
	require.NoError(t, os.MkdirAll(attemptDir, 0750))

	st := exec.DAGRunStatus{
		Name:                 "retry-dag",
		DAGRunID:             "retry-run",
		AttemptID:            "abc123",
		Status:               core.Failed,
		StartedAt:            "2024-01-15T12:00:00Z",
		FinishedAt:           "2024-01-15T12:01:00Z",
		Parent:               exec.NewDAGRunRef("parent-dag", "parent-run"),
		AutoRetryCount:       1,
		AutoRetryLimit:       3,
		AutoRetryInterval:    2 * time.Minute,
		AutoRetryBackoff:     2.0,
		AutoRetryMaxInterval: 10 * time.Minute,
		ProcGroup:            "shared-queue",
		SuspendFlagName:      "retry-dag",
	}
	data, err := json.Marshal(st)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "status.jsonl"), append(data, '\n'), 0600))

	// Add enough runs for the index to be written.
	createDayDir(t, dayDir, 9, core.Succeeded)

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	require.True(t, fromIndex)
	require.Len(t, entries, 10)

	var found *Entry
	for i := range entries {
		if entries[i].DagRunID == "retry-run" {
			found = &entries[i]
			break
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "parent-dag", found.ParentName)
	assert.Equal(t, "parent-run", found.ParentID)
	assert.Equal(t, 1, found.AutoRetryCount)
	assert.Equal(t, 3, found.AutoRetryLimit)
	assert.Equal(t, 2*time.Minute, found.AutoRetryInterval)
	assert.Equal(t, 2.0, found.AutoRetryBackoff)
	assert.Equal(t, 10*time.Minute, found.AutoRetryMaxInterval)
	assert.Equal(t, "shared-queue", found.ProcGroup)
	assert.Equal(t, "retry-dag", found.SuspendFlagName)
}

func TestTryLoadForDay_StaleIndex_NewRun(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Build index.
	_, _, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)

	// Add another run.
	runDir := filepath.Join(dayDir, "dag-run_20240115_130000Z_newrun")
	attemptDir := filepath.Join(runDir, "attempt_20240115_130000_001Z_xyz789")
	require.NoError(t, os.MkdirAll(attemptDir, 0750))
	st := exec.DAGRunStatus{Status: core.Succeeded, StartedAt: "2024-01-15T13:00:00Z", FinishedAt: "2024-01-15T13:01:00Z"}
	data, _ := json.Marshal(st)
	require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "status.jsonl"), append(data, '\n'), 0600))

	// Should detect new run and rebuild.
	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 11)
	assert.True(t, fromIndex)
}

func TestTryLoadForDay_StaleIndex_NewAttempt(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Build index.
	_, _, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)

	// Add a new attempt to an existing run (simulating a retry).
	dirEntries := readDayDir(t, dayDir)
	for _, de := range dirEntries {
		if de.IsDir() {
			newAttemptDir := filepath.Join(dayDir, de.Name(), "attempt_20240115_130000_002Z_retry1")
			require.NoError(t, os.MkdirAll(newAttemptDir, 0750))
			st := exec.DAGRunStatus{Status: core.Succeeded, StartedAt: "2024-01-15T13:00:00Z", FinishedAt: "2024-01-15T13:01:00Z"}
			data, _ := json.Marshal(st)
			require.NoError(t, os.WriteFile(filepath.Join(newAttemptDir, "status.jsonl"), append(data, '\n'), 0600))
			break
		}
	}

	// Should detect new attempt and rebuild.
	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 10)
	assert.True(t, fromIndex)

	// Verify the rebuilt index reflects the new attempt.
	found := false
	for _, e := range entries {
		if e.LatestAttemptDir == "attempt_20240115_130000_002Z_retry1" {
			found = true
			break
		}
	}
	assert.True(t, found, "Expected rebuilt index to contain the new attempt directory")
}

func TestTryLoadForDay_CorruptIndex(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Write corrupt data to index file.
	require.NoError(t, os.WriteFile(filepath.Join(dayDir, IndexFileName), []byte("garbage"), 0600))

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 10)
	assert.True(t, fromIndex) // Rebuilt and wrote new index.
}

func TestTryLoadForDay_VersionMismatch(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Write index with wrong version.
	idx := &indexv1.DAGRunIndex{Version: 999}
	data, err := proto.Marshal(idx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dayDir, IndexFileName), data, 0600))

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 10)
	assert.True(t, fromIndex)
}

func TestDeleteIndex(t *testing.T) {
	dayDir := t.TempDir()

	indexPath := filepath.Join(dayDir, IndexFileName)
	require.NoError(t, os.WriteFile(indexPath, []byte("data"), 0600))

	DeleteIndex(dayDir)

	_, err := os.Stat(indexPath)
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteIndex_NotExist(t *testing.T) {
	// Should not panic or error on non-existent file.
	DeleteIndex(t.TempDir())
}

func TestFilterDAGRunDirs(t *testing.T) {
	dir := t.TempDir()

	// Create some directories.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dag-run_20240115_120000Z_abc"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dag-run_20240115_120000Z_def"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "not-a-run"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0600))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	filtered := filterDAGRunDirs(entries)
	assert.Len(t, filtered, 2)
}

func TestFindLatestAttempt(t *testing.T) {
	dir := t.TempDir()

	// Create attempt directories.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "attempt_20240115_120000_001Z_aaa"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "attempt_20240115_130000_002Z_bbb"), 0750))
	// Hidden attempt (dequeued).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".attempt_20240115_140000_003Z_ccc"), 0750))

	latest, err := findLatestAttempt(dir)
	require.NoError(t, err)
	assert.Equal(t, "attempt_20240115_130000_002Z_bbb", latest)
}

func TestFindLatestAttempt_Empty(t *testing.T) {
	dir := t.TempDir()
	latest, err := findLatestAttempt(dir)
	require.NoError(t, err)
	assert.Empty(t, latest)
}

func TestParseDagRunID(t *testing.T) {
	assert.Equal(t, "myrun123", parseDagRunID("dag-run_20240115_120000Z_myrun123"))
	assert.Equal(t, "", parseDagRunID("invalid"))
}

func TestRebuildForDay_MixedStatuses(t *testing.T) {
	dayDir := t.TempDir()

	// Create 10 terminal + 2 active = 12 total
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Add 2 more active runs with different names.
	for i := range 2 {
		runName := fmt.Sprintf("dag-run_20240115_130000Z_active%d", i)
		runDir := filepath.Join(dayDir, runName)
		attemptDir := filepath.Join(runDir, "attempt_20240115_130000_001Z_abc123")
		require.NoError(t, os.MkdirAll(attemptDir, 0750))

		st := exec.DAGRunStatus{
			Name:      "test",
			DAGRunID:  fmt.Sprintf("active%d", i),
			AttemptID: "abc123",
			Status:    core.Running,
			StartedAt: "2024-01-15T13:00:00Z",
		}
		data, err := json.Marshal(st)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "status.jsonl"), append(data, '\n'), 0600))
	}

	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 12)
	assert.False(t, fromIndex) // Not all terminal, so no index written.

	// Verify no index file was created.
	_, statErr := os.Stat(filepath.Join(dayDir, IndexFileName))
	assert.True(t, os.IsNotExist(statErr))
}

func TestParseStatusFile_MultiLine(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.jsonl")

	// Write multiple status lines (simulating updates during a run).
	line1 := `{"status":1,"startedAt":"2024-01-15T12:00:00Z"}`
	line2 := `{"status":4,"startedAt":"2024-01-15T12:00:00Z","finishedAt":"2024-01-15T12:01:00Z"}`
	content := line1 + "\n" + line2 + "\n"
	require.NoError(t, os.WriteFile(statusPath, []byte(content), 0600))

	status, err := parseStatusFile(statusPath)
	require.NoError(t, err)
	// Should return the last valid line (succeeded status).
	assert.Equal(t, core.Succeeded, status.Status)
}

func TestParseStatusFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.jsonl")
	require.NoError(t, os.WriteFile(statusPath, []byte(""), 0600))

	_, err := parseStatusFile(statusPath)
	assert.Error(t, err)
}

func TestParseStatusFile_AllInvalid(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.jsonl")
	require.NoError(t, os.WriteFile(statusPath, []byte("not json\nalso not json\n"), 0600))

	_, err := parseStatusFile(statusPath)
	assert.Error(t, err)
}

func TestParseTimeToUnix_EdgeCases(t *testing.T) {
	// Empty string returns 0.
	assert.Equal(t, int64(0), parseTimeToUnix(""))

	// Invalid string returns 0.
	assert.Equal(t, int64(0), parseTimeToUnix("not-a-time"))

	// Valid RFC3339 returns correct unix timestamp.
	ts := parseTimeToUnix("2024-01-15T12:00:00Z")
	assert.Greater(t, ts, int64(0))
}

func TestParseDagRunID_MoreEdgeCases(t *testing.T) {
	// Empty string
	assert.Equal(t, "", parseDagRunID(""))

	// Prefix only
	assert.Equal(t, "", parseDagRunID("dag-run_"))

	// Valid with special characters in ID
	assert.Equal(t, "run-with-dashes", parseDagRunID("dag-run_20240115_120000Z_run-with-dashes"))
}

func TestRebuildForDay_WriteFailure(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 12, core.Succeeded)

	require.NoError(t, os.MkdirAll(filepath.Join(dayDir, IndexFileName), 0750))

	entries, fromIndex, err := RebuildForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 12)
	assert.False(t, fromIndex, "should return false when index write fails")
}

func TestRebuildForDay_UnreadableStatus(t *testing.T) {
	dayDir := t.TempDir()

	runName := "dag-run_20240115_120000Z_badstatus"
	runDir := filepath.Join(dayDir, runName)
	attemptDir := filepath.Join(runDir, "attempt_20240115_120000_001Z_abc123")
	require.NoError(t, os.MkdirAll(attemptDir, 0750))

	statusPath := filepath.Join(attemptDir, "status.jsonl")
	require.NoError(t, os.MkdirAll(statusPath, 0750))

	entries, fromIndex, err := RebuildForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Empty(t, entries, "unreadable status should be skipped")
	assert.False(t, fromIndex)
}

func TestRebuildForDay_AttemptReadError(t *testing.T) {
	dayDir := t.TempDir()

	runName := "dag-run_20240115_120000Z_noperm"
	runDir := filepath.Join(dayDir, runName)
	require.NoError(t, os.MkdirAll(runDir, 0750))
	entries := readDayDir(t, dayDir)
	require.NoError(t, os.RemoveAll(runDir))
	testutil.BlockPathWithFile(t, runDir)

	_, _, err := RebuildForDay(dayDir, entries)
	require.Error(t, err, "should return error when findLatestAttempt fails")
}

func TestValidateIndex_RunDirDeleted(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 12, core.Succeeded) // 12 so after deleting 1 we still have 11 >= MinRunsForIndex

	// Build index.
	_, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	require.True(t, fromIndex)

	// Delete one run dir.
	dirEntries := readDayDir(t, dayDir)
	for _, de := range dirEntries {
		if de.IsDir() {
			require.NoError(t, os.RemoveAll(filepath.Join(dayDir, de.Name())))
			break
		}
	}

	// Should detect missing dir and rebuild.
	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 11)
	assert.True(t, fromIndex, "should rebuild and write new index")
}

func TestValidateIndex_StatusFileModified(t *testing.T) {
	dayDir := t.TempDir()
	createDayDir(t, dayDir, 10, core.Succeeded)

	// Build index.
	_, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	require.True(t, fromIndex)

	// Modify a status file (append data to change size).
	dirEntries := readDayDir(t, dayDir)
	for _, de := range dirEntries {
		if de.IsDir() {
			attemptDir := filepath.Join(dayDir, de.Name(), "attempt_20240115_120000_001Z_abc123")
			statusPath := filepath.Join(attemptDir, "status.jsonl")
			f, err := os.OpenFile(statusPath, os.O_APPEND|os.O_WRONLY, 0600)
			require.NoError(t, err)
			_, err = f.WriteString(`{"status":4}` + "\n")
			require.NoError(t, err)
			require.NoError(t, f.Close())
			break
		}
	}

	// Should detect modified status and rebuild.
	entries, fromIndex, err := TryLoadForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Len(t, entries, 10)
	assert.True(t, fromIndex, "should rebuild and write new index")
}

func TestRebuildForDay_EmptyAttempts(t *testing.T) {
	dayDir := t.TempDir()

	// Create a run directory with no attempt subdirectories.
	runDir := filepath.Join(dayDir, "dag-run_20240115_120000Z_noattempts")
	require.NoError(t, os.MkdirAll(runDir, 0750))

	// Need at least MinRunsForIndex entries for TryLoadForDay to consider indexing.
	// But RebuildForDay doesn't have that restriction.
	entries, fromIndex, err := RebuildForDay(dayDir, readDayDir(t, dayDir))
	require.NoError(t, err)
	assert.Empty(t, entries) // No valid attempts found.
	assert.False(t, fromIndex)
}
