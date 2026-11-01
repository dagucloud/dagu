// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const schedulerPauseID = "paused"

type schedulerPauseRecord struct {
	Paused   bool      `json:"paused"`
	PausedAt time.Time `json:"pausedAt"`
	PausedBy string    `json:"pausedBy,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	// Hold marks the scheduler's own wake hold; see schedulerstate.Pause.
	Hold bool `json:"hold,omitempty"`
}

// SchedulerPauseStore persists the cluster-wide scheduler pause flag as a
// record in the scheduler state collection.
//
// The flag is written by the API and read by the scheduler on every dispatch
// decision. Reads are served from a cache validated against the backing
// record's version token, so a pause written by another process is observed
// without decoding the record on every check.
type SchedulerPauseStore struct {
	col persis.Collection
	rec *SingleRecord[schedulerPauseRecord]

	mu          sync.Mutex
	cached      schedulerPauseRecord
	cachedValid bool
	// cachedToken is the record version the cache was built from. The empty
	// string is a real value here: it marks an absent record, which is the
	// steady state while the scheduler is not paused.
	cachedToken string
}

var _ schedulerstate.PauseStore = (*SchedulerPauseStore)(nil)

// NewSchedulerPauseStore creates a scheduler pause store backed by col.
func NewSchedulerPauseStore(col persis.Collection) *SchedulerPauseStore {
	return &SchedulerPauseStore{
		col: col,
		rec: NewSingleRecord[schedulerPauseRecord](col, schedulerPauseID),
	}
}

// IsPaused reports whether scheduler-managed dispatch is currently paused.
func (s *SchedulerPauseStore) IsPaused(ctx context.Context) (bool, error) {
	pause, err := s.Get(ctx)
	if err != nil {
		return false, err
	}
	return pause.Paused, nil
}

// Get returns the full pause state. A missing or undecodable record reports
// not paused, so a damaged flag never blocks scheduling indefinitely.
func (s *SchedulerPauseStore) Get(ctx context.Context) (schedulerstate.Pause, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok := s.cachedLocked(ctx); ok {
		return pauseFromRecord(cached), nil
	}

	var record schedulerPauseRecord
	found, err := s.rec.Load(ctx, &record)
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			logger.Warn(ctx, "scheduler pause: corrupt record, treating as not paused", tag.Error(err))
			return schedulerstate.Pause{}, nil
		}
		return schedulerstate.Pause{}, fmt.Errorf("scheduler pause store: get: %w", err)
	}
	if !found {
		record = schedulerPauseRecord{}
	}
	s.cacheLocked(ctx, record)
	return pauseFromRecord(record), nil
}

// Set records the pause state. Resuming clears the recorded actor and reason.
func (s *SchedulerPauseStore) Set(ctx context.Context, paused bool, actor, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record := schedulerPauseRecord{Paused: paused}
	if paused {
		record.PausedAt = time.Now().UTC()
		record.PausedBy = actor
		record.Reason = reason
	}
	if err := s.rec.Save(ctx, &record); err != nil {
		s.clearCacheLocked()
		return fmt.Errorf("scheduler pause store: save: %w", err)
	}
	s.cacheLocked(ctx, record)
	return nil
}

// SetHold places or lifts the scheduler's wake hold as one atomic step against
// the record as it is: a hold goes in only where nothing is paused, and comes
// out only while it still stands. A record another process changed in between
// is left as that process wrote it, and the call reports no change.
func (s *SchedulerPauseStore) SetHold(ctx context.Context, hold bool, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.col.Get(ctx, schedulerPauseID)
	if err != nil && !errors.Is(err, persis.ErrNotFound) {
		return false, fmt.Errorf("scheduler pause store: get: %w", err)
	}
	var record schedulerPauseRecord
	if current != nil {
		if err := persis.Decode(current, &record); err != nil {
			// A corrupt record reads as not paused (Get), so a hold may take its place.
			record = schedulerPauseRecord{}
		}
	}
	var next schedulerPauseRecord
	switch {
	case hold && record.Paused:
		// A pause in force, an operator's or a hold already, governs.
		return false, nil
	case hold:
		next = schedulerPauseRecord{Paused: true, Hold: true, PausedAt: time.Now().UTC(), PausedBy: schedulerstate.PausedByScheduler, Reason: reason}
	case !record.Paused || !record.Hold:
		// Nothing of the scheduler's to lift.
		return false, nil
	}
	data, err := persis.Encode(&next)
	if err != nil {
		return false, fmt.Errorf("scheduler pause store: encode: %w", err)
	}
	if current == nil {
		now := time.Now().UTC()
		err = s.col.Create(ctx, &persis.Record{ID: schedulerPauseID, Data: data, CreatedAt: now, UpdatedAt: now})
	} else {
		err = s.col.CompareAndSwap(ctx, schedulerPauseID, current.Data, data)
	}
	s.clearCacheLocked()
	if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("scheduler pause store: hold: %w", err)
	}
	s.cacheLocked(ctx, next)
	return true, nil
}

func (s *SchedulerPauseStore) cachedLocked(ctx context.Context) (schedulerPauseRecord, bool) {
	if !s.cachedValid {
		return schedulerPauseRecord{}, false
	}
	token, ok, err := s.recordToken(ctx)
	if !ok || err != nil || token != s.cachedToken {
		if err != nil {
			s.clearCacheLocked()
		}
		return schedulerPauseRecord{}, false
	}
	return s.cached, true
}

func (s *SchedulerPauseStore) cacheLocked(ctx context.Context, record schedulerPauseRecord) {
	token, ok, err := s.recordToken(ctx)
	if !ok || err != nil {
		s.clearCacheLocked()
		return
	}
	s.cached = record
	s.cachedValid = true
	s.cachedToken = token
}

// recordToken returns the backing record's version token. An absent record
// yields the empty token rather than an error, so the not-paused steady state
// is cacheable and costs a single stat per check.
func (s *SchedulerPauseStore) recordToken(ctx context.Context) (string, bool, error) {
	token, ok, err := collectionRecordVersion(ctx, s.col, schedulerPauseID)
	if errors.Is(err, persis.ErrNotFound) {
		return "", ok, nil
	}
	return token, ok, err
}

func (s *SchedulerPauseStore) clearCacheLocked() {
	s.cached = schedulerPauseRecord{}
	s.cachedValid = false
	s.cachedToken = ""
}

func pauseFromRecord(record schedulerPauseRecord) schedulerstate.Pause {
	return schedulerstate.Pause{
		Paused:   record.Paused,
		PausedAt: record.PausedAt,
		PausedBy: record.PausedBy,
		Reason:   record.Reason,
		Hold:     record.Hold,
	}
}
