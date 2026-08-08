// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package kubernetes

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// Kubernetes executor runs a command as a Kubernetes Job.
/* Example DAG:
```yaml
steps:
  - name: run-in-k8s
    type: k8s
    with:
      image: python:3.11
      namespace: default
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
        limits:
          cpu: "500m"
          memory: "512Mi"
      env:
        - name: MY_VAR
          value: hello
    command: python -c "print('Hello from Kubernetes')"
```
*/

var _ executor.Executor = (*kubernetesExecutor)(nil)
var _ executor.ExitCoder = (*kubernetesExecutor)(nil)

type jobClient interface {
	CreateJob(ctx context.Context, stepName string, command []string) error
	WaitForPod(ctx context.Context) (string, error)
	StreamLogs(ctx context.Context, podName string, stdout io.Writer) error
	WaitForPodTermination(ctx context.Context, podName string) error
	GetExitCode(ctx context.Context, podName string) (int, error)
	WaitForCompletion(ctx context.Context) error
	DeleteJob(ctx context.Context) error
	GetJobName() string
}

var newJobClient = func(cfg *Config) (jobClient, error) {
	return NewClient(cfg)
}

const jobCleanupTimeout = 30 * time.Second

type kubernetesExecutor struct {
	step     ir.Step
	stdout   io.Writer
	stderr   io.Writer
	cfg      *Config
	client   jobClient
	mu       sync.Mutex
	exitCode int
	cancel   func()
}

func (e *kubernetesExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *kubernetesExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *kubernetesExecutor) Kill(_ os.Signal) error {
	cancel, client := e.stopExecution()
	if cancel != nil {
		cancel()
	}
	if client == nil {
		return nil
	}
	return e.deleteJob(context.Background(), client)
}

func (e *kubernetesExecutor) Run(ctx context.Context) error {
	logger.Info(ctx, "Kubernetes executor: starting",
		slog.String("step", e.step.Name),
		slog.String("image", e.cfg.Image),
		slog.String("namespace", e.cfg.Namespace),
	)

	ctx, cancelFunc := context.WithCancel(ctx)
	e.setCancel(cancelFunc)
	defer cancelFunc()
	defer e.clearCancel()

	// Wrap stderr with a tail writer for error messages
	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderr, 0, env.LogEncodingCharset)
	e.stderr = tw

	// Initialize Kubernetes client
	client, err := newJobClient(e.cfg)
	if err != nil {
		logger.Error(ctx, "Kubernetes executor: failed to create client", slog.Any("error", err))
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	e.setClient(client)

	// Build command from step
	command := buildCommand(e.step)

	// Create the Job
	if err := client.CreateJob(ctx, e.step.Name, command); err != nil {
		logger.Error(ctx, "Kubernetes executor: failed to create job", slog.Any("error", err))
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("failed to create kubernetes job: %w\nrecent stderr (tail):\n%s", err, tail)
		}
		return fmt.Errorf("failed to create kubernetes job: %w", err)
	}
	logger.Info(ctx, "Kubernetes executor: job created",
		slog.String("job", client.GetJobName()),
	)

	// Wait for a Pod to be scheduled and running
	podName, err := client.WaitForPod(ctx)
	if err != nil {
		logger.Error(ctx, "Kubernetes executor: pod scheduling failed", slog.Any("error", err))
		if ctx.Err() != nil {
			e.cleanup(ctx, true)
			return ctx.Err()
		}
		e.cleanup(ctx, false)
		return fmt.Errorf("pod scheduling failed: %w", err)
	}
	logger.Info(ctx, "Kubernetes executor: pod running",
		slog.String("pod", podName),
	)

	// Stream logs from the pod to stdout
	// Kubernetes merges stdout/stderr into a single stream
	if err := client.StreamLogs(ctx, podName, e.stdout); err != nil {
		logger.Warn(ctx, "Kubernetes executor: log streaming ended", slog.Any("error", err))
	}

	// Ensure pod has terminated before reading exit code
	if err := client.WaitForPodTermination(ctx, podName); err != nil {
		if ctx.Err() != nil {
			e.cleanup(ctx, true)
			return ctx.Err()
		}
		logger.Warn(ctx, "Kubernetes executor: error waiting for pod termination", slog.Any("error", err))
	}

	// Get exit code
	exitCode, err := client.GetExitCode(ctx, podName)
	if err != nil {
		logger.Warn(ctx, "Kubernetes executor: could not get exit code", slog.Any("error", err))
	} else {
		e.setExitCode(exitCode)
	}

	// Wait for Job completion status
	if err := client.WaitForCompletion(ctx); err != nil {
		if ctx.Err() != nil {
			e.cleanup(ctx, true)
			return ctx.Err()
		}
		logger.Error(ctx, "Kubernetes executor: job failed", slog.Any("error", err))
		e.cleanup(ctx, false)
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("kubernetes job failed: %w\nrecent stderr (tail):\n%s", err, tail)
		}
		return fmt.Errorf("kubernetes job failed: %w", err)
	}

	// Clean up if configured
	e.cleanup(ctx, false)

	logger.Info(ctx, "Kubernetes executor: completed successfully",
		slog.String("job", client.GetJobName()),
		slog.Int("exitCode", e.ExitCode()),
	)
	return nil
}

