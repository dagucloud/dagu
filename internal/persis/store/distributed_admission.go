// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	dispatchAdmissionStoreVersion = 1

	dispatchAdmissionAttemptsPrefix = "admissions/attempts/"
	dispatchAdmissionSlotsPrefix    = "admissions/slots/"
	dispatchAdmissionTokensPrefix   = "admissions/tokens/"
)

type dispatchAdmissionState string

const (
	dispatchAdmissionReserved dispatchAdmissionState = "reserved"
	dispatchAdmissionBinding  dispatchAdmissionState = "binding"
	dispatchAdmissionBound    dispatchAdmissionState = "bound"
)

type dispatchAdmissionAttemptPayload struct {
	Version          int                    `json:"version"`
	QueueName        string                 `json:"queueName"`
	AttemptKey       string                 `json:"attemptKey"`
	AttemptID        string                 `json:"attemptId"`
	DAGRun           exec.DAGRunRef         `json:"dagRun"`
	State            dispatchAdmissionState `json:"state"`
	ReservationToken string                 `json:"reservationToken"`
	SlotID           string                 `json:"slotId,omitempty"`
	PendingRecordID  string                 `json:"pendingRecordId"`
	PendingFileName  string                 `json:"pendingFileName"`
	TaskFingerprint  string                 `json:"taskFingerprint,omitempty"`
	CreatedAt        int64                  `json:"createdAt"`
	UpdatedAt        int64                  `json:"updatedAt"`
	BindStartedAt    int64                  `json:"bindStartedAt,omitempty"`
	BoundAt          int64                  `json:"boundAt,omitempty"`
}

type dispatchAdmissionSlotPayload struct {
	Version          int            `json:"version"`
	QueueName        string         `json:"queueName"`
	AttemptKey       string         `json:"attemptKey"`
	AttemptID        string         `json:"attemptId"`
	DAGRun           exec.DAGRunRef `json:"dagRun"`
	ReservationToken string         `json:"reservationToken"`
	CreatedAt        int64          `json:"createdAt"`
}

type dispatchAdmissionTokenPayload struct {
	Version          int    `json:"version"`
	ReservationToken string `json:"reservationToken"`
	AttemptRecordID  string `json:"attemptRecordId"`
	AttemptKey       string `json:"attemptKey"`
	CreatedAt        int64  `json:"createdAt"`
}

