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
	"github.com/dagucloud/dagu/v2/internal/runtime/controller"
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

	ctrlNode := plan.GetNodeByName(ir.ControllerStepName)
	if ctrlNode == nil {
		r.setLastError(fmt.Errorf("controller step %q is missing from the plan", ir.ControllerStepName))
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
	ctrlNode.SetStatus(ir.NodeRunning)
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

	// Resolve the models the same way a chat step does, so array-form llm.model
	// and value references work here too.
	models, err := ResolveModels(ctrlCtx, dag.LLM.GetModels())
	if err != nil {
		r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
		return
	}
	// The system prompt and the task descriptions are author-written prompt
	// text, so they take variables the same way any other workflow field does.
	system, err := resolveRuntimeString(
		ctrlCtx, dag.LLM.System, cmnvalue.WorkflowField("llm.system"))
	if err != nil {
		r.failController(ctrlCtx, plan, ctrlNode, fmt.Errorf(
			"failed to evaluate llm.system: %w", err), progressCh)
		return
	}
	if err := resolveTaskDescriptions(ctrlCtx, state); err != nil {
		r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
		return
	}
	planner := newControllerModelPlanner(
		ctrlCtx, dag.LLM, models, catalog, system,
		func(msgs []ir.LLMMessage) []ir.LLMMessage {
			return MaskSecretsForProvider(ctrlCtx, msgs)
		})

	// A run that suspended mid-action resumes here: report what became of the
	// action before asking for the next decision.
	if pending := state.Pending; pending != nil {
		answered := plan.GetNodeByName(pending.Step)
		state.Append(observe(ctrlCtx, answered, pending.ToolCallID))
		if answered != nil {
			answeredState := answered.State()
			finishedAt := ""
			if !answeredState.FinishedAt.IsZero() {
				finishedAt = stringutil.FormatTime(answeredState.FinishedAt)
			}
			errText := ""
			if answeredState.Error != nil {
				errText = answeredState.Error.Error()
			}
			state.FinalizeEvent(pending.Step, answeredState.Status.String(), finishedAt, errText)

			if pending.Question != "" {
				if answer, ok := askUserAnswer(answered, answeredState.HumanTaskInput); ok {
					state.RecordAnswer(pending.Question, limitControllerObservation(
						answer, dag.ControllerObservationMaxBytes()))
				}
			}
		}
		state.Pending = nil
		r.persistController(ctrlCtx, ctrlNode, state, progressCh)
	}

	maxTurns := dag.ControllerMaxIterations()
	for !state.Settled() {
		if r.isCanceled() {
			ctrlNode.SetStatus(ir.NodeAborted)
			r.report(progressCh, ctrlNode)
			return
		}
		if state.Turns >= maxTurns {
			r.failController(ctrlCtx, plan, ctrlNode, fmt.Errorf(
				"controller reached its %d turn limit with tasks still open: %s",
				maxTurns, strings.Join(state.OpenTaskNames(), ", ")), progressCh)
			return
		}

		observationKeepRecent := dag.ControllerObservationKeepRecent()
		if !state.ObservationAging && observationKeepRecent > 0 {
			maxContextTokens := dag.ControllerMaxContextTokens()
			promptTokens := state.LatestPromptTokens()
			if maxContextTokens > 0 && promptTokens >= maxContextTokens {
				state.EnableObservationAging()
				logger.Info(ctrlCtx, "Controller started aging old observations",
					slog.Int("promptTokens", promptTokens),
					slog.Int("maxContextTokens", maxContextTokens))
			}
		}
		if state.ObservationAging {
			state.CompactObservations(
				observationKeepRecent, dag.ControllerObservationMaxBytes())
		}

		decision, err := planner.Next(
			ctrlCtx, state, observationKeepRecent, dag.ControllerObservationMaxBytes())
		if err != nil {
			r.failController(ctrlCtx, plan, ctrlNode, err, progressCh)
			if state.ObservationAging {
				r.persistController(ctrlCtx, ctrlNode, state, progressCh)
			}
			return
		}

		// A decision can take a while to come back. If the run was stopped in the
		// meantime, drop it rather than settling a task or opening a question.
		if r.isCanceled() {
			ctrlNode.SetStatus(ir.NodeAborted)
			r.report(progressCh, ctrlNode)
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
			ctrlNode.SetStatus(ir.NodeSucceeded)
			r.report(progressCh, ctrlNode)
			return
		}
	}

	if failed := state.FailedTasks(); len(failed) > 0 {
		reasons := make([]string, 0, len(failed))
		for _, task := range failed {
			reasons = append(reasons, fmt.Sprintf("%s (%s)", task.Name, task.Reason))
		}
		r.failController(ctrlCtx, plan, ctrlNode, fmt.Errorf(
			"controller could not achieve: %s", strings.Join(reasons, "; ")), progressCh)
		r.persistController(ctrlCtx, ctrlNode, state, progressCh)
		return
	}

	r.skipUnusedActions(ctx, plan)
	ctrlNode.SetStatus(ir.NodeSucceeded)
	r.persistController(ctrlCtx, ctrlNode, state, progressCh)
	logger.Info(ctrlCtx, "Controller settled every task", slog.Int("turns", state.Turns))
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
	case controller.DecideSetTaskStatus:
		if err := state.SetTaskStatus(decision.Task, decision.TaskStatus, decision.Reason); err != nil {
			state.Append(toolResult(ctx, decision.ToolCallID, "Error: "+err.Error()))
			return false, nil
		}
		logger.Info(ctx, "Controller settled a task",
			slog.String("task", decision.Task),
			slog.String("status", string(decision.TaskStatus)),
			slog.String("reason", decision.Reason))
		state.RecordEvent(controller.Event{
			Kind:   controller.EventTaskStatus,
			Name:   decision.Task,
			Status: string(decision.TaskStatus),
			Reason: decision.Reason,
		})
		state.Append(toolResult(ctx, decision.ToolCallID, taskStatusAck(state, decision)))
		return false, nil

	case controller.DecideInvalid:
		state.RecordEvent(controller.Event{
			Kind:   controller.EventRejected,
			Name:   decision.ToolName,
			Reason: decision.Problem,
		})
		state.Append(toolResult(ctx, decision.ToolCallID, "Error: "+decision.Problem))
		return false, nil

	case controller.DecideAskUser:
		return r.askUser(ctx, plan, state, decision, progressCh)

	case controller.DecideStop:
		return false, r.nudge(ctx, state)

	case controller.DecideRunStep:
		return r.runControllerAction(ctx, plan, state, decision, progressCh)

	default:
		return false, fmt.Errorf("unhandled controller decision %v", decision.Kind)
	}
}

