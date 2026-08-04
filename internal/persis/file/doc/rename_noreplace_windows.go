// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package doc

import (
	"errors"

	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"golang.org/x/sys/windows"
)

func renameNoReplace(oldPath, newPath string) error {
	oldPathPtr, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newPathPtr, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(oldPathPtr, newPathPtr, 0); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return docs.ErrDocAlreadyExists
		}
		return err
	}
	return nil
}
