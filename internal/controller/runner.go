// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"bytes"
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
	maxEvidenceBytes          = 32 << 10
)

var errCandidateRejected = errors.New("controller runtime candidate was rejected")

// ChildRunRequest is the durable child request selected by the Router.
type ChildRunRequest struct {
	Workspace string
	State     string
	DAG       string
	DAGRunID  string
	Params    json.RawMessage
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
type DAGRunIDGenerator func(context.Context) (string, error)

// Runner reconciles every active Controller owned by the active scheduler.
type Runner struct {
	definitions DefinitionStore
	runtimes    RuntimeStore
	locker      ResourceLocker
	validator   *Validator
	router      *Router
	children    ChildRunGateway
	runIDs      DAGRunIDGenerator
	now         func() time.Time
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
) *Runner {
	if validator == nil {
		validator = NewValidator(nil)
	}
	return &Runner{
		definitions: definitions, runtimes: runtimes, locker: locker,
		validator: validator, router: router, children: children, runIDs: runIDs,
		now: time.Now,
	}
}

// Run scans immediately and then periodically until the scheduler cancels the context.
func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.definitions == nil || r.runtimes == nil || r.locker == nil {
		return
	}
	r.scan(ctx)
	ticker := time.NewTicker(defaultRunnerScanInterval)
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
		if ctx.Err() == nil {
			logger.Error(ctx, "Failed to list Controller runtimes",
				tag.String("error-category", runnerErrorCategory(err, "list_failed")))
		}
		return
	}
	if len(ids) == 0 {
		return
	}
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range min(defaultRunnerWorkers, len(ids)) {
		workers.Go(func() {
			for id := range jobs {
				r.reconcile(ctx, id)
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
	_, runtime, err := r.load(ctx, id)
	if err != nil {
		reconcileErr := error(nil)
		if runtime != nil && errors.Is(err, ErrDefinitionCorrupt) && !runtimeIsSettled(runtime) {
			if runtime.Status == core.Aborted {
				reconcileErr = r.reconcileAborted(ctx, *runtime)
			} else {
				reconcileErr = r.reconcileInvalidDefinition(ctx, *runtime)
			}
		}
		if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
			combinedErr := errors.Join(err, reconcileErr)
			logger.Error(ctx, "Failed to reconcile Controller",
				tag.String("controller-id", id),
				tag.String("error-category", runnerErrorCategory(combinedErr, "reconcile_failed")))
		}
		return
	}
	if runtime == nil || runtimeIsSettled(runtime) {
		return
	}

	var reconcileErr error
	switch {
	case runtime.Status == core.Aborted:
		reconcileErr = r.reconcileAborted(ctx, *runtime)
	case runtime.ActiveDAGRun != nil:
		reconcileErr = r.reconcileChild(ctx, *runtime)
	case runtime.Status == core.Waiting:
		return
	case runtime.Status == core.Running:
		reconcileErr = r.reconcileRoute(ctx, id)
	}
	if reconcileErr != nil && ctx.Err() == nil {
		logger.Error(ctx, "Failed to reconcile Controller",
			tag.String("controller-id", id),
			tag.String("error-category", runnerErrorCategory(reconcileErr, "reconcile_failed")))
	}
}

func runnerErrorCategory(err error, fallback string) string {
	switch {
	case errors.Is(err, ErrRuntimeCorrupt):
		return "runtime_corrupt"
	case errors.Is(err, ErrDefinitionCorrupt):
		return "definition_invalid"
	case errors.Is(err, ErrRouterCall) && errors.Is(err, context.DeadlineExceeded):
		return "router_timeout"
	case errors.Is(err, ErrRouterDecision):
		return "router_decision_invalid"
	case errors.Is(err, ErrRouterCall):
		return "router_error"
	case errors.Is(err, ErrSnapshotTooLarge), errors.Is(err, errCandidateRejected):
		return "runtime_snapshot_limit"
	default:
		return fallback
	}
}

func (r *Runner) reconcileAborted(ctx context.Context, runtime Runtime) error {
	if runtime.ActiveDAGRun == nil {
		return r.settleAborted(ctx, runtime.ID, "", false)
	}
	if r.children == nil {
		return errors.New("controller child gateway is not configured")
	}
	request := childRequest(runtime)
	observation, err := r.children.Observe(ctx, request)
	if err != nil {
		cause := fmt.Errorf("inspect stopped Controller child: %w", err)
		if observation.Exists && classifyChildStatus(observation.Status) == childStatusTerminal {
			return errors.Join(
				cause,
				r.settleAborted(ctx, runtime.ID, runtime.ActiveDAGRun.DAGRunID, true),
			)
		}
		return cause
	}
	if !observation.Exists {
		return r.settleAborted(ctx, runtime.ID, runtime.ActiveDAGRun.DAGRunID, false)
	}
	switch classifyChildStatus(observation.Status) {
	case childStatusTerminal, childStatusInvalid:
		return r.settleAborted(ctx, runtime.ID, runtime.ActiveDAGRun.DAGRunID, true)
	case childStatusActive:
	}
	if err := r.children.Stop(ctx, request); err != nil {
		return fmt.Errorf("stop Controller child: %w", err)
	}
	return nil
}

func (r *Runner) settleAborted(ctx context.Context, id, childRunID string, addRef bool) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
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
		return r.runtimes.Put(lockedCtx, runtime)
	})
}

