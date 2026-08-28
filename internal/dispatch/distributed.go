// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	ErrDispatchTaskNotFound                   = errors.New("dispatch task claim not found")
	ErrDispatchAdmissionNotFound              = errors.New("dispatch admission not found")
	ErrDispatchAdmissionConflict              = errors.New("dispatch admission conflict")
	ErrDispatchAdmissionLivenessNotConfigured = errors.New("dispatch admission liveness not configured")
	ErrDAGRunLeaseNotFound                    = errors.New("dag-run lease not found")
	ErrDAGRunLeaseConflict                    = errors.New("dag-run lease identity conflict")
	ErrActiveRunNotFound                      = errors.New("active distributed run not found")
	ErrWorkerHeartbeatNotFound                = errors.New("worker heartbeat not found")
)

// DefaultStaleWorkerHeartbeatThreshold is the default duration after which a
// worker heartbeat is no longer considered healthy.
const DefaultStaleWorkerHeartbeatThreshold = 30 * time.Second

// CoordinatorEndpoint identifies a coordinator instance that owns a
// distributed task or run.
type CoordinatorEndpoint struct {
	ID   string `json:"id,omitempty"`
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

// DispatchTaskClaim describes a worker poller's attempt to claim a shared
// distributed task.
type DispatchTaskClaim struct {
	WorkerID     string
	PollerID     string
	Labels       map[string]string
	Owner        CoordinatorEndpoint
	ClaimTimeout time.Duration
}

// ClaimedDispatchTask is a shared pending task that has been claimed by a
// specific worker poller and must be acknowledged before execution begins.
type ClaimedDispatchTask struct {
	Task       *DispatchTask
	ClaimToken string
	ClaimedAt  time.Time
	WorkerID   string
	PollerID   string
	Owner      CoordinatorEndpoint
}

// DispatchAdmissionRequest describes a queue admission reservation request.
type DispatchAdmissionRequest struct {
	QueueName             string
	MaxConcurrency        int
	NonAdmissionOccupancy int
	AttemptKey            string
	AttemptID             string
	DAGRun                ir.DAGRunRef
	StaleThreshold        time.Duration
}

// DispatchAdmissionRejectReason identifies why a reservation was not granted.
type DispatchAdmissionRejectReason string

const (
	DispatchAdmissionRejectedDuplicate  DispatchAdmissionRejectReason = "duplicate"
	DispatchAdmissionRejectedNoCapacity DispatchAdmissionRejectReason = "no_capacity"
)

// DispatchAdmissionDecision is the durable queue admission result.
type DispatchAdmissionDecision struct {
	Reserved         bool
	Reason           DispatchAdmissionRejectReason
	ReservationToken string
}

// DispatchAdmissionBindRequest describes a coordinator bind for a reservation.
type DispatchAdmissionBindRequest struct {
	ReservationToken string
	Task             *DispatchTask
}

// DispatchAdmissionStore reserves queue capacity and binds reservations to tasks.
type DispatchAdmissionStore interface {
	ReserveAdmission(ctx context.Context, req DispatchAdmissionRequest) (*DispatchAdmissionDecision, error)
	BindAdmission(ctx context.Context, req DispatchAdmissionBindRequest) error
	ReleaseAdmissionToken(ctx context.Context, reservationToken string) error
	FinalizeAdmissionAttempt(ctx context.Context, attemptKey string) error
	CleanupAdmissions(ctx context.Context, staleThreshold time.Duration) error
}

// DispatchTaskStore manages the shared distributed dispatch queue.
type DispatchTaskStore interface {
	Enqueue(ctx context.Context, task *DispatchTask) error
	ClaimNext(ctx context.Context, claim DispatchTaskClaim) (*ClaimedDispatchTask, error)
	GetClaim(ctx context.Context, claimToken string) (*ClaimedDispatchTask, error)
	ReleaseClaim(ctx context.Context, claimToken string) error
	DeleteClaim(ctx context.Context, claimToken string) error
	ListBundleDigests(ctx context.Context) ([]string, error)
	CountOutstandingByQueue(ctx context.Context, queueName string, claimTimeout time.Duration) (int, error)
	HasOutstandingAttempt(ctx context.Context, attemptKey string, claimTimeout time.Duration) (bool, error)
}

// WorkerHeartbeatRecord is the shared presence record for a worker.
type WorkerHeartbeatRecord struct {
	WorkerID        string            `json:"workerId"`
	Labels          map[string]string `json:"labels,omitempty"`
	Stats           *WorkerStats      `json:"stats,omitempty"`
	LastHeartbeatAt int64             `json:"lastHeartbeatAt"`
}

// WorkerStats describes worker poller capacity and running distributed tasks.
type WorkerStats struct {
	TotalPollers int32          `json:"totalPollers,omitempty"`
	BusyPollers  int32          `json:"busyPollers,omitempty"`
	RunningTasks []*RunningTask `json:"runningTasks,omitempty"`
}

// RunningTask describes one task currently executing on a worker.
type RunningTask struct {
	DAGRunID         string `json:"dagRunId,omitempty"`
	DAGName          string `json:"dagName,omitempty"`
	StartedAt        int64  `json:"startedAt,omitempty"`
	RootDAGRunName   string `json:"rootDagRunName,omitempty"`
	RootDAGRunID     string `json:"rootDagRunId,omitempty"`
	ParentDAGRunName string `json:"parentDagRunName,omitempty"`
	ParentDAGRunID   string `json:"parentDagRunId,omitempty"`
	AttemptKey       string `json:"attemptKey,omitempty"`
}

// LastHeartbeatTime returns the last heartbeat as a time.
func (r WorkerHeartbeatRecord) LastHeartbeatTime() time.Time {
	if r.LastHeartbeatAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(r.LastHeartbeatAt).UTC()
}

// WorkerHeartbeatFresh reports whether a worker heartbeat is within the given freshness threshold.
func WorkerHeartbeatFresh(record WorkerHeartbeatRecord, now time.Time, staleThreshold time.Duration) bool {
	if staleThreshold <= 0 {
		return false
	}
	lastHeartbeat := record.LastHeartbeatTime()
	return !lastHeartbeat.IsZero() && now.Sub(lastHeartbeat) <= staleThreshold
}

// WorkerHeartbeatMatches reports whether a worker satisfies the selector and target worker requirements.
func WorkerHeartbeatMatches(record WorkerHeartbeatRecord, selector map[string]string, targetWorkerID string) bool {
	if targetWorkerID != "" && record.WorkerID != targetWorkerID {
		return false
	}
	for key, value := range selector {
		if record.Labels[key] != value {
			return false
		}
	}
	return true
}

// WorkerHeartbeatStore persists shared worker presence across coordinators.
type WorkerHeartbeatStore interface {
	Upsert(ctx context.Context, record WorkerHeartbeatRecord) error
	Get(ctx context.Context, workerID string) (*WorkerHeartbeatRecord, error)
	List(ctx context.Context) ([]WorkerHeartbeatRecord, error)
	DeleteStale(ctx context.Context, before time.Time) (int, error)
}

// DAGRunLease is the shared liveness record for an accepted worker claim.
type DAGRunLease struct {
	AttemptKey            string              `json:"attemptKey"`
	DAGRun                ir.DAGRunRef        `json:"dagRun"`
	Root                  ir.DAGRunRef        `json:"root,omitzero"`
	AttemptID             string              `json:"attemptId"`
	ProfileName           string              `json:"profileName,omitempty"`
	QueueName             string              `json:"queueName"`
	WorkerID              string              `json:"workerId"`
	Owner                 CoordinatorEndpoint `json:"owner"`
	ClaimToken            string              `json:"claimToken,omitempty"`
	WorkspaceBundleDigest string              `json:"workspaceBundleDigest,omitempty"`
	ClaimedAt             int64               `json:"claimedAt"`
	LastHeartbeatAt       int64               `json:"lastHeartbeatAt"`
}

// MatchesClaim reports whether the lease identifies the worker claim.
func (l *DAGRunLease) MatchesClaim(claimKey, workerID string) bool {
	return l != nil && claimKey != "" && workerID != "" &&
		l.AttemptKey == claimKey && l.WorkerID == workerID
}

// LastHeartbeatTime returns the last run heartbeat time.
func (l DAGRunLease) LastHeartbeatTime() time.Time {
	if l.LastHeartbeatAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(l.LastHeartbeatAt).UTC()
}

// IsFresh reports whether the lease is still alive.
func (l DAGRunLease) IsFresh(now time.Time, staleThreshold time.Duration) bool {
	if staleThreshold <= 0 || l.LastHeartbeatAt == 0 {
		return false
	}
	return now.Sub(l.LastHeartbeatTime()) < staleThreshold
}

// DAGRunLeaseStore persists live worker-claim leases.
type DAGRunLeaseStore interface {
	Upsert(ctx context.Context, lease DAGRunLease) error
	Touch(ctx context.Context, attemptKey string, observedAt time.Time) error
	Delete(ctx context.Context, attemptKey string) error
	Get(ctx context.Context, attemptKey string) (*DAGRunLease, error)
	ListByQueue(ctx context.Context, queueName string) ([]DAGRunLease, error)
	ListAll(ctx context.Context) ([]DAGRunLease, error)
}

// ActiveDistributedRun is the durable active-set record for a remote attempt.
type ActiveDistributedRun struct {
	AttemptKey string       `json:"attemptKey"`
	DAGRun     ir.DAGRunRef `json:"dagRun"`
	Root       ir.DAGRunRef `json:"root,omitzero"`
	AttemptID  string       `json:"attemptId"`
	WorkerID   string       `json:"workerId"`
	Status     ir.Status    `json:"status"`
	UpdatedAt  int64        `json:"updatedAt"`
}

// ActiveDistributedRunStore persists the coordinator-owned active distributed
// attempt index used by the zombie detector.
type ActiveDistributedRunStore interface {
	Upsert(ctx context.Context, record ActiveDistributedRun) error
	Delete(ctx context.Context, attemptKey string) error
	Get(ctx context.Context, attemptKey string) (*ActiveDistributedRun, error)
	ListAll(ctx context.Context) ([]ActiveDistributedRun, error)
}

// IsRemoteWorkerID reports whether the status originated from a distributed
// worker instead of the local process runtime.
func IsRemoteWorkerID(workerID string) bool {
	return workerID != "" && workerID != "local"
}

// AttemptKeyForStatus resolves the authoritative attempt key for a persisted
// status. When older statuses omit AttemptKey, it is regenerated from the
// stored DAG-run identity and attempt ID.
func AttemptKeyForStatus(status *ir.DAGRunStatus, fallbackAttemptID string) string {
	if status == nil {
		return ""
	}
	if status.AttemptKey != "" {
		return status.AttemptKey
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if attemptID == "" {
		return ""
	}

	root := status.Root
	if root.Zero() {
		// Older root-run snapshots may omit Root entirely, but legacy sub-DAG
		// snapshots can also be missing Root/AttemptKey. Reconstructing a
		// self-rooted key is only safe when the status has no parent.
		if !status.Parent.Zero() {
			return ""
		}
		root = status.DAGRun()
	}

	return ir.GenerateAttemptKey(root.Name, root.ID, status.Name, status.DAGRunID, attemptID)
}

// LeaseMatchesStatus reports whether the lease still authoritatively belongs to
// the exact distributed attempt represented by the persisted status.
func LeaseMatchesStatus(
	lease *DAGRunLease,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
	now time.Time,
	staleThreshold time.Duration,
) bool {
	if lease == nil || status == nil {
		return false
	}
	if !lease.IsFresh(now, staleThreshold) {
		return false
	}
	return LeaseIdentityMatchesStatus(lease, status, fallbackAttemptID)
}

// LeaseIdentityMatchesStatus reports whether the lease belongs to the same
// persisted distributed attempt as status, independent of freshness.
func LeaseIdentityMatchesStatus(
	lease *DAGRunLease,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
) bool {
	if lease == nil || status == nil {
		return false
	}
	attemptKey := AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey != "" && lease.AttemptKey != attemptKey {
		return false
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if attemptID != "" && lease.AttemptID != "" && lease.AttemptID != attemptID {
		return false
	}

	if status.DAGRunID != "" && lease.DAGRun != status.DAGRun() {
		return false
	}
	if !status.Root.Zero() && !lease.Root.Zero() && lease.Root != status.Root {
		return false
	}
	if status.WorkerID != "" && lease.WorkerID != "" && lease.WorkerID != status.WorkerID {
		return false
	}

	return true
}

// DistributedLeaseExpiredReason is the canonical failure reason for a
// distributed attempt that lost authoritative worker ownership.
func DistributedLeaseExpiredReason(workerID string) string {
	return fmt.Sprintf(
		"distributed run lease expired: worker %s accepted the task claim but stopped reporting to the owner coordinator",
		workerID,
	)
}
