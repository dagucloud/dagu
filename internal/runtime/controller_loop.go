// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/controller"
)

// observationLogLines bounds how much of a step's output is reported back to the
// controller as an observation.
const observationLogLines = 40

// runControllerLoop drives a controller DAG: the model picks one action per
// turn, the runner carries it out, and the outcome is fed back as an
// observation. The loop ends when every task is complete, when an action opens a
// human task, or when a limit is reached.
func (r *Runner) runControllerLoop(ctx context.Context, plan *Plan, progressCh chan *Node) {
	dag := GetDAGContext(ctx).DAG

	ctrlNode := plan.GetNodeByName(core.ControllerStepName)
	if ctrlNode == nil {
		r.setLastError(fmt.Errorf("controller step %q is missing from the plan", core.ControllerStepName))
		return
	}

	state, err := controller.LoadState(ctrlNode.State().ControllerState, ctrlNode.GetChatMessages(), dag)
	if err != nil {
		r.failController(ctx, plan, ctrlNode, err, progressCh)
		return
	}

	ctrlCtx, err := r.setupVariables(ctx, plan, ctrlNode)
	if err != nil {
		r.failController(ctx, plan, ctrlNode, err, progressCh)
		return
	}
	ctrlNode.SetStatus(core.NodeRunning)
	if err := r.prepareNode(ctrlCtx, ctrlNode); err != nil {
		r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
		return
	}
	defer r.teardownPreparedNode(ctrlNode)
	r.report(progressCh, ctrlNode)

	catalog, err := controller.NewCatalog(ctrlCtx, dag)
	if err != nil {
		r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
		return
	}
	ctrlNode.SetToolDefinitions(catalog.Definitions())

	provider, err := NewLLMProvider(ctrlCtx, dag.LLM)
	if err != nil {
		r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
		return
	}
	planner := controller.NewPlanner(provider, dag.LLM, catalog)

	// A run that suspended mid-action resumes here: report what became of the
	// action before asking for the next decision.
	if pending := state.Pending; pending != nil {
		state.Append(observe(plan.GetNodeByName(pending.Step), pending.ToolCallID))
		state.Pending = nil
		r.persistController(ctrlCtx, ctrlNode, state, progressCh)
	}

	maxTurns := dag.ControllerMaxIterations()
	for !state.AllDone() {
		if r.isCanceled() {
			ctrlNode.SetStatus(core.NodeAborted)
			r.report(progressCh, ctrlNode)
			return
		}
		if state.Turns >= maxTurns {
			r.failController(ctrlCtx, plan, ctrlNode, fmt.Errorf(
				"controller reached its %d turn limit with tasks still open: %s",
				maxTurns, strings.Join(state.OpenTaskNames(), ", ")), progressCh)
			return
		}

		decision, err := planner.Next(ctrlCtx, state)
		if err != nil {
			r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
			return
		}

		suspended, err := r.applyDecision(ctrlCtx, plan, state, decision, progressCh)
		if err != nil {
			r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
			return
		}

		r.persistController(ctrlCtx, ctrlNode, state, progressCh)
		if suspended {
			// The action is waiting on a person. The run reports Waiting, the
			// process exits, and this loop resumes once the task is completed.
			ctrlNode.SetStatus(core.NodeSucceeded)
			r.report(progressCh, ctrlNode)
			return
		}
	}

	r.skipUnusedActions(ctx, plan)
	ctrlNode.SetStatus(core.NodeSucceeded)
	r.persistController(ctrlCtx, ctrlNode, state, progressCh)
	logger.Info(ctrlCtx, "Controller completed all tasks", slog.Int("turns", state.Turns))
}

// applyDecision carries out one controller decision and appends the resulting
// observation. It reports whether the run must suspend for human input.
func (r *Runner) applyDecision(
	ctx context.Context,
	plan *Plan,
	state *controller.State,
	decision *controller.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	if decision.Kind != controller.DecideStop {
		// Any turn that used a tool breaks a run of silent replies.
		state.Nudges = 0
	}

	switch decision.Kind {
	case controller.DecideCompleteTask:
		if err := state.CompleteTask(decision.Task, decision.Reason); err != nil {
			state.Append(toolResult(decision.ToolCallID, "Error: "+err.Error()))
			return false, nil
		}
		logger.Info(ctx, "Controller completed a task",
			slog.String("task", decision.Task), slog.String("reason", decision.Reason))
		state.Append(toolResult(decision.ToolCallID, completionAck(state, decision.Task)))
		return false, nil

	case controller.DecideReopenTask:
		if err := state.ReopenTask(decision.Task, decision.Reason); err != nil {
			state.Append(toolResult(decision.ToolCallID, "Error: "+err.Error()))
			return false, nil
		}
		logger.Info(ctx, "Controller reopened a task",
			slog.String("task", decision.Task), slog.String("reason", decision.Reason))
		state.Append(toolResult(decision.ToolCallID, fmt.Sprintf(
			"Task %q reopened. Still open: %s.",
			decision.Task, strings.Join(state.OpenTaskNames(), ", "))))
		return false, nil

	case controller.DecideInvalid:
		state.Append(toolResult(decision.ToolCallID, "Error: "+decision.Problem))
		return false, nil

	case controller.DecideStop:
		return false, r.nudge(ctx, state)

	case controller.DecideRunStep:
		return r.runControllerAction(ctx, plan, state, decision, progressCh)

	default:
		return false, fmt.Errorf("unhandled controller decision %v", decision.Kind)
	}
}

