// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
)

// slotIndexDirName is the slot index directory under the data directory.
const slotIndexDirName = "slot-index"

// DAGRunRepositoryOption configures the file-backed DAG-run repository.
type DAGRunRepositoryOption func(*dagRunRepositoryOptions)

type dagRunRepositoryOptions struct {
	HistoryFileCache  *fileutil.Cache[*ir.DAGRunStatus]
	LatestStatusToday bool
	RemovalEnqueuer   persis.DAGRunRemovalEnqueuer
}

// WithDAGRunRemovalEnqueuer records provider resources before DAG-run removal.
func WithDAGRunRemovalEnqueuer(enqueuer persis.DAGRunRemovalEnqueuer) DAGRunRepositoryOption {
	return func(o *dagRunRepositoryOptions) {
		o.RemovalEnqueuer = enqueuer
	}
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
func NewDAGRunRepository(cfg *config.Config, opts ...DAGRunRepositoryOption) *persis.DAGRunRepository {
	paths := v2ProjectPaths(cfg.Paths)
	return newDAGRunRepository(cfg, paths, EphemeralDAGRunsDir(paths.DAGRunsDir), runPaths(cfg, paths, true), opts...)
}

// newDAGRunRepository opens the runs of the project whose directories are p.
// Ephemeral runs live in ephemeralDir, a tree of their own outside the
// history, so no listing, retention pass or index of the history reaches
// them.
func newDAGRunRepository(
	cfg *config.Config,
	p config.ProjectPaths,
	ephemeralDir string,
	paths runpath.Mapper,
	opts ...DAGRunRepositoryOption,
) *persis.DAGRunRepository {
	options := dagRunRepositoryOptions{
		LatestStatusToday: cfg.Server.LatestStatusToday,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	storeOpts := []filedagrun.StoreOption{
		filedagrun.WithArtifactDir(p.ArtifactDir),
		filedagrun.WithRunPaths(paths),
		filedagrun.WithRetryCandidateCacheLimit(cfg.Cache.Limits().DAGRun.Limit),
	}
	if options.HistoryFileCache != nil {
		storeOpts = append(storeOpts, filedagrun.WithHistoryFileCache(options.HistoryFileCache))
	}
	store := filedagrun.NewStore(p.DAGRunsDir, storeOpts...)
	ephemeralStore := filedagrun.NewStore(ephemeralDir, storeOpts...)
	workDirs := filedagrun.NewWorkDirStore(p.DAGRunWorkDir, p.DAGRunsDir)
	var (
		slots   persis.SlotIndex
		onRetry func(context.Context, *ir.DAGRunStatus) error
	)
	if p.OtherStateDir != "" {
		slots = slotindex.New(filepath.Join(p.OtherStateDir, slotIndexDirName))
		onRetry = followBackfillRetry(filebackfill.New(filebackfill.Dir(p.OtherStateDir)))
	}
	return persis.NewDAGRunRepository(store, workDirs, persis.DAGRunRepositoryOptions{
		LatestStatusToday: options.LatestStatusToday,
		Location:          cfg.Core.Location,
		RemovalEnqueuer:   options.RemovalEnqueuer,
		EphemeralStore:    ephemeralStore,
		SlotIndex:         slots,
		CalendarsDir:      cfg.Paths.CalendarsDir,
		OnRetry:           onRetry,
	})
}

// runPaths returns how the run records of the project whose directories are
// p store the paths of logs, step output, and artifacts. Records store them
// relative to p's log and artifact directories while projects are on. With
// v2Runs, an absolute path under a v2 directory reads as the same path under
// p's.
func runPaths(cfg *config.Config, p config.ProjectPaths, v2Runs bool) runpath.Mapper {
	var v2 runpath.Dirs
	if v2Runs {
		v2 = runpath.Dirs{Log: cfg.Paths.LogDir, Artifact: cfg.Paths.ArtifactDir}
	}
	return runpath.New(runpath.Dirs{Log: p.LogDir, Artifact: p.ArtifactDir}, v2, cfg.ProjectsEnabled)
}

// followBackfillRetry has the backfill of a retried run follow the retry.
func followBackfillRetry(store backfill.Store) func(context.Context, *ir.DAGRunStatus) error {
	return func(ctx context.Context, status *ir.DAGRunStatus) error {
		if err := backfill.FollowRetry(ctx, store, status, time.Now()); err != nil {
			return fmt.Errorf("queue the slot of backfill %s again: %w", status.BackfillID, err)
		}
		return nil
	}
}

// EphemeralDAGRunsDir returns the directory that holds ephemeral runs. It is
// a sibling of the history directory, never inside it.
func EphemeralDAGRunsDir(dagRunsDir string) string {
	clean := filepath.Clean(dagRunsDir)
	return filepath.Join(filepath.Dir(clean), filepath.Base(clean)+"-ephemeral")
}
