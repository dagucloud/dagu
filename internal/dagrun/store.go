// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Store persists DAG runs and their attempts as one consistency boundary.
type Store interface {
	CreateAttempt(ctx context.Context, req CreateAttemptRequest) (Attempt, error)
	RecentAttempts(ctx context.Context, name string, limit int) ([]Attempt, error)
	LatestAttempt(ctx context.Context, query LatestAttemptQuery) (Attempt, error)
	QueryStatuses(ctx context.Context, query StatusQuery) (DAGRunStatusPage, error)
	CompareAndSwapLatestAttemptStatus(ctx context.Context, req CompareAndSwapStatusRequest) (*ir.DAGRunStatus, bool, error)
	FindAttempt(ctx context.Context, ref ir.DAGRunRef) (Attempt, error)
	FindSubAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (Attempt, error)
	RemoveOldDAGRuns(ctx context.Context, req RetentionRequest) ([]string, error)
	RemoveDAGRun(ctx context.Context, req RemoveDAGRunRequest) error
}

// RetryCandidateStore provides an optimized lookup for automatic retry candidates.
type RetryCandidateStore interface {
	ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error)
}

// CreateAttemptRequest identifies the run and attempt to create.
type CreateAttemptRequest struct {
	DAG        *ir.DAG
	RootDAGRun *ir.DAGRunRef
	Timestamp  time.Time
	DAGRunID   string
	AttemptID  string
	Retry      bool
}

// LatestAttemptQuery selects the newest visible attempt for a DAG.
type LatestAttemptQuery struct {
	Name      string
	NotBefore TimeInUTC
}

// CompareAndSwapStatusRequest describes an atomic latest-attempt status update.
type CompareAndSwapStatusRequest struct {
	DAGRun             ir.DAGRunRef
	RootDAGRun         ir.DAGRunRef
	ExpectedAttemptID  string
	ExpectedAttemptKey string
	ExpectedStatus     ir.Status
	Mutate             func(*ir.DAGRunStatus) error
}

// RetentionRequest describes normalized DAG-run cleanup policy.
type RetentionRequest struct {
	Name      string
	OlderThan TimeInUTC
	KeepRuns  int
	DryRun    bool
}

// RemoveDAGRunRequest identifies a DAG run to remove.
type RemoveDAGRunRequest struct {
	DAGRun       ir.DAGRunRef
	RejectActive bool
}