func (s *DispatchTaskStore) ReserveAdmission(
	ctx context.Context,
	req exec.DispatchAdmissionRequest,
) (*exec.DispatchAdmissionDecision, error) {
	if err := s.validateAdmissionRequest(req); err != nil {
		return nil, err
	}

	staleThreshold := normalizeDispatchReservationTTL(req.StaleThreshold)
	if err := s.CleanupAdmissions(ctx, staleThreshold); err != nil {
		return nil, err
	}

	occupied, err := s.dispatchAdmissionOccupancy(ctx, req.QueueName, req.NonAdmissionOccupancy, staleThreshold)
	if err != nil {
		return nil, err
	}
	if occupied >= req.MaxConcurrency {
		return &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedNoCapacity}, nil
	}

	now := time.Now().UTC()
	token := uuid.NewString()
	pendingFileName := deterministicAdmissionPendingFileName(req.QueueName, req.AttemptKey, token, now)
	pendingID, err := pendingDispatchRecordID(pendingFileName)
	if err != nil {
		return nil, err
	}

	attemptID := dispatchAdmissionAttemptRecordID(req.AttemptKey)
	attemptPayload := dispatchAdmissionAttemptPayload{
		Version:          dispatchAdmissionStoreVersion,
		QueueName:        req.QueueName,
		AttemptKey:       req.AttemptKey,
		AttemptID:        req.AttemptID,
		DAGRun:           req.DAGRun,
		State:            dispatchAdmissionReserved,
		ReservationToken: token,
		PendingRecordID:  pendingID,
		PendingFileName:  pendingFileName,
		CreatedAt:        now.UnixMilli(),
		UpdatedAt:        now.UnixMilli(),
	}
	attemptRec, err := newDispatchAdmissionRecord(attemptID, attemptPayload, now, now)
	if err != nil {
		return nil, err
	}
	if err := s.col.Create(ctx, attemptRec); err != nil {
		if errors.Is(err, persis.ErrConflict) {
			return &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedDuplicate}, nil
		}
		return nil, err
	}

	for {
		slots, err := s.listDispatchAdmissionSlots(ctx, req.QueueName)
		if err != nil {
			_ = s.col.Delete(context.WithoutCancel(ctx), attemptID)
			return nil, err
		}
		legacyOccupancy, err := s.dispatchLegacyAdmissionOccupancy(ctx, req.QueueName, staleThreshold)
		if err != nil {
			_ = s.col.Delete(context.WithoutCancel(ctx), attemptID)
			return nil, err
		}
		occupied = req.NonAdmissionOccupancy + len(slots) + legacyOccupancy
		if occupied >= req.MaxConcurrency {
			_ = s.col.Delete(context.WithoutCancel(ctx), attemptID)
			return &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedNoCapacity}, nil
		}

		for slotNo := 0; slotNo < req.MaxConcurrency; slotNo++ {
			slotID := dispatchAdmissionSlotRecordID(req.QueueName, slotNo)
			if _, ok := slots[slotID]; ok {
				continue
			}

			slotPayload := dispatchAdmissionSlotPayload{
				Version:          dispatchAdmissionStoreVersion,
				QueueName:        req.QueueName,
				AttemptKey:       req.AttemptKey,
				AttemptID:        req.AttemptID,
				DAGRun:           req.DAGRun,
				ReservationToken: token,
				CreatedAt:        now.UnixMilli(),
			}
			slotRec, err := newDispatchAdmissionRecord(slotID, slotPayload, now, now)
			if err != nil {
				_ = s.col.Delete(context.WithoutCancel(ctx), attemptID)
				return nil, err
			}
			if err := s.col.Create(ctx, slotRec); err != nil {
				if errors.Is(err, persis.ErrConflict) {
					continue
				}
				_ = s.col.Delete(context.WithoutCancel(ctx), attemptID)
				return nil, err
			}

			tokenRec, err := newDispatchAdmissionRecord(dispatchAdmissionTokenRecordID(token), dispatchAdmissionTokenPayload{
				Version:          dispatchAdmissionStoreVersion,
				ReservationToken: token,
				AttemptRecordID:  attemptID,
				AttemptKey:       req.AttemptKey,
				CreatedAt:        now.UnixMilli(),
			}, now, now)
			if err != nil {
				s.deleteAdmissionReservationRecords(context.WithoutCancel(ctx), attemptPayload, attemptID)
				_ = s.col.Delete(context.WithoutCancel(ctx), slotID)
				return nil, err
			}
			if err := s.col.Create(ctx, tokenRec); err != nil {
				s.deleteAdmissionReservationRecords(context.WithoutCancel(ctx), attemptPayload, attemptID)
				_ = s.col.Delete(context.WithoutCancel(ctx), slotID)
				return nil, err
			}

			attemptPayload.SlotID = slotID
			attemptPayload.UpdatedAt = time.Now().UTC().UnixMilli()
			nextAttemptRec, err := newDispatchAdmissionRecord(attemptID, attemptPayload, attemptRec.CreatedAt, time.Now().UTC())
			if err != nil {
				s.deleteAdmissionReservationRecords(context.WithoutCancel(ctx), attemptPayload, attemptID)
				return nil, err
			}
			if err := s.col.CompareAndSwap(ctx, attemptID, attemptRec.Data, nextAttemptRec.Data); err != nil {
				s.deleteAdmissionReservationRecords(context.WithoutCancel(ctx), attemptPayload, attemptID)
				if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
					return &exec.DispatchAdmissionDecision{Reason: exec.DispatchAdmissionRejectedDuplicate}, nil
				}
				return nil, err
			}

			return &exec.DispatchAdmissionDecision{
				Reserved:         true,
				ReservationToken: token,
				PendingRecordID:  pendingID,
			}, nil
		}
	}
}

