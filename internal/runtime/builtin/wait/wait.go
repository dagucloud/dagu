// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wait

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

const (
	executorType = "wait"

	opDuration = "duration"
	opUntil    = "until"
	opFile     = "file"
	opHTTP     = "http"

	stateExists  = "exists"
	stateMissing = "missing"
)

var _ executor.Executor = (*executorImpl)(nil)

type executorImpl struct {
	mu      sync.Mutex
	stdout  io.Writer
	stderr  io.Writer
	op      string
	cfg     parsedConfig
	workDir string
	kill    context.Context
	cancel  context.CancelFunc
}

func newExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}

	op := stepOperation(step)
	parsed, err := parseConfig(op, cfg)
	if err != nil {
		return nil, err
	}

	kill, cancel := context.WithCancel(ctx)
	env := runtime.GetEnv(ctx)
	return &executorImpl{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		op:      op,
		cfg:     parsed,
		workDir: env.WorkingDir,
		kill:    kill,
		cancel:  cancel,
	}, nil
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return err
	}
	_, err := parseConfig(stepOperation(step), cfg)
	return err
}

func stepOperation(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
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

func (e *executorImpl) Kill(_ os.Signal) error {
	e.cancel()
	return nil
}

func (e *executorImpl) Run(ctx context.Context) error {
	ctx, stop := e.runContext(ctx)
	defer stop()

	switch e.op {
	case opDuration:
		return waitFor(ctx, e.cfg.duration)
	case opUntil:
		return waitUntil(ctx, e.cfg.until)
	case opFile:
		return e.waitFile(ctx)
	case opHTTP:
		return e.waitHTTP(ctx)
	default:
		return fmt.Errorf("wait: unsupported operation %q", e.op)
	}
}

func (e *executorImpl) runContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-e.kill.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func waitFor(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitUntil(ctx context.Context, until time.Time) error {
	duration := time.Until(until)
	if duration <= 0 {
		return ctx.Err()
	}
	return waitFor(ctx, duration)
}

func (e *executorImpl) waitFile(ctx context.Context) error {
	return poll(ctx, e.cfg.pollInterval, func() (bool, error) {
		path := e.resolvePath(e.cfg.Path)
		_, err := os.Stat(path)
		switch e.cfg.State {
		case stateExists:
			if err == nil {
				return true, nil
			}
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		case stateMissing:
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			if err == nil {
				return false, nil
			}
			return false, err
		default:
			return false, fmt.Errorf("wait: unsupported file state %q", e.cfg.State)
		}
	})
}

func (e *executorImpl) waitHTTP(ctx context.Context) error {
	client := &http.Client{Timeout: e.cfg.requestTimeout}
	return poll(ctx, e.cfg.pollInterval, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, strings.ToUpper(e.cfg.Method), e.cfg.URL, strings.NewReader(e.cfg.Body))
		if err != nil {
			return false, err
		}
		for key, value := range e.cfg.Headers {
			req.Header.Set(key, value)
		}

		resp, err := client.Do(req)
		if err != nil {
			return false, nil
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode == e.cfg.Status, nil
	})
}

func (e *executorImpl) resolvePath(path string) string {
	if filepath.IsAbs(path) || e.workDir == "" {
		return path
	}
	return filepath.Join(e.workDir, path)
}

func poll(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if err := waitFor(ctx, interval); err != nil {
			return err
		}
	}
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
}
