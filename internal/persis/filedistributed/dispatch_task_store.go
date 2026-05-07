// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filedistributed

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	dispatchTaskStoreVersion      = 1
	defaultDispatchReservationTTL = exec.DefaultStaleLeaseThreshold
)

type DispatchTaskStoreOption func(*DispatchTaskStore)

type DispatchTaskStore struct {
	baseDir        string
	reservationTTL time.Duration
	mu             sync.Mutex
}

type dispatchTaskFile struct {
	Version      int                      `json:"version"`
	Task         *coordinatorv1.Task      `json:"task"`
	TaskFileName string                   `json:"taskFileName"`
	EnqueuedAt   int64                    `json:"enqueuedAt"`
	ClaimToken   string                   `json:"claimToken,omitempty"`
	ClaimedAt    int64                    `json:"claimedAt,omitempty"`
	WorkerID     string                   `json:"workerId,omitempty"`
	PollerID     string                   `json:"pollerId,omitempty"`
	Owner        exec.CoordinatorEndpoint `json:"owner,omitzero"`
}

func WithDispatchReservationTTL(ttl time.Duration) DispatchTaskStoreOption {
	return func(store *DispatchTaskStore) {
		store.reservationTTL = normalizeDispatchReservationTTL(ttl)
	}
}

func NewDispatchTaskStore(baseDir string, opts ...DispatchTaskStoreOption) *DispatchTaskStore {
	store := &DispatchTaskStore{
		baseDir:        baseDir,
		reservationTTL: defaultDispatchReservationTTL,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *DispatchTaskStore) pendingDir() string {
	return filepath.Join(s.baseDir, "pending")
}

func (s *DispatchTaskStore) claimsDir() string {
	return filepath.Join(s.baseDir, "claims")
}

func (s *DispatchTaskStore) claimPath(claimToken string) string {
	return filepath.Join(s.claimsDir(), "claim_"+encodeKey(claimToken)+".json")
}

func (s *DispatchTaskStore) Enqueue(_ context.Context, task *coordinatorv1.Task) error {
	if task == nil {
		return fmt.Errorf("task is required")
	}

	enqueuedAt := time.Now().UTC()
	fileName := fmt.Sprintf("task_%020d_%s.json", enqueuedAt.UnixMilli(), uuid.Must(uuid.NewV7()).String())
	record := dispatchTaskFile{
		Version:      dispatchTaskStoreVersion,
		Task:         cloneTask(task),
		TaskFileName: fileName,
		EnqueuedAt:   enqueuedAt.UnixMilli(),
	}

	return writeJSONAtomic(filepath.Join(s.pendingDir(), fileName), record)
}

func (s *DispatchTaskStore) ClaimNext(_ context.Context, claim exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.recycleExpiredReservations(); err != nil {
		return nil, err
	}

	files, err := sortedFiles(s.pendingDir())
	if err != nil {
		return nil, err
	}

	for _, pendingPath := range files {
		record, err := s.readTaskFile(pendingPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if record.Task == nil || !matchesSelector(claim.Labels, record.Task.WorkerSelector) {
			continue
		}

		claimToken := uuid.Must(uuid.NewV7()).String()
		claimedAt := time.Now().UTC()
		claimPath := s.claimPath(claimToken)
		if err := ensureDir(filepath.Dir(claimPath)); err != nil {
			return nil, err
		}
		if err := renameFile(pendingPath, claimPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("claim dispatch task %s: %w", pendingPath, err)
		}
		if err := os.Chtimes(claimPath, claimedAt, claimedAt); err != nil && !os.IsNotExist(err) {
			_ = renameFile(claimPath, pendingPath)
			return nil, fmt.Errorf("stamp claim file %s: %w", claimPath, err)
		}

		record.ClaimToken = claimToken
		record.ClaimedAt = claimedAt.UnixMilli()
		record.WorkerID = claim.WorkerID
		record.PollerID = claim.PollerID
		record.Owner = claim.Owner
		record.Task, err = applyTaskClaim(record.Task, claim.Owner, claimToken)
		if err != nil {
			_ = renameFile(claimPath, pendingPath)
			return nil, err
		}

		if err := writeJSONAtomic(claimPath, record); err != nil {
			_ = renameFile(claimPath, pendingPath)
			return nil, err
		}

		return &exec.ClaimedDispatchTask{
			Task:       cloneTask(record.Task),
			ClaimToken: claimToken,
			ClaimedAt:  claimedAt,
			WorkerID:   claim.WorkerID,
			PollerID:   claim.PollerID,
			Owner:      claim.Owner,
		}, nil
	}

	return nil, nil
}

func (s *DispatchTaskStore) GetClaim(_ context.Context, claimToken string) (*exec.ClaimedDispatchTask, error) {
	record, err := s.readTaskFile(s.claimPath(claimToken))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, exec.ErrDispatchTaskNotFound
		}
		return nil, err
	}
	if record.Task == nil || record.ClaimToken == "" || record.ClaimToken != claimToken || record.ClaimedAt == 0 {
		return nil, exec.ErrDispatchTaskNotFound
	}

	return &exec.ClaimedDispatchTask{
		Task:       cloneTask(record.Task),
		ClaimToken: record.ClaimToken,
		ClaimedAt:  time.UnixMilli(record.ClaimedAt).UTC(),
		WorkerID:   record.WorkerID,
		PollerID:   record.PollerID,
		Owner:      record.Owner,
	}, nil
}

