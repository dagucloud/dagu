// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"fmt"
	"time"
)

// RuntimeProfileEntry is non-secret metadata about a profile key injected into a run.
type RuntimeProfileEntry struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

// PendingStepRetry describes a step retry waiting to be scheduled.
type PendingStepRetry struct {
	StepName string        `json:"stepName"`
	Interval time.Duration `json:"interval"`
}

// MarshalJSON emits Interval as a Go duration string.
func (p PendingStepRetry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		StepName string `json:"stepName"`
		Interval string `json:"interval"`
	}{
		StepName: p.StepName,
		Interval: p.Interval.String(),
	})
}

// UnmarshalJSON accepts current string durations and legacy numeric nanoseconds.
func (p *PendingStepRetry) UnmarshalJSON(data []byte) error {
	var current struct {
		StepName string `json:"stepName"`
		Interval string `json:"interval"`
	}
	if err := json.Unmarshal(data, &current); err == nil && current.Interval != "" {
		interval, parseErr := time.ParseDuration(current.Interval)
		if parseErr != nil {
			return fmt.Errorf("parse pending step retry interval: %w", parseErr)
		}
		p.StepName = current.StepName
		p.Interval = interval
		return nil
	}

	var legacy struct {
		StepName string        `json:"stepName"`
		Interval time.Duration `json:"interval"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	p.StepName = legacy.StepName
	p.Interval = legacy.Interval
	return nil
}

// RunStatus is the result returned to parent DAG executors.
type RunStatus struct {
	Name               string
	DAGRunID           string
	Params             string
	Outputs            map[string]string
	OutputValues       map[string]any
	Status             Status
	PreconditionNotMet bool `json:"-"`
	PendingStepRetries []PendingStepRetry
}

// MarshalJSON implements json.Marshaler.
func (r *RunStatus) MarshalJSON() ([]byte, error) {
	return json.MarshalIndent(struct {
		Name               string             `json:"name,omitempty"`
		DAGRunID           string             `json:"dagRunId,omitempty"`
		Params             string             `json:"params,omitempty"`
		Outputs            map[string]string  `json:"outputs,omitzero"`
		OutputValues       map[string]any     `json:"outputValues,omitzero"`
		Status             string             `json:"status"`
		PendingStepRetries []PendingStepRetry `json:"pendingStepRetries,omitempty"`
	}{
		Name:               r.Name,
		DAGRunID:           r.DAGRunID,
		Params:             r.Params,
		Outputs:            r.Outputs,
		OutputValues:       r.OutputValues,
		Status:             r.Status.String(),
		PendingStepRetries: r.PendingStepRetries,
	}, "", "  ")
}
