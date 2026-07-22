// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	defaultRunnerScanInterval = time.Second
	defaultRunnerWorkers      = 4
	maxDAGRunRefs             = 20
	maxEvidenceBytes          = 32 << 10
	maxLastErrorBytes         = 1 << 10
)

var errCandidateRejected = errors.New("controller runtime candidate was rejected")

// ChildRunRequest is the durable child request selected by the Router.
type ChildRunRequest struct {
	ControllerID string
	Workspace    string
	State        string
	DAG          string
	DAGRunID     string
	Params       json.RawMessage
}

// ChildRunObservation describes the latest visible child attempt. Output values
// must already have passed the existing runtime secret-masking boundary.
type ChildRunObservation struct {
	Exists        bool
	Status        core.Status
	Outputs       map[string]string
	ErrorCategory string
}

// ChildRunGateway reuses the existing DAG enqueue, status, output, and stop paths.
type ChildRunGateway interface {
	EnsureEnqueued(ctx context.Context, request ChildRunRequest) error
	Observe(ctx context.Context, request ChildRunRequest) (ChildRunObservation, error)
	Stop(ctx context.Context, request ChildRunRequest) error
}

// DAGRunIDGenerator supplies the existing DAG-run identity format.
type DAGRunIDGenerator interface {
	GenDAGRunID(ctx context.Context) (string, error)
}

// DAGRunIDGeneratorFunc adapts a function to DAGRunIDGenerator.
type DAGRunIDGeneratorFunc func(context.Context) (string, error)

// GenDAGRunID implements DAGRunIDGenerator.
func (f DAGRunIDGeneratorFunc) GenDAGRunID(ctx context.Context) (string, error) {
	return f(ctx)
}

// RunnerOption configures Controller reconciliation.
type RunnerOption func(*Runner)

// WithRunnerTiming overrides scan timing and worker count.
func WithRunnerTiming(interval time.Duration, workers int) RunnerOption {
	return func(runner *Runner) {
		if interval > 0 {
			runner.scanInterval = interval
		}
		if workers > 0 {
			runner.workers = workers
		}
	}
}

// WithRunnerClock overrides persisted lifecycle timestamps.
func WithRunnerClock(now func() time.Time) RunnerOption {
	return func(runner *Runner) {
		if now != nil {
			runner.now = now
		}
	}
}

// Runner reconciles every active Controller owned by the active scheduler.
type Runner struct {
	definitions  DefinitionStore
	runtimes     RuntimeStore
	locker       ResourceLocker
	validator    *Validator
	router       *Router
	children     ChildRunGateway
	runIDs       DAGRunIDGenerator
	scanInterval time.Duration
	workers      int
	now          func() time.Time
	guards       keyedGuard
}

// NewRunner constructs a scheduler-owned Controller runner.
func NewRunner(
	definitions DefinitionStore,
	runtimes RuntimeStore,
	locker ResourceLocker,
	validator *Validator,
	router *Router,
	children ChildRunGateway,
	runIDs DAGRunIDGenerator,
	opts ...RunnerOption,
) *Runner {
	if validator == nil {
		validator = NewValidator(nil)
	}
	runner := &Runner{
		definitions: definitions, runtimes: runtimes, locker: locker,
		validator: validator, router: router, children: children, runIDs: runIDs,
		scanInterval: defaultRunnerScanInterval, workers: defaultRunnerWorkers, now: time.Now,
	}
	for _, opt := range opts {
		opt(runner)
	}
	return runner
}

// Run scans immediately and then periodically until the scheduler cancels the context.
func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.definitions == nil || r.runtimes == nil || r.locker == nil {
		return
	}
	r.scan(ctx)
	ticker := time.NewTicker(r.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scan(ctx)
		}
	}
}

func (r *Runner) scan(ctx context.Context) {
	ids, err := r.runtimes.List(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list Controller runtimes", tag.Error(err))
		return
	}
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range r.workers {
		workers.Go(func() {
			for id := range jobs {
				if !r.guards.TryLock(id) {
					continue
				}
				func() {
					defer r.guards.Unlock(id)
					r.reconcile(ctx, id)
				}()
			}
		})
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		case jobs <- id:
		}
	}
	close(jobs)
	workers.Wait()
}

