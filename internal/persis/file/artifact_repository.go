// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
)

// ArtifactRepositoryOption configures the file-backed artifact repository.
type ArtifactRepositoryOption func(*artifactRepositoryOptions)

type artifactRepositoryOptions struct {
	RecordCache *fileutil.Cache[*fileartifact.Record]
	RootLabels  fileartifact.RootLabelsFunc
}

// WithArtifactRecordCache sets the cache used for reading artifact index records.
func WithArtifactRecordCache(cache *fileutil.Cache[*fileartifact.Record]) ArtifactRepositoryOption {
	return func(o *artifactRepositoryOptions) {
		o.RecordCache = cache
	}
}

// WithArtifactRootLabels sets how the listing learns which workspace a child
// run's root belongs to.
func WithArtifactRootLabels(fn fileartifact.RootLabelsFunc) ArtifactRepositoryOption {
	return func(o *artifactRepositoryOptions) {
		o.RootLabels = fn
	}
}

// RootLabelsFromRuns answers that question from the root run's own recorded
// status, which is where its workspace label lives.
func RootLabelsFromRuns(runs *persis.DAGRunRepository) fileartifact.RootLabelsFunc {
	return func(ctx context.Context, ref ir.DAGRunRef) ([]string, bool) {
		attempt, err := runs.FindAttempt(ctx, ref)
		if err != nil {
			return nil, false
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil || status == nil {
			return nil, false
		}
		return status.Labels, true
	}
}

// NewArtifactRepository connects file storage to the shared artifact repository.
func NewArtifactRepository(cfg *config.Config, opts ...ArtifactRepositoryOption) *persis.ArtifactRepository {
	var options artifactRepositoryOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	storeOpts := []fileartifact.StoreOption{}
	if options.RecordCache != nil {
		storeOpts = append(storeOpts, fileartifact.WithRecordCache(options.RecordCache))
	}
	if options.RootLabels != nil {
		storeOpts = append(storeOpts, fileartifact.WithRootLabels(options.RootLabels))
	}
	return persis.NewArtifactRepository(fileartifact.NewStore(cfg.Paths.ArtifactDir, storeOpts...))
}
