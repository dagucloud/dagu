// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

// DAGRepositoryConfig contains process wiring options for creating a DAG repository.
type DAGRepositoryConfig struct {
	Cache                 *fileutil.Cache[*ir.DAG]
	SearchPaths           []string
	SkipDirectoryCreation bool
}

// NewDAGRepository creates the file-backed DAG repository used by command process roles.
func NewDAGRepository(cfg *config.Config, storeCfg DAGRepositoryConfig) (persis.DAGRepository, error) {
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
