// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const (
	watchStateVersion = 1
	watchStateID      = "watch"
)

type watchStateRecord struct {
	SchemaVersion int                                   `json:"version"`
	DAGs          map[string]schedulerstate.WatchLedger `json:"dags,omitempty"`
}

// WatchStateStore keeps the ledgers of watched folders in one record.
type WatchStateStore struct {
	rec *SingleRecord[watchStateRecord]
}

var _ schedulerstate.WatchStore = (*WatchStateStore)(nil)

// NewWatchStateStore creates a watch state store backed by col.
func NewWatchStateStore(col persis.Collection) *WatchStateStore {
	return &WatchStateStore{rec: NewSingleRecord[watchStateRecord](col, watchStateID)}
}

// LoadWatch reads the ledgers, starting fresh when the record is missing,
// corrupt, or of a version this build does not know; a folder is then
// watched as if for the first time.
func (s *WatchStateStore) LoadWatch(ctx context.Context) (map[string]schedulerstate.WatchLedger, error) {
	var record watchStateRecord
	found, err := s.rec.Load(ctx, &record)
	if errors.Is(err, ErrCorrupt) {
		logger.Warn(ctx, "watch state: corrupt record, starting fresh", tag.Error(err))
		return map[string]schedulerstate.WatchLedger{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("watch state store: get: %w", err)
	}
	if !found || record.SchemaVersion != watchStateVersion || record.DAGs == nil {
		return map[string]schedulerstate.WatchLedger{}, nil
	}
	return record.DAGs, nil
}

// SaveWatch writes the ledgers.
func (s *WatchStateStore) SaveWatch(ctx context.Context, ledgers map[string]schedulerstate.WatchLedger) error {
	if err := s.rec.Save(ctx, &watchStateRecord{SchemaVersion: watchStateVersion, DAGs: ledgers}); err != nil {
		return fmt.Errorf("watch state store: save: %w", err)
	}
	return nil
}
