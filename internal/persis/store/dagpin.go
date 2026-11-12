// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagpin"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ dagpin.Store = (*DAGPinStore)(nil)

// All pins live in one record, so listing DAGs reads a single small file.
const dagPinsRecordID = "pins"

// DAGPinStore persists the pinned DAG set in a collection.
type DAGPinStore struct {
	col persis.Collection
}

type dagPinsStoredRecord struct {
	FileNames []string `json:"fileNames"`
}

// NewDAGPinStore returns a pin store backed by col.
func NewDAGPinStore(col persis.Collection) (*DAGPinStore, error) {
	if col == nil {
		return nil, errors.New("DAG pin store: collection cannot be nil")
	}
	return &DAGPinStore{col: col}, nil
}

// List returns the pinned IDs.
func (s *DAGPinStore) List(ctx context.Context) (map[string]struct{}, error) {
	_, pins, err := s.load(ctx)
	return pins, err
}

// Pin adds id to the pinned set.
func (s *DAGPinStore) Pin(ctx context.Context, id string) error {
	if err := validateDAGPinID(id); err != nil {
		return err
	}
	return s.update(ctx, func(pins map[string]struct{}) bool {
		if _, ok := pins[id]; ok {
			return false
		}
		pins[id] = struct{}{}
		return true
	})
}

// Unpin removes id from the pinned set.
func (s *DAGPinStore) Unpin(ctx context.Context, id string) error {
	if err := validateDAGPinID(id); err != nil {
		return err
	}
	return s.update(ctx, func(pins map[string]struct{}) bool {
		if _, ok := pins[id]; !ok {
			return false
		}
		delete(pins, id)
		return true
	})
}

// Rename moves the pin from oldID to newID when oldID is pinned.
func (s *DAGPinStore) Rename(ctx context.Context, oldID, newID string) error {
	if err := validateDAGPinID(oldID); err != nil {
		return err
	}
	if err := validateDAGPinID(newID); err != nil {
		return err
	}
	return s.update(ctx, func(pins map[string]struct{}) bool {
		if _, ok := pins[oldID]; !ok || oldID == newID {
			return false
		}
		delete(pins, oldID)
		pins[newID] = struct{}{}
		return true
	})
}

func (s *DAGPinStore) load(ctx context.Context) (*persis.Record, map[string]struct{}, error) {
	rec, err := s.col.Get(ctx, dagPinsRecordID)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var stored dagPinsStoredRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, nil, fmt.Errorf("DAG pin store: decode: %w: %w", persis.ErrCorrupt, err)
	}
	pins := make(map[string]struct{}, len(stored.FileNames))
	for _, id := range stored.FileNames {
		pins[id] = struct{}{}
	}
	return rec, pins, nil
}

// update applies mutate to the current set and stores the result unless
// mutate reports no change. Concurrent updates are retried on conflict, and a
// corrupt record is never overwritten.
func (s *DAGPinStore) update(ctx context.Context, mutate func(map[string]struct{}) bool) error {
	return retryConflict(ctx, func(ctx context.Context) error {
		current, pins, err := s.load(ctx)
		if err != nil {
			return err
		}
		if !mutate(pins) {
			return nil
		}
		// Sorted so the stored bytes do not depend on map iteration order.
		fileNames := make([]string, 0, len(pins))
		for id := range pins {
			fileNames = append(fileNames, id)
		}
		slices.Sort(fileNames)
		data, err := persis.Encode(&dagPinsStoredRecord{FileNames: fileNames})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		next := &persis.Record{ID: dagPinsRecordID, Data: data, CreatedAt: now, UpdatedAt: now}
		return createOrSwap(ctx, s.col, current, next)
	})
}

func validateDAGPinID(id string) error {
	if id == "" {
		return errors.New("DAG pin store: DAG ID must not be empty")
	}
	if err := ir.ValidateDAGName(id); err != nil {
		return fmt.Errorf("DAG pin store: invalid DAG ID %q: %w", id, err)
	}
	return nil
}
