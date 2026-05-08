// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var (
	_ exec.DispatchTaskStore         = (*dispatchTaskStore)(nil)
	_ exec.WorkerHeartbeatStore      = (*workerHeartbeatStore)(nil)
	_ exec.DAGRunLeaseStore          = (*dagRunLeaseStore)(nil)
	_ exec.ActiveDistributedRunStore = (*activeDistributedRunStore)(nil)
)

var (
	protoJSONMarshal   = protojson.MarshalOptions{}
	protoJSONUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

type dispatchTaskStore struct {
	store *Store
}

type workerHeartbeatStore struct {
	store *Store
}

type dagRunLeaseStore struct {
	store *Store
}

type activeDistributedRunStore struct {
	store *Store
}

// DispatchTasks returns the distributed dispatch task store.
func (s *Store) DispatchTasks() exec.DispatchTaskStore {
	return &dispatchTaskStore{store: s}
}

// WorkerHeartbeats returns the distributed worker heartbeat store.
func (s *Store) WorkerHeartbeats() exec.WorkerHeartbeatStore {
	return &workerHeartbeatStore{store: s}
}

// DAGRunLeases returns the distributed DAG-run lease store.
func (s *Store) DAGRunLeases() exec.DAGRunLeaseStore {
	return &dagRunLeaseStore{store: s}
}

// ActiveDistributedRuns returns the active distributed run store.
func (s *Store) ActiveDistributedRuns() exec.ActiveDistributedRunStore {
	return &activeDistributedRunStore{store: s}
}

// Enqueue adds a distributed task to the shared dispatch queue.
func (s *dispatchTaskStore) Enqueue(ctx context.Context, task *coordinatorv1.Task) error {
	if task == nil {
		return errors.New("task is required")
	}
	if task.QueueName == "" {
		return errors.New("task queue name is required")
	}
	if task.AttemptKey == "" {
		return errors.New("task attempt key is required")
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate dispatch task id: %w", err)
	}
	selector, err := json.Marshal(task.WorkerSelector)
	if err != nil {
		return fmt.Errorf("marshal worker selector: %w", err)
	}
	taskData, err := marshalTask(task)
	if err != nil {
		return err
	}

	return s.store.queries.EnqueueDispatchTask(ctx, db.EnqueueDispatchTaskParams{
		ID:             id,
		QueueName:      task.QueueName,
		AttemptKey:     task.AttemptKey,
		WorkerSelector: selector,
		Data:           taskData,
		EnqueuedAt:     timestamptz(time.Now().UTC()),
	})
}

// ClaimNext claims the next dispatch task matching a worker's labels.
func (s *dispatchTaskStore) ClaimNext(ctx context.Context, claim exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	if claim.ClaimTimeout <= 0 {
		claim.ClaimTimeout = exec.DefaultStaleLeaseThreshold
	}
	if err := s.store.pruneExpiredPendingDispatchTasks(ctx, claim.ClaimTimeout); err != nil {
		return nil, err
	}

	labels, err := json.Marshal(claim.Labels)
	if err != nil {
		return nil, fmt.Errorf("marshal worker labels: %w", err)
	}

	var claimed *exec.ClaimedDispatchTask
	err = s.store.withTx(ctx, func(q *db.Queries) error {
		row, err := q.FindClaimableDispatchTask(ctx, labels)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		task, err := taskFromJSON(row.Data)
		if err != nil {
			return err
		}
		if task == nil {
			return errors.New("dispatch task data is empty")
		}

		claimToken, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate dispatch claim token: %w", err)
		}
		claimedAt := time.Now().UTC()
		task, err = applyDispatchTaskClaim(task, claim.Owner, claimToken.String())
		if err != nil {
			return err
		}
		task.WorkerId = claim.WorkerID

		taskData, err := marshalTask(task)
		if err != nil {
			return err
		}
		row, err = q.ClaimDispatchTaskByID(ctx, db.ClaimDispatchTaskByIDParams{
			ClaimToken: uuid.NullUUID{
				UUID:  claimToken,
				Valid: true,
			},
			ClaimedAt:      timestamptz(claimedAt),
			ClaimExpiresAt: timestamptz(claimedAt.Add(claim.ClaimTimeout)),
			WorkerID:       pgtype.Text{String: claim.WorkerID, Valid: claim.WorkerID != ""},
			PollerID:       pgtype.Text{String: claim.PollerID, Valid: claim.PollerID != ""},
			OwnerID:        claim.Owner.ID,
			OwnerHost:      claim.Owner.Host,
			OwnerPort:      pgtype.Int4{Int32: int32(claim.Owner.Port), Valid: true}, //nolint:gosec
			Data:           taskData,
			ID:             row.ID,
		})
		if err != nil {
			return err
		}

		claimed, err = dispatchClaimFromRow(row)
		return err
	})
	return claimed, err
}

