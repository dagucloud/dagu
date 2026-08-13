// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package schedulerstate defines durable scheduler state and its persistence contract.
package schedulerstate

import (
	"context"
	"maps"
	"time"
)

const CurrentVersion = 4

// State holds persistent scheduler watermark state.
type State struct {
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

// Store persists scheduler state.
type Store interface {
	Load(ctx context.Context) (*State, error)
	Save(ctx context.Context, state *State) error
}

// Clone returns a deep copy of state.
func Clone(state *State) *State {
	if state == nil {
		return nil
	}
	cloned := &State{
		Version:  state.Version,
		LastTick: state.LastTick,
		DAGs:     make(map[string]DAGWatermark, len(state.DAGs)),
	}
	for dagName, dagState := range state.DAGs {
		cloned.DAGs[dagName] = CloneDAGWatermark(dagState)
	}
	return cloned
}

// CloneDAGWatermark returns a deep copy of a DAG watermark.
func CloneDAGWatermark(watermark DAGWatermark) DAGWatermark {
	cloned := DAGWatermark{
		LastScheduledTime:        watermark.LastScheduledTime,
		StartScheduleFingerprint: watermark.StartScheduleFingerprint,
		SkipSuccessResetAt:       watermark.SkipSuccessResetAt,
	}
	if watermark.NextRun != nil {
		nextRun := *watermark.NextRun
		cloned.NextRun = &nextRun
	}
	if len(watermark.OneOffs) > 0 {
		cloned.OneOffs = make(map[string]OneOffScheduleState, len(watermark.OneOffs))
		maps.Copy(cloned.OneOffs, watermark.OneOffs)
	}
	return cloned
}