func (e *kubernetesExecutor) cleanup(ctx context.Context, force bool) {
	client := e.getClient()
	if client == nil {
		return
	}
	if !force && e.cfg.CleanupPolicy != cleanupPolicyDelete {
		return
	}
	if err := e.deleteJob(ctx, client); err != nil {
		logger.Warn(ctx, "Kubernetes executor: cleanup failed", slog.Any("error", err))
	}
}

// ExitCode implements executor.ExitCoder.
func (e *kubernetesExecutor) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *kubernetesExecutor) setExitCode(code int) {
	e.mu.Lock()
	e.exitCode = code
	e.mu.Unlock()
}

func (e *kubernetesExecutor) setCancel(cancel func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancel = cancel
}

func (e *kubernetesExecutor) clearCancel() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancel = nil
}

func (e *kubernetesExecutor) setClient(client jobClient) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.client = client
}

func (e *kubernetesExecutor) getClient() jobClient {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client
}

func (e *kubernetesExecutor) stopExecution() (func(), jobClient) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cancel := e.cancel
	e.cancel = nil
	return cancel, e.client
}

func (e *kubernetesExecutor) deleteJob(ctx context.Context, client jobClient) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), jobCleanupTimeout)
	defer cancel()

	logger.Info(ctx, "Deleting kubernetes job",
		slog.String("job", client.GetJobName()),
	)
	return client.DeleteJob(cleanupCtx)
}

func newKubernetes(_ context.Context, step ir.Step) (executor.Executor, error) {
	execCfg := step.ExecutorConfig

	if len(execCfg.Config) == 0 {
		return nil, fmt.Errorf("kubernetes step requires with.image")
	}

	cfg, err := LoadConfigFromMap(execCfg.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubernetes config: %w", err)
	}

	return &kubernetesExecutor{
		cfg:    cfg,
		step:   step,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}, nil
}

func validateStep(step ir.Step) error {
	if len(step.ExecutorConfig.Config) == 0 {
		return ir.NewValidationError("with.image", nil, ErrImageRequired)
	}
	if _, err := LoadConfigFromMap(step.ExecutorConfig.Config); err != nil {
		return ir.NewValidationError("with", nil, err)
	}
	return nil
}

// buildCommand constructs the command slice from the step's commands.
func buildCommand(step ir.Step) []string {
	if len(step.Commands) == 0 {
		return nil
	}
	// For k8s, use the first command entry as the container command
	cmd := step.Commands[0]
	if cmd.Command == "" {
		return nil
	}
	return append([]string{cmd.Command}, cmd.Args...)
}

func init() {
	caps := ir.ExecutorCapabilities{
		Command: true,
	}
	executor.RegisterExecutor("kubernetes", newKubernetes, validateStep, caps)
	executor.RegisterExecutor("k8s", newKubernetes, validateStep, caps)
}
