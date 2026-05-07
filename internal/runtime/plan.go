// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
)

var (
	ErrCyclicPlan  = errors.New("cyclic plan detected")
	ErrMissingNode = errors.New("missing node in execution plan")
)

// Plan represents a plan of execution for a set of steps.
// It encapsulates the graph structure and ensures thread-safe access.
type Plan struct {
	startedAt  time.Time
	finishedAt time.Time

	// Graph structure (immutable after construction)
	nodes      []*Node
	nodeByID   map[int]*Node
	nodeByName map[string]*Node

	// Immutable adjacency lists (exposing for unit tests)
	DependencyMap map[int][]int // node ID -> list of dependency node IDs (upstream)
	DependantMap  map[int][]int // node ID -> list of dependent node IDs (downstream)

	mu sync.RWMutex
}

// NewPlan creates a new execution plan from the given steps.
// It builds the graph, validates it (checking for cycles), and returns the plan.
func NewPlan(steps ...core.Step) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		DependencyMap: make(map[int][]int),
		DependantMap:  make(map[int][]int),
		nodes:         make([]*Node, 0, len(steps)),
		startedAt:     time.Now(),
	}

	// Initialize nodes
	for _, step := range steps {
		node := &Node{Data: newSafeData(NodeData{Step: step})}
		node.Init()
		p.addNode(node)
	}

	// Build edges
	if err := p.buildEdges(); err != nil {
		return nil, err
	}

	return p, nil
}

// CreateRetryPlan creates a new execution plan for retrying specific nodes.
func CreateRetryPlan(ctx context.Context, dag *core.DAG, nodes ...*Node) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		DependencyMap: make(map[int][]int),
		DependantMap:  make(map[int][]int),
		nodes:         make([]*Node, 0, len(nodes)),
		startedAt:     time.Now(),
	}

	steps := stepsByName(dag)

	if err := rebindRetryNodesToSteps(nodes, steps); err != nil {
		return nil, err
	}

	// Initialize nodes
	for _, node := range nodes {
		node.Init()
		p.addNode(node)
	}

	// Build edges
	if err := p.buildEdges(); err != nil {
		return nil, err
	}

	// Setup retry state
	if err := p.setupRetry(ctx, steps); err != nil {
		return nil, err
	}

	return p, nil
}

// NewPlanFromNodes creates a plan from existing nodes without modifying their states.
func NewPlanFromNodes(nodes ...*Node) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		DependencyMap: make(map[int][]int),
		DependantMap:  make(map[int][]int),
		nodes:         make([]*Node, 0, len(nodes)),
		startedAt:     time.Now(),
	}

	for _, node := range nodes {
		node.Init()
		p.addNode(node)
	}

	if err := p.buildEdges(); err != nil {
		return nil, err
	}

	return p, nil
}

// CreateStepRetryPlan creates a new execution plan for retrying a specific step.
func CreateStepRetryPlan(dag *core.DAG, nodes []*Node, stepName string) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		DependencyMap: make(map[int][]int),
		DependantMap:  make(map[int][]int),
		nodes:         make([]*Node, 0, len(nodes)),
		startedAt:     time.Now(),
	}

	steps := stepsByName(dag)

	if err := rebindRetryNodesToSteps(nodes, steps); err != nil {
		return nil, err
	}

	for _, node := range nodes {
		node.Init()
		p.addNode(node)
	}

	if err := p.buildEdges(); err != nil {
		return nil, err
	}

	targetNode := p.GetNodeByName(stepName)
	if targetNode == nil {
		return nil, fmt.Errorf("%w: %s", ErrMissingNode, stepName)
	}
	preserveRetryBudget := targetNode.State().Status == core.NodeRetrying

	step, err := retryStepForNode(targetNode, steps)
	if err != nil {
		return nil, err
	}

	retryCount := targetNode.GetRetryCount()
	targetNode.ClearState(step)
	targetNode.SetRetryCount(retryCount)
	if !preserveRetryBudget {
		targetNode.retryPolicy = RetryPolicy{} // manual step retries start with a fresh retry budget
	}

	return p, nil
}