// GetClaim returns a claimed dispatch task by claim token.
func (s *dispatchTaskStore) GetClaim(ctx context.Context, claimToken string) (*exec.ClaimedDispatchTask, error) {
	token, err := parseLeaseToken(claimToken)
	if err != nil {
		return nil, exec.ErrDispatchTaskNotFound
	}
	row, err := s.store.queries.GetDispatchTaskClaim(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, exec.ErrDispatchTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return dispatchClaimFromRow(row)
}

// DeleteClaim deletes an acknowledged dispatch task claim.
func (s *dispatchTaskStore) DeleteClaim(ctx context.Context, claimToken string) error {
	token, err := parseLeaseToken(claimToken)
	if err != nil {
		return nil
	}
	_, err = s.store.queries.DeleteDispatchTaskClaim(ctx, token)
	return err
}

// CountOutstandingByQueue counts pending or non-expired claimed dispatch tasks.
func (s *dispatchTaskStore) CountOutstandingByQueue(ctx context.Context, queueName string, claimTimeout time.Duration) (int, error) {
	if err := s.store.pruneExpiredPendingDispatchTasks(ctx, claimTimeout); err != nil {
		return 0, err
	}
	count, err := s.store.queries.CountOutstandingDispatchTasksByQueue(ctx, queueName)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// HasOutstandingAttempt reports whether an attempt already has an active dispatch reservation.
func (s *dispatchTaskStore) HasOutstandingAttempt(ctx context.Context, attemptKey string, claimTimeout time.Duration) (bool, error) {
	if attemptKey == "" {
		return false, nil
	}
	if err := s.store.pruneExpiredPendingDispatchTasks(ctx, claimTimeout); err != nil {
		return false, err
	}
	return s.store.queries.HasOutstandingDispatchTaskAttempt(ctx, attemptKey)
}

func (s *Store) pruneExpiredPendingDispatchTasks(ctx context.Context, claimTimeout time.Duration) error {
	if claimTimeout <= 0 {
		claimTimeout = exec.DefaultStaleLeaseThreshold
	}
	seconds := int64(claimTimeout.Seconds())
	if seconds <= 0 {
		seconds = int64(exec.DefaultStaleLeaseThreshold.Seconds())
	}
	_, err := s.queries.DeleteExpiredPendingDispatchTasks(ctx, seconds)
	return err
}

// Upsert stores a worker heartbeat.
func (s *workerHeartbeatStore) Upsert(ctx context.Context, record exec.WorkerHeartbeatRecord) error {
	if record.WorkerID == "" {
		return errors.New("worker id is required")
	}
	if record.LastHeartbeatAt == 0 {
		record.LastHeartbeatAt = time.Now().UTC().UnixMilli()
	}
	labels, err := json.Marshal(record.Labels)
	if err != nil {
		return fmt.Errorf("marshal worker labels: %w", err)
	}
	stats, err := marshalWorkerStats(record.Stats)
	if err != nil {
		return err
	}
	data, err := marshalWorkerHeartbeat(record, stats)
	if err != nil {
		return err
	}
	return s.store.queries.UpsertWorkerHeartbeat(ctx, db.UpsertWorkerHeartbeatParams{
		WorkerID:        record.WorkerID,
		Labels:          labels,
		Stats:           stats,
		LastHeartbeatAt: timestamptz(record.LastHeartbeatTime()),
		Data:            data,
	})
}

// Get returns a worker heartbeat by worker ID.
func (s *workerHeartbeatStore) Get(ctx context.Context, workerID string) (*exec.WorkerHeartbeatRecord, error) {
	row, err := s.store.queries.GetWorkerHeartbeat(ctx, workerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, exec.ErrWorkerHeartbeatNotFound
	}
	if err != nil {
		return nil, err
	}
	return workerHeartbeatFromRow(row)
}

// List returns all worker heartbeat records.
func (s *workerHeartbeatStore) List(ctx context.Context) ([]exec.WorkerHeartbeatRecord, error) {
	rows, err := s.store.queries.ListWorkerHeartbeats(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]exec.WorkerHeartbeatRecord, 0, len(rows))
	for _, row := range rows {
		record, err := workerHeartbeatFromRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, *record)
	}
	return records, nil
}

