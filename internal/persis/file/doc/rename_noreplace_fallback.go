// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package doc

import (
	"errors"
	"fmt"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/docs"
)

func renameNoReplaceFallback(oldPath, newPath string) error {
	info, err := os.Lstat(oldPath)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if err := os.Link(oldPath, newPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				return docs.ErrDocAlreadyExists
			}
			return err
		}
		if err := fileutil.Remove(oldPath); err != nil {
			if rollbackErr := fileutil.Remove(newPath); rollbackErr != nil {
				return fmt.Errorf("failed to remove source after linking destination: %w (destination rollback failed: %v)", err, rollbackErr)
			}
			return err
		}
		return nil
	}

	exists, err := pathExistsNoFollow(newPath)
	if err != nil {
		return err
	}
	if exists {
		return docs.ErrDocAlreadyExists
	}
	return fileutil.Rename(oldPath, newPath)
}
