// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
)

// observationLogLines bounds how much of a step's output is reported back to the
// agent as an observation.
const observationLogLines = 40

// runAgentLoop drives an agent DAG: the model picks actions, the runner carries
// them out, and their outcomes are fed back as observations. The loop ends when
// every task is complete, when an action opens a human task, or when a limit is
// reached.
func (r *Runner) runAgentLoop(ctx context.Context, plan *Plan, progressCh chan *Node) {
	dag := GetDAGContext(ctx).DAG

	agentNode := plan.GetNodeByName(ir.AgentStepName)
	if agentNode == nil {
		r.setLastError(fmt.Errorf("agent step %q is missing from the plan", ir.AgentStepName))
		return
	}

	state, err := agentloop.LoadState(agentNode.State().AgentState, agentNode.GetChatMessages(), dag)
	if err != nil {
		r.failAgent(ctx, plan, agentNode, err, progressCh)
		return
	}

	agentCtx, err := r.setupVariables(ctx, plan, agentNode)
	if err != nil {
		r.failAgent(ctx, plan, agentNode, err, progressCh)
		return
	}
	agentNode.SetStatus(ir.NodeRunning)
	if err := r.prepareNode(agentCtx, agentNode); err != nil {
		r.failAgent(agentCtx, plan, agentNode, err, progressCh)
		return
	}
	defer r.teardownPreparedNode(agentNode)
	r.report(progressCh, agentNode)

	catalog, err := agentloop.NewCatalog(agentCtx, dag)
	if err != nil {
		r.failAgent(agentCtx, plan, agentNode, err, progressCh)
		return
	}
	agentNode.SetToolDefinitions(catalog.Definitions())

	// Resolve the models the same way a chat step does, so array-form llm.model
	// and value references work here too.
	models, err := ResolveModels(agentCtx, dag.LLM.GetModels())
	if err != nil {
		r.failAgent(agentCtx, plan, agentNode, err, progressCh)
		return
	}
	// The system prompt and the task descriptions are author-written prompt
	// text, so they take variables the same way any other workflow field does.
	system, err := resolveRuntimeString(
		agentCtx, dag.LLM.System, cmnvalue.WorkflowField("llm.system"))
	if err != nil {
		r.failAgent(agentCtx, plan, agentNode, fmt.Errorf(
			"failed to evaluate llm.system: %w", err), progressCh)
		return
	}
	if err := resolveTaskDescriptions(agentCtx, state); err != nil {
		r.failAgent(agentCtx, plan, agentNode, err, progressCh)
		return
	}
	planner := newAgentModelPlanner(
		agentCtx, dag.LLM, models, catalog, system,
		func(msgs []ir.LLMMessage) []ir.LLMMessage {
			return MaskSecretsForProvider(agentCtx, msgs)
		})

	// A run that suspended mid-batch resumes here. No result from the batch is
	// reported until every waiting member has finished.
	if pending := state.PendingBatch(); len(pending) > 0 {
		for _, action := range pending {
			node := plan.GetNodeByName(action.Step)
			if node != nil && node.State().Status == ir.NodeWaiting {
				r.persistAgent(agentCtx, agentNode, state, progressCh)
				agentNode.SetStatus(ir.NodeSucceeded)
				r.report(progressCh, agentNode)
				return
			}
		}

		for _, action := range pending {
			answered := plan.GetNodeByName(action.Step)
			state.Append(observe(agentCtx, answered, action.ToolCallID))
			if answered == nil {
				continue
			}
			answeredState := answered.State()
			finishedAt := ""
			if !answeredState.FinishedAt.IsZero() {
				finishedAt = stringutil.FormatTime(answeredState.FinishedAt)
			}
			errText := ""
			if answeredState.Error != nil {
				errText = answeredState.Error.Error()
			}
			state.FinalizeEvent(action.ToolCallID, action.Step,
				answeredState.Status.String(), finishedAt, errText)

			if action.Question != "" {
				if answer, ok := askUserAnswer(answered, answeredState.HumanTaskInput); ok {
					state.RecordAnswer(action.Question, limitAgentObservation(
						answer, dag.AgentObservationMaxBytes()))
				}
			}
		}
		state.ClearPendingBatch()
		r.persistAgent(agentCtx, agentNode, state, progressCh)
	}

	maxTurns := dag.AgentMaxIterations()
	for !state.Settled() {
		if r.isCanceled() {
			agentNode.SetStatus(ir.NodeAborted)
			r.report(progressCh, agentNode)
			return
		}
		if state.Turns >= maxTurns {
			r.failAgent(agentCtx, plan, agentNode, fmt.Errorf(
				"agent reached its %d turn limit with tasks still open: %s",
				maxTurns, strings.Join(state.OpenTaskNames(), ", ")), progressCh)
			return
		}

		observationKeepRecent := dag.AgentObservationKeepRecent()
		if !state.ObservationAging && observationKeepRecent > 0 {
			maxContextTokens := dag.AgentMaxContextTokens()
			promptTokens := state.LatestPromptTokens()
			if maxContextTokens > 0 && promptTokens >= maxContextTokens {
				state.EnableObservationAging()
				logger.Info(agentCtx, "Agent started aging old observations",
					slog.Int("promptTokens", promptTokens),
					slog.Int("maxContextTokens", maxContextTokens))
			}
		}
		if state.ObservationAging {
			state.CompactObservations(
				observationKeepRecent, dag.AgentObservationMaxBytes())
		}

		decisions, err := planner.Next(
			agentCtx, state, observationKeepRecent, dag.AgentObservationMaxBytes())
		if err != nil {
			r.failAgent(agentCtx, plan, agentNode, err, progressCh)
			if state.ObservationAging {
				r.persistAgent(agentCtx, agentNode, state, progressCh)
			}
			return
		}

		// A decision can take a while to come back. If the run was stopped in the
		// meantime, drop it rather than settling a task or opening a question.
		if r.isCanceled() {
			agentNode.SetStatus(ir.NodeAborted)
			r.report(progressCh, agentNode)
			return
		}

		suspended, err := r.applyDecisions(agentCtx, plan, state, decisions, progressCh)
		if err != nil {
			r.failAgent(agentCtx, plan, agentNode, err, progressCh)
			return
		}

		r.persistAgent(agentCtx, agentNode, state, progressCh)
		if suspended {
			// The action is waiting on a person. The run reports Waiting, the
			// process exits, and this loop resumes once the task is completed.
			agentNode.SetStatus(ir.NodeSucceeded)
			r.report(progressCh, agentNode)
			return
		}
	}

	if failed := state.FailedTasks(); len(failed) > 0 {
		reasons := make([]string, 0, len(failed))
		for _, task := range failed {
			reasons = append(reasons, fmt.Sprintf("%s (%s)", task.Name, task.Reason))
		}
		r.failAgent(agentCtx, plan, agentNode, fmt.Errorf(
			"agent could not achieve: %s", strings.Join(reasons, "; ")), progressCh)
		r.persistAgent(agentCtx, agentNode, state, progressCh)
		return
	}

	r.skipUnusedActions(ctx, plan)
	agentNode.SetStatus(ir.NodeSucceeded)
	r.persistAgent(agentCtx, agentNode, state, progressCh)
	logger.Info(agentCtx, "Agent settled every task", slog.Int("turns", state.Turns))
}

