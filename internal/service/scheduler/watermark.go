// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"time"
)

// SchedulerState holds persistent watermark state for the scheduler.
// Loaded once at startup, mutated in memory, and saved periodically.
type SchedulerState struct {
	Version  int                     `json:"version"`
	LastTick time.Time               `json:"lastTick"`
	DAGs     map[string]DAGWatermark `json:"dags,omitempty"`
}

// DAGWatermark tracks scheduler state for a single DAG.
type DAGWatermark struct {
	LastScheduledTime        time.Time                      `json:"lastScheduledTime"`
	StartScheduleFingerprint string                         `json:"startScheduleFingerprint,omitempty"`
	SkipSuccessResetAt       time.Time                      `json:"skipSuccessResetAt"`
	OneOffs                  map[string]OneOffScheduleState `json:"oneOffs,omitempty"`
	NextRun                  *time.Time                     `json:"nextRun,omitempty"`
}

// OneOffScheduleStatus is the persisted state of a one-off schedule.
type OneOffScheduleStatus string

const (
	OneOffStatusPending  OneOffScheduleStatus = "pending"
	OneOffStatusConsumed OneOffScheduleStatus = "consumed"
)

// OneOffScheduleState tracks a single one-off schedule instance.
type OneOffScheduleState struct {
	ScheduledTime time.Time            `json:"scheduledTime"`
	Status        OneOffScheduleStatus `json:"status"`
}

// WatermarkStore persists scheduler watermark state to durable storage.
type WatermarkStore interface {
	Load(ctx context.Context) (*SchedulerState, error)
	Save(ctx context.Context, state *SchedulerState) error
}

// noopWatermarkStore is a no-op implementation used when no store is configured.
type noopWatermarkStore struct{}

var _ WatermarkStore = noopWatermarkStore{}

func (noopWatermarkStore) Load(_ context.Context) (*SchedulerState, error) {
	return &SchedulerState{Version: SchedulerStateVersion, DAGs: make(map[string]DAGWatermark)}, nil
}

func (noopWatermarkStore) Save(_ context.Context, _ *SchedulerState) error {
	return nil
}
