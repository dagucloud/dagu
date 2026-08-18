// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
)

type agentActionExecution struct {
	decision agentloop.Decision
	node     *Node
	ctx      context.Context
	attempt  int
}

// runAgentActionBatch runs independent actions from one model turn together.
func (r *Runner) runAgentActionBatch(
	ctx context.Context,
	plan *Plan,
	state *agentloop.State,
	decisions []agentloop.Decision,
	progressCh chan *Node,
) (suspended bool, err error) {
	state.Nudges = 0
	executions := make([]agentActionExecution, len(decisions))

	// Every batch member enters Running before variables are resolved. This
	// keeps sibling outputs out of the predecessor scope for the whole batch.
	for i := range decisions {
		decision := decisions[i]
		node := plan.GetNodeByName(decision.Step)
		prepareAgentActionNode(ctx, node, &decision)
		executions[i] = agentActionExecution{
			decision: decision,
			node:     node,
			attempt:  state.RecordStepRun(decision.Step),
		}
		logger.Info(ctx, "Agent running action", tag.Step(decision.Step))
		node.SetStatus(ir.NodeRunning)
	}

	runnable := make([]*agentActionExecution, 0, len(executions))
	for i := range executions {
		execution := &executions[i]
		actionCtx, setupErr := r.setupVariables(ctx, plan, execution.node)
		if setupErr != nil {
			execution.node.MarkError(setupErr)
			r.report(progressCh, execution.node)
			continue
		}
		execution.ctx = actionCtx
		runnable = append(runnable, execution)
	}

	var wg sync.WaitGroup
	limit := r.maxActiveRuns
	if limit <= 0 || limit > len(runnable) {
		limit = len(runnable)
	}
	semaphore := make(chan struct{}, limit)
	firstUnstarted := len(runnable)
	for i, execution := range runnable {
		if r.isCanceled() {
			firstUnstarted = i
			break
		}
		semaphore <- struct{}{}
		if r.isCanceled() {
			<-semaphore
			firstUnstarted = i
			break
		}
		wg.Add(1)
		go func(execution *agentActionExecution) {
			defer wg.Done()
			defer func() { <-semaphore }()
			r.executeAgentAction(execution.ctx, plan, execution.node, progressCh)
		}(execution)
	}
	wg.Wait()
	for _, execution := range runnable[firstUnstarted:] {
		execution.node.Cancel()
		r.report(progressCh, execution.node)
	}

	for i := range executions {
		execution := &executions[i]
		recordActionEvent(state, execution.decision.ToolCallID,
			execution.decision.Step, execution.attempt, execution.node)
		if execution.node.State().Status == ir.NodeWaiting {
			suspended = true
		}
	}
	r.setLastError(nil)

	if suspended {
		pending := make([]agentloop.PendingAction, len(executions))
		for i := range executions {
			pending[i] = agentloop.PendingAction{
				ToolCallID: executions[i].decision.ToolCallID,
				ToolName:   executions[i].decision.ToolName,
				Step:       executions[i].decision.Step,
			}
		}
		state.SetPendingBatch(pending)
		return true, nil
	}

	for i := range executions {
		execution := &executions[i]
		state.Append(observe(ctx, execution.node, execution.decision.ToolCallID))
	}
	return false, nil
}

func prepareAgentActionNode(ctx context.Context, node *Node, decision *agentloop.Decision) {
	step := declaredStep(ctx, decision.Step, node)
	if node.State().Status != ir.NodeNotStarted {
		previous := node.State().SubRuns
		archived := node.State().SubRunsRepeated
		node.ResetForRerun(step)
		node.SetRepeated(true)
		node.AddSubRunsRepeated(archived...)
		node.AddSubRunsRepeated(previous...)
	}
	if step.SubDAG != nil {
		params := agentloop.MergeParams(
			step.SubDAG.Params, decision.Args, agentloop.PinnedParams(step))
		node.SetSubDAG(ir.SubDAG{Name: step.SubDAG.Name, Params: params})
	}
}
