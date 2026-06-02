// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ dagsettings.Store = (*DAGSettingsStore)(nil)

type DAGSettingsStore struct {
	col persis.Collection
}

type dagSettingsStoredRecord struct {
	Settings *dagsettings.Settings `json:"settings"`
}

func NewDAGSettingsStore(col persis.Collection) (*DAGSettingsStore, error) {
	if col == nil {
		return nil, errors.New("DAG settings store: collection cannot be nil")
	}
	return &DAGSettingsStore{col: col}, nil
}

func (s *DAGSettingsStore) Get(ctx context.Context, dagName string) (*dagsettings.Settings, error) {
	if err := dagsettings.ValidateDAGName(dagName); err != nil {
		return nil, err
	}
	rec, err := s.col.Get(ctx, dagName)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, dagsettings.ErrNotFound
		}
		return nil, err
	}
	var stored dagSettingsStoredRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("DAG settings store: decode %q: %w", dagName, err)
	}
	if stored.Settings == nil {
		return nil, fmt.Errorf("DAG settings store: decode %q: missing settings payload", dagName)
	}
	return stored.Settings.Clone(), nil
}

func (s *DAGSettingsStore) Upsert(ctx context.Context, settings *dagsettings.Settings) error {
	if settings == nil {
		return errors.New("DAG settings store: settings cannot be nil")
	}
	if err := dagsettings.ValidateDAGName(settings.DAGName); err != nil {
		return err
	}
	stored := settings.Clone()
	data, err := persis.Encode(&dagSettingsStoredRecord{Settings: stored})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	rec := &persis.Record{
		ID:        stored.DAGName,
		Data:      data,
		CreatedAt: stored.CreatedAt,
		UpdatedAt: now,
	}
	existing, err := s.col.Get(ctx, stored.DAGName)
	if err == nil {
		rec.CreatedAt = existing.CreatedAt
		return s.col.Put(ctx, rec)
	}
	if !errors.Is(err, persis.ErrNotFound) {
		return err
	}
	return s.col.Create(ctx, rec)
}

func (s *DAGSettingsStore) Delete(ctx context.Context, dagName string) error {
	if err := dagsettings.ValidateDAGName(dagName); err != nil {
		return err
	}
	if err := s.col.Delete(ctx, dagName); err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return dagsettings.ErrNotFound
		}
		return err
	}
	return nil
}
