// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/signal"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

var (
	ErrExecutorConfigRequired = errors.New("docker step configuration is required")
)

// Docker executor runs a command in a Docker container.
/* Example DAG:
```yaml
steps:
 - name: exec-in-existing
   type: docker
   with:
     container_name: <container-name>
     auto_remove: true
     exec:
       user: root     # optional
       working_dir: /  # optional
       env:           # optional
         - MY_VAR=value
   command: echo "Hello from existing container"

 - name: create-new
   type: docker
   with:
     image: alpine:latest
     auto_remove: true
   command: echo "Hello from new container"
```
*/

var _ executor.Executor = (*docker)(nil)
var _ executor.ExitCoder = (*docker)(nil)

type containerClientCtxKey struct{}
type registryAuthCtxKey struct{}

// WithContainerClient creates a new context with a client for container
func WithContainerClient(ctx context.Context, cli *Client) context.Context {
	return context.WithValue(ctx, containerClientCtxKey{}, cli)
}

// GetContainerClient retrieves the container client from the context.
func GetContainerClient(ctx context.Context) *Client {
	if cli, ok := ctx.Value(containerClientCtxKey{}).(*Client); ok {
		return cli
	}
	return nil
}

// WithRegistryAuth creates a new context with registry authentication.
func WithRegistryAuth(ctx context.Context, auths map[string]*core.AuthConfig) context.Context {
	return context.WithValue(ctx, registryAuthCtxKey{}, auths)
}

// getRegistryAuth retrieves the registry authentication from the context.
func getRegistryAuth(ctx context.Context) map[string]*core.AuthConfig {
	if auths, ok := ctx.Value(registryAuthCtxKey{}).(map[string]*core.AuthConfig); ok {
		return auths
	}
	return nil
}

type docker struct {
	step      core.Step
	stdout    io.Writer
	stderr    io.Writer
	context   context.Context
	cancel    func()
	cfg       *Config
	container *Client
	mu        sync.Mutex
	exitCode  int
}

func (e *docker) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *docker) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *docker) Kill(sig os.Signal) error {
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	if e.container == nil {
		return nil
	}

	if sig == syscall.SIGKILL {
		return e.container.Stop(sig)
	}
	if sig == syscall.SIGTERM && e.step.SignalOnStop != "" {
		sig = syscall.Signal(signal.GetSignalNum(e.step.SignalOnStop))
	}

	// Wait for max clean up time before forcefully killing the container
	go func() {
		env := runtime.GetEnv(e.context)
		<-time.After(env.DAG.MaxCleanUpTime)
		logger.Warn(e.context, "Forcefully stopping container after max clean up time",
			slog.String("container", e.step.Name),
		)
		_ = e.container.Stop(syscall.SIGKILL)
	}()

	return e.container.Stop(sig)
}

func (e *docker) Run(ctx context.Context) error {
	logger.Debug(ctx, "Docker executor: Run started",
		slog.String("stepName", e.step.Name),
		slog.Int("numCommands", len(e.step.Commands)),
	)

	ctx, cancelFunc := context.WithCancel(ctx)
	e.context = ctx
	e.cancel = cancelFunc

	defer cancelFunc()

	// Wrap stderr with a tail writer to capture recent output for inclusion in
	// error messages. Use encoding from DAGContext to properly decode non-UTF-8 output.
	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(e.stderr, 0, env.LogEncodingCharset)
	e.stderr = tw

	// Only use DAG-level container client if this step does NOT have its own container config.
	// When a step has its own container configuration (e.cfg != nil), it should run in its own
	// container instead of the DAG-level shared container.
	cli := GetContainerClient(ctx)
	if cli != nil && e.cfg == nil {
		logger.Debug(ctx, "Docker executor: using existing container client from context")
		return e.runInExistingContainer(ctx, cli, tw)
	}

	if e.cfg == nil {
		logger.Error(ctx, "Docker executor: config is nil")
		return ErrExecutorConfigRequired
	}

	logger.Debug(ctx, "Docker executor: initializing new container client",
		slog.String("image", e.cfg.Image),
		slog.String("containerName", e.cfg.ContainerName),
	)
	cli, err := InitializeClient(ctx, e.cfg)
	if err != nil {
		logger.Error(ctx, "Docker executor: failed to initialize client", slog.Any("error", err))
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("failed to setup container: %w\nrecent stderr (tail):\n%s", err, tail)
		}
		return fmt.Errorf("failed to setup container: %w", err)
	}
	logger.Debug(ctx, "Docker executor: container client initialized")

	e.container = cli
	defer e.container.Close(ctx)

	return e.runInNewContainer(ctx, tw)
}

