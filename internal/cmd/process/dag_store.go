// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

// DAGStoreConfig contains process wiring options for creating a DAG store.
type DAGStoreConfig struct {
	Cache                 *fileutil.Cache[*ir.DAG]
	SearchPaths           []string
	SkipDirectoryCreation bool
}

// NewDAGStore creates the file-backed DAG store used by command process roles.
func NewDAGStore(cfg *config.Config, storeCfg DAGStoreConfig) (exec.DAGStore, error) {
	searchPaths := append([]string{}, storeCfg.SearchPaths...)
	if cfg.Paths.AltDAGsDir != "" {
		searchPaths = append(searchPaths, cfg.Paths.AltDAGsDir)
	}
	return file.NewDAGStore(
		cfg,
		file.WithDAGFileCache(storeCfg.Cache),
		file.WithDAGSearchPaths(searchPaths),
		file.WithDAGSkipDirectoryCreation(storeCfg.SkipDirectoryCreation),
	)
}
