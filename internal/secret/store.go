// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import "context"

type ListOptions struct {
	// Workspace filters by internal workspace name. Nil means all scopes.
	// A pointer to GlobalWorkspace means the global scope.
	Workspace *string
}

type Store interface {
	Create(ctx context.Context, secret *Secret, initialValue *WriteValueInput) error
	GetByID(ctx context.Context, id string) (*Secret, error)
	GetByRef(ctx context.Context, workspace, ref string) (*Secret, error)
	List(ctx context.Context, opts ListOptions) ([]*Secret, error)
	Update(ctx context.Context, secret *Secret) error
	Delete(ctx context.Context, id string) error
	WriteValue(ctx context.Context, id string, input WriteValueInput) (*Secret, error)
	GetCurrentVersion(ctx context.Context, id string) (*VersionMetadata, error)
	ResolveValue(ctx context.Context, id string) (string, *VersionMetadata, error)
}
