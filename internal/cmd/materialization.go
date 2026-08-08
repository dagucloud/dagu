// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/build"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
)

func localMaterializationStore(ctx *Context) build.MaterializationStore {
	if ctx == nil || ctx.Config == nil || ctx.Config.Paths.DataDir == "" {
		return nil
	}
	return filematerialization.New(filepath.Join(ctx.Config.Paths.DataDir, "materializations"))
}
