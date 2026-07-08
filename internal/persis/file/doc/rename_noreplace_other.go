// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux

package doc

import (
	"os"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/docs"
)

func renameNoReplace(oldPath, newPath string) error {
	err := fileutil.Rename(oldPath, newPath)
	if os.IsExist(err) {
		return docs.ErrDocAlreadyExists
	}
	return err
}
