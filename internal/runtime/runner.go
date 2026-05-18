// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/signal"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrUpstreamFailed   = fmt.Errorf("upstream failed")
	ErrUpstreamSkipped  = fmt.Errorf("upstream skipped")
	ErrUpstreamRejected = fmt.Errorf("upstream rejected")
	ErrDeadlockDetected = errors.New("deadlock detected: no runnable nodes but DAG not finished")
)

// ChatMessagesHandler handles chat session messages for persistence.
type ChatMessagesHandler interface {
	// WriteStepMessages writes messages for a single step.
	WriteStepMessages(ctx context.Context, stepName string, messages []exec.LLMMessage) error
	// ReadStepMessages reads messages for a single step.
	ReadStepMessages(ctx context.Context, stepName string) ([]exec.LLMMessage, error)
}

// Runner runs a plan of steps.
type Runner struct {
	logDir          string
	maxActiveRuns   int
	timeout         time.Duration
	delay           time.Duration
	dry             bool
	onInit          *core.Step
	onExit          *core.Step
	onSuccess       *core.Step
	onFailure       *core.Step
	onAbort         *core.Step
	dagRunID        string
	messagesHandler ChatMessagesHandler
	onWait          *core.Step
	forcedStatus    *core.Status

	dagRunAutoRetryCount int
	dagRunAutoRetryLimit int
	dagRunIsRoot         bool

	canceled  int32
	mu        sync.RWMutex
	pause     time.Duration
	lastError error

	handlerMu sync.RWMutex
	handlers  map[core.HandlerType]*Node

	metrics struct {
		startTime          time.Time
		totalNodes         int
		completedNodes     int
		failedNodes        int
		skippedNodes       int
		canceledNodes      int
		totalExecutionTime time.Duration
	}
}

func New(cfg *Config) *Runner {
	return &Runner{
		logDir:               cfg.LogDir,
		maxActiveRuns:        cfg.MaxActiveSteps,
		timeout:              cfg.Timeout,
		delay:                cfg.Delay,
		dry:                  cfg.Dry,
		onInit:               cfg.OnInit,
		onExit:               cfg.OnExit,
		onSuccess:            cfg.OnSuccess,
		onFailure:            cfg.OnFailure,
		onAbort:              cfg.OnAbort,
		dagRunID:             cfg.DAGRunID,
		messagesHandler:      cfg.MessagesHandler,
		pause:                time.Millisecond * 100,
		onWait:               cfg.OnWait,
		forcedStatus:         cfg.ForcedStatus,
		dagRunAutoRetryCount: cfg.DAGRunAutoRetryCount,
		dagRunAutoRetryLimit: cfg.DAGRunAutoRetryLimit,
		dagRunIsRoot:         cfg.DAGRunIsRoot,
	}
}

type Config struct {
	LogDir          string
	MaxActiveSteps  int
	Timeout         time.Duration
	Delay           time.Duration
	Dry             bool
	OnInit          *core.Step
	OnExit          *core.Step
	OnSuccess       *core.Step
	OnFailure       *core.Step
	OnAbort         *core.Step
	DAGRunID        string
	MessagesHandler ChatMessagesHandler
	OnWait          *core.Step
	ForcedStatus    *core.Status

	DAGRunAutoRetryCount int
	DAGRunAutoRetryLimit int
	DAGRunIsRoot         bool
}