// runInExistingContainer executes commands in an existing container from context.
func (e *docker) runInExistingContainer(ctx context.Context, cli *Client, tw *executor.TailWriter) error {
	execOpts := ExecOptions{}

	// If no commands, run with empty command (use image default)
	if len(e.step.Commands) == 0 {
		exitCode, err := cli.Exec(ctx, nil, e.stdout, e.stderr, execOpts)
		e.setExitCode(exitCode)
		if err != nil && tw.Tail() != "" {
			return fmt.Errorf("%w\nrecent stderr (tail):\n%s", err, tw.Tail())
		}
		return err
	}

	// Execute each command sequentially
	for i, cmdEntry := range e.step.Commands {
		cmd := e.buildCommand(cmdEntry)

		logger.Debug(ctx, "Docker executor: executing command in existing container",
			slog.Int("commandIndex", i+1),
			slog.Int("totalCommands", len(e.step.Commands)),
			slog.Any("cmd", cmd),
		)

		exitCode, err := cli.Exec(ctx, cmd, e.stdout, e.stderr, execOpts)
		e.setExitCode(exitCode)

		if err != nil {
			if tail := tw.Tail(); tail != "" {
				return fmt.Errorf("command %d failed: %w\nrecent stderr (tail):\n%s", i+1, err, tail)
			}
			return fmt.Errorf("command %d failed: %w", i+1, err)
		}

		// Check context cancellation between commands
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	return nil
}

// runInNewContainer executes commands in a newly created container.
func (e *docker) runInNewContainer(ctx context.Context, tw *executor.TailWriter) error {
	// If no step-level commands, use container.command (StartCmd) if specified
	if len(e.step.Commands) == 0 {
		exitCode, err := e.container.Run(ctx, e.cfg.StartCmd, e.stdout, e.stderr)
		e.setExitCode(exitCode)
		if err != nil {
			logger.Error(ctx, "Docker executor: Run completed with error", slog.Any("error", err))
			if tail := tw.Tail(); tail != "" {
				return fmt.Errorf("%w\nrecent stderr (tail):\n%s", err, tail)
			}
		}
		return err
	}

	// For single command, use the simple Run approach
	if len(e.step.Commands) == 1 {
		cmd := e.buildCommand(e.step.Commands[0])
		cmd = wrapCommandWithShell(e.cfg.Shell, cmd)

		logger.Debug(ctx, "Docker executor: calling container.Run for single command",
			slog.Any("cmd", cmd),
		)

		exitCode, err := e.container.Run(ctx, cmd, e.stdout, e.stderr)
		e.setExitCode(exitCode)

		if err != nil {
			logger.Error(ctx, "Docker executor: command failed", slog.Any("error", err))
			if tail := tw.Tail(); tail != "" {
				return fmt.Errorf("%w\nrecent stderr (tail):\n%s", err, tail)
			}
		}
		return err
	}

	// For multiple commands, start container in background and exec all commands
	logger.Debug(ctx, "Docker executor: starting container in background for multiple commands",
		slog.Int("numCommands", len(e.step.Commands)),
	)

	// Start container in background - this will use startup:command mode if configured,
	// otherwise the default keepalive. The container stays running while we exec commands.
	if err := e.container.StartBackground(ctx); err != nil {
		logger.Error(ctx, "Docker executor: failed to start container in background", slog.Any("error", err))
		if tail := tw.Tail(); tail != "" {
			return fmt.Errorf("failed to start container: %w\nrecent stderr (tail):\n%s", err, tail)
		}
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Execute all commands via Exec
	for i, cmdEntry := range e.step.Commands {
		// Check context cancellation between commands
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		cmd := e.buildCommandRaw(cmdEntry)

		logger.Debug(ctx, "Docker executor: executing command",
			slog.Int("commandIndex", i+1),
			slog.Int("totalCommands", len(e.step.Commands)),
			slog.Any("cmd", cmd),
		)

		exitCode, err := e.container.Exec(ctx, cmd, e.stdout, e.stderr, ExecOptions{})
		e.setExitCode(exitCode)

		if err != nil {
			logger.Error(ctx, "Docker executor: command failed",
				slog.Int("commandIndex", i+1),
				slog.Any("error", err),
			)
			if tail := tw.Tail(); tail != "" {
				return fmt.Errorf("command %d failed: %w\nrecent stderr (tail):\n%s", i+1, err, tail)
			}
			return fmt.Errorf("command %d failed: %w", i+1, err)
		}
	}

	logger.Debug(ctx, "Docker executor: all commands completed successfully")
	return nil
}

// ExitCode implements ExitCoder.
func (e *docker) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

// setExitCode safely sets the exit code.
func (e *docker) setExitCode(code int) {
	e.mu.Lock()
	e.exitCode = code
	e.mu.Unlock()
}

// buildCommand builds a command slice from a CommandEntry, applying shell wrapping if configured.
// This method is used when executing in an existing container where shell wrapping is needed.
func (e *docker) buildCommand(cmdEntry core.CommandEntry) []string {
	// For shell wrapping, use CmdWithArgs (original string) instead of reconstructed array
	// This preserves quoting and matches command executor behavior
	if e.cfg != nil && len(e.cfg.Shell) > 0 && cmdEntry.CmdWithArgs != "" {
		return []string{cmdEntry.CmdWithArgs}
	}
	return e.buildCommandRaw(cmdEntry)
}

// buildCommandRaw builds a command slice from a CommandEntry without shell consideration.
func (e *docker) buildCommandRaw(cmdEntry core.CommandEntry) []string {
	if cmdEntry.Command == "" {
		return nil
	}
	return append([]string{cmdEntry.Command}, cmdEntry.Args...)
}

func newDocker(ctx context.Context, step core.Step) (executor.Executor, error) {
	execCfg := step.ExecutorConfig
	registryAuths := getRegistryAuth(ctx)

	var cfg *Config

	// Priority 1: Step-level container field (new intuitive syntax)
	// This is the preferred way to configure containers at step level
	if step.Container != nil {
		// Merge step env into container env BEFORE evaluation so that
		// all variable references (including DAG env/params in step env)
		// are resolved together with the full runtime scope.
		ct := *step.Container
		ct.Env = mergeEnvVars(step.Env, ct.Env)

		// Expand environment variables in container fields at execution time
		env := runtime.GetEnv(ctx)
		expanded, err := EvalContainerFields(ctx, ct)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate container config: %w", err)
		}
		c, err := LoadConfig(env.WorkingDir, expanded, registryAuths)
		if err != nil {
			return nil, fmt.Errorf("failed to load step container config: %w", err)
		}
		// Set ShouldStart to true for step-level containers
		// This ensures the container is automatically created and started
		c.ShouldStart = true
		cfg = c
	} else if len(execCfg.Config) > 0 {
		// Priority 2: Executor config map (legacy syntax: executor.config)
		c, err := LoadConfigFromMap(execCfg.Config, registryAuths)
		if err != nil {
			return nil, fmt.Errorf("failed to load container config: %w", err)
		}
		// Set ShouldStart to true for Step-level containers
		// This ensures the container is automatically created and started
		// if it does not exist or is stopped.
		c.ShouldStart = true
		cfg = c
	}

	if cfg != nil {
		env := runtime.GetEnv(ctx)
		if env.DAG != nil && env.DAG.Resources.HasLimits() &&
			!ApplyResourceLimitsToConfig(cfg, env.DAG.Resources.Limits) {
			logger.Warn(ctx, "Resource limits requested but cannot be applied to an existing container")
		}
	}

	return &docker{
		cfg:    cfg,
		step:   step,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}, nil
}

// mergeEnvVars merges two env var slices, with later values taking precedence.
// Both slices use "KEY=VALUE" format. If the same key appears in both,
// the value from the second slice (higher priority) is used.
func mergeEnvVars(base, override []string) []string {
	if len(base) == 0 {
		return override
	}
	if len(override) == 0 {
		return base
	}

	// Build a map of key -> value from base
	envMap := make(map[string]string)
	for _, env := range base {
		if idx := strings.Index(env, "="); idx > 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	// Override with values from the second slice
	for _, env := range override {
		if idx := strings.Index(env, "="); idx > 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	// Convert back to slice
	result := make([]string, 0, len(envMap))
	for key, value := range envMap {
		result = append(result, key+"="+value)
	}

	return result
}

// EvalContainerFields evaluates environment variables in container fields at runtime.
// Only fields that commonly use variables are evaluated:
// - Exec, Image, Name, User, WorkingDir, Network (string fields)
// - Volumes, Ports, Env, Command, Shell (slice fields)
// Fields like PullPolicy, Startup, WaitFor, KeepContainer are NOT evaluated
// as they have specific enum/boolean values.
func EvalContainerFields(ctx context.Context, ct core.Container) (core.Container, error) {
	var err error

	// Evaluate exec field (for exec-into-existing-container mode)
	if ct.Exec, err = runtime.EvalStepString(ctx, ct.Exec); err != nil {
		return ct, fmt.Errorf("failed to evaluate exec: %w", err)
	}

	// Evaluate string fields
	if ct.Image, err = runtime.EvalStepString(ctx, ct.Image); err != nil {
		return ct, fmt.Errorf("failed to evaluate image: %w", err)
	}
	if ct.Name, err = runtime.EvalStepString(ctx, ct.Name); err != nil {
		return ct, fmt.Errorf("failed to evaluate name: %w", err)
	}
	if ct.User, err = runtime.EvalStepString(ctx, ct.User); err != nil {
		return ct, fmt.Errorf("failed to evaluate user: %w", err)
	}
	if ct.WorkingDir, err = runtime.EvalStepString(ctx, ct.WorkingDir); err != nil {
		return ct, fmt.Errorf("failed to evaluate workingDir: %w", err)
	}
	if ct.Network, err = runtime.EvalStepString(ctx, ct.Network); err != nil {
		return ct, fmt.Errorf("failed to evaluate network: %w", err)
	}

	// Evaluate slice fields
	if ct.Volumes, err = evalStringSlice(ctx, ct.Volumes); err != nil {
		return ct, fmt.Errorf("failed to evaluate volumes: %w", err)
	}
	if ct.Ports, err = evalStringSlice(ctx, ct.Ports); err != nil {
		return ct, fmt.Errorf("failed to evaluate ports: %w", err)
	}
	if ct.Env, err = evalEnvSequentially(ctx, ct.Env); err != nil {
		return ct, fmt.Errorf("failed to evaluate env: %w", err)
	}
	if ct.Command, err = evalStringSlice(ctx, ct.Command); err != nil {
		return ct, fmt.Errorf("failed to evaluate command: %w", err)
	}
	if ct.Shell, err = evalStringSlice(ctx, ct.Shell); err != nil {
		return ct, fmt.Errorf("failed to evaluate shell: %w", err)
	}

	return ct, nil
}

// evalStringSlice evaluates each string in a slice as a step-owned field.
func evalStringSlice(ctx context.Context, ss []string) ([]string, error) {
	if len(ss) == 0 {
		return ss, nil
	}
	result := make([]string, len(ss))
	for i, s := range ss {
		evaluated, err := runtime.EvalStepString(ctx, s)
		if err != nil {
			return nil, err
		}
		result[i] = evaluated
	}
	return result, nil
}

// evalEnvSequentially evaluates "KEY=VALUE" env entries in order,
// accumulating resolved keys so that later entries can reference earlier
// ones (e.g., B=${A} where A is defined earlier in the same list).
func evalEnvSequentially(ctx context.Context, envs []string) ([]string, error) {
	if len(envs) == 0 {
		return envs, nil
	}
	resolved := make(map[string]string, len(envs))
	result := make([]string, 0, len(envs))
	for _, entry := range envs {
		key, rawVal, found := strings.Cut(entry, "=")
		if !found {
			result = append(result, entry)
			continue
		}
		val, err := runtime.EvalString(ctx, rawVal, eval.WithVariables(resolved))
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", key, err)
		}
		resolved[key] = val
		result = append(result, key+"="+val)
	}
	return result, nil
}

func init() {
	caps := core.ExecutorCapabilities{
		Command:          true,
		MultipleCommands: true,
		Container:        true,
		GetCommandEvalOptions: func(ctx context.Context, step core.Step) []eval.Option {
			if hasShellConfigured(ctx, step) {
				return []eval.Option{eval.WithoutDollarEscape()}
			}
			return nil
		},
		// Env vars are expanded on host before passing to container (default behavior)
	}
	executor.RegisterExecutor("docker", newDocker, nil, caps)
	executor.RegisterExecutor("container", newDocker, nil, caps)
}

func hasShellConfigured(ctx context.Context, step core.Step) bool {
	if step.Container != nil {
		return cmdutil.HasShellArgs(step.Container.Shell)
	}
	if len(step.ExecutorConfig.Config) > 0 {
		return cmdutil.IsShellValueSet(step.ExecutorConfig.Config["shell"])
	}

	env := runtime.GetEnv(ctx)
	if env.DAG != nil && env.DAG.Container != nil {
		return cmdutil.HasShellArgs(env.DAG.Container.Shell)
	}

	return false
}
