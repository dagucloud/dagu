// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dirlock

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("ValidDirectory", func(t *testing.T) {
		lock := New("/tmp/test", nil)
		require.NotNil(t, lock)
	})

	t.Run("DefaultOptions", func(t *testing.T) {
		lock := New("/tmp/test", nil)

		dl := lock.(*dirLock)
		require.Equal(t, 30*time.Second, dl.opts.StaleThreshold)
		require.Equal(t, 50*time.Millisecond, dl.opts.RetryInterval)
	})

	t.Run("CustomOptions", func(t *testing.T) {
		opts := &LockOptions{
			StaleThreshold: 10 * time.Second,
			RetryInterval:  100 * time.Millisecond,
		}
		lock := New("/tmp/test", opts)

		dl := lock.(*dirLock)
		require.Equal(t, 10*time.Second, dl.opts.StaleThreshold)
		require.Equal(t, 100*time.Millisecond, dl.opts.RetryInterval)
	})
}

func TestRetryableLockStateError_WindowsAccessDenied(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific lock error")
	}

	err := errors.New(`CreateFile C:\locks\.dagu_lock: Access is denied.`)

	require.True(t, isRetryableLockStateError(err))
}

func TestTryLock(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("AcquireLockSuccessfully", func(t *testing.T) {
		lock := New(tmpDir, nil)

		err := lock.TryLock()
		require.NoError(t, err)
		require.True(t, lock.IsHeldByMe())
		require.True(t, lock.IsLocked())

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)
	})

	t.Run("LockConflict", func(t *testing.T) {
		lock1 := New(tmpDir, nil)
		lock2 := New(tmpDir, nil)

		// First lock succeeds
		err := lock1.TryLock()
		require.NoError(t, err)

		// Second lock fails
		err = lock2.TryLock()
		require.ErrorIs(t, err, ErrLockConflict)
		require.False(t, lock2.IsHeldByMe())

		// Cleanup
		err = lock1.Unlock()
		require.NoError(t, err)
	})

	t.Run("ReacquireAfterUnlock", func(t *testing.T) {
		lock := New(tmpDir, nil)

		// Acquire
		err := lock.TryLock()
		require.NoError(t, err)

		// Release
		err = lock.Unlock()
		require.NoError(t, err)

		// Reacquire
		err = lock.TryLock()
		require.NoError(t, err)

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)
	})

	t.Run("PermissionErrorsAreNotTreatedAsConflictsOutsideWindows", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows uses retryable sharing violation handling")
		}

		parentDir := filepath.Join(tmpDir, "permission-blocked")
		require.NoError(t, os.Mkdir(parentDir, 0o700))

		targetDir := filepath.Join(parentDir, "target")
		require.NoError(t, os.Mkdir(targetDir, 0o700))
		require.NoError(t, os.Chmod(parentDir, 0o600))
		t.Cleanup(func() {
			_ = os.Chmod(parentDir, 0o700)
		})

		lock := New(targetDir, nil)
		err := lock.TryLock()
		require.Error(t, err)
		require.ErrorIs(t, err, os.ErrPermission)
		require.NotErrorIs(t, err, ErrLockConflict)
	})
}

func TestLock(t *testing.T) {
	t.Run("AcquireImmediately", func(t *testing.T) {
		tmpDir := t.TempDir()
		lock := New(tmpDir, nil)

		ctx := context.Background()
		err := lock.Lock(ctx)
		require.NoError(t, err)
		require.True(t, lock.IsHeldByMe())

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)
	})

	t.Run("WaitForLock", func(t *testing.T) {
		tmpDir := t.TempDir()
		waiting := make(chan struct{})
		lock1 := New(tmpDir, &LockOptions{
			RetryInterval: 10 * time.Millisecond,
		})

		lock2 := New(tmpDir, &LockOptions{
			RetryInterval: 10 * time.Millisecond,
			OnWait: func() {
				close(waiting)
			},
		})

		// First lock acquired
		err := lock1.TryLock()
		require.NoError(t, err)

		ctx := t.Context()

		lockErr := make(chan error, 1)
		go func() {
			lockErr <- lock2.Lock(ctx)
		}()

		<-waiting
		require.NoError(t, lock1.Unlock())
		require.NoError(t, <-lockErr)

		// Cleanup
		err = lock2.Unlock()
		require.NoError(t, err)
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		tmpDir := t.TempDir()
		lock1 := New(tmpDir, nil)
		lock2 := New(tmpDir, nil)

		// First lock acquired
		err := lock1.TryLock()
		require.NoError(t, err)

		// Try to acquire with a canceled context.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = lock2.Lock(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, lock2.IsHeldByMe())

		// Cleanup
		err = lock1.Unlock()
		require.NoError(t, err)
	})
}