// Run runs the plan of steps.
func (r *Runner) Run(ctx context.Context, plan *Plan, progressCh chan *Node) error {
	if err := r.setup(ctx); err != nil {
		return err
	}

	// Create a cancellable context for the entire execution
	var cancel context.CancelFunc
	if r.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	defer plan.Finish()

	// Initialize node count metrics
	nodes := plan.Nodes()
	r.metrics.totalNodes = len(nodes)

	// If one of the conditions does not met, cancel the execution.
	rCtx := GetDAGContext(ctx)
	// Get evaluated shell for DAG-level preconditions (no step context needed)
	shell := DAGShell(ctx)
	if err := EvalConditions(ctx, shell, rCtx.DAG.Preconditions); err != nil {
		logger.Info(ctx, "Preconditions are not met", tag.Error(err))
		r.Cancel(plan)
	}

	// Execute init handler after preconditions pass, before steps
	if !r.isCanceled() {
		if initNode := r.handlers[core.HandlerOnInit]; initNode != nil {
			logger.Debug(ctx, "Init handler execution started",
				tag.Handler(initNode.Name()),
			)
			if err := r.runEventHandler(ctx, plan, initNode, nil); err != nil {
				r.setLastError(err)
				r.setCanceled() // Fail the DAG if init fails
			}
			if progressCh != nil {
				progressCh <- initNode
			}
		}
	}

	// Channels for event loop
	// Buffer size = total nodes to avoid blocking
	readyCh := make(chan *Node, len(nodes))
	doneCh := make(chan *Node, len(nodes))

	// Find initial ready nodes
	for _, node := range nodes {
		if node.State().Status == core.NodeNotStarted && isReady(ctx, plan, node) {
			logger.Debug(ctx, "Initial node ready", tag.Step(node.Name()))
			readyCh <- node
		}
	}

	var wg sync.WaitGroup
	running := 0

	// Event loop
	ctxDoneCh := ctx.Done()
	for !plan.CheckFinished() {
		// If canceled and no running nodes, we are done
		if r.isCanceled() && running == 0 {
			break
		}

		var activeReadyCh chan *Node
		// Only accept new nodes if:
		// 1. Not canceled
		// 2. maxActiveRuns is 0 (unlimited) OR running < maxActiveRuns
		if !r.isCanceled() && (r.maxActiveRuns == 0 || running < r.maxActiveRuns) {
			activeReadyCh = readyCh
		}

		// Check for Wait condition: no running nodes, no ready nodes, and waiting for approval.
		nodeStates := plan.NodeStates()
		if running == 0 && len(readyCh) == 0 && nodeStates.HasWaiting {
			logger.Info(ctx, "DAG entering wait status - waiting for human approval")
			break
		}
		if running == 0 && len(readyCh) == 0 && nodeStates.HasRetrying {
			logger.Info(ctx, "DAG pending step retry - returning control to parent scheduler")
			break
		}

		// Deadlock detection: if no nodes are running, no nodes are ready, and the graph is not finished,
		// then we are stuck (nodes are waiting for dependencies that will never be satisfied).
		if running == 0 && len(activeReadyCh) == 0 && !plan.CheckFinished() {
			r.setLastError(ErrDeadlockDetected)
			logger.Error(ctx, "Deadlock detected: no runnable nodes remaining")
			break
		}

		select {
		case node := <-activeReadyCh:
			logger.Debug(ctx, "Processing ready node", tag.Step(node.Name()))
			// Double check status - must be NotStarted to proceed
			if node.State().Status != core.NodeNotStarted {
				continue
			}

			// Immediately mark as running to prevent duplicate execution
			// when multiple parents complete simultaneously
			node.SetStatus(core.NodeRunning)

			running++
			wg.Add(1)

			logger.Info(ctx, "Step started", tag.Step(node.Name()))

			go func(n *Node) {
				// Set step context for all logs in this goroutine
				ctx := logger.WithValues(ctx, tag.Step(n.Name()))
				// Anything evaluated during Prepare must see the node's real pre-execution env.
				ctx = r.setupVariables(ctx, plan, n)

				// Ensure node is finished and wg is decremented
				defer r.finishNode(n, &wg)
				// Recover from panics and signal progress for status updates
				defer r.recoverNodePanic(ctx, n, progressCh)
				// Signal completion to runner loop
				defer func() {
					doneCh <- n
				}()

				if err := r.prepareNode(ctx, n); err != nil {
					r.setLastError(err)
					n.MarkError(err)
					n.SetStatus(core.NodeFailed)
					return
				}

				// Status already set to Running before goroutine spawn
				// Send progress notification after successful preparation
				if progressCh != nil {
					progressCh <- n
				}

				r.runNodeExecution(ctx, plan, n, progressCh)
			}(node)

			if r.delay > 0 {
				time.Sleep(r.delay)
			}

		case node := <-doneCh:
			logger.Debug(ctx, "Node execution finished", tag.Step(node.Name()))
			running--
			r.processCompletedNode(ctx, plan, node, readyCh)

		case <-ctxDoneCh:
			r.mu.Lock()
			if r.lastError == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				r.lastError = ctx.Err()
			}
			r.mu.Unlock()
			ctxDoneCh = nil
		}
	}

	wg.Wait()

	// Collect final metrics
	r.metrics.totalExecutionTime = time.Since(r.metrics.startTime)

	var eventHandlers []core.HandlerType
	finalStatus := r.Status(ctx, plan)
	switch finalStatus {
	case core.Succeeded:
		eventHandlers = append(eventHandlers, core.HandlerOnSuccess)

	case core.PartiallySucceeded:
		// PartialSuccess is treated as success since primary work was completed
		// despite some non-critical failures that were allowed to continue
		eventHandlers = append(eventHandlers, core.HandlerOnSuccess)

	case core.Failed:
		if r.shouldRunFailureHandler(finalStatus) {
			eventHandlers = append(eventHandlers, core.HandlerOnFailure)
		} else {
			logger.Info(ctx, "Skipping failure handler while DAG auto-retry is pending",
				slog.Int("autoRetryCount", r.dagRunAutoRetryCount),
				slog.Int("autoRetryLimit", r.dagRunAutoRetryLimit),
			)
		}

	case core.Aborted:
		eventHandlers = append(eventHandlers, core.HandlerOnAbort)

	case core.Rejected:
		eventHandlers = append(eventHandlers, core.HandlerOnFailure)

	case core.Waiting:
		// Execute onWait handler before terminating
		r.handlerMu.RLock()
		handlerNode := r.handlers[core.HandlerOnWait]
		r.handlerMu.RUnlock()

		if handlerNode != nil {
			// Set DAG_WAITING_STEPS environment variable
			waitingSteps := strings.Join(plan.WaitingStepNames(), ",")

			logger.Info(ctx, "Executing onWait handler",
				slog.String("waitingSteps", waitingSteps),
			)

			if err := r.runEventHandler(ctx, plan, handlerNode, map[string]string{
				"DAG_WAITING_STEPS": waitingSteps,
			}); err != nil {
				// Log error but don't fail - notification failure shouldn't block Wait status
				logger.Error(ctx, "onWait handler failed", tag.Error(err))
			}

			if progressCh != nil {
				progressCh <- handlerNode
			}
		}

		logger.Info(ctx, "DAG waiting for approval")
		return r.lastError

	case core.Queued:
		logger.Info(ctx, "DAG queued for pending step retry")
		return r.lastError

	case core.NotStarted, core.Running:
		// These states should not occur at this point
		logger.Warn(ctx, "Unexpected final status",
			tag.Status(finalStatus.String()),
		)
	}

	eventHandlers = append(eventHandlers, core.HandlerOnExit)

	r.handlerMu.RLock()
	defer r.handlerMu.RUnlock()

	for _, handler := range eventHandlers {
		if handlerNode := r.handlers[handler]; handlerNode != nil {
			logger.Debug(ctx, "Handler execution started",
				tag.Handler(handlerNode.Name()),
			)
			if err := r.runEventHandler(ctx, plan, handlerNode, nil); err != nil {
				r.setLastError(err)
			}

			if progressCh != nil {
				progressCh <- handlerNode
			}
		}
	}

	logger.Debug(ctx, "Runner execution complete",
		tag.Status(r.Status(ctx, plan).String()),
		tag.Error(r.lastError),
	)

	return r.lastError
}

