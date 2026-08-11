// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// DAGRepositoryOption configures the file-backed DAG repository.
type DAGRepositoryOption func(*DAGRepositoryOptions)

// DAGRepositoryOptions contains file-backed DAG repository settings.
type DAGRepositoryOptions struct {
	Cache                 *fileutil.Cache[*ir.DAG]
	SearchPaths           []string
	SkipExamples          *bool
	Symlinks              bool
	SkipDirectoryCreation bool
}

// WithDAGFileCache sets the cache used for loading DAG definitions.
func WithDAGFileCache(cache *fileutil.Cache[*ir.DAG]) DAGRepositoryOption {
	return func(o *DAGRepositoryOptions) {
		o.Cache = cache
	}
}

// WithDAGSearchPaths sets additional directories used to resolve DAG definitions.
func WithDAGSearchPaths(paths []string) DAGRepositoryOption {
	return func(o *DAGRepositoryOptions) {
		o.SearchPaths = append([]string{}, paths...)
	}
}

// WithDAGSkipExamples controls whether example DAG files are created.
func WithDAGSkipExamples(skip bool) DAGRepositoryOption {
	return func(o *DAGRepositoryOptions) {
		o.SkipExamples = &skip
	}
}

// WithDAGSymlinks includes file symlinks in recursive discovery and permits external targets.
func WithDAGSymlinks(enabled bool) DAGRepositoryOption {
	return func(o *DAGRepositoryOptions) {
		o.Symlinks = enabled
	}
}

// WithDAGSkipDirectoryCreation controls whether the DAG directory is created on startup.
func WithDAGSkipDirectoryCreation(skip bool) DAGRepositoryOption {
	return func(o *DAGRepositoryOptions) {
		o.SkipDirectoryCreation = skip
	}
}

// NewDAGRepository connects the file-backed definition store to the shared repository.
func NewDAGRepository(cfg *config.Config, opts ...DAGRepositoryOption) (*persis.DAGRepository, error) {
	options := DAGRepositoryOptions{Symlinks: cfg.DAGDiscovery.Symlinks}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	skipExamples := cfg.Core.SkipExamples
	if options.SkipExamples != nil {
		skipExamples = *options.SkipExamples
	}
	workspaceBaseConfigDir := workspace.BaseConfigDir(cfg.Paths.DAGsDir)
	definitions := filedag.NewDefinitionStore(
		cfg.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(cfg.Paths.SuspendFlagsDir),
		filedag.WithSearchPaths(options.SearchPaths),
		filedag.WithBaseConfig(cfg.Paths.BaseConfig),
		filedag.WithWorkspaceBaseConfigDir(workspaceBaseConfigDir),
		filedag.WithFileCache(options.Cache),
		filedag.WithSkipExamples(skipExamples),
		filedag.WithRecursiveDiscovery(cfg.DAGDiscovery.Recursive),
		filedag.WithSymlinks(options.Symlinks),
		filedag.WithSkipDirectoryCreation(options.SkipDirectoryCreation),
	)
	if err := definitions.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize DAG definition store: %w", err)
	}
	return persis.NewDAGRepository(definitions, persis.DAGRepositoryOptions{
		BaseConfigPath:         cfg.Paths.BaseConfig,
		WorkspaceBaseConfigDir: workspaceBaseConfigDir,
	}), nil
}
