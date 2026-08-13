// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
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

// Lease coordinates one active monitor through a directory lock.
type Lease struct {
	dirlock.DirLock
	location string
}

// LeaseOptions configures file-lock ownership behavior.
type LeaseOptions struct {
	StaleThreshold time.Duration
	RetryInterval  time.Duration
	OnWait         func(location string)
}

// NewLease creates a file-backed monitor lease for a state file.
func NewLease(stateFile string, opts LeaseOptions) *Lease {
	if stateFile == "" {
		return nil
	}
	location := filepath.Clean(stateFile) + ".lock"
	lock := dirlock.New(location, &dirlock.LockOptions{
		StaleThreshold: opts.StaleThreshold,
		RetryInterval:  opts.RetryInterval,
		OnWait: func() {
			if opts.OnWait != nil {
				opts.OnWait(location)
			}
		},
	})
	return &Lease{DirLock: lock, location: location}
}

func (l *Lease) Location() string {
	return l.location
}
