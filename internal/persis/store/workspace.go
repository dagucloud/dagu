// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/workspace"
)

var _ workspace.Store = (*WorkspaceStore)(nil)

// WorkspaceStore implements [workspace.Store] over [persis.Collection],
// keeping an in-memory name→id index rebuilt on startup.
type WorkspaceStore struct {
	col persis.Collection

	mu     sync.RWMutex
	byName map[string]string
}

// NewWorkspaceStore creates a WorkspaceStore backed by col.
func NewWorkspaceStore(col persis.Collection) (*WorkspaceStore, error) {
	s := &WorkspaceStore{
		col:    col,
		byName: make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("workspace store: build index: %w", err)
	}
	return s, nil
}

func (s *WorkspaceStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var stored workspace.WorkspaceForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		s.byName[stored.Name] = stored.ID
	}
	return nil
}

// Create stores a new workspace.
func (s *WorkspaceStore) Create(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace store: workspace cannot be nil")
	}
	if ws.ID == "" {
		return workspace.ErrInvalidWorkspaceID
	}
	if err := workspace.ValidateName(ws.Name); err != nil {
		return err
	}

	data, err := persis.Encode(ws.ToStorage())
	if err != nil {
		return fmt.Errorf("workspace store: encode: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[ws.Name]; exists {
		return workspace.ErrWorkspaceAlreadyExists
	}
	if _, err := s.col.Get(ctx, ws.ID); err == nil {
		return workspace.ErrWorkspaceAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        ws.ID,
		Data:      data,
		CreatedAt: ws.CreatedAt,
		UpdatedAt: ws.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("workspace store: create: %w", err)
	}
	s.byName[ws.Name] = ws.ID
	return nil
}

// GetByID returns the workspace with the given ID.
func (s *WorkspaceStore) GetByID(ctx context.Context, id string) (*workspace.Workspace, error) {
	if id == "" {
		return nil, workspace.ErrInvalidWorkspaceID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, workspace.ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("workspace store: get: %w", err)
	}
	return workspaceFromRecord(rec)
}

// GetByName returns the workspace with the given name.
func (s *WorkspaceStore) GetByName(ctx context.Context, name string) (*workspace.Workspace, error) {
	if err := workspace.ValidateName(name); err != nil {
		return nil, err
	}
	s.mu.RLock()
	wsID, exists := s.byName[name]
	s.mu.RUnlock()
	if !exists {
		return nil, workspace.ErrWorkspaceNotFound
	}
	return s.GetByID(ctx, wsID)
}

// List returns all workspaces.
func (s *WorkspaceStore) List(ctx context.Context) ([]*workspace.Workspace, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*workspace.Workspace, 0, len(recs))
	for _, rec := range recs {
		ws, err := workspaceFromRecord(rec)
		if err != nil {
			continue
		}
		out = append(out, ws)
	}
	return out, nil
}

// Update modifies an existing workspace.
func (s *WorkspaceStore) Update(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("workspace store: workspace cannot be nil")
	}
	if ws.ID == "" {
		return workspace.ErrInvalidWorkspaceID
	}
	if err := workspace.ValidateName(ws.Name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingRec, err := s.col.Get(ctx, ws.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return workspace.ErrWorkspaceNotFound
		}
		return fmt.Errorf("workspace store: get for update: %w", err)
	}
	var existingStored workspace.WorkspaceForStorage
	if err := persis.Decode(existingRec, &existingStored); err != nil {
		return fmt.Errorf("workspace store: decode existing: %w", err)
	}

	if existingStored.Name != ws.Name {
		if id, taken := s.byName[ws.Name]; taken && id != ws.ID {
			return workspace.ErrWorkspaceAlreadyExists
		}
	}

	data, err := persis.Encode(ws.ToStorage())
	if err != nil {
		return fmt.Errorf("workspace store: encode: %w", err)
	}
	if err := s.col.Put(ctx, &persis.Record{
		ID:        ws.ID,
		Data:      data,
		CreatedAt: existingRec.CreatedAt,
		UpdatedAt: ws.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("workspace store: update: %w", err)
	}
	if existingStored.Name != ws.Name {
		delete(s.byName, existingStored.Name)
		s.byName[ws.Name] = ws.ID
	}
	return nil
}

// Delete removes the workspace with the given ID.
func (s *WorkspaceStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return workspace.ErrInvalidWorkspaceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return workspace.ErrWorkspaceNotFound
		}
		return fmt.Errorf("workspace store: get for delete: %w", err)
	}
	var stored workspace.WorkspaceForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("workspace store: decode for delete: %w", err)
	}
	if err := s.col.Delete(ctx, id); err != nil {
		return fmt.Errorf("workspace store: delete: %w", err)
	}
	delete(s.byName, stored.Name)
	return nil
}

func workspaceFromRecord(rec *persis.Record) (*workspace.Workspace, error) {
	var stored workspace.WorkspaceForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("workspace store: decode record %q: %w", rec.ID, err)
	}
	return stored.ToWorkspace(), nil
}
