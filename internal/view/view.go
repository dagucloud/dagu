// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package view defines shared saved view configurations.
package view

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// Render types.
const (
	// TypeKanban renders the view as the Overview Kanban board.
	TypeKanban = "kanban"
	// TypeWorkflow filters and sorts the Workflows page.
	TypeWorkflow = "workflow"
)

// Workflow workspace scopes.
const (
	WorkspaceScopeAll       = "all"
	WorkspaceScopeDefault   = "default"
	WorkspaceScopeWorkspace = "workspace"
)

// Workflow sort fields and orders.
const (
	WorkflowSortName    = "name"
	WorkflowSortNextRun = "nextRun"
	SortOrderAscending  = "asc"
	SortOrderDescending = "desc"
)

// Kanban columns.
const (
	ColumnQueued  = "queued"
	ColumnRunning = "running"
	ColumnReview  = "review"
	ColumnDone    = "done"
	ColumnFailed  = "failed"
)

var defaultColumns = []string{
	ColumnQueued,
	ColumnRunning,
	ColumnReview,
	ColumnDone,
	ColumnFailed,
}

// Field bounds.
const (
	MaxNameLength    = 100
	MaxDAGNameLength = 255
	MaxLabels        = 50
	MaxLabelLength   = 128
	MinIntervalDays  = 1
	MaxIntervalDays  = 30
)

// Sentinel errors returned by views and their stores.
var (
	ErrInvalidViewID         = errors.New("view: invalid id")
	ErrViewNotFound          = errors.New("view: not found")
	ErrViewExists            = errors.New("view: already exists")
	ErrInvalidName           = errors.New("view: name is required")
	ErrNameTooLong           = errors.New("view: name too long")
	ErrDAGNameTooLong        = errors.New("view: dagName too long")
	ErrInvalidInterval       = errors.New("view: intervalDays out of range")
	ErrTooManyLabels         = errors.New("view: too many labels")
	ErrInvalidType           = errors.New("view: unknown type")
	ErrInvalidColumns        = errors.New("view: invalid columns")
	ErrInvalidWorkspaceScope = errors.New("view: invalid workspace scope")
	ErrInvalidSortField      = errors.New("view: invalid sort field")
	ErrInvalidSortOrder      = errors.New("view: invalid sort order")
	ErrViewChanged           = errors.New("view: changed")
)

