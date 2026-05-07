// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var workspaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Workspace is the domain model for a cockpit workspace.
type Workspace struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewWorkspace creates a Workspace with a new UUID and current timestamps.
func NewWorkspace(name, description string) *Workspace {
	now := time.Now().UTC()
	return &Workspace{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ValidateName checks whether a workspace name can be safely reused as a label
// value and filesystem path segment.
func ValidateName(name string) error {
	if name == "" {
		return ErrInvalidWorkspaceName
	}
	switch strings.ToLower(name) {
	case "all", "default":
		return fmt.Errorf("%w: all and default are reserved names", ErrInvalidWorkspaceName)
	}
	if !workspaceNamePattern.MatchString(name) {
		return fmt.Errorf("%w: must contain only letters, numbers, underscores, and hyphens", ErrInvalidWorkspaceName)
	}
	return nil
}

// WorkspaceForStorage is used for JSON serialization to persistent storage.
type WorkspaceForStorage struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToStorage converts a Workspace to WorkspaceForStorage.
func (w *Workspace) ToStorage() *WorkspaceForStorage {
	return &WorkspaceForStorage{
		ID:          w.ID,
		Name:        w.Name,
		Description: w.Description,
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
}

// ToWorkspace converts WorkspaceForStorage back to Workspace.
func (s *WorkspaceForStorage) ToWorkspace() *Workspace {
	return &Workspace{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
}
