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

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/runctx"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
	WriteStepMessages(ctx context.Context, stepName string, messages []ir.LLMMessage) error
	// ReadStepMessages reads messages for a single step.
	ReadStepMessages(ctx context.Context, stepName string) ([]ir.LLMMessage, error)
}

// Runner runs a plan of steps.
type Runner struct {
	logDir           string
	maxActiveRuns    int
	timeout          time.Duration
	delay            time.Duration
	dry              bool
	onInit           *ir.Step
	onExit           *ir.Step
	onSuccess        *ir.Step
	onFailure        *ir.Step
	onAbort          *ir.Step
	dagRunID         string
	messagesHandler  ChatMessagesHandler
	stepExecutor     *StepExecutor
	onWait           *ir.Step
	forcedStatus     *ir.Status
	materializations build.MaterializationStore
	noReuse          bool

	dagRunAutoRetryCount int
	dagRunAutoRetryLimit int
	dagRunIsRoot         bool

	canceled      int32
	failed        int32
	mu            sync.RWMutex
	pause         time.Duration
	lastError     error
	preconditions []ir.ConditionResult

	handlerMu sync.RWMutex
	handlers  map[ir.HandlerType]*Node

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
		stepExecutor:         NewStepExecutor(),
		pause:                time.Millisecond * 100,
		onWait:               cfg.OnWait,
		forcedStatus:         cfg.ForcedStatus,
		materializations:     cfg.MaterializationStore,
		noReuse:              cfg.NoReuse,
		dagRunAutoRetryCount: cfg.DAGRunAutoRetryCount,
		dagRunAutoRetryLimit: cfg.DAGRunAutoRetryLimit,
		dagRunIsRoot:         cfg.DAGRunIsRoot,
	}
}

type Config struct {
	LogDir               string
	MaxActiveSteps       int
	Timeout              time.Duration
	Delay                time.Duration
	Dry                  bool
	OnInit               *ir.Step
	OnExit               *ir.Step
	OnSuccess            *ir.Step
	OnFailure            *ir.Step
	OnAbort              *ir.Step
	DAGRunID             string
	MessagesHandler      ChatMessagesHandler
	OnWait               *ir.Step
	ForcedStatus         *ir.Status
	MaterializationStore build.MaterializationStore
	NoReuse              bool

	DAGRunAutoRetryCount int
	DAGRunAutoRetryLimit int
	DAGRunIsRoot         bool
}

