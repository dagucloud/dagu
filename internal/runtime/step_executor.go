// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

var errNodeExecutionAborted = errors.New("node execution aborted before start")

// StepExecutor owns a single step execution attempt.
//
// The scheduler-facing Runner decides when a node should run. StepExecutor owns
// the executor-specific protocol for setting up one run, passing executor
// context, collecting executor side channels, and capturing outputs.
type StepExecutor struct{}

// NewStepExecutor creates a StepExecutor.
func NewStepExecutor() *StepExecutor {
	return &StepExecutor{}
}

// Execute runs one node execution attempt and stores executor side effects on
// the node. Runner owns scheduling, retries, repeats, and final DAG-state
// decisions; StepExecutor only preserves executor-provided status overrides.
func (e *StepExecutor) Execute(ctx context.Context, node *Node, onSetup ...func()) error {
	ctx, cancel, stepTimeout := node.setupContextWithTimeout(ctx)
	defer cancel()

	if err := preRunAbortErr(ctx, node); err != nil {
		node.SetError(err)
		return err
	}

	ctx, cmd, err := node.setupExecutor(ctx)
	if err != nil {
		err = wrapStepSetupError(err)
		node.SetError(err)
		return err
	}

	defer func() {
		if closeErr := executor.CloseExecutor(cmd); closeErr != nil {
			logger.Warn(ctx, "Failed to close executor",
				tag.Step(node.Name()),
				tag.Error(closeErr))
		}
	}()

	// Notify after executor setup so SubRuns (set for subDAG steps) are
	// persisted to storage before the executor starts running.
	for _, fn := range onSetup {
		if fn != nil {
			fn()
		}
	}

	if err := preRunAbortErr(ctx, node); err != nil {
		node.SetError(err)
		return err
	}

	e.setupExecutorSideChannels(cmd, node)

	flusher := node.startOutputFlusher()
	defer func() {
		node.stopOutputFlusher(flusher)
	}()

	exitCode, err := node.runCommand(ctx, cmd, stepTimeout)
	node.SetError(err)
	node.SetExitCode(exitCode)

	if err := e.captureExecutorSideChannels(ctx, cmd, node); err != nil {
		return err
	}

	if err == nil {
		if err := node.captureDeclaredStepOutputs(ctx); err != nil {
			node.SetError(err)
			return err
		}
	}

	if err := node.captureOutput(ctx); err != nil {
		return err
	}

	statusErr := node.determineNodeStatus(cmd)
	if execErr := node.Error(); execErr != nil {
		return execErr
	}
	return statusErr
}

func preRunAbortErr(ctx context.Context, node *Node) error {
	if node.Status() == core.NodeAborted {
		return errNodeExecutionAborted
	}
	return ctx.Err()
}

func wrapStepSetupError(err error) error {
	return &stepSetupError{err: err}
}

type stepSetupError struct {
	err error
}

func (e *stepSetupError) Error() string {
	return "failed to set up step: " + e.err.Error()
}

func (e *stepSetupError) Unwrap() error {
	return e.err
}

func (e *StepExecutor) setupExecutorSideChannels(cmd executor.Executor, node *Node) {
	if chatHandler, ok := cmd.(executor.ChatMessageHandler); ok {
		if messages := node.GetChatMessages(); len(messages) > 0 {
			chatHandler.SetContext(messages)
		}
	}

	state := node.State()
	if state.ApprovalIteration <= 0 {
		return
	}

	if pbHandler, ok := cmd.(executor.PushBackAware); ok {
		pbHandler.SetPushBackContext(state.PushBackInputs, state.ApprovalIteration)
	}
	if pbHandler, ok := cmd.(executor.PushBackPreviousStdoutAware); ok {
		pbHandler.SetPushBackPreviousStdout(state.PushBackPreviousStdout)
	}
}

func (e *StepExecutor) captureExecutorSideChannels(ctx context.Context, cmd executor.Executor, node *Node) error {
	if chatHandler, ok := cmd.(executor.ChatMessageHandler); ok {
		node.SetChatMessages(chatHandler.GetMessages())
	}

	if subRunProvider, ok := cmd.(executor.SubRunProvider); ok {
		if node.IsRepeated() && len(node.State().SubRuns) > 0 {
			node.AddSubRunsRepeated(node.State().SubRuns...)
		}

		subRuns := subRunProvider.GetSubRuns()
		runtimeSubRuns := make([]SubDAGRun, len(subRuns))
		for i, sr := range subRuns {
			runtimeSubRuns[i] = SubDAGRun(sr)
		}
		node.SetSubRuns(runtimeSubRuns)
	}

	if toolDefProvider, ok := cmd.(executor.ToolDefinitionProvider); ok {
		toolDefs := toolDefProvider.GetToolDefinitions()
		node.SetToolDefinitions(toolDefs)
	}

	if outputsProvider, ok := cmd.(executor.OutputsProvider); ok {
		outputs := outputsProvider.GetOutputs()
		if len(outputs) == 0 {
			node.clearOutputsValue()
			return nil
		}
		value, err := serializeOutputsValue(ctx, outputs)
		if err != nil {
			return err
		}
		node.setOutputsValue(value)
	}

	return nil
}
