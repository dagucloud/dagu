// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
)

func newDAGRepository(cfg *config.Config, storeCfg dagRepositoryConfig) (*persis.DAGRepository, error) {
	searchPaths := append([]string{}, storeCfg.SearchPaths...)
	if cfg.Paths.AltDAGsDir != "" {
		searchPaths = append(searchPaths, cfg.Paths.AltDAGsDir)
	}
	return file.NewDAGRepository(
		cfg,
		file.WithDAGFileCache(storeCfg.Cache),
		file.WithDAGSearchPaths(searchPaths),
		file.WithDAGSkipDirectoryCreation(storeCfg.SkipDirectoryCreation),
	)
}

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
