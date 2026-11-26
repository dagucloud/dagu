// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// maxStepNodes is the most nodes one step runs on, as Spec 018 limits the
// items of parallel.
const maxStepNodes = 1000

// errNoNodeInventory reports a run that cannot read the inventory.
var errNoNodeInventory = errors.New("the node inventory is not available to this run")

// errStepNotListed reports a step whose matched nodes the run's node list
// names none of. The step is skipped, and does not block the steps after it
// (RFC 013 §Narrowing a run).
var errStepNotListed = errors.New("the run's node list names none of the nodes the step selects")

// runsOnEachNode reports a command step with nodes. Its timeout and retry
// policy apply to each node's child run, so one hung or failing host does not
// use up the others' time or rerun them.
func runsOnEachNode(step ir.Step) bool {
	return step.Nodes != nil && step.SubDAG == nil
}

// setupNodesExecutor prepares a step with nodes (RFC 013). It finds the
// step's nodes and gives each enabled node a child run bound to it. A command
// step's command resolves in each child run, so the parent resolves none of
// it. An executor from override replaces every node's child run, so the
// selector is not matched and no child is recorded.
func (n *Node) setupNodesExecutor(ctx context.Context, override ExecutorOverride) (context.Context, executor.Executor, error) {
	if cmd, ok := replacementExecutor(ctx, override, n.Step()); ok {
		n.cmd = cmd
		if err := n.outputs.setupExecutorIO(ctx, cmd, n.NodeData()); err != nil {
			return ctx, nil, fmt.Errorf("failed to set up step output: %w", err)
		}
		return ctx, cmd, nil
	}

	runs, err := n.stepNodes(ctx)
	if err != nil {
		return ctx, nil, err
	}
	GetDAGContext(ctx).NodeList.MarkRan()

	child, err := n.resolveNodeChild(ctx)
	if err != nil {
		return ctx, nil, err
	}

	for i, node := range runs {
		// A disabled or removed node keeps the child run an earlier attempt
		// gave it, so a retry that finds it able to run again retries that
		// run. One that never had a child run gets none.
		if !node.Runs() && node.Run.RunID == "" {
			continue
		}
		run := child
		run.Node = node.Node
		if node.Worker {
			run.TargetWorkerID = node.Node
		}
		run.RunID = node.Run.RunID
		if run.RunID == "" {
			run.RunID = GenerateSubDAGRunIDForTarget(ctx, child.DAGName, child.Params+"\x00node="+node.Node, false)
		}
		runs[i].Run = run
	}

	// A child run whose params read a masked output never starts.
	if err := maskedReadError(ctx, n); err != nil {
		return ctx, nil, err
	}

	execStep := n.Step()
	execStep.ExecutorConfig = ir.ExecutorConfig{Type: ir.ExecutorTypeNodes}
	cmd, err := executor.NewExecutor(ctx, execStep)
	if err != nil {
		return ctx, nil, err
	}
	n.cmd = cmd
	if err := n.outputs.setupExecutorIO(ctx, cmd, n.NodeData()); err != nil {
		return ctx, nil, fmt.Errorf("failed to set up step output: %w", err)
	}

	nodesExecutor, ok := cmd.(executor.NodesExecutor)
	if !ok {
		return ctx, nil, fmt.Errorf("action %q does not run steps on nodes", execStep.ExecutorConfig.Type)
	}
	var subRuns []executor.RunParams
	for _, run := range runs {
		if run.Runs() {
			subRuns = append(subRuns, run.Run)
		}
	}
	n.SetSubRuns(subRunsFromParams(subRuns))
	nodesExecutor.SetNodes(runs)
	return ctx, cmd, nil
}

// stepNodes returns the nodes of a step with nodes, in name order. A retry
// keeps the nodes the step's first attempt recorded (RFC 013 §Retry). A step
// that records none, or whose records cannot be read, matches its selector.
func (n *Node) stepNodes(ctx context.Context) ([]executor.NodeRun, error) {
	if dir := n.State().RetryNodeRunsDir; dir != "" {
		records, err := noderuns.Read(dir)
		if err == nil {
			return recordedNodes(ctx, records)
		}
		logger.Warn(ctx, "Failed to read the nodes the step ran on; matching its selector again",
			tag.Dir(dir), tag.Error(err))
	}
	matched, err := matchStepNodes(ctx, n.Step().Nodes.Selector)
	if err != nil {
		return nil, err
	}
	runs := make([]executor.NodeRun, len(matched))
	for i, node := range matched {
		runs[i] = executor.NodeRun{Node: node.Name, Disabled: node.Disabled, Worker: node.Kind == inventory.NodeKindWorker}
	}
	return runs, nil
}

