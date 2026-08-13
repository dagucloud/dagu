// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

var _ runtime.DAGLoader = &fallbackDAGLoader{}

type fallbackDAGLoader struct {
	local  dagDetailsLoader
	remote RemoteDAGLoader
}

type dagDetailsLoader interface {
	GetDetails(context.Context, string, persis.DAGLoadOptions) (*ir.DAG, error)
}

func newDAGLoader(local dagDetailsLoader, remote RemoteDAGLoader) *fallbackDAGLoader {
	return &fallbackDAGLoader{local: local, remote: remote}
}

func (l *fallbackDAGLoader) GetDAG(ctx context.Context, name string) (*ir.DAG, error) {
	if l.local == nil {
		logger.Info(ctx, "No local DAG store, trying remote fallback", tag.SubDAG(name))
		if l.remote == nil {
			return nil, fmt.Errorf("no local DAG store and no remote loader configured for DAG %s", name)
		}
		remoteDAG, remoteErr := l.remote(ctx, name)
		if remoteErr != nil {
			logger.Warn(ctx, "Remote DAG fallback failed", tag.SubDAG(name), tag.Error(remoteErr))
			return nil, fmt.Errorf("remote DAG load failed for %s: %w", name, remoteErr)
		}
		if remoteDAG == nil {
			return nil, fmt.Errorf("DAG %s not found locally or remotely", name)
		}
		logger.Info(ctx, "DAG loaded from remote fallback", tag.SubDAG(name))
		return remoteDAG, nil
	}

	dag, err := l.local.GetDetails(ctx, name, persis.DAGLoadOptions{})
	if err == nil {
		return dag, nil
	}
	// Only fallback to remote for not-found errors; propagate other errors directly
	if !errors.Is(err, persis.ErrDAGNotFound) {
		return nil, err
	}
	// Try remote fallback if configured
	if l.remote == nil {
		return nil, err
	}
	logger.Info(ctx, "DAG not found locally, trying remote fallback",
		tag.SubDAG(name),
	)
	remoteDAG, remoteErr := l.remote(ctx, name)
	if remoteErr != nil {
		logger.Warn(ctx, "Remote DAG fallback failed",
			tag.SubDAG(name),
			tag.Error(remoteErr),
		)
		return nil, err // Return the original local error
	}
	if remoteDAG == nil {
		return nil, err // Return the original local error
	}
	logger.Info(ctx, "DAG loaded from remote fallback",
		tag.SubDAG(name),
	)
	return remoteDAG, nil
}