func (r *Runner) applyDecisions(
	ctx context.Context,
	plan *Plan,
	state *agentloop.State,
	decisions []agentloop.Decision,
	progressCh chan *Node,
) (bool, error) {
	if len(decisions) > 1 {
		if problem := validateAgentActionBatch(plan, state, decisions); problem != "" {
			state.Nudges = 0
			for _, decision := range decisions {
				state.RecordEvent(agentloop.Event{
					Kind:       agentloop.EventRejected,
					Name:       decision.ToolName,
					Reason:     problem,
					ToolCallID: decision.ToolCallID,
				})
				state.Append(toolResult(ctx, decision.ToolCallID,
					"Error: action batch rejected: "+problem))
			}
			return false, nil
		}
		return r.runAgentActionBatch(ctx, plan, state, decisions, progressCh)
	}
	for i := range decisions {
		suspended, err := r.applyDecision(ctx, plan, state, &decisions[i], progressCh)
		if suspended || err != nil {
			return suspended, err
		}
	}
	return false, nil
}

func validateAgentActionBatch(
	plan *Plan,
	state *agentloop.State,
	decisions []agentloop.Decision,
) string {
	seen := make(map[string]struct{}, len(decisions))
	for _, decision := range decisions {
		if decision.Kind != agentloop.DecideRunStep {
			if decision.Kind == agentloop.DecideInvalid {
				return decision.Problem
			}
			return "multiple tool calls may contain only workflow actions; " +
				agentloop.AskUserTool + " and " + agentloop.SetTaskStatusTool + " must be called alone"
		}
		if _, ok := seen[decision.Step]; ok {
			return fmt.Sprintf("action %q appears more than once", decision.Step)
		}
		seen[decision.Step] = struct{}{}
		if plan.GetNodeByName(decision.Step) == nil {
			return fmt.Sprintf("step %q is not part of this workflow", decision.Step)
		}
		if runs := state.StepRunCount(decision.Step); runs >= ir.DefaultAgentMaxStepRuns {
			return fmt.Sprintf("action %q has already run %d times, which is its limit",
				decision.Step, runs)
		}
	}
	return ""
}

