// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package monitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/gofrs/flock"
)

// StateStore persists encoded monitor state in a file.
type StateStore struct {
	path string
}

// NewStateStore creates file-backed monitor state storage.
func NewStateStore(path string) *StateStore {
	if path == "" {
		return nil
	}
	return &StateStore{path: filepath.Clean(path)}
}

func (s *StateStore) Load(_ context.Context) ([]byte, bool, error) {
	data, err := fileutil.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func (s *StateStore) Save(_ context.Context, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create notification state dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := writeStateFile(tmp, data); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := fileutil.ReplaceFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename notification state: %w", err)
	}
	return nil
}

func writeStateFile(path string, data []byte) error {
	file, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // internal state path
	if err != nil {
		return fmt.Errorf("open notification state file: %w", err)
	}

	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write notification state file: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync notification state file: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close notification state file: %w", closeErr)
	}
	return nil
}

func (s *StateStore) Quarantine(_ context.Context) (string, error) {
	quarantinedPath := fmt.Sprintf("%s.corrupt.%s", s.path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := fileutil.Rename(s.path, quarantinedPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("quarantine notification state: %w", err)
	}
	return quarantinedPath, nil
}

// Lease coordinates one active monitor across processes.
type Lease struct {
	dirlock.DirLock
	processLock *flock.Flock
	location    string
	opts        *dirlock.LockOptions
}

// NewLease creates a file-backed monitor lease for a state file.
func NewLease(stateFile string, opts *dirlock.LockOptions) *Lease {
	if stateFile == "" {
		return nil
	}
	if opts == nil {
		opts = &dirlock.LockOptions{}
	}
	location := filepath.Clean(stateFile) + ".lock"
	return &Lease{
		DirLock:     dirlock.New(location, opts),
		processLock: flock.New(location + ".flock"),
		location:    location,
		opts:        opts,
	}
}

func (l *Lease) Location() string {
	return l.location
}

func (l *Lease) TryLock() error {
	if err := fileutil.MkdirAll(filepath.Dir(l.location), 0o750); err != nil {
		return fmt.Errorf("create monitor lock directory: %w", err)
	}

	// Prevent stale recovery from replacing a lease held by a live process.
	locked, err := l.processLock.TryLock()
	if err != nil {
		return fmt.Errorf("acquire monitor process lock: %w", err)
	}
	if !locked {
		return dirlock.ErrLockConflict
	}

	if err := l.DirLock.TryLock(); err != nil {
		unlockErr := l.processLock.Unlock()
		if unlockErr != nil {
			unlockErr = fmt.Errorf("release monitor process lock: %w", unlockErr)
		}
		return errors.Join(err, unlockErr)
	}
	return nil
}

func (l *Lease) Lock(ctx context.Context) error {
	if err := l.TryLock(); err == nil {
		return nil
	} else if !errors.Is(err, dirlock.ErrLockConflict) {
		return err
	}
	if l.opts.OnWait != nil {
		l.opts.OnWait()
	}

	ticker := time.NewTicker(l.opts.RetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := l.TryLock(); err == nil {
				return nil
			} else if !errors.Is(err, dirlock.ErrLockConflict) {
				return err
			}
		}
	}
}

func (l *Lease) Unlock() error {
	directoryErr := l.DirLock.Unlock()
	processErr := l.processLock.Unlock()
	if processErr != nil {
		processErr = fmt.Errorf("release monitor process lock: %w", processErr)
	}
	return errors.Join(directoryErr, processErr)
}

func (l *Lease) IsHeldByMe() bool {
	return l.processLock.Locked() && l.DirLock.IsHeldByMe()
}