func (r *Runner) shouldRunFailureHandler(status core.Status) bool {
	if status != core.Failed {
		return true
	}
	if !r.dagRunIsRoot {
		return true
	}
	if r.dagRunAutoRetryLimit <= 0 {
		return true
	}
	return r.dagRunAutoRetryCount >= r.dagRunAutoRetryLimit
}

func (r *Runner) processCompletedNode(ctx context.Context, plan *Plan, node *Node, readyCh chan *Node) {
	if r.isCanceled() {
		return
	}

	// Queue of nodes to process (nodes that just finished)
	queue := []*Node{node}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, childID := range plan.Dependents(curr.id) {
			child := plan.GetNode(childID)
			if child.State().Status == core.NodeNotStarted {
				if isReady(ctx, plan, child) {
					logger.Debug(ctx, "Dependency satisfied, node ready",
						tag.Step(child.Name()),
						tag.Parent(curr.Name()),
					)
					readyCh <- child
				} else if child.State().Status != core.NodeNotStarted {
					// Child was marked as Aborted/Skipped/Failed by isReady
					// Add to queue to propagate to its children
					queue = append(queue, child)
				}
			}
		}
	}
}

func (r *Runner) runNodeExecution(ctx context.Context, plan *Plan, node *Node, progressCh chan *Node) {
	logger.Debug(ctx, "Starting node execution")
	nodeCtx, nodeCancel := context.WithCancel(ctx)
	defer nodeCancel()

	// Create span for step execution
	spanCtx := nodeCtx
	parentSpan := trace.SpanFromContext(nodeCtx)
	if parentSpan.SpanContext().IsValid() {
		spanAttrs := []attribute.KeyValue{
			attribute.String("step.name", node.Name()),
		}
		// Use the otel package to get the global tracer
		tracer := otel.Tracer("github.com/dagucloud/dagu")
		var span trace.Span
		spanCtx, span = tracer.Start(
			nodeCtx,
			fmt.Sprintf("Step: %s", node.Name()),
			trace.WithAttributes(spanAttrs...),
		)
		defer func() {
			// Set final step attributes
			nodeData := node.NodeData()
			span.SetAttributes(
				attribute.String("step.status", nodeData.State.Status.String()),
			)
			if nodeData.State.ExitCode != 0 {
				span.SetAttributes(attribute.Int("step.exit_code", nodeData.State.ExitCode))
			}
			span.End()
		}()
	}

	ctx = spanCtx
	ctx = r.setupNodeExecutionEnv(ctx, node)

	// Check preconditions
	logger.Debug(ctx, "Checking preconditions")
	if !meetsPreconditions(ctx, node, progressCh) {
		return
	}

	// Setup chat messages from dependencies before execution
	r.setupChatMessages(ctx, node)
	r.setupPushBackConversation(ctx, node)

ExecRepeat: // repeat execution
	for !r.isCanceled() {
		logger.Debug(ctx, "Executing node loop")
		execErr := r.execNode(ctx, node, progressCh)
		isRetriable := r.handleNodeExecutionError(ctx, plan, node, execErr)
		if isRetriable {
			continue ExecRepeat
		}
		if node.State().Status == core.NodeRetrying {
			if progressCh != nil {
				progressCh <- node
			}
			return
		}

		if node.State().Status != core.NodeAborted {
			node.IncDoneCount()
		}

		shouldRepeat := r.shouldRepeatNode(ctx, node, execErr)
		if shouldRepeat && !r.isCanceled() {
			r.prepareNodeForRepeat(ctx, node, progressCh)
			continue
		}

		if execErr != nil && progressCh != nil {
			progressCh <- node
			return
		}

		break ExecRepeat
	}

	// Determine final status for nodes still in running state.
	// Repetitive tasks complete naturally (signal not sent - see runner.Signal).
	// Only mark as aborted if: not a repetitive task AND runner was canceled.
	if node.State().Status == core.NodeRunning {
		isRepetitive := node.Step().RepeatPolicy.RepeatMode != ""
		if !isRepetitive && r.isCanceled() {
			node.SetStatus(core.NodeAborted)
		} else if node.Step().Approval != nil {
			// Step has approval config — enter waiting state for human review.
			// Push-back is human-controlled, no iteration limit.
			node.SetStatus(core.NodeWaiting)
		} else {
			node.SetStatus(core.NodeSucceeded)
		}
	}

	// For executors that implement NodeStatusDeterminer (e.g. call/dag, parallel),
	// the status may have been set to NodeSucceeded by the executor.
	// If the step has an approval config, override to NodeWaiting.
	if node.State().Status == core.NodeSucceeded && node.Step().Approval != nil {
		node.SetStatus(core.NodeWaiting)
	}

	// Save chat messages after execution (including waiting steps for push-back continuity).
	if node.State().Status == core.NodeSucceeded || node.State().Status == core.NodeWaiting {
		r.saveChatMessages(ctx, node)
	}

	if err := r.teardownNode(node); err != nil {
		r.setLastError(err)
		node.SetStatus(core.NodeFailed)
	}

	if progressCh != nil {
		progressCh <- node
	}
}

// setupNodeExecutionEnv prepares the runtime-managed step env before
// preconditions and command execution so both paths evaluate against the same
// context.
func (r *Runner) setupNodeExecutionEnv(ctx context.Context, node *Node) context.Context {
	ctx = node.SetupEnv(ctx)

	if node.State().ApprovalIteration == 0 {
		return ctx
	}

	state := node.State()
	env := GetEnv(ctx)
	approval := node.Step().Approval
	var allowedInputs []string
	if approval != nil {
		allowedInputs = approval.Input
	}

	filteredInputs := exec.FilterPushBackInputs(allowedInputs, state.PushBackInputs)
	for k, v := range filteredInputs {
		env = env.WithEnvVars(k, v)
	}
	env = env.WithEnvVars(exec.EnvKeyDAGPushBackIteration, strconv.Itoa(state.ApprovalIteration))
	if state.PushBackPreviousStdout != "" {
		env = env.WithEnvVars(exec.EnvKeyDAGPushBackPreviousStdoutFile, state.PushBackPreviousStdout)
	}

	if approval != nil && len(filteredInputs) != len(state.PushBackInputs) {
		for k := range state.PushBackInputs {
			if _, ok := filteredInputs[k]; ok {
				continue
			}
			logger.Warn(ctx, "Ignoring unexpected push-back input", slog.String("input", k))
		}
	}

	payload, err := marshalPushBackPayload(allowedInputs, state)
	if err != nil {
		logger.Warn(ctx, "Failed to marshal push-back payload", tag.Error(err))
	} else if payload != "" {
		env = env.WithEnvVars(exec.EnvKeyDAGPushBack, payload)
	}

	return WithEnv(ctx, env)
}

