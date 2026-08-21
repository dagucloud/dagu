// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

const (
	executorType = "s3"

	opUpload   = "upload"
	opDownload = "download"
	opList     = "list"
	opDelete   = "delete"
)

var (
	_ executor.Executor  = (*executorImpl)(nil)
	_ executor.ExitCoder = (*executorImpl)(nil)
	_ io.Closer          = (*executorImpl)(nil)
)

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{Command: true})
}

func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	// Check for command in Commands field
	var command string
	if len(step.Commands) > 0 {
		command = step.Commands[0].Command
	}
	if strings.TrimSpace(command) == "" {
		return ir.NewValidationError("command", nil, fmt.Errorf("%w: command is required (upload, download, list, delete)", ErrConfig))
	}
	return nil
}

type executorImpl struct {
	mu       sync.Mutex
	stdout   io.Writer
	stderr   io.Writer
	cancel   context.CancelFunc
	ctx      context.Context
	exitCode int

	cfg       *Config
	operation string
	client    *awss3.Client
	closed    bool
}

func newExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	cfg := DefaultConfig()

	// Get DAG-level S3 config from context and apply as defaults
	if dagS3 := getS3ConfigFromContext(ctx); dagS3 != nil {
		cfg.ApplyDefaults(dagS3)
	}

	// Step-level with/config values override DAG-level defaults.
	if step.ExecutorConfig.Config != nil {
		if err := decodeConfig(step.ExecutorConfig.Config, cfg); err != nil {
			return nil, fmt.Errorf("invalid s3 configuration: %w", err)
		}
	}

	// Extract command from Commands field
	var command string
	if len(step.Commands) > 0 {
		command = step.Commands[0].Command
	}
	op := strings.ToLower(strings.TrimSpace(command))
	if op == "" {
		return nil, fmt.Errorf("%w: command must specify s3 operation", ErrConfig)
	}

	switch op {
	case opUpload, opDownload, opList, opDelete:
	default:
		return nil, fmt.Errorf("%w: unsupported s3 operation %q (valid: upload, download, list, delete)", ErrConfig, op)
	}

	if err := cfg.ValidateForOperation(op); err != nil {
		return nil, err
	}

	rCtx, cancel := context.WithCancel(ctx)

	client, err := createClient(rCtx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}

	return &executorImpl{
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		cancel:    cancel,
		ctx:       rCtx,
		cfg:       cfg,
		operation: op,
		client:    client,
	}, nil
}

func (e *executorImpl) SetStdout(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stdout = out
}

func (e *executorImpl) SetStderr(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stderr = out
}

func (e *executorImpl) Run(_ context.Context) error {
	e.mu.Lock()
	ctx := e.ctx
	e.mu.Unlock()

	err := e.runOperation(ctx)

	e.mu.Lock()
	e.exitCode = exitCodeFor(err)
	e.mu.Unlock()

	return err
}

func (e *executorImpl) runOperation(ctx context.Context) error {
	switch e.operation {
	case opUpload:
		return e.runUpload(ctx)
	case opDownload:
		return e.runDownload(ctx)
	case opList:
		return e.runList(ctx)
	case opDelete:
		return e.runDelete(ctx)
	default:
		return fmt.Errorf("%w: unknown operation %q", ErrConfig, e.operation)
	}
}

func (e *executorImpl) Kill(_ os.Signal) error {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (e *executorImpl) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *executorImpl) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	return nil
}

// writeResult writes the result as JSON to stdout.
func (e *executorImpl) writeResult(v any) error {
	return encodeJSON(e.stdout, v)
}
