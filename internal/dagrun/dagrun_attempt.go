// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// NewDAGRunAttemptOptions contains options for creating a new run record
type NewDAGRunAttemptOptions struct {
	// RootDAGRun is the root dag-run reference for this attempt.
	RootDAGRun *ir.DAGRunRef
	// Retry indicates whether this is a retry of a previous run.
	Retry bool
	// AttemptID is an optional attempt ID. If set, this ID is used instead of generating a new one.
	// This is used when the coordinator has already created an attempt and wants the worker
	// to use the same ID for consistency.
	AttemptID string
}

// DAGRunAttempt represents a single execution of a dag-run to record the status and execution details.
type DAGRunAttempt interface {
	// ID returns the identifier for the attempt that is unique within the dag-run.
	ID() string
	// Open prepares the attempt for writing status updates
	Open(ctx context.Context) error
	// Write updates the status of the attempt
	Write(ctx context.Context, status DAGRunStatus) error
	// Close finalizes writing to the attempt
	Close(ctx context.Context) error
	// ReadStatus retrieves the current status of the attempt
	ReadStatus(ctx context.Context) (*DAGRunStatus, error)
	// ReadDAG reads the DAG associated with this run attempt
	ReadDAG(ctx context.Context) (*ir.DAG, error)
	// SetDAG sets the DAG for this attempt (must be called before Open for DAG to be persisted)
	SetDAG(dag *ir.DAG)
	// Abort requests aborting the attempt
	Abort(ctx context.Context) error
	// IsAborting checks if an abort has been requested for the attempt
	IsAborting(ctx context.Context) (bool, error)
	// Hide marks the attempt as hidden from normal operations.
	// This is useful for preserving previous state visibility when dequeuing.
	Hide(ctx context.Context) error
	// Hidden returns true if the attempt is hidden from normal operations.
	Hidden() bool
	// WriteOutputs writes the collected step outputs for the dag-run.
	// Does nothing if outputs is nil or has no output entries.
	WriteOutputs(ctx context.Context, outputs *ir.DAGRunOutputs) error
	// ReadOutputs reads the collected step outputs for the dag-run.
	// Returns nil if no outputs file exists or if the file is in v1 format.
	ReadOutputs(ctx context.Context) (*ir.DAGRunOutputs, error)
	// WriteStepMessages writes LLM messages for a single step.
	WriteStepMessages(ctx context.Context, stepName string, messages []ir.LLMMessage) error
	// ReadStepMessages reads LLM messages for a single step.
	// Returns nil if no messages exist for the step.
	ReadStepMessages(ctx context.Context, stepName string) ([]ir.LLMMessage, error)
	// WorkDir returns the path to the per-DAG-run working directory.
	// Returns "" if the attempt does not support local storage.
	WorkDir() string
}