func (r *Runner) reconcile(ctx context.Context, id string) {
	definition, runtime, err := r.load(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			logger.Error(ctx, "Failed to load Controller", tag.Error(err))
		}
		return
	}
	if runtime == nil {
		return
	}

	switch {
	case runtime.Status == core.Aborted:
		r.reconcileAborted(ctx, *definition, *runtime)
	case runtime.ActiveDAGRun != nil:
		r.reconcileChild(ctx, *definition, *runtime)
	case runtime.Status == core.Waiting:
		return
	case runtime.Status == core.Running:
		r.reconcileRoute(ctx, *definition)
	}
}

func (r *Runner) reconcileAborted(ctx context.Context, definition Definition, runtime Runtime) {
	if runtime.ActiveDAGRun == nil {
		r.settleAborted(ctx, definition.ID, "", false)
		return
	}
	if r.children == nil {
		return
	}
	request := childRequest(definition, runtime)
	observation, err := r.children.Observe(ctx, request)
	if err != nil {
		logger.Error(ctx, "Failed to inspect stopped Controller child", tag.Error(err))
		return
	}
	if !observation.Exists {
		r.settleAborted(ctx, definition.ID, runtime.ActiveDAGRun.DAGRunID, false)
		return
	}
	switch classifyChildStatus(observation.Status) {
	case childStatusTerminal, childStatusInvalid:
		r.settleAborted(ctx, definition.ID, runtime.ActiveDAGRun.DAGRunID, true)
		return
	case childStatusActive:
	}
	if err := r.children.Stop(ctx, request); err != nil && ctx.Err() == nil {
		logger.Error(ctx, "Failed to stop Controller child", tag.Error(err))
	}
}

func (r *Runner) settleAborted(ctx context.Context, id, childRunID string, addRef bool) {
	_ = r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil {
			return err
		}
		if runtime.Status != core.Aborted {
			return nil
		}
		if childRunID != "" && (runtime.ActiveDAGRun == nil || runtime.ActiveDAGRun.DAGRunID != childRunID) {
			return nil
		}
		if runtime.ActiveDAGRun == nil && runtime.FinishedAt != nil {
			return nil
		}
		now := r.now().UTC()
		if addRef && runtime.ActiveDAGRun != nil {
			appendRunRef(runtime, DAGRunRef{State: runtime.CurrentState, DAG: runtime.ActiveDAGRun.DAG, DAGRunID: runtime.ActiveDAGRun.DAGRunID})
		}
		runtime.ActiveDAGRun = nil
		runtime.FinishedAt = &now
		runtime.UpdatedAt = now
		return r.runtimes.Put(ctx, runtime)
	})
}

func (r *Runner) reconcileChild(ctx context.Context, definition Definition, runtime Runtime) {
	if r.children == nil {
		r.failActiveChild(ctx, definition.ID, runtime.ActiveDAGRun.DAGRunID, "child_dispatch_unavailable")
		return
	}
	request := childRequest(definition, runtime)
	observation, err := r.children.Observe(ctx, request)
	if err != nil {
		if ctx.Err() == nil {
			logger.Error(ctx, "Failed to inspect Controller child", tag.Error(err))
			if observation.Exists {
				r.failObservedActiveChild(ctx, definition.ID, request, "child_observation_failed")
			} else {
				r.failActiveChild(ctx, definition.ID, request.DAGRunID, "child_observation_failed")
			}
		}
		return
	}
	if !observation.Exists {
		if err := r.ensureActiveChildEnqueued(ctx, definition.ID, request); err != nil {
			if ctx.Err() == nil {
				r.failActiveChild(ctx, definition.ID, request.DAGRunID, "child_enqueue_failed")
			}
		}
		return
	}

	switch classifyChildStatus(observation.Status) {
	case childStatusInvalid:
		r.failObservedActiveChild(ctx, definition.ID, request, "child_status_invalid")
	case childStatusActive:
		if observation.Status != core.Queued {
			r.updateObservedChild(ctx, definition.ID, request, observation.Status)
			return
		}
		if err := r.ensureActiveChildEnqueued(ctx, definition.ID, request); err != nil {
			if ctx.Err() == nil {
				r.failActiveChild(ctx, definition.ID, request.DAGRunID, "child_enqueue_failed")
			}
			return
		}
		r.updateObservedChild(ctx, definition.ID, request, observation.Status)
	case childStatusTerminal:
		r.finishObservedChild(ctx, definition.ID, request, observation)
	}
}

