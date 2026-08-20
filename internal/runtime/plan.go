// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	ErrCyclicPlan  = errors.New("cyclic plan detected")
	ErrMissingNode = errors.New("missing node in execution plan")
)

// Plan represents a plan of execution for a set of steps. Graph mutation is
// confined to planning before execution starts.
type Plan struct {
	startedAt       time.Time
	finishedAt      time.Time
	cancelRequested bool

	// Graph structure (immutable after planning)
	nodes         []*Node
	nodeByID      map[int]*Node
	nodeByName    map[string]*Node
	inferredEdges map[[2]int]struct{}
	stepRetry     *stepRetrySelection

	// Adjacency lists (immutable after planning; exposed for unit tests)
	DependencyMap map[int][]int // node ID -> list of dependency node IDs (upstream)
	DependantMap  map[int][]int // node ID -> list of dependent node IDs (downstream)

	mu sync.RWMutex
}

type stepRetrySelection struct {
	targetID int
	resetIDs map[int]struct{}
}

// NewPlan creates a new execution plan from the given steps.
// It builds the graph, validates it (checking for cycles), and returns the plan.
func NewPlan(steps ...ir.Step) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		inferredEdges: make(map[[2]int]struct{}),
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
func CreateRetryPlan(ctx context.Context, dag *ir.DAG, nodes ...*Node) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		inferredEdges: make(map[[2]int]struct{}),
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
		inferredEdges: make(map[[2]int]struct{}),
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

// StepRetryPlanOptions configures a targeted step retry plan.
type StepRetryPlanOptions struct {
	// IncludeDownstream resets the selected step and every reachable descendant.
	// Unrelated branches keep their existing status.
	IncludeDownstream bool
}

// CreateStepRetryPlan creates a new execution plan for retrying a specific step.
func CreateStepRetryPlan(dag *ir.DAG, nodes []*Node, stepName string) (*Plan, error) {
	return CreateStepRetryPlanWithOptions(dag, nodes, stepName, StepRetryPlanOptions{})
}

// CreateStepRetryPlanWithOptions creates a step retry plan with explicit options.
func CreateStepRetryPlanWithOptions(dag *ir.DAG, nodes []*Node, stepName string, opts StepRetryPlanOptions) (*Plan, error) {
	p := &Plan{
		nodeByID:      make(map[int]*Node),
		nodeByName:    make(map[string]*Node),
		inferredEdges: make(map[[2]int]struct{}),
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
	resetStepRetryNode(targetNode, targetNode.State().Status == ir.NodeRetrying)

	if opts.IncludeDownstream {
		p.stepRetry = &stepRetrySelection{
			targetID: targetNode.id,
			resetIDs: map[int]struct{}{targetNode.id: {}},
		}
		p.expandStepRetrySelection()
	}

	return p, nil
}

func resetStepRetryNode(node *Node, preserveRetryBudget bool) {
	retryCount := node.GetRetryCount()
	clearNodeForRetry(node, node.Step())
	node.SetRetryCount(retryCount)
	if !preserveRetryBudget {
		node.retryPolicy = RetryPolicy{} // manual step retries start with a fresh retry budget
	}
}

func (p *Plan) expandStepRetrySelection() {
	if p.stepRetry == nil {
		return
	}
	for _, node := range p.reachableDescendants(p.nodeByID[p.stepRetry.targetID]) {
		if _, reset := p.stepRetry.resetIDs[node.id]; reset {
			continue
		}
		resetStepRetryNode(node, false)
		p.stepRetry.resetIDs[node.id] = struct{}{}
	}
	p.markSkippedSidePrerequisites(p.stepRetry.resetIDs)
}

func (p *Plan) finalizeStepRetrySelection() {
	p.expandStepRetrySelection()
	p.stepRetry = nil
}

// reachableDescendants returns nodes reachable from the given node through
// explicit dependency edges, excluding the node itself.
func (p *Plan) reachableDescendants(node *Node) []*Node {
	if node == nil {
		return nil
	}
	seen := map[int]struct{}{node.id: {}}
	var result []*Node
	queue := []int{node.id}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, id := range p.DependantMap[current] {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			desc := p.nodeByID[id]
			if desc == nil {
				continue
			}
			result = append(result, desc)
			queue = append(queue, id)
		}
	}
	return result
}

// markSkippedSidePrerequisites converts preserved ordinary skipped
// prerequisites of reset join descendants to SkippedByRetry so the runner
// can re-execute those descendants without treating the skipped side as a
// hard skip.
func (p *Plan) markSkippedSidePrerequisites(resetIDs map[int]struct{}) {
	for id := range resetIDs {
		for _, depID := range p.DependencyMap[id] {
			if _, reset := resetIDs[depID]; reset {
				continue
			}
			dep := p.nodeByID[depID]
			if dep == nil {
				continue
			}
			state := dep.State()
			if state.Status == ir.NodeSkipped && !state.SkippedByRetry {
				dep.SetSkippedByRetry(true)
			}
		}
	}
}

