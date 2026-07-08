// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// DispatchMetadataCommand identifies a submitted run.
type DispatchMetadataCommand struct {
	DAGRun exec.DAGRunRef
}

// DispatchMetadata is the data needed by dispatch queue callers.
type DispatchMetadata struct {
	DAGRun    exec.DAGRunRef
	QueueName string
}

// DispatchMetadata returns dispatch queue metadata for a submitted run.
func (s *Service) DispatchMetadata(ctx context.Context, cmd DispatchMetadataCommand) (*DispatchMetadata, error) {
	if err := s.validateDispatchMetadata(cmd); err != nil {
		return nil, err
	}
	attempt, err := s.cfg.DAGRunStore.FindAttempt(ctx, cmd.DAGRun)
	if err != nil {
		return nil, err
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, fmt.Errorf("error reading DAG: %w", err)
	}
	return &DispatchMetadata{
		DAGRun:    cmd.DAGRun,
		QueueName: dag.ProcGroup(),
	}, nil
}

func (s *Service) validateDispatchMetadata(cmd DispatchMetadataCommand) error {
	if s.cfg.DAGRunStore == nil {
		return fmt.Errorf("dag-run store is required")
	}
	if err := validateDAGRunRef(cmd.DAGRun); err != nil {
		return err
	}
	return nil
}
