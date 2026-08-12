// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestExecuteDAGSyncWriteDeadlineStartsWithResponse(t *testing.T) {
	t.Parallel()

	const timeout = 25 * time.Millisecond
	handler := resetSyncWriteDeadline(timeout)(
		func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
			time.Sleep(2 * timeout)
			return nil, nil
		},
		"ExecuteDAGSync",
	)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := handler(r.Context(), w, r, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.WriteTimeout = timeout
	server.Start()
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, resp.Body.Close())
	})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestWaitForLocalDAGStartReturnsNilWhenStarterProcessStillAlive(t *testing.T) {
	t.Parallel()

	api := newLocalStartTestAPI(t)
	done := make(chan error)
	started := currentProcessStartResult(t, done)

	err := api.waitForLocalDAGStart(context.Background(), &ir.DAG{Name: "pending"}, "run-1", started, time.Nanosecond)
	require.NoError(t, err)
}

func TestWaitForLocalDAGStartReturnsCanceledWhenContextCanceled(t *testing.T) {
	t.Parallel()

	api := newLocalStartTestAPI(t)
	done := make(chan error)
	started := currentProcessStartResult(t, done)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := api.waitForLocalDAGStart(ctx, &ir.DAG{Name: "pending"}, "run-1", started, time.Second)
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

	err := api.waitForLocalDAGStart(context.Background(), &ir.DAG{Name: "pending"}, "run-1", &launcher.StartResult{
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
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))
	return &API{
		dagRunMgr: runtime.NewManager(dagRunRepository, procStore, &config.Config{}),
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
