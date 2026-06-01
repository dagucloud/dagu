// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"context"
	"errors"
	"sync"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

var errNoMatchingRunner = errors.New("no child workflow runner matched request")

// Router selects the first child workflow runner that accepts a request.
type Router struct {
	runners []executor.SubWorkflowRunner

	mu       sync.Mutex
	selected map[string]executor.SubWorkflowRunner
}

var _ executor.SubWorkflowRunner = (*Router)(nil)

// NewRouter creates a child workflow runner that tries runners in order.
func NewRouter(runners ...executor.SubWorkflowRunner) *Router {
	filtered := make([]executor.SubWorkflowRunner, 0, len(runners))
	for _, runner := range runners {
		if runner != nil {
			filtered = append(filtered, runner)
		}
	}
	return &Router{
		runners:  filtered,
		selected: make(map[string]executor.SubWorkflowRunner),
	}
}

// ShouldRun reports whether any runner accepts req.
func (r *Router) ShouldRun(ctx context.Context, req executor.SubWorkflowRequest) bool {
	return r.selectRunner(ctx, req) != nil
}

// Run executes req with the first matching runner.
func (r *Router) Run(ctx context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	runner := r.selectRunner(ctx, req)
	if runner == nil {
		return nil, errNoMatchingRunner
	}
	r.remember(req.RunID, runner)
	defer r.forget(req.RunID)
	return runner.Run(ctx, req)
}

// Retry retries req with the first matching runner.
func (r *Router) Retry(ctx context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	runner := r.selectRunner(ctx, req.SubWorkflowRequest)
	if runner == nil {
		return nil, errNoMatchingRunner
	}
	r.remember(req.RunID, runner)
	defer r.forget(req.RunID)
	return runner.Retry(ctx, req)
}

// Cancel routes cancellation to the runner that owns req.RunID.
func (r *Router) Cancel(ctx context.Context, req executor.SubWorkflowCancelRequest) error {
	r.mu.Lock()
	runner := r.selected[req.RunID]
	r.mu.Unlock()
	if runner != nil {
		return runner.Cancel(ctx, req)
	}

	var errs []error
	for _, candidate := range r.runners {
		if err := candidate.Cancel(ctx, req); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Cleanup releases resources held by child runners.
func (r *Router) Cleanup(ctx context.Context) error {
	var errs []error
	for _, runner := range r.runners {
		cleaner, ok := runner.(interface{ Cleanup(context.Context) error })
		if !ok {
			continue
		}
		if err := cleaner.Cleanup(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Router) selectRunner(ctx context.Context, req executor.SubWorkflowRequest) executor.SubWorkflowRunner {
	if r == nil {
		return nil
	}
	for _, runner := range r.runners {
		if runner.ShouldRun(ctx, req) {
			return runner
		}
	}
	return nil
}

func (r *Router) remember(runID string, runner executor.SubWorkflowRunner) {
	if runID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.selected[runID] = runner
}

func (r *Router) forget(runID string) {
	if runID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.selected, runID)
}