func (r *Runner) reconcileChild(ctx context.Context, runtime Runtime) error {
	if r.children == nil {
		return r.failActiveChild(ctx, runtime.ID, runtime.ActiveDAGRun.DAGRunID, "child_dispatch_unavailable")
	}
	request := childRequest(runtime)
	observation, err := r.children.Observe(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cause := fmt.Errorf("inspect Controller child: %w", err)
		if observation.Exists {
			return errors.Join(cause, r.failObservedActiveChild(ctx, runtime.ID, request, "child_observation_failed"))
		}
		return errors.Join(cause, r.failActiveChild(ctx, runtime.ID, request.DAGRunID, "child_observation_failed"))
	}
	if !observation.Exists {
		if err := r.ensureActiveChildEnqueued(ctx, runtime.ID, request); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.Join(
				fmt.Errorf("enqueue Controller child: %w", err),
				r.failActiveChild(ctx, runtime.ID, request.DAGRunID, "child_enqueue_failed"),
			)
		}
		return nil
	}

	switch classifyChildStatus(observation.Status) {
	case childStatusInvalid:
		return r.failObservedActiveChild(ctx, runtime.ID, request, "child_status_invalid")
	case childStatusActive:
		if observation.Status == core.Queued {
			if err := r.ensureActiveChildEnqueued(ctx, runtime.ID, request); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errors.Join(
					fmt.Errorf("enqueue Controller child: %w", err),
					r.failObservedActiveChild(ctx, runtime.ID, request, "child_enqueue_failed"),
				)
			}
		}
		return r.updateObservedChild(ctx, runtime.ID, request, observation.Status)
	case childStatusTerminal:
		return r.finishObservedChild(ctx, runtime.ID, request, observation)
	}
	return nil
}

func (r *Runner) reconcileInvalidDefinition(ctx context.Context, runtime Runtime) error {
	if runtime.ActiveDAGRun == nil {
		return r.failInactiveRuntime(ctx, runtime.ID, "definition_invalid")
	}
	if r.children == nil {
		return errors.New("controller child gateway is not configured")
	}
	request := childRequest(runtime)
	observation, err := r.children.Observe(ctx, request)
	if err != nil {
		cause := fmt.Errorf("inspect Controller child after definition failure: %w", err)
		if observation.Exists && classifyChildStatus(observation.Status) != childStatusActive {
			return errors.Join(cause, r.failObservedActiveChild(ctx, runtime.ID, request, "definition_invalid"))
		}
		return cause
	}
	if !observation.Exists {
		return r.failActiveChild(ctx, runtime.ID, request.DAGRunID, "definition_invalid")
	}
	if classifyChildStatus(observation.Status) != childStatusActive {
		return r.failObservedActiveChild(ctx, runtime.ID, request, "definition_invalid")
	}
	if err := r.children.Stop(ctx, request); err != nil {
		return fmt.Errorf("stop Controller child after definition failure: %w", err)
	}
	return nil
}

func (r *Runner) failInactiveRuntime(ctx context.Context, id, code string) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil || runtimeIsSettled(runtime) || runtime.ActiveDAGRun != nil {
			return err
		}
		return r.failRuntime(lockedCtx, runtime, code)
	})
}

func (r *Runner) updateObservedChild(ctx context.Context, id string, request ChildRunRequest, status core.Status) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil || !matchesActive(runtime, request.DAGRunID) || runtime.Status == core.Aborted {
			return err
		}
		ref := DAGRunRef{State: request.State, DAG: request.DAG, DAGRunID: request.DAGRunID}
		base := cloneRuntime(runtime)
		appendRunRef(base, ref)
		addedRunRef := appendRunRef(runtime, ref)
		nextStatus := core.Running
		if status == core.Waiting {
			nextStatus = core.Waiting
		}
		if runtime.Status == nextStatus && !addedRunRef {
			return nil
		}
		runtime.Status = nextStatus
		runtime.UpdatedAt = r.now().UTC()
		return r.putCandidate(lockedCtx, base, runtime)
	})
}

