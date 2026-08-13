// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

type noopStateStore struct{}

var _ schedulerstate.Store = noopStateStore{}

func (noopStateStore) Load(_ context.Context) (*schedulerstate.State, error) {
	return &schedulerstate.State{DAGs: make(map[string]schedulerstate.DAGWatermark)}, nil
}

func (noopStateStore) Save(_ context.Context, _ *schedulerstate.State) error {
	return nil
}
