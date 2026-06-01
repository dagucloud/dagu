// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import "context"

type Store interface {
	Create(ctx context.Context, profile *Profile) error
	GetByName(ctx context.Context, name string) (*Profile, error)
	List(ctx context.Context) ([]*Profile, error)
	Update(ctx context.Context, profile *Profile) error
	Delete(ctx context.Context, name string) error
}
