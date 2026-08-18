// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agent registers the executor identity of the synthesized step
// that drives an agent DAG. The decision loop itself is driven by the
// runner, which needs access to the execution plan; this executor exists so the
// agent has a node, a log file, and a persisted LLM transcript.
package agent

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

type agentExecutor struct {
	stdout io.Writer
	stderr io.Writer
}

func newAgent(_ context.Context, _ ir.Step) (executor.Executor, error) {
	return &agentExecutor{stdout: os.Stdout, stderr: os.Stderr}, nil
}

func (e *agentExecutor) Run(_ context.Context) error {
	return nil
}

func (e *agentExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *agentExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *agentExecutor) Kill(_ os.Signal) error {
	return nil
}

func init() {
	executor.RegisterExecutor(ir.ExecutorTypeAgent, newAgent, nil, registry.ExecutorCapabilities{
		LLM: true,
	})

	registry.RegisterStepValidator(ir.ExecutorTypeAgent, func(step ir.Step) error {
		if step.Name != ir.AgentStepName {
			return fmt.Errorf("agent is not a step action; set the DAG type to 'agent' instead")
		}
		return nil
	})
}
