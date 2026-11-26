// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const restartBackoffID = "restart_backoff"

type restartBackoffRecord struct {
	// DAGs is the default project's backoff, where v2 keeps it.
	DAGs map[string]schedulerstate.RestartBackoff `json:"dags,omitempty"`
	// Projects is the backoff of every other project, by project ID.
	Projects map[string]map[string]schedulerstate.RestartBackoff `json:"projects,omitempty"`
}

// RestartBackoffStore keeps the restart backoff as a record in the scheduler
// process collection. As a schedulerstate.RestartBackoffStore it holds the
// default project's backoff; ForProject gives another project's.
type RestartBackoffStore struct {
	rec *SingleRecord[restartBackoffRecord]
	// mu keeps one save from undoing another project's save in this
	// process. Only the active scheduler saves.
	mu sync.Mutex
}

var _ schedulerstate.RestartBackoffStore = (*RestartBackoffStore)(nil)

// NewRestartBackoffStore creates a restart backoff store backed by col.
func NewRestartBackoffStore(col persis.Collection) *RestartBackoffStore {
	return &RestartBackoffStore{rec: NewSingleRecord[restartBackoffRecord](col, restartBackoffID)}
}

// Load returns the backoff that the scheduler last saved, which is empty before
// the first save.
func (s *RestartBackoffStore) Load(ctx context.Context) (map[string]schedulerstate.RestartBackoff, error) {
	return s.load(ctx, "")
}

// Save replaces the backoff.
func (s *RestartBackoffStore) Save(ctx context.Context, backoff map[string]schedulerstate.RestartBackoff) error {
	return s.save(ctx, "", backoff)
}

// ForProject returns the store of the backoff of the project with the
// given ID, which is kept apart from every other project's. The default
// project's backoff is s itself.
func (s *RestartBackoffStore) ForProject(id string) schedulerstate.RestartBackoffStore {
	return projectRestartBackoff{store: s, id: id}
}

func (s *RestartBackoffStore) load(ctx context.Context, id string) (map[string]schedulerstate.RestartBackoff, error) {
	var record restartBackoffRecord
	if _, err := s.rec.Load(ctx, &record); err != nil {
		return nil, fmt.Errorf("restart backoff store: load: %w", err)
	}
	if id == "" {
		return record.DAGs, nil
	}
	return record.Projects[id], nil
}

func (s *RestartBackoffStore) save(ctx context.Context, id string, backoff map[string]schedulerstate.RestartBackoff) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A record that does not decode is replaced: the backoff only lengthens
	// waits, so losing it costs less than never saving again. A read that
	// fails keeps the record, which holds other projects' backoff.
	var record restartBackoffRecord
	if _, err := s.rec.Load(ctx, &record); err != nil {
		if !errors.Is(err, ErrCorrupt) {
			return fmt.Errorf("restart backoff store: save: %w", err)
		}
		record = restartBackoffRecord{}
	}
	switch {
	case id == "":
		record.DAGs = backoff
	case len(backoff) == 0:
		delete(record.Projects, id)
	default:
		if record.Projects == nil {
			record.Projects = make(map[string]map[string]schedulerstate.RestartBackoff)
		}
		record.Projects[id] = backoff
	}
	if err := s.rec.Save(ctx, &record); err != nil {
		return fmt.Errorf("restart backoff store: save: %w", err)
	}
	return nil
}

// projectRestartBackoff is the backoff of one project other than the
// default one.
type projectRestartBackoff struct {
	store *RestartBackoffStore
	id    string
}

func (p projectRestartBackoff) Load(ctx context.Context) (map[string]schedulerstate.RestartBackoff, error) {
	return p.store.load(ctx, p.id)
}

func (p projectRestartBackoff) Save(ctx context.Context, backoff map[string]schedulerstate.RestartBackoff) error {
	return p.store.save(ctx, p.id, backoff)
}
