// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

var attemptIDFlag = commandLineFlag{
	name:   "attempt-id",
	usage:  "[only for distributed worker execution] exact attempt ID",
	hidden: true,
}

func getAttemptID(ctx *Context) string {
	attemptID, err := ctx.StringParam("attempt-id")
	if err != nil {
		return ""
	}
	return attemptID
}

func requireWorkerAttemptID(ctx *Context, workerID string) (string, error) {
	attemptID := getAttemptID(ctx)
	if workerID == "local" {
		return attemptID, nil
	}
	if attemptID == "" {
		return "", fmt.Errorf("attempt-id is required for distributed worker execution")
	}
	return attemptID, nil
}

func resolveWorkerPreparedAttempt(
	ctx context.Context,
	dagRunStore dagrun.DAGRunStore,
	dagName, dagRunID string,
	root ir.DAGRunRef,
	requestedAttemptID string,
) (dagrun.DAGRunAttempt, *ir.DAGRunStatus, error) {
	attempt, runStatus, err := readLatestAttempt(ctx, dagRunStore, dagName, dagRunID, root)
	if err != nil {
		return nil, nil, err
	}
	if err := validateWorkerAttemptBinding(dagRunID, requestedAttemptID, attempt, runStatus); err != nil {
		return nil, nil, err
	}
	return attempt, runStatus, nil
}

func readLatestAttempt(
	ctx context.Context,
	dagRunStore dagrun.DAGRunStore,
	dagName, dagRunID string,
	root ir.DAGRunRef,
) (dagrun.DAGRunAttempt, *ir.DAGRunStatus, error) {
	var (
		attempt dagrun.DAGRunAttempt
		err     error
	)
	if root.ID != "" && root.ID != dagRunID {
		attempt, err = dagRunStore.FindSubAttempt(ctx, root, dagRunID)
	} else {
		attempt, err = dagRunStore.FindAttempt(ctx, ir.NewDAGRunRef(dagName, dagRunID))
	}
	if err != nil {
		return nil, nil, err
	}

	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	return attempt, runStatus, nil
}

func validateWorkerAttemptBinding(
	dagRunID, requestedAttemptID string,
	attempt dagrun.DAGRunAttempt,
	runStatus *ir.DAGRunStatus,
) error {
	currentAttemptID := requestedAttemptID
	if runStatus != nil && runStatus.AttemptID != "" {
		currentAttemptID = runStatus.AttemptID
	} else if attempt != nil && attempt.ID() != "" {
		currentAttemptID = attempt.ID()
	}

	if requestedAttemptID == "" {
		return fmt.Errorf("attempt-id is required for distributed worker execution")
	}
	if currentAttemptID != requestedAttemptID {
		return fmt.Errorf(
			"distributed worker attempt %q is stale for dag-run %s; latest attempt is %q",
			requestedAttemptID,
			dagRunID,
			currentAttemptID,
		)
	}
	return nil
}