// applyDecision carries out one agent decision and appends the resulting
// observation. It reports whether the run must suspend for human input.
func (r *Runner) applyDecision(
	ctx context.Context,
	plan *Plan,
	state *agentloop.State,
	decision *agentloop.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	if decision.Kind != agentloop.DecideStop {
		// Any turn that used a tool breaks a run of silent replies.
		state.Nudges = 0
	}

	switch decision.Kind {
	case agentloop.DecideSetTaskStatus:
		if err := state.SetTaskStatus(decision.Task, decision.TaskStatus, decision.Reason); err != nil {
			state.Append(toolResult(ctx, decision.ToolCallID, "Error: "+err.Error()))
			return false, nil
		}
		logger.Info(ctx, "Agent settled a task",
			slog.String("task", decision.Task),
			slog.String("status", string(decision.TaskStatus)),
			slog.String("reason", decision.Reason))
		state.RecordEvent(agentloop.Event{
			Kind:       agentloop.EventTaskStatus,
			Name:       decision.Task,
			Status:     string(decision.TaskStatus),
			Reason:     decision.Reason,
			ToolCallID: decision.ToolCallID,
		})
		state.Append(toolResult(ctx, decision.ToolCallID, taskStatusAck(state, decision)))
		return false, nil

	case agentloop.DecideInvalid:
		state.RecordEvent(agentloop.Event{
			Kind:       agentloop.EventRejected,
			Name:       decision.ToolName,
			Reason:     decision.Problem,
			ToolCallID: decision.ToolCallID,
		})
		state.Append(toolResult(ctx, decision.ToolCallID, "Error: "+decision.Problem))
		return false, nil

	case agentloop.DecideAskUser:
		return r.askUser(ctx, plan, state, decision, progressCh)

	case agentloop.DecideStop:
		return false, r.nudge(ctx, state)

	case agentloop.DecideRunStep:
		return r.runAgentAction(ctx, plan, state, decision, progressCh)

	default:
		return false, fmt.Errorf("unhandled agent decision %v", decision.Kind)
	}
}

// askUser opens the agent's own human task with the question it wrote, and
// suspends the run. Answering it resumes the same run with the reply as the next
// observation, so the agent keeps its context.
func (r *Runner) askUser(
	ctx context.Context,
	plan *Plan,
	state *agentloop.State,
	decision *agentloop.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	node := plan.GetNodeByName(ir.AskUserStepName)
	if node == nil {
		state.Append(toolResult(ctx, decision.ToolCallID,
			"Error: this workflow cannot ask questions"))
		return false, nil
	}

	// Waiting for a person is a root-run capability. An agent running as
	// somebody's child says so and carries on rather than stalling the parent.
	if rCtx := runctx.GetContext(ctx); rCtx.RootDAGRun.ID != "" &&
		rCtx.RootDAGRun.ID != rCtx.DAGRunID {
		state.Append(toolResult(ctx, decision.ToolCallID,
			"Error: this run is a sub-workflow, so nobody can be asked. "+
				"Decide with the information you have, or settle the task as failed."))
		return false, nil
	}

	question := decision.Question
	if resolved, rerr := resolveRuntimeString(
		ctx, question, cmnvalue.WorkflowField("ask_user.question")); rerr == nil {
		question = resolved
	}

	// Hold the agent to an answer it already has, rather than putting the
	// same question to a person twice.
	if prior, ok := state.PriorAnswer(question); ok {
		state.Append(toolResult(ctx, decision.ToolCallID, fmt.Sprintf(
			"You already asked this and were told: %s", prior)))
		return false, nil
	}
	if state.QuestionCount() >= ir.DefaultAgentMaxQuestions {
		state.Append(toolResult(ctx, decision.ToolCallID, fmt.Sprintf(
			"Error: this run has already asked %d questions, which is its limit. "+
				"Decide with what you have, or settle the task as failed.",
			ir.DefaultAgentMaxQuestions)))
		return false, nil
	}

	// A later question reuses the same task, so clear the previous answer.
	if node.State().Status != ir.NodeNotStarted {
		node.ResetForRerun(node.Step())
	}

	logger.Info(ctx, "Agent is asking a question", slog.String("question", question))
	node.OpenHumanTask(question, time.Now())
	state.RecordEvent(agentloop.Event{
		Kind:       agentloop.EventAskUser,
		Name:       ir.AskUserStepName,
		Status:     ir.NodeWaiting.String(),
		Reason:     question,
		ToolCallID: decision.ToolCallID,
		StartedAt:  stringutil.FormatTime(node.State().StartedAt),
	})
	r.report(progressCh, node)

	state.SetPendingBatch([]agentloop.PendingAction{{
		ToolCallID: decision.ToolCallID,
		ToolName:   decision.ToolName,
		Step:       ir.AskUserStepName,
		Question:   question,
	}})
	return true, nil
}

