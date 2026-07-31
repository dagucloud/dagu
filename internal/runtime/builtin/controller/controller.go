// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package controller registers the executor identity of the synthesized step
// that drives a controller DAG. The decision loop itself is driven by the
// runner, which needs access to the execution plan; this executor exists so the
// controller has a node, a log file, and a persisted LLM transcript.
package controller

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

type controllerExecutor struct {
	stdout io.Writer
	stderr io.Writer
}

func newController(_ context.Context, _ core.Step) (executor.Executor, error) {
	return &controllerExecutor{stdout: os.Stdout, stderr: os.Stderr}, nil
}

func (e *controllerExecutor) Run(_ context.Context) error {
	return nil
}

func (e *controllerExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *controllerExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *controllerExecutor) Kill(_ os.Signal) error {
	return nil
}

func init() {
	executor.RegisterExecutor(core.ExecutorTypeController, newController, nil, core.ExecutorCapabilities{
		LLM: true,
	})

	core.RegisterStepValidator(core.ExecutorTypeController, func(step core.Step) error {
		if step.Name != core.ControllerStepName {
			return fmt.Errorf("controller is not a step action; set the DAG type to 'controller' instead")
		}
		return nil
	})
}