func (r *Runner) updateObservedChild(ctx context.Context, id string, request ChildRunRequest, status core.Status) {
	_ = r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil || !matchesActive(runtime, request.DAGRunID) || runtime.Status == core.Aborted {
			return err
		}
		base := cloneRuntime(runtime)
		hadRunRef := hasRunRef(runtime, request.DAGRunID)
		appendRunRef(runtime, DAGRunRef{State: request.State, DAG: request.DAG, DAGRunID: request.DAGRunID})
		nextStatus := core.Running
		if status == core.Waiting {
			nextStatus = core.Waiting
		}
		if runtime.Status == nextStatus {
			if hadRunRef {
				return nil
			}
			runtime.UpdatedAt = r.now().UTC()
			return r.putCandidate(ctx, base, runtime)
		}
		runtime.Status = nextStatus
		runtime.UpdatedAt = r.now().UTC()
		return r.putCandidate(ctx, base, runtime)
	})
}

func (r *Runner) finishObservedChild(ctx context.Context, id string, request ChildRunRequest, observation ChildRunObservation) {
	_ = r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil || !matchesActive(runtime, request.DAGRunID) || runtime.Status == core.Aborted {
			return err
		}
		evidence, err := ExecutionEvidenceMessage(*runtime.ActiveDAGRun, observation)
		if err != nil {
			return r.failRuntime(ctx, runtime, "child_result_invalid")
		}
		base := cloneRuntime(runtime)
		runtime.Context = append(runtime.Context, evidence)
		appendRunRef(runtime, DAGRunRef{State: request.State, DAG: request.DAG, DAGRunID: request.DAGRunID})
		runtime.ActiveDAGRun = nil
		runtime.WaitingQuestion = nil
		now := r.now().UTC()
		runtime.UpdatedAt = now
		if observation.Status == core.Succeeded || observation.Status == core.PartiallySucceeded {
			runtime.Status = core.Running
			runtime.FinishedAt = nil
			runtime.LastError = nil
		} else {
			runtime.Status = core.Failed
			runtime.FinishedAt = &now
			code := boundedErrorCode("child_dag_" + observation.Status.String())
			runtime.LastError = &code
		}
		return r.putCandidate(ctx, base, runtime)
	})
}

func (r *Runner) failActiveChild(ctx context.Context, id, runID, code string) {
	_ = r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil || !matchesActive(runtime, runID) || runtime.Status == core.Aborted {
			return err
		}
		runtime.ActiveDAGRun = nil
		return r.failRuntime(ctx, runtime, code)
	})
}

func (r *Runner) failObservedActiveChild(ctx context.Context, id string, request ChildRunRequest, code string) {
	_ = r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil || !matchesActive(runtime, request.DAGRunID) || runtime.Status == core.Aborted {
			return err
		}
		appendRunRef(runtime, DAGRunRef{State: request.State, DAG: request.DAG, DAGRunID: request.DAGRunID})
		runtime.ActiveDAGRun = nil
		return r.failRuntime(ctx, runtime, code)
	})
}