func TestUnlock(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("UnlockHeldLock", func(t *testing.T) {
		lock := New(tmpDir, nil)

		err := lock.TryLock()
		require.NoError(t, err)

		err = lock.Unlock()
		require.NoError(t, err)
		require.False(t, lock.IsHeldByMe())
		require.False(t, lock.IsLocked())
	})

	t.Run("UnlockNotHeld", func(t *testing.T) {
		lock := New(tmpDir, nil)

		err := lock.Unlock()
		require.NoError(t, err)
	})

	t.Run("DoubleUnlock", func(t *testing.T) {
		lock := New(tmpDir, nil)

		err := lock.TryLock()
		require.NoError(t, err)

		err = lock.Unlock()
		require.NoError(t, err)

		err = lock.Unlock()
		require.NoError(t, err)
	})
}

func TestIsLocked(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("NoLock", func(t *testing.T) {
		lock := New(tmpDir, nil)
		require.False(t, lock.IsLocked())
	})

	t.Run("WithLock", func(t *testing.T) {
		lock1 := New(tmpDir, nil)
		lock2 := New(tmpDir, nil)

		err := lock1.TryLock()
		require.NoError(t, err)

		require.True(t, lock1.IsLocked())
		require.True(t, lock2.IsLocked())

		err = lock1.Unlock()
		require.NoError(t, err)

		require.False(t, lock1.IsLocked())
		require.False(t, lock2.IsLocked())
	})
}

func TestStaleDetection(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("CleanStaleLock", func(t *testing.T) {
		// Create a stale lock manually
		lockPath := filepath.Join(tmpDir, ".dagu_lock")
		err := os.Mkdir(lockPath, 0700)
		require.NoError(t, err)

		// Set modification time to past
		pastTime := time.Now().Add(-60 * time.Second)
		err = os.Chtimes(lockPath, pastTime, pastTime)
		require.NoError(t, err)

		lock := New(tmpDir, &LockOptions{
			StaleThreshold: 30 * time.Second,
		})

		// TryLock should clean up stale lock and succeed
		err = lock.TryLock()
		require.NoError(t, err)

		// Verify lock is held
		require.True(t, lock.IsHeldByMe())

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)
	})
}

func TestRemoveLockDir(t *testing.T) {
	t.Run("WithOwnerFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, ".dagu_lock")
		require.NoError(t, os.Mkdir(lockPath, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(lockPath, lockOwnerFileName), []byte("token"), 0600))

		require.NoError(t, removeLockDir(lockPath))

		_, err := os.Stat(lockPath)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("EmptyLockDir", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, ".dagu_lock")
		require.NoError(t, os.Mkdir(lockPath, 0700))

		require.NoError(t, removeLockDir(lockPath))

		_, err := os.Stat(lockPath)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("MissingLockDir", func(t *testing.T) {
		tmpDir := t.TempDir()
		lockPath := filepath.Join(tmpDir, ".dagu_lock")

		require.NoError(t, removeLockDir(lockPath))
	})
}

func TestForceUnlock(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("ForceUnlockExistingLock", func(t *testing.T) {
		lock := New(tmpDir, nil)

		err := lock.TryLock()
		require.NoError(t, err)
		require.True(t, lock.IsLocked())

		err = ForceUnlock(tmpDir)
		require.NoError(t, err)
		require.False(t, lock.IsLocked())
	})

	t.Run("ForceUnlockEmptyDirectory", func(t *testing.T) {
		err := ForceUnlock(tmpDir)
		require.NoError(t, err)
	})
}

func TestConcurrency(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("MultipleGoroutinesCompeting", func(t *testing.T) {
		const numGoroutines = 10
		const numIterations = 5

		var wg sync.WaitGroup
		successCount := make([]int, numGoroutines)

		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				lock := New(tmpDir, &LockOptions{
					RetryInterval: 5 * time.Millisecond,
				})

				for range numIterations {
					ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					if err := lock.Lock(ctx); err == nil {
						successCount[id]++
						time.Sleep(2 * time.Millisecond) // Simulate work
						_ = lock.Unlock()
					}
					cancel()
				}
			}(i)
		}

		wg.Wait()

		// Verify that locks were acquired
		totalSuccess := 0
		for _, count := range successCount {
			totalSuccess += count
		}
		require.Greater(t, totalSuccess, 0)
	})
}

