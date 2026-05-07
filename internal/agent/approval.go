// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

const approvalTimeout = 5 * time.Minute

func requestCommandApproval(
	parentCtx context.Context,
	emit EmitUserPromptFunc,
	wait WaitUserResponseFunc,
	cmd string,
	workingDir string,
	question string,
) (bool, error) {
	return requestCommandApprovalWithTimeout(parentCtx, emit, wait, cmd, workingDir, question, approvalTimeout)
}

func requestCommandApprovalWithTimeout(
	parentCtx context.Context,
	emit EmitUserPromptFunc,
	wait WaitUserResponseFunc,
	cmd string,
	workingDir string,
	question string,
	timeout time.Duration,
) (bool, error) {
	if emit == nil || wait == nil {
		return false, errors.New("approval channel unavailable")
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if timeout <= 0 {
		timeout = approvalTimeout
	}

	promptID := uuid.Must(uuid.NewV7()).String()
	emit(UserPrompt{
		PromptID:   promptID,
		PromptType: PromptTypeCommandApproval,
		Question:   question,
		Command:    cmd,
		WorkingDir: workingDir,
		Options: []UserPromptOption{
			{ID: "approve", Label: "Approve"},
			{ID: "reject", Label: "Reject"},
		},
	})

	timeoutCtx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	resp, err := wait(timeoutCtx, promptID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return false, fmt.Errorf("approval timed out after %v", timeout)
		}
		return false, err
	}
	if resp.Cancelled {
		return false, nil
	}
	return slices.Contains(resp.SelectedOptionIDs, "approve"), nil
}
