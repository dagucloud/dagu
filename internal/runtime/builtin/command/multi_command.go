// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

var _ executor.Executor = (*multiCommandExecutor)(nil)
var _ executor.ExitCoder = (*multiCommandExecutor)(nil)
var _ executor.OutputBufferingAware = (*multiCommandExecutor)(nil)

// multiCommandExecutor executes multiple commands sequentially.
// It stops on the first command that fails (non-zero exit code).
type multiCommandExecutor struct {
	mu       sync.Mutex
	configs  []*commandConfig
	current  *commandExecutor
	exitCode int
	stdout   io.Writer
	stderr   io.Writer
}

// newMultiCommandExecutor creates an executor that runs multiple commands sequentially.
func newMultiCommandExecutor(ctx context.Context, step core.Step) (*multiCommandExecutor, error) {
	env := runtime.GetEnv(ctx)

	configs := make([]*commandConfig, 0, len(step.Commands))
	for _, cmd := range step.Commands {
		cfg := &commandConfig{
			Ctx:                ctx,
			Dir:                env.WorkingDir,
			Command:            cmd.Command,
			Args:               cmd.Args,
			Shell:              env.Shell(ctx),
			ShellCommandArgs:   cmd.CmdWithArgs,
			ShellPackages:      step.ShellPackages,
			UserSpecifiedShell: step.Shell != "",
			Stdout:             os.Stdout,
			Stderr:             os.Stderr,
		}
		configs = append(configs, cfg)
	}

	return &multiCommandExecutor{
		configs: configs,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
	}, nil
}

func (e *multiCommandExecutor) SetStdout(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stdout = out
	for _, cfg := range e.configs {
		cfg.Stdout = out
	}
}

func (e *multiCommandExecutor) SetStderr(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stderr = out
	for _, cfg := range e.configs {
		cfg.Stderr = out
	}
}

// SetOutputBuffering implements executor.OutputBufferingAware.
func (e *multiCommandExecutor) SetOutputBuffering(mode core.OutputBuffering) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, cfg := range e.configs {
		cfg.outputBuffering = mode
	}
}

func (e *multiCommandExecutor) Kill(sig os.Signal) error {
	e.mu.Lock()
	current := e.current
	e.mu.Unlock()

	if current != nil {
		return current.Kill(sig)
	}
	return nil
}

func (e *multiCommandExecutor) Run(ctx context.Context) error {
	for i, cfg := range e.configs {
		// Create a new single command executor for this command
		exec := &commandExecutor{config: cfg}

		e.mu.Lock()
		e.current = exec
		e.mu.Unlock()

		// Run the command
		if err := exec.Run(ctx); err != nil {
			e.mu.Lock()
			e.exitCode = exec.ExitCode()
			e.current = nil
			e.mu.Unlock()
			return fmt.Errorf("command %d failed: %w", i+1, err)
		}

		e.mu.Lock()
		e.exitCode = exec.ExitCode()
		e.current = nil
		e.mu.Unlock()

		// Check context cancellation between commands
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	return nil
}

func (e *multiCommandExecutor) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}
