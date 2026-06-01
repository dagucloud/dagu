// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import "context"

// Store persists runtime profiles by name. Implementations must be safe for
// concurrent use and enforce profile name uniqueness. GetByName and Delete
// return ErrNotFound when the named profile does not exist. Create and Update
// validate stored profile data before persisting it.
type Store interface {
	// Create persists a new Profile and returns ErrAlreadyExists when the name is already used.
	Create(ctx context.Context, profile *Profile) error
	// GetByName returns the Profile with the given name or ErrNotFound.
	GetByName(ctx context.Context, name string) (*Profile, error)
	// List returns all stored profiles.
	List(ctx context.Context) ([]*Profile, error)
	// Update replaces an existing Profile and returns ErrNotFound when it does not exist.
	Update(ctx context.Context, profile *Profile) error
	// Delete removes the named Profile and returns ErrNotFound when it does not exist.
	Delete(ctx context.Context, name string) error
}
