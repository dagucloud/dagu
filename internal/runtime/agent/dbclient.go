// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
)

var _ runtime.Database = &dbClient{}

type dbClient struct {
	ds  exec.DAGStore
	drs exec.DAGRunStore
}

func newDBClient(drs exec.DAGRunStore, ds exec.DAGStore) *dbClient {
	return &dbClient{drs: drs, ds: ds}
}

// GetDAG implements core.DBClient.
func (o *dbClient) GetDAG(ctx context.Context, name string) (*core.DAG, error) {
	return o.ds.GetDetails(ctx, name)
}

func (o *dbClient) GetSubDAGRunStatus(ctx context.Context, dagRunID string, rootDAGRun exec.DAGRunRef) (*runtime.RunStatus, error) {
	subAttempt, err := o.drs.FindSubAttempt(ctx, rootDAGRun, dagRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to find run for dag-run ID %s: %w", dagRunID, err)
	}
	status, err := subAttempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read status: %w", err)
	}

	outputVariables := make(map[string]string)
	for _, node := range status.Nodes {
		if node.OutputVariables != nil {
			node.OutputVariables.Range(func(_, value any) bool {
				// split the value by '=' to get the key and value
				key, val, found := strings.Cut(value.(string), "=")
				if found {
					outputVariables[key] = val
				}
				return true
			})
		}
	}
	return &runtime.RunStatus{
		Status:             status.Status,
		Outputs:            outputVariables,
		OutputValues:       runtime.OutputValuesFromExecNodes(status.Nodes),
		Name:               status.Name,
		DAGRunID:           status.DAGRunID,
		Params:             status.Params,
		PendingStepRetries: exec.PendingStepRetriesFromStatus(status),
	}, nil
}

func (o *dbClient) IsSubDAGRunCompleted(ctx context.Context, dagRunID string, rootDAGRun exec.DAGRunRef) (bool, error) {
	subAttempt, err := o.drs.FindSubAttempt(ctx, rootDAGRun, dagRunID)
	if err != nil {
		return false, fmt.Errorf("failed to find run for dag-run ID %s: %w", dagRunID, err)
	}
	status, err := subAttempt.ReadStatus(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read status: %w", err)
	}

	return !status.Status.IsActive(), nil
}

func (o *dbClient) RequestChildCancel(ctx context.Context, dagRunID string, rootDAGRun exec.DAGRunRef) error {
	subAttempt, err := o.drs.FindSubAttempt(ctx, rootDAGRun, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to find child attempt for dag-run ID %s: %w", dagRunID, err)
	}
	return subAttempt.Abort(ctx)
}
