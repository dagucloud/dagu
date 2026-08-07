// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

var _ runtime.Database = &dbClient{}

type dbClient struct {
	ds              exec.DAGStore
	drs             exec.DAGRunStore
	remoteDAGLoader RemoteDAGLoader
}

func newDBClient(drs exec.DAGRunStore, ds exec.DAGStore, remoteDAGLoader RemoteDAGLoader) *dbClient {
	return &dbClient{drs: drs, ds: ds, remoteDAGLoader: remoteDAGLoader}
}

// GetDAG implements core.DBClient.
func (o *dbClient) GetDAG(ctx context.Context, name string) (*core.DAG, error) {
	// Guard against nil DAG store
	if o.ds == nil {
		logger.Info(ctx, "No local DAG store, trying remote fallback", tag.SubDAG(name))
		if o.remoteDAGLoader == nil {
			return nil, fmt.Errorf("no local DAG store and no remote loader configured for DAG %s", name)
		}
		remoteDAG, remoteErr := o.remoteDAGLoader(ctx, name)
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

	dag, err := o.ds.GetDetails(ctx, name, exec.DAGLoadOptions{})
	if err == nil {
		return dag, nil
	}
	// Only fallback to remote for not-found errors; propagate other errors directly
	if !errors.Is(err, exec.ErrDAGNotFound) {
		return nil, err
	}
	// Try remote fallback if configured
	if o.remoteDAGLoader == nil {
		return nil, err
	}
	logger.Info(ctx, "DAG not found locally, trying remote fallback",
		tag.SubDAG(name),
	)
	remoteDAG, remoteErr := o.remoteDAGLoader(ctx, name)
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

func (o *dbClient) RequestChildCancel(ctx context.Context, dagRunID string, rootDAGRun exec.DAGRunRef) error {
	subAttempt, err := o.drs.FindSubAttempt(ctx, rootDAGRun, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to find child attempt for dag-run ID %s: %w", dagRunID, err)
	}
	return subAttempt.Abort(ctx)
}
