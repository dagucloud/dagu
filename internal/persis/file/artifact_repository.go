// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
)

// ArtifactRepositoryOption configures the file-backed artifact repository.
type ArtifactRepositoryOption func(*artifactRepositoryOptions)

type artifactRepositoryOptions struct {
	RecordCache *fileutil.Cache[*fileartifact.Record]
}

// WithArtifactRecordCache sets the cache used for reading artifact index records.
func WithArtifactRecordCache(cache *fileutil.Cache[*fileartifact.Record]) ArtifactRepositoryOption {
	return func(o *artifactRepositoryOptions) {
		o.RecordCache = cache
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
	return persis.NewArtifactRepository(fileartifact.NewStore(cfg.Paths.ArtifactDir, storeOpts...))
}
