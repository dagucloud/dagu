// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var _ executor.NodesExecutor = (*parallelExecutor)(nil)

// nodeDispatch is the state of a step with nodes (RFC 013), which runs as a
// parallel step over one bound child run per node, with its own failure
// policy and aggregate.
type nodeDispatch struct {
	// matched lists the matched nodes in name order.
	matched []executor.NodeRun
	// keepGoing starts the remaining nodes after one fails.
	keepGoing bool
	// failed records that a node failed.
	failed bool
	// started holds when each node that started first did so, by run ID.
	started map[string]time.Time
	// finished holds when each node last returned, by run ID.
	finished map[string]time.Time
	// active holds the run IDs of the nodes with an attempt in flight.
	active map[string]bool
	// changed marks a change the step's records do not show yet.
	changed bool
	// maxOutputSize limits the aggregate the step writes.
	maxOutputSize int
}

// nodeResult is one entry of a node step's aggregate: the entry parallel
// writes, plus the node.
type nodeResult struct {
	Node         string            `json:"node"`
	Name         string            `json:"name,omitempty"`
	DAGRunID     string            `json:"dagRunId,omitempty"`
	Params       string            `json:"params,omitempty"`
	Outputs      map[string]string `json:"outputs,omitzero"`
	OutputValues map[string]any    `json:"outputValues,omitzero"`
	Status       string            `json:"status"`
}

func newNodesExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	if step.Nodes == nil {
		return nil, fmt.Errorf("step %q sets no nodes", step.Name)
	}
	dir := runtime.GetEnv(ctx).WorkingDir
	if dir != "" && !fileutil.FileExists(dir) {
		return nil, ErrWorkingDirNotExist
	}
	maxOutputSize := ir.DefaultMaxOutputSize
	if dag := runctx.GetContext(ctx).DAG; dag != nil && dag.MaxOutputSize > 0 {
		maxOutputSize = dag.MaxOutputSize
	}
	return &parallelExecutor{
		step:          step,
		workDir:       dir,
		maxConcurrent: max(step.Nodes.MaxConcurrent, ir.DefaultNodeMaxConcurrent),
		results:       make(map[string]*ir.RunStatus),
		children:      make(map[string]*executor.SubDAGExecutor),
		cancel:        make(chan struct{}),
		nodes: &nodeDispatch{
			keepGoing:     step.Nodes.KeepGoing,
			started:       make(map[string]time.Time),
			finished:      make(map[string]time.Time),
			active:        make(map[string]bool),
			maxOutputSize: maxOutputSize,
		},
	}, nil
}

// SetNodes implements executor.NodesExecutor.
func (e *parallelExecutor) SetNodes(matched []executor.NodeRun) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.nodes.matched = matched
	e.runParamsList = nil
	for _, node := range matched {
		if !node.Disabled {
			e.runParamsList = append(e.runParamsList, node.Run)
		}
	}
}

// checkNodeChild fails a dag.run step with nodes before any child starts when
// the child DAG cannot run bound to a node.
func (e *parallelExecutor) checkNodeChild(ctx context.Context) error {
	if e.nodes == nil || e.step.SubDAG == nil {
		return nil
	}
	child, err := executor.NewSubDAGExecutor(ctx, e.step.SubDAG.Name)
	if err != nil {
		return err
	}
	defer func() { _ = child.Cleanup(context.WithoutCancel(ctx)) }()
	if !child.DAG.NodeRequired {
		return fmt.Errorf("DAG %q does not declare node: required, so it cannot run bound to a node", e.step.SubDAG.Name)
	}
	return nil
}

// newNodeChildExecutor creates the executor of one node's child run: the
// dag.run step's DAG, or the command step alone.
func (e *parallelExecutor) newNodeChildExecutor(ctx context.Context) (*executor.SubDAGExecutor, error) {
	if e.step.SubDAG != nil {
		return executor.NewSubDAGExecutor(ctx, e.step.SubDAG.Name)
	}
	return executor.NewNodeStepExecutor(ctx, e.step.Name)
}

// startsNoMoreNodes reports whether nodes that have not started stay so: a
// node failed and the step does not keep going.
func (e *parallelExecutor) startsNoMoreNodes() bool {
	e.lock.Lock()
	defer e.lock.Unlock()
	return e.nodes != nil && e.nodes.failed && !e.nodes.keepGoing
}

// dropUnstartedNodes removes the first attempts of nodes that never started.
// Retries of nodes that did start remain, since a running node finishes.
func (e *parallelExecutor) dropUnstartedNodes(pending []scheduledAttempt) []scheduledAttempt {
	e.lock.Lock()
	defer e.lock.Unlock()
	kept := pending[:0]
	for _, attempt := range pending {
		if _, started := e.nodes.started[attempt.runParams.RunID]; started {
			kept = append(kept, attempt)
		}
	}
	return kept
}

// noteNodeStart records that a node's child run started. A retry of the node
// keeps the time of its first start.
func (e *parallelExecutor) noteNodeStart(runID string) {
	if e.nodes == nil {
		return
	}
	e.lock.Lock()
	defer e.lock.Unlock()
	if _, started := e.nodes.started[runID]; !started {
		e.nodes.started[runID] = time.Now()
	}
	e.nodes.active[runID] = true
	e.nodes.changed = true
}

