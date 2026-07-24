// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"os/user"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/humantask"
	"github.com/spf13/cobra"
)

const (
	humanTaskFlagInput      = "input"
	humanTaskFlagInputsJSON = "inputs-json"

	humanTaskSettleTimeout      = 5 * time.Second
	humanTaskSettlePollInterval = 50 * time.Millisecond
)

var (
	humanTaskRunIDFlag = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "DAG-run ID containing the human task",
		required:  true,
	}
	humanTaskStepFlag = commandLineFlag{
		name:     "step",
		usage:    "ID of the human task step to complete",
		required: true,
	}
	humanTaskInputsJSONFlag = commandLineFlag{
		name:  humanTaskFlagInputsJSON,
		usage: "Human task inputs as a JSON object",
	}
)

// HumanTask returns the command for managing human tasks.
func HumanTask() *cobra.Command {
	command := NewCommand(&cobra.Command{
		Use:   "human-task",
		Short: "Manage human tasks",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	command.AddCommand(humanTaskCompleteCommand())
	return command
}

func humanTaskCompleteCommand() *cobra.Command {
	command := NewCommand(&cobra.Command{
		Use:   "complete [flags] <DAG name>",
		Short: "Complete a waiting human task",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{
		humanTaskRunIDFlag,
		humanTaskStepFlag,
		humanTaskInputsJSONFlag,
	}, runHumanTaskComplete)
	command.Flags().StringArray(humanTaskFlagInput, nil, "Human task input in key=value form; repeatable")
	return command
}

type humanTaskCompleteDeps struct {
	now         func() time.Time
	currentUser func() (*user.User, error)
}

func defaultHumanTaskCompleteDeps() humanTaskCompleteDeps {
	return humanTaskCompleteDeps{
		now:         time.Now,
		currentUser: user.Current,
	}
}

func runHumanTaskComplete(ctx *Context, args []string) error {
	return runHumanTaskCompleteWith(ctx, args, defaultHumanTaskCompleteDeps())
}

func runHumanTaskCompleteWith(ctx *Context, args []string, deps humanTaskCompleteDeps) error {
	if ctx.IsRemote() {
		return fmt.Errorf("human-task complete only supports the local context")
	}
	if ctx.DAGRunStore == nil {
		return fmt.Errorf("DAG-run store is not configured")
	}

	dagRunID, err := ctx.StringParam(humanTaskRunIDFlag.name)
	if err != nil {
		return err
	}
	stepID, err := ctx.StringParam(humanTaskStepFlag.name)
	if err != nil {
		return err
	}
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return fmt.Errorf("--step must not be empty")
	}
	input, err := parseHumanTaskCompletionInput(ctx.Command)
	if err != nil {
		return err
	}
	dagName := strings.TrimSpace(args[0])
	if dagName == "" {
		return fmt.Errorf("DAG name must not be empty")
	}
	if err := waitForRemoteHumanTaskCompletionReady(ctx, dagName, dagRunID, stepID); err != nil {
		return err
	}

	service := humantask.Service{
		DAGRunStore: ctx.DAGRunStore,
		QueueStore:  ctx.QueueStore,
		ProcStore:   ctx.ProcStore,
		Now:         deps.now,
	}
	completedBy, completedByID := localHumanTaskSubject(deps)
	result, err := service.Complete(ctx, humantask.CompleteRequest{
		DAGName:       dagName,
		DAGRunID:      dagRunID,
		StepID:        stepID,
		Input:         input,
		CompletedBy:   completedBy,
		CompletedByID: completedByID,
	})
	if err != nil {
		var resumeErr *humantask.ResumeError
		if errors.As(err, &resumeErr) {
			return fmt.Errorf("%w; run the same completion command again to retry", err)
		}
		return err
	}
	if result.RemainingWaitingSteps > 0 {
		if result.AlreadyCompleted {
			_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Human task %s was already completed.\n", stepID)
			return err
		}
		_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Completed human task %s; DAG-run remains waiting.\n", stepID)
		return err
	}
	if !result.Queued {
		if !result.AlreadyCompleted {
			_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Completed human task %s; DAG-run was already queued for resume.\n", stepID)
			return err
		}
		_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Human task %s was already completed.\n", stepID)
		return err
	}
	message := fmt.Sprintf("Completed human task %s", stepID)
	if result.AlreadyCompleted {
		message = fmt.Sprintf("Human task %s was already completed", stepID)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "%s; DAG-run queued for resume.\n", message)
	return err
}

func waitForRemoteHumanTaskCompletionReady(
	ctx *Context,
	dagName string,
	dagRunID string,
	stepID string,
) error {
	attempt, err := ctx.DAGRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dagName, dagRunID))
	if err != nil {
		return fmt.Errorf("failed to find DAG-run %q with run ID %q: %w", dagName, dagRunID, err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to read DAG-run status: %w", err)
	}
	if status == nil {
		return fmt.Errorf("failed to read DAG-run status: status data is nil")
	}
	if status.Status != core.Waiting || status.AttemptID == "" || !exec.IsRemoteWorkerID(status.WorkerID) {
		return nil
	}

	deadline := time.Now().Add(humanTaskSettleTimeout)
	_, err = waitForRemoteHumanTaskAttempt(ctx, attempt, status, stepID, deadline)
	return err
}

func waitForRemoteHumanTaskAttempt(
	ctx *Context,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
	stepID string,
	deadline time.Time,
) (*exec.DAGRunStatus, error) {
	if ctx.DAGRunLeaseStore == nil {
		return nil, fmt.Errorf("DAG-run lease store is not configured")
	}
	claimKey := status.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = exec.AttemptKeyForStatus(status, status.AttemptID)
	}
	if claimKey == "" {
		return nil, fmt.Errorf("distributed DAG-run claim key is missing")
	}

	for {
		_, err := ctx.DAGRunLeaseStore.Get(ctx, claimKey)
		if errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to check whether distributed DAG-run attempt is still finalizing: %w", err)
		}
		if !time.Now().Before(deadline) {
			return nil, humanTaskFinalizingError(status.AttemptID)
		}
		if err := waitForNextHumanTaskPoll(ctx); err != nil {
			return nil, err
		}
	}

	latest, err := reloadHumanTaskStatus(ctx, attempt)
	if err != nil {
		return nil, err
	}
	return waitForHumanTaskAttemptFinalization(ctx, attempt, status.AttemptID, latest, stepID, deadline)
}

