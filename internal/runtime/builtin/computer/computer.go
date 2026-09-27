// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package computer implements the computer.extract and computer.run
// actions, which operate the desktop of the worker's user session.
package computer

import (
	"context"
	"io"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.Executor                = (*computerExecutor)(nil)
	_ executor.AgentSessionHandler     = (*computerExecutor)(nil)
	_ executor.ProgressCallbackAware   = (*computerExecutor)(nil)
	_ executor.NodeStatusDeterminer    = (*computerExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*computerExecutor)(nil)
	_ io.Closer                        = (*computerExecutor)(nil)
)

// providerFactory builds a provider for one resolved model configuration.
type providerFactory func(ctx context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error)

// sessionFactory starts a computer-use session with a provider.
type sessionFactory func(providerType llmpkg.ProviderType, provider llmpkg.Provider, mode computeruse.Mode, opts computeruse.Options) (computeruse.Session, error)

type computerExecutor struct {
	step        ir.Step
	cfg         config
	openDesktop func() (*desktop.Driver, error)
	launch      func(dir, command string, args []string) error
	newProvider providerFactory
	newSession  sessionFactory
	settle      settleTiming
	// desktopLock is the directory of the lock that gives one step at a
	// time the desktop; empty uses the data directory.
	desktopLock string
	// idlePoll spaces the checks for a person using the desktop.
	idlePoll time.Duration
	stdout   io.Writer
	stderr   io.Writer

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
		return nil, errNoModel
	}
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &computerExecutor{
		step:        step,
		cfg:         cfg,
		openDesktop: desktop.Open,
		launch:      desktop.Launch,
		newProvider: runtime.NewLLMProvider,
		newSession:  computeruse.New,
		settle:      defaultSettle,
		desktopLock: userDesktopLock(),
		idlePoll:    defaultIdlePoll,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}, nil
}

func (e *computerExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *computerExecutor) SetStderr(out io.Writer) { e.stderr = out }

func (e *computerExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (e *computerExecutor) Close() error {
	return e.Kill(nil)
}

func (e *computerExecutor) Run(ctx context.Context) error {
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

func (e *computerExecutor) SetAgentSession(session *ir.AgentSession) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.session = ir.CloneAgentSession(session)
}

func (e *computerExecutor) GetAgentSession() *ir.AgentSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ir.CloneAgentSession(e.session)
}

func (e *computerExecutor) SetProgressCallback(callback func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress = callback
}

func (e *computerExecutor) DetermineNodeStatus() (ir.NodeStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hasNodeStatus {
		return e.nodeStatus, nil
	}
	return ir.NodeRunning, nil
}

func (e *computerExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

func (e *computerExecutor) PublishesDeclaredOutputs() bool {
	return true
}

// updateSession applies fn to the agent session and reports the change.
func (e *computerExecutor) updateSession(fn func(*ir.AgentSession)) {
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

func (e *computerExecutor) setOutputs(outputs map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outputs = maps.Clone(outputs)
}

func (e *computerExecutor) setNodeStatus(status ir.NodeStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nodeStatus = status
	e.hasNodeStatus = true
}
