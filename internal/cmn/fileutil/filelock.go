// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"os"
)

// ErrLocked is the error of TryLockFile on a file that another holder has
// locked.
var ErrLocked = errors.New("the file is locked by another process")

// FileLock is an exclusive lock on a file. The operating system keeps it
// while the process lives, also while the process is paused, and releases it
// when the process exits.
type FileLock struct {
	file *os.File
}

// TryLockFile creates path with perm when it is missing and locks it without
// waiting. It returns ErrLocked while another holder, in this process or
// another, has the lock.
func TryLockFile(path string, perm os.FileMode) (*FileLock, error) {
	// Windows can refuse the open while another process briefly holds the
	// file, such as a scanner, so a transient failure is retried.
	var file *os.File
	err := retryWindowsFileOp(func() error {
		// #nosec G304 -- path is chosen by the caller.
		opened, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, perm)
		file = opened
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &FileLock{file: file}, nil
}

// Unlock releases the lock.
func (l *FileLock) Unlock() error {
	return errors.Join(unlockFile(l.file), l.file.Close())
}
