// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import "github.com/dagucloud/dagu/v2/internal/ir"

// ConditionResult records one condition definition and its latest evaluation error.
type ConditionResult struct {
	Condition string `json:"condition,omitempty"`
	Eval      string `json:"eval,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Negate    bool   `json:"negate,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewConditionResults snapshots condition definitions without evaluation errors.
func NewConditionResults(conditions []*ir.Condition) []ConditionResult {
	if len(conditions) == 0 {
		return nil
	}
	results := make([]ConditionResult, len(conditions))
	for i, condition := range conditions {
		if condition == nil {
			continue
		}
		results[i] = ConditionResult{
			Condition: condition.Condition,
			Eval:      condition.Eval,
			Expected:  condition.Expected,
			Negate:    condition.Negate,
		}
	}
	return results
}

// Definition returns the immutable condition definition represented by the result.
func (r ConditionResult) Definition() *ir.Condition {
	return &ir.Condition{
		Condition: r.Condition,
		Eval:      r.Eval,
		Expected:  r.Expected,
		Negate:    r.Negate,
	}
}

// CloneConditionResults returns an independent copy of results.
func CloneConditionResults(results []ConditionResult) []ConditionResult {
	return append([]ConditionResult(nil), results...)
}

// StepSnapshot combines an immutable step definition with runtime condition results.
// Its JSON shape matches the persisted step object used by existing DAG-run records.
type StepSnapshot struct {
	ir.Step
	Preconditions []ConditionResult `json:"preconditions,omitempty"`
}

// NewStepSnapshot creates a persistence snapshot for a step.
func NewStepSnapshot(step ir.Step, results []ConditionResult) StepSnapshot {
	if results == nil {
		results = NewConditionResults(step.Preconditions)
	}
	step.Preconditions = nil
	return StepSnapshot{
		Step:          step,
		Preconditions: CloneConditionResults(results),
	}
}

// Definition reconstructs the immutable step definition from the snapshot.
func (s StepSnapshot) Definition() ir.Step {
	step := s.Step
	if len(s.Preconditions) == 0 {
		step.Preconditions = nil
		return step
	}
	step.Preconditions = make([]*ir.Condition, len(s.Preconditions))
	for i, result := range s.Preconditions {
		step.Preconditions[i] = result.Definition()
	}
	return step
}