// askUser opens the controller's own human task with the question it wrote, and
// suspends the run. Answering it resumes the same run with the reply as the next
// observation, so the controller keeps its context.
func (r *Runner) askUser(
	ctx context.Context,
	plan *Plan,
	state *controller.State,
	decision *controller.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	node := plan.GetNodeByName(ir.AskUserStepName)
	if node == nil {
		state.Append(toolResult(ctx, decision.ToolCallID,
			"Error: this workflow cannot ask questions"))
		return false, nil
	}

	// Waiting for a person is a root-run capability. A controller running as
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

	// Hold the controller to an answer it already has, rather than putting the
	// same question to a person twice.
	if prior, ok := state.PriorAnswer(question); ok {
		state.Append(toolResult(ctx, decision.ToolCallID, fmt.Sprintf(
			"You already asked this and were told: %s", prior)))
		return false, nil
	}
	if state.QuestionCount() >= ir.DefaultControllerMaxQuestions {
		state.Append(toolResult(ctx, decision.ToolCallID, fmt.Sprintf(
			"Error: this run has already asked %d questions, which is its limit. "+
				"Decide with what you have, or settle the task as failed.",
			ir.DefaultControllerMaxQuestions)))
		return false, nil
	}

	// A later question reuses the same task, so clear the previous answer.
	if node.State().Status != ir.NodeNotStarted {
		node.ResetForRerun(node.Step())
	}

	logger.Info(ctx, "Controller is asking a question", slog.String("question", question))
	node.OpenHumanTask(question, time.Now())
	state.RecordEvent(controller.Event{
		Kind:      controller.EventAskUser,
		Name:      ir.AskUserStepName,
		Status:    ir.NodeWaiting.String(),
		Reason:    question,
		StartedAt: stringutil.FormatTime(node.State().StartedAt),
	})
	r.report(progressCh, node)

	state.Pending = &controller.PendingAction{
		ToolCallID: decision.ToolCallID,
		ToolName:   decision.ToolName,
		Step:       ir.AskUserStepName,
		Question:   question,
	}
	return true, nil
}

