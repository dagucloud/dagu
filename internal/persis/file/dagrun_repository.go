// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
)

// DAGRunRepositoryOption configures the file-backed DAG-run repository.
type DAGRunRepositoryOption func(*dagRunRepositoryOptions)

type dagRunRepositoryOptions struct {
	HistoryFileCache  *fileutil.Cache[*ir.DAGRunStatus]
	LatestStatusToday bool
}

// WithDAGRunHistoryFileCache sets the cache used for reading DAG-run history files.
func WithDAGRunHistoryFileCache(cache *fileutil.Cache[*ir.DAGRunStatus]) DAGRunRepositoryOption {
	return func(o *dagRunRepositoryOptions) {
		o.HistoryFileCache = cache
	}
}

// WithDAGRunLatestStatusToday controls whether latest status lookups are limited to today.
func WithDAGRunLatestStatusToday(latestStatusToday bool) DAGRunRepositoryOption {
	return func(o *dagRunRepositoryOptions) {
		o.LatestStatusToday = latestStatusToday
	}
}

// NewDAGRunRepository connects file storage to the shared DAG-run repository.
func NewDAGRunRepository(cfg *config.Config, opts ...DAGRunRepositoryOption) *dagrun.Repository {
	options := dagRunRepositoryOptions{
		LatestStatusToday: cfg.Server.LatestStatusToday,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	storeOpts := []filedagrun.StoreOption{
		filedagrun.WithArtifactDir(cfg.Paths.ArtifactDir),
	}
	if options.HistoryFileCache != nil {
		storeOpts = append(storeOpts, filedagrun.WithHistoryFileCache(options.HistoryFileCache))
	}
	store := filedagrun.NewStore(cfg.Paths.DAGRunsDir, storeOpts...)
	workspaces := filedagrun.NewWorkspaceStore(cfg.Paths.DAGRunsDir)
	return dagrun.NewRepository(store, workspaces, dagrun.RepositoryOptions{
		LatestStatusToday: options.LatestStatusToday,
		Location:          cfg.Core.Location,
	})
}
