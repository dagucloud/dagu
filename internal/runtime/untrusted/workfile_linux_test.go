// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file that Dagu opens for a step in the work directory of a run with an
// untrusted step does not follow a symbolic link, which an untrusted step could
// have made to a file of the worker.
func TestOpenWorkFile(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "worker-secret")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(work, "report.txt")))
	require.NoError(t, os.Symlink(filepath.Dir(outside), filepath.Join(work, "logs")))
	require.NoError(t, os.WriteFile(filepath.Join(work, "plain.txt"), []byte("plain"), 0o600))

	_, err := OpenWorkFile(work, filepath.Join(work, "report.txt"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")

	_, err = OpenWorkFile(work, filepath.Join(work, "logs", "worker-secret"), os.O_RDONLY, 0)
	require.Error(t, err)

	file, err := OpenWorkFile(work, filepath.Join(work, "plain.txt"), os.O_RDONLY, 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	file, err = OpenWorkFile(work, filepath.Join(work, "new.txt"), os.O_WRONLY|os.O_CREATE, 0o600)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	file, err = OpenWorkFile(work, outside, os.O_RDONLY, 0)
	require.NoError(t, err, "a path outside the work directory opens as usual")
	require.NoError(t, file.Close())

	data, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(data), "the file of the worker does not change")

	// A FIFO that an untrusted step made at the path of stdin or stdout would block
	// the open for ever. The open refuses it at once.
	fifo := filepath.Join(work, "stdin.txt")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	for _, flags := range []int{os.O_RDONLY, os.O_WRONLY} {
		done := make(chan error, 1)
		go func() {
			file, err := OpenWorkFile(work, fifo, flags, 0)
			if err == nil {
				_ = file.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			require.Error(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("the open of a FIFO blocked")
		}
	}
}

// The restricted user can run a file with the bit x for others in a folder
// that it can pass.
func TestCheckExecutable(t *testing.T) {
	t.Parallel()

	cfg := config.UntrustedConfig{Enabled: true, UID: 64031, GID: 64031}
	dir, err := os.MkdirTemp("", "dagu-untrusted-exec-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	require.NoError(t, os.Chmod(dir, 0o755))
	binary := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	assert.NoError(t, CheckExecutable(binary, cfg))

	require.NoError(t, os.Chmod(binary, 0o750))
	assert.ErrorContains(t, CheckExecutable(binary, cfg), "cannot run it")
	require.NoError(t, os.Chmod(binary, 0o755))
	require.NoError(t, os.Chmod(dir, 0o700))
	assert.ErrorContains(t, CheckExecutable(binary, cfg), "cannot pass it")
	assert.ErrorContains(t, CheckExecutable(filepath.Join(dir, "none"), cfg), "no such file")
	assert.ErrorContains(t, CheckExecutable(binary, config.UntrustedConfig{}), "untrusted.uid")
}
