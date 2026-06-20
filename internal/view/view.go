// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package view defines saved Overview view configurations. A view captures a
// DAG-run query (workspace, labels, DAG name, relative date window) and the
// render type used to display it. Kanban is currently the only render type;
// the Type field is a forward-compatible discriminator for future types.
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
)

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
	ErrInvalidViewID   = errors.New("view: invalid id")
	ErrViewNotFound    = errors.New("view: not found")
	ErrViewExists      = errors.New("view: already exists")
	ErrInvalidName     = errors.New("view: name is required")
	ErrNameTooLong     = errors.New("view: name too long")
	ErrDAGNameTooLong  = errors.New("view: dagName too long")
	ErrInvalidInterval = errors.New("view: intervalDays out of range")
	ErrTooManyLabels   = errors.New("view: too many labels")
	ErrInvalidType     = errors.New("view: unknown type")
	ErrViewChanged     = errors.New("view: changed")
)

// View is a saved Overview view configuration. Views are global and shared:
// they are keyed by ID with no per-user scoping. CreatedBy is recorded for
// display only and confers no ownership.
type View struct {
	ID           string
	Name         string
	Type         string
	Workspace    string // empty means all workspaces
	Labels       []string
	DAGName      string
	IntervalDays int
	Pinned       bool
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Normalize trims string fields, drops empty or oversized labels, and applies
// the default render type. Call before Validate.
func (v *View) Normalize() {
	v.Name = strings.TrimSpace(v.Name)
	v.Workspace = strings.TrimSpace(v.Workspace)
	v.DAGName = strings.TrimSpace(v.DAGName)
	v.Type = strings.TrimSpace(v.Type)
	if v.Type == "" {
		v.Type = TypeKanban
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
	case v.IntervalDays < MinIntervalDays || v.IntervalDays > MaxIntervalDays:
		return ErrInvalidInterval
	case len(v.Labels) > MaxLabels:
		return ErrTooManyLabels
	case !ValidType(v.Type):
		return ErrInvalidType
	}
	return nil
}

// ValidType reports whether t is a known render type.
func ValidType(t string) bool {
	switch t {
	case TypeKanban:
		return true
	default:
		return false
	}
}

// ViewForStorage is the on-disk JSON representation of a View.
type ViewForStorage struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	Workspace    string    `json:"workspace,omitempty"`
	Labels       []string  `json:"labels,omitempty"`
	DAGName      string    `json:"dag_name,omitempty"`
	IntervalDays int       `json:"interval_days"`
	Pinned       bool      `json:"pinned,omitempty"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ToStorage converts a View to its persistence representation.
func (v *View) ToStorage() *ViewForStorage {
	return &ViewForStorage{
		ID:           v.ID,
		Name:         v.Name,
		Type:         v.Type,
		Workspace:    v.Workspace,
		Labels:       slices.Clone(v.Labels),
		DAGName:      v.DAGName,
		IntervalDays: v.IntervalDays,
		Pinned:       v.Pinned,
		CreatedBy:    v.CreatedBy,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	}
}

// ToView converts a stored representation back to a View.
func (s *ViewForStorage) ToView() *View {
	return &View{
		ID:           s.ID,
		Name:         s.Name,
		Type:         s.Type,
		Workspace:    s.Workspace,
		Labels:       slices.Clone(s.Labels),
		DAGName:      s.DAGName,
		IntervalDays: s.IntervalDays,
		Pinned:       s.Pinned,
		CreatedBy:    s.CreatedBy,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
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
