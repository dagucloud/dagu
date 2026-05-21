// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedag"
	"github.com/dagucloud/dagu/internal/workspace"
)

// DAGStoreConfig contains process wiring options for creating a DAG store.
type DAGStoreConfig struct {
	Cache                 *fileutil.Cache[*core.DAG]
	SearchPaths           []string
	SkipDirectoryCreation bool
}

// NewDAGStore creates the file-backed DAG store used by command process roles.
func NewDAGStore(cfg *config.Config, storeCfg DAGStoreConfig) (exec.DAGStore, error) {
	searchPaths := append([]string{}, storeCfg.SearchPaths...)
	if cfg.Paths.AltDAGsDir != "" {
		searchPaths = append(searchPaths, cfg.Paths.AltDAGsDir)
	}

	store := filedag.New(
		cfg.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(cfg.Paths.SuspendFlagsDir),
		filedag.WithSearchPaths(searchPaths),
		filedag.WithBaseConfig(cfg.Paths.BaseConfig),
		filedag.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)),
		filedag.WithFileCache(storeCfg.Cache),
		filedag.WithSkipExamples(cfg.Core.SkipExamples),
		filedag.WithSkipDirectoryCreation(storeCfg.SkipDirectoryCreation),
	)

	if s, ok := store.(*filedag.Storage); ok {
		if err := s.Initialize(); err != nil {
			return nil, fmt.Errorf("failed to initialize DAG store: %w", err)
		}
	}

	return store, nil
}
