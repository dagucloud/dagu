// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"runtime"
	"strings"
	"time"
)

func replaceFile(src, dst string) error {
	return retryFileAccess(func() error {
		return replaceFileOnce(src, dst)
	})
}

func retryFileAccess(op func() error) error {
	var err error
	for attempt := range 40 {
		err = op()
		if err == nil {
			return nil
		}
		if !isRetryableFileAccessError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	return err
}

func isRetryableFileAccessError(err error) bool {
	if err == nil || runtime.GOOS != "windows" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "cannot access the file") ||
		strings.Contains(msg, "sharing violation") ||
		strings.Contains(msg, "used by another process")
}