func (s *DispatchTaskStore) BindAdmission(ctx context.Context, req exec.DispatchAdmissionBindRequest) error {
	if err := s.validateAdmissionConfigured(); err != nil {
		return err
	}
	if strings.TrimSpace(req.ReservationToken) == "" {
		return fmt.Errorf("reservation token is required")
	}
	if req.Task == nil {
		return fmt.Errorf("dispatch task is required")
	}

	taskFingerprint, err := dispatchAdmissionTaskFingerprint(req.Task)
	if err != nil {
		return err
	}

	return retryCAS(ctx, func(ctx context.Context) error {
		attemptRec, attemptPayload, err := s.loadAdmissionAttemptByToken(ctx, req.ReservationToken)
		if err != nil {
			return err
		}
		if attemptPayload.ReservationToken != req.ReservationToken {
			return exec.ErrDispatchAdmissionConflict
		}
		if attemptPayload.QueueName != req.Task.QueueName ||
			attemptPayload.AttemptKey != req.Task.AttemptKey ||
			attemptPayload.AttemptID != "" && req.Task.AttemptID != "" && attemptPayload.AttemptID != req.Task.AttemptID {
			return exec.ErrDispatchAdmissionConflict
		}
		if err := s.verifyAdmissionSlot(ctx, attemptPayload); err != nil {
			return err
		}
		if attemptPayload.State == dispatchAdmissionReserved &&
			dispatchAdmissionAttemptExpired(attemptRec, attemptPayload, s.reservationTTL) {
			evidence, err := s.dispatchAdmissionHasExecutionEvidence(ctx, attemptPayload, s.reservationTTL)
			if err != nil {
				return err
			}
			if !evidence {
				s.deleteAdmissionReservationRecords(ctx, attemptPayload, attemptRec.ID)
				return exec.ErrDispatchAdmissionNotFound
			}
		}

		switch attemptPayload.State {
		case dispatchAdmissionBound:
			if attemptPayload.TaskFingerprint != taskFingerprint {
				return exec.ErrDispatchAdmissionConflict
			}
			return nil
		case dispatchAdmissionBinding:
			if attemptPayload.TaskFingerprint != taskFingerprint {
				return exec.ErrDispatchAdmissionConflict
			}
			evidence, err := s.dispatchAdmissionHasExecutionEvidence(ctx, attemptPayload, s.reservationTTL)
			if err != nil {
				return err
			}
			if evidence {
				return s.markAdmissionAttemptBound(ctx, attemptRec, attemptPayload)
			}
		case dispatchAdmissionReserved:
			now := time.Now().UTC()
			nextPayload := attemptPayload
			nextPayload.State = dispatchAdmissionBinding
			nextPayload.TaskFingerprint = taskFingerprint
			nextPayload.BindStartedAt = now.UnixMilli()
			nextPayload.UpdatedAt = now.UnixMilli()
			nextData, err := persis.Encode(nextPayload)
			if err != nil {
				return err
			}
			if err := s.col.CompareAndSwap(ctx, attemptRec.ID, attemptRec.Data, nextData); err != nil {
				return err
			}
			attemptRec.Data = nextData
			attemptPayload = nextPayload
		default:
			return exec.ErrDispatchAdmissionConflict
		}

		if err := s.createAdmissionPendingRecord(ctx, attemptPayload, req.Task, taskFingerprint); err != nil {
			return err
		}
		return s.markAdmissionAttemptBound(ctx, attemptRec, attemptPayload)
	})
}

func (s *DispatchTaskStore) ReleaseAdmissionToken(ctx context.Context, reservationToken string) error {
	if err := s.validateAdmissionConfigured(); err != nil {
		return err
	}
	attemptRec, attemptPayload, err := s.loadAdmissionAttemptByToken(ctx, reservationToken)
	if err != nil {
		return err
	}
	if attemptPayload.State != dispatchAdmissionReserved {
		evidence, err := s.dispatchAdmissionHasExecutionEvidence(ctx, attemptPayload, s.reservationTTL)
		if err != nil {
			return err
		}
		if evidence {
			return exec.ErrDispatchAdmissionConflict
		}
	}
	s.deleteAdmissionReservationRecords(ctx, attemptPayload, attemptRec.ID)
	return nil
}

func (s *DispatchTaskStore) FinalizeAdmissionAttempt(ctx context.Context, attemptKey string) error {
	if attemptKey == "" {
		return nil
	}
	attemptID := dispatchAdmissionAttemptRecordID(attemptKey)
	rec, err := s.col.Get(ctx, attemptID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil
		}
		return err
	}
	payload, err := dispatchAdmissionAttemptPayloadFromRecord(rec)
	if err != nil {
		return err
	}
	s.deleteAdmissionReservationRecords(ctx, payload, attemptID)
	return nil
}