func (r *Runner) setLastError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastError = err
}

func (r *Runner) prepareNode(ctx context.Context, node *Node) error {
	if r.dry {
		return nil
	}
	return node.Prepare(ctx, r.logDir, r.dagRunID)
}

func (r *Runner) teardownNode(node *Node) error {
	if r.dry {
		return nil
	}
	return node.Teardown()
}

// setupChatMessages loads and merges chat messages from dependent steps.
func (r *Runner) setupChatMessages(ctx context.Context, node *Node) {
	if r.messagesHandler == nil {
		return
	}

	step := node.Step()

	executorType := step.ExecutorConfig.Type
	if !core.SupportsLLM(executorType) && !core.SupportsAgent(executorType) {
		return
	}

	if len(step.Depends) == 0 {
		return
	}

	// Read messages from each dependency step
	var inherited []exec.LLMMessage
	for _, dep := range step.Depends {
		msgs, err := r.messagesHandler.ReadStepMessages(ctx, dep)
		if err != nil {
			logger.Warn(ctx, "Failed to read chat messages for dependency",
				tag.Step(dep), tag.Error(err))
			continue
		}
		inherited = append(inherited, msgs...)
	}

	// Deduplicate system messages (keep only first)
	inherited = exec.DeduplicateSystemMessages(inherited)
	if len(inherited) > 0 {
		node.SetChatMessages(inherited)
	}
}

// saveChatMessages saves the node's chat messages to the handler.
func (r *Runner) saveChatMessages(ctx context.Context, node *Node) {
	if r.messagesHandler == nil {
		return
	}

	savedMsgs := node.GetChatMessages()
	if len(savedMsgs) == 0 {
		return
	}

	// Direct write - no read-modify-write cycle
	if err := r.messagesHandler.WriteStepMessages(ctx, node.Step().Name, savedMsgs); err != nil {
		logger.Warn(ctx, "Failed to write chat messages", tag.Error(err))
	}
}

// setupPushBackConversation loads the step's own previous conversation for
// AI steps being re-executed after push-back. This REPLACES any dependency
// messages (which are already embedded in the previous conversation from
// iteration 0). Non-AI steps are unaffected.
func (r *Runner) setupPushBackConversation(ctx context.Context, node *Node) {
	if r.messagesHandler == nil {
		return
	}
	step := node.Step()
	if !core.SupportsAgent(step.ExecutorConfig.Type) && !core.SupportsLLM(step.ExecutorConfig.Type) {
		return
	}
	if node.State().ApprovalIteration == 0 {
		return
	}

	ownMessages, err := r.messagesHandler.ReadStepMessages(ctx, step.Name)
	if err != nil {
		logger.Warn(ctx, "Failed to read own messages for push-back",
			tag.Step(step.Name), tag.Error(err))
		return
	}
	if len(ownMessages) == 0 {
		return
	}

	if len(ownMessages) > 200 {
		logger.Warn(ctx, "Large conversation history after push-back iterations",
			tag.Step(step.Name), slog.Int("messageCount", len(ownMessages)),
			slog.Int("iteration", node.State().ApprovalIteration))
	}

	// REPLACE (not merge) — dependencies are already embedded in the
	// previous conversation from iteration 0.
	node.SetChatMessages(ownMessages)
}

func (r *Runner) setupVariables(ctx context.Context, plan *Plan, node *Node) context.Context {
	env := NewPlanEnv(ctx, node.Step(), plan)
	node.SetWorkingDir(env.WorkingDir)

	// Load output variables and approval inputs from predecessor nodes (dependencies)
	// This traverses backwards from the current node to find all nodes it depends on
	curr := node.id
	visited := make(map[int]struct{})
	queue := []int{}

	// Start with direct dependencies (nodes this node depends on)
	queue = append(queue, plan.Dependencies(curr)...)

	// Traverse all predecessor nodes
	for len(queue) > 0 {
		predID := queue[0]
		queue = queue[1:]

		if _, ok := visited[predID]; ok {
			continue
		}
		visited[predID] = struct{}{}

		// Add this node's dependencies to the queue
		queue = append(queue, plan.Dependencies(predID)...)

		// Load output variables from this predecessor node
		// (includes approval inputs which are stored in OutputVariables)
		predNode := plan.GetNode(predID)
		if predNode == nil {
			continue
		}

		// Add predecessor outputs to scope
		if outputs := predNode.OutputVariablesMap(); len(outputs) > 0 {
			stepID := predNode.Step().ID
			if stepID == "" {
				stepID = predNode.Step().Name
			}
			env.Scope = env.Scope.WithStepOutputs(outputs, stepID)
		}
	}

	// Helper to evaluate and store environment variables
	addEnvVars := func(envList []string) {
		for _, v := range envList {
			key, value, found := strings.Cut(v, "=")
			if !found {
				logger.Error(ctx, "Invalid environment variable format", slog.String("var", v))
				continue
			}
			evaluatedValue, err := env.EvalString(ctx, value)
			if err != nil {
				logger.Error(ctx, "Failed to evaluate environment variable",
					slog.String("var", v),
					tag.Error(err),
				)
				continue
			}
			env.Scope = env.Scope.WithEntry(key, evaluatedValue, eval.EnvSourceStepEnv)
		}
	}

	// Add step-level environment variables
	addEnvVars(node.Step().Env)

	// Add container environment variables (step-level takes precedence over DAG-level)
	// This ensures container env vars are available when evaluating command arguments
	if ct := node.Step().Container; ct != nil {
		addEnvVars(ct.Env)
	} else if dag := env.DAG; dag != nil && dag.Container != nil {
		addEnvVars(dag.Container.Env)
	}

	return WithEnv(ctx, env)
}

