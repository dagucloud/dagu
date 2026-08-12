// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Store persists DAG runs and their attempts as one consistency boundary.
type Store interface {
	// CreateAttempt atomically creates an attempt and returns it initialized with req.DAG.
	CreateAttempt(ctx context.Context, req CreateAttemptRequest) (Attempt, error)
	// RecentStatuses returns newest readable statuses in descending run order.
	RecentStatuses(ctx context.Context, name string, limit int) ([]ir.DAGRunStatus, error)
	// LatestAttempt returns the newest visible attempt matching the query.
	LatestAttempt(ctx context.Context, query LatestAttemptQuery) (Attempt, error)
	// QueryStatuses returns statuses in canonical newest-first order. A zero limit is unbounded.
	QueryStatuses(ctx context.Context, query StatusQuery) (StatusPage, error)
	// CompareAndSwapLatestAttemptStatus atomically applies Mutate when all expectations match.
	CompareAndSwapLatestAttemptStatus(ctx context.Context, req CompareAndSwapStatusRequest) (*ir.DAGRunStatus, bool, error)
	// FindAttempt returns the latest visible attempt for a DAG run.
	FindAttempt(ctx context.Context, ref ir.DAGRunRef) (Attempt, error)
	// FindSubAttempt returns the latest visible attempt for a child DAG run.
	FindSubAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (Attempt, error)
	// RemoveOldDAGRuns removes final runs outside the normalized retention request and preserves active runs.
	RemoveOldDAGRuns(ctx context.Context, req RetentionRequest) ([]string, error)
	// RemoveDAGRun removes a DAG run according to the request's safety policy.
	RemoveDAGRun(ctx context.Context, req RemoveDAGRunRequest) error
}

// RetryCandidateLister provides an optimized lookup for automatic retry candidates.
type RetryCandidateLister interface {
	ListRetryCandidates(ctx context.Context, from TimeInUTC) ([]*ir.DAGRunStatus, error)
}

// StatusQuery contains normalized backend filters for listing runs.
// Limit is zero for an unbounded query and positive otherwise.
type StatusQuery struct {
	DAGRunID        string
	Name            string
	ExactName       string
	From            TimeInUTC
	To              TimeInUTC
	Statuses        []ir.Status
	Limit           int
	Cursor          string
	Labels          []string
	WorkspaceFilter *workspace.WorkspaceFilter
}

// StatusPage is one forward-only page of DAG-run statuses.
type StatusPage struct {
	Items      []*ir.DAGRunStatus
	NextCursor string
}

// CreateAttemptRequest identifies the run and attempt to create.
// A zero RootDAGRun creates a root run; a nonzero value creates its child.
type CreateAttemptRequest struct {
	DAG        *ir.DAG
	RootDAGRun ir.DAGRunRef
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