func (s *DispatchTaskStore) CleanupAdmissions(ctx context.Context, staleThreshold time.Duration) error {
	if err := s.validateAdmissionConfigured(); err != nil {
		return err
	}
	staleThreshold = normalizeDispatchReservationTTL(staleThreshold)
	now := time.Now().UTC()

	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: dispatchAdmissionAttemptsPrefix})
	if err != nil {
		return err
	}
	for _, rec := range recs {
		payload, err := dispatchAdmissionAttemptPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		updatedAt := dispatchRecordTimestamp(payload.UpdatedAt, rec.UpdatedAt)
		if now.Sub(updatedAt) < staleThreshold {
			continue
		}
		evidence, err := s.dispatchAdmissionHasExecutionEvidence(ctx, payload, staleThreshold)
		if err != nil {
			return err
		}
		if evidence {
			continue
		}
		s.deleteAdmissionReservationRecords(ctx, payload, rec.ID)
	}
	return s.cleanupOrphanAdmissionSlots(ctx, staleThreshold, now)
}

func (s *DispatchTaskStore) validateAdmissionRequest(req exec.DispatchAdmissionRequest) error {
	if err := s.validateAdmissionConfigured(); err != nil {
		return err
	}
	if strings.TrimSpace(req.QueueName) == "" {
		return fmt.Errorf("queue name is required")
	}
	if req.MaxConcurrency <= 0 {
		return fmt.Errorf("max concurrency must be positive")
	}
	if req.NonAdmissionOccupancy < 0 {
		return fmt.Errorf("non-admission occupancy must be non-negative")
	}
	if strings.TrimSpace(req.AttemptKey) == "" {
		return fmt.Errorf("attempt key is required")
	}
	if strings.TrimSpace(req.AttemptID) == "" {
		return fmt.Errorf("attempt ID is required")
	}
	if req.DAGRun.Zero() {
		return fmt.Errorf("DAG run is required")
	}
	return nil
}

func (s *DispatchTaskStore) validateAdmissionConfigured() error {
	if s.admissionLeaseStore == nil || s.admissionActiveRunStore == nil {
		return exec.ErrDispatchAdmissionLivenessNotConfigured
	}
	return nil
}

func (s *DispatchTaskStore) dispatchAdmissionOccupancy(
	ctx context.Context,
	queueName string,
	nonAdmissionOccupancy int,
	staleThreshold time.Duration,
) (int, error) {
	slots, err := s.listDispatchAdmissionSlots(ctx, queueName)
	if err != nil {
		return 0, err
	}
	legacy, err := s.dispatchLegacyAdmissionOccupancy(ctx, queueName, staleThreshold)
	if err != nil {
		return 0, err
	}
	return nonAdmissionOccupancy + len(slots) + legacy, nil
}

func (s *DispatchTaskStore) dispatchLegacyAdmissionOccupancy(
	ctx context.Context,
	queueName string,
	staleThreshold time.Duration,
) (int, error) {
	seen := make(map[string]struct{})
	if err := s.addLegacyDispatchRecords(ctx, seen, queueName, dispatchPendingPrefix, staleThreshold); err != nil {
		return 0, err
	}
	if err := s.addLegacyDispatchRecords(ctx, seen, queueName, dispatchClaimsPrefix, staleThreshold); err != nil {
		return 0, err
	}

	leases, err := s.admissionLeaseStore.ListByQueue(ctx, queueName)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, lease := range leases {
		if lease.AttemptKey == "" || !lease.IsFresh(now, staleThreshold) {
			continue
		}
		if s.admissionAttemptExists(ctx, lease.AttemptKey) {
			continue
		}
		seen[dispatchLegacyOccupancyKey("lease", lease.AttemptKey)] = struct{}{}
	}
	return len(seen), nil
}