func TestHeartbeat(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("HeartbeatUpdatesLockTimestamp", func(t *testing.T) {
		lock := New(tmpDir, nil)

		// Acquire lock
		err := lock.TryLock()
		require.NoError(t, err)

		// Get initial lock info
		info1, err := lock.Info()
		require.NoError(t, err)
		require.NotNil(t, info1)
		initialTime := info1.AcquiredAt

		// Wait a bit to ensure timestamp difference
		time.Sleep(10 * time.Millisecond)

		// Heartbeat
		err = lock.Heartbeat(context.Background())
		require.NoError(t, err)

		// Get updated lock info
		info2, err := lock.Info()
		require.NoError(t, err)
		require.NotNil(t, info2)

		// Verify timestamp was updated
		require.True(t, info2.AcquiredAt.After(initialTime))

		// Verify lock is still held
		require.True(t, lock.IsHeldByMe())

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)
	})

	t.Run("HeartbeatWithoutLockFails", func(t *testing.T) {
		lock := New(tmpDir, nil)

		err := lock.Heartbeat(context.Background())
		require.ErrorIs(t, err, ErrNotLocked)
	})

	t.Run("ConcurrentHeartbeatAndCheck", func(t *testing.T) {
		// Use a different temp dir to avoid conflicts
		isolatedDir := t.TempDir()
		lock := New(isolatedDir, nil)

		err := lock.TryLock()
		require.NoError(t, err)

		// Run heartbeat and checks concurrently
		done := make(chan bool)
		errCh := make(chan error, 1)
		go func() {
			for range 5 {
				err := lock.Heartbeat(context.Background())
				if err != nil {
					errCh <- err
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			done <- true
		}()

		// Check lock status while heartbeat is running
		for range 5 {
			require.True(t, lock.IsLocked())
			require.True(t, lock.IsHeldByMe())
			time.Sleep(5 * time.Millisecond)
		}

		select {
		case err := <-errCh:
			t.Fatalf("Heartbeat failed: %v", err)
		case <-done:
			// Success
		}

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)
	})
}

func TestFenceToken_LostOwnerDoesNotDeleteReplacementLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".dagu_lock")

	lockA := New(tmpDir, nil)
	require.NoError(t, lockA.TryLock())

	// Simulate lock theft: remove lockA's lock, then lockB acquires
	require.NoError(t, os.RemoveAll(lockPath))

	lockB := New(tmpDir, nil)
	require.NoError(t, lockB.TryLock())

	// lockA's heartbeat must detect the token mismatch
	err := lockA.Heartbeat(context.Background())
	require.ErrorIs(t, err, ErrLockNotHeld)

	// lockA's Unlock must NOT remove lockB's lock directory.
	// Since Heartbeat already cleared isHeld, Unlock returns nil (no-op).
	err = lockA.Unlock()
	require.NoError(t, err)

	// Lock directory must still exist with lockB's token
	_, statErr := os.Stat(lockPath)
	require.NoError(t, statErr, "lock directory should still exist")

	// lockB should still be healthy
	require.NoError(t, lockB.Heartbeat(context.Background()))
	require.NoError(t, lockB.Unlock())

	_, statErr = os.Stat(lockPath)
	require.True(t, os.IsNotExist(statErr), "lock directory should be removed after lockB unlocks")
}

func TestFenceToken_UnlockDetectsTokenMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".dagu_lock")

	lockA := New(tmpDir, nil)
	require.NoError(t, lockA.TryLock())

	// Simulate lock theft without calling Heartbeat first
	require.NoError(t, os.RemoveAll(lockPath))
	lockB := New(tmpDir, nil)
	require.NoError(t, lockB.TryLock())

	// lockA calls Unlock directly (isHeld is still true) — must detect token mismatch
	err := lockA.Unlock()
	require.ErrorIs(t, err, ErrLockNotHeld)

	// lockB's lock must still be intact
	_, statErr := os.Stat(lockPath)
	require.NoError(t, statErr)

	require.NoError(t, lockB.Unlock())
}

