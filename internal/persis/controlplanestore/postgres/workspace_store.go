// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
	"github.com/dagucloud/dagu/internal/workspace"
)

var _ workspace.Store = (*workspaceStore)(nil)

type workspaceStore struct{ store *Store }

// Workspaces returns the workspace store.
func (s *Store) Workspaces() workspace.Store {
	return &workspaceStore{store: s}
}

func (s *workspaceStore) Create(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("postgres workspace store: workspace cannot be nil")
	}
	if err := workspace.ValidateName(ws.Name); err != nil {
		return err
	}
	idString, id, err := ensureUUIDv7String(ws.ID)
	if err != nil {
		return workspace.ErrInvalidWorkspaceID
	}
	ws.ID = idString
	data, err := json.Marshal(ws.ToStorage())
	if err != nil {
		return fmt.Errorf("marshal workspace: %w", err)
	}
	err = s.store.queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		ID:        id,
		Name:      ws.Name,
		Data:      data,
		CreatedAt: timestamptz(ws.CreatedAt),
		UpdatedAt: timestamptz(ws.UpdatedAt),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return workspace.ErrWorkspaceAlreadyExists
		}
		return err
	}
	return nil
}

func (s *workspaceStore) GetByID(ctx context.Context, id string) (*workspace.Workspace, error) {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return nil, workspace.ErrInvalidWorkspaceID
	}
	row, err := s.store.queries.GetWorkspaceByID(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workspace.ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return workspaceFromRow(row)
}

func (s *workspaceStore) GetByName(ctx context.Context, name string) (*workspace.Workspace, error) {
	if err := workspace.ValidateName(name); err != nil {
		return nil, err
	}
	row, err := s.store.queries.GetWorkspaceByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workspace.ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return workspaceFromRow(row)
}

func (s *workspaceStore) List(ctx context.Context) ([]*workspace.Workspace, error) {
	rows, err := s.store.queries.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	workspaces := make([]*workspace.Workspace, 0, len(rows))
	for _, row := range rows {
		ws, err := workspaceFromRow(row)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, ws)
	}
	return workspaces, nil
}

func (s *workspaceStore) Update(ctx context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("postgres workspace store: workspace cannot be nil")
	}
	id, err := parseUUIDv7(ws.ID)
	if err != nil {
		return workspace.ErrInvalidWorkspaceID
	}
	if err := workspace.ValidateName(ws.Name); err != nil {
		return err
	}
	data, err := json.Marshal(ws.ToStorage())
	if err != nil {
		return fmt.Errorf("marshal workspace: %w", err)
	}
	rows, err := s.store.queries.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{
		ID:        id,
		Name:      ws.Name,
		Data:      data,
		UpdatedAt: timestamptz(ws.UpdatedAt),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return workspace.ErrWorkspaceAlreadyExists
		}
		return err
	}
	if rows == 0 {
		return workspace.ErrWorkspaceNotFound
	}
	return nil
}

func (s *workspaceStore) Delete(ctx context.Context, id string) error {
	uid, err := parseUUIDv7(id)
	if err != nil {
		return workspace.ErrInvalidWorkspaceID
	}
	rows, err := s.store.queries.DeleteWorkspace(ctx, uid)
	if err != nil {
		return err
	}
	if rows == 0 {
		return workspace.ErrWorkspaceNotFound
	}
	return nil
}

func workspaceFromRow(row db.DaguWorkspace) (*workspace.Workspace, error) {
	var stored workspace.WorkspaceForStorage
	if err := json.Unmarshal(row.Data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshal workspace: %w", err)
	}
	ws := stored.ToWorkspace()
	ws.ID = row.ID.String()
	ws.Name = row.Name
	ws.CreatedAt = timeFromTimestamptz(row.CreatedAt)
	ws.UpdatedAt = timeFromTimestamptz(row.UpdatedAt)
	return ws, nil
}