func (s *DispatchTaskStore) addLegacyDispatchRecords(
	ctx context.Context,
	seen map[string]struct{},
	queueName string,
	prefix string,
	staleThreshold time.Duration,
) error {
	recs, err := s.listDispatchRecords(ctx, prefix)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, rec := range recs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return err
		}
		if payload.Task == nil || payload.Task.QueueName != queueName {
			continue
		}
		if payload.AdmissionReservationToken != "" {
			continue
		}
		recordAt := dispatchRecordTimestamp(payload.EnqueuedAt, rec.CreatedAt)
		if prefix == dispatchClaimsPrefix {
			recordAt = dispatchRecordTimestamp(payload.ClaimedAt, rec.UpdatedAt)
		}
		if now.Sub(recordAt) >= staleThreshold {
			continue
		}
		key := payload.Task.AttemptKey
		if key == "" {
			key = rec.ID
		}
		seen[dispatchLegacyOccupancyKey("dispatch", key)] = struct{}{}
	}
	return nil
}

func (s *DispatchTaskStore) admissionAttemptExists(ctx context.Context, attemptKey string) bool {
	if attemptKey == "" {
		return false
	}
	_, err := s.col.Get(ctx, dispatchAdmissionAttemptRecordID(attemptKey))
	return err == nil
}

func (s *DispatchTaskStore) loadAdmissionAttemptByToken(
	ctx context.Context,
	token string,
) (*persis.Record, dispatchAdmissionAttemptPayload, error) {
	tokenRec, err := s.col.Get(ctx, dispatchAdmissionTokenRecordID(token))
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, dispatchAdmissionAttemptPayload{}, exec.ErrDispatchAdmissionNotFound
		}
		return nil, dispatchAdmissionAttemptPayload{}, err
	}
	var tokenPayload dispatchAdmissionTokenPayload
	if err := persis.Decode(tokenRec, &tokenPayload); err != nil {
		return nil, dispatchAdmissionAttemptPayload{}, fmt.Errorf("dispatch admission token: decode %q: %w", tokenRec.ID, err)
	}
	if tokenPayload.ReservationToken != token || tokenPayload.AttemptRecordID == "" {
		return nil, dispatchAdmissionAttemptPayload{}, exec.ErrDispatchAdmissionConflict
	}

	attemptRec, err := s.col.Get(ctx, tokenPayload.AttemptRecordID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, dispatchAdmissionAttemptPayload{}, exec.ErrDispatchAdmissionNotFound
		}
		return nil, dispatchAdmissionAttemptPayload{}, err
	}
	attemptPayload, err := dispatchAdmissionAttemptPayloadFromRecord(attemptRec)
	if err != nil {
		return nil, dispatchAdmissionAttemptPayload{}, err
	}
	return attemptRec, attemptPayload, nil
}

func (s *DispatchTaskStore) verifyAdmissionSlot(ctx context.Context, attempt dispatchAdmissionAttemptPayload) error {
	if attempt.SlotID == "" {
		return exec.ErrDispatchAdmissionConflict
	}
	slotRec, err := s.col.Get(ctx, attempt.SlotID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return exec.ErrDispatchAdmissionNotFound
		}
		return err
	}
	var slot dispatchAdmissionSlotPayload
	if err := persis.Decode(slotRec, &slot); err != nil {
		return fmt.Errorf("dispatch admission slot: decode %q: %w", attempt.SlotID, err)
	}
	if slot.QueueName != attempt.QueueName ||
		slot.AttemptKey != attempt.AttemptKey ||
		slot.ReservationToken != attempt.ReservationToken {
		return exec.ErrDispatchAdmissionConflict
	}
	return nil
}

func (s *DispatchTaskStore) createAdmissionPendingRecord(
	ctx context.Context,
	attempt dispatchAdmissionAttemptPayload,
	task *exec.DispatchTask,
	taskFingerprint string,
) error {
	now := time.Now().UTC()
	payload := dispatchTaskPayload{
		Version:                   dispatchTaskStoreVersion,
		Task:                      cloneDispatchTask(task),
		TaskFileName:              attempt.PendingFileName,
		EnqueuedAt:                now.UnixMilli(),
		AdmissionReservationToken: attempt.ReservationToken,
	}
	rec, err := s.newDispatchRecord(attempt.PendingRecordID, payload, now, now)
	if err != nil {
		return err
	}
	if err := s.col.Create(ctx, rec); err != nil {
		if !errors.Is(err, persis.ErrConflict) {
			return err
		}
		existing, getErr := s.col.Get(ctx, attempt.PendingRecordID)
		if getErr != nil {
			if errors.Is(getErr, persis.ErrNotFound) {
				return persis.ErrConflict
			}
			return getErr
		}
		existingPayload, err := dispatchTaskPayloadFromRecord(existing)
		if err != nil {
			return err
		}
		if existingPayload.AdmissionReservationToken != attempt.ReservationToken {
			return exec.ErrDispatchAdmissionConflict
		}
		existingFingerprint, err := dispatchAdmissionTaskFingerprint(existingPayload.Task)
		if err != nil {
			return err
		}
		if existingFingerprint != taskFingerprint {
			return exec.ErrDispatchAdmissionConflict
		}
		return nil
	}
	if s.index != nil {
		s.index.addPending(rec, payload)
	}
	return nil
}

