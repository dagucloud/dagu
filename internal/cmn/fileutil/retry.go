// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	windowsFileRetryAttempts    = 12
	windowsFileRetryInitialWait = 10 * time.Millisecond
	windowsFileRetryMaxWait     = 100 * time.Millisecond
)

// ReadFileWithRetry retries transient Windows sharing violations while reading
// files that may be momentarily held by another process.
func ReadFileWithRetry(path string) ([]byte, error) {
	var data []byte
	err := retryWindowsFileOp(func() error {
		readData, err := os.ReadFile(path) //nolint:gosec // caller controls internal path
		if err != nil {
			return err
		}
		data = readData
		return nil
	})
	return data, err
}

// RemoveWithRetry retries transient Windows sharing violations while deleting a file.
func RemoveWithRetry(path string) error {
	return retryWindowsFileOp(func() error {
		return os.Remove(path)
	})
}

// RemoveAllWithRetry removes a tree without using os.RemoveAll's recursive
// open-at path, which fails on older Windows versions with openfdat errors.
func RemoveAllWithRetry(path string) error {
	if path == "" {
		return nil
	}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return removeAllWithRetry(path, info)
}

func removeAllWithRetry(path string, info os.FileInfo) error {
	if !info.IsDir() {
		if err := RemoveWithRetry(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	var firstErr error
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		firstErr = err
	}

	for _, entry := range entries {
		childPath := filepath.Join(path, entry.Name())
		childInfo, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := removeAllWithRetry(childPath, childInfo); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if err := RemoveWithRetry(path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// RenameWithRetry retries transient Windows sharing violations while renaming a file.
func RenameWithRetry(oldPath, newPath string) error {
	return retryWindowsFileOp(func() error {
		return os.Rename(oldPath, newPath)
	})
}

// ReplaceFileWithRetry replaces target with source, retrying transient Windows
// sharing violations that can happen while another process is still releasing
// the target file handle.
func ReplaceFileWithRetry(source, target string) error {
	return retryWindowsFileOp(func() error {
		return replaceFile(source, target)
	})
}

func retryWindowsFileOp(op func() error) error {
	err := op()
	if err == nil || !isTransientWindowsFileError(err) {
		return err
	}

	wait := windowsFileRetryInitialWait
	for range windowsFileRetryAttempts {
		time.Sleep(wait)
		err = op()
		if err == nil || !isTransientWindowsFileError(err) {
			return err
		}
		wait *= 2
		if wait > windowsFileRetryMaxWait {
			wait = windowsFileRetryMaxWait
		}
	}

	return err
}

func isTransientWindowsFileError(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "used by another process") ||
		strings.Contains(msg, "cannot access the file") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "sharing violation")
}
