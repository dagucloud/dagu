// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package browser implements the browser.extract and browser.run actions.
package browser

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	goruntime "runtime"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.Executor                = (*browserExecutor)(nil)
	_ executor.AgentSessionHandler     = (*browserExecutor)(nil)
	_ executor.ProgressCallbackAware   = (*browserExecutor)(nil)
	_ executor.NodeStatusDeterminer    = (*browserExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*browserExecutor)(nil)
	_ io.Closer                        = (*browserExecutor)(nil)
)

type browserExecutor struct {
	step        ir.Step
	cfg         config
	launcher    launcher
	newProvider providerFactory
	stdout      io.Writer
	stderr      io.Writer
	// askSupported reports whether a browser left open for an ask outlives
	// the step process on this platform.
	askSupported bool

	mu            sync.Mutex
	cancel        context.CancelFunc
	session       *ir.AgentSession
	progress      func()
	outputs       map[string]any
	nodeStatus    ir.NodeStatus
	hasNodeStatus bool
}

func newExecutor(_ context.Context, step ir.Step) (executor.Executor, error) {
	if step.LLM == nil {
		return nil, errors.New("browser actions need a model: set llm at the DAG level or with.llm on the step")
	}
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &browserExecutor{
		step:     step,
		cfg:      cfg,
		launcher: stagehandLauncher{},
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		// On Windows, Dagu runs steps in a job object that ends every child
		// process, including the browser, when the step process exits.
		askSupported: goruntime.GOOS != "windows",
	}, nil
}

func (e *browserExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *browserExecutor) SetStderr(out io.Writer) { e.stderr = out }

func (e *browserExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (e *browserExecutor) Close() error {
	return e.Kill(nil)
}

func (e *browserExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	r, err := newRun(ctx, e)
	if err != nil {
		return err
	}
	return r.execute(ctx)
}

func (e *browserExecutor) SetAgentSession(session *ir.AgentSession) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.session = ir.CloneAgentSession(session)
}

func (e *browserExecutor) GetAgentSession() *ir.AgentSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ir.CloneAgentSession(e.session)
}

func (e *browserExecutor) SetProgressCallback(callback func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress = callback
}

func (e *browserExecutor) DetermineNodeStatus() (ir.NodeStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hasNodeStatus {
		return e.nodeStatus, nil
	}
	return ir.NodeRunning, nil
}

func (e *browserExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

func (e *browserExecutor) PublishesDeclaredOutputs() bool {
	return true
}

// updateSession applies fn to the agent session and reports the change.
func (e *browserExecutor) updateSession(fn func(*ir.AgentSession)) {
	e.mu.Lock()
	if e.session == nil {
		e.session = &ir.AgentSession{Provider: providerName, Generation: 1}
	}
	fn(e.session)
	callback := e.progress
	e.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (e *browserExecutor) setOutputs(outputs map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outputs = maps.Clone(outputs)
}

func (e *browserExecutor) setNodeStatus(status ir.NodeStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nodeStatus = status
	e.hasNodeStatus = true
}