// nudge answers a turn where the model stopped calling tools while tasks were
// still open. One reminder is allowed; a second silent turn ends the run.
func (r *Runner) nudge(ctx context.Context, state *agentloop.State) error {
	open := strings.Join(state.OpenTaskNames(), ", ")
	if state.Nudges > 0 {
		return fmt.Errorf("agent stopped acting with tasks still open: %s", open)
	}
	state.Nudges++
	state.RecordEvent(agentloop.Event{
		Kind:   agentloop.EventStalled,
		Reason: "no action chosen while " + open + " remained open",
	})
	logger.Warn(ctx, "Agent answered without acting", slog.String("openTasks", open))
	state.Append(ir.LLMMessage{
		Role: ir.LLMRoleUser,
		Content: fmt.Sprintf(
			"These tasks are still open: %s. Either run an action that advances one of them, "+
				"or settle each one with %s as completed, skipped, or failed.",
			open, agentloop.SetTaskStatusTool),
	})
	return nil
}

// runAgentAction runs the step the agent chose, resetting the node
// first when the step has already run in this DAG run.
func (r *Runner) runAgentAction(
	ctx context.Context,
	plan *Plan,
	state *agentloop.State,
	decision *agentloop.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	node := plan.GetNodeByName(decision.Step)
	if node == nil {
		state.Append(toolResult(ctx, decision.ToolCallID,
			fmt.Sprintf("Error: step %q is not part of this workflow", decision.Step)))
		return false, nil
	}

	runs := state.StepRunCount(decision.Step)
	if runs >= ir.DefaultAgentMaxStepRuns {
		state.Append(toolResult(ctx, decision.ToolCallID, fmt.Sprintf(
			"Error: action %q has already run %d times, which is its limit. Choose a different action.",
			decision.Step, runs)))
		return false, nil
	}

	prepareAgentActionNode(ctx, node, decision)
	attempt := state.RecordStepRun(decision.Step)

	logger.Info(ctx, "Agent running action", tag.Step(decision.Step))
	node.SetStatus(ir.NodeRunning)

	actionCtx, err := r.setupVariables(ctx, plan, node)
	if err != nil {
		node.MarkError(err)
		r.report(progressCh, node)
		recordActionEvent(state, decision.ToolCallID, decision.Step, attempt, node)
		state.Append(observe(ctx, node, decision.ToolCallID))
		return false, nil
	}

	r.executeAgentAction(actionCtx, plan, node, progressCh)
	recordActionEvent(state, decision.ToolCallID, decision.Step, attempt, node)

	// The agent has taken responsibility for the outcome: it is reported as
	// an observation, not as a run-level error. Leaving the error set would make
	// the process exit non-zero for a run the agent went on to complete.
	r.setLastError(nil)

	if node.State().Status == ir.NodeWaiting {
		state.SetPendingBatch([]agentloop.PendingAction{{
			ToolCallID: decision.ToolCallID,
			ToolName:   decision.ToolName,
			Step:       decision.Step,
		}})
		return true, nil
	}

	state.Append(observe(ctx, node, decision.ToolCallID))
	return false, nil
}