func waitForHumanTaskAttemptFinalization(
	ctx *Context,
	attempt exec.DAGRunAttempt,
	attemptID string,
	status *exec.DAGRunStatus,
	stepID string,
	deadline time.Time,
) (*exec.DAGRunStatus, error) {
	for {
		finalizing, err := humanTaskAttemptIsFinalizing(status, attemptID, stepID)
		if err != nil {
			return nil, err
		}
		if !finalizing {
			return status, nil
		}
		if !time.Now().Before(deadline) {
			return nil, humanTaskFinalizingError(attemptID)
		}
		if err := waitForNextHumanTaskPoll(ctx); err != nil {
			return nil, err
		}
		status, err = reloadHumanTaskStatus(ctx, attempt)
		if err != nil {
			return nil, err
		}
	}
}

func humanTaskAttemptIsFinalizing(status *exec.DAGRunStatus, attemptID, stepID string) (bool, error) {
	if status.Status != core.Waiting || status.AttemptID != attemptID || status.FinishedAt != "" {
		return false, nil
	}
	node, err := findHumanTaskNodeByID(status.Nodes, stepID)
	if err != nil {
		return false, err
	}
	return len(node.HumanTaskInput) == 0, nil
}

func findHumanTaskNodeByID(nodes []*exec.Node, stepID string) (*exec.Node, error) {
	var found *exec.Node
	for _, node := range nodes {
		if node == nil || node.Step.ID != stepID {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("human task step ID %q is ambiguous", stepID)
		}
		found = node
	}
	if found == nil {
		return nil, fmt.Errorf("human task step ID %q was not found", stepID)
	}
	if found.Step.HumanTask == nil {
		return nil, fmt.Errorf("step %q is not a human task", stepID)
	}
	return found, nil
}

func reloadHumanTaskStatus(ctx *Context, attempt exec.DAGRunAttempt) (*exec.DAGRunStatus, error) {
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to reload DAG-run status after waiting for the attempt to settle: %w", err)
	}
	if status == nil {
		return nil, fmt.Errorf("failed to reload DAG-run status after waiting for the attempt to settle: status data is nil")
	}
	return status, nil
}

func waitForNextHumanTaskPoll(ctx *Context) error {
	timer := time.NewTimer(humanTaskSettlePollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func humanTaskFinalizingError(attemptID string) error {
	return fmt.Errorf("DAG-run attempt %s is still finalizing; retry human-task completion", attemptID)
}

func localHumanTaskSubject(deps humanTaskCompleteDeps) (name, id string) {
	if deps.currentUser == nil {
		return "", ""
	}
	current, err := deps.currentUser()
	if err != nil || current == nil {
		return "", ""
	}
	name = strings.TrimSpace(current.Username)
	if uid := strings.TrimSpace(current.Uid); uid != "" {
		id = "os:" + uid
	}
	return name, id
}

func parseHumanTaskCompletionInput(command *cobra.Command) (humantask.Input, error) {
	pairs, err := command.Flags().GetStringArray(humanTaskFlagInput)
	if err != nil {
		return humantask.Input{}, fmt.Errorf("failed to read --%s: %w", humanTaskFlagInput, err)
	}
	rawJSON, err := command.Flags().GetString(humanTaskFlagInputsJSON)
	if err != nil {
		return humantask.Input{}, fmt.Errorf("failed to read --%s: %w", humanTaskFlagInputsJSON, err)
	}
	if len(pairs) > 0 && command.Flags().Changed(humanTaskFlagInputsJSON) {
		return humantask.Input{}, fmt.Errorf("--%s and --%s cannot be used together", humanTaskFlagInput, humanTaskFlagInputsJSON)
	}

	if command.Flags().Changed(humanTaskFlagInputsJSON) {
		input, err := humantask.ParseJSONInput([]byte(rawJSON))
		if err != nil {
			return humantask.Input{}, fmt.Errorf("invalid --%s JSON value: %w", humanTaskFlagInputsJSON, err)
		}
		return input, nil
	}
	return parseHumanTaskInputPairs(pairs)
}

func parseHumanTaskInputPairs(pairs []string) (humantask.Input, error) {
	values := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return humantask.Input{}, fmt.Errorf("--%s must use key=value form", humanTaskFlagInput)
		}
		if _, exists := values[name]; exists {
			return humantask.Input{}, fmt.Errorf("--%s contains duplicate key %q", humanTaskFlagInput, name)
		}
		values[name] = value
	}
	return humantask.Input{Values: values, CoerceStrings: len(pairs) > 0}, nil
}
