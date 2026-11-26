// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/sshconn"
)

// localWorkerID is the worker ID of a run on the Dagu host itself.
const localWorkerID = "local"

// defaultSSHPort is the port ${node.port} reads for a node that sets none.
const defaultSSHPort = "22"

// boundNodeOf returns the node a run is bound to. A retry keeps the binding of
// the run it retries, and a node: required DAG started with one node is
// bound to it.
func boundNodeOf(dag *ir.DAG, opts Options) string {
	if opts.BoundNode != "" {
		return opts.BoundNode
	}
	if opts.RetryTarget != nil && opts.RetryTarget.BoundNode != "" {
		return opts.RetryTarget.BoundNode
	}
	if listed := targetNodesOf(opts); dag != nil && dag.NodeRequired && len(listed) == 1 {
		return listed[0]
	}
	return ""
}

// targetNodesOf returns the nodes a run is narrowed to. A retry keeps the
// list of the run it retries.
func targetNodesOf(opts Options) []string {
	if len(opts.TargetNodes) == 0 && opts.RetryTarget != nil {
		return opts.RetryTarget.TargetNodes
	}
	return opts.TargetNodes
}

// nodeListOf returns the record of what a narrowed run's steps with nodes do
// with its list. A dag.run child run adds to its parent's, and a retry
// continues the record of the attempt it retries.
func nodeListOf(opts Options) *runctx.NodeListOutcome {
	if opts.NodeList != nil {
		return opts.NodeList
	}
	if len(targetNodesOf(opts)) == 0 {
		return nil
	}
	var from *ir.NodeListRecord
	if opts.RetryTarget != nil {
		from = opts.RetryTarget.NodeList
	}
	return runctx.NewNodeListOutcome(from)
}

// checksNodeList reports whether the run fails when its node list selected
// no node. A child run does not: its steps count toward its parent's record.
func (a *Agent) checksNodeList() bool {
	return len(a.targetNodes) > 0 && a.parentDAGRun.Zero()
}

// nodeListRecord returns the record a run that checks its node list keeps
// in its status, so a retry continues it.
func (a *Agent) nodeListRecord() *ir.NodeListRecord {
	if !a.checksNodeList() {
		return nil
	}
	return a.nodeList.Record()
}

// nodeTargetingError fails, before any step starts, a run that uses node
// targeting (RFC 013) in a way that is not built yet:
//
//   - a node: required DAG started without a node;
//   - a run on a worker that is bound to another node than that worker, or
//     has a step that sets nodes.
func nodeTargetingError(dag *ir.DAG, boundNode, workerID string) error {
	if dag.NodeRequired && boundNode == "" {
		return fmt.Errorf("DAG %q declares node: required, so it runs only bound to a node, as the child of a dag.run step with nodes or started with one node (--node)", dag.Name)
	}
	if workerID == "" || workerID == localWorkerID {
		return nil
	}
	if boundNode != "" && !strings.EqualFold(boundNode, workerID) {
		return fmt.Errorf("the run is bound to inventory node %q, and running a bound run on a worker is not available yet", boundNode)
	}
	for _, step := range allSteps(dag) {
		if step.Nodes != nil {
			return fmt.Errorf("step %q sets nodes, and running a step on inventory nodes from a worker is not available yet", step.Name)
		}
	}
	return nil
}

// nodeBinding is the node a bound run runs on, with the node it connects
// through.
type nodeBinding struct {
	node    inventory.ResolvedNode
	bastion *inventory.ResolvedNode
	// connects reports a run bound to an SSH node with a step that reaches
	// the node over SSH.
	connects bool
}

// lookupBoundNode reads the node a bound run runs on from the project's
// nodes. A run bound to a worker node runs only on that worker, and its
// steps run there, so it reaches no node over SSH. It returns nil for a run
// that is not bound.
func (a *Agent) lookupBoundNode(ctx context.Context) (*nodeBinding, error) {
	if a.boundNode == "" {
		return nil, nil
	}
	if a.nodeSelector == nil {
		return nil, fmt.Errorf("the run is bound to node %q, but the node inventory is not available", a.boundNode)
	}
	nodes, err := a.nodeSelector.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read node %q: %w", a.boundNode, err)
	}
	find := func(name string) *inventory.ResolvedNode {
		i := slices.IndexFunc(nodes, func(n inventory.ResolvedNode) bool { return strings.EqualFold(n.Name, name) })
		if i < 0 {
			return nil
		}
		return &nodes[i]
	}
	node := find(a.boundNode)
	switch {
	case node == nil:
		return nil, fmt.Errorf("node %q is not in the project's inventories", a.boundNode)
	case node.Kind == inventory.NodeKindWorker && !strings.EqualFold(node.Name, a.workerID):
		return nil, fmt.Errorf("the run is bound to worker node %q, so it runs only on that worker", node.Name)
	case node.Kind == inventory.NodeKindWorker:
		steps := allSteps(a.dag)
		if i := slices.IndexFunc(steps, connectsToNode); i >= 0 {
			return nil, fmt.Errorf("step %q connects to its node over SSH, but worker node %q has no SSH connection; set with.host", steps[i].Name, node.Name)
		}
	case node.Disabled:
		return nil, fmt.Errorf("node %q is disabled", node.Name)
	}
	binding := &nodeBinding{
		node:     *node,
		connects: node.Kind == inventory.NodeKindSSH && slices.ContainsFunc(allSteps(a.dag), reachesNode),
	}
	if node.Bastion != "" {
		if binding.bastion = find(node.Bastion); binding.bastion == nil {
			return nil, fmt.Errorf("bastion %q of node %q is not in the project's inventories", node.Bastion, node.Name)
		}
	}
	if err := checkNodeReferences(a.dag, binding.valueNode()); err != nil {
		return nil, err
	}
	return binding, nil
}