func (r *Runner) setupEnvironEventHandler(
	ctx context.Context,
	plan *Plan,
	node *Node,
	extraEnvs map[string]string,
) context.Context {
	// Preserve any extra env vars from the incoming context (e.g., DAG_WAITING_STEPS)
	existingEnv := GetEnv(ctx)

	env := NewPlanEnv(ctx, node.Step(), plan)
	node.SetWorkingDir(env.WorkingDir)

	// Add DAG_RUN_STATUS to scope
	env.Scope = env.Scope.WithEntry(
		exec.EnvKeyDAGRunStatus,
		r.Status(ctx, plan).String(),
		eval.EnvSourceStepEnv,
	)

	// Copy extra env vars from existing scope that aren't already set
	if existingEnv.Scope != nil {
		for k, v := range existingEnv.Scope.AllBySource(eval.EnvSourceStepEnv) {
			if _, exists := env.Scope.Get(k); !exists {
				env.Scope = env.Scope.WithEntry(k, v, eval.EnvSourceStepEnv)
			}
		}
	}

	for k, v := range extraEnvs {
		env.Scope = env.Scope.WithEntry(k, v, eval.EnvSourceStepEnv)
	}

	// Load all output variables from all nodes
	for _, n := range plan.Nodes() {
		if outputs := n.OutputVariablesMap(); len(outputs) > 0 {
			stepID := n.Step().ID
			if stepID == "" {
				stepID = n.Step().Name
			}
			env.Scope = env.Scope.WithStepOutputs(outputs, stepID)
		}
	}

	return WithEnv(ctx, env)
}

func (r *Runner) execNode(ctx context.Context, node *Node, progressCh chan *Node) error {
	if r.dry {
		return nil
	}
	if progressCh != nil && node.Step().SubDAG != nil {
		// Send an additional progress notification after the executor is set up
		// so that SubRuns are persisted to storage before the subDAG starts running.
		return node.Execute(ctx, func() { progressCh <- node })
	}
	return node.Execute(ctx)
}

// Signal sends a signal to the runner.
// for a node with repeat policy, it does not stop the node and
// wait to finish current run.
func (r *Runner) Signal(
	ctx context.Context, plan *Plan, sig os.Signal, done chan bool, allowOverride bool,
) {
	isTermination := signal.IsTerminationSignalOS(sig)

	// Set canceled flag FIRST so execution loops see it immediately.
	// This prevents a race where the execution loop checks isCanceled()
	// before we've set the flag, causing it to mark nodes as Succeeded
	// instead of Aborted.
	if !r.isCanceled() && isTermination {
		r.setCanceled()
	}

	for _, node := range plan.Nodes() {
		// for a repetitive task, we'll wait for the job to finish
		// until time reaches max wait time
		if node.Step().RepeatPolicy.RepeatMode != "" {
			logger.Info(ctx, "Waiting for repeat node to finish",
				tag.Step(node.Step().Name),
			)
			continue
		}
		node.Signal(ctx, sig, allowOverride)
	}

	if done != nil && isTermination {
		defer func() {
			for plan.HasActiveNodes() {
				time.Sleep(r.pause)
			}
			done <- true
		}()
	}
}

// Cancel sends -1 signal to all nodes.
func (r *Runner) Cancel(p *Plan) {
	r.setCanceled()
	for _, node := range p.Nodes() {
		node.Cancel()
	}
}

// Status returns the status of the runner.
func (r *Runner) Status(ctx context.Context, p *Plan) core.Status {
	if r.isCanceled() && !r.isSucceed(p) {
		return core.Aborted
	}
	if !p.IsStarted() {
		return core.NotStarted
	}

	// Get node states atomically, then check plan finished state.
	// Note: IsFinished() is called separately, so there's a small window where
	// the plan could be marked finished between these calls. This is acceptable
	// for status reporting as it self-corrects on the next Status() call.
	states := p.NodeStates()

	if states.HasRunning {
		return core.Running
	}
	if states.HasRetrying {
		return core.Queued
	}
	if states.HasRejected {
		return core.Rejected
	}
	if states.HasWaiting {
		return core.Waiting
	}
	if states.HasNotStarted && !p.IsFinished() {
		return core.Running
	}
	if r.forcedStatus != nil {
		return *r.forcedStatus
	}

	if r.isPartialSuccess(ctx, p) {
		return core.PartiallySucceeded
	}

	if r.isError() {
		return core.Failed
	}

	return core.Succeeded
}

func (r *Runner) isError() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastError != nil
}

// HandlerNode returns the handler node with the given name.
func (r *Runner) HandlerNode(name core.HandlerType) *Node {
	r.handlerMu.RLock()
	defer r.handlerMu.RUnlock()
	if v, ok := r.handlers[name]; ok {
		return v
	}
	return nil
}

// isCanceled returns true if the runner is canceled.
func (r *Runner) isCanceled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.canceled == 1
}

