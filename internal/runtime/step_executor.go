// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
)

var errNodeExecutionAborted = errors.New("node execution aborted before start")

// liveModelUsageInterval is how often, at most, a step that reports no
// progress of its own has its model usage written while it runs. Tests
// shorten it.
var liveModelUsageInterval = 5 * time.Second

// ExecutorOverride supplies executors that run in place of step actions.
type ExecutorOverride interface {
	// Executor returns the executor that runs in place of the step's action
	// for one execution, or false when the step runs its own action. It
	// answers the same way for every execution of a step, since a step's
	// state, such as its child runs, assumes one kind of action. The
	// step's fields are resolved as they are for its own action. On a step
	// that starts child runs, such as dag.run, parallel, or a step with
	// nodes, the executor replaces every child run, so none starts or is
	// recorded.
	Executor(ctx context.Context, step ir.Step) (executor.Executor, bool)
}

// StepExecutor owns a single step execution attempt.
//
// The scheduler-facing Runner decides when a node should run. StepExecutor owns
// the executor-specific protocol for setting up one run, passing executor
// context, collecting executor side channels, and capturing outputs.
type StepExecutor struct {
	// override supplies executors in place of step actions. Nil builds each
	// step's own.
	override ExecutorOverride
}

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

func (e *StepExecutor) execute(ctx context.Context, node *Node, onSetup []func(), onProgress func()) (retErr error) {
	attemptStarted := time.Now()
	node.SetStatusDetails(nil)
	ctx, cancel, stepTimeout := node.setupContextWithTimeout(ctx)
	defer cancel()

	// Every model call the step makes adds to its meter, which goes on from
	// what the step's earlier attempts used. The executor is created with
	// this context, since some keep the context they are made with.
	meter := modelusage.NewMeter(node.ModelUsage())
	ctx = modelusage.WithMeter(ctx, meter)
	defer func() { node.SetModelUsage(meter.Snapshot()) }()

	if err := preRunAbortErr(ctx, node); err != nil {
		return recordPreRunAbort(ctx, node, err, stepTimeout, attemptStarted)
	}

	leaveGate, err := enterStepGate(ctx, node)
	if err != nil {
		if ctx.Err() != nil {
			// A timeout or a stop ends the wait as it ends an attempt that
			// has not started.
			return recordPreRunAbort(ctx, node, err, stepTimeout, attemptStarted)
		}
		node.SetError(err)
		node.SetExitCode(1)
		return err
	}
	defer leaveGate()
	if untrustedRun(ctx) && !node.Step().IsUntrusted() {
		ctx = untrusted.WithGitSafetyContext(ctx)
		var removeHome func()
		if ctx, removeHome, err = withTrustedHome(ctx, node); err != nil {
			node.SetError(err)
			node.SetExitCode(1)
			return err
		}
		defer removeHome()
	}

	ctx, untrustedStep, err := beginUntrustedStep(ctx, node)
	if err != nil {
		node.SetError(err)
		node.SetExitCode(1)
		return err
	}
	// Registered before End, so that End copies the output of an untrusted
	// step into the file before the file is removed.
	defer func() {
		if cleanupErr := node.cleanupStepOutputFile(); cleanupErr != nil {
			logger.Warn(ctx, "Failed to remove step output file",
				tag.Step(node.Name()),
				tag.Error(cleanupErr))
		}
	}()
	untrustedEnded := false
	if untrustedStep != nil {
		// End runs once; this call covers an early return and a panic.
		defer func() {
			if untrustedEnded {
				return
			}
			if endErr := untrustedStep.End(ctx); endErr != nil {
				retErr = errors.Join(retErr, endErr)
				node.SetError(retErr)
			}
		}()
	}

	ctx, cmd, err := node.setupExecutor(ctx, e.override)
	if errors.Is(err, errStepNotListed) {
		node.SetStatus(ir.NodeSkipped)
		node.SetSkippedByNodeList(true)
		GetDAGContext(ctx).NodeList.MarkSkipped()
		logger.Info(ctx, "Step skipped: the run's node list names none of its nodes", tag.Step(node.Name()))
		return nil
	}
	if err != nil {
		// Setup can fail on starred text, which the masked read explains.
		err = preferMaskedRead(ctx, node, wrapStepSetupError(err))
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
	if err := maskedReadError(ctx, node); err != nil {
		node.SetError(err)
		return err
	}
	cmnvalue.RejectMaskedReads(ctx)

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
			node.SetModelUsage(meter.Snapshot())
			if onProgress != nil {
				onProgress()
			}
		})
	} else if onProgress != nil {
		// An executor that reports no progress of its own, such as a chat
		// step, has its model usage reported while it runs.
		flusher := meter.StartFlusher(liveModelUsageInterval, func(usage *ir.StepModelUsage) {
			node.SetModelUsage(usage)
			onProgress()
		})
		defer flusher.Stop()
	}

	if err := checkUntrustedExecutor(ctx, node, cmd); err != nil {
		node.SetError(err)
		node.SetExitCode(1)
		return err
	}

	flusher := node.startOutputFlusher()
	defer func() {
		node.stopOutputFlusher(flusher)
	}()

	exitCode, err := node.runCommand(ctx, cmd, stepTimeout)
	if untrustedStep != nil {
		// The end of an untrusted step comes before Dagu reads its outputs.
		untrustedEnded = true
		if endErr := untrustedStep.End(ctx); endErr != nil {
			err = errors.Join(err, endErr)
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}
	node.SetError(err)
	node.SetExitCode(exitCode)

	e.captureExecutorSideChannels(cmd, node)
	if outputErr := captureStepOutputs(ctx, cmd, node, err == nil); outputErr != nil {
		// The attempt fails, so it publishes nothing, including the outputs
		// its action published before it failed.
		node.clearOutputsValue()
		node.clearStepOutputsValue()
		if errors.Is(err, untrusted.ErrRunAborted) {
			// The run still stops.
			return errors.Join(err, outputErr)
		}
		return outputErr
	}

	statusErr := node.determineNodeStatus(cmd)
	if execErr := node.Error(); execErr != nil {
		return execErr
	}
	return statusErr
}