// View is a shared saved view configuration. CreatedBy is recorded for display
// only and confers no ownership.
type View struct {
	ID             string
	Name           string
	Type           string
	Workspace      string
	Labels         []string
	DAGName        string
	IntervalDays   int
	Columns        []string
	Pinned         bool
	WorkspaceScope string
	SortField      string
	SortOrder      string
	ActiveOnly     bool
	Default        bool
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Normalize trims string fields, drops empty or oversized labels, and applies
// default values. Call before Validate.
func (v *View) Normalize() {
	v.Name = strings.TrimSpace(v.Name)
	v.Workspace = strings.TrimSpace(v.Workspace)
	v.DAGName = strings.TrimSpace(v.DAGName)
	v.Type = strings.TrimSpace(v.Type)
	if v.Type == "" {
		v.Type = TypeKanban
	}
	switch v.Type {
	case TypeKanban:
		v.WorkspaceScope = ""
		v.SortField = ""
		v.SortOrder = ""
		v.ActiveOnly = false
		v.Default = false
		if len(v.Columns) == 0 {
			v.Columns = DefaultColumns()
		}
	case TypeWorkflow:
		v.WorkspaceScope = strings.TrimSpace(v.WorkspaceScope)
		if v.WorkspaceScope == "" {
			if v.Workspace == "" {
				v.WorkspaceScope = WorkspaceScopeAll
			} else {
				v.WorkspaceScope = WorkspaceScopeWorkspace
			}
		}
		v.SortField = strings.TrimSpace(v.SortField)
		if v.SortField == "" {
			v.SortField = WorkflowSortName
		}
		v.SortOrder = strings.TrimSpace(v.SortOrder)
		if v.SortOrder == "" {
			v.SortOrder = SortOrderAscending
		}
		v.IntervalDays = MinIntervalDays
		v.Columns = nil
	}
	labels := make([]string, 0, len(v.Labels))
	for _, l := range v.Labels {
		l = strings.TrimSpace(l)
		if l != "" && len([]rune(l)) <= MaxLabelLength {
			labels = append(labels, l)
		}
	}
	v.Labels = labels
}

// Validate reports whether the view's fields satisfy their bounds. It assumes
// Normalize has already been applied.
func (v *View) Validate() error {
	switch {
	case v.Name == "":
		return ErrInvalidName
	case len([]rune(v.Name)) > MaxNameLength:
		return ErrNameTooLong
	case len([]rune(v.DAGName)) > MaxDAGNameLength:
		return ErrDAGNameTooLong
	case len(v.Labels) > MaxLabels:
		return ErrTooManyLabels
	case !ValidType(v.Type):
		return ErrInvalidType
	}
	if v.Type == TypeKanban {
		switch {
		case v.IntervalDays < MinIntervalDays || v.IntervalDays > MaxIntervalDays:
			return ErrInvalidInterval
		case !ValidColumns(v.Columns):
			return ErrInvalidColumns
		}
		return nil
	}
	switch {
	case !ValidWorkspaceScope(v.WorkspaceScope, v.Workspace):
		return ErrInvalidWorkspaceScope
	case !ValidWorkflowSortField(v.SortField):
		return ErrInvalidSortField
	case !ValidSortOrder(v.SortOrder):
		return ErrInvalidSortOrder
	}
	return nil
}

// DefaultColumns returns all Kanban columns in their default display order.
func DefaultColumns() []string {
	return slices.Clone(defaultColumns)
}

// ValidColumns reports whether columns is a non-empty, duplicate-free subset
// of the supported Kanban columns.
func ValidColumns(columns []string) bool {
	if len(columns) == 0 || len(columns) > len(defaultColumns) {
		return false
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if !slices.Contains(defaultColumns, column) {
			return false
		}
		if _, exists := seen[column]; exists {
			return false
		}
		seen[column] = struct{}{}
	}
	return true
}

// ValidType reports whether t is a known render type.
func ValidType(t string) bool {
	switch t {
	case TypeKanban, TypeWorkflow:
		return true
	default:
		return false
	}
}

// ValidWorkspaceScope reports whether scope and workspace identify a workflow scope.
func ValidWorkspaceScope(scope string, workspace string) bool {
	switch scope {
	case WorkspaceScopeAll, WorkspaceScopeDefault:
		return workspace == ""
	case WorkspaceScopeWorkspace:
		return workspace != ""
	default:
		return false
	}
}

// ValidWorkflowSortField reports whether field is supported by the Workflows page.
func ValidWorkflowSortField(field string) bool {
	return field == WorkflowSortName || field == WorkflowSortNextRun
}

// ValidSortOrder reports whether order is a supported view sort order.
func ValidSortOrder(order string) bool {
	return order == SortOrderAscending || order == SortOrderDescending
}

// ViewForStorage is the on-disk JSON representation of a View.
type ViewForStorage struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Workspace      string    `json:"workspace,omitempty"`
	Labels         []string  `json:"labels,omitempty"`
	DAGName        string    `json:"dag_name,omitempty"`
	IntervalDays   int       `json:"interval_days"`
	Columns        []string  `json:"columns,omitempty"`
	Pinned         bool      `json:"pinned,omitempty"`
	WorkspaceScope string    `json:"workspace_scope,omitempty"`
	SortField      string    `json:"sort_field,omitempty"`
	SortOrder      string    `json:"sort_order,omitempty"`
	ActiveOnly     bool      `json:"active_only,omitempty"`
	Default        bool      `json:"default,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ToStorage converts a View to its persistence representation.
func (v *View) ToStorage() *ViewForStorage {
	return &ViewForStorage{
		ID:             v.ID,
		Name:           v.Name,
		Type:           v.Type,
		Workspace:      v.Workspace,
		Labels:         slices.Clone(v.Labels),
		DAGName:        v.DAGName,
		IntervalDays:   v.IntervalDays,
		Columns:        slices.Clone(v.Columns),
		Pinned:         v.Pinned,
		WorkspaceScope: v.WorkspaceScope,
		SortField:      v.SortField,
		SortOrder:      v.SortOrder,
		ActiveOnly:     v.ActiveOnly,
		Default:        v.Default,
		CreatedBy:      v.CreatedBy,
		CreatedAt:      v.CreatedAt,
		UpdatedAt:      v.UpdatedAt,
	}
}

// ToView converts a stored representation back to a View.
func (s *ViewForStorage) ToView() *View {
	columns := slices.Clone(s.Columns)
	if len(columns) == 0 && (s.Type == "" || s.Type == TypeKanban) {
		columns = DefaultColumns()
	}
	return &View{
		ID:             s.ID,
		Name:           s.Name,
		Type:           s.Type,
		Workspace:      s.Workspace,
		Labels:         slices.Clone(s.Labels),
		DAGName:        s.DAGName,
		IntervalDays:   s.IntervalDays,
		Columns:        columns,
		Pinned:         s.Pinned,
		WorkspaceScope: s.WorkspaceScope,
		SortField:      s.SortField,
		SortOrder:      s.SortOrder,
		ActiveOnly:     s.ActiveOnly,
		Default:        s.Default,
		CreatedBy:      s.CreatedBy,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

// Store persists view configurations. Implementations are safe for concurrent
// use. List returns views ordered by creation time, oldest first.
type Store interface {
	Create(ctx context.Context, v *View) error
	GetByID(ctx context.Context, id string) (*View, error)
	List(ctx context.Context) ([]*View, error)
	Update(ctx context.Context, v *View, expectedWorkspace string) error
	Delete(ctx context.Context, id string, expectedWorkspace string) error
}