// recordedNodes returns the nodes an earlier attempt of a step recorded, each
// with its child run, as the project holds them now. A node whose child run
// succeeded keeps its result. Any other node is removed when it has left the
// inventory, and disabled when it is disabled now. A node added since is not
// added. The step fails when no node can run or keep its result.
func recordedNodes(ctx context.Context, records ir.NodeRuns) ([]executor.NodeRun, error) {
	inventoryNodes := GetDAGContext(ctx).Nodes
	if inventoryNodes == nil {
		return nil, errNoNodeInventory
	}
	current, err := inventoryNodes.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the nodes: %w", err)
	}
	byName := make(map[string]inventory.ResolvedNode, len(current))
	for _, node := range current {
		byName[strings.ToLower(node.Name)] = node
	}

	runs := make([]executor.NodeRun, len(records.Runs))
	runnable := false
	for i, record := range records.Runs {
		run := executor.NodeRun{Node: record.Node, Run: executor.RunParams{RunID: record.DAGRunID}}
		node, found := byName[strings.ToLower(record.Node)]
		switch {
		case record.DAGRunID != "" && (record.Status == ir.NodeRunSucceeded || record.Status == ir.NodeRunPartiallySucceeded):
			run.Reuse = true
		case !found:
			run.Removed = true
		case node.Disabled:
			run.Disabled = true
		}
		run.Worker = found && node.Kind == inventory.NodeKindWorker
		runnable = runnable || run.Runs()
		runs[i] = run
	}
	if !runnable {
		return nil, fmt.Errorf("none of the %d nodes the step ran on before can run: each has left the inventory or is disabled", len(runs))
	}
	return runs, nil
}

// nodeRunsToRetry returns the directory of the records whose nodes a retry
// of a step with nodes keeps: the records the step's last attempt wrote, or,
// when it wrote none, the records that attempt kept. A step a seeded run
// carried over from another run holds that run's log paths, whose records
// name that run's child runs, so it keeps none of them.
func nodeRunsToRetry(step ir.Step, state NodeState) string {
	if step.Nodes == nil {
		return ""
	}
	if dir := noderuns.Dir(state.Stdout, step.Name); dir != "" && !state.SkippedByRetry && noderuns.Exists(dir) {
		return dir
	}
	return state.RetryNodeRunsDir
}

// resolveNodeChild returns what every node's child run shares: for dag.run,
// the child DAG and its params; for a command step, this DAG, which the child
// narrows to the step.
func (n *Node) resolveNodeChild(ctx context.Context) (executor.RunParams, error) {
	subDAG := n.Step().SubDAG
	if subDAG == nil {
		// The command reads what it would read without nodes, the outputs of
		// earlier steps included (RFC 013 §The nodes field).
		return executor.RunParams{
			DAGName:   GetDAGContext(ctx).DAG.Name,
			PassedEnv: stepOutputEnvs(GetEnv(ctx).Scope),
		}, nil
	}
	name, err := resolveRuntimeString(ctx, subDAG.Name, cmnvalue.SubDAGNameField("sub_dag.name"))
	if err != nil {
		return executor.RunParams{}, fmt.Errorf("failed to eval sub DAG name: %w", err)
	}
	resolved := *subDAG
	resolved.Name = name
	n.SetSubDAG(resolved)

	params, err := resolveRuntimeString(ctx, subDAG.Params, cmnvalue.SubDAGParamsField("sub_dag.params"))
	if err != nil {
		return executor.RunParams{}, fmt.Errorf("failed to eval sub dag params: %w", err)
	}
	passedEnv, err := resolveSubDAGPassEnv(ctx, subDAG.PassEnv)
	if err != nil {
		return executor.RunParams{}, err
	}
	return executor.RunParams{DAGName: name, Params: params, PassedEnv: passedEnv}, nil
}

