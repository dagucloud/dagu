// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file that Dagu opens for a step in the work directory of a run with an
// untrusted step does not follow a symbolic link, which an untrusted step could
// have made to a file of the worker. The walk of a kernel without openat2 keeps
// the same rules.
func TestOpenWorkFile(t *testing.T) {
	t.Parallel()

	openers := map[string]func(workDir, path string, flags int, perm fs.FileMode) (*os.File, error){
		"openat2": OpenWorkFile,
		"walk": func(workDir, path string, flags int, perm fs.FileMode) (*os.File, error) {
			rel, base, ok := workRelative(workDir, path)
			if !ok {
				return OpenWorkFile(workDir, path, flags, perm)
			}
			return openWorkFileWalk(base, rel, flags, perm)
		},
	}
	for name, open := range openers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testOpenWorkFile(t, open)
		})
	}
}

func testOpenWorkFile(t *testing.T, open func(workDir, path string, flags int, perm fs.FileMode) (*os.File, error)) {
	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "worker-secret")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(work, "report.txt")))
	require.NoError(t, os.Symlink(filepath.Dir(outside), filepath.Join(work, "logs")))
	require.NoError(t, os.WriteFile(filepath.Join(work, "plain.txt"), []byte("plain"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(work, "sub", "dir"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(work, "sub", "dir", "deep.txt"), []byte("deep"), 0o600))

	_, err := open(work, filepath.Join(work, "report.txt"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")

	_, err = open(work, filepath.Join(work, "logs", "worker-secret"), os.O_RDONLY, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbolic link")

	file, err := open(work, filepath.Join(work, "plain.txt"), os.O_RDONLY, 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	// A mode without O_CREATE is ignored, as os.OpenFile ignores it.
	file, err = open(work, filepath.Join(work, "plain.txt"), os.O_RDONLY, 0o644)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	file, err = open(work, filepath.Join(work, "sub", "dir", "deep.txt"), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	file, err = open(work, filepath.Join(work, "new.txt"), os.O_WRONLY|os.O_CREATE, 0o600)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = open(work, filepath.Join(work, "missing", "new.txt"), os.O_WRONLY|os.O_CREATE, 0o600)
	require.ErrorIs(t, err, fs.ErrNotExist)

	file, err = open(work, outside, os.O_RDONLY, 0)
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
			file, err := open(work, fifo, flags, 0)
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
