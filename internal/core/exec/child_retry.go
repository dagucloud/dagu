// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrChildRetryStepNotFound    = errors.New("child retry step not found")
	ErrChildRetryInvalidAncestry = errors.New("child retry ancestry is invalid")
	ErrChildRetryUnsupported     = errors.New("child retry parent step is unsupported")
)

// ChildRetryRoute identifies a step in one persisted child DAG run.
type ChildRetryRoute struct {
	Segments   []ChildRetrySegment `json:"segments"`
	TargetStep string              `json:"targetStep"`
}

// ChildRetrySegment identifies one parent-to-child invocation in a retry route.
type ChildRetrySegment struct {
	ParentStep string      `json:"parentStep"`
	DAGRunID   string      `json:"dagRunId"`
	DAGName    string      `json:"dagName"`
	Params     string      `json:"params,omitempty"`
	Runs       []SubDAGRun `json:"runs,omitempty"`
}

// RootStep returns the root DAG step that contains the target child run.
func (r ChildRetryRoute) RootStep() string {
	if len(r.Segments) == 0 {
		return ""
	}
	return r.Segments[0].ParentStep
}

// Current returns the child invocation owned by the current DAG level.
func (r ChildRetryRoute) Current() (ChildRetrySegment, bool) {
	if len(r.Segments) == 0 {
		return ChildRetrySegment{}, false
	}
	return r.Segments[0], true
}

// Advance returns the route to pass into the selected child run.
func (r ChildRetryRoute) Advance() ChildRetryRoute {
	if len(r.Segments) == 0 {
		return ChildRetryRoute{TargetStep: r.TargetStep}
	}
	segments := make([]ChildRetrySegment, len(r.Segments)-1)
	copy(segments, r.Segments[1:])
	return ChildRetryRoute{Segments: segments, TargetStep: r.TargetStep}
}

// NextStep returns the step that the selected child run must retry.
func (r ChildRetryRoute) NextStep() string {
	next := r.Advance()
	if segment, ok := next.Current(); ok {
		return segment.ParentStep
	}
	return next.TargetStep
}

// MarshalText serializes the route for internal command and transport fields.
func (r ChildRetryRoute) MarshalText() ([]byte, error) {
	if len(r.Segments) == 0 || r.TargetStep == "" {
		return nil, nil
	}
	type routeJSON ChildRetryRoute
	return json.Marshal(routeJSON(r))
}

// ParseChildRetryRoute parses an internal child retry route.
func ParseChildRetryRoute(value string) (ChildRetryRoute, error) {
	if value == "" {
		return ChildRetryRoute{}, nil
	}
	var route ChildRetryRoute
	if err := json.Unmarshal([]byte(value), &route); err != nil {
		return ChildRetryRoute{}, fmt.Errorf("parse child retry route: %w", err)
	}
	if len(route.Segments) == 0 || route.TargetStep == "" {
		return ChildRetryRoute{}, fmt.Errorf("parse child retry route: route is incomplete")
	}
	for _, segment := range route.Segments {
		if segment.ParentStep == "" || segment.DAGRunID == "" || segment.DAGName == "" {
			return ChildRetryRoute{}, fmt.Errorf("parse child retry route: segment is incomplete")
		}
	}
	return route, nil
}

