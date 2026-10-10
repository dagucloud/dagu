// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathLockSamePath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "locked.xlsx")
	release := holdWorkbook(t, path)

	requireWaits(t, path)
	release()
	requireProceeds(t, path)
}

func TestPathLockOtherPath(t *testing.T) {
	t.Parallel()
	holdWorkbook(t, filepath.Join(t.TempDir(), "a.xlsx"))

	requireProceeds(t, filepath.Join(t.TempDir(), "b.xlsx"))
}

// A workbook reached through a symbolic link is the same workbook, even when
// the directories below the link do not exist yet.
func TestPathLockSymlinkAlias(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rel  []string
	}{
		{name: "ExistingDir", rel: []string{"book.xlsx"}},
		{name: "MissingDir", rel: []string{"new", "book.xlsx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			realDir := filepath.Join(root, "real")
			aliasDir := filepath.Join(root, "alias")
			require.NoError(t, os.Mkdir(realDir, 0o750))
			if err := os.Symlink(realDir, aliasDir); err != nil {
				t.Skipf("filesystem symlinks are unavailable: %v", err)
			}
			holdWorkbook(t, filepath.Join(append([]string{aliasDir}, tc.rel...)...))

			requireWaits(t, filepath.Join(append([]string{realDir}, tc.rel...)...))
		})
	}
}

// A case variant is the same workbook only where the filesystem ignores case.
func TestPathLockCaseAlias(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	upper := filepath.Join(dir, "Book.xlsx")
	lower := filepath.Join(dir, "book.xlsx")
	require.NoError(t, os.WriteFile(upper, []byte("workbook"), 0o600))
	upperInfo, err := os.Lstat(upper)
	require.NoError(t, err)
	lowerInfo, lowerErr := os.Lstat(lower)
	holdWorkbook(t, upper)

	if lowerErr == nil && os.SameFile(upperInfo, lowerInfo) {
		requireWaits(t, lower)
		return
	}
	requireProceeds(t, lower)
}

// A write that gives up on one of its paths must not keep the others.
func TestPathLockPartialRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.xlsx")
	pathB := filepath.Join(dir, "b.xlsx")
	holdWorkbook(t, pathB)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := withLock(ctx, pathA, LockOptions{}, func() (*struct{}, error) {
		return nil, nil
	}, pathB)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	requireProceeds(t, pathA)
}

func TestPathLockLogsWait(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orders.xlsx")
	release := holdWorkbook(t, path)

	logged := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		opts := LockOptions{Log: func(msg string) { logged <- msg }}
		_, err := withLock(context.Background(), path, opts, func() (*struct{}, error) {
			return nil, nil
		})
		done <- err
	}()
	select {
	case msg := <-logged:
		assert.Equal(t, "orders.xlsx is being written by another step; waiting for it to finish", msg)
	case <-time.After(2 * time.Second):
		require.Fail(t, "a waiting write logged nothing")
	}

	release()
	require.NoError(t, <-done)
}

// holdWorkbook opens a write transaction on path that stays open until the
// returned release runs or the test ends.
func holdWorkbook(t *testing.T, path string) (release func()) {
	t.Helper()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := withLock(context.Background(), path, LockOptions{}, func() (*struct{}, error) {
			close(entered)
			<-unblock
			return nil, nil
		})
		done <- err
	}()
	<-entered

	var once sync.Once
	release = func() {
		once.Do(func() {
			close(unblock)
			require.NoError(t, <-done)
		})
	}
	t.Cleanup(release)
	return release
}

// requireWaits asserts that a write transaction on path cannot start while
// another one holds the workbook.
func requireWaits(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ran := false
	_, err := withLock(ctx, path, LockOptions{}, func() (*struct{}, error) {
		ran = true
		return nil, nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, ran)
}

// requireProceeds asserts that a write transaction on path starts without
// waiting for another one.
func requireProceeds(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := withLock(ctx, path, LockOptions{}, func() (*struct{}, error) {
		return nil, nil
	})
	require.NoError(t, err)
}
