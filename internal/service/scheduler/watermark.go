// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// noopWatermarkStore is a no-op implementation used when no store is configured.
type noopWatermarkStore struct{}

var _ schedulerstate.Store = noopWatermarkStore{}

func (noopWatermarkStore) Load(_ context.Context) (*schedulerstate.State, error) {
	return &schedulerstate.State{
		Version: schedulerstate.CurrentVersion,
		DAGs:    make(map[string]schedulerstate.DAGWatermark),
	}, nil
}

func (noopWatermarkStore) Save(_ context.Context, _ *schedulerstate.State) error {
	return nil
}
