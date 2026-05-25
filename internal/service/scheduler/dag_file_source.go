// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
)

const (
	// dagFileSnapshotRetries bounds how long watcher handling waits for
	// atomic replace operations to settle before treating the file as deleted.
	dagFileSnapshotRetries = 6
	// dagFileSnapshotInitialWait keeps the common retry path short for
	// transient Windows sharing and rename gaps.
	dagFileSnapshotInitialWait = 10 * time.Millisecond
	// dagFileSnapshotMaxWait caps backoff so watcher processing stays responsive.
	dagFileSnapshotMaxWait = 100 * time.Millisecond
)

// dagFileSource resolves watched DAG files into stable metadata snapshots.
type dagFileSource struct {
	dir  string
	load func(context.Context, string) (*core.DAG, error)
}

// dagFileSnapshot represents the scheduler-visible state of one DAG file.
type dagFileSnapshot struct {
	dag    *core.DAG
	exists bool
}

// newDAGFileSource creates the production DAG file source for a watched directory.
func newDAGFileSource(dir string) *dagFileSource {
	return &dagFileSource{
		dir:  dir,
		load: loadDAGMetadata,
	}
}

// loadDAGMetadata loads only scheduler metadata from a DAG spec file.
func loadDAGMetadata(ctx context.Context, filePath string) (*core.DAG, error) {
	return spec.Load(
		ctx,
		filePath,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
}

// snapshot returns the current stable state of a DAG file for watcher handling.
func (s *dagFileSource) snapshot(ctx context.Context, fileName string) (dagFileSnapshot, error) {
	filePath := filepath.Join(s.dir, fileName)
	wait := dagFileSnapshotInitialWait

	for attempt := 0; ; attempt++ {
		dag, err := s.load(ctx, filePath)
		if err == nil {
			return dagFileSnapshot{dag: dag, exists: true}, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return dagFileSnapshot{}, err
		}
		if attempt >= dagFileSnapshotRetries {
			return dagFileSnapshot{exists: false}, nil
		}
		if !sleepDAGFileSnapshot(ctx, wait) {
			return dagFileSnapshot{}, ctx.Err()
		}
		wait *= 2
		if wait > dagFileSnapshotMaxWait {
			wait = dagFileSnapshotMaxWait
		}
	}
}

// sleepDAGFileSnapshot waits between snapshot retries while honoring cancellation.
func sleepDAGFileSnapshot(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