func (s *DispatchTaskStore) markAdmissionAttemptBound(
	ctx context.Context,
	rec *persis.Record,
	payload dispatchAdmissionAttemptPayload,
) error {
	if payload.State == dispatchAdmissionBound {
		return nil
	}
	now := time.Now().UTC()
	nextPayload := payload
	nextPayload.State = dispatchAdmissionBound
	nextPayload.BoundAt = now.UnixMilli()
	nextPayload.UpdatedAt = now.UnixMilli()
	nextData, err := persis.Encode(nextPayload)
	if err != nil {
		return err
	}
	if err := s.col.CompareAndSwap(ctx, rec.ID, rec.Data, nextData); err != nil {
		return err
	}
	rec.Data = nextData
	return nil
}

func (s *DispatchTaskStore) dispatchAdmissionHasExecutionEvidence(
	ctx context.Context,
	attempt dispatchAdmissionAttemptPayload,
	staleThreshold time.Duration,
) (bool, error) {
	if attempt.PendingRecordID != "" {
		if _, err := s.col.Get(ctx, attempt.PendingRecordID); err == nil {
			return true, nil
		} else if !errors.Is(err, persis.ErrNotFound) {
			return false, err
		}
	}

	hasClaim, err := s.dispatchAdmissionHasClaim(ctx, attempt)
	if err != nil || hasClaim {
		return hasClaim, err
	}

	lease, err := s.admissionLeaseStore.Get(ctx, attempt.AttemptKey)
	switch {
	case err == nil:
		if lease.IsFresh(time.Now().UTC(), staleThreshold) {
			return true, nil
		}
	case errors.Is(err, exec.ErrDAGRunLeaseNotFound):
	default:
		return false, err
	}

	_, err = s.admissionActiveRunStore.Get(ctx, attempt.AttemptKey)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, exec.ErrActiveRunNotFound):
		return false, nil
	default:
		return false, err
	}
}

func (s *DispatchTaskStore) dispatchAdmissionHasClaim(
	ctx context.Context,
	attempt dispatchAdmissionAttemptPayload,
) (bool, error) {
	recs, err := s.listDispatchRecords(ctx, dispatchClaimsPrefix)
	if err != nil {
		return false, err
	}
	for _, rec := range recs {
		payload, err := dispatchTaskPayloadFromRecord(rec)
		if err != nil {
			return false, err
		}
		if payload.AdmissionReservationToken == attempt.ReservationToken {
			return true, nil
		}
		if payload.Task != nil && payload.Task.AttemptKey != "" && payload.Task.AttemptKey == attempt.AttemptKey {
			return true, nil
		}
	}
	return false, nil
}

func (s *DispatchTaskStore) listDispatchAdmissionSlots(
	ctx context.Context,
	queueName string,
) (map[string]dispatchAdmissionSlotPayload, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: dispatchAdmissionSlotQueuePrefix(queueName)})
	if err != nil {
		return nil, err
	}
	slots := make(map[string]dispatchAdmissionSlotPayload, len(recs))
	for _, rec := range recs {
		var payload dispatchAdmissionSlotPayload
		if err := persis.Decode(rec, &payload); err != nil {
			return nil, fmt.Errorf("dispatch admission slot: decode %q: %w", rec.ID, err)
		}
		if payload.QueueName != queueName || payload.ReservationToken == "" {
			continue
		}
		slots[rec.ID] = payload
	}
	return slots, nil
}