func (r *Runner) finishObservedChild(ctx context.Context, id string, request ChildRunRequest, observation ChildRunObservation) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil || !matchesActive(runtime, request.DAGRunID) || runtime.Status == core.Aborted {
			return err
		}
		evidence, err := ExecutionEvidenceMessage(*runtime.ActiveDAGRun, observation)
		if err != nil {
			return errors.Join(
				fmt.Errorf("build Controller child result: %w", err),
				r.failRuntime(lockedCtx, runtime, "child_result_invalid"),
			)
		}
		ref := DAGRunRef{State: request.State, DAG: request.DAG, DAGRunID: request.DAGRunID}
		base := cloneRuntime(runtime)
		appendRunRef(base, ref)
		runtime.Context = append(runtime.Context, evidence)
		appendRunRef(runtime, ref)
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
			code := "child_dag_failed"
			runtime.LastError = &code
		}
		return r.putCandidate(lockedCtx, base, runtime)
	})
}

func (r *Runner) failActiveChild(ctx context.Context, id, runID, code string) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil || !matchesActive(runtime, runID) || runtime.Status == core.Aborted {
			return err
		}
		runtime.ActiveDAGRun = nil
		return r.failRuntime(lockedCtx, runtime, code)
	})
}

func (r *Runner) failObservedActiveChild(ctx context.Context, id string, request ChildRunRequest, code string) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil || !matchesActive(runtime, request.DAGRunID) || runtime.Status == core.Aborted {
			return err
		}
		appendRunRef(runtime, DAGRunRef{State: request.State, DAG: request.DAG, DAGRunID: request.DAGRunID})
		runtime.ActiveDAGRun = nil
		return r.failRuntime(lockedCtx, runtime, code)
	})
}

func (r *Runner) reconcileRoute(ctx context.Context, id string) error {
	var definition *Definition
	var turn *Runtime
	err := r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		currentDefinition, currentRuntime, err := r.load(lockedCtx, id)
		if err != nil {
			return err
		}
		if currentRuntime == nil || currentRuntime.Status != core.Running || currentRuntime.ActiveDAGRun != nil {
			return nil
		}
		if _, err := r.validator.Validate(lockedCtx, currentDefinition); err != nil {
			return errors.Join(
				fmt.Errorf("validate Controller definition: %w", err),
				r.failRuntime(lockedCtx, currentRuntime, "definition_invalid"),
			)
		}
		if currentRuntime.TurnCount >= currentDefinition.EffectiveMaxTurns() {
			return r.failRuntime(lockedCtx, currentRuntime, "max_turns_exceeded")
		}
		base := cloneRuntime(currentRuntime)
		currentRuntime.TurnCount++
		currentRuntime.UpdatedAt = r.now().UTC()
		if err := r.putCandidate(lockedCtx, base, currentRuntime); err != nil {
			return err
		}
		definition = currentDefinition
		turn = cloneRuntime(currentRuntime)
		return nil
	})
	if err != nil {
		return err
	}
	if turn == nil {
		return nil
	}
	if r.router == nil {
		return r.failTurn(ctx, id, turn.TurnCount, turn.CurrentState, "router_unavailable")
	}

	decision, routeErr := r.router.Decide(ctx, *definition, *turn)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if routeErr != nil {
		code := "router_error"
		switch {
		case errors.Is(routeErr, context.DeadlineExceeded):
			code = "router_timeout"
		case errors.Is(routeErr, ErrRouterDecision):
			code = "router_decision_invalid"
		}
		return errors.Join(
			fmt.Errorf("route Controller: %w", routeErr),
			r.failTurn(ctx, id, turn.TurnCount, turn.CurrentState, code),
		)
	}
	return r.adoptDecision(ctx, id, *turn, *decision)
}

func (r *Runner) failTurn(ctx context.Context, id string, turn int, state, code string) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil {
			return err
		}
		if runtime.Status != core.Running || runtime.ActiveDAGRun != nil || runtime.TurnCount != turn || runtime.CurrentState != state {
			return nil
		}
		return r.failRuntime(lockedCtx, runtime, code)
	})
}

