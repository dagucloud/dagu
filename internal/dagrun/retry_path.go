// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	ErrRetryStepNotFound = errors.New("retry step not found")
	ErrInvalidRetryPath  = errors.New("retry path is invalid")
	// ErrRepeatingStepTarget indicates the target child DAG run was invoked by a
	// repeating step. Such child runs carry non-reproducible IDs, so only the
	// repeating step itself can be retried.
	ErrRepeatingStepTarget = errors.New("child DAG runs of a repeating step cannot be retried individually")
)

// RetryPath identifies a step in a persisted child DAG run.
type RetryPath struct {
	Hops []RetryHop `json:"hops"`
	Step string     `json:"step"`
}

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

// NextStep returns the step that the selected child run must retry.
func (p RetryPath) NextStep() string {
	if len(p.Hops) > 1 {
		return p.Hops[1].Step
	}
	return p.Step
}

// Encode serializes the path for internal transport.
func (p RetryPath) Encode() string {
	if len(p.Hops) == 0 || p.Step == "" {
		return ""
	}
	data, _ := json.Marshal(p)
	return string(data)
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
	if len(path.Hops) == 0 || path.Step == "" {
		return RetryPath{}, fmt.Errorf("parse retry path: path is incomplete")
	}
	for _, hop := range path.Hops {
		if hop.Step == "" || hop.RunID == "" {
			return RetryPath{}, fmt.Errorf("parse retry path: hop is incomplete")
		}
	}
	return path, nil
}

// ResolveRetryPath resolves the ancestry of a persisted child DAG run.
func ResolveRetryPath(
	ctx context.Context,
	store DAGRunStore,
	root ir.DAGRunRef,
	targetRunID string,
	stepName string,
) (RetryPath, *ir.DAGRunStatus, error) {
	if store == nil {
		return RetryPath{}, nil, errors.New("retry path: DAG-run store is not configured")
	}
	if root.Zero() || targetRunID == "" || stepName == "" {
		return RetryPath{}, nil, fmt.Errorf("%w: root run, child run, and step are required", ErrInvalidRetryPath)
	}

	rootAttempt, err := store.FindAttempt(ctx, root)
	if err != nil {
		return RetryPath{}, nil, fmt.Errorf("find root DAG run: %w", err)
	}
	rootStatus, err := readRetryStatus(ctx, rootAttempt)
	if err != nil {
		return RetryPath{}, nil, fmt.Errorf("read root DAG run: %w", err)
	}

	targetAttempt, err := store.FindSubAttempt(ctx, root, targetRunID)
	if err != nil {
		return RetryPath{}, nil, fmt.Errorf("find child DAG run %s: %w", targetRunID, err)
	}
	targetStatus, err := readRetryStatus(ctx, targetAttempt)
	if err != nil {
		return RetryPath{}, nil, fmt.Errorf("read child DAG run %s: %w", targetRunID, err)
	}

	targetNode, err := targetStatus.NodeByName(stepName)
	if err != nil {
		return RetryPath{}, nil, fmt.Errorf("%w: %s in DAG run %s", ErrRetryStepNotFound, stepName, targetRunID)
	}

	var reversed []RetryHop
	current := targetStatus
	seen := make(map[string]struct{})
	for current.DAGRunID != root.ID {
		if _, ok := seen[current.DAGRunID]; ok {
			return RetryPath{}, nil, fmt.Errorf("%w: cycle at DAG run %s", ErrInvalidRetryPath, current.DAGRunID)
		}
		seen[current.DAGRunID] = struct{}{}

		parentRef := current.Parent
		if parentRef.ID == "" {
			return RetryPath{}, nil, fmt.Errorf("%w: DAG run %s has no parent", ErrInvalidRetryPath, current.DAGRunID)
		}

		var parentStatus *ir.DAGRunStatus
		if parentRef.ID == root.ID {
			parentStatus = rootStatus
		} else {
			parentAttempt, findErr := store.FindSubAttempt(ctx, root, parentRef.ID)
			if findErr != nil {
				return RetryPath{}, nil, fmt.Errorf("%w: find parent DAG run %s: %v", ErrInvalidRetryPath, parentRef.ID, findErr)
			}
			parentStatus, err = readRetryStatus(ctx, parentAttempt)
			if err != nil {
				return RetryPath{}, nil, fmt.Errorf("%w: read parent DAG run %s: %v", ErrInvalidRetryPath, parentRef.ID, err)
			}
		}

		node := retryParentNode(parentStatus, current.DAGRunID)
		if node == nil {
			return RetryPath{}, nil, fmt.Errorf("%w: parent DAG run %s does not reference child %s", ErrInvalidRetryPath, parentRef.ID, current.DAGRunID)
		}
		if node.Step.SubDAG == nil {
			return RetryPath{}, nil, fmt.Errorf("%w: step %s in DAG run %s is not a sub-DAG", ErrInvalidRetryPath, node.Step.Name, parentRef.ID)
		}
		if node.Step.RepeatPolicy.RepeatMode != "" || len(node.SubRunsRepeated) > 0 {
			return RetryPath{}, nil, fmt.Errorf("%w: step %s in DAG run %s repeats", ErrRepeatingStepTarget, node.Step.Name, parentRef.ID)
		}
		reversed = append(reversed, RetryHop{
			Step:  node.Step.Name,
			RunID: current.DAGRunID,
		})
		current = parentStatus
	}

	if len(reversed) == 0 {
		return RetryPath{}, nil, fmt.Errorf("%w: target %s is not a child DAG run", ErrInvalidRetryPath, targetRunID)
	}
	slices.Reverse(reversed)
	return RetryPath{Hops: reversed, Step: targetNode.Step.Name}, targetStatus, nil
}

func readRetryStatus(ctx context.Context, attempt DAGRunAttempt) (*ir.DAGRunStatus, error) {
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, ErrNoStatusData
	}
	return status, nil
}

func retryParentNode(status *ir.DAGRunStatus, childRunID string) *ir.Node {
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		for _, run := range node.SubRuns {
			if run.DAGRunID == childRunID {
				return node
			}
		}
		for _, run := range node.SubRunsRepeated {
			if run.DAGRunID == childRunID {
				return node
			}
		}
	}
	return nil
}