// recordActionEvent puts one run of a step on the decision timeline, carrying
// the status and timing the UI needs to render it.
func recordActionEvent(
	state *agentloop.State,
	toolCallID string,
	step string,
	attempt int,
	node *Node,
) {
	nodeState := node.State()
	event := agentloop.Event{
		Kind:       agentloop.EventAction,
		Name:       step,
		Status:     nodeState.Status.String(),
		Attempt:    attempt,
		ToolCallID: toolCallID,
		StartedAt:  stringutil.FormatTime(nodeState.StartedAt),
	}
	if !nodeState.FinishedAt.IsZero() {
		event.FinishedAt = stringutil.FormatTime(nodeState.FinishedAt)
	}
	if nodeState.Error != nil {
		event.Reason = nodeState.Error.Error()
	}
	// The child run this attempt produced, so the timeline can link to it.
	if len(nodeState.SubRuns) > 0 {
		event.ChildDAGRunID = nodeState.SubRuns[0].DAGRunID
		event.ChildDAGName = nodeState.SubRuns[0].DAGName
	}
	state.RecordEvent(event)
}

// resolveTaskDescriptions expands variables in the completion criteria the
// agent judges against, so they can be parameterised per run.
func resolveTaskDescriptions(ctx context.Context, state *agentloop.State) error {
	for i, task := range state.Tasks {
		resolved, err := resolveRuntimeString(
			ctx, task.Description, cmnvalue.WorkflowField("tasks.description"))
		if err != nil {
			return fmt.Errorf("failed to evaluate description of task %q: %w", task.Name, err)
		}
		state.Tasks[i].Description = resolved
	}
	return nil
}

