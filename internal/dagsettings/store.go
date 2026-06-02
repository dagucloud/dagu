// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagsettings

import "context"

type Store interface {
	Get(ctx context.Context, dagName string) (*Settings, error)
	Upsert(ctx context.Context, settings *Settings) error
	Delete(ctx context.Context, dagName string) error
}
