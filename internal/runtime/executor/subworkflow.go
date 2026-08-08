// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"os"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
)

// SubWorkflowRunner runs child workflows behind a workflow-level interface.
type SubWorkflowRunner interface {
	ShouldRun(ctx context.Context, req SubWorkflowRequest) bool
	Run(ctx context.Context, req SubWorkflowRequest) (*dagrun.RunStatus, error)
	Retry(ctx context.Context, req SubWorkflowRetryRequest) (*dagrun.RunStatus, error)
	Cancel(ctx context.Context, req SubWorkflowCancelRequest) error
}

// SubWorkflowRequest describes a child workflow invocation.
type SubWorkflowRequest struct {
	DAG               *ir.DAG
	ParentDAG         *ir.DAG
	RootDAGRun        dagrun.DAGRunRef
	ParentDAGRun      dagrun.DAGRunRef
	RunID             string
	Params            string
	ProfileName       string
	TriggerActor      string
	WorkDir           string
	WorkerSelector    map[string]string
	ExternalStepRetry bool
	Reuse             bool
	RetryPath         dagrun.RetryPath
	Workspace         *SubWorkflowWorkspace
}

// SubWorkflowRetryRequest describes a child workflow step retry.
type SubWorkflowRetryRequest struct {
	SubWorkflowRequest
	StepName string
}

// SubWorkflowCancelMode describes how a child workflow should be stopped.
type SubWorkflowCancelMode string

const (
	// SubWorkflowCancelModeGraceful requests a graceful stop of the child workflow.
	SubWorkflowCancelModeGraceful SubWorkflowCancelMode = "graceful"
	// SubWorkflowCancelModeForce requests a forced stop of the child workflow.
	SubWorkflowCancelModeForce SubWorkflowCancelMode = "force"
)

// SubWorkflowCancelIntent carries runtime-owned cancellation intent.
type SubWorkflowCancelIntent struct {
	Mode   SubWorkflowCancelMode
	Signal os.Signal
}

// SubWorkflowCancelRequest describes a child workflow cancellation.
type SubWorkflowCancelRequest struct {
	DAG        *ir.DAG
	RootDAGRun dagrun.DAGRunRef
	RunID      string
	Intent     SubWorkflowCancelIntent
}

// SubWorkflowWorkspace carries an immutable child workflow workspace.
type SubWorkflowWorkspace struct {
	Descriptor workspacebundle.Descriptor
	Archive    []byte
}

type subWorkflowRunnerKey struct{}

// WithSubWorkflowRunner injects a child workflow runner into ctx.
func WithSubWorkflowRunner(ctx context.Context, runner SubWorkflowRunner) context.Context {
	if runner == nil {
		return ctx
	}
	return context.WithValue(ctx, subWorkflowRunnerKey{}, runner)
}

// SubWorkflowRunnerFromContext returns the child workflow runner in ctx, if any.
func SubWorkflowRunnerFromContext(ctx context.Context) (SubWorkflowRunner, bool) {
	runner, ok := ctx.Value(subWorkflowRunnerKey{}).(SubWorkflowRunner)
	return runner, ok
}
