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
	humanTaskFlagInput             = "input"
	humanTaskFlagInputsJSON        = "inputs-json"
	humanTaskFlagExpectedIteration = "expected-iteration"
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
	humanTaskPushBackStepFlag = commandLineFlag{
		name:     "step",
		usage:    "ID of the human task step to push back",
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
	command.AddCommand(humanTaskPushBackCommand())
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

func humanTaskPushBackCommand() *cobra.Command {
	command := NewCommand(&cobra.Command{
		Use:   "push-back [flags] <DAG name>",
		Short: "Send a waiting human task back to its rewind target with feedback",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{
		humanTaskRunIDFlag,
		humanTaskPushBackStepFlag,
		humanTaskInputsJSONFlag,
	}, runHumanTaskPushBack)
	command.Flags().StringArray(humanTaskFlagInput, nil, "Push-back feedback in key=value form; repeatable")
	command.Flags().Int(humanTaskFlagExpectedIteration, 0, "Fail unless the task is at this push-back iteration")
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

// humanTaskCommandArgs holds the arguments shared by human-task subcommands.
type humanTaskCommandArgs struct {
	dagName  string
	dagRunID string
	stepID   string
	input    humantask.Input
}

func readHumanTaskCommandArgs(ctx *Context, args []string, subcommand string) (humanTaskCommandArgs, error) {
	if ctx.IsRemote() {
		return humanTaskCommandArgs{}, fmt.Errorf("human-task %s only supports the local context", subcommand)
	}
	if ctx.Persistence.DAGRunRepository == nil {
		return humanTaskCommandArgs{}, fmt.Errorf("DAG-run repository is not configured")
	}

	dagRunID, err := ctx.StringParam(humanTaskRunIDFlag.name)
	if err != nil {
		return humanTaskCommandArgs{}, err
	}
	stepID, err := ctx.StringParam(humanTaskStepFlag.name)
	if err != nil {
		return humanTaskCommandArgs{}, err
	}
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return humanTaskCommandArgs{}, fmt.Errorf("--step must not be empty")
	}
	input, err := parseHumanTaskCompletionInput(ctx.Command)
	if err != nil {
		return humanTaskCommandArgs{}, err
	}
	dagName := strings.TrimSpace(args[0])
	if dagName == "" {
		return humanTaskCommandArgs{}, fmt.Errorf("DAG name must not be empty")
	}
	return humanTaskCommandArgs{dagName: dagName, dagRunID: dagRunID, stepID: stepID, input: input}, nil
}

func newLocalHumanTaskService(ctx *Context, deps humanTaskCompleteDeps) humantask.Service {
	return humantask.Service{
		DAGRunRepository: ctx.Persistence.DAGRunRepository,
		QueueStore:       ctx.Persistence.QueueStore,
		ProcRepository:   ctx.Persistence.ProcRepository,
		Now:              deps.now,
	}
}

func runHumanTaskCompleteWith(ctx *Context, args []string, deps humanTaskCompleteDeps) error {
	command, err := readHumanTaskCommandArgs(ctx, args, "complete")
	if err != nil {
		return err
	}
	stepID := command.stepID

	service := newLocalHumanTaskService(ctx, deps)
	completedBy, completedByID := localHumanTaskSubject(deps)
	result, err := service.Complete(ctx, humantask.CompleteRequest{
		DAGName:       command.dagName,
		DAGRunID:      command.dagRunID,
		StepID:        stepID,
		Input:         command.input,
		CompletedBy:   completedBy,
		CompletedByID: completedByID,
	})
	if err != nil {
		if _, ok := errors.AsType[*humantask.ResumeError](err); ok {
			return fmt.Errorf("%w; run the same completion command again to retry", err)
		}
		return err
	}
	if !result.ResumeRequested {
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

func runHumanTaskPushBack(ctx *Context, args []string) error {
	return runHumanTaskPushBackWith(ctx, args, defaultHumanTaskCompleteDeps())
}

func runHumanTaskPushBackWith(ctx *Context, args []string, deps humanTaskCompleteDeps) error {
	command, err := readHumanTaskCommandArgs(ctx, args, "push-back")
	if err != nil {
		return err
	}
	expectedIteration, err := parseHumanTaskExpectedIteration(ctx.Command)
	if err != nil {
		return err
	}

	service := newLocalHumanTaskService(ctx, deps)
	by, byID := localHumanTaskSubject(deps)
	result, err := service.PushBack(ctx, humantask.PushBackRequest{
		DAGName:           command.dagName,
		DAGRunID:          command.dagRunID,
		StepID:            command.stepID,
		Input:             command.input,
		ExpectedIteration: expectedIteration,
		By:                by,
		ByID:              byID,
	})
	if err != nil {
		if _, ok := errors.AsType[*humantask.PushBackQueueError](err); ok {
			return fmt.Errorf("%w; run the same command again to retry", err)
		}
		return err
	}

	out := ctx.Command.OutOrStdout()
	if result.AlreadyPushedBack {
		message := fmt.Sprintf("Human task %s was already pushed back to %s", command.stepID, result.RewindTo)
		if result.Queued {
			_, err = fmt.Fprintf(out, "%s; DAG-run queued for resume.\n", message)
			return err
		}
		_, err = fmt.Fprintf(out, "%s.\n", message)
		return err
	}
	message := fmt.Sprintf("Pushed back human task %s to %s", command.stepID, result.RewindTo)
	switch {
	case !result.ResumeRequested:
		_, err = fmt.Fprintf(out, "%s; DAG-run remains waiting.\n", message)
	case !result.Queued:
		_, err = fmt.Fprintf(out, "%s; DAG-run was already queued for resume.\n", message)
	default:
		_, err = fmt.Fprintf(out, "%s; DAG-run queued for resume.\n", message)
	}
	return err
}

func parseHumanTaskExpectedIteration(command *cobra.Command) (*int, error) {
	if !command.Flags().Changed(humanTaskFlagExpectedIteration) {
		return nil, nil
	}
	iteration, err := command.Flags().GetInt(humanTaskFlagExpectedIteration)
	if err != nil {
		return nil, fmt.Errorf("failed to read --%s: %w", humanTaskFlagExpectedIteration, err)
	}
	if iteration < 0 {
		return nil, fmt.Errorf("--%s must be a non-negative integer", humanTaskFlagExpectedIteration)
	}
	return &iteration, nil
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