// ResolveChildRetryRoute resolves the ancestry of a persisted child DAG run.
func ResolveChildRetryRoute(
	ctx context.Context,
	store DAGRunStore,
	root DAGRunRef,
	targetRunID string,
	stepName string,
) (ChildRetryRoute, *DAGRunStatus, error) {
	if store == nil {
		return ChildRetryRoute{}, nil, errors.New("child retry: DAG-run store is not configured")
	}
	if root.Zero() || targetRunID == "" || stepName == "" {
		return ChildRetryRoute{}, nil, fmt.Errorf("%w: root run, child run, and step are required", ErrChildRetryInvalidAncestry)
	}

	rootAttempt, err := store.FindAttempt(ctx, root)
	if err != nil {
		return ChildRetryRoute{}, nil, fmt.Errorf("find root DAG run: %w", err)
	}
	rootStatus, err := readChildRetryStatus(ctx, rootAttempt)
	if err != nil {
		return ChildRetryRoute{}, nil, fmt.Errorf("read root DAG run: %w", err)
	}

	targetAttempt, err := store.FindSubAttempt(ctx, root, targetRunID)
	if err != nil {
		return ChildRetryRoute{}, nil, fmt.Errorf("find child DAG run %s: %w", targetRunID, err)
	}
	targetStatus, err := readChildRetryStatus(ctx, targetAttempt)
	if err != nil {
		return ChildRetryRoute{}, nil, fmt.Errorf("read child DAG run %s: %w", targetRunID, err)
	}

	canonicalStep, ok := childRetryStepName(targetStatus, stepName)
	if !ok {
		return ChildRetryRoute{}, nil, fmt.Errorf("%w: %s in DAG run %s", ErrChildRetryStepNotFound, stepName, targetRunID)
	}

	var reversed []ChildRetrySegment
	current := targetStatus
	seen := make(map[string]struct{})
	for current.DAGRunID != root.ID {
		if _, ok := seen[current.DAGRunID]; ok {
			return ChildRetryRoute{}, nil, fmt.Errorf("%w: cycle at DAG run %s", ErrChildRetryInvalidAncestry, current.DAGRunID)
		}
		seen[current.DAGRunID] = struct{}{}

		parentRef := current.Parent
		if parentRef.ID == "" {
			return ChildRetryRoute{}, nil, fmt.Errorf("%w: DAG run %s has no parent", ErrChildRetryInvalidAncestry, current.DAGRunID)
		}

		var parentStatus *DAGRunStatus
		if parentRef.ID == root.ID {
			parentStatus = rootStatus
		} else {
			parentAttempt, findErr := store.FindSubAttempt(ctx, root, parentRef.ID)
			if findErr != nil {
				return ChildRetryRoute{}, nil, fmt.Errorf("%w: find parent DAG run %s: %v", ErrChildRetryInvalidAncestry, parentRef.ID, findErr)
			}
			parentStatus, err = readChildRetryStatus(ctx, parentAttempt)
			if err != nil {
				return ChildRetryRoute{}, nil, fmt.Errorf("%w: read parent DAG run %s: %v", ErrChildRetryInvalidAncestry, parentRef.ID, err)
			}
		}

		node, link := childRetryParentNode(parentStatus, current.DAGRunID)
		if node == nil {
			return ChildRetryRoute{}, nil, fmt.Errorf("%w: parent DAG run %s does not reference child %s", ErrChildRetryInvalidAncestry, parentRef.ID, current.DAGRunID)
		}
		if node.Step.SubDAG == nil {
			return ChildRetryRoute{}, nil, fmt.Errorf("%w: step %s in DAG run %s", ErrChildRetryUnsupported, node.Step.Name, parentRef.ID)
		}
		if link.DAGName == "" {
			link.DAGName = current.Name
		}
		reversed = append(reversed, ChildRetrySegment{
			ParentStep: node.Step.Name,
			DAGRunID:   link.DAGRunID,
			DAGName:    link.DAGName,
			Params:     link.Params,
			Runs:       childRetryRuns(node),
		})
		current = parentStatus
	}

	if len(reversed) == 0 {
		return ChildRetryRoute{}, nil, fmt.Errorf("%w: target %s is not a child DAG run", ErrChildRetryInvalidAncestry, targetRunID)
	}
	segments := make([]ChildRetrySegment, len(reversed))
	for i := range reversed {
		segments[len(reversed)-1-i] = reversed[i]
	}
	return ChildRetryRoute{Segments: segments, TargetStep: canonicalStep}, targetStatus, nil
}

func readChildRetryStatus(ctx context.Context, attempt DAGRunAttempt) (*DAGRunStatus, error) {
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, ErrNoStatusData
	}
	return status, nil
}

func childRetryStepName(status *DAGRunStatus, stepName string) (string, bool) {
	for _, node := range status.Nodes {
		if node == nil || (node.Step.Name != stepName && node.Step.ID != stepName) {
			continue
		}
		return node.Step.Name, true
	}
	return "", false
}

func childRetryParentNode(status *DAGRunStatus, childRunID string) (*Node, SubDAGRun) {
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		for _, run := range node.SubRuns {
			if run.DAGRunID == childRunID {
				return node, run
			}
		}
		for _, run := range node.SubRunsRepeated {
			if run.DAGRunID == childRunID {
				return node, run
			}
		}
	}
	return nil, SubDAGRun{}
}

func childRetryRuns(node *Node) []SubDAGRun {
	runs := make([]SubDAGRun, 0, len(node.SubRuns)+len(node.SubRunsRepeated))
	seen := make(map[string]struct{}, cap(runs))
	for _, run := range append(append([]SubDAGRun(nil), node.SubRuns...), node.SubRunsRepeated...) {
		if run.DAGRunID == "" {
			continue
		}
		if _, ok := seen[run.DAGRunID]; ok {
			continue
		}
		seen[run.DAGRunID] = struct{}{}
		runs = append(runs, run)
	}
	return runs
}
