// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/procutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/launcher"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/file/proc"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestWaitForLocalDAGStartReturnsNilWhenStarterProcessStillAlive(t *testing.T) {
	t.Parallel()

	api := newLocalStartTestAPI(t)
	done := make(chan error)
	started := currentProcessStartResult(t, done)

	err := api.waitForLocalDAGStart(context.Background(), &core.DAG{Name: "pending"}, "run-1", started, time.Nanosecond)
	require.NoError(t, err)
}

func TestWaitForLocalDAGStartReturnsCanceledWhenContextCanceled(t *testing.T) {
	t.Parallel()

	api := newLocalStartTestAPI(t)
	done := make(chan error)
	started := currentProcessStartResult(t, done)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := api.waitForLocalDAGStart(ctx, &core.DAG{Name: "pending"}, "run-1", started, time.Second)
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, statusClientClosedRequest, apiErr.HTTPStatus)
	require.Equal(t, openapiv1.ErrorCodeInternalError, apiErr.Code)
	require.Equal(t, "DAG start request canceled", apiErr.Message)
}

func TestWaitForLocalDAGStartReturnsErrorWhenStarterExitedWithoutStatus(t *testing.T) {
	t.Parallel()

	api := newLocalStartTestAPI(t)
	done := make(chan error, 1)
	done <- errors.New("exit status 1")
	close(done)

	err := api.waitForLocalDAGStart(context.Background(), &core.DAG{Name: "pending"}, "run-1", &launcher.StartResult{
		PID:  1,
		Done: done,
	}, time.Nanosecond)
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusInternalServerError, apiErr.HTTPStatus)
	require.Equal(t, openapiv1.ErrorCodeInternalError, apiErr.Code)
	require.Contains(t, apiErr.Message, "DAG start process exited before publishing status")
	require.Contains(t, apiErr.Message, "exit status 1")
}

func newLocalStartTestAPI(t *testing.T) *API {
	t.Helper()

	tmpDir := t.TempDir()
	dagRunStore := dagrun.New(filepath.Join(tmpDir, "dag-runs"))
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))
	return &API{
		dagRunMgr: runtime.NewManager(dagRunStore, procStore, &config.Config{}),
	}
}

func newTestProcStore(procDir string) *proc.Store {
	return proc.New(procDir)
}

func currentProcessStartResult(t *testing.T, done <-chan error) *launcher.StartResult {
	t.Helper()

	pid := os.Getpid()
	startedAt, _ := procutil.StartTime(pid)
	return &launcher.StartResult{
		PID:          pid,
		PIDStartedAt: startedAt,
		Done:         done,
	}
}
