// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Errors related to dag-run management
var (
	ErrDAGRunIDNotFound    = errors.New("dag-run ID not found")
	ErrDAGRunIDEmpty       = errors.New("dag-run ID is empty")
	ErrDAGRunAlreadyExists = errors.New("dag-run already exists")
	ErrDAGRunActive        = errors.New("dag-run is active")
	ErrNoStatusData        = errors.New("no status data")
	ErrCorruptedStatusFile = errors.New("corrupted status file") // Status file exists but contains no valid data or is corrupted
	ErrInvalidQueryCursor  = errors.New("dagrun: invalid query cursor")
)

// StatusQuery contains normalized filters for listing runs.
type StatusQuery struct {
	DAGRunID        string
	Name            string
	ExactName       string
	From            TimeInUTC
	To              TimeInUTC
	Statuses        []ir.Status
	Limit           int
	Cursor          string
	Labels          []string // Filter by DAG labels (AND logic - all labels must match)
	WorkspaceFilter *workspace.WorkspaceFilter
	Unlimited       bool
	AllHistory      bool
}

// ListDAGRunStatusesOption configures a status query.
type ListDAGRunStatusesOption func(*StatusQuery)

// WithFrom sets the start time for listing dag-runs
func WithFrom(from TimeInUTC) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.From = from
	}
}

// WithTo sets the end time for listing dag-runs
func WithTo(to TimeInUTC) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.To = to
	}
}

// WithStatuses sets the statuses for listing dag-runs
func WithStatuses(statuses []ir.Status) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.Statuses = statuses
	}
}

// WithExactName sets the name for listing dag-runs
func WithExactName(name string) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.ExactName = name
	}
}

// WithName sets the name for listing dag-runs
func WithName(name string) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.Name = name
	}
}

// WithDAGRunID sets the dag-run ID for listing dag-runs
func WithDAGRunID(dagRunID string) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.DAGRunID = dagRunID
	}
}

// WithLabels sets the labels filter for listing dag-runs (AND logic - all labels must match)
func WithLabels(labels []string) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.Labels = labels
	}
}

// WithWorkspaceFilter sets the workspace visibility filter for listing dag-runs.
func WithWorkspaceFilter(filter *workspace.WorkspaceFilter) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.WorkspaceFilter = filter
	}
}

// WithLimit sets the maximum number of results to return when listing dag-runs
func WithLimit(limit int) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.Limit = limit
	}
}

// WithCursor sets the opaque cursor for forward-only DAG-run pagination.
func WithCursor(cursor string) ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.Cursor = cursor
	}
}

// WithoutLimit disables the default 1000-item cap for internal callers that
// need to scan the full recent result set.
func WithoutLimit() ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.Unlimited = true
	}
}

// WithAllHistory disables the default implicit "today only" time window when
// no explicit range is supplied.
func WithAllHistory() ListDAGRunStatusesOption {
	return func(o *StatusQuery) {
		o.AllHistory = true
	}
}

// DAGRunStatusPage is one forward-only page of DAG-run statuses.
type DAGRunStatusPage struct {
	Items      []*ir.DAGRunStatus
	NextCursor string
}

type removeDAGRunOptions struct {
	// RejectActive if true, refuses to remove dag-runs with an active status.
	RejectActive bool
}

// RemoveDAGRunOption configures DAG-run removal.
type RemoveDAGRunOption func(*removeDAGRunOptions)

// WithRejectActiveDAGRun refuses to remove dag-runs that are still active.
func WithRejectActiveDAGRun() RemoveDAGRunOption {
	return func(o *removeDAGRunOptions) {
		o.RejectActive = true
	}
}

type removeOldDAGRunsOptions struct {
	// DryRun if true, only returns the paths that would be removed without actually deleting
	DryRun bool
	// RetentionRuns keeps the most recent number of dag-runs when set.
	RetentionRuns *int
	// OlderThan when set, deletes dag-runs whose recorded time is strictly before this
	// cutoff. When set, the retentionDays argument is ignored.
	OlderThan *time.Time
}

// RemoveOldDAGRunsOption configures retention cleanup.
type RemoveOldDAGRunsOption func(*removeOldDAGRunsOptions)

// WithDryRun sets the dry-run mode for removing old dag-runs
func WithDryRun() RemoveOldDAGRunsOption {
	return func(o *removeOldDAGRunsOptions) {
		o.DryRun = true
	}
}

// WithRetentionRuns keeps the most recent number of dag-runs.
func WithRetentionRuns(runs int) RemoveOldDAGRunsOption {
	return func(o *removeOldDAGRunsOptions) {
		o.RetentionRuns = &runs
	}
}

// WithOlderThan deletes dag-runs older than the given cutoff time. A zero cutoff
// removes no dag-runs.
// When set, the retentionDays argument to RemoveOldDAGRuns is ignored.
func WithOlderThan(t time.Time) RemoveOldDAGRunsOption {
	return func(o *removeOldDAGRunsOptions) {
		cutoff := t.UTC()
		o.OlderThan = &cutoff
	}
}

type compareAndSwapStatusOptions struct {
	RootDAGRun         ir.DAGRunRef
	ExpectedAttemptKey string
}

// CompareAndSwapStatusOption configures CompareAndSwapLatestAttemptStatus.
type CompareAndSwapStatusOption func(*compareAndSwapStatusOptions)

// WithCompareAndSwapRootDAGRun routes CompareAndSwapLatestAttemptStatus
// through a root dag-run when the target dag-run is stored as a sub-DAG attempt.
func WithCompareAndSwapRootDAGRun(root ir.DAGRunRef) CompareAndSwapStatusOption {
	return func(opts *compareAndSwapStatusOptions) {
		opts.RootDAGRun = root
	}
}

// WithCompareAndSwapExpectedAttemptKey requires the current status attempt key
// to match.
func WithCompareAndSwapExpectedAttemptKey(attemptKey string) CompareAndSwapStatusOption {
	return func(opts *compareAndSwapStatusOptions) {
		opts.ExpectedAttemptKey = attemptKey
	}
}

func newCompareAndSwapStatusOptions(opts ...CompareAndSwapStatusOption) compareAndSwapStatusOptions {
	var cfg compareAndSwapStatusOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}