func isReady(ctx context.Context, plan *Plan, node *Node) bool {
	for _, depID := range plan.Dependencies(node.id) {
		dep := plan.GetNode(depID)
		status := dep.State().Status

		switch status {
		case core.NodeSucceeded, core.NodePartiallySucceeded:
			continue

		case core.NodeFailed:
			if dep.ShouldContinue(ctx) {
				logger.Debug(ctx, "Dependency failed but allowed to continue",
					tag.Step(node.Name()), tag.Dependency(dep.Name()))
				continue
			}
			logger.Debug(ctx, "Dependency failed",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(core.NodeAborted)
			node.SetError(ErrUpstreamFailed)
			return false

		case core.NodeSkipped:
			if dep.State().SkippedByRetry {
				logger.Debug(ctx, "Dependency skipped by retry",
					tag.Step(node.Name()), tag.Dependency(dep.Name()))
				continue
			}
			if dep.ShouldContinue(ctx) {
				logger.Debug(ctx, "Dependency skipped but allowed to continue",
					tag.Step(node.Name()), tag.Dependency(dep.Name()))
				continue
			}
			logger.Debug(ctx, "Dependency skipped",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(core.NodeSkipped)
			node.SetError(ErrUpstreamSkipped)
			return false

		case core.NodeAborted:
			logger.Debug(ctx, "Dependency aborted",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(core.NodeAborted)
			return false

		case core.NodeRejected:
			logger.Debug(ctx, "Dependency rejected",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(core.NodeAborted)
			node.SetError(ErrUpstreamRejected)
			return false

		case core.NodeNotStarted, core.NodeRunning:
			logger.Debug(ctx, "Dependency not finished",
				tag.Step(node.Name()), tag.Dependency(dep.Name()),
				tag.Status(status.String()))
			return false

		case core.NodeRetrying:
			logger.Debug(ctx, "Dependency waiting for retry",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			return false

		case core.NodeWaiting:
			logger.Debug(ctx, "Dependency waiting for approval",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			return false

		default:
			return false
		}
	}
	return true
}

func (r *Runner) runEventHandler(ctx context.Context, plan *Plan, node *Node, extraEnvs map[string]string) error {
	defer node.Finish()

	if r.dry {
		node.SetStatus(core.NodeSucceeded)
		return nil
	}

	// Handler stdout/stderr paths are also evaluated during Prepare, so attach the
	// complete handler env before preparing the node.
	ctx = r.setupEnvironEventHandler(ctx, plan, node, extraEnvs)

	if err := node.Prepare(ctx, r.logDir, r.dagRunID); err != nil {
		node.SetStatus(core.NodeFailed)
		return nil
	}
	defer func() { _ = node.Teardown() }()

	if err := node.evalPreconditions(ctx); err != nil {
		node.SetStatus(core.NodeSkipped)
		return nil
	}

	node.SetStatus(core.NodeRunning)

	if err := node.Execute(ctx); err != nil {
		node.SetStatus(core.NodeFailed)
		return err
	}

	node.SetStatus(core.NodeSucceeded)
	return nil
}

func (r *Runner) setup(ctx context.Context) (err error) {
	if !r.dry {
		if err := os.MkdirAll(r.logDir, 0750); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}
	}

	// Initialize handlers
	r.handlerMu.Lock()
	defer r.handlerMu.Unlock()

	r.handlers = make(map[core.HandlerType]*Node)
	handlerSteps := map[core.HandlerType]*core.Step{
		core.HandlerOnInit:    r.onInit,
		core.HandlerOnExit:    r.onExit,
		core.HandlerOnSuccess: r.onSuccess,
		core.HandlerOnFailure: r.onFailure,
		core.HandlerOnAbort:   r.onAbort,
		core.HandlerOnWait:    r.onWait,
	}
	for handlerType, step := range handlerSteps {
		if step != nil {
			r.handlers[handlerType] = &Node{Data: newSafeData(NodeData{Step: *step})}
		}
	}

	r.metrics.startTime = time.Now()

	logger.Debug(ctx, "Runner setup complete",
		slog.String("dagRunId", r.dagRunID),
		slog.Int("maxActiveRuns", r.maxActiveRuns),
		slog.Duration("timeout", r.timeout),
		slog.Bool("dry", r.dry),
	)

	return nil
}

func (r *Runner) setCanceled() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.canceled = 1
}

func (r *Runner) isSucceed(p *Plan) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, node := range p.Nodes() {
		nodeStatus := node.State().Status
		if nodeStatus == core.NodeSucceeded || nodeStatus == core.NodeSkipped || nodeStatus == core.NodePartiallySucceeded {
			continue
		}
		return false
	}
	return true
}

// isPartialSuccess checks if the DAG completed with some failures that were allowed to continue.
// This represents scenarios where execution continued despite failures due to continueOn conditions.
func (r *Runner) isPartialSuccess(ctx context.Context, p *Plan) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hasFailuresWithContinueOn := false
	hasSuccessfulNodes := false

	// First pass: check if any failed node is NOT allowed to continue
	// If so, this is an error, not partial success
	for _, node := range p.Nodes() {
		if node.State().Status == core.NodeFailed {
			if !node.ShouldContinue(ctx) {
				// Found a failed node that was NOT allowed to continue
				// This disqualifies the DAG from being partial success
				return false
			}
		}
	}

	// Second pass: check for partial success conditions
	for _, node := range p.Nodes() {
		switch node.State().Status {
		case core.NodeSucceeded:
			hasSuccessfulNodes = true
		case core.NodeFailed:
			if node.ShouldContinue(ctx) && !node.ShouldMarkSuccess(ctx) {
				hasFailuresWithContinueOn = true
			}
		case core.NodePartiallySucceeded:
			// Partial success at node level contributes to overall partial success
			hasFailuresWithContinueOn = true
			hasSuccessfulNodes = true
		case core.NodeNotStarted, core.NodeRunning, core.NodeRetrying, core.NodeAborted, core.NodeSkipped, core.NodeWaiting, core.NodeRejected:
			// These statuses don't affect partial success determination, but are needed for linter
		}
	}

	// Partial success requires:
	// 1. At least one failed node with continueOn (some non-critical failures)
	// 2. No failed nodes without continueOn (checked in first pass)
	// Note: Skipped nodes alone do not count as successful completion
	return hasSuccessfulNodes && hasFailuresWithContinueOn
}

func (r *Runner) isTimeout(startedAt time.Time) bool {
	return r.timeout > 0 && time.Since(startedAt) > r.timeout
}

// GetMetrics returns the current metrics for the runner
func (r *Runner) GetMetrics() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metrics := map[string]any{
		"totalNodes":         r.metrics.totalNodes,
		"completedNodes":     r.metrics.completedNodes,
		"failedNodes":        r.metrics.failedNodes,
		"skippedNodes":       r.metrics.skippedNodes,
		"canceledNodes":      r.metrics.canceledNodes,
		"totalExecutionTime": r.metrics.totalExecutionTime.String(),
	}

	return metrics
}

// shouldRetryNode handles the retry logic for a node based on exit codes and retry policy
func (r *Runner) shouldRetryNode(ctx context.Context, node *Node, execErr error) (shouldRetry bool) {
	exitCode := 1
	if code, found := exitCodeFromError(execErr); found {
		exitCode = code
		logger.Debug(ctx, "Resolved exit code from error",
			tag.Error(execErr),
			tag.ExitCode(exitCode),
		)
	} else {
		logger.Debug(ctx, "Could not determine exit code",
			tag.Error(execErr),
			slog.String("error-type", fmt.Sprintf("%T", execErr)),
		)
	}

	shouldRetry = node.retryPolicy.ShouldRetry(exitCode)
	logger.Debug(ctx, "Checking retry policy",
		tag.ExitCode(exitCode),
		slog.Any("allowed-codes", node.retryPolicy.ExitCodes),
		slog.Bool("should-retry", shouldRetry),
	)

	if !shouldRetry {
		// finish the node with error
		node.SetStatus(core.NodeFailed)
		node.MarkError(execErr)
		r.setLastError(execErr)
		return false
	}

	logger.Info(ctx, "Step execution failed; retrying",
		tag.Error(execErr),
		slog.Int("retry", node.GetRetryCount()),
		tag.ExitCode(exitCode),
	)

	if externalStepRetryEnabled(ctx) {
		node.IncRetryCount()
		node.SetStatus(core.NodeRetrying)
		logger.Info(ctx, "Step retry will be scheduled by the parent executor",
			slog.Int("retry", node.GetRetryCount()),
			slog.Duration("interval", core.CalculateBackoffInterval(
				node.Step().RetryPolicy.Interval,
				node.Step().RetryPolicy.Backoff,
				node.Step().RetryPolicy.MaxInterval,
				node.GetRetryCount()-1,
			)),
		)
		return false
	}

	// Set the node status to running so that it can be retried inline
	node.IncRetryCount()
	interval := core.CalculateBackoffInterval(
		node.Step().RetryPolicy.Interval,
		node.Step().RetryPolicy.Backoff,
		node.Step().RetryPolicy.MaxInterval,
		node.GetRetryCount()-1, // -1 because we just incremented
	)
	time.Sleep(interval)
	node.SetRetriedAt(time.Now())
	node.SetStatus(core.NodeRunning)
	return true
}

// recoverNodePanic handles panic recovery for a node goroutine.
// It signals progressCh so the agent can write the updated status to storage.
func (r *Runner) recoverNodePanic(ctx context.Context, node *Node, progressCh chan *Node) {
	if panicObj := recover(); panicObj != nil {
		stack := string(debug.Stack())
		err := fmt.Errorf("panic recovered in node %s: %v\n%s", node.Name(), panicObj, stack)
		logger.Error(ctx, "Panic occurred",
			tag.Error(err),
			slog.String("stack", stack),
			tag.RunID(r.dagRunID),
		)
		node.MarkError(err)
		r.setLastError(err)

		// Update metrics for failed node
		r.mu.Lock()
		r.metrics.failedNodes++
		r.mu.Unlock()

		// Signal progress so status is written to storage
		if progressCh != nil {
			progressCh <- node
		}
	}
}

// finishNode updates metrics and finalizes the node.
func (r *Runner) finishNode(node *Node, wg *sync.WaitGroup) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch node.State().Status {
	case core.NodeSucceeded, core.NodePartiallySucceeded:
		r.metrics.completedNodes++
	case core.NodeFailed, core.NodeRejected:
		r.metrics.failedNodes++
	case core.NodeSkipped:
		r.metrics.skippedNodes++
	case core.NodeAborted:
		r.metrics.canceledNodes++
	case core.NodeWaiting, core.NodeNotStarted, core.NodeRunning, core.NodeRetrying:
		// Waiting nodes are counted when they complete after approval.
		// NotStarted/Running should not happen at this point.
	}

	node.Finish()
	wg.Done()
}

