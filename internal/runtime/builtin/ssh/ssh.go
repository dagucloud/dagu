// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"golang.org/x/crypto/ssh"
)

var _ executor.Executor = (*sshExecutor)(nil)

type sshClientCtxKey struct{}

// WithSSHClient creates a new context with client
func WithSSHClient(ctx context.Context, cli *Client) context.Context {
	return context.WithValue(ctx, sshClientCtxKey{}, cli)
}

// getSSHClientFromContext retrieves the Client from the context.
func getSSHClientFromContext(ctx context.Context) *Client {
	if cli, ok := ctx.Value(sshClientCtxKey{}).(*Client); ok {
		return cli
	}
	return nil
}

type sshExecutor struct {
	mu        sync.Mutex
	step      core.Step
	client    *Client
	stdout    io.Writer
	stderr    io.Writer
	conn      *ssh.Client  // SSH connection (must be closed after session)
	session   *ssh.Session // SSH session
	closed    bool         // Whether session/conn have been closed
	shell     string
	shellArgs []string
}

func NewSSHExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	client, err := resolveSSHClient(ctx, step)
	if err != nil {
		return nil, fmt.Errorf("failed to set up ssh step: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("ssh configuration is not found")
	}

	shell, shellArgs := resolveShell(step, client)

	return &sshExecutor{
		step:      step,
		client:    client,
		shell:     shell,
		shellArgs: shellArgs,
		stdout:    os.Stdout,
		stderr:    os.Stderr,
	}, nil
}

func (e *sshExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *sshExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *sshExecutor) Kill(_ os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}
	e.closed = true

	var sessionErr, connErr error
	if e.session != nil {
		sessionErr = e.session.Close()
	}
	if e.conn != nil {
		connErr = e.conn.Close()
	}
	return errors.Join(sessionErr, connErr)
}

func (e *sshExecutor) Run(ctx context.Context) error {
	if len(e.step.Commands) == 0 && e.step.Script == "" {
		return nil
	}

	conn, session, err := e.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}

	e.mu.Lock()
	e.conn = conn
	e.session = session
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		if e.closed {
			return
		}

		// Close session first, then the underlying connection
		if closeErr := session.Close(); closeErr != nil {
			logger.Warn(ctx, "SSH session close error", tag.Error(closeErr))
		}
		if closeErr := conn.Close(); closeErr != nil {
			logger.Warn(ctx, "SSH connection close error", tag.Error(closeErr))
		}
		e.closed = true
	}()

	session.Stdout = e.stdout
	session.Stderr = e.stderr
	session.Stdin = strings.NewReader(e.buildScript())

	return e.runWithCancellation(ctx, session, e.buildShellCommand())
}

// runWithCancellation executes the session command with context cancellation support.
func (e *sshExecutor) runWithCancellation(ctx context.Context, session *ssh.Session, shellCmd string) error {
	done := make(chan error, 1)
	go func() {
		done <- session.Run(shellCmd)
	}()

	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		return fmt.Errorf("ssh execution failed: %w", err)
	case <-ctx.Done():
		// Close session to unblock the goroutine, then wait for it to finish
		_ = session.Close()
		<-done
		return ctx.Err()
	}
}

// buildShellCommand constructs the shell command string with arguments.
func (e *sshExecutor) buildShellCommand() string {
	if len(e.shellArgs) == 0 {
		return e.shell
	}
	return e.shell + " " + strings.Join(e.shellArgs, " ")
}

// buildScript constructs a complete script for SSH execution, wrapped in a function.
// The function wrapper ensures the shell reads all input before execution,
// making it robust against slow networks and buffering issues.
func (e *sshExecutor) buildScript() string {
	var body strings.Builder

	// For SSH execution, only use working directory if explicitly set at step level.
	// DAG-level workingDir is for LOCAL execution and may not exist on the remote host.
	// If step.Dir is empty, run in SSH user's home directory.
	workingDir := e.step.Dir

	// Change to working directory if explicitly specified
	if workingDir != "" {
		fmt.Fprintf(&body, "cd %s || return 1\n", cmdutil.ShellQuote(workingDir))
	}

	// Add error handling (exit on first error)
	body.WriteString("set -e\n")

	// Add script content or commands
	if e.step.Script != "" {
		body.WriteString(e.step.Script)
		if !strings.HasSuffix(e.step.Script, "\n") {
			body.WriteString("\n")
		}
	} else {
		for _, cmd := range e.step.Commands {
			body.WriteString(e.buildCommandString(cmd))
			body.WriteString("\n")
		}
	}

	// Wrap in function - shell MUST read entire body before executing
	return fmt.Sprintf("__dagu_exec(){\n%s}\n__dagu_exec\n", body.String())
}

// buildCommandString constructs a command string from a CommandEntry.
// For SSH, we prefer CmdWithArgs (the original command string) so that
// variable references like $HOME are passed through to the remote shell
// without being single-quoted.
func (e *sshExecutor) buildCommandString(cmd core.CommandEntry) string {
	if cmd.CmdWithArgs != "" {
		return cmd.CmdWithArgs
	}
	if len(cmd.Args) == 0 {
		return cmd.Command
	}
	return cmd.Command + " " + cmdutil.ShellQuoteArgs(cmd.Args)
}

// resolveShell determines the shell to use for remote execution.
// Priority:
// 1. Shell specified in SSH configuration (step-level or DAG-level).
// 2. Shell specified in the step's Shell field.
// 3. /bin/sh as POSIX-compliant fallback.
// Note: DAG-level shell (dag.Shell) is NOT used as it's configured for local execution.
func resolveShell(step core.Step, client *Client) (string, []string) {
	if client != nil && client.Shell != "" {
		return client.Shell, slices.Clone(client.ShellArgs)
	}
	if step.Shell != "" {
		return step.Shell, slices.Clone(step.ShellArgs)
	}
	// Fallback to /bin/sh - POSIX standard, available on all Unix systems
	return "/bin/sh", nil
}

func init() {
	caps := core.ExecutorCapabilities{
		Command:          true,
		MultipleCommands: true,
		Script:           true,
		Shell:            true,
		GetCommandEvalOptions: func(ctx context.Context, step core.Step) []cmnvalue.Option {
			if hasShellConfigured(ctx, step) {
				// Shell is configured, shell features (expansion, pipes, etc.) are supported
				return []cmnvalue.Option{cmnvalue.WithoutDollarEscape()}
			}
			// No shell configured - skip shell expansion for remote execution
			return []cmnvalue.Option{cmnvalue.WithoutExpandShell()}
		},
		GetScriptEvalOptions: func(ctx context.Context, step core.Step) []cmnvalue.Option {
			if hasShellConfigured(ctx, step) {
				// Shell is configured, shell features (expansion, pipes, etc.) are supported
				return []cmnvalue.Option{cmnvalue.WithoutDollarEscape()}
			}
			// No shell configured - skip shell expansion for remote execution
			return []cmnvalue.Option{cmnvalue.WithoutExpandShell()}
		},
	}
	executor.RegisterExecutor("ssh", NewSSHExecutor, nil, caps)
}

func hasShellConfigured(ctx context.Context, step core.Step) bool {
	if len(step.ExecutorConfig.Config) > 0 {
		return cmdutil.IsShellValueSet(step.ExecutorConfig.Config["shell"])
	}
	if cli := getSSHClientFromContext(ctx); cli != nil && cli.Shell != "" {
		return true
	}
	return step.Shell != ""
}
