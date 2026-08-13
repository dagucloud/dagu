// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
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
