// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
)

const (
	profilesDirName   = "profiles"
	profileLockSuffix = ".lock"
	profileDirMode    = 0o700
	// profileHeartbeatInterval keeps the lock well inside dirlock's
	// staleness threshold.
	profileHeartbeatInterval = 10 * time.Second
)

// profileLease holds exclusive use of a persistent browser profile. Chrome
// cannot share a profile directory between processes, so runs using the same
// profile wait for each other.
type profileLease struct {
	dir  string
	lock dirlock.DirLock
	stop context.CancelFunc
}

// acquireProfile waits for the named profile. It fails fast when a run that
// is waiting for input still holds the profile's browser open.
func acquireProfile(ctx context.Context, browserDir, name string, store *browserhost.Store, ownRecordID string) (*profileLease, error) {
	profilesDir := filepath.Join(browserDir, profilesDirName)
	dir := filepath.Join(profilesDir, name)
	lockDir := dir + profileLockSuffix
	if err := os.MkdirAll(dir, profileDirMode); err != nil {
		return nil, fmt.Errorf("create browser profile %q: %w", name, err)
	}
	if err := os.MkdirAll(lockDir, profileDirMode); err != nil {
		return nil, fmt.Errorf("create browser profile lock %q: %w", name, err)
	}
	lock := dirlock.New(lockDir, nil)
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock browser profile %q: %w", name, err)
	}
	records, err := store.List()
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	for _, record := range records {
		if record.Profile == name && record.ID != ownRecordID && record.State == browserhost.StateDetached {
			_ = lock.Unlock()
			return nil, fmt.Errorf("browser profile %q is held by DAG run %s, which is waiting for input", name, record.DAGRunID)
		}
	}
	heartbeatCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	go heartbeat(heartbeatCtx, lock)
	return &profileLease{dir: dir, lock: lock, stop: stop}, nil
}

func (l *profileLease) release() {
	if l == nil {
		return
	}
	l.stop()
	_ = l.lock.Unlock()
}

func heartbeat(ctx context.Context, lock dirlock.DirLock) {
	ticker := time.NewTicker(profileHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = lock.Heartbeat(ctx)
		}
	}
}
