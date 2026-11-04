// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrRetryStepNotFound = errors.New("retry step not found")
	ErrInvalidRetryPath  = errors.New("retry path is invalid")
	// ErrRepeatingStepTarget indicates the target child DAG run was invoked by a
	// repeating step. Such child runs carry non-reproducible IDs, so only the
	// repeating step itself can be retried.
	ErrRepeatingStepTarget = errors.New("child DAG runs of a repeating step cannot be retried individually")
)

// RetryPath identifies a step in a persisted child DAG run. A path with a Leaf
// instead of a Step targets the last child run itself.
type RetryPath struct {
	Hops []RetryHop `json:"hops"`
	Step string     `json:"step"`
	Leaf RetryLeaf  `json:"leaf,omitempty"`
}

// RetryLeaf selects how the last child run on a path is entered when the path
// names no step in it.
type RetryLeaf string

const (
	// RetryLeafResume continues the child run from its persisted checkpoint.
	RetryLeafResume RetryLeaf = "resume"
	// RetryLeafReuse reports the child run's persisted result without running it.
	RetryLeafReuse RetryLeaf = "reuse"
)

// RetryHop identifies one parent-to-child invocation.
type RetryHop struct {
	Step  string `json:"step"`
	RunID string `json:"runId"`
}

// RootStep returns the root DAG step that contains the target child run.
func (p RetryPath) RootStep() string {
	if len(p.Hops) == 0 {
		return ""
	}
	return p.Hops[0].Step
}

// Current returns the child invocation owned by the current DAG level.
func (p RetryPath) Current() (RetryHop, bool) {
	if len(p.Hops) == 0 {
		return RetryHop{}, false
	}
	return p.Hops[0], true
}

// Advance returns the path to pass into the selected child run.
func (p RetryPath) Advance() RetryPath {
	if len(p.Hops) > 0 {
		p.Hops = p.Hops[1:]
	}
	return p
}

// NextStep returns the step that the selected child run must retry. It is
// empty when the path ends at the selected child run.
func (p RetryPath) NextStep() string {
	if len(p.Hops) > 1 {
		return p.Hops[1].Step
	}
	return p.Step
}

// NextLeaf returns how the selected child run is entered when the path ends at
// that run. It is empty when the path continues to a step or another hop.
func (p RetryPath) NextLeaf() RetryLeaf {
	if len(p.Hops) == 1 {
		return p.Leaf
	}
	return ""
}

// Encode serializes the path for internal transport.
func (p RetryPath) Encode() string {
	if len(p.Hops) == 0 || !p.hasTarget() {
		return ""
	}
	data, _ := json.Marshal(p)
	return string(data)
}

// hasTarget reports whether the path ends at exactly one of a step or a known
// leaf.
func (p RetryPath) hasTarget() bool {
	switch p.Leaf {
	case "":
		return p.Step != ""
	case RetryLeafResume, RetryLeafReuse:
		return p.Step == ""
	default:
		return false
	}
}

// ParseRetryPath parses an internal retry path.
func ParseRetryPath(value string) (RetryPath, error) {
	if value == "" {
		return RetryPath{}, nil
	}
	var path RetryPath
	if err := json.Unmarshal([]byte(value), &path); err != nil {
		return RetryPath{}, fmt.Errorf("parse retry path: %w", err)
	}
	if len(path.Hops) == 0 || !path.hasTarget() {
		return RetryPath{}, fmt.Errorf("parse retry path: path is incomplete")
	}
	for _, hop := range path.Hops {
		if hop.Step == "" || hop.RunID == "" {
			return RetryPath{}, fmt.Errorf("parse retry path: hop is incomplete")
		}
	}
	return path, nil
}
