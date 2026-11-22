// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/ssh"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// localWorkerID is the worker ID of a run on the Dagu host itself.
const localWorkerID = "local"

// defaultSSHPort is the port ${node.port} reads for a node that sets none.
const defaultSSHPort = "22"

// boundNodeOf returns the node a run is bound to. A retry keeps the binding of
// the run it retries.
func boundNodeOf(opts Options) string {
	if opts.BoundNode == "" && opts.RetryTarget != nil {
		return opts.RetryTarget.BoundNode
	}
	return opts.BoundNode
}

// nodeTargetingError fails, before any step starts, a run that uses node
// targeting (RFC 013) in a way that is not built yet:
//
//   - a node: required DAG started without a node;
//   - a run on a worker that is bound to a node or has a step that sets
//     nodes.
func nodeTargetingError(dag *ir.DAG, boundNode, workerID string) error {
	if dag.NodeRequired && boundNode == "" {
		return fmt.Errorf("DAG %q declares node: required, so it runs only bound to a node, as the child of a dag.run step with nodes", dag.Name)
	}
	if workerID == "" || workerID == localWorkerID {
		return nil
	}
	if boundNode != "" {
		return fmt.Errorf("the run is bound to inventory node %q, and running a bound run on a worker is not available yet", boundNode)
	}
	for _, step := range allSteps(dag) {
		if step.Nodes != nil {
			return fmt.Errorf("step %q sets nodes, and running a step on inventory nodes from a worker is not available yet", step.Name)
		}
	}
	return nil
}

// nodeBinding is the inventory node a bound run runs on, with the node it
// connects through.
type nodeBinding struct {
	node    inventory.ResolvedNode
	bastion *inventory.ResolvedNode
	// connects reports a run with a step that reaches the node over SSH.
	connects bool
}

// lookupBoundNode reads the node a bound run runs on from the project's
// inventories. It returns nil for a run that is not bound.
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
	case node.Disabled:
		return nil, fmt.Errorf("node %q is disabled", node.Name)
	}
	binding := &nodeBinding{node: *node, connects: slices.ContainsFunc(allSteps(a.dag), reachesNode)}
	if node.Bastion != "" {
		if binding.bastion = find(node.Bastion); binding.bastion == nil {
			return nil, fmt.Errorf("bastion %q of node %q is not in the project's inventories", node.Bastion, node.Name)
		}
	}
	if err := missingNodeLabel(a.dag, binding.valueNode()); err != nil {
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
	port := nodePort(b.node)
	if port == "" {
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

// missingNodeLabel fails a run that reads a label its node lacks, naming the
// node and the key. It reads the fields of dag that resolve values, as
// stored, so text such as a description is not checked.
func missingNodeLabel(dag *ir.DAG, node *cmnvalue.BoundNode) error {
	for _, field := range spec.ReferenceFields(dag) {
		if err := cmnvalue.MissingNodeLabel(field.Value, node); err != nil {
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
	cfg := ir.SSHConfig{SSHAuth: b.node.Auth}
	if b.bastion != nil {
		cfg.Bastion = &ir.BastionConfig{SSHAuth: b.bastion.Auth}
	}
	return cfg.SecretRefs("nodes." + b.node.Name)
}

// sshConfig returns the connection to the node, through its bastion. Each hop
// accepts only its own node's pinned or trusted host key.
func (b *nodeBinding) sshConfig(trusted ssh.TrustedHostKeys) (ssh.Config, error) {
	shell, shellArgs, err := splitShell(b.node.Shell)
	if err != nil {
		return ssh.Config{}, fmt.Errorf("node %q: invalid shell: %w", b.node.Name, err)
	}
	cfg := ssh.Config{
		User:        b.node.User,
		Host:        b.node.Host,
		Port:        nodePort(b.node),
		SSHAuth:     b.node.Auth,
		Shell:       shell,
		ShellArgs:   shellArgs,
		Timeout:     b.node.Timeout,
		NodeHostKey: nodeHostKey(b.node, trusted),
	}
	if b.bastion != nil {
		cfg.Bastion = &ssh.BastionConfig{
			Host:        b.bastion.Host,
			Port:        nodePort(*b.bastion),
			User:        b.bastion.User,
			SSHAuth:     b.bastion.Auth,
			NodeHostKey: nodeHostKey(*b.bastion, trusted),
		}
	}
	return cfg, nil
}

func nodeHostKey(node inventory.ResolvedNode, trusted ssh.TrustedHostKeys) *ssh.NodeHostKey {
	return &ssh.NodeHostKey{
		Node:         node.Name,
		Pinned:       node.HostKey,
		AcceptAnyKey: !node.StrictHostKey,
		Trusted:      trusted,
	}
}

// nodePort returns the node's SSH port, or empty for the default.
func nodePort(node inventory.ResolvedNode) string {
	if node.Port == 0 {
		return ""
	}
	return strconv.Itoa(node.Port)
}

// splitShell splits a node's shell, such as "bash -e", into the shell and
// its arguments.
func splitShell(shell string) (string, []string, error) {
	if strings.TrimSpace(shell) == "" {
		return "", nil, nil
	}
	return cmdutil.SplitCommand(shell)
}

// reachesNode reports whether a step of a run bound to an SSH node runs on
// the node: a command step, or an ssh.run or sftp.* step that names no other
// host (RFC 013 §Bound runs). Other actions run where the run runs, as under
// DAG-level ssh.
func reachesNode(step ir.Step) bool {
	switch step.ExecutorConfig.Type {
	case "ssh", "sftp":
		_, host := step.ExecutorConfig.Config["host"]
		_, ip := step.ExecutorConfig.Config["ip"]
		return !host && !ip
	default:
		return isCommandStep(step)
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