// IsAgent reports whether execution order is decided by an agent step
// rather than by dependency edges.
func (p *Plan) IsAgent() bool {
	_, ok := p.nodeByName[ir.AgentStepName]
	return ok
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
func rebindRetryNodesToSteps(nodes []*Node, steps map[string]ir.Step) error {
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

func retryStepForNode(node *Node, steps map[string]ir.Step) (ir.Step, error) {
	step, ok := steps[node.Name()]
	if !ok {
		return ir.Step{}, fmt.Errorf("%w: %s", ErrMissingNode, node.Name())
	}
	return step, nil
}

// addEdge adds a directed edge from 'from' to 'to'.
func (p *Plan) addEdge(from, to *Node) bool {
	if slices.Contains(p.DependencyMap[to.id], from.id) {
		return false
	}
	p.DependantMap[from.id] = append(p.DependantMap[from.id], to.id)
	p.DependencyMap[to.id] = append(p.DependencyMap[to.id], from.id)
	return true
}

// AddInferredDependency adds one file-derived dependency before execution starts.
func (p *Plan) AddInferredDependency(producerName, consumerName string) error {
	producer := p.GetNodeByName(producerName)
	consumer := p.GetNodeByName(consumerName)
	if producer == nil {
		return fmt.Errorf("%w: %s", ErrMissingNode, producerName)
	}
	if consumer == nil {
		return fmt.Errorf("%w: %s", ErrMissingNode, consumerName)
	}
	edge := [2]int{producer.id, consumer.id}
	if !p.addEdge(producer, consumer) {
		return nil
	}
	p.inferredEdges[edge] = struct{}{}
	if p.isCyclic() {
		delete(p.inferredEdges, edge)
		p.DependantMap[producer.id] = removeNodeID(p.DependantMap[producer.id], consumer.id)
		p.DependencyMap[consumer.id] = removeNodeID(p.DependencyMap[consumer.id], producer.id)
		return ErrCyclicPlan
	}
	return nil
}

func removeNodeID(ids []int, target int) []int {
	for idx, id := range ids {
		if id == target {
			return append(ids[:idx], ids[idx+1:]...)
		}
	}
	return ids
}

// IsInferredDependency reports whether an edge was derived from matching paths.
// It may be called concurrently after planning is complete.
func (p *Plan) IsInferredDependency(producerID, consumerID int) bool {
	_, ok := p.inferredEdges[[2]int{producerID, consumerID}]
	return ok
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
func (p *Plan) setupRetry(ctx context.Context, steps map[string]ir.Step) error {
	// Identify nodes that need to be retried (failed or aborted)
	toRetry := make(map[int]bool)
	nodeStatus := make(map[int]ir.NodeStatus)

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
				nodeStatus[u] == ir.NodeFailed ||
				nodeStatus[u] == ir.NodeRetrying ||
				nodeStatus[u] == ir.NodeAborted ||
				nodeStatus[u] == ir.NodeRejected

			if shouldRetry {
				node := p.nodeByID[u]
				logger.Debug(ctx, "Clearing node state",
					tag.Step(node.Name()),
				)
				step, err := retryStepForNode(node, steps)
				if err != nil {
					return err
				}
				clearNodeForRetry(node, step)
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

func clearNodeForRetry(node *Node, step ir.Step) {
	session := node.GetAgentSession()
	previousStatus := node.State().Status
	node.ClearState(step)
	if session == nil {
		return
	}
	if session.PromptSent && (previousStatus.IsDone() || session.State == ir.AgentSessionFailed || session.State == ir.AgentSessionAborted) {
		session.StartNewGeneration()
	} else {
		session.State = ir.AgentSessionStarting
		session.LastError = ""
	}
	node.SetAgentSession(session)
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

func (p *Plan) requestCancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelRequested = true
}

func (p *Plan) isCancelRequested() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cancelRequested
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
		case ir.NodeRunning:
			states.HasRunning = true
		case ir.NodeRetrying:
			states.HasRetrying = true
		case ir.NodeWaiting:
			states.HasWaiting = true
		case ir.NodeNotStarted:
			states.HasNotStarted = true
		case ir.NodeRejected:
			states.HasRejected = true
		case ir.NodeSucceeded, ir.NodeFailed, ir.NodeAborted, ir.NodeSkipped, ir.NodePartiallySucceeded:
			// Terminal states
		}
	}
	return states
}

// WaitingStepNames returns the names of steps that require manual action.
func (p *Plan) WaitingStepNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var names []string
	for _, node := range p.nodes {
		if node.State().Status == ir.NodeWaiting {
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
		if s == ir.NodeRunning || s == ir.NodeRetrying {
			return true
		}
		if s == ir.NodeNotStarted && p.finishedAt.IsZero() {
			return true
		}
	}
	return false
}

func (p *Plan) hasActiveNodesLocked() bool {
	for _, node := range p.nodes {
		s := node.State().Status
		if s == ir.NodeRunning || s == ir.NodeRetrying {
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
		if node.State().Status == ir.NodeRunning ||
			node.State().Status == ir.NodeRetrying ||
			node.State().Status == ir.NodeNotStarted {
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
func stepsByName(dag *ir.DAG) map[string]ir.Step {
	m := make(map[string]ir.Step, len(dag.Steps))
	for _, s := range dag.Steps {
		m[s.Name] = s
	}
	return m
}
