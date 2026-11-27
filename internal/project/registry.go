// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"context"
	"errors"
)

// Errors returned by Registry methods and the name rules.
var (
	ErrNotFound           = errors.New("project not found")
	ErrNameTaken          = errors.New("a project with this name already exists")
	ErrInvalidName        = errors.New("invalid project name")
	ErrInvalidDisplayName = errors.New("invalid project display name")
	ErrDefaultProject     = errors.New("the default project cannot be deleted")
)

// Errors that refuse a create or a delete. A refused create or delete
// changes nothing.
var (
	// ErrStateDirTaken reports that a state directory of the name to create
	// records another project.
	ErrStateDirTaken = errors.New("a state directory of this name records another project")
	// ErrProtectedPath reports that a project's DAG directory would be or
	// contain a path that the server, the library, or another project uses.
	ErrProtectedPath = errors.New("protected paths in the project directory")
	// ErrDeleteRefused reports that a project cannot be deleted in its
	// current state.
	ErrDeleteRefused = errors.New("project cannot be deleted")
)

// Registry is the server-wide list of projects, and the only one: a project
// exists exactly when the registry holds it. Methods that take an ID return
// ErrNotFound when no project has it. Implementations are safe for
// concurrent use by several goroutines and processes.
type Registry interface {
	// List returns every registered project, ordered by name.
	List(ctx context.Context) ([]Project, error)

	// Get returns the project whose name matches name without regard to
	// case, with the name as stored. It returns ErrNotFound when none does.
	Get(ctx context.Context, name string) (Project, error)

	// GetByID returns the project with the given ID.
	GetByID(ctx context.Context, id string) (Project, error)

	// Create registers a project under a new ID and returns it. It returns
	// ErrInvalidName or ErrInvalidDisplayName when in breaks the name rules,
	// and ErrNameTaken when a project has the same name without regard to
	// case.
	Create(ctx context.Context, in CreateInput) (Project, error)

	// EnsureDefault returns the default project, registering it first when
	// the registry lacks it.
	EnsureDefault(ctx context.Context) (Project, error)

	// SetDisplayName changes a project's display name and returns the
	// project. An empty display name restores the name. It returns
	// ErrInvalidDisplayName when displayName breaks the rules.
	SetDisplayName(ctx context.Context, id, displayName string) (Project, error)

	// SetDeleting marks or unmarks a project as being deleted and returns
	// the project. It returns ErrDefaultProject when asked to mark the
	// default project.
	SetDeleting(ctx context.Context, id string, deleting bool) (Project, error)

	// Delete removes a project from the registry. It returns
	// ErrDefaultProject for the default project.
	Delete(ctx context.Context, id string) error

	// Watch calls onChange with every registered project when it starts, or
	// once the registry can be read, and again each time the registry
	// changes, until ctx ends. Calls are made one at a time from the
	// goroutine that called Watch.
	Watch(ctx context.Context, onChange func([]Project))
}

// CreateInput describes a project to register.
type CreateInput struct {
	Name string
	// DisplayName is the name when empty.
	DisplayName string
	// MigratedFrom is the name of the v2 workspace a migrated project comes
	// from. It decides the project's slot seed; see Project.SlotSeed.
	MigratedFrom string
}
