// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/dagrun/humantask"
	"github.com/dagucloud/dagu/internal/dagwarning"
	"github.com/dagucloud/dagu/internal/launcher"
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
	now    func() time.Time
	resume func(*Context, *core.DAG, *exec.DAGRunStatus) error
}

func defaultHumanTaskCompleteDeps() humanTaskCompleteDeps {
	return humanTaskCompleteDeps{
		now: time.Now,
		resume: func(ctx *Context, dag *core.DAG, status *exec.DAGRunStatus) error {
			return launchHumanTaskRetry(ctx, dag, status)
		},
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
	dagName, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}

	service := humantask.Service{
		DAGRunStore: ctx.DAGRunStore,
		QueueStore:  ctx.QueueStore,
		ProcStore:   ctx.ProcStore,
		Now:         deps.now,
		LocalResumer: humantask.LocalResumeFunc(func(
			resumeCtx context.Context,
			dag *core.DAG,
			status *exec.DAGRunStatus,
		) error {
			if deps.resume == nil {
				return fmt.Errorf("local human-task resumer is not configured")
			}
			localCtx := *ctx
			localCtx.Context = resumeCtx
			return deps.resume(&localCtx, dag, status)
		}),
	}
	result, err := service.Complete(ctx, humantask.CompleteRequest{
		DAGName:  dagName,
		DAGRunID: dagRunID,
		StepID:   stepID,
		Input:    input,
	})
	if err != nil {
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
	if !result.ResumeRequested {
		_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Human task %s was already completed.\n", stepID)
		return err
	}
	message := fmt.Sprintf("Completed human task %s", stepID)
	if result.AlreadyCompleted {
		message = fmt.Sprintf("Human task %s was already completed", stepID)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "%s; DAG-run resume requested.\n", message)
	return err
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
	input, err := humantask.ParseInputPairs(pairs)
	if err != nil {
		message := strings.Replace(err.Error(), "input ", "--"+humanTaskFlagInput+" ", 1)
		return humantask.Input{}, fmt.Errorf("%s", message)
	}
	return input, nil
}

func launchHumanTaskRetry(ctx *Context, dag *core.DAG, status *exec.DAGRunStatus) error {
	if ctx.Config == nil {
		return fmt.Errorf("configuration is not available")
	}
	result, err := spec.ResolveEnvWithWarnings(ctx, dag, status.ParamsList, spec.ResolveEnvOptions{
		BaseConfig: ctx.Config.Paths.BaseConfig,
	})
	if err != nil {
		return fmt.Errorf("prepare retry environment: %w", err)
	}
	dagwarning.Log(ctx, result.BuildWarnings)
	prepared := dag.Clone()
	prepared.Env = result.Env

	retrySpec := humanTaskRetrySpec(ctx, prepared, status.DAGRunID, status.HumanTaskResume.ClaimToken)
	if err := launcher.Start(ctx, retrySpec); err != nil {
		return fmt.Errorf("start retry subprocess: %w", err)
	}
	return nil
}

func humanTaskRetrySpec(ctx *Context, dag *core.DAG, dagRunID, claimToken string) launcher.CmdSpec {
	builder := launcher.NewSubCmdBuilder(ctx.Config)
	retrySpec := builder.HumanTaskRetry(dag, dagRunID, claimToken)
	if daguHome := explicitHumanTaskDAGUHome(ctx); daguHome != "" {
		target := retrySpec.Args[len(retrySpec.Args)-1]
		retrySpec.Args = append(retrySpec.Args[:len(retrySpec.Args)-1], "--dagu-home="+daguHome, target)
	}
	return retrySpec
}

func explicitHumanTaskDAGUHome(ctx *Context) string {
	if ctx == nil || ctx.Command == nil || !ctx.Command.Flags().Changed(daguHomeFlag.name) {
		return ""
	}
	if ctx.Config != nil {
		for _, entry := range ctx.Config.Core.BaseEnv.AsSlice() {
			key, value, found := strings.Cut(entry, "=")
			if found && key == "DAGU_HOME" {
				return value
			}
		}
	}
	value, err := ctx.Command.Flags().GetString(daguHomeFlag.name)
	if err != nil {
		return ""
	}
	return fileutil.ResolvePathOrBlank(value)
}
