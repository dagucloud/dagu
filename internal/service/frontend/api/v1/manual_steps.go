// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/dagucloud/dagu/internal/core/exec"
)

var (
	errManualStepNotApproval = errors.New("manual step is not an approval")
	errManualStepHumanTask   = errors.New("human-task state requires the human-task completion API")
)

func (a *API) compareAndSwapManualStatus(
	ctx context.Context,
	mutationRef exec.DAGRunRef,
	status *exec.DAGRunStatus,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	if status == nil {
		return nil, false, errors.New("manual step status is nil")
	}
	targetRef := status.DAGRun()
	if targetRef.Zero() {
		return nil, false, errors.New("manual step DAG-run identity is incomplete")
	}
	opts := []exec.CompareAndSwapStatusOption{
		exec.WithCompareAndSwapExpectedAttemptKey(status.AttemptKey),
	}
	if mutationRef != targetRef {
		opts = append(opts, exec.WithCompareAndSwapRootDAGRun(mutationRef))
	}
	return a.dagRunStore.CompareAndSwapLatestAttemptStatus(
		a.withEventContext(ctx),
		targetRef,
		status.AttemptID,
		status.Status,
		mutate,
		opts...,
	)
}

func cloneManualStatus(status *exec.DAGRunStatus) (*exec.DAGRunStatus, error) {
	data, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	var clone exec.DAGRunStatus
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func (a *API) rollbackPushBack(
	ctx context.Context,
	mutationRef exec.DAGRunRef,
	applied *exec.DAGRunStatus,
	original *exec.DAGRunStatus,
) error {
	if applied == nil || original == nil {
		return errors.New("push-back rollback status is nil")
	}
	type changedNode struct {
		applied  *exec.Node
		original *exec.Node
	}
	changes := make(map[string]changedNode)
	for _, originalNode := range original.Nodes {
		if originalNode == nil {
			continue
		}
		appliedIdx := findStepByName(applied.Nodes, originalNode.Step.Name)
		if appliedIdx < 0 {
			return fmt.Errorf("pushed-back step %s is missing", originalNode.Step.Name)
		}
		appliedNode := applied.Nodes[appliedIdx]
		if !reflect.DeepEqual(originalNode, appliedNode) {
			changes[originalNode.Step.Name] = changedNode{applied: appliedNode, original: originalNode}
		}
	}
	if len(changes) == 0 {
		return nil
	}

	_, swapped, err := a.compareAndSwapManualStatus(ctx, mutationRef, applied, func(latest *exec.DAGRunStatus) error {
		for stepName, change := range changes {
			idx := findStepByName(latest.Nodes, stepName)
			if idx < 0 || !reflect.DeepEqual(latest.Nodes[idx], change.applied) {
				return fmt.Errorf("step %s changed after push-back", stepName)
			}
		}
		for stepName, change := range changes {
			idx := findStepByName(latest.Nodes, stepName)
			latest.Nodes[idx] = change.original
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !swapped {
		return errors.New("DAG-run state changed before push-back could be rolled back")
	}
	return nil
}

func requireApprovalNode(node *exec.Node, stepName string) error {
	if node == nil || node.Step.HumanTask != nil {
		return fmt.Errorf("%w: step %s is a human task", errManualStepHumanTask, stepName)
	}
	if node.Step.Approval == nil {
		return fmt.Errorf("%w: step %s does not have approval configuration", errManualStepNotApproval, stepName)
	}
	return nil
}
