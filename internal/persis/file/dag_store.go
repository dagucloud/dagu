// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// DAGStoreOption configures the file-backed DAG definition store.
type DAGStoreOption func(*DAGStoreOptions)

// DAGStoreOptions contains file-backed DAG definition store settings.
type DAGStoreOptions struct {
	Cache                 *fileutil.Cache[*ir.DAG]
	SearchPaths           []string
	SkipExamples          *bool
	SkipDirectoryCreation bool
}

// WithDAGFileCache sets the cache used for loading DAG definitions.
func WithDAGFileCache(cache *fileutil.Cache[*ir.DAG]) DAGStoreOption {
	return func(o *DAGStoreOptions) {
		o.Cache = cache
	}
}

// WithDAGSearchPaths sets additional directories used to resolve DAG definitions.
func WithDAGSearchPaths(paths []string) DAGStoreOption {
	return func(o *DAGStoreOptions) {
		o.SearchPaths = append([]string{}, paths...)
	}
}

// WithDAGSkipExamples controls whether example DAG files are created.
func WithDAGSkipExamples(skip bool) DAGStoreOption {
	return func(o *DAGStoreOptions) {
		o.SkipExamples = &skip
	}
}

// WithDAGSkipDirectoryCreation controls whether the DAG directory is created on startup.
func WithDAGSkipDirectoryCreation(skip bool) DAGStoreOption {
	return func(o *DAGStoreOptions) {
		o.SkipDirectoryCreation = skip
	}
}

// NewDAGStore wires the file-backed DAG definition store from application config.
func NewDAGStore(cfg *config.Config, opts ...DAGStoreOption) (exec.DAGStore, error) {
	options := DAGStoreOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	skipExamples := cfg.Core.SkipExamples
	if options.SkipExamples != nil {
		skipExamples = *options.SkipExamples
	}
	store := filedag.New(
		cfg.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(cfg.Paths.SuspendFlagsDir),
		filedag.WithSearchPaths(options.SearchPaths),
		filedag.WithBaseConfig(cfg.Paths.BaseConfig),
		filedag.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)),
		filedag.WithFileCache(options.Cache),
		filedag.WithSkipExamples(skipExamples),
		filedag.WithRecursiveDiscovery(cfg.DAGDiscovery.Recursive),
		filedag.WithSkipDirectoryCreation(options.SkipDirectoryCreation),
	)
	if s, ok := store.(*filedag.Storage); ok {
		if err := s.Initialize(); err != nil {
			return nil, fmt.Errorf("initialize DAG store: %w", err)
		}
	}
	return store, nil
}
