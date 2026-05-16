// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wait

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/require"
)

func TestExecutorRunWaitsForDuration(t *testing.T) {
	t.Parallel()

	exec, err := newExecutor(context.Background(), core.Step{
		Commands: []core.CommandEntry{{Command: opDuration}},
		ExecutorConfig: core.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"duration": "5ms",
			},
		},
	})
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, exec.Run(context.Background()))
	require.GreaterOrEqual(t, time.Since(start), 5*time.Millisecond)
}

func TestExecutorRunWaitsUntilFileExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "ready.flag")
	exec, err := newExecutor(context.Background(), core.Step{
		Commands: []core.CommandEntry{{Command: opFile}},
		ExecutorConfig: core.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"path":          target,
				"poll_interval": "1ms",
			},
		},
	})
	require.NoError(t, err)

	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = os.WriteFile(target, []byte("ready"), 0o600)
	}()

	require.NoError(t, exec.Run(context.Background()))
}

func TestExecutorRunWaitsForHTTPStatus(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exec, err := newExecutor(context.Background(), core.Step{
		Commands: []core.CommandEntry{{Command: opHTTP}},
		ExecutorConfig: core.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"url":           server.URL,
				"status":        http.StatusNoContent,
				"poll_interval": "1ms",
			},
		},
	})
	require.NoError(t, err)

	require.NoError(t, exec.Run(context.Background()))
	require.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestNewExecutorRejectsNonHTTPURL(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(context.Background(), core.Step{
		Commands: []core.CommandEntry{{Command: opHTTP}},
		ExecutorConfig: core.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"url": "ftp://example.com/ready",
			},
		},
	})
	require.ErrorContains(t, err, "url must be an absolute HTTP URL")
}

func TestExecutorRunStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	exec, err := newExecutor(context.Background(), core.Step{
		Commands: []core.CommandEntry{{Command: opDuration}},
		ExecutorConfig: core.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"duration": "1h",
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, exec.Run(ctx), context.Canceled)
}
