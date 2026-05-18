// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var workspaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const (
	// BaseConfigDirName is the directory below DAGsDir that stores workspace-scoped config.
	BaseConfigDirName = "workspaces"
	// BaseConfigFileName is the file name for a workspace-scoped base config.
	BaseConfigFileName = "base.yaml"
)

// BaseConfigDir returns the root directory for workspace-scoped base configs.
func BaseConfigDir(dagsDir string) string {
	if strings.TrimSpace(dagsDir) == "" {
		return ""
	}
	return filepath.Join(dagsDir, BaseConfigDirName)
}

// BaseConfigPath returns the workspace-scoped base config path for name.
func BaseConfigPath(dagsDir, name string) string {
	if strings.TrimSpace(dagsDir) == "" || strings.TrimSpace(name) == "" {
		return ""
	}
	if err := ValidateName(name); err != nil {
		return ""
	}
	return filepath.Join(BaseConfigDir(dagsDir), name, BaseConfigFileName)
}

// BaseConfigStem returns the sync item name for a base config file without extension.
func BaseConfigStem() string {
	return strings.TrimSuffix(BaseConfigFileName, filepath.Ext(BaseConfigFileName))
}

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
		ID:          uuid.New().String(),
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
	case "all", "default", "global":
		return fmt.Errorf("%w: all, default, and global are reserved names", ErrInvalidWorkspaceName)
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
