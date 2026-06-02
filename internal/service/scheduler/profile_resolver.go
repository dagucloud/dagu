// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/dagucloud/dagu/internal/profile"
)

type dagProfileResolver struct {
	settingsStore dagsettings.Store
	profileStore  profile.Store
}

func NewDAGProfileResolver(settingsStore dagsettings.Store, profileStore profile.Store) DAGProfileResolver {
	return &dagProfileResolver{
		settingsStore: settingsStore,
		profileStore:  profileStore,
	}
}

func (r *dagProfileResolver) ResolveProfile(ctx context.Context, dagName string) (string, error) {
	if r == nil {
		return "", nil
	}
	return dagsettings.ResolveProfile(ctx, r.settingsStore, r.profileStore, dagName)
}