func TestFenceToken_UnlockDoesNotDeleteOwnerlessReplacementLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".dagu_lock")

	lockA := New(tmpDir, nil)
	require.NoError(t, lockA.TryLock())

	// Model the replacement lock in the window after recreating the directory
	// but before writing the owner token file.
	require.NoError(t, os.RemoveAll(lockPath))
	require.NoError(t, os.Mkdir(lockPath, 0700))

	err := lockA.Unlock()
	require.ErrorIs(t, err, ErrLockNotHeld)

	_, statErr := os.Stat(lockPath)
	require.NoError(t, statErr, "ownerless replacement lock directory must not be removed")
}

func TestFenceToken_IsHeldByMeDetectsStolenLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".dagu_lock")

	lockA := New(tmpDir, nil)
	require.NoError(t, lockA.TryLock())
	require.True(t, lockA.IsHeldByMe())

	// Simulate lock theft
	require.NoError(t, os.RemoveAll(lockPath))
	lockB := New(tmpDir, nil)
	require.NoError(t, lockB.TryLock())

	require.False(t, lockA.IsHeldByMe())
	require.True(t, lockB.IsHeldByMe())

	require.NoError(t, lockB.Unlock())
}

func TestIsLocked_NoLongerRemovesStaleLocks(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".dagu_lock")

	lockA := New(tmpDir, &LockOptions{StaleThreshold: 1 * time.Second})
	require.NoError(t, lockA.TryLock())

	// Backdate the lock directory to exceed the stale threshold
	pastTime := time.Now().Add(-60 * time.Second)
	require.NoError(t, os.Chtimes(lockPath, pastTime, pastTime))

	// IsLocked should report false (stale) but NOT remove the directory
	require.False(t, lockA.IsLocked())

	_, statErr := os.Stat(lockPath)
	require.NoError(t, statErr, "IsLocked must not remove the lock directory")

	// TryLock from a new instance should still clean stale locks and succeed
	lockB := New(tmpDir, &LockOptions{StaleThreshold: 1 * time.Second})
	require.NoError(t, lockB.TryLock())
	require.NoError(t, lockB.Unlock())
}

func TestEdgeCases(t *testing.T) {
	t.Run("NonExistentDirectory", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "non-existent")
		lock := New(nonExistentDir, nil)

		// Should succeed and create the directory
		err := lock.TryLock()
		require.NoError(t, err)

		// Verify directory was created
		_, err = os.Stat(nonExistentDir)
		require.NoError(t, err)

		err = lock.Unlock()
		require.NoError(t, err)
	})

	t.Run("InfoReturnsCorrectData", func(t *testing.T) {
		tmpDir := t.TempDir()
		lock := New(tmpDir, nil)

		// No lock initially
		info, err := lock.Info()
		require.NoError(t, err)
		require.Nil(t, info)

		// Acquire lock
		err = lock.TryLock()
		require.NoError(t, err)

		// Get info
		info, err = lock.Info()
		require.NoError(t, err)
		require.NotNil(t, info)
		require.Equal(t, ".dagu_lock", info.LockDirName)
		require.WithinDuration(t, time.Now(), info.AcquiredAt, 1*time.Second)

		// Cleanup
		err = lock.Unlock()
		require.NoError(t, err)

		// No lock after unlock
		info, err = lock.Info()
		require.NoError(t, err)
		require.Nil(t, info)
	})
}

// staleLockKinds builds the stale lock layouts a contender may find: one left
// by a crashed holder, an empty directory, and the legacy owner-file layout.
// mayAllBackOff marks layouts where contenders racing at the same moment can
// all back off: each one that removes an empty stale directory may remove
// another's new lock directory before its owner file lands. The next attempt
// then finds the lock free.
var staleLockKinds = []struct {
	name          string
	create        func(t *testing.T, dir string)
	mayAllBackOff bool
}{
	{
		name: "AbandonedHolder",
		create: func(t *testing.T, dir string) {
			t.Helper()
			require.NoError(t, New(dir, nil).TryLock())
			backdateLock(t, dir)
		},
	},
	{
		name: "EmptyDir",
		create: func(t *testing.T, dir string) {
			t.Helper()
			require.NoError(t, os.Mkdir(filepath.Join(dir, LockDirectoryName), 0o700))
			backdateLock(t, dir)
		},
		mayAllBackOff: true,
	},
	{
		name: "LegacyOwnerFile",
		create: func(t *testing.T, dir string) {
			t.Helper()
			lockPath := filepath.Join(dir, LockDirectoryName)
			require.NoError(t, os.Mkdir(lockPath, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(lockPath, "owner"), []byte("legacy"), 0o600))
			backdateLock(t, dir)
		},
	},
}

