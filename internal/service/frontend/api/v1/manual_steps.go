// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	errManualStepNotApproval = errors.New("manual step is not an approval")
	errManualStepHumanTask   = errors.New("human-task state requires the human-task completion API")
)

const manualStepRollbackTimeout = 10 * time.Second

type manualStatusMutationError struct {
	cause error
}

func (e *manualStatusMutationError) Error() string {
	return e.cause.Error()
}

func (e *manualStatusMutationError) Unwrap() error {
	return e.cause
}

func isManualStatusMutationError(err error) bool {
	var mutationErr *manualStatusMutationError
	return errors.As(err, &mutationErr)
}

func manualActionSubject(ctx context.Context) (name, id string) {
	user, ok := auth.UserFromContext(ctx)
	if !ok || user == nil {
		return "", ""
	}
	return user.Username, user.ID
}

func (a *API) compareAndSwapManualStatus(
	ctx context.Context,
	mutationRef ir.DAGRunRef,
	status *ir.DAGRunStatus,
	mutate func(*ir.DAGRunStatus) error,
) (*ir.DAGRunStatus, bool, error) {
	if status == nil {
		return nil, false, errors.New("manual step status is nil")
	}
	targetRef := status.DAGRun()
	if targetRef.Zero() {
		return nil, false, errors.New("manual step DAG-run identity is incomplete")
	}
	var opts []dagrun.CompareAndSwapStatusOption
	if mutationRef != targetRef {
		opts = append(opts, dagrun.WithCompareAndSwapRootDAGRun(mutationRef))
	}
	wrappedMutate := func(latest *ir.DAGRunStatus) error {
		if err := mutate(latest); err != nil {
			return &manualStatusMutationError{cause: err}
		}
		return nil
	}
	return a.dagRunStore.CompareAndSwapLatestAttemptStatus(
		a.withEventContext(ctx),
		targetRef,
		status.AttemptID,
		status.Status,
		wrappedMutate,
		opts...,
	)
}

func cloneManualStatus(status *ir.DAGRunStatus) (*ir.DAGRunStatus, error) {
	data, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	var clone ir.DAGRunStatus
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func (a *API) rollbackPushBack(
	ctx context.Context,
	mutationRef ir.DAGRunRef,
	applied *ir.DAGRunStatus,
	original *ir.DAGRunStatus,
) error {
	if applied == nil || original == nil {
		return errors.New("push-back rollback status is nil")
	}
	type changedNode struct {
		applied  *ir.Node
		original *ir.Node
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

	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), manualStepRollbackTimeout)
	defer cancel()
	_, swapped, err := a.compareAndSwapManualStatus(rollbackCtx, mutationRef, applied, func(latest *ir.DAGRunStatus) error {
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

func requireApprovalNode(node *ir.Node, stepName string) error {
	if node == nil || node.Step.HumanTask != nil {
		return fmt.Errorf("%w: step %s is a human task", errManualStepHumanTask, stepName)
	}
	if node.Step.Approval == nil {
		return fmt.Errorf("%w: step %s does not have approval configuration", errManualStepNotApproval, stepName)
	}
	return nil
}
