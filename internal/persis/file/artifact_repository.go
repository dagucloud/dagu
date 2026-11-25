// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
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
	paths := v2ProjectPaths(cfg.Paths)
	return newArtifactRepository(paths.ArtifactDir, runPaths(cfg, paths, true), opts...)
}

// newArtifactRepository opens the artifacts kept in dir, whose run records
// store their paths as paths maps them.
func newArtifactRepository(dir string, paths runpath.Mapper, opts ...ArtifactRepositoryOption) *persis.ArtifactRepository {
	var options artifactRepositoryOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	storeOpts := []fileartifact.StoreOption{fileartifact.WithRunPaths(paths)}
	if options.RecordCache != nil {
		storeOpts = append(storeOpts, fileartifact.WithRecordCache(options.RecordCache))
	}
	return persis.NewArtifactRepository(fileartifact.NewStore(dir, storeOpts...))
}
