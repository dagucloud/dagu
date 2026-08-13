// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

var ErrInvalidDAGRunQueryCursor = errors.New("dagrun: invalid query cursor")

// DAGRunStore persists DAG runs and their attempts as one consistency boundary.
type DAGRunStore interface {
	CreateAttempt(ctx context.Context, req DAGRunCreateAttemptRequest) (dagrun.Attempt, error)
	RecentStatuses(ctx context.Context, name string, limit int) ([]ir.DAGRunStatus, error)
	LatestAttempt(ctx context.Context, query DAGRunLatestAttemptQuery) (dagrun.Attempt, error)
	QueryStatuses(ctx context.Context, query DAGRunStatusQuery) (DAGRunStatusPage, error)
	CompareAndSwapLatestAttemptStatus(ctx context.Context, req DAGRunCompareAndSwapStatusRequest) (*ir.DAGRunStatus, bool, error)
	FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error)
	FindSubAttempt(ctx context.Context, root ir.DAGRunRef, childRunID string) (dagrun.Attempt, error)
	RemoveOldDAGRuns(ctx context.Context, req DAGRunRetentionRequest) ([]ir.DAGRunRef, error)
	RemoveDAGRun(ctx context.Context, req DAGRunRemoveRequest) error
}

// DAGRunStatusQuery contains normalized backend filters for listing runs.
// Limit is zero for an unbounded query and positive otherwise.
type DAGRunStatusQuery struct {
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

// DAGRunStatusPage is one forward-only page of DAG-run statuses.
type DAGRunStatusPage struct {
	Items      []*ir.DAGRunStatus
	NextCursor string
}

// DAGRunCreateAttemptRequest identifies the run and attempt to create.
// A zero RootDAGRun creates a root run; a nonzero value creates its child.
type DAGRunCreateAttemptRequest struct {
	DAG        *ir.DAG
	RootDAGRun ir.DAGRunRef
	Timestamp  time.Time
	DAGRunID   string
	AttemptID  string
	Retry      bool
}

// DAGRunLatestAttemptQuery selects the newest visible attempt for a DAG.
type DAGRunLatestAttemptQuery struct {
	Name      string
	NotBefore TimeInUTC
}

// DAGRunCompareAndSwapStatusRequest describes an atomic latest-attempt status update.
type DAGRunCompareAndSwapStatusRequest struct {
	DAGRun             ir.DAGRunRef
	RootDAGRun         ir.DAGRunRef
	ExpectedAttemptID  string
	ExpectedAttemptKey string
	ExpectedStatus     ir.Status
	Mutate             func(*ir.DAGRunStatus) error
}

// DAGRunRetentionRequest describes normalized DAG-run cleanup policy.
type DAGRunRetentionRequest struct {
	Name      string
	OlderThan TimeInUTC
	KeepRuns  int
	DryRun    bool
}

// DAGRunRemoveRequest identifies a DAG run to remove.
type DAGRunRemoveRequest struct {
	DAGRun       ir.DAGRunRef
	RejectActive bool
}

// DAGRunRepositoryOptions configures application-level DAG-run behavior.
type DAGRunRepositoryOptions struct {
	LatestStatusToday bool
	Location          *time.Location
	Now               func() time.Time
}

// DAGRunCreateAttemptOptions configures creation of a run attempt.
type DAGRunCreateAttemptOptions struct {
	// RootDAGRun identifies the root when creating a child attempt.
	RootDAGRun ir.DAGRunRef
	// Retry indicates that the attempt retries an existing DAG run.
	Retry bool
	// AttemptID uses a caller-assigned attempt identifier when non-empty.
	AttemptID string
}

// DAGRunLatestAttemptOptions configures a latest-attempt lookup.
type DAGRunLatestAttemptOptions struct {
	// AllHistory disables the repository's optional today-only lookup window.
	AllHistory bool
}

// DAGRunListOptions configures status listing.
type DAGRunListOptions struct {
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
	// AllHistory disables the implicit recent-time window when no range is set.
	AllHistory bool
	// Unbounded disables the repository's result limit.
	Unbounded bool
}

// DAGRunCompareAndSwapOptions configures an atomic latest-attempt status update.
type DAGRunCompareAndSwapOptions struct {
	// RootDAGRun identifies the root containing a child attempt.
	RootDAGRun ir.DAGRunRef
	// ExpectedAttemptKey rejects an update when the persisted key differs.
	ExpectedAttemptKey string
}

// DAGRunRetentionOptions configures retention cleanup.
type DAGRunRetentionOptions struct {
	DryRun bool
	// RetentionRuns takes precedence over OlderThan and retention days when set.
	RetentionRuns *int
	// OlderThan takes precedence over retention days when RetentionRuns is unset.
	OlderThan *time.Time
}

// DAGRunRemoveOptions configures DAG-run removal.
type DAGRunRemoveOptions struct {
	// RejectActive refuses to remove an active DAG run.
	RejectActive bool
}

// TimeInUTC wraps a time normalized to UTC.
type TimeInUTC struct{ time.Time }

// NewUTC returns t normalized to UTC.
func NewUTC(t time.Time) TimeInUTC {
	return TimeInUTC{Time: t.UTC()}
}

type dagRunListReadBatchContextKey struct{}

var nextDAGRunListReadBatchID atomic.Uint64

// DAGRunListReadBatch identifies list reads that may share the same storage work.
type DAGRunListReadBatch struct {
	id uint64
}

// NewDAGRunListReadBatch creates a new list-read batch.
func NewDAGRunListReadBatch() *DAGRunListReadBatch {
	return &DAGRunListReadBatch{id: nextDAGRunListReadBatchID.Add(1)}
}

// WithDAGRunListReadBatch associates a list-read batch with a context.
func WithDAGRunListReadBatch(ctx context.Context, batch *DAGRunListReadBatch) context.Context {
	return context.WithValue(ctx, dagRunListReadBatchContextKey{}, batch)
}

// DAGRunListReadBatchID returns the list-read batch ID associated with ctx.
func DAGRunListReadBatchID(ctx context.Context) (uint64, bool) {
	batch, ok := ctx.Value(dagRunListReadBatchContextKey{}).(*DAGRunListReadBatch)
	if !ok {
		return 0, false
	}
	return batch.id, true
}
