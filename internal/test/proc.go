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
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/stretchr/testify/require"
)

func newProcStore(cfg *config.Config) exec.ProcStore {
	return file.NewProcStore(cfg)
}

func procGroupDir(procDir, groupName, dagName string) string {
	return filepath.Join(procDir, groupName, dagName)
}

// ProcHeartbeatObserver is the proc-store surface needed by heartbeat liveness tests.
type ProcHeartbeatObserver interface {
	LatestHeartbeat(ctx context.Context, groupName string, dagRun exec.DAGRunRef) (*exec.ProcHeartbeat, error)
}

// WaitForProcHeartbeat returns the latest heartbeat observation for dagRun once it exists.
func WaitForProcHeartbeat(
	t *testing.T,
	ctx context.Context,
	procStore ProcHeartbeatObserver,
	groupName string,
	dagRun exec.DAGRunRef,
	timeout time.Duration,
) exec.ProcHeartbeat {
	t.Helper()

	var heartbeat *exec.ProcHeartbeat
	var lastErr error
	require.Eventually(t, func() bool {
		heartbeat, lastErr = procStore.LatestHeartbeat(ctx, groupName, dagRun)
		if lastErr != nil {
			return false
		}
		return heartbeat != nil && heartbeat.Fresh
	}, timeout, 25*time.Millisecond, "timed out waiting for proc heartbeat for %s: %v", dagRun.String(), lastErr)

	return *heartbeat
}

// RequireProcHeartbeatAdvance verifies dagRun's proc heartbeat updates within the timeout.
func RequireProcHeartbeatAdvance(
	t *testing.T,
	ctx context.Context,
	procStore ProcHeartbeatObserver,
	groupName string,
	dagRun exec.DAGRunRef,
	timeout time.Duration,
) {
	t.Helper()

	initial := WaitForProcHeartbeat(t, ctx, procStore, groupName, dagRun, timeout)

	var lastErr error
	require.Eventually(t, func() bool {
		next, err := procStore.LatestHeartbeat(ctx, groupName, dagRun)
		if err != nil {
			lastErr = err
			return false
		}
		return next != nil && next.Fresh && next.AdvancedSince(initial)
	}, timeout, 25*time.Millisecond, "proc heartbeat did not advance for %s: %v", dagRun.String(), lastErr)
}

// CreateStaleLegacyProcFile writes a stale legacy .proc heartbeat file for the given dag-run.
func CreateStaleLegacyProcFile(
	t *testing.T,
	procDir string,
	groupName string,
	dagRun exec.DAGRunRef,
	startedAt time.Time,
	age time.Duration,
) string {
	return CreateStaleLegacyProcFileWithAttempt(t, procDir, groupName, dagRun, "attempt_"+dagRun.ID, startedAt, age)
}

// CreateStaleLegacyProcFileWithAttempt writes a stale legacy .proc heartbeat file for the given dag-run and attempt.
func CreateStaleLegacyProcFileWithAttempt(
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
