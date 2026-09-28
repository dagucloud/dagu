// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	desktopLockName = "desktop.lock"
	desktopDirMode  = 0o700
	// lastInputFile holds when the step that last held the desktop sent
	// input, so the next step does not take that input for a person's.
	lastInputFile     = "last-input"
	lastInputFileMode = 0o600
)

// desktopLease holds exclusive use of the desktop. Two steps typing and
// clicking at once would interfere, so computer steps on one host take
// turns.
type desktopLease struct {
	dir  string
	lock dirlock.DirLock
	stop context.CancelFunc
}

// userDesktopLock returns the lock shared by every Dagu process of the user,
// whatever its data directory, since they all operate the same desktop. It
// is empty when the user has no cache directory.
func userDesktopLock() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "dagu", computerhost.DataDirName, desktopLockName)
}

// acquireDesktop waits for the desktop lock in lockDir.
func acquireDesktop(ctx context.Context, lockDir string, t *agentstep.Timeline) (*desktopLease, error) {
	if err := os.MkdirAll(lockDir, desktopDirMode); err != nil {
		return nil, fmt.Errorf("create desktop lock: %w", err)
	}
	lock := dirlock.New(lockDir, &dirlock.LockOptions{
		OnWait: func() {
			t.Waiting(waitReasonDesktop, "Waiting for another computer step to finish using the desktop")
		},
	})
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock the desktop: %w", err)
	}
	stop := agentstep.KeepLockAlive(ctx, lock)
	return &desktopLease{dir: lockDir, lock: lock, stop: stop}, nil
}

// lastInput returns when the previous holder of the desktop last sent input,
// or the zero time.
func (l *desktopLease) lastInput() time.Time {
	data, err := os.ReadFile(filepath.Join(l.dir, lastInputFile)) //nolint:gosec // the lock directory is Dagu's own
	if err != nil {
		return time.Time{}
	}
	nanos, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// recordInput notes when this holder last sent input, for the next one.
func (l *desktopLease) recordInput(at time.Time) {
	if at.IsZero() {
		return
	}
	_ = os.WriteFile(filepath.Join(l.dir, lastInputFile), []byte(strconv.FormatInt(at.UnixNano(), 10)), lastInputFileMode)
}

func (l *desktopLease) release() {
	if l == nil {
		return
	}
	l.stop()
	_ = l.lock.Unlock()
}