// nudge answers a turn where the model stopped calling tools while tasks were
// still open. One reminder is allowed; a second silent turn ends the run.
func (r *Runner) nudge(ctx context.Context, state *controller.State) error {
	open := strings.Join(state.OpenTaskNames(), ", ")
	if state.Nudges > 0 {
		return fmt.Errorf("controller stopped acting with tasks still open: %s", open)
	}
	state.Nudges++
	state.RecordEvent(controller.Event{
		Kind:   controller.EventStalled,
		Reason: "no action chosen while " + open + " remained open",
	})
	logger.Warn(ctx, "Controller answered without acting", slog.String("openTasks", open))
	state.Append(ir.LLMMessage{
		Role: ir.LLMRoleUser,
		Content: fmt.Sprintf(
			"These tasks are still open: %s. Either run an action that advances one of them, "+
				"or settle each one with %s as completed, skipped, or failed.",
			open, controller.SetTaskStatusTool),
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
		state.Append(toolResult(ctx, decision.ToolCallID,
			fmt.Sprintf("Error: step %q is not part of this workflow", decision.Step)))
		return false, nil
	}

	runs := state.StepRunCount(decision.Step)
	if runs >= ir.DefaultControllerMaxStepRuns {
		state.Append(toolResult(ctx, decision.ToolCallID, fmt.Sprintf(
			"Error: action %q has already run %d times, which is its limit. Choose a different action.",
			decision.Step, runs)))
		return false, nil
	}

	// Reset against the declared step, not the node's current one, so arguments
	// from an earlier invocation do not leak into this one.
	step := declaredStep(ctx, decision.Step, node)
	if node.State().Status != ir.NodeNotStarted {
		// Re-running: clear the previous attempt and mark the node repeated so a
		// child DAG run gets a fresh run ID instead of reusing the earlier one.
		// Links to earlier attempts' child runs are carried across the reset, so
		// every attempt stays reachable from the step.
		previous := node.State().SubRuns
		archived := node.State().SubRunsRepeated
		node.ResetForRerun(step)
		node.SetRepeated(true)
		node.AddSubRunsRepeated(archived...)
		node.AddSubRunsRepeated(previous...)
	}
	if step.SubDAG != nil {
		params := controller.MergeParams(
			step.SubDAG.Params, decision.Args, controller.PinnedParams(step))
		node.SetSubDAG(ir.SubDAG{Name: step.SubDAG.Name, Params: params})
	}
	attempt := state.RecordStepRun(decision.Step)

	logger.Info(ctx, "Controller running action", tag.Step(decision.Step))
	node.SetStatus(ir.NodeRunning)

	actionCtx, err := r.setupVariables(ctx, plan, node)
	if err != nil {
		node.MarkError(err)
		r.report(progressCh, node)
		recordActionEvent(state, decision.Step, attempt, node)
		state.Append(observe(ctx, node, decision.ToolCallID))
		return false, nil
	}

	r.executeControllerAction(actionCtx, plan, node, progressCh)
	recordActionEvent(state, decision.Step, attempt, node)

	// The controller has taken responsibility for the outcome: it is reported as
	// an observation, not as a run-level error. Leaving the error set would make
	// the process exit non-zero for a run the controller went on to complete.
	r.setLastError(nil)

	if node.State().Status == ir.NodeWaiting {
		state.Pending = &controller.PendingAction{
			ToolCallID: decision.ToolCallID,
			ToolName:   decision.ToolName,
			Step:       decision.Step,
		}
		return true, nil
	}

	state.Append(observe(ctx, node, decision.ToolCallID))
	return false, nil
}

// recordActionEvent puts one run of a step on the decision timeline, carrying
// the status and timing the UI needs to render it.
func recordActionEvent(state *controller.State, step string, attempt int, node *Node) {
	nodeState := node.State()
	event := controller.Event{
		Kind:      controller.EventAction,
		Name:      step,
		Status:    nodeState.Status.String(),
		Attempt:   attempt,
		StartedAt: stringutil.FormatTime(nodeState.StartedAt),
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
// controller judges against, so they can be parameterised per run.
func resolveTaskDescriptions(ctx context.Context, state *controller.State) error {
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
		if node.State().Status != ir.NodeNotStarted {
			continue
		}
		logger.Debug(ctx, "Controller never ran step", tag.Step(node.Name()))
		node.SetStatus(ir.NodeSkipped)
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
	if childRunID == "" || rCtx.DAGRunStore == nil {
		return ""
	}
	attempt, err := rCtx.DAGRunStore.FindSubAttempt(ctx, rCtx.RootDAGRun, childRunID)
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

// askUserAnswer pulls the reply out of an ask_user submission so the controller
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
	maxBytes := ir.DefaultControllerObservationMaxBytes
	if dag != nil {
		maxBytes = dag.ControllerObservationMaxBytes()
	}
	return ir.LLMMessage{
		Role:       ir.LLMRoleTool,
		ToolCallID: toolCallID,
		Content:    limitControllerObservation(content, maxBytes),
	}
}

func limitControllerObservation(content string, maxBytes int) string {
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

func taskStatusAck(state *controller.State, decision *controller.Decision) string {
	open := state.OpenTaskNames()
	if len(open) == 0 {
		return fmt.Sprintf("Task %q is now %s. No task is open.", decision.Task, decision.TaskStatus)
	}
	return fmt.Sprintf("Task %q is now %s. Still open: %s.",
		decision.Task, decision.TaskStatus, strings.Join(open, ", "))
}