// DeleteStale deletes worker heartbeats older than before.
func (s *workerHeartbeatStore) DeleteStale(ctx context.Context, before time.Time) (int, error) {
	rows, err := s.store.queries.DeleteStaleWorkerHeartbeats(ctx, timestamptz(before))
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// Upsert stores an active distributed DAG-run lease.
func (s *dagRunLeaseStore) Upsert(ctx context.Context, lease exec.DAGRunLease) error {
	if lease.AttemptKey == "" {
		return errors.New("attempt key is required")
	}
	if lease.ClaimedAt == 0 {
		now := time.Now().UTC().UnixMilli()
		lease.ClaimedAt = now
		if lease.LastHeartbeatAt == 0 {
			lease.LastHeartbeatAt = now
		}
	}
	if lease.LastHeartbeatAt == 0 {
		lease.LastHeartbeatAt = time.Now().UTC().UnixMilli()
	}

	root := lease.Root
	if root.Zero() {
		root = lease.DAGRun
	}
	lease.Root = root
	data, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("marshal DAG-run lease: %w", err)
	}

	return s.store.queries.UpsertDAGRunLease(ctx, db.UpsertDAGRunLeaseParams{
		AttemptKey:      lease.AttemptKey,
		DagName:         lease.DAGRun.Name,
		DagRunID:        lease.DAGRun.ID,
		RootDagName:     root.Name,
		RootDagRunID:    root.ID,
		AttemptID:       lease.AttemptID,
		QueueName:       lease.QueueName,
		WorkerID:        lease.WorkerID,
		OwnerID:         lease.Owner.ID,
		OwnerHost:       lease.Owner.Host,
		OwnerPort:       pgtype.Int4{Int32: int32(lease.Owner.Port), Valid: true}, //nolint:gosec
		ClaimedAt:       timestamptz(lease.ClaimedTime()),
		LastHeartbeatAt: timestamptz(lease.LastHeartbeatTime()),
		Data:            data,
	})
}

// Touch refreshes a DAG-run lease heartbeat.
func (s *dagRunLeaseStore) Touch(ctx context.Context, attemptKey string, observedAt time.Time) error {
	rows, err := s.store.queries.TouchDAGRunLease(ctx, db.TouchDAGRunLeaseParams{
		LastHeartbeatAt:       timestamptz(observedAt),
		LastHeartbeatAtMillis: observedAt.UTC().UnixMilli(),
		AttemptKey:            attemptKey,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return exec.ErrDAGRunLeaseNotFound
	}
	return nil
}

// Delete deletes a DAG-run lease.
func (s *dagRunLeaseStore) Delete(ctx context.Context, attemptKey string) error {
	_, err := s.store.queries.DeleteDAGRunLease(ctx, attemptKey)
	return err
}

func (s *dagRunLeaseStore) Get(ctx context.Context, attemptKey string) (*exec.DAGRunLease, error) {
	row, err := s.store.queries.GetDAGRunLease(ctx, attemptKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, exec.ErrDAGRunLeaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return dagRunLeaseFromRow(row)
}

// ListByQueue lists DAG-run leases for one queue.
func (s *dagRunLeaseStore) ListByQueue(ctx context.Context, queueName string) ([]exec.DAGRunLease, error) {
	rows, err := s.store.queries.ListDAGRunLeasesByQueue(ctx, queueName)
	if err != nil {
		return nil, err
	}
	return dagRunLeasesFromRows(rows)
}

// ListAll lists all DAG-run leases.
func (s *dagRunLeaseStore) ListAll(ctx context.Context) ([]exec.DAGRunLease, error) {
	rows, err := s.store.queries.ListAllDAGRunLeases(ctx)
	if err != nil {
		return nil, err
	}
	return dagRunLeasesFromRows(rows)
}

// Upsert stores an active distributed run record.
func (s *activeDistributedRunStore) Upsert(ctx context.Context, record exec.ActiveDistributedRun) error {
	if record.AttemptKey == "" {
		return errors.New("attempt key is required")
	}
	if record.UpdatedAt == 0 {
		record.UpdatedAt = time.Now().UTC().UnixMilli()
	}
	root := record.Root
	if root.Zero() {
		root = record.DAGRun
	}
	record.Root = root
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal active distributed run: %w", err)
	}
	return s.store.queries.UpsertActiveDistributedRun(ctx, db.UpsertActiveDistributedRunParams{
		AttemptKey:   record.AttemptKey,
		DagName:      record.DAGRun.Name,
		DagRunID:     record.DAGRun.ID,
		RootDagName:  root.Name,
		RootDagRunID: root.ID,
		AttemptID:    record.AttemptID,
		WorkerID:     record.WorkerID,
		Status:       int32(record.Status), //nolint:gosec
		ObservedAt:   timestamptz(time.UnixMilli(record.UpdatedAt).UTC()),
		Data:         data,
	})
}

// Delete deletes an active distributed run.
func (s *activeDistributedRunStore) Delete(ctx context.Context, attemptKey string) error {
	_, err := s.store.queries.DeleteActiveDistributedRun(ctx, attemptKey)
	return err
}

func (s *activeDistributedRunStore) Get(ctx context.Context, attemptKey string) (*exec.ActiveDistributedRun, error) {
	row, err := s.store.queries.GetActiveDistributedRun(ctx, attemptKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, exec.ErrActiveRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return activeDistributedRunFromRow(row)
}

// ListAll lists all active distributed runs.
func (s *activeDistributedRunStore) ListAll(ctx context.Context) ([]exec.ActiveDistributedRun, error) {
	rows, err := s.store.queries.ListActiveDistributedRuns(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]exec.ActiveDistributedRun, 0, len(rows))
	for _, row := range rows {
		record, err := activeDistributedRunFromRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, *record)
	}
	return records, nil
}