// captureStepOutputs publishes the step's named outputs once its executor has
// returned, and returns the error that fails the attempt while doing so.
// executorSucceeded reports that the executor returned no error.
func captureStepOutputs(ctx context.Context, cmd executor.Executor, node *Node, executorSucceeded bool) error {
	declaredOutputsValue, hasDeclaredOutputs, err := captureExecutorOutputs(ctx, cmd, node)
	if err != nil {
		if executorSucceeded {
			node.SetError(err)
		}
		return err
	}

	// An executor that publishes every named output of its step replaces the
	// output file and the step's output fields as their source.
	publisher, publishesOutputs := cmd.(executor.NamedOutputsPublisher)
	if executorSucceeded && !publishesOutputs {
		if err := node.captureDeclaredStepOutputs(ctx); err != nil {
			node.SetError(err)
			return err
		}
		if hasDeclaredOutputs {
			node.setStepOutputsValue(declaredOutputsValue)
		}
	}

	// An output field is resolved after the run and can read a masked value.
	if publishesOutputs {
		err = node.capturePublishedOutputs(ctx, publisher.NamedOutputs())
	} else {
		err = node.captureOutput(ctx)
	}
	if err != nil {
		return preferMaskedRead(ctx, node, err)
	}
	// An executor can resolve a field while it runs.
	if err := maskedReadError(ctx, node); err != nil {
		node.SetError(err)
		return err
	}
	return nil
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
	if aware, ok := cmd.(executor.SecretMaskerAware); ok {
		aware.SetSecretMasker(node.outputs.addSecrets)
	}

	// Executors turn feedback into the request they send, in place of or on top
	// of the step's own prompt. Only a step that ran has work for it to revise; a
	// step first running after a push-back keeps its prompt, and can still read
	// the feedback from its environment.
	state := node.State()
	if state.ApprovalIteration <= 0 || state.PushBackPreviousStdout == "" {
		return
	}

	if pbHandler, ok := cmd.(executor.PushBackAware); ok {
		pbHandler.SetPushBackContext(visiblePushBackInputs(node.Step(), state), state.ApprovalIteration)
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

func (e *StepExecutor) captureExecutorSideChannels(cmd executor.Executor, node *Node) {
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
}

// captureExecutorOutputs publishes the outputs the executor reports. It
// returns their payload and whether the executor declares them as the step's
// named outputs, which replace those of the output file.
func captureExecutorOutputs(ctx context.Context, cmd executor.Executor, node *Node) (string, bool, error) {
	// An executor publishes through either channel, never both: this branch
	// returns before the OutputsProvider branch below can run.
	if valueProvider, ok := cmd.(executor.OutputsValueProvider); ok {
		payload := valueProvider.GetOutputsValue()
		if payload == nil {
			node.clearOutputsValue()
			return "", false, nil
		}
		serialized, err := serializeOutputsValue(ctx, payload)
		if err != nil {
			// The step itself did its work, so an unpublishable payload
			// leaves the channel empty rather than failing the step.
			logger.Warn(ctx, "Dropped step outputs payload",
				tag.Step(node.Name()),
				tag.Error(err))
			node.clearOutputsValue()
			return "", false, nil
		}
		node.setOutputsValue(serialized)
		return "", false, nil
	}

	if outputsProvider, ok := cmd.(executor.OutputsProvider); ok {
		outputs := outputsProvider.GetOutputs()
		hasDeclaredOutputs := false
		if declared, ok := cmd.(executor.DeclaredOutputsProvider); ok {
			hasDeclaredOutputs = declared.PublishesDeclaredOutputs()
		}
		// An executor that published nothing declares nothing, so the step
		// output channels stay empty rather than holding a null payload. A
		// failed attempt publishes nothing, even outputs a failed executor
		// reports, such as those of a failed child run.
		if len(outputs) == 0 || node.Error() != nil {
			node.clearOutputsValue()
			return "", false, nil
		}

		declaredOutputsValue, err := serializeOutputsValue(ctx, outputs)
		if err != nil {
			return "", false, err
		}
		node.setOutputsValue(declaredOutputsValue)
		return declaredOutputsValue, hasDeclaredOutputs, nil
	}

	return "", false, nil
}
