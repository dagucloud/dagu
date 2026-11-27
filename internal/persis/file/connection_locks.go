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

var (
	_ connection.Locks        = (*connectionLocks)(nil)
	_ connection.CatalogLocks = catalogLocks{}
)

// connectionLocks keeps one dirlock per connection, so a refresh that hangs
// at one provider never holds back another connection.
type connectionLocks struct {
	dir            string
	staleThreshold time.Duration
}

// catalogLocks keeps one dirlock per tool catalog, in a directory apart
// from the connections' locks.
type catalogLocks struct {
	locks *connectionLocks
}

const (
	// connectionLocksDirName holds the lock directories of connections
	// under the data directory.
	connectionLocksDirName = "connection-locks"
	// catalogLocksDirName holds the lock directories of tool catalogs under
	// the data directory.
	catalogLocksDirName = "connection-catalog-locks"
)

// ConnectionLocks returns the per-connection locks that the token broker
// refreshes under. Every process that shares the data directory gets the
// same locks.
func (s *ServerStores) ConnectionLocks() connection.Locks {
	return s.connectionLocks(connectionLocksDirName)
}

// ConnectionCatalogLocks returns the per-catalog locks that tool catalogs
// are listed under. Every process that shares the data directory gets the
// same locks, and none of them is a lock of ConnectionLocks.
func (s *ServerStores) ConnectionCatalogLocks() connection.CatalogLocks {
	return catalogLocks{locks: s.connectionLocks(catalogLocksDirName)}
}

func (s *ServerStores) connectionLocks(dirName string) *connectionLocks {
	return &connectionLocks{
		dir:            filepath.Join(s.cfg.Paths.DataDir, dirName),
		staleThreshold: lockStaleThreshold,
	}
}

func (l *connectionLocks) TryLock(ctx context.Context, key connection.Key) (connection.Lock, error) {
	return l.tryLock(ctx, key.Workspace+"/"+key.Name)
}

func (l *connectionLocks) WaitUnlocked(ctx context.Context, key connection.Key) error {
	return l.waitUnlocked(ctx, key.Workspace+"/"+key.Name)
}

func (l *connectionLocks) StaleThreshold() time.Duration {
	return l.staleThreshold
}

func (l catalogLocks) TryLock(ctx context.Context, key connection.Key, service string) (connection.Lock, error) {
	return l.locks.tryLock(ctx, catalogLockID(key, service))
}

func (l catalogLocks) WaitUnlocked(ctx context.Context, key connection.Key, service string) error {
	return l.locks.waitUnlocked(ctx, catalogLockID(key, service))
}

// catalogLockID separates its parts with "/", which no workspace,
// connection, or service name holds.
func catalogLockID(key connection.Key, service string) string {
	return key.Workspace + "/" + key.Name + "/" + service
}

func (l *connectionLocks) tryLock(ctx context.Context, id string) (connection.Lock, error) {
	lock := l.lock(id)
	if err := lock.TryLock(); err != nil {
		if errors.Is(err, dirlock.ErrLockConflict) {
			return nil, connection.ErrLocked
		}
		return nil, err
	}
	held, release := renewHeld(ctx, lock, l.staleThreshold, 0)
	return connectionLock{held: held, release: release}, nil
}

func (l *connectionLocks) waitUnlocked(ctx context.Context, id string) error {
	lock := l.lock(id)
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

// lock returns the dirlock of id. Its directory is named by a hash of id,
// which keeps names that differ only in case apart on a file system that
// ignores case.
func (l *connectionLocks) lock(id string) dirlock.DirLock {
	sum := sha256.Sum256([]byte(id))
	dir := filepath.Join(l.dir, hex.EncodeToString(sum[:16]))
	return dirlock.New(dir, &dirlock.LockOptions{StaleThreshold: l.staleThreshold})
}

type connectionLock struct {
	held    func() error
	release func()
}

func (l connectionLock) Held() error { return l.held() }

func (l connectionLock) Unlock() { l.release() }
