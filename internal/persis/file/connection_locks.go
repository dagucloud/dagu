// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

// unlockPollInterval is how often a waiter checks whether a connection's
// lock was released.
const unlockPollInterval = 50 * time.Millisecond

var _ connection.Locks = (*connectionLocks)(nil)

// connectionLocks keeps one dirlock per connection, so a refresh that hangs
// at one provider never holds back another connection.
type connectionLocks struct {
	dir            string
	staleThreshold time.Duration
}

// connectionLocksDirName holds the lock directories of connections under
// the data directory.
const connectionLocksDirName = "connection-locks"

// ConnectionLocks returns the per-connection locks that the token broker
// refreshes under. Every process that shares the data directory gets the
// same locks.
func (s *ServerStores) ConnectionLocks() connection.Locks {
	return &connectionLocks{
		dir:            filepath.Join(s.cfg.Paths.DataDir, connectionLocksDirName),
		staleThreshold: lockStaleThreshold,
	}
}

func (l *connectionLocks) TryLock(ctx context.Context, key connection.Key) (connection.Lock, error) {
	lock := l.lock(key)
	if err := lock.TryLock(); err != nil {
		if errors.Is(err, dirlock.ErrLockConflict) {
			return nil, connection.ErrLocked
		}
		return nil, err
	}
	held, release := renewHeld(ctx, lock, l.staleThreshold, 0)
	return connectionLock{held: held, release: release}, nil
}

func (l *connectionLocks) WaitUnlocked(ctx context.Context, key connection.Key) error {
	lock := l.lock(key)
	ticker := time.NewTicker(unlockPollInterval)
	defer ticker.Stop()
	for lock.IsLocked() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

func (l *connectionLocks) StaleThreshold() time.Duration {
	return l.staleThreshold
}

// lock returns the dirlock of key. Its directory is named by a hash of the
// key, which keeps names that differ only in case apart on a file system
// that ignores case.
func (l *connectionLocks) lock(key connection.Key) dirlock.DirLock {
	sum := sha256.Sum256([]byte(key.Workspace + "/" + key.Name))
	dir := filepath.Join(l.dir, hex.EncodeToString(sum[:16]))
	return dirlock.New(dir, &dirlock.LockOptions{StaleThreshold: l.staleThreshold})
}

type connectionLock struct {
	held    func() error
	release func()
}

func (l connectionLock) Held() error { return l.held() }

func (l connectionLock) Unlock() { l.release() }
