// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/stretchr/testify/require"
)

const procFilePattern = "proc_*.proc"

func newProcStore(cfg *config.Config) *store.ProcStore {
	return store.NewProcStore(
		file.NewCollection(cfg.Paths.ProcDir),
		store.WithProcHeartbeatInterval(cfg.Proc.HeartbeatInterval),
		store.WithProcHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
		store.WithProcStaleThreshold(cfg.Proc.StaleThreshold),
		store.WithProcLegacyDir(cfg.Paths.ProcDir),
	)
}

func procGroupDir(procDir, groupName, dagName string) string {
	return filepath.Join(procDir, groupName, dagName)
}

// WaitForProcFile returns the heartbeat proc file for the given dag-run once it exists.
func WaitForProcFile(t *testing.T, procDir, groupName string, dagRun exec.DAGRunRef, timeout time.Duration) string {
	t.Helper()

	var match string
	require.Eventually(t, func() bool {
		pattern := filepath.Join(procGroupDir(procDir, groupName, dagRun.Name), procFilePattern)
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)
		if len(matches) == 0 {
			return false
		}
		match = matches[0]
		return true
	}, timeout, 25*time.Millisecond, "timed out waiting for proc file for %s", dagRun.String())

	return match
}

// RequireHeartbeatAdvance verifies the proc file heartbeat updates within the timeout.
func RequireHeartbeatAdvance(t *testing.T, procFile string, timeout time.Duration) {
	t.Helper()

	initialValue, initialModTime, err := readHeartbeat(procFile)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		value, modTime, err := readHeartbeat(procFile)
		require.NoError(t, err)
		return value > initialValue || modTime.After(initialModTime)
	}, timeout, 25*time.Millisecond, "proc heartbeat did not advance for %s", procFile)
}

// CreateStaleProcFile writes a stale proc heartbeat file for the given dag-run.
func CreateStaleProcFile(
	t *testing.T,
	procDir string,
	groupName string,
	dagRun exec.DAGRunRef,
	startedAt time.Time,
	age time.Duration,
) string {
	return CreateStaleProcFileWithAttempt(t, procDir, groupName, dagRun, "attempt_"+dagRun.ID, startedAt, age)
}

// CreateStaleProcFileWithAttempt writes a stale proc heartbeat file for the given dag-run and attempt.
func CreateStaleProcFileWithAttempt(
	t *testing.T,
	procDir string,
	groupName string,
	dagRun exec.DAGRunRef,
	attemptID string,
	startedAt time.Time,
	age time.Duration,
) string {
	t.Helper()

	dir := procGroupDir(procDir, groupName, dagRun.Name)
	require.NoError(t, os.MkdirAll(dir, 0o750))

	staleTime := startedAt.Add(-age).UTC()
	fileName := fmt.Sprintf(
		"proc_%sZ_%s_%s.proc",
		staleTime.Format("20060102_150405"),
		hex.EncodeToString([]byte(dagRun.ID)),
		hex.EncodeToString([]byte(attemptID)),
	)
	procFile := filepath.Join(dir, fileName)

	staleUnix := staleTime.Unix()
	require.GreaterOrEqual(t, staleUnix, int64(0), "stale heartbeat timestamp must be after unix epoch")
	meta, err := json.Marshal(map[string]any{
		"version":         1,
		"dag_name":        dagRun.Name,
		"dag_run_id":      dagRun.ID,
		"attempt_id":      attemptID,
		"root_name":       dagRun.Name,
		"root_dag_run_id": dagRun.ID,
		"started_at":      startedAt.Unix(),
	})
	require.NoError(t, err)

	data := make([]byte, 8+len(meta))
	binary.BigEndian.PutUint64(data[:8], uint64(staleUnix)) //nolint:gosec // staleUnix is validated non-negative above
	copy(data[8:], meta)
	require.NoError(t, os.WriteFile(procFile, data, 0o600))
	require.NoError(t, os.Chtimes(procFile, staleTime, staleTime))

	return procFile
}

// ReadRunStatus loads the persisted status for the given dag-run reference.
func ReadRunStatus(ctx context.Context, t *testing.T, store exec.DAGRunStore, dagRun exec.DAGRunRef) *exec.DAGRunStatus {
	t.Helper()

	attempt, err := store.FindAttempt(ctx, dagRun)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	return status
}

func readHeartbeat(procFile string) (uint64, time.Time, error) {
	info, err := os.Stat(procFile)
	if err != nil {
		return 0, time.Time{}, err
	}

	data, err := fileutil.ReadFileWithRetry(procFile)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(data) < 8 {
		return 0, time.Time{}, fmt.Errorf("heartbeat file %s is too short", procFile)
	}

	return binary.BigEndian.Uint64(data[:8]), info.ModTime(), nil
}
