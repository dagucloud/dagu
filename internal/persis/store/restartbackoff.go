// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const restartBackoffID = "restart_backoff"

type restartBackoffRecord struct {
	DAGs map[string]schedulerstate.RestartBackoff `json:"dags,omitempty"`
}

// RestartBackoffStore keeps the restart backoff as a record in the scheduler
// state collection.
type RestartBackoffStore struct {
	rec *SingleRecord[restartBackoffRecord]
}

var _ schedulerstate.RestartBackoffStore = (*RestartBackoffStore)(nil)

// NewRestartBackoffStore creates a restart backoff store backed by col.
func NewRestartBackoffStore(col persis.Collection) *RestartBackoffStore {
	return &RestartBackoffStore{rec: NewSingleRecord[restartBackoffRecord](col, restartBackoffID)}
}

// Load returns the backoff that the scheduler last saved, which is empty before
// the first save.
func (s *RestartBackoffStore) Load(ctx context.Context) (map[string]schedulerstate.RestartBackoff, error) {
	var record restartBackoffRecord
	if _, err := s.rec.Load(ctx, &record); err != nil {
		return nil, fmt.Errorf("restart backoff store: load: %w", err)
	}
	return record.DAGs, nil
}

// Save replaces the backoff.
func (s *RestartBackoffStore) Save(ctx context.Context, backoff map[string]schedulerstate.RestartBackoff) error {
	if err := s.rec.Save(ctx, &restartBackoffRecord{DAGs: backoff}); err != nil {
		return fmt.Errorf("restart backoff store: save: %w", err)
	}
	return nil
}
