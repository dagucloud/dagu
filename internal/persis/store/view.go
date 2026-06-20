// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/view"
)

var _ view.Store = (*ViewStore)(nil)

// ViewStore implements [view.Store] over a [persis.Collection]. Views are
// global and shared: each record is keyed by its view ID with no per-user
// scoping. Views have no secondary-key lookups, so no in-memory index is
// maintained; writes are serialized under mu.
type ViewStore struct {
	col persis.Collection
	mu  sync.Mutex
}

// NewViewStore creates a ViewStore backed by col.
func NewViewStore(col persis.Collection) (*ViewStore, error) {
	return &ViewStore{col: col}, nil
}

// Create stores a new view. Returns [view.ErrViewExists] if a view with the
// same ID already exists.
func (s *ViewStore) Create(ctx context.Context, v *view.View) error {
	if v == nil || v.ID == "" {
		return view.ErrInvalidViewID
	}
	data, err := persis.Encode(v.ToStorage())
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch _, err := s.col.Get(ctx, v.ID); {
	case err == nil:
		return view.ErrViewExists
	case errors.Is(err, persis.ErrNotFound):
		// proceed
	default:
		return fmt.Errorf("view store: precheck: %w", err)
	}

	return s.col.Put(ctx, &persis.Record{
		ID:        v.ID,
		Data:      data,
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	})
}

// GetByID retrieves a view by ID. Returns [view.ErrViewNotFound] if absent.
func (s *ViewStore) GetByID(ctx context.Context, id string) (*view.View, error) {
	if id == "" {
		return nil, view.ErrInvalidViewID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, view.ErrViewNotFound
		}
		return nil, err
	}
	return viewFromRecord(rec)
}

// List returns all views ordered by creation time ascending, tiebroken by ID.
func (s *ViewStore) List(ctx context.Context) ([]*view.View, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*view.View, 0, len(recs))
	for _, rec := range recs {
		v, err := viewFromRecord(rec)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Update replaces an existing view, preserving its original CreatedAt and
// stamping UpdatedAt with the current time. Returns [view.ErrViewNotFound]
// if the view does not exist.
func (s *ViewStore) Update(ctx context.Context, v *view.View) error {
	if v == nil || v.ID == "" {
		return view.ErrInvalidViewID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.col.Get(ctx, v.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return view.ErrViewNotFound
		}
		return err
	}

	// Preserve the original creation time regardless of the caller's input.
	v.CreatedAt = existing.CreatedAt
	v.UpdatedAt = time.Now().UTC()
	data, err := persis.Encode(v.ToStorage())
	if err != nil {
		return err
	}
	return s.col.Put(ctx, &persis.Record{
		ID:        v.ID,
		Data:      data,
		CreatedAt: existing.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	})
}

// Delete removes a view by ID. Returns [view.ErrViewNotFound] if absent.
func (s *ViewStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return view.ErrInvalidViewID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.col.Get(ctx, id); err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return view.ErrViewNotFound
		}
		return err
	}
	return s.col.Delete(ctx, id)
}

func viewFromRecord(rec *persis.Record) (*view.View, error) {
	var stored view.ViewForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("view store: decode record %q: %w", rec.ID, err)
	}
	return stored.ToView(), nil
}
