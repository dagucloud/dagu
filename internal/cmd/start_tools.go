// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
	daguaqua "github.com/dagucloud/dagu/v2/internal/tools/aqua"
)

func prepareDAGTools(ctx *Context, dag *ir.DAG) ([]string, error) {
	workDir := ""
	if dag != nil {
		workDir = dag.WorkingDir
	}
	return dagutools.PrepareDAG(ctx.Context, dag, daguaqua.New(), dagutools.InstallOptions{
		ToolsDir: ctx.Config.Paths.ToolsDir,
		DataDir:  ctx.Config.Paths.DataDir,
		WorkDir:  workDir,
	}, dagToolsBasePath(ctx))
}

// prepareTestDAGTools prepares the DAG tools for a workflow test run. It
// installs them only when the test has a real step, since no other step runs
// a command that needs them. Without one, it only checks that the DAG can use
// tools.
func prepareTestDAGTools(ctx *Context, dag *ir.DAG, hasRealSteps bool) ([]string, error) {
	if hasRealSteps {
		return prepareDAGTools(ctx, dag)
	}
	return nil, dagutools.ValidateDAGSupported(dag)
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