// declaredStep returns the step as written in the DAG, falling back to the
// node's current definition.
func declaredStep(ctx context.Context, name string, node *Node) ir.Step {
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

// executeAgentAction runs a single action to completion, mirroring the
// per-node handling of the graph loop.
func (r *Runner) executeAgentAction(ctx context.Context, plan *Plan, node *Node, progressCh chan *Node) {
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

// skipUnusedActions marks the steps the agent never chose. Without this the
// run would report Running forever, because unstarted nodes keep the plan from
// looking finished.
func (r *Runner) skipUnusedActions(ctx context.Context, plan *Plan) {
	for _, node := range plan.Nodes() {
		if node.State().Status != ir.NodeNotStarted {
			continue
		}
		logger.Debug(ctx, "Agent never ran step", tag.Step(node.Name()))
		node.SetStatus(ir.NodeSkipped)
	}
}

// failAgent ends the run with an error. Steps the agent never chose
// are marked skipped so the plan reads as finished rather than still running.
func (r *Runner) failAgent(ctx context.Context, plan *Plan, node *Node, err error, progressCh chan *Node) {
	logger.Error(ctx, "Agent failed", tag.Error(err))
	r.setLastError(err)
	node.MarkError(err)
	r.skipUnusedActions(ctx, plan)
	r.report(progressCh, node)
}

// persistAgent writes the agent's state and transcript to the node so
// they survive suspension and appear in the UI.
func (r *Runner) persistAgent(ctx context.Context, node *Node, state *agentloop.State, progressCh chan *Node) {
	raw, err := state.Marshal()
	if err != nil {
		logger.Error(ctx, "Failed to persist agent state", tag.Error(err))
	} else {
		node.SetAgentState(raw)
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

// observe renders the outcome of an action as the tool result the agent
// sees on its next turn.
func observe(ctx context.Context, node *Node, toolCallID string) ir.LLMMessage {
	if node == nil {
		return toolResult(ctx, toolCallID, "Error: the step disappeared from the workflow")
	}

	state := node.State()
	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %s\n", state.Status.String())

	if state.Error != nil {
		fmt.Fprintf(&sb, "error: %s\n", state.Error.Error())
	}
	if len(state.HumanTaskInput) > 0 {
		if answer, ok := askUserAnswer(node, state.HumanTaskInput); ok {
			fmt.Fprintf(&sb, "answer: %s\n", answer)
		} else {
			fmt.Fprintf(&sb, "submitted: %s\n", string(state.HumanTaskInput))
		}
		if state.HumanTaskCompletedBy != "" {
			fmt.Fprintf(&sb, "answered by: %s\n", state.HumanTaskCompletedBy)
		}
	}
	declared := publishedOutputs(state)
	if declared != "" {
		fmt.Fprintf(&sb, "outputs: %s\n", declared)
	} else if state.OutputValue != nil && *state.OutputValue != "" {
		fmt.Fprintf(&sb, "output: %s\n", *state.OutputValue)
	}
	// A step that launched a child DAG is reported from the child run itself.
	// Its stdout only mirrors the child's status JSON, once per internal retry,
	// and is empty altogether on a repeated run. A child that declared its
	// outputs has already reported them, so only its failures are worth adding.
	if len(state.SubRuns) > 0 {
		if summary := childRunSummary(ctx, state.SubRuns[0].DAGRunID, declared != ""); summary != "" {
			sb.WriteString(summary)
			return toolResult(ctx, toolCallID, sb.String())
		}
	}

	if tail := logTail(state.Stdout); tail != "" {
		fmt.Fprintf(&sb, "log:\n%s\n", tail)
	}
	if tail := logTail(state.Stderr); tail != "" {
		fmt.Fprintf(&sb, "stderr:\n%s\n", tail)
	}

	return toolResult(ctx, toolCallID, sb.String())
}

// publishedOutputs returns the outputs a step published explicitly, preferring
// declared file-based outputs over the general payload. It is empty for a step
// that published nothing.
func publishedOutputs(state NodeState) string {
	if state.StepOutputsValue != nil && *state.StepOutputsValue != "" {
		return *state.StepOutputsValue
	}
	if state.OutputsValue != nil && *state.OutputsValue != "" {
		return *state.OutputsValue
	}
	return ""
}

// childRunSummary reports what a child DAG run produced, read from the run
// itself rather than scraped from the parent step's log. When the child
// declared its outputs, outputsReported suppresses the scraped fallback so
// intermediate variables stay out of the transcript.
func childRunSummary(ctx context.Context, childRunID string, outputsReported bool) string {
	rCtx := runctx.GetContext(ctx)
	if childRunID == "" || rCtx.RunStateStore == nil {
		return ""
	}
	attempt, err := rCtx.RunStateStore.OpenChildAttempt(ctx, rCtx.RootDAGRun, childRunID)
	if err != nil {
		return ""
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil || status == nil {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "child run: %s (%s)\n", status.Name, status.Status.String())

	if !outputsReported {
		if outputs := childOutputs(status.Nodes); len(outputs) > 0 {
			sb.WriteString("outputs:\n")
			for _, key := range slices.Sorted(maps.Keys(outputs)) {
				fmt.Fprintf(&sb, "  %s=%s\n", key, stringutil.TruncString(outputs[key], 2000))
			}
		}
	}

	for _, node := range status.Nodes {
		if node == nil || node.Status != ir.NodeFailed {
			continue
		}
		fmt.Fprintf(&sb, "failed step %s: %s\n", node.Step.Name, node.Error)
	}

	return sb.String()
}

// childOutputs flattens the output variables declared by a child run's steps.
func childOutputs(nodes []*ir.Node) map[string]string {
	outputs := make(map[string]string)
	for _, node := range nodes {
		if node == nil || node.OutputVariables == nil {
			continue
		}
		node.OutputVariables.Range(func(key, value any) bool {
			k, okKey := key.(string)
			v, okValue := value.(string)
			if !okKey || !okValue {
				return true
			}
			outputs[k] = strings.TrimPrefix(v, k+"=")
			return true
		})
	}
	return outputs
}

// askUserAnswer pulls the reply out of an ask_user submission so the agent
// reads prose rather than a form payload.
func askUserAnswer(node *Node, input json.RawMessage) (string, bool) {
	if node.Name() != ir.AskUserStepName {
		return "", false
	}
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		return "", false
	}
	answer, ok := fields[ir.AskUserAnswerField].(string)
	return answer, ok
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

func toolResult(ctx context.Context, toolCallID, content string) ir.LLMMessage {
	dag := GetDAGContext(ctx).DAG
	maxBytes := ir.DefaultAgentObservationMaxBytes
	if dag != nil {
		maxBytes = dag.AgentObservationMaxBytes()
	}
	return ir.LLMMessage{
		Role:       ir.LLMRoleTool,
		ToolCallID: toolCallID,
		Content:    limitAgentObservation(content, maxBytes),
	}
}

func limitAgentObservation(content string, maxBytes int) string {
	const marker = "\n[observation truncated]"

	content = strings.ToValidUTF8(content, "\uFFFD")
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	if maxBytes < len(marker) {
		return stringutil.TruncUTF8Bytes(content, maxBytes)
	}
	return stringutil.TruncUTF8Bytes(content, maxBytes-len(marker)) + marker
}

func taskStatusAck(state *agentloop.State, decision *agentloop.Decision) string {
	open := state.OpenTaskNames()
	if len(open) == 0 {
		return fmt.Sprintf("Task %q is now %s. No task is open.", decision.Task, decision.TaskStatus)
	}
	return fmt.Sprintf("Task %q is now %s. Still open: %s.",
		decision.Task, decision.TaskStatus, strings.Join(open, ", "))
}
