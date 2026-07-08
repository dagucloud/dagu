// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"

	"github.com/dagucloud/dagu/internal/core/exec"
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

func validateWorkerAttemptBinding(
	dagRunID, requestedAttemptID string,
	attempt exec.DAGRunAttempt,
	runStatus *exec.DAGRunStatus,
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
