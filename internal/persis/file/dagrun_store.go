// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
)

const DAGRunOutputsFileName = filedagrun.OutputsFile

// DAGRunStoreOption configures the file-backed DAG-run store.
type DAGRunStoreOption func(*DAGRunStoreOptions)

// DAGRunStoreOptions contains file-backed DAG-run store settings.
type DAGRunStoreOptions struct {
	HistoryFileCache  *fileutil.Cache[*dagrun.DAGRunStatus]
	LatestStatusToday bool
}

// WithDAGRunHistoryFileCache sets the cache used for reading DAG-run history files.
func WithDAGRunHistoryFileCache(cache *fileutil.Cache[*dagrun.DAGRunStatus]) DAGRunStoreOption {
	return func(o *DAGRunStoreOptions) {
		o.HistoryFileCache = cache
	}
}

// WithDAGRunLatestStatusToday controls whether latest status lookups are limited to today.
func WithDAGRunLatestStatusToday(latestStatusToday bool) DAGRunStoreOption {
	return func(o *DAGRunStoreOptions) {
		o.LatestStatusToday = latestStatusToday
	}
}

// NewDAGRunStore wires the file-backed DAG-run store from application config.
func NewDAGRunStore(cfg *config.Config, opts ...DAGRunStoreOption) dagrun.DAGRunStore {
	options := DAGRunStoreOptions{
		LatestStatusToday: cfg.Server.LatestStatusToday,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	storeOpts := []filedagrun.DAGRunStoreOption{
		filedagrun.WithArtifactDir(cfg.Paths.ArtifactDir),
		filedagrun.WithLatestStatusToday(options.LatestStatusToday),
		filedagrun.WithLocation(cfg.Core.Location),
	}
	if options.HistoryFileCache != nil {
		storeOpts = append(storeOpts, filedagrun.WithHistoryFileCache(options.HistoryFileCache))
	}
	return filedagrun.New(cfg.Paths.DAGRunsDir, storeOpts...)
}