// junkOnlyStaleLock has no owner or claim file. Contenders that claim it at the
// same moment may all back off, so it is left out of the many-contender test.
var junkOnlyStaleLock = struct {
	name          string
	create        func(t *testing.T, dir string)
	mayAllBackOff bool
}{
	name: "JunkOnly",
	create: func(t *testing.T, dir string) {
		t.Helper()
		lockPath := filepath.Join(dir, LockDirectoryName)
		require.NoError(t, os.MkdirAll(filepath.Join(lockPath, "unexpected"), 0o700))
		backdateLock(t, dir)
	},
	mayAllBackOff: true,
}

// A single contender takes over every stale layout and leaves no temporary
// lock directories behind.
func TestStaleTakeover_SingleContender(t *testing.T) {
	for _, tt := range append(staleLockKinds, junkOnlyStaleLock) {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.create(t, dir)

			lock := New(dir, nil)
			require.NoError(t, lock.TryLock())
			require.True(t, lock.IsHeldByMe())
			require.NoError(t, lock.Unlock())

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func backdateLock(t *testing.T, dir string) {
	t.Helper()
	past := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(dir, LockDirectoryName), past, past))
}

// Two contenders observe the same stale lock. The first replaces it before the
// second claims it; the second must not take over the fresh lock.
func TestStaleTakeover_InterleavedContenders(t *testing.T) {
	for _, tt := range append(staleLockKinds, junkOnlyStaleLock) {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.create(t, dir)

			first := New(dir, nil)
			second := New(dir, nil).(*dirLock)
			var firstErr error
			second.beforeStaleClaim = func() {
				firstErr = first.TryLock()
			}

			secondErr := second.TryLock()

			require.NoError(t, firstErr)
			require.ErrorIs(t, secondErr, ErrLockConflict)
			require.True(t, first.IsHeldByMe())
			require.False(t, second.IsHeldByMe())
			require.NoError(t, first.Heartbeat(context.Background()))
			require.NoError(t, first.Unlock())
		})
	}
}

// Many contenders race on one stale lock with random delays between observing
// it and claiming it. Exactly one may win, or none where all may back off.
func TestStaleTakeover_ManyContenders(t *testing.T) {
	const contenders = 16
	const rounds = 20

	for _, tt := range staleLockKinds {
		t.Run(tt.name, func(t *testing.T) {
			for range rounds {
				dir := t.TempDir()
				tt.create(t, dir)

				locks := make([]*dirLock, contenders)
				for i := range locks {
					locks[i] = New(dir, nil).(*dirLock)
					locks[i].beforeStaleClaim = func() {
						time.Sleep(time.Duration(rand.IntN(2000)) * time.Microsecond)
					}
				}

				var wg sync.WaitGroup
				var winners atomic.Int32
				errs := make([]error, contenders)
				start := make(chan struct{})
				for i, lock := range locks {
					wg.Go(func() {
						<-start
						errs[i] = lock.TryLock()
						if errs[i] == nil {
							winners.Add(1)
						}
					})
				}
				close(start)
				wg.Wait()

				for _, err := range errs {
					if err != nil {
						require.ErrorIs(t, err, ErrLockConflict)
					}
				}
				if tt.mayAllBackOff {
					require.LessOrEqual(t, winners.Load(), int32(1))
				} else {
					require.Equal(t, int32(1), winners.Load())
				}
				held := 0
				for _, lock := range locks {
					if lock.IsHeldByMe() {
						held++
						require.NoError(t, lock.Unlock())
					}
				}
				require.Equal(t, int(winners.Load()), held)
				if winners.Load() == 0 {
					next := New(dir, nil)
					require.NoError(t, next.TryLock())
					require.NoError(t, next.Unlock())
				}
			}
		})
	}
}