func marshalTask(task *coordinatorv1.Task) ([]byte, error) {
	data, err := protoJSONMarshal.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("marshal dispatch task: %w", err)
	}
	return data, nil
}

func taskFromJSON(data []byte) (*coordinatorv1.Task, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var task coordinatorv1.Task
	if err := protoJSONUnmarshal.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("unmarshal dispatch task: %w", err)
	}
	return &task, nil
}

func dispatchClaimFromRow(row db.DaguDispatchTask) (*exec.ClaimedDispatchTask, error) {
	task, err := taskFromJSON(row.Data)
	if err != nil {
		return nil, err
	}
	claimToken := ""
	if row.ClaimToken.Valid {
		claimToken = row.ClaimToken.UUID.String()
	}
	return &exec.ClaimedDispatchTask{
		Task:       cloneTask(task),
		ClaimToken: claimToken,
		ClaimedAt:  timeFromTimestamptz(row.ClaimedAt),
		WorkerID:   row.WorkerID.String,
		PollerID:   row.PollerID.String,
		Owner: exec.CoordinatorEndpoint{
			ID:   row.OwnerID.String,
			Host: row.OwnerHost.String,
			Port: int(row.OwnerPort.Int32),
		},
	}, nil
}

func applyDispatchTaskClaim(task *coordinatorv1.Task, owner exec.CoordinatorEndpoint, claimToken string) (*coordinatorv1.Task, error) {
	task = cloneTask(task)
	if task == nil {
		return nil, nil
	}
	if owner.Port < 0 || owner.Port > math.MaxInt32 {
		return nil, fmt.Errorf("owner coordinator port out of range: %d", owner.Port)
	}
	task.OwnerCoordinatorId = owner.ID
	task.OwnerCoordinatorHost = owner.Host
	task.OwnerCoordinatorPort = int32(owner.Port)
	task.ClaimToken = claimToken
	return task, nil
}

func cloneTask(task *coordinatorv1.Task) *coordinatorv1.Task {
	if task == nil {
		return nil
	}
	cloned, ok := proto.Clone(task).(*coordinatorv1.Task)
	if !ok {
		return nil
	}
	return cloned
}

func marshalWorkerStats(stats *coordinatorv1.WorkerStats) ([]byte, error) {
	if stats == nil {
		return nil, nil
	}
	data, err := protoJSONMarshal.Marshal(stats)
	if err != nil {
		return nil, fmt.Errorf("marshal worker stats: %w", err)
	}
	return data, nil
}