func (s *DispatchTaskStore) cleanupOrphanAdmissionSlots(
	ctx context.Context,
	staleThreshold time.Duration,
	now time.Time,
) error {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{Prefix: dispatchAdmissionSlotsPrefix})
	if err != nil {
		return err
	}
	for _, rec := range recs {
		var slot dispatchAdmissionSlotPayload
		if err := persis.Decode(rec, &slot); err != nil {
			return fmt.Errorf("dispatch admission slot: decode %q: %w", rec.ID, err)
		}
		createdAt := dispatchRecordTimestamp(slot.CreatedAt, rec.CreatedAt)
		if now.Sub(createdAt) < staleThreshold {
			continue
		}
		attemptRec, err := s.col.Get(ctx, dispatchAdmissionAttemptRecordID(slot.AttemptKey))
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				_ = s.col.Delete(ctx, rec.ID)
				continue
			}
			return err
		}
		attempt, err := dispatchAdmissionAttemptPayloadFromRecord(attemptRec)
		if err != nil {
			return err
		}
		if attempt.SlotID == rec.ID && attempt.ReservationToken == slot.ReservationToken {
			continue
		}
		_ = s.col.Delete(ctx, rec.ID)
	}
	return nil
}

func (s *DispatchTaskStore) deleteAdmissionReservationRecords(
	ctx context.Context,
	attempt dispatchAdmissionAttemptPayload,
	attemptRecordID string,
) {
	if attempt.SlotID != "" {
		_ = s.col.Delete(ctx, attempt.SlotID)
	}
	if attempt.ReservationToken != "" {
		_ = s.col.Delete(ctx, dispatchAdmissionTokenRecordID(attempt.ReservationToken))
	}
	if attemptRecordID != "" {
		_ = s.col.Delete(ctx, attemptRecordID)
	}
}

func dispatchAdmissionAttemptPayloadFromRecord(rec *persis.Record) (dispatchAdmissionAttemptPayload, error) {
	var payload dispatchAdmissionAttemptPayload
	if err := persis.Decode(rec, &payload); err != nil {
		return dispatchAdmissionAttemptPayload{}, fmt.Errorf("dispatch admission attempt: decode %q: %w", rec.ID, err)
	}
	return payload, nil
}

func newDispatchAdmissionRecord(id string, payload any, createdAt, updatedAt time.Time) (*persis.Record, error) {
	data, err := persis.Encode(payload)
	if err != nil {
		return nil, err
	}
	return &persis.Record{
		ID:        id,
		Data:      data,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func dispatchAdmissionAttemptRecordID(attemptKey string) string {
	return dispatchAdmissionAttemptsPrefix + distributedRecordKey(attemptKey)
}

func dispatchAdmissionSlotQueuePrefix(queueName string) string {
	return dispatchAdmissionSlotsPrefix + distributedRecordKey(queueName) + "/"
}

func dispatchAdmissionSlotRecordID(queueName string, slotNo int) string {
	return dispatchAdmissionSlotQueuePrefix(queueName) + fmt.Sprintf("slot_%06d", slotNo)
}

func dispatchAdmissionTokenRecordID(token string) string {
	return dispatchAdmissionTokensPrefix + distributedRecordKey(token)
}

func deterministicAdmissionPendingFileName(queueName, attemptKey, token string, createdAt time.Time) string {
	input := strings.Join([]string{queueName, attemptKey, token}, "\x00")
	return fmt.Sprintf("task_%020d_%s.json", createdAt.UnixMilli(), distributedRecordKey(input))
}

func dispatchLegacyOccupancyKey(kind, key string) string {
	return kind + "\x00" + key
}

func dispatchAdmissionTaskFingerprint(task *exec.DispatchTask) (string, error) {
	if task == nil {
		return "", fmt.Errorf("dispatch task is required")
	}
	stable := cloneDispatchTask(task)
	stable.Owner = exec.CoordinatorEndpoint{}
	stable.ClaimToken = ""
	data, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	return distributedRecordKey(string(data)), nil
}

func dispatchAdmissionAttemptExpired(
	rec *persis.Record,
	payload dispatchAdmissionAttemptPayload,
	ttl time.Duration,
) bool {
	updatedAt := dispatchRecordTimestamp(payload.UpdatedAt, rec.UpdatedAt)
	return time.Since(updatedAt) >= normalizeDispatchReservationTTL(ttl)
}
