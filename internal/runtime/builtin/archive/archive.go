// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

const (
	executorType = "archive"

	opExtract = "extract"
	opCreate  = "create"
	opList    = "list"
)

var (
	_ executor.Executor  = (*executorImpl)(nil)
	_ executor.ExitCoder = (*executorImpl)(nil)
)

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	// Check for command in Commands field
	var command string
	if len(step.Commands) > 0 {
		command = step.Commands[0].Command
	}
	if strings.TrimSpace(command) == "" {
		return core.NewValidationError("command", nil, fmt.Errorf("%w: command is required", ErrConfig))
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

	cfg       Config
	operation string
	stepName  string
}

func newExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	cfg := defaultConfig()

	if step.ExecutorConfig.Config != nil {
		if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
			return nil, fmt.Errorf("invalid archive configuration: %w", err)
		}
	}

	// Extract command from Commands field
	var command string
	if len(step.Commands) > 0 {
		command = step.Commands[0].Command
	}
	op := strings.ToLower(strings.TrimSpace(command))
	if op == "" {
		return nil, core.NewValidationError("command", nil, fmt.Errorf("%w: command must specify archive operation", ErrConfig))
	}

	switch op {
	case opExtract, opCreate, opList:
	default:
		return nil, fmt.Errorf("%w: unsupported archive operation %q", ErrConfig, op)
	}

	if err := validateConfig(op, &cfg); err != nil {
		return nil, err
	}

	rCtx, cancel := context.WithCancel(ctx)

	return &executorImpl{
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		cancel:    cancel,
		ctx:       rCtx,
		cfg:       cfg,
		operation: op,
		stepName:  step.Name,
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

	var err error

	switch e.operation {
	case opExtract:
		err = e.runExtract(ctx)
	case opCreate:
		err = e.runCreate(ctx)
	case opList:
		err = e.runList(ctx)
	default:
		err = fmt.Errorf("%w: unknown operation %q", ErrConfig, e.operation)
	}

	e.mu.Lock()
	if err == nil {
		e.exitCode = 0
	} else {
		e.exitCode = e.exitCodeFor(err)
	}
	e.mu.Unlock()

	return err
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

func (e *executorImpl) exitCodeFor(err error) int {
	switch {
	case errors.Is(err, ErrConfig):
		return 2
	case errors.Is(err, ErrSourceNotFound):
		return 3
	case errors.Is(err, ErrDestination):
		return 4
	case errors.Is(err, ErrFormatDetection):
		return 5
	case errors.Is(err, ErrExtract):
		return 6
	case errors.Is(err, ErrCreate):
		return 7
	case errors.Is(err, ErrCompress):
		return 8
	case errors.Is(err, ErrDecompress):
		return 9
	case errors.Is(err, ErrPermission):
		return 10
	case errors.Is(err, ErrDiskSpace):
		return 11
	case errors.Is(err, ErrCorrupted):
		return 12
	case errors.Is(err, ErrPassword):
		return 13
	case errors.Is(err, context.Canceled):
		return 14
	default:
		return 1
	}
}

// resultWriter marshals v into JSON and writes with a trailing newline.
func (e *executorImpl) resultWriter(v any) error {
	return encodeJSON(e.stdout, v)
}

func (e *executorImpl) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(e.stderr, "archive step warning: %s\n", msg)
}

func (e *executorImpl) ensureDir(path string) error {
	if e.cfg.DryRun {
		return nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %w", ErrPermission, err)
		}
		return fmt.Errorf("%w: %w", ErrDestination, err)
	}
	return nil
}

func (e *executorImpl) secureJoin(base, rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	if rel == "." {
		return base, nil
	}
	target := filepath.Join(base, rel)
	if !pathWithin(base, target) {
		return "", fmt.Errorf("%w: target %q escapes destination", ErrCorrupted, rel)
	}
	return target, nil
}

func sanitizeArchivePath(name string, strip int, preserve bool) string {
	clean := filepath.ToSlash(name)
	clean = strings.TrimPrefix(clean, "./")
	if clean == "" || clean == "." {
		return ""
	}
	parts := strings.Split(clean, "/")
	if strip > 0 {
		if strip >= len(parts) {
			return ""
		}
		parts = parts[strip:]
	}
	if !preserve {
		return filepath.Base(strings.Join(parts, "/"))
	}
	return filepath.Join(parts...)
}

// matchesFilters returns true if path matches include/exclude sets.
func matchesFilters(path string, include, exclude []string) bool {
	if len(include) > 0 {
		matched := false
		for _, pattern := range include {
			if globMatch(pattern, path) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	for _, pattern := range exclude {
		if globMatch(pattern, path) {
			return false
		}
	}
	return true
}

func pathWithin(base, target string) bool {
	baseClean := filepath.Clean(base)
	targetClean := filepath.Clean(target)

	rel, err := filepath.Rel(baseClean, targetClean)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}

	if runtime.GOOS == "windows" {
		baseClean = strings.ToLower(baseClean)
		targetClean = strings.ToLower(targetClean)
	}

	if !strings.HasSuffix(baseClean, string(os.PathSeparator)) {
		baseClean += string(os.PathSeparator)
	}
	if !strings.HasSuffix(targetClean, string(os.PathSeparator)) {
		targetClean += string(os.PathSeparator)
	}

	return strings.HasPrefix(targetClean, baseClean)
}