func externalStepRetryEnabled(ctx context.Context) bool {
	if os.Getenv(exec.EnvKeyExternalStepRetry) != "" {
		return true
	}

	rCtx := exec.GetContext(ctx)
	if value, ok := rCtx.UserEnvsMap()[exec.EnvKeyExternalStepRetry]; ok {
		return value != ""
	}
	return false
}

// checkPreconditions evaluates the preconditions for a node and updates its status accordingly.
func meetsPreconditions(ctx context.Context, node *Node, progressCh chan *Node) bool {
	err := node.evalPreconditions(ctx)
	if err != nil {
		// Precondition not met, skip the node
		node.SetStatus(core.NodeSkipped)
		if !errors.Is(err, ErrConditionNotMet) {
			node.SetError(err)
		}
		if progressCh != nil {
			progressCh <- node
		}
		return false
	}
	return true
}

// handleNodeExecutionError handles the error from node execution and determines if it should be retried.
func (r *Runner) handleNodeExecutionError(ctx context.Context, plan *Plan, node *Node, execErr error) bool {
	if execErr == nil {
		return false // no error, nothing to handle
	}

	s := node.State().Status
	switch {
	case s == core.NodeSucceeded || s == core.NodeAborted || s == core.NodePartiallySucceeded:
		// do nothing

	// Check for timeout errors first (both step-level and DAG-level)
	case errors.Is(execErr, context.DeadlineExceeded):
		step := node.Step()
		if step.Timeout > 0 {
			// Step-level timeout: Node.Execute already set status to failed and exitCode=124.
			// Keep failed status and ensure we don't retry.
			logger.Info(ctx, "Step timed out (step-level timeout)",
				tag.Timeout(step.Timeout),
				tag.Error(execErr),
			)
			// Ensure status is failed (in case earlier logic differed)
			node.SetStatus(core.NodeFailed)
		} else if r.isTimeout(plan.StartAt()) {
			// DAG-level timeout -> treat as aborted (global cancellation semantics)
			logger.Info(ctx, "Step deadline exceeded (DAG-level timeout)",
				tag.Timeout(r.timeout),
				tag.Error(execErr),
			)
			node.SetStatus(core.NodeAborted)
		} else {
			// Parent context canceled or other deadline; mark aborted for safety
			logger.Info(ctx, "Step deadline exceeded", tag.Error(execErr))
			node.SetStatus(core.NodeAborted)
		}
		r.setLastError(execErr)

	case r.isTimeout(plan.StartAt()):
		// DAG-level timeout (non-context error case)
		logger.Info(ctx, "Step deadline exceeded (DAG-level timeout)",
			tag.Timeout(r.timeout),
			tag.Error(execErr),
		)
		node.SetStatus(core.NodeAborted)
		r.setLastError(execErr)

	case r.isCanceled():
		r.setLastError(execErr)

	case node.retryPolicy.Limit > node.GetRetryCount():
		if r.shouldRetryNode(ctx, node, execErr) {
			return true
		}

	default:
		// node execution error is unexpected and unrecoverable
		node.SetStatus(core.NodeFailed)
		if node.ShouldMarkSuccess(ctx) {
			// mark as success if the node should be force marked as success
			// i.e. continueOn.markSuccess is set to true
			node.SetStatus(core.NodeSucceeded)
		} else {
			node.MarkError(execErr)
			r.setLastError(execErr)
		}
	}

	return false
}

