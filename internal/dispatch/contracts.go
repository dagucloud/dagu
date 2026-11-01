// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DispatchOperation identifies the operation requested for a distributed DAG run.
type DispatchOperation int32

const (
	DispatchOperationUnspecified DispatchOperation = iota
	DispatchOperationStart
	DispatchOperationRetry
)

func (o DispatchOperation) String() string {
	switch o {
	case DispatchOperationStart:
		return "start"
	case DispatchOperationRetry:
		return "retry"
	case DispatchOperationUnspecified:
		return "unspecified"
	default:
		return fmt.Sprintf("DispatchOperation(%d)", o)
	}
}

// DispatchTask describes a DAG run request for a distributed executor.
type DispatchTask struct {
	RootDAGRunName string
	RootDAGRunID   string

	ParentDAGRunName string
	ParentDAGRunID   string

	Operation    DispatchOperation
	DAGRunID     string
	Target       string
	Definition   string
	AttemptID    string
	AttemptKey   string
	Step         string
	Params       string
	ParallelItem string
	// PassedEnv carries resolved "KEY=value" pairs the parent opted to share
	// with the child run via the step's pass_env field.
	PassedEnv      []string
	QueueName      string
	WorkerID       string
	TargetWorkerID string
	ProfileName    string
	DefinitionID   string
	TriggerActor   string

	PreviousStatus *ir.DAGRunStatus

	BaseConfig          string
	BaseConfigWorkspace *string
	Labels              string
	ScheduleTime        string
	SourceFile          string
	SourceWorkDir       string

	WorkerSelector map[string]string

	// HoldUntil is set when the task was accepted with no healthy worker
	// matching it. Until then the task waits for one to register; after it
	// the coordinator fails the run instead.
	HoldUntil time.Time

	ExternalStepRetry   bool
	IncludeDownstream   bool
	BypassPreconditions bool
	RetryPath           string

	WorkspaceBundleDigest      string
	WorkspaceBundleSize        int64
	WorkspaceBundleDAGPath     string
	WorkspaceBundleOriginalRef string
	WorkspaceBundleResolvedRef string

	Owner      CoordinatorEndpoint
	ClaimToken string
}

// DAGRunStatusResult is a distributed status lookup result.
type DAGRunStatusResult struct {
	Found  bool
	Status *ir.DAGRunStatus
}

// DispatchRequest describes a distributed dispatch call.
type DispatchRequest struct {
	Task                      *DispatchTask
	AdmissionReservationToken string
}

// Dispatcher defines distributed DAG run operations.
type Dispatcher interface {
	Dispatch(ctx context.Context, req DispatchRequest) error
	Cleanup(ctx context.Context) error
	GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*DAGRunStatusResult, error)
	RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) error
}

// DefinitionError reports a dispatch rejected because its DAG definition
// cannot be built. Dispatching the same definition again cannot succeed.
type DefinitionError struct {
	Err error
}

func (e *DefinitionError) Error() string {
	return "invalid DAG definition: " + e.Err.Error()
}

func (e *DefinitionError) Unwrap() error {
	return e.Err
}