func (r *Runner) adoptDecision(ctx context.Context, id string, turn Runtime, decision RouteDecision) error {
	var enqueueRequest *ChildRunRequest
	if err := r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		currentDefinition, runtime, err := r.load(lockedCtx, id)
		if err != nil {
			return err
		}
		if runtime == nil || runtime.Status != core.Running || runtime.ActiveDAGRun != nil || runtime.TurnCount != turn.TurnCount || runtime.CurrentState != turn.CurrentState {
			return nil
		}
		if _, err := r.validator.Validate(lockedCtx, currentDefinition); err != nil {
			return errors.Join(
				fmt.Errorf("validate Controller definition: %w", err),
				r.failRuntime(lockedCtx, runtime, "definition_invalid"),
			)
		}
		if _, err := validateRouteArguments(*currentDefinition, *runtime, routeArgumentsFromDecision(decision)); err != nil {
			return errors.Join(
				fmt.Errorf("revalidate Controller route decision: %w", err),
				r.failRuntime(lockedCtx, runtime, "router_decision_stale"),
			)
		}
		if decision.Action == "run" {
			if r.router == nil {
				return r.failRuntime(lockedCtx, runtime, "router_decision_stale")
			}
			resolvedParams, err := r.router.ValidateCurrentParams(lockedCtx, *currentDefinition, decision.DAG, decision.inputParams)
			if err != nil {
				return errors.Join(
					fmt.Errorf("revalidate Controller child params: %w", err),
					r.failRuntime(lockedCtx, runtime, "router_decision_stale"),
				)
			}
			if !bytes.Equal(resolvedParams, decision.Params) {
				return errors.Join(
					fmt.Errorf("revalidate Controller child params: resolved params changed"),
					r.failRuntime(lockedCtx, runtime, "router_decision_stale"),
				)
			}
		}

		var (
			runID    string
			outcome  exec.LLMMessage
			terminal string
		)
		switch decision.Action {
		case "run":
			if r.children == nil || r.runIDs == nil {
				return r.failRuntime(lockedCtx, runtime, "child_dispatch_unavailable")
			}
			runID, err = r.runIDs(lockedCtx)
			if err != nil {
				return errors.Join(
					fmt.Errorf("generate Controller child run ID: %w", err),
					r.failRuntime(lockedCtx, runtime, "child_run_id_failed"),
				)
			}
		case "wait":
			outcome, err = RoutingOutcomeMessage(decision, "")
			if err != nil {
				return errors.Join(
					fmt.Errorf("build Controller wait outcome: %w", err),
					r.failRuntime(lockedCtx, runtime, "routing_outcome_invalid"),
				)
			}
		case "complete":
			terminal = currentDefinition.States[decision.NextState].Terminal
			outcome, err = RoutingOutcomeMessage(decision, terminal)
			if err != nil {
				return errors.Join(
					fmt.Errorf("build Controller completion outcome: %w", err),
					r.failRuntime(lockedCtx, runtime, "routing_outcome_invalid"),
				)
			}
		}

		base := cloneRuntime(runtime)
		decisionAt := r.now().UTC()
		runtime.Context = append(runtime.Context, decision.Assistant)
		runtime.CurrentState = decision.NextState
		runtime.UpdatedAt = decisionAt

		switch decision.Action {
		case "run":
			runtime.Status = core.Running
			runtime.ActiveDAGRun = &ActiveDAGRun{ToolCallID: decision.ToolCallID, DAG: decision.DAG, Params: decision.Params, DAGRunID: runID}
			if err := r.putCandidate(lockedCtx, base, runtime); err != nil {
				return err
			}
			request := childRequest(*runtime)
			enqueueRequest = &request
		case "wait":
			runtime.Context = append(runtime.Context, outcome)
			runtime.Status = core.Waiting
			question := decision.Question
			runtime.WaitingQuestion = &question
			return r.putCandidate(lockedCtx, base, runtime)
		case "complete":
			runtime.Context = append(runtime.Context, outcome)
			runtime.Status = core.Succeeded
			if terminal == "failed" {
				runtime.Status = core.Failed
			}
			runtime.FinishedAt = &decisionAt
			runtime.WaitingQuestion = nil
			runtime.LastError = nil
			return r.putCandidate(lockedCtx, base, runtime)
		}
		return nil
	}); err != nil {
		return err
	}
	if enqueueRequest == nil {
		return nil
	}
	if err := r.ensureActiveChildEnqueued(ctx, id, *enqueueRequest); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.Join(
			fmt.Errorf("enqueue Controller child: %w", err),
			r.failActiveChild(ctx, id, enqueueRequest.DAGRunID, "child_enqueue_failed"),
		)
	}
	return nil
}

