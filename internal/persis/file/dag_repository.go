// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/spec"
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
	return newDAGRepository(cfg, dagSource{
		paths:                  v2ProjectPaths(cfg.Paths),
		baseConfig:             cfg.Paths.BaseConfig,
		workspaceBaseConfigDir: workspace.BaseConfigDir(cfg.Paths.DAGsDir),
		calendars:              spec.NewCalendarSource(cfg.Paths.CalendarsDir),
		records:                dagRecords(cfg.Paths),
	}, opts...)
}

// dagSource places the DAG files of one project and what goes with them.
type dagSource struct {
	paths                  config.ProjectPaths
	baseConfig             string
	workspaceBaseConfigDir string
	// calendars serves the store and the repository, so every load shares
	// the parsed calendars.
	calendars *spec.CalendarSource
	// records holds the scheduler records that move with a DAG; nil keeps
	// none.
	records persis.Collection
	// library is searched after the search paths of the options.
	library string
	// project names the project whose lookups the repository resolves,
	// by owners. Both are empty while projects are off.
	project string
	owners  *filedag.Owners
}

func newDAGRepository(cfg *config.Config, src dagSource, opts ...DAGRepositoryOption) (*persis.DAGRepository, error) {
	options := DAGRepositoryOptions{Symlinks: cfg.DAGDiscovery.Symlinks}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if src.library != "" {
		options.SearchPaths = append(options.SearchPaths, src.library)
	}

	skipExamples := cfg.Core.SkipExamples
	if options.SkipExamples != nil {
		skipExamples = *options.SkipExamples
	}
	dagStore := filedag.NewStore(
		src.paths.DAGsDir,
		filedag.WithFlagsBaseDir(src.paths.SuspendFlagsDir),
		filedag.WithLegacyFlagsBaseDir(src.paths.LegacySuspendFlagsDir),
		filedag.WithIndexDir(dagIndexDir(src.paths.OtherStateDir)),
		filedag.WithSearchPaths(options.SearchPaths),
		filedag.WithBaseConfig(src.baseConfig),
		filedag.WithWorkspaceBaseConfigDir(src.workspaceBaseConfigDir),
		filedag.WithCalendars(src.calendars),
		filedag.WithFileCache(options.Cache),
		filedag.WithSkipExamples(skipExamples),
		filedag.WithRecursiveDiscovery(cfg.DAGDiscovery.Recursive),
		filedag.WithSymlinks(options.Symlinks),
		filedag.WithSkipDirs(cfg.DAGDiscovery.SkipDirs),
		filedag.WithSkipRoots(cfg.DiscoverySkipRoots()),
		filedag.WithSkipDirectoryCreation(options.SkipDirectoryCreation),
		filedag.WithDAGRecords(src.records),
		filedag.WithProject(src.project, src.owners),
	)
	if err := dagStore.Initialize(); err != nil {
		return nil, fmt.Errorf("initialize DAG definition store: %w", err)
	}
	return persis.NewDAGRepository(dagStore, persis.DAGRepositoryOptions{
		BaseConfigPath:         src.baseConfig,
		WorkspaceBaseConfigDir: src.workspaceBaseConfigDir,
		Calendars:              src.calendars,
	}), nil
}

// dagRecords returns the scheduler state collection that holds the records
// that move and go with a DAG, or nil without a data directory.
func dagRecords(paths config.PathsConfig) persis.Collection {
	if paths.DataDir == "" {
		return nil
	}
	return NewBackend(paths).Collection(persis.CollectionSchedulerState)
}

// dagIndexDir keeps the rebuildable DAG index out of the DAGs directory, which
// may be read-only. Without a state directory the store's default applies.
func dagIndexDir(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "cache", "dag-index")
}

// v2ProjectPaths returns the directories where v2 keeps the stores of its one
// project, which are the default project's while projects are off.
func v2ProjectPaths(paths config.PathsConfig) config.ProjectPaths {
	// The default project always resolves while projects are off.
	p, _ := (&config.Config{Paths: paths}).ProjectPaths(project.DefaultName)
	return p
}
