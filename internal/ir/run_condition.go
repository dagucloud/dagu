// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "slices"

// ConditionResult records one condition definition and its latest evaluation error.
type ConditionResult struct {
	Condition
	Error string `json:"error,omitempty"`
}

func conditionResults(conditions []*Condition) []ConditionResult {
	if len(conditions) == 0 {
		return nil
	}
	results := make([]ConditionResult, len(conditions))
	for i, condition := range conditions {
		if condition == nil {
			continue
		}
		results[i].Condition = *condition
	}
	return results
}

type stepSnapshot struct {
	Step
	Preconditions []ConditionResult `json:"preconditions,omitempty"`
}

func newStepSnapshot(step Step, results []ConditionResult) stepSnapshot {
	if results == nil {
		results = conditionResults(step.Preconditions)
	}
	step.Preconditions = nil
	return stepSnapshot{
		Step:          step,
		Preconditions: slices.Clone(results),
	}
}

func (s stepSnapshot) definition() Step {
	step := s.Step
	if len(s.Preconditions) == 0 {
		step.Preconditions = nil
		return step
	}
	step.Preconditions = make([]*Condition, len(s.Preconditions))
	for i, result := range s.Preconditions {
		step.Preconditions[i] = new(Condition)
		*step.Preconditions[i] = result.Condition
	}
	return step
}