// A contender stalls after claiming a lock instance for removal. Meanwhile the
// claimed instance looks stale again, another contender takes it over and
// creates a new lock. The stalled contender must not touch the new lock: it
// must not even move it off the live lock name, because a third contender
// could take the free name. Freezing the target directory's mtime detects
// any rename on the live lock name.
func TestStaleTakeover_StalledClaimer(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string, stalled *dirLock)
		act     func(stalled *dirLock) error
		wantErr error
	}{
		{
			name: "TryLock",
			setup: func(t *testing.T, dir string, _ *dirLock) {
				staleLockKinds[0].create(t, dir)
			},
			act:     func(stalled *dirLock) error { return stalled.TryLock() },
			wantErr: ErrLockConflict,
		},
		{
			name: "Unlock",
			setup: func(t *testing.T, _ string, stalled *dirLock) {
				require.NoError(t, stalled.TryLock())
			},
			act:     func(stalled *dirLock) error { return stalled.Unlock() },
			wantErr: ErrLockNotHeld,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			stalled := New(dir, nil).(*dirLock)
			tt.setup(t, dir, stalled)

			takeover := New(dir, nil)
			frozen := time.Now().Add(-time.Hour).Truncate(time.Second)
			var takeoverErr error
			stalled.afterClaim = func() {
				backdateLock(t, dir)
				takeoverErr = takeover.TryLock()
				require.NoError(t, os.Chtimes(dir, frozen, frozen))
			}

			err := tt.act(stalled)

			require.NoError(t, takeoverErr)
			require.ErrorIs(t, err, tt.wantErr)
			info, statErr := os.Stat(dir)
			require.NoError(t, statErr)
			require.True(t, info.ModTime().Equal(frozen), "live lock name was renamed by the stalled contender")
			require.True(t, takeover.IsHeldByMe())
			require.False(t, stalled.IsHeldByMe())
			require.NoError(t, takeover.Unlock())
		})
	}
}

// A holder whose stale lock was taken over must not release the new lock,
// whichever ownership check it runs first.
func TestUnlock_AfterStaleTakeover(t *testing.T) {
	tests := []struct {
		name       string
		before     func(t *testing.T, old DirLock)
		wantUnlock error
	}{
		{
			name:       "UnlockOnly",
			before:     func(*testing.T, DirLock) {},
			wantUnlock: ErrLockNotHeld,
		},
		{
			name: "HeartbeatFirst",
			before: func(t *testing.T, old DirLock) {
				require.ErrorIs(t, old.Heartbeat(context.Background()), ErrLockNotHeld)
			},
		},
		{
			name: "IsHeldByMeFirst",
			before: func(t *testing.T, old DirLock) {
				require.False(t, old.IsHeldByMe())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			old := New(dir, nil)
			require.NoError(t, old.TryLock())
			backdateLock(t, dir)
			current := New(dir, nil)
			require.NoError(t, current.TryLock())

			tt.before(t, old)
			err := old.Unlock()
			if tt.wantUnlock == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantUnlock)
			}

			require.True(t, current.IsHeldByMe())
			require.NoError(t, current.Heartbeat(context.Background()))
			require.NoError(t, current.Unlock())
			require.NoDirExists(t, filepath.Join(dir, LockDirectoryName))
		})
	}
}

// Errors from claim renames that mean another process moved or removed the
// lock directory first. macOS reports EINVAL in that case.
func TestIsLostClaimError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "Nil", err: nil, want: false},
		{name: "NotExist", err: &os.LinkError{Op: "rename", Err: syscall.ENOENT}, want: true},
		{name: "InvalidArgument", err: &os.LinkError{Op: "rename", Err: syscall.EINVAL}, want: true},
		{name: "Permission", err: &os.LinkError{Op: "rename", Err: syscall.EACCES}, want: false},
		{name: "Other", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isLostClaimError(tt.err))
		})
	}
}

// Taking over a stale lock also removes temporary lock directories left by
// crashed processes, and only those.
func TestStaleTakeover_CleansAbandonedTempDirs(t *testing.T) {
	tests := []struct {
		name     string
		entry    string
		old      bool
		wantKept bool
	}{
		{name: "OldReleasing", entry: releasedLockDirectoryPrefix + "1.x", old: true, wantKept: false},
		{name: "FreshReleasing", entry: releasedLockDirectoryPrefix + "1.x", old: false, wantKept: true},
		{name: "OldUnrelated", entry: "data", old: true, wantKept: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.entry)
			require.NoError(t, os.MkdirAll(filepath.Join(path, "sub"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(path, "sub", "file"), []byte("x"), 0o600))
			if tt.old {
				past := time.Now().Add(-time.Hour)
				require.NoError(t, os.Chtimes(path, past, past))
			}
			staleLockKinds[0].create(t, dir)

			lock := New(dir, nil)
			require.NoError(t, lock.TryLock())
			defer func() { require.NoError(t, lock.Unlock()) }()

			if tt.wantKept {
				require.DirExists(t, path)
			} else {
				require.NoDirExists(t, path)
			}
		})
	}
}