// noteNodeFinish records that an attempt of a node's child run returned.
func (e *parallelExecutor) noteNodeFinish(runID string) {
	if e.nodes == nil {
		return
	}
	e.lock.Lock()
	defer e.lock.Unlock()
	e.nodes.finished[runID] = time.Now()
	e.nodes.active[runID] = false
	e.nodes.changed = true
}

// nodeRunStatus returns the status of a matched node's run. ended tells that
// the step has ended, after which no attempt of a node runs. A node that
// returned no result failed. Callers hold e.lock.
func (e *parallelExecutor) nodeRunStatus(node executor.NodeRun, ended bool) ir.NodeRunStatus {
	if node.Disabled {
		return ir.NodeRunDisabled
	}
	id := node.Run.RunID
	if _, started := e.nodes.started[id]; !started {
		return ir.NodeRunNotStarted
	}
	active := e.nodes.active[id]
	result, returned := e.results[id]
	switch {
	case active && e.cancelled():
		return ir.NodeRunAborted
	case active && !ended:
		return ir.NodeRunRunning
	case returned && len(result.PendingStepRetries) > 0 && !ended && !e.cancelled():
		// A step retry of the node is pending.
		return ir.NodeRunRunning
	case returned:
		return ir.NodeRunStatusOf(result.Status)
	case e.cancelled():
		return ir.NodeRunAborted
	default:
		return ir.NodeRunFailed
	}
}

// noteNodeResult records a node that failed. A node with a step retry pending
// has not failed yet, and one waiting for approval has not failed.
func (e *parallelExecutor) noteNodeResult(res attemptResult) {
	if e.nodes == nil {
		return
	}
	failed := res.err != nil
	if res.result != nil {
		failed = len(res.result.PendingStepRetries) == 0 &&
			!res.result.Status.IsSuccess() && res.result.Status != ir.Waiting
	}
	if failed {
		e.lock.Lock()
		e.nodes.failed = true
		e.lock.Unlock()
	}
}

// nodeEntries returns the step's aggregate entries in name order, a summary
// of them, and the outputs of the nodes that succeeded. Callers hold e.lock.
func (e *parallelExecutor) nodeEntries() ([]nodeResult, []map[string]string, int, int) {
	entries := make([]nodeResult, 0, len(e.nodes.matched))
	outputs := make([]map[string]string, 0, len(e.results))
	var succeeded, failed int
	for _, node := range e.nodes.matched {
		status := e.nodeRunStatus(node, true)
		entry := nodeResult{Node: node.Node, Status: string(status)}
		if !node.Disabled {
			entry.Name, entry.Params, entry.DAGRunID = node.Run.DAGName, node.Run.Params, node.Run.RunID
			if result, ok := e.results[node.Run.RunID]; ok {
				entry.Name, entry.Params = result.Name, result.Params
				entry.Outputs, entry.OutputValues = result.Outputs, result.OutputValues
				if result.Status.IsSuccess() && result.Outputs != nil {
					outputs = append(outputs, result.Outputs)
				}
			}
			switch status {
			case ir.NodeRunNotStarted:
			case ir.NodeRunSucceeded, ir.NodeRunPartiallySucceeded:
				succeeded++
			default:
				failed++
			}
		}
		entries = append(entries, entry)
	}
	return entries, outputs, succeeded, failed
}

// outputNodeResults writes the aggregate of a step with nodes. When it would
// exceed max_output_size, it leaves out the outputs, which each child run
// keeps.
func (e *parallelExecutor) outputNodeResults() error {
	e.lock.Lock()
	entries, outputs, succeeded, failed := e.nodeEntries()
	limit := e.nodes.maxOutputSize
	stdout := e.stdout
	e.lock.Unlock()

	type summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	}
	aggregate := struct {
		Summary summary             `json:"summary"`
		Results []nodeResult        `json:"results"`
		Outputs []map[string]string `json:"outputs"`
	}{summary{len(entries), succeeded, failed}, entries, outputs}

	data, err := json.MarshalIndent(aggregate, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal outputs: %w", err)
	}
	if len(data) >= limit {
		for i := range aggregate.Results {
			aggregate.Results[i].Outputs, aggregate.Results[i].OutputValues = nil, nil
		}
		aggregate.Outputs = []map[string]string{}
		if data, err = json.MarshalIndent(aggregate, "", "  "); err != nil {
			return fmt.Errorf("failed to marshal outputs: %w", err)
		}
	}
	if stdout == nil {
		return nil
	}
	if _, err := stdout.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write outputs: %w", err)
	}
	return nil
}

// nodeStatusDetails lists the matched nodes in name order with their
// statuses. A disabled node shows as skipped.
func (e *parallelExecutor) nodeStatusDetails() []ir.NodeStatusDetail {
	details := make([]ir.NodeStatusDetail, 0, len(e.nodes.matched))
	for _, node := range e.nodes.matched {
		status := ir.NodeSkipped
		if !node.Disabled {
			_, started := e.nodes.started[node.Run.RunID]
			result, finished := e.results[node.Run.RunID]
			switch {
			case finished:
				status = parallelNodeStatus(result.Status)
			case e.cancelled():
				status = ir.NodeAborted
			case started:
				status = ir.NodeFailed
			default:
				status = ir.NodeNotStarted
			}
		}
		details = append(details, ir.NodeStatusDetail{Label: node.Node, Status: status})
	}
	return details
}

func init() {
	executor.RegisterExecutor(ir.ExecutorTypeNodes, newNodesExecutor, nil, registry.ExecutorCapabilities{SubDAG: true})
}
