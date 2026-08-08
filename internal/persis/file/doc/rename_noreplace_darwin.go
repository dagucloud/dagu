// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package doc

import (
	"errors"

	"github.com/dagucloud/dagu/v2/internal/docs"
	"golang.org/x/sys/unix"
)

func renameNoReplace(oldPath, newPath string) error {
	err := unix.RenameatxNp(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCL)
	if errors.Is(err, unix.EEXIST) {
		return docs.ErrDocAlreadyExists
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return renameNoReplaceFallback(oldPath, newPath)
	}
	return err
}
