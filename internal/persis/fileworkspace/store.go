// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fileworkspace provides a file-based implementation of the workspace Store interface.
package fileworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/workspace"
)

const (
	fileExtension   = ".json"
	dirPermissions  = 0750
	filePermissions = 0600
)

// Store implements workspace.Store using the local filesystem.
// Workspaces are stored as individual JSON files.
// Thread-safe through internal locking.
type Store struct {
	baseDir string

	mu     sync.RWMutex
	byID   map[string]string // id -> file path
	byName map[string]string // name -> id
}

var _ workspace.Store = (*Store)(nil)

// New creates a file-backed Store that persists workspaces as individual JSON files in baseDir.
func New(baseDir string) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("fileworkspace: baseDir cannot be empty")
	}

	store := &Store{
		baseDir: baseDir,
		byID:    make(map[string]string),
		byName:  make(map[string]string),
	}

	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("fileworkspace: failed to create directory %s: %w", baseDir, err)
	}

	if err := store.rebuildIndex(); err != nil {
		return nil, fmt.Errorf("fileworkspace: failed to build index: %w", err)
	}

	return store, nil
}

func (s *Store) rebuildIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID = make(map[string]string)
	s.byName = make(map[string]string)

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", s.baseDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}

		filePath := filepath.Join(s.baseDir, entry.Name())
		stored, err := loadStoredFromFile(filePath)
		if err != nil {
			slog.Warn("Failed to load workspace file during index rebuild",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}

		s.byID[stored.ID] = filePath
		s.byName[stored.Name] = stored.ID
	}

	return nil
}

func loadStoredFromFile(filePath string) (*workspace.WorkspaceForStorage, error) {
	data, err := fileutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	var stored workspace.WorkspaceForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("failed to parse workspace file %s: %w", filePath, err)
	}

	return &stored, nil
}

func (s *Store) wsFilePath(id string) string {
	return filepath.Join(s.baseDir, id+fileExtension)
}

func writeWSToFile(filePath string, ws *workspace.Workspace) error {
	stored := ws.ToStorage()
	if err := fileutil.WriteJSONAtomic(filePath, stored, filePermissions); err != nil {
		return fmt.Errorf("fileworkspace: %w", err)
	}
	return nil
}

func (s *Store) Create(_ context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("fileworkspace: workspace cannot be nil")
	}
	if ws.ID == "" {
		return workspace.ErrInvalidWorkspaceID
	}
	if err := workspace.ValidateName(ws.Name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[ws.Name]; exists {
		return workspace.ErrWorkspaceAlreadyExists
	}
	if _, exists := s.byID[ws.ID]; exists {
		return workspace.ErrWorkspaceAlreadyExists
	}

	filePath := s.wsFilePath(ws.ID)
	if err := writeWSToFile(filePath, ws); err != nil {
		return err
	}

	s.byID[ws.ID] = filePath
	s.byName[ws.Name] = ws.ID

	return nil
}

func (s *Store) GetByID(_ context.Context, id string) (*workspace.Workspace, error) {
	if id == "" {
		return nil, workspace.ErrInvalidWorkspaceID
	}

	s.mu.RLock()
	filePath, exists := s.byID[id]
	s.mu.RUnlock()

	if !exists {
		return nil, workspace.ErrWorkspaceNotFound
	}

	stored, err := loadStoredFromFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, workspace.ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("fileworkspace: failed to load workspace %s: %w", id, err)
	}

	return stored.ToWorkspace(), nil
}

func (s *Store) GetByName(ctx context.Context, name string) (*workspace.Workspace, error) {
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

func (s *Store) List(ctx context.Context) ([]*workspace.Workspace, error) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	workspaces := make([]*workspace.Workspace, 0, len(ids))
	for _, id := range ids {
		ws, err := s.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, workspace.ErrWorkspaceNotFound) {
				continue
			}
			return nil, err
		}
		workspaces = append(workspaces, ws)
	}

	return workspaces, nil
}

func (s *Store) Update(_ context.Context, ws *workspace.Workspace) error {
	if ws == nil {
		return errors.New("fileworkspace: workspace cannot be nil")
	}
	if ws.ID == "" {
		return workspace.ErrInvalidWorkspaceID
	}
	if err := workspace.ValidateName(ws.Name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, exists := s.byID[ws.ID]
	if !exists {
		return workspace.ErrWorkspaceNotFound
	}

	existing, err := loadStoredFromFile(filePath)
	if err != nil {
		return fmt.Errorf("fileworkspace: failed to load existing workspace: %w", err)
	}

	if existing.Name != ws.Name {
		if existingID, taken := s.byName[ws.Name]; taken && existingID != ws.ID {
			return workspace.ErrWorkspaceAlreadyExists
		}
		delete(s.byName, existing.Name)
		s.byName[ws.Name] = ws.ID
	}

	if err := writeWSToFile(filePath, ws); err != nil {
		if existing.Name != ws.Name {
			delete(s.byName, ws.Name)
			s.byName[existing.Name] = ws.ID
		}
		return err
	}

	return nil
}

func (s *Store) Delete(_ context.Context, id string) error {
	if id == "" {
		return workspace.ErrInvalidWorkspaceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, exists := s.byID[id]
	if !exists {
		return workspace.ErrWorkspaceNotFound
	}

	stored, err := loadStoredFromFile(filePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("Failed to load workspace metadata for deletion, proceeding anyway",
				slog.String("id", id),
				slog.String("error", err.Error()))
		}
		stored = nil
	}

	if err := fileutil.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("fileworkspace: failed to delete workspace file: %w", err)
	}

	delete(s.byID, id)
	if stored != nil {
		delete(s.byName, stored.Name)
	} else {
		for name, wsID := range s.byName {
			if wsID == id {
				delete(s.byName, name)
				break
			}
		}
	}

	return nil
}