// A takeover sweeps directories left by earlier releases, which may take a
// while. The sweep must not eat into the new lock's lease: right after TryLock
// returns, the lock must not look stale to other contenders.
func TestStaleTakeover_SlowSweep(t *testing.T) {
	const threshold = 200 * time.Millisecond
	dir := t.TempDir()
	require.NoError(t, New(dir, nil).TryLock())
	backdateLock(t, dir)

	first := New(dir, &LockOptions{StaleThreshold: threshold}).(*dirLock)
	first.beforeSweep = func() { time.Sleep(2 * threshold) }
	require.NoError(t, first.TryLock())

	second := New(dir, &LockOptions{StaleThreshold: threshold})
	require.ErrorIs(t, second.TryLock(), ErrLockConflict)
	require.True(t, first.IsHeldByMe())
	require.NoError(t, first.Unlock())
}

// When the owner file cannot be written, the new lock directory must not be
// left behind: once the cause is fixed, the next attempt acquires the lock
// without waiting for it to go stale.
func TestCreateLock_OwnerWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny file creation here")
	}
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockDirectoryName)
	lock := New(dir, nil).(*dirLock)
	lock.beforeOwnerWrite = func() {
		require.NoError(t, os.Chmod(lockPath, 0o500))
	}

	require.ErrorIs(t, lock.TryLock(), os.ErrPermission)
	require.False(t, lock.IsHeldByMe())
	_ = os.Chmod(lockPath, 0o700)

	next := New(dir, nil)
	require.NoError(t, next.TryLock())
	require.NoError(t, next.Unlock())
}

// The lock directory is empty until its owner file is written, so stale-lock
// recovery may remove it, and another contender may create or claim the
// directory at that path in the meantime. The creator must back off and leave
// the other contender's files alone.
func TestCreateLock_Contested(t *testing.T) {
	const otherOwner = ownerFilePrefix + "other"
	const otherClaim = claimedFilePrefix + "other"
	tests := []struct {
		name    string
		contend func(t *testing.T, lockPath string)
		want    []string // entries left at the lock path; nil means no directory
	}{
		{
			name: "Removed",
			contend: func(t *testing.T, lockPath string) {
				require.NoError(t, os.Remove(lockPath))
			},
		},
		{
			name: "Replaced",
			contend: func(t *testing.T, lockPath string) {
				require.NoError(t, os.Remove(lockPath))
				require.NoError(t, os.Mkdir(lockPath, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(lockPath, otherOwner), nil, 0o600))
			},
			want: []string{otherOwner},
		},
		{
			name: "Claimed",
			contend: func(t *testing.T, lockPath string) {
				require.NoError(t, os.WriteFile(filepath.Join(lockPath, otherClaim), nil, 0o600))
			},
			want: []string{otherClaim},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			lockPath := filepath.Join(dir, LockDirectoryName)
			lock := New(dir, nil).(*dirLock)
			lock.beforeOwnerWrite = func() { tt.contend(t, lockPath) }

			require.ErrorIs(t, lock.TryLock(), ErrLockConflict)
			require.False(t, lock.IsHeldByMe())
			if tt.want == nil {
				require.NoDirExists(t, lockPath)
				return
			}
			entries, err := os.ReadDir(lockPath)
			require.NoError(t, err)
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			require.Equal(t, tt.want, names)
		})
	}
}

// Releases before the per-instance owner file create the lock directory empty
// and write "owner" into it afterwards. Creating a new lock must stay exclusive
// against that, or both versions hold the lock at once. Old and new binaries
// share locks between an in-place upgrade and the next restart.
func TestTryLock_LegacyContenders(t *testing.T) {
	const contenders = 6 // per version
	const increments = 60
	const retryInterval = 100 * time.Microsecond

	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")
	increment := func() {
		data, _ := os.ReadFile(counter)
		n, _ := strconv.Atoi(string(data))
		time.Sleep(time.Duration(rand.IntN(300)) * time.Microsecond)
		_ = os.WriteFile(counter, []byte(strconv.Itoa(n+1)), 0o600)
	}
	acquire := func(tryLock func() bool) {
		for !tryLock() {
			time.Sleep(retryInterval)
		}
	}

	var wg sync.WaitGroup
	for range contenders {
		wg.Go(func() {
			legacy := legacyLock{lockPath: filepath.Join(dir, LockDirectoryName), token: newUniqueID()}
			for range increments {
				acquire(legacy.tryLock)
				increment()
				legacy.unlock()
			}
		})
		wg.Go(func() {
			lock := New(dir, nil)
			for range increments {
				acquire(func() bool { return lock.TryLock() == nil })
				increment()
				_ = lock.Unlock()
			}
		})
	}
	wg.Wait()

	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(2*contenders*increments), string(data))
}

