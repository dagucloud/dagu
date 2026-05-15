// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	daguaqua "github.com/dagucloud/dagu/internal/tools/aqua"
)

func prepareDAGTools(ctx *Context, dag *core.DAG) ([]string, error) {
	workDir := ""
	if dag != nil {
		workDir = dag.WorkingDir
	}
	return dagutools.PrepareDAG(ctx.Context, dag, daguaqua.New(), dagutools.InstallOptions{
		DataDir: ctx.Config.Paths.DataDir,
		WorkDir: workDir,
	}, dagToolsBasePath(ctx))
}

func dagToolsBasePath(ctx *Context) string {
	if ctx != nil && ctx.Config != nil {
		for _, env := range ctx.Config.Core.BaseEnv.AsSlice() {
			key, value, ok := strings.Cut(env, "=")
			if ok && strings.EqualFold(key, "PATH") {
				return value
			}
		}
	}
	return os.Getenv("PATH")
}