// addNode adds a node to the plan structures.
func (p *Plan) addNode(node *Node) {
	p.nodeByID[node.id] = node
	p.nodeByName[node.Name()] = node
	p.nodes = append(p.nodes, node)
}

// buildEdges populates dependency edges and validates acyclicity.
func (p *Plan) buildEdges() error {
	for _, node := range p.nodes {
		for _, depName := range node.Step().Depends {
			depNode, ok := p.nodeByName[depName]
			if !ok {
				return fmt.Errorf("%w: %s", ErrMissingNode, depName)
			}
			p.addEdge(depNode, node)
		}
	}

	if p.isCyclic() {
		return ErrCyclicPlan
	}
	return nil
}

// rebindRetryNodesToSteps makes the restored DAG definition authoritative for
// retry execution. Persisted retry nodes carry mutable runtime state, but their
// embedded Step snapshots can be lossy because some nested config is excluded
// from JSON serialization for security reasons.
func rebindRetryNodesToSteps(nodes []*Node, steps map[string]core.Step) error {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		step, err := retryStepForNode(node, steps)
		if err != nil {
			return err
		}
		node.SetStep(step)
	}
	return nil
}

func retryStepForNode(node *Node, steps map[string]core.Step) (core.Step, error) {
	step, ok := steps[node.Name()]
	if !ok {
		return core.Step{}, fmt.Errorf("%w: %s", ErrMissingNode, node.Name())
	}
	if workingDir := node.State().WorkingDir; workingDir != "" {
		step.Dir = workingDir
	}
	return step, nil
}

// addEdge adds a directed edge from 'from' to 'to'.
func (p *Plan) addEdge(from, to *Node) {
	p.DependantMap[from.id] = append(p.DependantMap[from.id], to.id)
	p.DependencyMap[to.id] = append(p.DependencyMap[to.id], from.id)
}

// isCyclic checks for cycles in the graph using Kahn's algorithm.
func (p *Plan) isCyclic() bool {
	inDegrees := make(map[int]int)
	for id, deps := range p.DependencyMap {
		inDegrees[id] = len(deps)
	}

	var queue []int
	for _, node := range p.nodes {
		if inDegrees[node.id] == 0 {
			queue = append(queue, node.id)
		}
	}

	processedCount := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		processedCount++

		for _, v := range p.DependantMap[u] {
			inDegrees[v]--
			if inDegrees[v] == 0 {
				queue = append(queue, v)
			}
		}
	}

	return processedCount != len(p.nodes)
}

// setupRetry resets the state of failed/aborted nodes and their dependents.
func (p *Plan) setupRetry(ctx context.Context, steps map[string]core.Step) error {
	// Identify nodes that need to be retried (failed or aborted)
	toRetry := make(map[int]bool)
	nodeStatus := make(map[int]core.NodeStatus)

	for _, node := range p.nodes {
		nodeStatus[node.id] = node.Status()
		toRetry[node.id] = false
	}

	var frontier []int
	for _, node := range p.nodes {
		if len(p.DependencyMap[node.id]) == 0 {
			frontier = append(frontier, node.id)
		}
	}

	for len(frontier) > 0 {
		var next []int
		for _, u := range frontier {
			shouldRetry := toRetry[u] ||
				nodeStatus[u] == core.NodeFailed ||
				nodeStatus[u] == core.NodeRetrying ||
				nodeStatus[u] == core.NodeAborted ||
				nodeStatus[u] == core.NodeRejected

			if shouldRetry {
				node := p.nodeByID[u]
				logger.Debug(ctx, "Clearing node state",
					tag.Step(node.Name()),
				)
				step, err := retryStepForNode(node, steps)
				if err != nil {
					return err
				}
				node.ClearState(step)
				toRetry[u] = true
			}

			for _, v := range p.DependantMap[u] {
				if toRetry[u] {
					toRetry[v] = true
				}
				next = append(next, v)
			}
		}
		frontier = next
	}

	return nil
}

// --- Accessors ---

// Nodes returns a slice of all nodes in the plan.
func (p *Plan) Nodes() []*Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// Return a copy to prevent modification of the underlying slice
	nodes := make([]*Node, len(p.nodes))
	copy(nodes, p.nodes)
	return nodes
}