// shouldRepeatNode determines if a node should be repeated based on its repeat policy
func (r *Runner) shouldRepeatNode(ctx context.Context, node *Node, execErr error) bool {
	rp := node.Step().RepeatPolicy

	// Check the hard limit first - this overrides everything
	if rp.Limit > 0 && node.State().DoneCount >= rp.Limit {
		return false
	}

	// Reload output variables into context before evaluating conditions
	ctx = r.reloadNodeOutputs(ctx, node)
	shell := GetEnv(ctx).Shell(ctx)

	switch rp.RepeatMode {
	case core.RepeatModeWhile:
		return r.evalWhileCondition(ctx, shell, node, rp, execErr)
	case core.RepeatModeUntil:
		return r.evalUntilCondition(ctx, shell, node, rp, execErr)
	default:
		return false
	}
}

// reloadNodeOutputs updates the context with the node's current output variables.
func (r *Runner) reloadNodeOutputs(ctx context.Context, node *Node) context.Context {
	outputs := node.OutputVariablesMap()
	if len(outputs) == 0 {
		return ctx
	}
	env := GetEnv(ctx)
	stepID := node.Step().ID
	if stepID == "" {
		stepID = node.Step().Name
	}
	env.Scope = env.Scope.WithStepOutputs(outputs, stepID)
	return WithEnv(ctx, env)
}

// evalWhileCondition evaluates the repeat condition for a "while" loop.
func (r *Runner) evalWhileCondition(ctx context.Context, shell []string, node *Node, rp core.RepeatPolicy, execErr error) bool {
	if rp.Condition != nil {
		err := EvalCondition(ctx, shell, rp.Condition)
		return err == nil // Repeat while condition is met
	}
	if len(rp.ExitCode) > 0 {
		return slices.Contains(rp.ExitCode, node.State().ExitCode)
	}
	// Unconditional while: repeat as long as the step succeeds
	return execErr == nil
}

// evalUntilCondition evaluates the repeat condition for an "until" loop.
func (r *Runner) evalUntilCondition(ctx context.Context, shell []string, node *Node, rp core.RepeatPolicy, execErr error) bool {
	if rp.Condition != nil {
		err := EvalCondition(ctx, shell, rp.Condition)
		return err != nil // Repeat until condition is met
	}
	if len(rp.ExitCode) > 0 {
		return !slices.Contains(rp.ExitCode, node.State().ExitCode)
	}
	// Unconditional until: repeat until the step succeeds
	return execErr != nil
}

// prepareNodeForRepeat sets up a node for repetition
func (r *Runner) prepareNodeForRepeat(ctx context.Context, node *Node, progressCh chan *Node) {
	step := node.Step()

	node.SetStatus(core.NodeRunning) // reset status to running for the repeat
	if r.lastError == node.Error() {
		r.setLastError(nil) // clear last error if we are repeating
	}
	logger.Info(ctx, "Step will be repeated",
		slog.Duration("interval", step.RepeatPolicy.Interval),
	)
	interval := core.CalculateBackoffInterval(
		step.RepeatPolicy.Interval,
		step.RepeatPolicy.Backoff,
		step.RepeatPolicy.MaxInterval,
		node.State().DoneCount,
	)
	time.Sleep(interval)
	node.SetRepeated(true) // mark as repeated
	logger.Info(ctx, "Repeating step")

	if progressCh != nil {
		progressCh <- node
	}
}

func NewPlanEnv(ctx context.Context, step core.Step, plan *Plan) Env {
	env := NewEnv(ctx, step)
	for _, n := range plan.Nodes() {
		if n.Step().ID != "" {
			env.StepMap[n.Step().ID] = n.StepInfo()
		}
	}
	return env
}
