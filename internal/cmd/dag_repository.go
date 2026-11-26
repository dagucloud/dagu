// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
)

// warnTestNamedDAGs logs each file in the DAGs directory that discovery skips
// as a test file although it holds a DAG. A process that schedules or serves
// DAGs calls it once at startup.
func warnTestNamedDAGs(ctx *Context) {
	cfg := ctx.Config
	filedag.WarnTestNamedDAGs(ctx, cfg.Paths.DAGsDir, filedag.DiscoveryOptions{
		Recursive: cfg.DAGDiscovery.Recursive,
		Symlinks:  cfg.DAGDiscovery.Symlinks,
		SkipDirs:  cfg.DAGDiscovery.SkipDirs,
		SkipRoots: cfg.DiscoverySkipRoots(),
	})
}
