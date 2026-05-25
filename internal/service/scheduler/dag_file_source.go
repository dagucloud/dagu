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
	dagFileSnapshotAttempts    = 6
	dagFileSnapshotInitialWait = 10 * time.Millisecond
	dagFileSnapshotMaxWait     = 100 * time.Millisecond
)

type dagFileSource struct {
	dir  string
	load func(context.Context, string) (*core.DAG, error)
}

type dagFileSnapshot struct {
	dag    *core.DAG
	exists bool
}

func newDAGFileSource(dir string) *dagFileSource {
	return &dagFileSource{
		dir:  dir,
		load: loadDAGMetadata,
	}
}

func loadDAGMetadata(ctx context.Context, filePath string) (*core.DAG, error) {
	return spec.Load(
		ctx,
		filePath,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
}

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
		if attempt >= dagFileSnapshotAttempts {
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