func (r *Runner) reconcileRoute(ctx context.Context, definition Definition) {
	var turn *Runtime
	err := r.locker.WithLock(ctx, definition.ID, func() error {
		currentDefinition, currentRuntime, err := r.load(ctx, definition.ID)
		if err != nil {
			return err
		}
		if currentRuntime == nil || currentRuntime.Status != core.Running || currentRuntime.ActiveDAGRun != nil {
			return nil
		}
		if err := r.validator.Validate(ctx, currentDefinition); err != nil {
			return r.failRuntime(ctx, currentRuntime, "definition_invalid")
		}
		if currentRuntime.TurnCount >= currentDefinition.EffectiveMaxTurns() {
			return r.failRuntime(ctx, currentRuntime, "max_turns_exceeded")
		}
		base := cloneRuntime(currentRuntime)
		currentRuntime.TurnCount++
		currentRuntime.UpdatedAt = r.now().UTC()
		if err := r.putCandidate(ctx, base, currentRuntime); err != nil {
			return err
		}
		definition = *currentDefinition
		turn = cloneRuntime(currentRuntime)
		return nil
	})
	if err != nil || turn == nil {
		return
	}
	if r.router == nil {
		r.failTurn(ctx, definition.ID, turn.TurnCount, turn.CurrentState, "router_unavailable")
		return
	}

	decision, routeErr := r.router.Decide(ctx, definition, *turn)
	if ctx.Err() != nil {
		return
	}
	if routeErr != nil {
		code := "router_error"
		switch {
		case errors.Is(routeErr, context.DeadlineExceeded):
			code = "router_timeout"
		case errors.Is(routeErr, ErrRouterDecision):
			code = "router_decision_invalid"
		}
		r.failTurn(ctx, definition.ID, turn.TurnCount, turn.CurrentState, code)
		return
	}
	r.adoptDecision(ctx, definition, *turn, *decision)
}

func (r *Runner) failTurn(ctx context.Context, id string, turn int, state, code string) {
	_ = r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil {
			return err
		}
		if runtime.Status != core.Running || runtime.ActiveDAGRun != nil || runtime.TurnCount != turn || runtime.CurrentState != state {
			return nil
		}
		return r.failRuntime(ctx, runtime, code)
	})
}

func (r *Runner) adoptDecision(ctx context.Context, definition Definition, turn Runtime, decision RouteDecision) {
	var enqueueRequest *ChildRunRequest
	_ = r.locker.WithLock(ctx, definition.ID, func() error {
		currentDefinition, runtime, err := r.load(ctx, definition.ID)
		if err != nil {
			return err
		}
		if runtime == nil || runtime.Status != core.Running || runtime.ActiveDAGRun != nil || runtime.TurnCount != turn.TurnCount || runtime.CurrentState != turn.CurrentState {
			return nil
		}
		if err := r.validator.Validate(ctx, currentDefinition); err != nil {
			return r.failRuntime(ctx, runtime, "definition_invalid")
		}
		if _, err := validateRouteArguments(*currentDefinition, *runtime, routeArgumentsFromDecision(decision)); err != nil {
			return r.failRuntime(ctx, runtime, "router_decision_stale")
		}
		if decision.Action == "run" {
			if r.router == nil || r.router.ValidateCurrentParams(ctx, decision.DAG, decision.Params) != nil {
				return r.failRuntime(ctx, runtime, "router_decision_stale")
			}
		}
		base := cloneRuntime(runtime)
		runtime.Context = append(runtime.Context, decision.Assistant)
		runtime.CurrentState = decision.NextState
		runtime.UpdatedAt = r.now().UTC()

		switch decision.Action {
		case "run":
			if r.children == nil || r.runIDs == nil {
				return r.failRuntime(ctx, runtime, "child_dispatch_unavailable")
			}
			runID, err := r.runIDs.GenDAGRunID(ctx)
			if err != nil {
				return r.failRuntime(ctx, runtime, "child_run_id_failed")
			}
			runtime.Status = core.Running
			runtime.ActiveDAGRun = &ActiveDAGRun{ToolCallID: decision.ToolCallID, DAG: decision.DAG, Params: decision.Params, DAGRunID: runID}
			if err := r.putCandidate(ctx, base, runtime); err != nil {
				return err
			}
			request := childRequest(*currentDefinition, *runtime)
			enqueueRequest = &request
		case "wait":
			outcome, err := RoutingOutcomeMessage(decision, "")
			if err != nil {
				return r.failRuntime(ctx, runtime, "routing_outcome_invalid")
			}
			runtime.Context = append(runtime.Context, outcome)
			runtime.Status = core.Waiting
			question := decision.Question
			runtime.WaitingQuestion = &question
			return r.putCandidate(ctx, base, runtime)
		case "complete":
			terminal := currentDefinition.States[decision.NextState].Terminal
			outcome, err := RoutingOutcomeMessage(decision, terminal)
			if err != nil {
				return r.failRuntime(ctx, runtime, "routing_outcome_invalid")
			}
			runtime.Context = append(runtime.Context, outcome)
			runtime.Status = core.Succeeded
			if terminal == "failed" {
				runtime.Status = core.Failed
			}
			now := r.now().UTC()
			runtime.FinishedAt = &now
			runtime.WaitingQuestion = nil
			runtime.LastError = nil
			return r.putCandidate(ctx, base, runtime)
		}
		return nil
	})
	if enqueueRequest == nil {
		return
	}
	if err := r.ensureActiveChildEnqueued(ctx, definition.ID, *enqueueRequest); err != nil && ctx.Err() == nil {
		r.failActiveChild(ctx, definition.ID, enqueueRequest.DAGRunID, "child_enqueue_failed")
	}
}

