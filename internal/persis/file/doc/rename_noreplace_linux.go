// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package doc

import (
	"errors"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"golang.org/x/sys/unix"
)

func renameNoReplace(oldPath, newPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return docs.ErrDocAlreadyExists
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return fileutil.Rename(oldPath, newPath)
	}
	return err
}