func (r *Runner) ensureActiveChildEnqueued(ctx context.Context, id string, request ChildRunRequest) error {
	return r.locker.WithLock(ctx, id, func(lockedCtx context.Context) error {
		runtime, err := r.runtimes.Get(lockedCtx, id)
		if err != nil {
			return err
		}
		if runtime.Status == core.Aborted || !matchesActive(runtime, request.DAGRunID) {
			return nil
		}
		if runtime.Status != core.Running && runtime.Status != core.Waiting {
			return fmt.Errorf("controller child cannot be enqueued from status %s", runtime.Status)
		}
		return r.children.EnsureEnqueued(lockedCtx, request)
	})
}

func (r *Runner) load(ctx context.Context, id string) (*Definition, *Runtime, error) {
	runtime, err := r.runtimes.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if runtimeIsSettled(runtime) {
		return nil, runtime, nil
	}
	data, err := r.definitions.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, runtime, fmt.Errorf("%w: definition %s is missing", ErrDefinitionCorrupt, id)
		}
		return nil, runtime, err
	}
	definition, err := ParseDefinition(data)
	if err != nil {
		return nil, runtime, fmt.Errorf("%w: definition %s: %v", ErrDefinitionCorrupt, id, err)
	}
	if definition.ID != id {
		return nil, runtime, fmt.Errorf("%w: definition %s has ID %s", ErrDefinitionCorrupt, id, definition.ID)
	}
	if err := validateRuntimeAgainstDefinition(*definition, runtime); err != nil {
		return definition, runtime, err
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

func childRequest(runtime Runtime) ChildRunRequest {
	active := runtime.ActiveDAGRun
	if active == nil {
		return ChildRunRequest{}
	}
	return ChildRunRequest{
		Workspace: runtime.Workspace, State: runtime.CurrentState,
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

func appendRunRef(runtime *Runtime, ref DAGRunRef) bool {
	for _, existing := range runtime.DAGRunRefs {
		if existing.DAGRunID == ref.DAGRunID {
			return false
		}
	}
	runtime.DAGRunRefs = append(runtime.DAGRunRefs, ref)
	if len(runtime.DAGRunRefs) > maxDAGRunRefs {
		runtime.DAGRunRefs = append([]DAGRunRef(nil), runtime.DAGRunRefs[len(runtime.DAGRunRefs)-maxDAGRunRefs:]...)
	}
	return true
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

// ExecutionEvidenceMessage builds one bounded, untrusted child result message.
func ExecutionEvidenceMessage(active ActiveDAGRun, observation ChildRunObservation) (exec.LLMMessage, error) {
	keys := make([]string, 0, len(observation.Outputs))
	for key := range observation.Outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	selected := make(map[string]string, len(keys))
	base, err := evidenceEnvelope(active, observation, selected, len(keys))
	if err != nil {
		return exec.LLMMessage{}, err
	}
	remaining := maxEvidenceBytes - len(base)
	if remaining < 0 {
		return exec.LLMMessage{}, fmt.Errorf("execution evidence envelope exceeds %d bytes", maxEvidenceBytes)
	}
	selectedBytes := 0
	for _, key := range keys {
		entryBytes := len(key) + len(observation.Outputs[key]) + 5
		if selectedBytes > 0 {
			entryBytes++
		}
		if entryBytes > remaining-selectedBytes {
			continue
		}
		entry, err := json.Marshal(map[string]string{key: observation.Outputs[key]})
		if err != nil {
			return exec.LLMMessage{}, err
		}
		entryBytes = len(entry) - 2
		if selectedBytes > 0 {
			entryBytes++
		}
		if entryBytes > remaining-selectedBytes {
			continue
		}
		selected[key] = observation.Outputs[key]
		selectedBytes += entryBytes
	}
	omitted := len(keys) - len(selected)
	content, err := evidenceEnvelope(active, observation, selected, omitted)
	if err != nil {
		return exec.LLMMessage{}, err
	}
	if len(content) > maxEvidenceBytes {
		return exec.LLMMessage{}, fmt.Errorf("execution evidence envelope exceeds %d bytes", maxEvidenceBytes)
	}
	return exec.LLMMessage{Role: exec.RoleTool, ToolCallID: active.ToolCallID, Content: content}, nil
}

func evidenceEnvelope(active ActiveDAGRun, observation ChildRunObservation, outputs map[string]string, omitted int) (string, error) {
	payload := map[string]any{
		"dag": active.DAG, "dag_run_id": active.DAGRunID, "status": observation.Status.String(),
		"outputs": outputs, "untrusted": true,
	}
	if observation.ErrorCategory != "" {
		payload["error_category"] = boundedErrorCode(observation.ErrorCategory)
	}
	if omitted > 0 {
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