// nudge answers a turn where the model stopped calling tools while tasks were
// still open. One reminder is allowed; a second silent turn ends the run.
func (r *Runner) nudge(ctx context.Context, state *controller.State) error {
	open := strings.Join(state.OpenTaskNames(), ", ")
	if state.Nudges > 0 {
		return fmt.Errorf("controller stopped acting with tasks still open: %s", open)
	}
	state.Nudges++
	logger.Warn(ctx, "Controller answered without acting", slog.String("openTasks", open))
	state.Append(exec.LLMMessage{
		Role: exec.RoleUser,
		Content: fmt.Sprintf(
			"These tasks are still open: %s. Either run an action that advances one of them, "+
				"or call %s for any whose criteria are already satisfied.",
			open, controller.CompleteTaskTool),
	})
	return nil
}

// runControllerAction runs the step the controller chose, resetting the node
// first when the step has already run in this DAG run.
func (r *Runner) runControllerAction(
	ctx context.Context,
	plan *Plan,
	state *controller.State,
	decision *controller.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	node := plan.GetNodeByName(decision.Step)
	if node == nil {
		state.Append(toolResult(decision.ToolCallID,
			fmt.Sprintf("Error: step %q is not part of this workflow", decision.Step)))
		return false, nil
	}

	runs := state.StepRunCount(decision.Step)
	if runs >= core.DefaultControllerMaxStepRuns {
		state.Append(toolResult(decision.ToolCallID, fmt.Sprintf(
			"Error: action %q has already run %d times, which is its limit. Choose a different action.",
			decision.Step, runs)))
		return false, nil
	}

	// Reset against the declared step, not the node's current one, so arguments
	// from an earlier invocation do not leak into this one.
	step := declaredStep(ctx, decision.Step, node)
	if node.State().Status != core.NodeNotStarted {
		// Re-running: clear the previous attempt and mark the node repeated so a
		// child DAG run gets a fresh run ID instead of reusing the earlier one.
		// The step shows the latest attempt; the controller transcript is the
		// record of every attempt.
		node.ClearState(step)
		node.SetRepeated(true)
	}
	if step.SubDAG != nil {
		params := step.SubDAG.Params
		if len(decision.Args) > 0 {
			params = controller.ParamString(decision.Args)
		}
		node.SetSubDAG(core.SubDAG{Name: step.SubDAG.Name, Params: params})
	}
	state.RecordStepRun(decision.Step)

	logger.Info(ctx, "Controller running action", tag.Step(decision.Step))
	node.SetStatus(core.NodeRunning)

	actionCtx, err := r.setupVariables(ctx, plan, node)
	if err != nil {
		node.MarkError(err)
		r.report(progressCh, node)
		state.Append(observe(node, decision.ToolCallID))
		return false, nil
	}

	r.executeControllerAction(actionCtx, plan, node, progressCh)

	// The controller has taken responsibility for the outcome: it is reported as
	// an observation, not as a run-level error. Leaving the error set would make
	// the process exit non-zero for a run the controller went on to complete.
	r.setLastError(nil)

	if node.State().Status == core.NodeWaiting {
		state.Pending = &controller.PendingAction{
			ToolCallID: decision.ToolCallID,
			ToolName:   decision.ToolName,
			Step:       decision.Step,
		}
		return true, nil
	}

	state.Append(observe(node, decision.ToolCallID))
	return false, nil
}

// declaredStep returns the step as written in the DAG, falling back to the
// node's current definition.
func declaredStep(ctx context.Context, name string, node *Node) core.Step {
	dag := GetDAGContext(ctx).DAG
	if dag != nil {
		for _, step := range dag.Steps {
			if step.Name == name {
				return step
			}
		}
	}
	return node.Step()
}