func (s *DispatchTaskStore) DeleteClaim(_ context.Context, claimToken string) error {
	err := removeFile(s.claimPath(claimToken))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *DispatchTaskStore) CountOutstandingByQueue(_ context.Context, queueName string, _ time.Duration) (int, error) {
	paths, err := s.snapshotOutstandingPaths()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, path := range paths {
		record, readErr := s.readTaskFile(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return 0, readErr
		}
		if record == nil || record.Task == nil {
			continue
		}
		if queueName != "" && record.Task.QueueName != queueName {
			continue
		}
		count++
	}

	return count, nil
}

func (s *DispatchTaskStore) HasOutstandingAttempt(_ context.Context, attemptKey string, _ time.Duration) (bool, error) {
	if attemptKey == "" {
		return false, nil
	}

	paths, err := s.snapshotOutstandingPaths()
	if err != nil {
		return false, err
	}

	for _, path := range paths {
		record, readErr := s.readTaskFile(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return false, readErr
		}
		if record == nil || record.Task == nil {
			continue
		}
		if record.Task.AttemptKey == attemptKey {
			return true, nil
		}
	}

	return false, nil
}

func (s *DispatchTaskStore) recycleExpiredClaims() error {
	files, err := sortedFiles(s.claimsDir())
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, claimPath := range files {
		record, readErr := s.readTaskFile(claimPath)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return readErr
		}

		claimedAt, err := recordTimestamp(claimPath, record.ClaimedAt)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if now.Sub(claimedAt) < s.reservationTTL {
			continue
		}

		record.EnqueuedAt = now.UnixMilli()
		record.ClaimToken = ""
		record.ClaimedAt = 0
		record.WorkerID = ""
		record.PollerID = ""
		record.Owner = exec.CoordinatorEndpoint{}
		record.Task = clearTaskClaim(record.Task)
		if err := writeJSONAtomic(claimPath, record); err != nil {
			return err
		}

		pendingPath := filepath.Join(s.pendingDir(), record.TaskFileName)
		if err := ensureDir(filepath.Dir(pendingPath)); err != nil {
			return err
		}
		if err := renameFile(claimPath, pendingPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("requeue expired claim %s: %w", claimPath, err)
		}
	}

	return nil
}

func (s *DispatchTaskStore) recycleExpiredPending() error {
	files, err := sortedFiles(s.pendingDir())
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, pendingPath := range files {
		record, readErr := s.readTaskFile(pendingPath)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return readErr
		}

		enqueuedAt, err := recordTimestamp(pendingPath, record.EnqueuedAt)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if now.Sub(enqueuedAt) < s.reservationTTL {
			continue
		}

		if err := removeFile(pendingPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove expired pending dispatch task %s: %w", pendingPath, err)
		}
	}

	return nil
}

func (s *DispatchTaskStore) recycleExpiredReservations() error {
	if err := s.recycleExpiredClaims(); err != nil {
		return err
	}
	return s.recycleExpiredPending()
}

func (s *DispatchTaskStore) snapshotOutstandingPaths() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.recycleExpiredReservations(); err != nil {
		return nil, err
	}

	return s.listOutstandingPathsLocked()
}

func (s *DispatchTaskStore) listOutstandingPathsLocked() ([]string, error) {
	var paths []string
	for _, dir := range []string{s.pendingDir(), s.claimsDir()} {
		files, err := sortedFiles(dir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, files...)
	}
	return paths, nil
}

func (s *DispatchTaskStore) readTaskFile(path string) (*dispatchTaskFile, error) {
	var record dispatchTaskFile
	if err := readJSONFile(path, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func normalizeDispatchReservationTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultDispatchReservationTTL
	}
	return ttl
}

func recordTimestamp(path string, unixMillis int64) (time.Time, error) {
	if unixMillis > 0 {
		return time.UnixMilli(unixMillis).UTC(), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime().UTC(), nil
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

func applyTaskClaim(task *coordinatorv1.Task, owner exec.CoordinatorEndpoint, claimToken string) (*coordinatorv1.Task, error) {
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

func clearTaskClaim(task *coordinatorv1.Task) *coordinatorv1.Task {
	task = cloneTask(task)
	if task == nil {
		return nil
	}
	task.OwnerCoordinatorId = ""
	task.OwnerCoordinatorHost = ""
	task.OwnerCoordinatorPort = 0
	task.ClaimToken = ""
	task.WorkerId = ""
	return task
}

func matchesSelector(workerLabels, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}
	for key, value := range selector {
		if workerLabels[key] != value {
			return false
		}
	}
	return true
}