func (r *Runner) ensureActiveChildEnqueued(ctx context.Context, id string, request ChildRunRequest) error {
	return r.locker.WithLock(ctx, id, func() error {
		runtime, err := r.runtimes.Get(ctx, id)
		if err != nil {
			return err
		}
		if runtime.Status == core.Aborted || !matchesActive(runtime, request.DAGRunID) {
			return nil
		}
		if runtime.Status != core.Running && runtime.Status != core.Waiting {
			return fmt.Errorf("controller child cannot be enqueued from status %s", runtime.Status)
		}
		return r.children.EnsureEnqueued(ctx, request)
	})
}

func (r *Runner) load(ctx context.Context, id string) (*Definition, *Runtime, error) {
	data, err := r.definitions.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	definition, err := ParseDefinition(data)
	if err != nil || definition.ID != id {
		return nil, nil, fmt.Errorf("%w: %s", ErrDefinitionCorrupt, id)
	}
	runtime, err := r.runtimes.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return definition, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := validateDefinitionRuntimeIdentity(definition, runtime); err != nil {
		return nil, nil, err
	}
	return definition, runtime, nil
}

func (r *Runner) failRuntime(ctx context.Context, runtime *Runtime, code string) error {
	markRuntimeFailed(runtime, code, r.now())
	return r.runtimes.Put(ctx, runtime)
}

func (r *Runner) putCandidate(ctx context.Context, base, candidate *Runtime) error {
	_, accepted, err := persistRuntimeCandidate(ctx, r.runtimes, base, candidate, r.now())
	if err != nil {
		return err
	}
	if !accepted {
		return errCandidateRejected
	}
	return nil
}

func childRequest(definition Definition, runtime Runtime) ChildRunRequest {
	active := runtime.ActiveDAGRun
	if active == nil {
		return ChildRunRequest{}
	}
	return ChildRunRequest{
		ControllerID: definition.ID, Workspace: definition.Workspace(), State: runtime.CurrentState,
		DAG: active.DAG, DAGRunID: active.DAGRunID, Params: append(json.RawMessage(nil), active.Params...),
	}
}

type childStatusClass uint8

const (
	childStatusInvalid childStatusClass = iota
	childStatusActive
	childStatusTerminal
)

func classifyChildStatus(status core.Status) childStatusClass {
	switch status {
	case core.Succeeded, core.PartiallySucceeded, core.Failed, core.Aborted, core.Rejected:
		return childStatusTerminal
	case core.Running, core.Queued, core.Waiting:
		return childStatusActive
	case core.NotStarted:
		return childStatusInvalid
	}
	return childStatusInvalid
}

func matchesActive(runtime *Runtime, runID string) bool {
	return runtime != nil && runtime.ActiveDAGRun != nil && runtime.ActiveDAGRun.DAGRunID == runID
}

func appendRunRef(runtime *Runtime, ref DAGRunRef) {
	for _, existing := range runtime.DAGRunRefs {
		if existing.DAGRunID == ref.DAGRunID {
			return
		}
	}
	runtime.DAGRunRefs = append(runtime.DAGRunRefs, ref)
	if len(runtime.DAGRunRefs) > maxDAGRunRefs {
		runtime.DAGRunRefs = append([]DAGRunRef(nil), runtime.DAGRunRefs[len(runtime.DAGRunRefs)-maxDAGRunRefs:]...)
	}
}

