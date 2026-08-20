// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"os/user"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/spf13/cobra"
)

const (
	humanTaskFlagInput      = "input"
	humanTaskFlagInputsJSON = "inputs-json"
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
	if ctx.Persistence.DAGRunRepository == nil {
		return fmt.Errorf("DAG-run repository is not configured")
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

	service := humantask.Service{
		DAGRunRepository: ctx.Persistence.DAGRunRepository,
		QueueStore:       ctx.Persistence.QueueStore,
		ProcRepository:   ctx.Persistence.ProcRepository,
		Now:              deps.now,
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
		if _, ok := errors.AsType[*humantask.ResumeError](err); ok {
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