// Run runs the plan of steps.
func (r *Runner) Run(ctx context.Context, plan *Plan, progressCh chan *Node) error {
	if err := r.setup(ctx); err != nil {
		return err
	}
	if err := prepareBuildPlan(ctx, plan); err != nil {
		return err
	}
	plan.finalizeStepRetrySelection()
	r.resetRunState(plan)

	// Create a cancellable context for the entire execution
	parentCtx := ctx
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
	rCtx := MustDAGContext(ctx)
	if len(rCtx.DAG.Preconditions) > 0 {
		r.setPreconditionResults(conditionResults(rCtx.DAG.Preconditions))
		shell, err := ResolveDAGShell(ctx)
		if err != nil {
			logger.Info(ctx, "Preconditions are not met", tag.Error(err))
			r.setLastError(err)
			r.setFailed()
			r.Cancel(plan)
		} else {
			results, conditionErr := EvaluateConditions(ctx, shell, rCtx.DAG.Preconditions)
			r.setPreconditionResults(results)
			if conditionErr != nil {
				logger.Info(ctx, "Preconditions are not met", tag.Error(conditionErr))
				if !errors.Is(conditionErr, ErrConditionNotMet) {
					r.setLastError(conditionErr)
					r.setFailed()
				}
				r.Cancel(plan)
			}
		}
	}

	// Execute init handler after preconditions pass, before steps
	if !r.isCanceled() {
		if initNode := r.handlers[ir.HandlerOnInit]; initNode != nil {
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

	if rCtx.DAG.IsAgent() {
		r.runAgentLoop(ctx, plan, progressCh)
	} else {
		r.runGraphLoop(ctx, plan, nodes, progressCh)
	}

	// Collect final metrics
	r.metrics.totalExecutionTime = time.Since(r.metrics.startTime)

	handlerCtx := ctx
	if r.timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) && parentCtx.Err() == nil {
		handlerCtx = parentCtx
	}

	var eventHandlers []ir.HandlerType
	finalStatus := r.Status(ctx, plan)
	switch finalStatus {
	case ir.Succeeded:
		eventHandlers = append(eventHandlers, ir.HandlerOnSuccess)

	case ir.PartiallySucceeded:
		// PartialSuccess is treated as success since primary work was completed
		// despite some non-critical failures that were allowed to continue
		eventHandlers = append(eventHandlers, ir.HandlerOnSuccess)

	case ir.Failed:
		if r.shouldRunFailureHandler(finalStatus) {
			eventHandlers = append(eventHandlers, ir.HandlerOnFailure)
		} else {
			logger.Info(ctx, "Skipping failure handler while effective DAG retry policy is pending",
				slog.Int("autoRetryCount", r.dagRunAutoRetryCount),
				slog.Int("autoRetryLimit", r.dagRunAutoRetryLimit),
			)
		}

	case ir.Aborted:
		eventHandlers = append(eventHandlers, ir.HandlerOnAbort)

	case ir.Rejected:
		eventHandlers = append(eventHandlers, ir.HandlerOnFailure)

	case ir.Waiting:
		// Execute onWait handler before terminating
		r.handlerMu.RLock()
		handlerNode := r.handlers[ir.HandlerOnWait]
		r.handlerMu.RUnlock()

		if handlerNode != nil {
			// Set DAG_WAITING_STEPS environment variable
			waitingSteps := strings.Join(plan.WaitingStepNames(), ",")

			logger.Info(handlerCtx, "Executing onWait handler",
				slog.String("waitingSteps", waitingSteps),
			)

			if err := r.runEventHandler(handlerCtx, plan, handlerNode, map[string]string{
				runenv.EnvKeyDAGWaitingSteps: waitingSteps,
			}); err != nil {
				// Log error but don't fail - notification failure shouldn't block Wait status
				logger.Error(handlerCtx, "onWait handler failed", tag.Error(err))
			}

			if progressCh != nil {
				progressCh <- handlerNode
			}
		}

		logger.Info(ctx, "DAG waiting for human input")
		return r.lastError

	case ir.Queued:
		logger.Info(ctx, "DAG queued for pending step retry")
		return r.lastError

	case ir.NotStarted, ir.Running:
		// These states should not occur at this point
		logger.Warn(ctx, "Unexpected final status",
			tag.Status(finalStatus.String()),
		)
	}

	eventHandlers = append(eventHandlers, ir.HandlerOnExit)

	r.handlerMu.RLock()
	defer r.handlerMu.RUnlock()

	for _, handler := range eventHandlers {
		if handlerNode := r.handlers[handler]; handlerNode != nil {
			logger.Debug(handlerCtx, "Handler execution started",
				tag.Handler(handlerNode.Name()),
			)
			if err := r.runEventHandler(handlerCtx, plan, handlerNode, nil); err != nil {
				r.setLastError(err)
			}

			if progressCh != nil {
				progressCh <- handlerNode
			}
		}
	}

	logger.Debug(handlerCtx, "Runner execution complete",
		tag.Status(r.Status(handlerCtx, plan).String()),
		tag.Error(r.lastError),
	)

	return r.lastError
}

// runGraphLoop runs dependency-ordered execution: every node whose dependencies
// are satisfied is dispatched, up to the active-run limit.
func (r *Runner) runGraphLoop(ctx context.Context, plan *Plan, nodes []*Node, progressCh chan *Node) {
	// Channels for event loop
	// Buffer size = total nodes to avoid blocking
	readyCh := make(chan *Node, len(nodes))
	doneCh := make(chan *Node, len(nodes))

	// Find initial ready nodes
	for _, node := range nodes {
		if node.State().Status == ir.NodeNotStarted && isReady(ctx, plan, node) {
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

		// Check for Wait condition: no running nodes, no ready nodes, and waiting for manual completion.
		nodeStates := plan.NodeStates()
		if running == 0 && len(readyCh) == 0 && nodeStates.HasWaiting {
			logger.Info(ctx, "DAG entering wait status - waiting for human input")
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
			if node.State().Status != ir.NodeNotStarted {
				continue
			}

			// Immediately mark as running to prevent duplicate execution
			// when multiple parents complete simultaneously
			node.SetStatus(ir.NodeRunning)

			running++
			wg.Add(1)

			logger.Info(ctx, "Step started", tag.Step(node.Name()))

			go func(n *Node) {
				// Set step context for all logs in this goroutine
				ctx := logger.WithValues(ctx, tag.Step(n.Name()))

				// Ensure node is finished and wg is decremented
				defer r.finishNode(n, &wg)
				// Recover from panics and signal progress for status updates
				defer r.recoverNodePanic(ctx, n, progressCh)
				// Signal completion to runner loop
				defer func() {
					doneCh <- n
				}()

				// Anything evaluated during Prepare must see the node's real pre-execution env.
				var err error
				ctx, err = r.setupVariables(ctx, plan, n)
				if err != nil {
					r.setLastError(err)
					n.MarkError(err)
					n.SetStatus(ir.NodeFailed)
					return
				}
				if n.Step().HumanTask != nil {
					r.runHumanTask(ctx, plan, n, progressCh)
					return
				}
				if !r.dry {
					if err := validateBuildRuntimeRedirectAliases(ctx, plan, n); err != nil {
						r.setLastError(err)
						n.MarkError(err)
						n.SetStatus(ir.NodeFailed)
						return
					}
				}

				if err := r.prepareNode(ctx, n); err != nil {
					r.setLastError(err)
					n.MarkError(err)
					n.SetStatus(ir.NodeFailed)
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
}

func (r *Runner) shouldRunFailureHandler(status ir.Status) bool {
	if status != ir.Failed {
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
			if child.State().Status == ir.NodeNotStarted {
				if isReady(ctx, plan, child) {
					logger.Debug(ctx, "Dependency satisfied, node ready",
						tag.Step(child.Name()),
						tag.Parent(curr.Name()),
					)
					readyCh <- child
				} else if child.State().Status != ir.NodeNotStarted {
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
		tracer := otel.Tracer("github.com/dagucloud/dagu/v2")
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
	preparedNodeTornDown := false
	teardownPreparedNode := func() {
		if preparedNodeTornDown {
			return
		}
		preparedNodeTornDown = true
		r.teardownPreparedNode(node)
	}
	reportPreparedNode := func() {
		teardownPreparedNode()
		if progressCh != nil {
			progressCh <- node
		}
	}
	defer teardownPreparedNode()

	var stagingPath string
	ctx, buildSession, err := r.startBuildSession(ctx, plan, node)
	if buildSession != nil {
		defer func() {
			if closeErr := buildSession.Close(stagingPath); closeErr != nil {
				logger.Warn(ctx, "Failed to release build path locks", tag.Error(closeErr))
			}
		}()
	}
	if err != nil {
		r.setLastError(err)
		node.MarkError(err)
		node.SetStatus(ir.NodeFailed)
		reportPreparedNode()
		return
	}

	// Check preconditions
	logger.Debug(ctx, "Checking preconditions")
	preconditionProgress := progressCh
	if buildSession != nil {
		preconditionProgress = nil
	}
	met, err := meetsPreconditions(ctx, node, preconditionProgress)
	if err != nil {
		markBuildPrecondition(buildSession, node, ir.BuildReasonPreconditionError, "", progressCh)
		r.setLastError(err)
		r.Cancel(plan)
		return
	}
	if !met {
		markBuildPrecondition(buildSession, node, ir.BuildReasonPreconditionNotMet,
			"step precondition was not met", progressCh)
		return
	}
	if r.evaluateBuildNode(ctx, node, buildSession, reportPreparedNode) {
		return
	}

	// Setup chat messages from dependencies before execution
	r.setupChatMessages(ctx, node)
	r.setupPushBackConversation(ctx, node)
	declaredStep := node.Step()

ExecRepeat: // repeat execution
	for !r.isCanceled() {
		logger.Debug(ctx, "Executing node loop")
		attemptCtx, nextStagingPath, attemptErr := prepareBuildAttempt(ctx, node, buildSession, declaredStep)
		stagingPath = nextStagingPath
		if attemptErr != nil {
			r.setLastError(attemptErr)
			node.MarkError(attemptErr)
			node.SetStatus(ir.NodeFailed)
			return
		}
		execErr := r.execNode(attemptCtx, node, progressCh)
		if execErr == nil {
			committed, commitErr := commitBuildAttempt(attemptCtx, node, buildSession, stagingPath)
			if committed {
				stagingPath = ""
			}
			execErr = commitErr
		}
		isRetriable := r.handleNodeExecutionError(ctx, plan, node, execErr)
		if isRetriable {
			if stagingPath != "" {
				_ = os.Remove(stagingPath)
				stagingPath = ""
			}
			continue ExecRepeat
		}
		if node.State().Status == ir.NodeRetrying {
			reportPreparedNode()
			return
		}

		if node.State().Status != ir.NodeAborted {
			node.IncDoneCount()
		}

		shouldRepeat := r.shouldRepeatNode(ctx, node, execErr)
		if shouldRepeat && !r.isCanceled() {
			if stagingPath != "" {
				_ = os.Remove(stagingPath)
				stagingPath = ""
			}
			r.prepareNodeForRepeat(ctx, node, progressCh)
			continue
		}

		if execErr != nil && progressCh != nil {
			reportPreparedNode()
			return
		}

		break ExecRepeat
	}

	// Determine final status for nodes still in running state.
	// Repetitive tasks complete naturally (signal not sent - see runner.Signal).
	// Only mark as aborted if: not a repetitive task AND runner was canceled.
	if node.State().Status == ir.NodeRunning {
		isRepetitive := node.Step().RepeatPolicy.RepeatMode != ""
		if !isRepetitive && r.isCanceled() {
			node.SetStatus(ir.NodeAborted)
		} else if node.Step().Approval != nil {
			// Step has approval config — enter waiting state for human review.
			// Push-back is human-controlled, no iteration limit.
			node.SetStatus(ir.NodeWaiting)
		} else {
			node.SetStatus(ir.NodeSucceeded)
		}
	}

	// For executors that implement NodeStatusDeterminer (e.g. call/dag, parallel),
	// the status may have been set to NodeSucceeded by the executor.
	// If the step has an approval config, override to NodeWaiting.
	if node.State().Status == ir.NodeSucceeded && node.Step().Approval != nil {
		node.SetStatus(ir.NodeWaiting)
	}
	if node.State().Status == ir.NodeSucceeded && buildSession != nil {
		metadata := buildSession.Metadata()
		metadata.Phase = ir.BuildPhaseComplete
		node.setBuild(metadata)
	}

	// Save chat messages after execution (including waiting steps for push-back continuity).
	if node.State().Status == ir.NodeSucceeded || node.State().Status == ir.NodeWaiting {
		r.saveChatMessages(ctx, node)
	}

	reportPreparedNode()
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

	filteredInputs := dagrun.FilterPushBackInputs(allowedInputs, state.PushBackInputs)
	for k, v := range filteredInputs {
		env = env.WithEnvVars(k, v)
	}
	env = env.WithEnvVars(runenv.EnvKeyDAGPushBackIteration, strconv.Itoa(state.ApprovalIteration))
	if state.PushBackPreviousStdout != "" {
		env = env.WithEnvVars(runenv.EnvKeyDAGPushBackPreviousStdoutFile, state.PushBackPreviousStdout)
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
		env = env.WithEnvVars(runenv.EnvKeyDAGPushBack, payload)
	}

	return WithEnv(ctx, env)
}

func (r *Runner) setLastError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastError = err
}

func (r *Runner) setPreconditionResults(results []ir.ConditionResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preconditions = slices.Clone(results)
}

// PreconditionResults returns the latest DAG-level precondition evaluation.
func (r *Runner) PreconditionResults() []ir.ConditionResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.preconditions)
}

func (r *Runner) prepareNode(ctx context.Context, node *Node) error {
	if r.dry {
		return nil
	}
	return node.Prepare(ctx, r.logDir, r.dagRunID)
}

func (r *Runner) runHumanTask(ctx context.Context, plan *Plan, node *Node, progressCh chan *Node) {
	ctx = r.setupNodeExecutionEnv(ctx, node)
	met, err := meetsPreconditions(ctx, node, progressCh)
	if err != nil {
		r.setLastError(err)
		r.Cancel(plan)
		return
	}
	if !met {
		return
	}

	task := node.Step().HumanTask
	prompt, err := resolveRuntimeString(ctx, task.Prompt, cmnvalue.WorkflowField("with.prompt"))
	if err != nil {
		err = fmt.Errorf("failed to evaluate human task prompt: %w", err)
		r.setLastError(err)
		node.MarkError(err)
		node.SetStatus(ir.NodeFailed)
		if progressCh != nil {
			progressCh <- node
		}
		return
	}

	if r.dry {
		node.CompleteHumanTaskDryRun(prompt)
	} else {
		node.OpenHumanTask(prompt, time.Now())
	}
	if progressCh != nil {
		progressCh <- node
	}
}

func (r *Runner) teardownNode(node *Node) error {
	if r.dry {
		return nil
	}
	return node.Teardown()
}

func (r *Runner) teardownPreparedNode(node *Node) {
	if err := r.teardownNode(node); err != nil {
		r.setLastError(err)
		node.SetStatus(ir.NodeFailed)
	}
}

// setupChatMessages loads and merges chat messages from dependent steps.
func (r *Runner) setupChatMessages(ctx context.Context, node *Node) {
	if r.messagesHandler == nil {
		return
	}

	step := node.Step()

	if !stepSupportsChatMessages(step) {
		return
	}

	if len(step.Depends) == 0 {
		return
	}

	// Read messages from each dependency step
	var inherited []ir.LLMMessage
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
	inherited = ir.DeduplicateSystemMessages(inherited)
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
	if !stepSupportsChatMessages(step) {
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

func stepSupportsChatMessages(step ir.Step) bool {
	return registry.ExecutorCapabilitiesFor(step.ExecutorConfig.Type).LLM
}

func (r *Runner) setupVariables(ctx context.Context, plan *Plan, node *Node) (context.Context, error) {
	env, err := NewPlanEnvForNodeWithError(ctx, node, plan)
	if err != nil {
		return ctx, err
	}
	node.SetWorkingDir(env.WorkingDir)

	// Load output variables and approval inputs from predecessor nodes.
	for _, predNode := range planPredecessorNodes(plan, node) {
		// Add predecessor outputs to scope
		if outputs := predNode.OutputVariablesMap(); len(outputs) > 0 {
			stepID := predNode.Step().ID
			if stepID == "" {
				stepID = predNode.Step().Name
			}
			env.Scope = env.Scope.WithStepOutputs(outputs, stepID)
		}
	}

	// Path-dependent environment values are resolved after build paths
	// enter scope. Output-dependent values are refreshed for every attempt.
	stepEnv := node.Step().Env
	if env.DAG != nil && env.DAG.Type == ir.TypeBuild {
		_, hasPathOutput := node.Step().PathOutput()
		if len(node.Step().Inputs) > 0 || hasPathOutput {
			stepEnv = environmentWithoutAttemptPaths(stepEnv)
		}
	}
	if err := addResolvedEnvVars(ctx, &env, stepEnv, "env.", cmnvalue.StepEnvField); err != nil {
		return ctx, err
	}

	// Add container environment variables (step-level takes precedence over DAG-level)
	// This ensures container env vars are available when evaluating command arguments
	if ct := node.Step().Container; ct != nil {
		if err := addResolvedEnvVars(ctx, &env, ct.Env, "container.env.", cmnvalue.ContainerEnvField); err != nil {
			return ctx, err
		}
	} else if dag := env.DAG; dag != nil && dag.Container != nil {
		if err := addResolvedEnvVars(ctx, &env, dag.Container.Env, "container.env.", cmnvalue.ContainerEnvField); err != nil {
			return ctx, err
		}
	}
	if _, err := env.ResolveShell(ctx); err != nil {
		return ctx, err
	}

	return WithEnv(ctx, env), nil
}

func environmentWithoutAttemptPaths(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if !cmnvalue.HasReferenceToNamespace(value, "inputs", "outputs") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func environmentWithoutAttemptOutputs(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if !cmnvalue.HasReferenceToNamespace(value, "outputs") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func addResolvedEnvVars(ctx context.Context, env *Env, envList []string, fieldPrefix string, fieldForKey func(string) cmnvalue.Field) error {
	for _, v := range envList {
		key, value, found := strings.Cut(v, "=")
		if !found {
			return fmt.Errorf("invalid environment variable format %q", v)
		}
		evaluatedValue, err := resolverFromEnv(*env).String(ctx, value, fieldForKey(fieldPrefix+key))
		if err != nil {
			return fmt.Errorf("failed to evaluate environment variable %q: %w", v, err)
		}
		env.Scope = env.Scope.WithEntry(key, evaluatedValue, cmnvalue.EnvSourceStepEnv)
	}
	return nil
}

func (r *Runner) setupEnvironEventHandler(
	ctx context.Context,
	plan *Plan,
	node *Node,
	extraEnvs map[string]string,
) (context.Context, error) {
	// Preserve any extra env vars from the incoming context (e.g., DAG_WAITING_STEPS)
	existingEnv := GetEnv(ctx)

	env, err := NewPlanEnvWithError(ctx, node.Step(), plan)
	if err != nil {
		return ctx, err
	}
	disableDeclaredStepOutputs(&env)
	node.SetWorkingDir(env.WorkingDir)

	// Add DAG_RUN_STATUS to scope
	env.Scope = env.Scope.WithEntry(
		runenv.EnvKeyDAGRunStatus,
		r.Status(ctx, plan).String(),
		cmnvalue.EnvSourceStepEnv,
	)

	// Copy extra env vars from existing scope that aren't already set
	if existingEnv.Scope != nil {
		for k, v := range existingEnv.Scope.AllBySource(cmnvalue.EnvSourceStepEnv) {
			if _, exists := env.Scope.Get(k); !exists {
				env.Scope = env.Scope.WithEntry(k, v, cmnvalue.EnvSourceStepEnv)
			}
		}
	}

	for k, v := range extraEnvs {
		env.Scope = env.Scope.WithEntry(k, v, cmnvalue.EnvSourceStepEnv)
	}

	if err := addResolvedEnvVars(ctx, &env, node.Step().Env, "env.", cmnvalue.StepEnvField); err != nil {
		return ctx, err
	}
	if _, err := env.ResolveShell(ctx); err != nil {
		return ctx, err
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

	return WithEnv(ctx, env), nil
}

func disableDeclaredStepOutputs(env *Env) {
	if env == nil {
		return
	}
	for id, info := range env.StepMap {
		info.DeclaredOutputs = nil
		env.StepMap[id] = info
	}
}

func (r *Runner) execNode(ctx context.Context, node *Node, progressCh chan *Node) error {
	if r.dry {
		return nil
	}
	report := func() {
		if progressCh != nil {
			progressCh <- node
		}
	}
	if progressCh != nil && node.Step().SubDAG != nil {
		// Send an additional progress notification after the executor is set up
		// so that SubRuns are persisted to storage before the subDAG starts running.
		return r.stepExecutor.ExecuteWithProgress(ctx, node, report, report)
	}
	return r.stepExecutor.ExecuteWithProgress(ctx, node, nil, report)
}

// Signal sends a signal to the runner.
// for a node with repeat policy, it does not stop the node and
// wait to finish current run.
func (r *Runner) Signal(
	ctx context.Context, plan *Plan, sig os.Signal, done chan bool, allowOverride bool,
) {
	r.Stop(ctx, plan, cmdutil.TerminationFromSignal(sig), done, allowOverride)
}

// Stop requests that all active nodes stop according to lifecycle intent.
func (r *Runner) Stop(
	ctx context.Context, plan *Plan, intent cmdutil.TerminationIntent, done chan bool, allowOverride bool,
) {
	isTermination := intent.IsTermination()

	// Record termination before inspecting nodes so execution that has not
	// started yet observes the request.
	if isTermination {
		plan.requestCancel()
		if !r.isCanceled() {
			r.setCanceled()
		}
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
		node.Stop(ctx, intent, allowOverride)
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
	p.requestCancel()
	r.setCanceled()
	for _, node := range p.Nodes() {
		node.Cancel()
	}
}

// Status returns the status of the runner.
func (r *Runner) Status(ctx context.Context, p *Plan) ir.Status {
	if r.isFailed() {
		return ir.Failed
	}
	if r.isCanceled() && !r.isSucceed(p) {
		return ir.Aborted
	}
	if !p.IsStarted() {
		return ir.NotStarted
	}

	// Get node states atomically, then check plan finished state.
	// Note: IsFinished() is called separately, so there's a small window where
	// the plan could be marked finished between these calls. This is acceptable
	// for status reporting as it self-corrects on the next Status() call.
	states := p.NodeStates()

	if states.HasRunning {
		return ir.Running
	}
	if states.HasRetrying {
		return ir.Queued
	}
	if states.HasRejected {
		return ir.Rejected
	}
	if states.HasWaiting {
		return ir.Waiting
	}
	if states.HasNotStarted && !p.IsFinished() {
		return ir.Running
	}
	if r.forcedStatus != nil {
		return *r.forcedStatus
	}

	if r.isPartialSuccess(ctx, p) {
		return ir.PartiallySucceeded
	}

	if r.isError() {
		return ir.Failed
	}

	return ir.Succeeded
}

func (r *Runner) isError() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastError != nil
}

// HandlerNode returns the handler node with the given name.
func (r *Runner) HandlerNode(name ir.HandlerType) *Node {
	r.handlerMu.RLock()
	defer r.handlerMu.RUnlock()
	if v, ok := r.handlers[name]; ok {
		return v
	}
	return nil
}

// handlersBeforeSteps and handlersAfterSteps list the lifecycle handlers by
// when they run relative to the DAG's steps.
var (
	handlersBeforeSteps = []ir.HandlerType{ir.HandlerOnInit}
	handlersAfterSteps  = []ir.HandlerType{
		ir.HandlerOnWait,
		ir.HandlerOnSuccess,
		ir.HandlerOnFailure,
		ir.HandlerOnAbort,
		ir.HandlerOnExit,
	}
)

// NodesInRunOrder returns the plan's step nodes together with the lifecycle
// handler nodes that were configured, ordered by when they run. Handlers that
// the DAG does not declare are omitted.
func (r *Runner) NodesInRunOrder(plan *Plan) []*Node {
	var steps []*Node
	if plan != nil {
		steps = plan.Nodes()
	}

	nodes := make([]*Node, 0, len(steps)+len(handlersBeforeSteps)+len(handlersAfterSteps))
	for _, handler := range handlersBeforeSteps {
		if node := r.HandlerNode(handler); node != nil {
			nodes = append(nodes, node)
		}
	}
	nodes = append(nodes, steps...)
	for _, handler := range handlersAfterSteps {
		if node := r.HandlerNode(handler); node != nil {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// isCanceled returns true if the runner is canceled.
func (r *Runner) isCanceled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.canceled == 1
}

func (r *Runner) isFailed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.failed == 1
}

func isReady(ctx context.Context, plan *Plan, node *Node) bool {
	for _, depID := range plan.Dependencies(node.id) {
		dep := plan.GetNode(depID)
		status := dep.State().Status

		switch status {
		case ir.NodeSucceeded, ir.NodePartiallySucceeded:
			continue

		case ir.NodeFailed:
			if dep.ShouldContinue(ctx) {
				logger.Debug(ctx, "Dependency failed but allowed to continue",
					tag.Step(node.Name()), tag.Dependency(dep.Name()))
				continue
			}
			logger.Debug(ctx, "Dependency failed",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(ir.NodeAborted)
			node.SetError(ErrUpstreamFailed)
			return false

		case ir.NodeSkipped:
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
			node.SetStatus(ir.NodeSkipped)
			node.SetError(ErrUpstreamSkipped)
			return false

		case ir.NodeAborted:
			logger.Debug(ctx, "Dependency aborted",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(ir.NodeAborted)
			return false

		case ir.NodeRejected:
			logger.Debug(ctx, "Dependency rejected",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			node.SetStatus(ir.NodeAborted)
			node.SetError(ErrUpstreamRejected)
			return false

		case ir.NodeNotStarted, ir.NodeRunning:
			logger.Debug(ctx, "Dependency not finished",
				tag.Step(node.Name()), tag.Dependency(dep.Name()),
				tag.Status(status.String()))
			return false

		case ir.NodeRetrying:
			logger.Debug(ctx, "Dependency waiting for retry",
				tag.Step(node.Name()), tag.Dependency(dep.Name()))
			return false

		case ir.NodeWaiting:
			logger.Debug(ctx, "Dependency waiting for manual completion",
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
		node.SetStatus(ir.NodeSucceeded)
		return nil
	}

	var err error
	ctx, err = r.setupEnvironEventHandler(ctx, plan, node, extraEnvs)
	if err != nil {
		node.SetStatus(ir.NodeFailed)
		return err
	}

	if err := node.Prepare(ctx, r.logDir, r.dagRunID); err != nil {
		node.SetStatus(ir.NodeFailed)
		return err
	}
	defer func() { _ = node.Teardown() }()

	if err := node.evalPreconditions(ctx); err != nil {
		if errors.Is(err, ErrConditionNotMet) {
			node.SetStatus(ir.NodeSkipped)
			return nil
		}
		node.SetStatus(ir.NodeFailed)
		return err
	}

	node.SetStatus(ir.NodeRunning)

	if err := r.stepExecutor.Execute(ctx, node); err != nil {
		node.SetStatus(ir.NodeFailed)
		return err
	}

	node.SetStatus(ir.NodeSucceeded)
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

	r.handlers = make(map[ir.HandlerType]*Node)
	handlerSteps := map[ir.HandlerType]*ir.Step{
		ir.HandlerOnInit:    r.onInit,
		ir.HandlerOnExit:    r.onExit,
		ir.HandlerOnSuccess: r.onSuccess,
		ir.HandlerOnFailure: r.onFailure,
		ir.HandlerOnAbort:   r.onAbort,
		ir.HandlerOnWait:    r.onWait,
	}
	for handlerType, step := range handlerSteps {
		if step != nil {
			r.handlers[handlerType] = &Node{Data: newSafeData(NodeData{Step: *step})}
		}
	}

	r.metrics.startTime = time.Now()

	logger.Debug(ctx, "Runner setup complete",
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

func (r *Runner) setFailed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = 1
}

func (r *Runner) resetRunState(plan *Plan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.canceled = 0
	if plan.isCancelRequested() {
		r.canceled = 1
	}
	r.failed = 0
	r.lastError = nil
	r.preconditions = nil
}

func (r *Runner) isSucceed(p *Plan) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, node := range p.Nodes() {
		nodeStatus := node.State().Status
		if nodeStatus == ir.NodeSucceeded || nodeStatus == ir.NodeSkipped || nodeStatus == ir.NodePartiallySucceeded {
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
		if node.State().Status == ir.NodeFailed {
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
		case ir.NodeSucceeded:
			hasSuccessfulNodes = true
		case ir.NodeFailed:
			if node.ShouldContinue(ctx) && !node.ShouldMarkSuccess(ctx) {
				hasFailuresWithContinueOn = true
			}
		case ir.NodePartiallySucceeded:
			// Partial success at node level contributes to overall partial success
			hasFailuresWithContinueOn = true
			hasSuccessfulNodes = true
		case ir.NodeNotStarted, ir.NodeRunning, ir.NodeRetrying, ir.NodeAborted, ir.NodeSkipped, ir.NodeWaiting, ir.NodeRejected:
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
		node.SetStatus(ir.NodeFailed)
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
		node.SetStatus(ir.NodeRetrying)
		logger.Info(ctx, "Step retry will be scheduled by the parent executor",
			slog.Int("retry", node.GetRetryCount()),
			slog.Duration("interval", ir.CalculateBackoffInterval(
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
	interval := ir.CalculateBackoffInterval(
		node.Step().RetryPolicy.Interval,
		node.Step().RetryPolicy.Backoff,
		node.Step().RetryPolicy.MaxInterval,
		node.GetRetryCount()-1, // -1 because we just incremented
	)
	time.Sleep(interval)
	node.SetRetriedAt(time.Now())
	node.SetStatus(ir.NodeRunning)
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
	case ir.NodeSucceeded, ir.NodePartiallySucceeded:
		r.metrics.completedNodes++
	case ir.NodeFailed, ir.NodeRejected:
		r.metrics.failedNodes++
	case ir.NodeSkipped:
		r.metrics.skippedNodes++
	case ir.NodeAborted:
		r.metrics.canceledNodes++
	case ir.NodeWaiting, ir.NodeNotStarted, ir.NodeRunning, ir.NodeRetrying:
		// Waiting nodes are counted when they complete after manual input.
		// NotStarted/Running should not happen at this point.
	}

	if node.State().Status != ir.NodeWaiting || node.Step().HumanTask == nil {
		node.Finish()
	}
	if wg != nil {
		wg.Done()
	}
}

func externalStepRetryEnabled(ctx context.Context) bool {
	if os.Getenv(runenv.EnvKeyExternalStepRetry) != "" {
		return true
	}

	rCtx := runctx.GetContext(ctx)
	if value, ok := rCtx.UserEnvsMap()[runenv.EnvKeyExternalStepRetry]; ok {
		return value != ""
	}
	return false
}

// checkPreconditions evaluates the preconditions for a node and updates its status accordingly.
func meetsPreconditions(ctx context.Context, node *Node, progressCh chan *Node) (bool, error) {
	err := node.evalPreconditions(ctx)
	if err != nil {
		if errors.Is(err, ErrConditionNotMet) {
			node.SetStatus(ir.NodeSkipped)
			if progressCh != nil {
				progressCh <- node
			}
			return false, nil
		}
		node.SetStatus(ir.NodeFailed)
		node.SetError(err)
		if progressCh != nil {
			progressCh <- node
		}
		return false, err
	}
	return true, nil
}

// handleNodeExecutionError handles the error from node execution and determines if it should be retried.
func (r *Runner) handleNodeExecutionError(ctx context.Context, plan *Plan, node *Node, execErr error) bool {
	if execErr == nil {
		return false // no error, nothing to handle
	}

	s := node.State().Status
	switch {
	case s == ir.NodeSucceeded || s == ir.NodeAborted || s == ir.NodePartiallySucceeded:
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
			node.SetStatus(ir.NodeFailed)
		} else if r.isTimeout(plan.StartAt()) {
			// DAG-level timeout -> treat as aborted (global cancellation semantics)
			logger.Info(ctx, "Step deadline exceeded (DAG-level timeout)",
				tag.Timeout(r.timeout),
				tag.Error(execErr),
			)
			node.SetStatus(ir.NodeAborted)
		} else {
			// Parent context canceled or other deadline; mark aborted for safety
			logger.Info(ctx, "Step deadline exceeded", tag.Error(execErr))
			node.SetStatus(ir.NodeAborted)
		}
		r.setLastError(execErr)

	case r.isTimeout(plan.StartAt()):
		// DAG-level timeout (non-context error case)
		logger.Info(ctx, "Step deadline exceeded (DAG-level timeout)",
			tag.Timeout(r.timeout),
			tag.Error(execErr),
		)
		node.SetStatus(ir.NodeAborted)
		r.setLastError(execErr)

	case r.isCanceled():
		node.SetStatus(ir.NodeAborted)

	case node.retryPolicy.Limit > node.GetRetryCount():
		if r.shouldRetryNode(ctx, node, execErr) {
			return true
		}

	default:
		// node execution error is unexpected and unrecoverable
		node.SetStatus(ir.NodeFailed)
		if node.ShouldMarkSuccess(ctx) {
			// mark as success if the node should be force marked as success
			// i.e. continueOn.markSuccess is set to true
			node.SetStatus(ir.NodeSucceeded)
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
	case ir.RepeatModeWhile:
		return r.evalWhileCondition(ctx, shell, node, rp, execErr)
	case ir.RepeatModeUntil:
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
func (r *Runner) evalWhileCondition(ctx context.Context, shell []string, node *Node, rp ir.RepeatPolicy, execErr error) bool {
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
func (r *Runner) evalUntilCondition(ctx context.Context, shell []string, node *Node, rp ir.RepeatPolicy, execErr error) bool {
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

	node.SetStatus(ir.NodeRunning) // reset status to running for the repeat
	if r.lastError == node.Error() {
		r.setLastError(nil) // clear last error if we are repeating
	}
	logger.Info(ctx, "Step will be repeated",
		slog.Duration("interval", step.RepeatPolicy.Interval),
	)
	interval := ir.CalculateBackoffInterval(
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

func NewPlanEnv(ctx context.Context, step ir.Step, plan *Plan) Env {
	env := NewEnv(ctx, step)
	addInheritedStepMap(ctx, &env)
	addPlanStepsToEnv(&env, plan)
	return env
}

func NewPlanEnvWithError(ctx context.Context, step ir.Step, plan *Plan) (Env, error) {
	env, err := NewEnvWithError(ctx, step)
	if err != nil {
		return Env{}, err
	}
	addInheritedStepMap(ctx, &env)
	addPlanStepsToEnv(&env, plan)
	return env, nil
}

func NewPlanEnvForNode(ctx context.Context, node *Node, plan *Plan) Env {
	env := NewEnv(ctx, node.Step())
	addInheritedStepMap(ctx, &env)
	addPlanPredecessorStepsToEnv(&env, plan, node)
	return env
}

func NewPlanEnvForNodeWithError(ctx context.Context, node *Node, plan *Plan) (Env, error) {
	env, err := NewEnvWithError(ctx, node.Step())
	if err != nil {
		return Env{}, err
	}
	addInheritedStepMap(ctx, &env)
	addPlanPredecessorStepsToEnv(&env, plan, node)
	return env, nil
}

func addInheritedStepMap(ctx context.Context, env *Env) {
	if env == nil {
		return
	}
	inherited, ok := LookupEnv(ctx)
	if !ok || len(inherited.StepMap) == 0 {
		return
	}
	if env.StepMap == nil {
		env.StepMap = make(map[string]cmnvalue.StepInfo, len(inherited.StepMap))
	}
	for id, info := range inherited.StepMap {
		if _, exists := env.StepMap[id]; !exists {
			env.StepMap[id] = info
		}
	}
}

func addPlanStepsToEnv(env *Env, plan *Plan) {
	for _, n := range plan.Nodes() {
		if n.Step().ID != "" {
			env.StepMap[n.Step().ID] = n.StepInfo()
		}
	}
}

func addPlanPredecessorStepsToEnv(env *Env, plan *Plan, node *Node) {
	for _, n := range planPredecessorNodes(plan, node) {
		if n.Step().ID != "" {
			env.StepMap[n.Step().ID] = n.StepInfo()
		}
	}
}

func planPredecessorNodes(plan *Plan, node *Node) []*Node {
	if plan == nil || node == nil {
		return nil
	}

	// An agent plan has no edges: the agent picks the order, so every
	// action it has already run is upstream of the one starting now.
	if plan.IsAgent() {
		var nodes []*Node
		for _, candidate := range plan.Nodes() {
			if candidate.ID() == node.ID() || candidate.Name() == ir.AgentStepName {
				continue
			}
			if candidate.State().Status.IsDone() {
				nodes = append(nodes, candidate)
			}
		}
		return nodes
	}

	visited := make(map[int]struct{})
	queue := append([]int(nil), plan.Dependencies(node.ID())...)
	var nodes []*Node
	for len(queue) > 0 {
		predID := queue[0]
		queue = queue[1:]
		if _, ok := visited[predID]; ok {
			continue
		}
		visited[predID] = struct{}{}
		queue = append(queue, plan.Dependencies(predID)...)
		predNode := plan.GetNode(predID)
		if predNode != nil {
			nodes = append(nodes, predNode)
		}
	}
	return nodes
}
