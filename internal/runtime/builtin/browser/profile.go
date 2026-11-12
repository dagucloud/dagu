// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	profilesDirName   = "profiles"
	profileLockSuffix = ".lock"
	profileDirMode    = 0o700
)

// profileLease holds exclusive use of a persistent browser profile. Chrome
// cannot share a profile directory between processes, so runs using the same
// profile wait for each other.
type profileLease struct {
	dir  string
	lock dirlock.DirLock
	stop context.CancelFunc
}

// profileHeldError reports a profile whose browser something else keeps
// open: a step waiting for input, a browser session, or a running step.
type profileHeldError struct {
	message string
}

func (e *profileHeldError) Error() string { return e.message }

// acquireProfile takes the named profile for the record ownRecordID. When
// wait is set, it waits while another browser uses the profile; otherwise
// it fails at once. Either way it fails fast when a step waiting for input
// or a browser session keeps the profile's browser open between uses.
func acquireProfile(ctx context.Context, browserDir, name, ownRecordID string, wait bool) (*profileLease, error) {
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
	var err error
	if wait {
		err = lock.Lock(ctx)
	} else {
		err = lock.TryLock()
	}
	if errors.Is(err, dirlock.ErrLockConflict) {
		if heldErr := profileHolder(browserDir, name, ownRecordID); heldErr != nil {
			return nil, heldErr
		}
		return nil, &profileHeldError{message: fmt.Sprintf("browser profile %q is in use by a running browser step", name)}
	}
	if err != nil {
		return nil, fmt.Errorf("lock browser profile %q: %w", name, err)
	}
	if heldErr := profileHolder(browserDir, name, ownRecordID); heldErr != nil {
		_ = lock.Unlock()
		return nil, heldErr
	}
	stop := agentstep.KeepLockAlive(ctx, lock)
	return &profileLease{dir: dir, lock: lock, stop: stop}, nil
}

// profileHolder returns why a browser kept open between uses holds the
// named profile, or nil when none does. ownRecordID is not counted.
func profileHolder(browserDir, name, ownRecordID string) error {
	steps, err := browserhost.NewStore(browserDir).List()
	if err != nil {
		return err
	}
	for _, record := range steps {
		if record.Profile == name && record.ID != ownRecordID && record.State == browserhost.StateDetached {
			return &profileHeldError{message: fmt.Sprintf("browser profile %q is held by DAG run %s, which is waiting for input", name, record.DAGRunID)}
		}
	}
	sessions, err := browserhost.NewInteractiveStore(browserDir).List()
	if err != nil {
		return err
	}
	for _, record := range sessions {
		held := record.State == browserhost.StateInteractive || record.State == browserhost.StateRunning
		if record.Profile == name && record.ID != ownRecordID && held {
			return &profileHeldError{message: fmt.Sprintf("browser profile %q is held by browser session %s; close it with \"dagu browser session close %s\"",
				name, record.ID, record.ID)}
		}
	}
	return nil
}

func (l *profileLease) release() {
	if l == nil {
		return
	}
	l.stop()
	_ = l.lock.Unlock()
}
