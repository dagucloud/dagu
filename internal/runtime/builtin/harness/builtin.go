// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/builtin/agentstep"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

func (e *harnessExecutor) runBuiltinOnce(ctx context.Context, cfg providerConfig) (*os.File, error) {
	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderrWriter(), 0, env.LogEncodingCharset)
	e.stderrTail = tw

	stdout, err := newStdoutSpool()
	if err != nil {
		e.exitCode = 1
		return nil, fmt.Errorf("harness: failed to create stdout spool: %w", err)
	}

	step, err := e.builtinAgentStep(cfg)
	if err != nil {
		e.exitCode = 1
		_ = cleanupStdoutSpool(stdout)
		return nil, err
	}

	agentExec, err := agentstep.NewExecutor(ctx, step)
	if err != nil {
		e.exitCode = 1
		_ = cleanupStdoutSpool(stdout)
		return nil, fmt.Errorf("harness: failed to create builtin provider: %w", err)
	}
	defer func() {
		if closeErr := executor.CloseExecutor(agentExec); closeErr != nil {
			_, _ = fmt.Fprintf(e.stderrWriter(), "harness: failed to close builtin provider: %v\n", closeErr)
		}
	}()

	agentExec.SetStdout(stdout)
	agentExec.SetStderr(tw)
	if chatHandler, ok := agentExec.(executor.ChatMessageHandler); ok {
		chatHandler.SetContext(e.contextMessages)
	}
	if pbHandler, ok := agentExec.(executor.PushBackAware); ok && e.pushBackIteration > 0 {
		pbHandler.SetPushBackContext(e.pushBackInputs, e.pushBackIteration)
	}

	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancelBuiltin = cancel
	e.builtinStopped = false
	e.mu.Unlock()

	err = agentExec.Run(runCtx)
	runCtxErr := runCtx.Err()
	parentCtxErr := ctx.Err()
	cancel()

	e.mu.Lock()
	stopped := e.builtinStopped
	e.cancelBuiltin = nil
	e.builtinStopped = false
	e.mu.Unlock()

	if builtinRunCanceled(stopped, runCtxErr, parentCtxErr) {
		e.exitCode = 124
		_ = cleanupStdoutSpool(stdout)
		return nil, context.Canceled
	}

	if err != nil {
		e.exitCode = 1
		stdoutTail, tailErr := readSpoolTail(stdout, failedStdoutTailLimit, env.LogEncodingCharset)
		_ = cleanupStdoutSpool(stdout)
		if tailErr != nil {
			return nil, fmt.Errorf("harness: failed to read stdout tail: %w", tailErr)
		}
		if stdoutTail != "" {
			_, _ = fmt.Fprintf(e.stderrWriter(), "recent stdout (tail):\n%s\n", stdoutTail)
		}
		return nil, formatProcessFailure(err, tw.Tail(), stdoutTail)
	}

	if _, err := stdout.Seek(0, io.SeekStart); err != nil {
		e.exitCode = 1
		_ = cleanupStdoutSpool(stdout)
		return nil, fmt.Errorf("harness: failed to rewind stdout spool: %w", err)
	}

	if chatHandler, ok := agentExec.(executor.ChatMessageHandler); ok {
		e.savedMessages = append([]coreexec.LLMMessage(nil), chatHandler.GetMessages()...)
	}

	return stdout, nil
}

func builtinRunCanceled(stopped bool, runCtxErr, parentCtxErr error) bool {
	return stopped || errors.Is(runCtxErr, context.Canceled) || errors.Is(parentCtxErr, context.Canceled)
}

func (e *harnessExecutor) builtinAgentStep(cfg providerConfig) (core.Step, error) {
	agentCfg, err := agentConfigFromBuiltinHarnessConfig(cfg.flags)
	if err != nil {
		return core.Step{}, err
	}

	message := promptAndScript(e.effectivePrompt(), e.script)
	if message == "" {
		return core.Step{}, errors.New("harness: builtin provider requires a prompt")
	}

	return core.Step{
		ID:             e.step.ID,
		Name:           e.step.Name,
		Dir:            e.step.Dir,
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeAgent},
		Messages: []core.LLMMessage{
			{Role: core.LLMRoleUser, Content: message},
		},
		Agent:    agentCfg,
		Approval: e.step.Approval,
	}, nil
}
