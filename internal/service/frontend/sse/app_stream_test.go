// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
)

func TestDirectoryWatcherStopIsIdempotent(t *testing.T) {
	watcher := &directoryWatcher{
		done: make(chan struct{}),
	}

	require.NotPanics(t, func() {
		watcher.Stop()
		watcher.Stop()
	})
}

func TestAppStreamServiceShutdownIsIdempotent(t *testing.T) {
	service := &AppStreamService{
		cancel: func() {},
		watchers: []appWatcher{
			&directoryWatcher{done: make(chan struct{})},
		},
	}

	require.NotPanics(t, func() {
		service.Shutdown()
		service.Shutdown()
	})
}

func TestNewAppStreamServiceDoesNotCreateDAGRunsDir(t *testing.T) {
	root := t.TempDir()
	dagRunsDir := filepath.Join(root, "dag-runs")

	service, err := NewAppStreamService(AppStreamConfig{
		Paths: config.PathsConfig{
			SuspendFlagsDir: filepath.Join(root, "suspend"),
			DAGRunsDir:      dagRunsDir,
			QueueDir:        filepath.Join(root, "queue"),
		},
	})

	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	assert.NoDirExists(t, dagRunsDir)
}
