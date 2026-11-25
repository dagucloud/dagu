// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// History holds the operator's pauses that ended, the maxPeriods that
	// ended last within periodRetention, in the order they ended.
	History []schedulerstate.Period `json:"history,omitempty"`
}

// SchedulerPauseStore persists the cluster-wide scheduler pause flag as a
// record in the scheduler process collection.
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

	record, err := s.recordLocked(ctx)
	if err != nil {
		return schedulerstate.Pause{}, err
	}
	return pauseFromRecord(record), nil
}

// endedPauses returns the operator's pauses that ended, as the record last
// read holds them while it is current.
func (s *SchedulerPauseStore) endedPauses(ctx context.Context) ([]schedulerstate.Period, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.recordLocked(ctx)
	return slices.Clone(record.History), err
}

// recordLocked returns the record, from the cache while it is current. A
// missing or undecodable record reads as empty.
func (s *SchedulerPauseStore) recordLocked(ctx context.Context) (schedulerPauseRecord, error) {
	if cached, ok := s.cachedLocked(ctx); ok {
		return cached, nil
	}

	// The version is read before the record, so a write between the two
	// leaves the cache behind the record and the next read reloads it.
	token, versioned, tokenErr := s.recordToken(ctx)
	var record schedulerPauseRecord
	found, err := s.rec.Load(ctx, &record)
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			logger.Warn(ctx, "scheduler pause: corrupt record, treating as not paused", tag.Error(err))
			return schedulerPauseRecord{}, nil
		}
		return schedulerPauseRecord{}, fmt.Errorf("scheduler pause store: get: %w", err)
	}
	if !found {
		record = schedulerPauseRecord{}
	}
	if !versioned || tokenErr != nil {
		s.clearCacheLocked()
		return record, nil
	}
	s.cached, s.cachedValid, s.cachedToken = record, true, token
	return record, nil
}

// Set records the pause state. A pause in force keeps the time it began.
// Resuming clears the recorded actor and reason and, in the same write, adds
// the operator's pause it ends to the history; a wake hold it ends adds
// nothing.
func (s *SchedulerPauseStore) Set(ctx context.Context, paused bool, actor, reason string) error {
	// The write retries on conflicts without the lock reads take, so no
	// pause check waits for it.
	err := retryConflict(ctx, func(ctx context.Context) error {
		current, err := s.col.Get(ctx, schedulerPauseID)
		if err != nil && !errors.Is(err, persis.ErrNotFound) {
			return err
		}
		var record schedulerPauseRecord
		if current != nil && persis.Decode(current, &record) != nil {
			// A corrupt record reads as not paused (Get), so this write replaces it.
			record = schedulerPauseRecord{}
		}
		now := time.Now().UTC()
		operator := record.Paused && !record.Hold && !record.PausedAt.IsZero()
		next := schedulerPauseRecord{Paused: paused, History: record.History}
		switch {
		case paused:
			next.PausedAt, next.PausedBy, next.Reason = now, actor, reason
			if operator {
				next.PausedAt = record.PausedAt
			}
		case operator:
			next.History, _ = appendPeriod(record.History, schedulerstate.Period{From: record.PausedAt, To: now}, now)
		}
		data, err := persis.Encode(&next)
		if err != nil {
			return err
		}
		return createOrSwap(ctx, s.col, current, &persis.Record{ID: schedulerPauseID, Data: data, CreatedAt: now, UpdatedAt: now})
	})
	// Another process may write right after this write, so the next read
	// loads the record, reading its version first.
	s.mu.Lock()
	s.clearCacheLocked()
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("scheduler pause store: save: %w", err)
	}
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
		next = schedulerPauseRecord{Paused: true, Hold: true, PausedAt: time.Now().UTC(), PausedBy: schedulerstate.PausedByScheduler, Reason: reason, History: record.History}
	case !record.Paused || !record.Hold:
		// Nothing of the scheduler's to lift.
		return false, nil
	default:
		next = schedulerPauseRecord{History: record.History}
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
