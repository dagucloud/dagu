// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"fmt"
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

	Operation   DispatchOperation
	DAGRunID    string
	Target      string
	Definition  string
	AttemptID   string
	AttemptKey  string
	Step        string
	Params      string
	QueueName   string
	WorkerID    string
	ProfileName string

	PreviousStatus *DAGRunStatus

	BaseConfig   string
	Labels       string
	ScheduleTime string
	SourceFile   string

	WorkerSelector map[string]string
	AgentSnapshot  []byte

	ExternalStepRetry bool

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
	Status *DAGRunStatus
}

// Dispatcher defines distributed DAG run operations.
type Dispatcher interface {
	Dispatch(ctx context.Context, task *DispatchTask) error
	Cleanup(ctx context.Context) error
	GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *DAGRunRef) (*DAGRunStatusResult, error)
	RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *DAGRunRef) error
}