func hasRunRef(runtime *Runtime, runID string) bool {
	for _, ref := range runtime.DAGRunRefs {
		if ref.DAGRunID == runID {
			return true
		}
	}
	return false
}

func cloneRuntime(runtime *Runtime) *Runtime {
	if runtime == nil {
		return nil
	}
	clone := *runtime
	clone.DAGRunRefs = append([]DAGRunRef(nil), runtime.DAGRunRefs...)
	clone.Context = append([]exec.LLMMessage(nil), runtime.Context...)
	clone.WaitingQuestion = cloneStringPointer(runtime.WaitingQuestion)
	clone.LastError = cloneStringPointer(runtime.LastError)
	clone.FinishedAt = cloneTimePointer(runtime.FinishedAt)
	if runtime.ActiveDAGRun != nil {
		active := *runtime.ActiveDAGRun
		active.Params = append(json.RawMessage(nil), runtime.ActiveDAGRun.Params...)
		clone.ActiveDAGRun = &active
	}
	return &clone
}

func routeArgumentsFromDecision(decision RouteDecision) routeArguments {
	dag := decision.DAG
	question := decision.Question
	arguments := routeArguments{Action: decision.Action, NextState: decision.NextState, Reason: decision.Reason}
	if decision.Action == "run" {
		arguments.DAG = &dag
		arguments.Params = decision.Params
	}
	if decision.Action == "wait" {
		arguments.Question = &question
	}
	return arguments
}

// ExecutionEvidenceMessage builds one bounded, untrusted child result message.
func ExecutionEvidenceMessage(active ActiveDAGRun, observation ChildRunObservation) (exec.LLMMessage, error) {
	keys := make([]string, 0, len(observation.Outputs))
	for key := range observation.Outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	selected := make(map[string]string, len(keys))
	for _, key := range keys {
		selected[key] = observation.Outputs[key]
	}
	for {
		omitted := len(keys) - len(selected)
		content, err := evidenceEnvelope(active, observation, selected, omitted > 0, omitted)
		if err != nil {
			return exec.LLMMessage{}, err
		}
		if len(content) <= maxEvidenceBytes {
			return exec.LLMMessage{Role: exec.RoleTool, ToolCallID: active.ToolCallID, Content: content}, nil
		}
		removed := false
		for index := len(keys) - 1; index >= 0; index-- {
			if _, exists := selected[keys[index]]; !exists {
				continue
			}
			delete(selected, keys[index])
			removed = true
			break
		}
		if !removed {
			return exec.LLMMessage{}, fmt.Errorf("execution evidence envelope exceeds %d bytes", maxEvidenceBytes)
		}
	}
}

func evidenceEnvelope(active ActiveDAGRun, observation ChildRunObservation, outputs map[string]string, truncated bool, omitted int) (string, error) {
	payload := map[string]any{
		"dag": active.DAG, "dag_run_id": active.DAGRunID, "status": observation.Status.String(),
		"outputs": outputs, "untrusted": true,
	}
	if observation.ErrorCategory != "" {
		payload["error_category"] = boundedErrorCode(observation.ErrorCategory)
	}
	if truncated {
		payload["truncated"] = true
		payload["omitted_count"] = omitted
	}
	return marshalEnvelope("execution_evidence", "runtime_untrusted", "dag_run:"+active.DAGRunID, payload)
}

func boundedErrorCode(value string) string {
	if len(value) <= maxLastErrorBytes {
		return value
	}
	value = value[:maxLastErrorBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

type keyedGuard struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func (g *keyedGuard) TryLock(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == nil {
		g.active = make(map[string]struct{})
	}
	if _, exists := g.active[key]; exists {
		return false
	}
	g.active[key] = struct{}{}
	return true
}

func (g *keyedGuard) Unlock(key string) {
	g.mu.Lock()
	delete(g.active, key)
	g.mu.Unlock()
}