// legacyLock acquires and releases a fresh lock the way releases before the
// per-instance owner file did.
type legacyLock struct {
	lockPath string
	token    string
}

func (l legacyLock) tryLock() bool {
	if _, err := os.Stat(l.lockPath); !os.IsNotExist(err) {
		return false
	}
	if err := os.Mkdir(l.lockPath, 0o700); err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(l.lockPath, lockOwnerFileName), []byte(l.token), 0o600); err != nil {
		_ = removeLockDir(l.lockPath)
		return false
	}
	return true
}

func (l legacyLock) unlock() {
	data, err := os.ReadFile(filepath.Join(l.lockPath, lockOwnerFileName))
	if err != nil || string(data) != l.token {
		return
	}
	_ = removeLockDir(l.lockPath)
}

// A read-only entry inside the lock directory must not keep the lock name
// occupied. Releasing a lock, by its holder or by a stale takeover, deletes it
// and frees the name for the next contender.
func TestRelease_ReadOnlyEntry(t *testing.T) {
	tests := []struct {
		name    string
		release func(t *testing.T, dir string)
	}{
		{
			name: "Unlock",
			release: func(t *testing.T, dir string) {
				holder := New(dir, nil)
				require.NoError(t, holder.TryLock())
				addReadOnlyEntry(t, dir)
				require.NoError(t, holder.Unlock())
			},
		},
		{
			name: "StaleTakeover",
			release: func(t *testing.T, dir string) {
				require.NoError(t, New(dir, nil).TryLock())
				addReadOnlyEntry(t, dir)
				backdateLock(t, dir)
				takeover := New(dir, nil)
				require.NoError(t, takeover.TryLock())
				require.NoError(t, takeover.Unlock())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.release(t, dir)

			next := New(dir, nil)
			require.NoError(t, next.TryLock())
			require.NoError(t, next.Unlock())
		})
	}
}

// addReadOnlyEntry puts a read-only directory with a file inside the lock
// directory, so the file cannot be deleted until the directory is writable.
func addReadOnlyEntry(t *testing.T, dir string) {
	t.Helper()
	readOnly := filepath.Join(dir, LockDirectoryName, "read-only")
	require.NoError(t, os.Mkdir(readOnly, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(readOnly, "file"), nil, 0o600))
	require.NoError(t, os.Chmod(readOnly, 0o500))
	restoreAccessOnCleanup(t, dir)
}

// restoreAccessOnCleanup gives the owner access back to every directory under
// dir at cleanup, wherever a test's restricted directories ended up, so the
// temporary directory can be removed.
func restoreAccessOnCleanup(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(dir, LockDirectoryName), 0o700)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
}

// A stale lock directory this user owns may have lost permissions, for example
// through a restore that did not keep modes. Taking it over must still work,
// as it does when older releases move the directory aside.
func TestStaleTakeover_RestrictedLockDir(t *testing.T) {
	for _, mode := range []os.FileMode{0o000, 0o300, 0o500} {
		t.Run(strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, New(dir, nil).TryLock())
			lockPath := filepath.Join(dir, LockDirectoryName)
			require.NoError(t, os.Chmod(lockPath, mode))
			restoreAccessOnCleanup(t, dir)
			backdateLock(t, dir)

			lock := New(dir, nil)
			require.NoError(t, lock.TryLock())
			require.NoError(t, lock.Unlock())
		})
	}
}

// A held lock whose directory lost owner access must still be released at
// once, as older releases do by moving the directory aside, rather than block
// the next holder until the lock goes stale.
func TestUnlock_RestrictedLockDir(t *testing.T) {
	for _, mode := range []os.FileMode{0o000, 0o300, 0o500} {
		t.Run(strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			dir := t.TempDir()
			holder := New(dir, nil)
			require.NoError(t, holder.TryLock())
			require.NoError(t, os.Chmod(filepath.Join(dir, LockDirectoryName), mode))
			restoreAccessOnCleanup(t, dir)

			require.NoError(t, holder.Unlock())

			next := New(dir, nil)
			require.NoError(t, next.TryLock())
			require.NoError(t, next.Unlock())
		})
	}
}
