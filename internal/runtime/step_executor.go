// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
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
	return e.execute(ctx, node, onSetup, nil)
}

// ExecuteWithProgress runs a step and reports live executor side-channel updates.
func (e *StepExecutor) ExecuteWithProgress(ctx context.Context, node *Node, onSetup, onProgress func()) error {
	var setup []func()
	if onSetup != nil {
		setup = append(setup, onSetup)
	}
	return e.execute(ctx, node, setup, onProgress)
}

func (e *StepExecutor) execute(ctx context.Context, node *Node, onSetup []func(), onProgress func()) error {
	attemptStarted := time.Now()
	node.SetStatusDetails(nil)
	ctx, cancel, stepTimeout := node.setupContextWithTimeout(ctx)
	defer cancel()

	if err := preRunAbortErr(ctx, node); err != nil {
		return recordPreRunAbort(ctx, node, err, stepTimeout, attemptStarted)
	}

	ctx, cmd, err := node.setupExecutor(ctx)
	defer func() {
		if cleanupErr := node.cleanupStepOutputFile(); cleanupErr != nil {
			logger.Warn(ctx, "Failed to remove step output file",
				tag.Step(node.Name()),
				tag.Error(cleanupErr))
		}
	}()
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
		return recordPreRunAbort(ctx, node, err, stepTimeout, attemptStarted)
	}

	e.setupExecutorSideChannels(cmd, node)
	if progressAware, ok := cmd.(executor.ProgressCallbackAware); ok {
		progressAware.SetProgressCallback(func() {
			e.captureLiveExecutorSideChannels(cmd, node)
			if onProgress != nil {
				onProgress()
			}
		})
	}

	flusher := node.startOutputFlusher()
	defer func() {
		node.stopOutputFlusher(flusher)
	}()

	exitCode, err := node.runCommand(ctx, cmd, stepTimeout)
	node.SetError(err)
	node.SetExitCode(exitCode)

	declaredOutputsValue, hasDeclaredOutputs, captureErr := e.captureExecutorSideChannels(ctx, cmd, node)
	if captureErr != nil {
		if err == nil {
			node.SetError(captureErr)
		}
		return captureErr
	}

	if err == nil {
		if err := node.captureDeclaredStepOutputs(ctx); err != nil {
			node.SetError(err)
			return err
		}
		if hasDeclaredOutputs {
			node.setStepOutputsValue(declaredOutputsValue)
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
	if node.Status() == ir.NodeAborted {
		return errNodeExecutionAborted
	}
	return ctx.Err()
}

func recordPreRunAbort(ctx context.Context, node *Node, err error, stepTimeout time.Duration, attemptStarted time.Time) error {
	if stepTimeout > 0 && errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		exitCode, timeoutErr := node.handleTimeout(ctx, node.Step(), stepTimeout, time.Since(attemptStarted))
		node.SetExitCode(exitCode)
		return timeoutErr
	}

	node.SetError(err)
	return err
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
	if agentHandler, ok := cmd.(executor.AgentSessionHandler); ok {
		agentHandler.SetAgentSession(node.GetAgentSession())
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

func (e *StepExecutor) captureLiveExecutorSideChannels(cmd executor.Executor, node *Node) {
	if chatHandler, ok := cmd.(executor.ChatMessageHandler); ok {
		node.SetChatMessages(chatHandler.GetMessages())
	}
	if agentHandler, ok := cmd.(executor.AgentSessionHandler); ok {
		node.SetAgentSession(agentHandler.GetAgentSession())
	}
}

func (e *StepExecutor) captureExecutorSideChannels(
	ctx context.Context,
	cmd executor.Executor,
	node *Node,
) (string, bool, error) {
	if statusDetailsProvider, ok := cmd.(executor.StatusDetailsProvider); ok {
		node.SetStatusDetails(statusDetailsProvider.GetStatusDetails())
	}

	if chatHandler, ok := cmd.(executor.ChatMessageHandler); ok {
		node.SetChatMessages(chatHandler.GetMessages())
	}

	if agentHandler, ok := cmd.(executor.AgentSessionHandler); ok {
		node.SetAgentSession(agentHandler.GetAgentSession())
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
		hasDeclaredOutputs := false
		if declared, ok := cmd.(executor.DeclaredOutputsProvider); ok {
			hasDeclaredOutputs = declared.PublishesDeclaredOutputs()
		}
		if len(outputs) == 0 {
			node.clearOutputsValue()
			if !hasDeclaredOutputs {
				return "", false, nil
			}
		}

		declaredOutputsValue, err := serializeOutputsValue(ctx, outputs)
		if err != nil {
			return "", false, err
		}
		if len(outputs) > 0 {
			node.setOutputsValue(declaredOutputsValue)
		}
		return declaredOutputsValue, hasDeclaredOutputs, nil
	}

	return "", false, nil
}