// valueNode returns the node as ${node.*} reads it.
func (b *nodeBinding) valueNode() *cmnvalue.BoundNode {
	labels := make(map[string]string, len(b.node.Labels))
	for _, label := range b.node.Labels {
		labels[label.Key] = label.Value
	}
	// A worker node has no port or user.
	port := nodePort(b.node)
	if port == "" && b.node.Kind == inventory.NodeKindSSH {
		port = defaultSSHPort
	}
	return &cmnvalue.BoundNode{
		Name:        b.node.Name,
		Host:        b.node.Host,
		Port:        port,
		User:        b.node.User,
		Description: b.node.Description,
		Labels:      labels,
	}
}

// checkNodeReferences fails a run that reads a node path the namespace does
// not define or a label its node lacks, naming it. It reads the fields of dag
// that resolve values, as stored and by each field's rules, so text such as a
// description is not checked.
func checkNodeReferences(dag *ir.DAG, node *cmnvalue.BoundNode) error {
	for _, field := range spec.ReferenceFields(dag) {
		if err := field.Field.CheckNodeReferences(field.Value, node); err != nil {
			return err
		}
	}
	return nil
}

// credentialRefs returns the secret refs of the node's credentials and its
// bastion's, named by field. A run that never connects needs none.
func (b *nodeBinding) credentialRefs() []secretref.Ref {
	if b == nil || !b.connects {
		return nil
	}
	return sshconn.NodeSecretRefs(b.node, b.bastion)
}

// sshConfig returns the connection to the node, through its bastion.
func (b *nodeBinding) sshConfig(trusted sshconn.TrustedHostKeys) (sshconn.Config, error) {
	return sshconn.NodeConfig(b.node, b.bastion, trusted)
}

// nodePort returns the node's SSH port, or empty for the default.
func nodePort(node inventory.ResolvedNode) string {
	if node.Port == 0 {
		return ""
	}
	return strconv.Itoa(node.Port)
}

// reachesNode reports whether a step of a run bound to an SSH node runs on
// the node: a command step, or an ssh.run or sftp.* step that names no other
// host (RFC 013 §Bound runs). Other actions run where the run runs, as under
// DAG-level ssh.
func reachesNode(step ir.Step) bool {
	return connectsToNode(step) || isCommandStep(step)
}

// connectsToNode reports an ssh.run or sftp.* step that names no host, so it
// connects to the node the run is bound to.
func connectsToNode(step ir.Step) bool {
	switch step.ExecutorConfig.Type {
	case "ssh", "sftp":
		_, host := step.ExecutorConfig.Config["host"]
		_, ip := step.ExecutorConfig.Config["ip"]
		return !host && !ip
	default:
		return false
	}
}

// isCommandStep reports whether a step runs on the command executor: a step
// with no action, or one typed command or shell. In a bound run such a step
// runs on the node, never on the Dagu host.
func isCommandStep(step ir.Step) bool {
	switch step.ExecutorConfig.Type {
	case "", "command", "shell":
		return step.HumanTask == nil
	default:
		return false
	}
}

// onNode returns a copy of dag whose command steps run over SSH, as
// DAG-level ssh makes them. dag is left unchanged.
func onNode(dag *ir.DAG) *ir.DAG {
	bound := dag.Clone()
	bound.Steps = routeSteps(dag.Steps)
	for _, handler := range []**ir.Step{
		&bound.HandlerOn.Init, &bound.HandlerOn.Failure, &bound.HandlerOn.Success,
		&bound.HandlerOn.Abort, &bound.HandlerOn.Exit, &bound.HandlerOn.Wait,
	} {
		if *handler != nil {
			routed := routeStep(**handler)
			*handler = &routed
		}
	}
	return bound
}

func routeSteps(steps []ir.Step) []ir.Step {
	routed := slices.Clone(steps)
	for i := range routed {
		routed[i] = routeStep(routed[i])
	}
	return routed
}

// routeStep returns step, and its foreach body, with command steps running
// over SSH.
func routeStep(step ir.Step) ir.Step {
	if isCommandStep(step) {
		step.ExecutorConfig.Type = "ssh"
	}
	if step.Foreach != nil {
		foreach := *step.Foreach
		foreach.Steps = routeSteps(foreach.Steps)
		step.Foreach = &foreach
	}
	return step
}

// allSteps returns the steps of dag, including foreach bodies and handlers.
func allSteps(dag *ir.DAG) []ir.Step {
	var steps []ir.Step
	var add func(ir.Step)
	add = func(step ir.Step) {
		steps = append(steps, step)
		if step.Foreach != nil {
			for _, child := range step.Foreach.Steps {
				add(child)
			}
		}
	}
	for _, step := range dag.Steps {
		add(step)
	}
	for _, handler := range handlerSteps(dag) {
		add(*handler)
	}
	return steps
}

// handlerSteps returns the lifecycle handlers dag sets.
func handlerSteps(dag *ir.DAG) []*ir.Step {
	var handlers []*ir.Step
	for _, handler := range []*ir.Step{dag.HandlerOn.Init, dag.HandlerOn.Failure, dag.HandlerOn.Success, dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait} {
		if handler != nil {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}