func workerStatsFromJSON(data []byte) (*coordinatorv1.WorkerStats, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var stats coordinatorv1.WorkerStats
	if err := protoJSONUnmarshal.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("unmarshal worker stats: %w", err)
	}
	return &stats, nil
}

func marshalWorkerHeartbeat(record exec.WorkerHeartbeatRecord, stats []byte) ([]byte, error) {
	payload := struct {
		WorkerID        string            `json:"workerId"`
		Labels          map[string]string `json:"labels,omitempty"`
		Stats           json.RawMessage   `json:"stats,omitempty"`
		LastHeartbeatAt int64             `json:"lastHeartbeatAt"`
	}{
		WorkerID:        record.WorkerID,
		Labels:          record.Labels,
		LastHeartbeatAt: record.LastHeartbeatAt,
	}
	if len(stats) > 0 {
		payload.Stats = stats
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal worker heartbeat: %w", err)
	}
	return data, nil
}

func workerHeartbeatFromRow(row db.DaguWorkerHeartbeat) (*exec.WorkerHeartbeatRecord, error) {
	var labels map[string]string
	if len(row.Labels) > 0 {
		if err := json.Unmarshal(row.Labels, &labels); err != nil {
			return nil, fmt.Errorf("unmarshal worker labels: %w", err)
		}
	}
	stats, err := workerStatsFromJSON(row.Stats)
	if err != nil {
		return nil, err
	}
	return &exec.WorkerHeartbeatRecord{
		WorkerID:        row.WorkerID,
		Labels:          labels,
		Stats:           stats,
		LastHeartbeatAt: timeFromTimestamptz(row.LastHeartbeatAt).UnixMilli(),
	}, nil
}

func dagRunLeaseFromRow(row db.DaguDagRunLease) (*exec.DAGRunLease, error) {
	var lease exec.DAGRunLease
	if len(row.Data) > 0 {
		if err := json.Unmarshal(row.Data, &lease); err != nil {
			return nil, fmt.Errorf("unmarshal DAG-run lease: %w", err)
		}
	}
	if lease.AttemptKey == "" {
		lease = exec.DAGRunLease{
			AttemptKey:      row.AttemptKey,
			DAGRun:          exec.NewDAGRunRef(row.DagName, row.DagRunID),
			Root:            exec.NewDAGRunRef(row.RootDagName, row.RootDagRunID),
			AttemptID:       row.AttemptID,
			QueueName:       row.QueueName,
			WorkerID:        row.WorkerID,
			Owner:           endpointFromRow(row.OwnerID, row.OwnerHost, row.OwnerPort),
			ClaimedAt:       timeFromTimestamptz(row.ClaimedAt).UnixMilli(),
			LastHeartbeatAt: timeFromTimestamptz(row.LastHeartbeatAt).UnixMilli(),
		}
	}
	return &lease, nil
}

func dagRunLeasesFromRows(rows []db.DaguDagRunLease) ([]exec.DAGRunLease, error) {
	leases := make([]exec.DAGRunLease, 0, len(rows))
	for _, row := range rows {
		lease, err := dagRunLeaseFromRow(row)
		if err != nil {
			return nil, err
		}
		leases = append(leases, *lease)
	}
	return leases, nil
}

func activeDistributedRunFromRow(row db.DaguActiveDistributedRun) (*exec.ActiveDistributedRun, error) {
	var record exec.ActiveDistributedRun
	if len(row.Data) > 0 {
		if err := json.Unmarshal(row.Data, &record); err != nil {
			return nil, fmt.Errorf("unmarshal active distributed run: %w", err)
		}
	}
	if record.AttemptKey == "" {
		record = exec.ActiveDistributedRun{
			AttemptKey: row.AttemptKey,
			DAGRun:     exec.NewDAGRunRef(row.DagName, row.DagRunID),
			Root:       exec.NewDAGRunRef(row.RootDagName, row.RootDagRunID),
			AttemptID:  row.AttemptID,
			WorkerID:   row.WorkerID,
			Status:     core.Status(row.Status),
			UpdatedAt:  timeFromTimestamptz(row.ObservedAt).UnixMilli(),
		}
	}
	return &record, nil
}

func endpointFromRow(id pgtype.Text, host pgtype.Text, port pgtype.Int4) exec.CoordinatorEndpoint {
	return exec.CoordinatorEndpoint{
		ID:   id.String,
		Host: host.String,
		Port: int(port.Int32),
	}
}