// GetNode returns the node with the given ID.
func (p *Plan) GetNode(id int) *Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nodeByID[id]
}

// GetNodeByName returns the node with the given name.
func (p *Plan) GetNodeByName(name string) *Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nodeByName[name]
}

// Dependencies returns the IDs of the nodes that the given node depends on.
func (p *Plan) Dependencies(nodeID int) []int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	deps := p.DependencyMap[nodeID]
	result := make([]int, len(deps))
	copy(result, deps)
	return result
}

// Dependents returns the IDs of the nodes that depend on the given node.
func (p *Plan) Dependents(nodeID int) []int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	deps := p.DependantMap[nodeID]
	result := make([]int, len(deps))
	copy(result, deps)
	return result
}

// --- Status & Time ---

func (p *Plan) StartAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.startedAt
}

func (p *Plan) FinishAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.finishedAt
}

func (p *Plan) Duration() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.finishedAt.IsZero() {
		return time.Since(p.startedAt)
	}
	return p.finishedAt.Sub(p.startedAt)
}

func (p *Plan) IsStarted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.startedAt.IsZero()
}

func (p *Plan) IsFinished() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.finishedAt.IsZero()
}

func (p *Plan) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishedAt = time.Now()
}

// PlanNodeStates holds the state flags for nodes in a plan.
type PlanNodeStates struct {
	HasRunning    bool
	HasRetrying   bool
	HasWaiting    bool
	HasNotStarted bool
	HasRejected   bool
}

// NodeStates returns whether any nodes are running, waiting, not started, or rejected.
// Single pass, single lock for atomic read.
func (p *Plan) NodeStates() PlanNodeStates {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var states PlanNodeStates
	for _, node := range p.nodes {
		switch node.State().Status {
		case core.NodeRunning:
			states.HasRunning = true
		case core.NodeRetrying:
			states.HasRetrying = true
		case core.NodeWaiting:
			states.HasWaiting = true
		case core.NodeNotStarted:
			states.HasNotStarted = true
		case core.NodeRejected:
			states.HasRejected = true
		case core.NodeSucceeded, core.NodeFailed, core.NodeAborted, core.NodeSkipped, core.NodePartiallySucceeded:
			// Terminal states
		}
	}
	return states
}

// WaitingStepNames returns the names of steps that are waiting for approval.
func (p *Plan) WaitingStepNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var names []string
	for _, node := range p.nodes {
		if node.State().Status == core.NodeWaiting {
			names = append(names, node.Name())
		}
	}
	return names
}

// IsRunning checks if any node is currently running or pending.
func (p *Plan) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.isRunningLocked()
}

// HasActiveNodes checks if any node is actively executing or waiting for a retry.
func (p *Plan) HasActiveNodes() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hasActiveNodesLocked()
}

// isRunningLocked is the lock-free implementation for internal use.
func (p *Plan) isRunningLocked() bool {
	for _, node := range p.nodes {
		s := node.State().Status
		if s == core.NodeRunning || s == core.NodeRetrying {
			return true
		}
		if s == core.NodeNotStarted && p.finishedAt.IsZero() {
			return true
		}
	}
	return false
}

func (p *Plan) hasActiveNodesLocked() bool {
	for _, node := range p.nodes {
		s := node.State().Status
		if s == core.NodeRunning || s == core.NodeRetrying {
			return true
		}
	}
	return false
}

// CheckFinished checks if all nodes have completed (successfully or otherwise).
func (p *Plan) CheckFinished() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, node := range p.nodes {
		if node.State().Status == core.NodeRunning ||
			node.State().Status == core.NodeRetrying ||
			node.State().Status == core.NodeNotStarted {
			return false
		}
	}
	return true
}

// NodeData returns a snapshot of data for all nodes.
func (p *Plan) NodeData() []NodeData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var ret []NodeData
	for _, node := range p.nodes {
		// Node's internal lock is handled by NodeData()
		ret = append(ret, node.NodeData())
	}
	return ret
}

// Helper
func stepsByName(dag *core.DAG) map[string]core.Step {
	m := make(map[string]core.Step, len(dag.Steps))
	for _, s := range dag.Steps {
		m[s.Name] = s
	}
	return m
}
