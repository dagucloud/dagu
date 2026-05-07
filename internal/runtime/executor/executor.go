// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// CloseExecutor safely closes an executor if it implements io.Closer.
// Returns nil if executor doesn't implement io.Closer or is nil.
// This should be called after executor.Run() completes to release resources.
func CloseExecutor(exec Executor) error {
	if exec == nil {
		return nil
	}
	if closer, ok := exec.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Executor is an interface for executing steps in a DAG.
type Executor interface {
	SetStdout(out io.Writer)
	SetStderr(out io.Writer)
	Kill(sig os.Signal) error
	Run(ctx context.Context) error
}

// ExecutorFactory is a function type that creates an Executor based on the step configuration.
type ExecutorFactory func(ctx context.Context, step core.Step) (Executor, error)

// NewExecutor creates a new Executor based on the step's executor type.
func NewExecutor(ctx context.Context, step core.Step) (Executor, error) {
	executorRegistryMu.RLock()
	factory, ok := executorRegistry[step.ExecutorConfig.Type]
	executorRegistryMu.RUnlock()
	if ok {
		return factory(ctx, step)
	}

	logger.Error(ctx, "Step type is not registered",
		tag.Type(step.ExecutorConfig.Type),
		tag.Step(step.Name),
	)
	return nil, fmt.Errorf("step type %q is not registered", step.ExecutorConfig.Type)
}

// RegisterExecutor registers a new executor type with its factory, validator, and capabilities.
func RegisterExecutor(executorType string, factory ExecutorFactory, validator core.StepValidator, caps core.ExecutorCapabilities) {
	executorRegistryMu.Lock()
	executorRegistry[executorType] = factory
	executorRegistryMu.Unlock()
	if validator != nil {
		core.RegisterStepValidator(executorType, validator)
	}
	core.RegisterExecutorCapabilities(executorType, caps)
}

// UnregisterExecutor removes a registered executor type.
func UnregisterExecutor(executorType string) {
	executorRegistryMu.Lock()
	defer executorRegistryMu.Unlock()
	delete(executorRegistry, executorType)
}

var executorRegistry = make(map[string]ExecutorFactory)

var executorRegistryMu sync.RWMutex

// ExitCoder is an interface for executors that can return an exit code.
type ExitCoder interface {
	ExitCode() int
}

// NodeStatusDeterminer is an interface for reporting the status of a node execution.
type NodeStatusDeterminer interface {
	DetermineNodeStatus() (core.NodeStatus, error)
}

// DAGExecutor is an interface for sub DAG executors.
type DAGExecutor interface {
	Executor

	// SetParams sets the parameters for running a sub DAG.
	SetParams(RunParams)
}

// ParallelExecutor is an interface for parallel step executors.
type ParallelExecutor interface {
	Executor

	// SetParamsList sets the parameters for running multiple sub DAGs in parallel.
	SetParamsList([]RunParams)
}

// RunParams holds the parameters for running a sub DAG.
type RunParams struct {
	RunID   string
	Params  string
	DAGName string
}

// ChatMessageHandler is an interface for executors that handle chat session messages.
type ChatMessageHandler interface {
	SetContext([]exec.LLMMessage)
	GetMessages() []exec.LLMMessage
}

// PushBackAware is implemented by executors that can incorporate
// push-back feedback into their conversation flow.
type PushBackAware interface {
	SetPushBackContext(inputs map[string]string, iteration int)
}

// PushBackPreviousStdoutAware is implemented by executors that can consume the
// previous stdout log path for push-back re-execution.
type PushBackPreviousStdoutAware interface {
	SetPushBackPreviousStdout(path string)
}

// SubRunProvider is an interface for executors that spawn sub-DAG runs.
// This is used by executors like chat (with tools) to report sub-runs
// for UI drill-down functionality.
type SubRunProvider interface {
	GetSubRuns() []exec.SubDAGRun
}

// ToolDefinitionProvider is an interface for executors that provide tool definitions.
// This is used by chat executors to report what tools were available to the LLM
// for debugging and visibility purposes.
type ToolDefinitionProvider interface {
	GetToolDefinitions() []exec.ToolDefinition
}