// executeControllerAction runs a single action to completion, mirroring the
// per-node handling of the graph loop.
func (r *Runner) executeControllerAction(ctx context.Context, plan *Plan, node *Node, progressCh chan *Node) {
	defer r.finishNode(node, nil)
	defer r.recoverNodePanic(ctx, node, progressCh)

	if node.Step().HumanTask != nil {
		r.runHumanTask(ctx, plan, node, progressCh)
		return
	}

	if err := r.prepareNode(ctx, node); err != nil {
		r.setLastError(err)
		node.MarkError(err)
		r.report(progressCh, node)
		return
	}
	r.report(progressCh, node)
	r.runNodeExecution(ctx, plan, node, progressCh)
}

// skipUnusedActions marks the steps the controller never chose. Without this the
// run would report Running forever, because unstarted nodes keep the plan from
// looking finished.
func (r *Runner) skipUnusedActions(ctx context.Context, plan *Plan) {
	for _, node := range plan.Nodes() {
		if node.State().Status != core.NodeNotStarted {
			continue
		}
		logger.Debug(ctx, "Controller never ran step", tag.Step(node.Name()))
		node.SetStatus(core.NodeSkipped)
	}
}

// failController ends the run with an error. Steps the controller never chose
// are marked skipped so the plan reads as finished rather than still running.
func (r *Runner) failController(ctx context.Context, plan *Plan, node *Node, err error, progressCh chan *Node) {
	logger.Error(ctx, "Controller failed", tag.Error(err))
	r.setLastError(err)
	node.MarkError(err)
	r.skipUnusedActions(ctx, plan)
	r.report(progressCh, node)
}

// persistController writes the controller's state and transcript to the node so
// they survive suspension and appear in the UI.
func (r *Runner) persistController(ctx context.Context, node *Node, state *controller.State, progressCh chan *Node) {
	raw, err := state.Marshal()
	if err != nil {
		logger.Error(ctx, "Failed to persist controller state", tag.Error(err))
	} else {
		node.SetControllerState(raw)
	}
	node.SetChatMessages(state.Messages())
	r.saveChatMessages(ctx, node)
	r.report(progressCh, node)
}

func (r *Runner) report(progressCh chan *Node, node *Node) {
	if progressCh != nil {
		progressCh <- node
	}
}

// observe renders the outcome of an action as the tool result the controller
// sees on its next turn.
func observe(node *Node, toolCallID string) exec.LLMMessage {
	if node == nil {
		return toolResult(toolCallID, "Error: the step disappeared from the workflow")
	}

	state := node.State()
	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %s\n", state.Status.String())

	if state.Error != nil {
		fmt.Fprintf(&sb, "error: %s\n", state.Error.Error())
	}
	if len(state.HumanTaskInput) > 0 {
		fmt.Fprintf(&sb, "submitted: %s\n", string(state.HumanTaskInput))
		if state.HumanTaskCompletedBy != "" {
			fmt.Fprintf(&sb, "submitted by: %s\n", state.HumanTaskCompletedBy)
		}
	}
	if state.StepOutputsValue != nil && *state.StepOutputsValue != "" {
		fmt.Fprintf(&sb, "outputs: %s\n", *state.StepOutputsValue)
	} else if state.OutputValue != nil && *state.OutputValue != "" {
		fmt.Fprintf(&sb, "output: %s\n", *state.OutputValue)
	}
	if tail := logTail(state.Stdout); tail != "" {
		fmt.Fprintf(&sb, "log:\n%s\n", tail)
	}
	if tail := logTail(state.Stderr); tail != "" {
		fmt.Fprintf(&sb, "stderr:\n%s\n", tail)
	}

	return toolResult(toolCallID, sb.String())
}

func logTail(path string) string {
	if path == "" {
		return ""
	}
	result, err := fileutil.ReadLogLines(path, fileutil.LogReadOptions{Tail: observationLogLines})
	if err != nil || result == nil || len(result.Lines) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(result.Lines, "\n"))
}

func toolResult(toolCallID, content string) exec.LLMMessage {
	return exec.LLMMessage{
		Role:       exec.RoleTool,
		ToolCallID: toolCallID,
		Content:    content,
	}
}

func completionAck(state *controller.State, task string) string {
	open := state.OpenTaskNames()
	if len(open) == 0 {
		return fmt.Sprintf("Task %q marked complete. All tasks are now complete.", task)
	}
	return fmt.Sprintf("Task %q marked complete. Still open: %s.", task, strings.Join(open, ", "))
}