// stepOutputEnvs returns the output variables of earlier steps in scope, as
// NAME=value. An output restored with a secret masked holds no value to pass.
func stepOutputEnvs(scope *cmnvalue.EnvScope) []string {
	outputs := scope.AllBySource(cmnvalue.EnvSourceOutput)
	envs := make([]string, 0, len(outputs))
	for _, name := range slices.Sorted(maps.Keys(outputs)) {
		if entry, ok := scope.GetEntry(name); ok && !entry.Masked {
			envs = append(envs, name+"="+entry.Value)
		}
	}
	return envs
}

// matchStepNodes returns the nodes a step's selector matches and the run's
// node list names, in name order. A step that would run on no node, or on
// more than maxStepNodes, fails, unless only the list leaves it no node.
func matchStepNodes(ctx context.Context, selector string) ([]inventory.ResolvedNode, error) {
	resolved, err := resolveNodeSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	nodes := GetDAGContext(ctx).Nodes
	if nodes == nil {
		return nil, errNoNodeInventory
	}
	matched, err := nodes.Select(ctx, resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to match nodes %q: %w", resolved, err)
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("selector matched no nodes: %s", resolved)
	}
	if listed := GetDAGContext(ctx).TargetNodes; len(listed) > 0 {
		matched = slices.DeleteFunc(matched, func(node inventory.ResolvedNode) bool {
			return !slices.ContainsFunc(listed, func(name string) bool { return strings.EqualFold(name, node.Name) })
		})
		if len(matched) == 0 {
			return nil, errStepNotListed
		}
	}
	if len(matched) > maxStepNodes {
		return nil, fmt.Errorf("selector %s matched %d nodes, more than the %d a step runs on", resolved, len(matched), maxStepNodes)
	}
	for _, node := range matched {
		if !node.Disabled {
			return matched, nil
		}
	}
	return nil, fmt.Errorf("selector %s matched only disabled nodes", resolved)
}

// resolveNodeSelector resolves the references in a selector. Each part of a
// term that holds a reference must resolve to one label key or value, which
// holds no ",", "!", "=", "*", or "?", so a value from a run cannot add a
// term, negate one, or widen a match (RFC 013 §References in a selector).
func resolveNodeSelector(ctx context.Context, selector string) (string, error) {
	if !strings.Contains(selector, "${") {
		return selector, nil
	}
	resolver := ValueResolverWithScope(ctx, GetEnv(ctx).Scope)
	field := cmnvalue.WorkflowField("nodes.labels")
	resolve := func(part string, valid func(string) error) (string, error) {
		if !strings.Contains(part, "${") {
			return part, nil
		}
		value, err := resolver.String(ctx, part, field)
		if err != nil {
			return "", fmt.Errorf("failed to eval nodes selector %q: %w", part, err)
		}
		if err := valid(value); err != nil {
			return "", fmt.Errorf("nodes selector %q resolved to %q: %w", part, value, err)
		}
		return value, nil
	}

	terms := strings.Split(selector, ",")
	for i, term := range terms {
		negated := strings.HasPrefix(strings.TrimSpace(term), "!")
		body := strings.TrimPrefix(strings.TrimSpace(term), "!")
		rawKey, rawValue, hasValue := strings.Cut(body, "=")
		key, err := resolve(rawKey, func(v string) error {
			return ir.ValidateLabel(ir.Label{Key: v})
		})
		if err != nil {
			return "", err
		}
		value, err := resolve(rawValue, func(v string) error {
			if v == "" || len(v) > ir.MaxLabelValueLength || !ir.ValidLabelValuePattern.MatchString(v) {
				return errors.New("not a label value")
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		resolved := key
		if hasValue {
			resolved += "=" + value
		}
		if negated {
			resolved = "!" + resolved
		}
		terms[i] = resolved
	}
	resolved := strings.Join(terms, ",")
	if _, err := inventory.ParseSelector(resolved); err != nil {
		return "", fmt.Errorf("nodes selector %q resolved to %q: %w", selector, resolved, err)
	}
	return resolved, nil
}
