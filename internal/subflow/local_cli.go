// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	dagutools "github.com/dagucloud/dagu/internal/tools"
)

var errNoRunDatabase = errors.New("child workflow status database is not configured")

// LocalCLI executes child workflows through the Dagu CLI subprocess contract.
type LocalCLI struct {
	mu        sync.Mutex
	processes map[string]*cmdutil.ManagedProcess
}

var _ executor.SubWorkflowRunner = (*LocalCLI)(nil)

// NewLocalCLI creates a subprocess-backed local child workflow runner.
func NewLocalCLI() *LocalCLI {
	return &LocalCLI{
		processes: make(map[string]*cmdutil.ManagedProcess),
	}
}

// ShouldRun reports whether req can use the local CLI path.
func (r *LocalCLI) ShouldRun(_ context.Context, req executor.SubWorkflowRequest) bool {
	return validateLocalRequest(req) == nil
}

// Run starts a child workflow through the local CLI and returns its stored result.
func (r *LocalCLI) Run(ctx context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	if err := validateLocalRequest(req); err != nil {
		return nil, err
	}

	workDir := req.WorkDir
	target := req.DAG.Location
	if req.Workspace != nil {
		var cleanup func()
		var err error
		workDir, target, cleanup, err = materializeLocalWorkspace(req)
		if err != nil {
			return nil, err
		}
		defer cleanup()
	}

	cmd, err := r.buildStartCommand(ctx, req, workDir, target)
	if err != nil {
		return nil, err
	}
	return r.runCommand(ctx, req, cmd)
}

// Retry retries a child workflow step through the local CLI.
func (r *LocalCLI) Retry(ctx context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	if err := validateLocalRequest(req.SubWorkflowRequest); err != nil {
		return nil, err
	}
	cmd, err := r.buildRetryCommand(ctx, req)
	if err != nil {
		return nil, err
	}
	return r.runCommand(ctx, req.SubWorkflowRequest, cmd)
}

// Cancel stops a running local CLI child process.
func (r *LocalCLI) Cancel(ctx context.Context, req executor.SubWorkflowCancelRequest) error {
	r.mu.Lock()
	process := r.processes[req.RunID]
	r.mu.Unlock()

	if process != nil {
		_, err := process.Stop(cmdutil.StopRequest{
			Intent: req.Intent,
			Reason: cmdutil.StopReasonCancel,
		})
		return err
	}

	rCtx := exec.GetContext(ctx)
	if rCtx.DB == nil {
		return nil
	}
	if err := rCtx.DB.RequestChildCancel(ctx, req.RunID, req.RootDAGRun); err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func validateLocalRequest(req executor.SubWorkflowRequest) error {
	if req.DAG == nil {
		return errMissingChildDAG
	}
	if req.RunID == "" {
		return errRunIDNotSet
	}
	if req.RootDAGRun.Zero() {
		return errRootRunNotSet
	}
	return nil
}

func (r *LocalCLI) buildStartCommand(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	workDir string,
	target string,
) (*osexec.Cmd, error) {
	args := []string{
		"start",
		fmt.Sprintf("--root=%s", req.RootDAGRun.String()),
		fmt.Sprintf("--parent=%s", req.ParentDAGRun.String()),
		fmt.Sprintf("--run-id=%s", req.RunID),
		"--trigger-type=subdag",
	}
	if workDir != "" {
		args = append(args, fmt.Sprintf("--default-working-dir=%s", workDir))
	}
	if req.Params != "" {
		return r.newCommand(ctx, req, workDir, args, target, "--", req.Params)
	}
	return r.newCommand(ctx, req, workDir, args, target)
}

func (r *LocalCLI) buildRetryCommand(
	ctx context.Context,
	req executor.SubWorkflowRetryRequest,
) (*osexec.Cmd, error) {
	args := []string{
		"retry",
		fmt.Sprintf("--run-id=%s", req.RunID),
		fmt.Sprintf("--root=%s", req.RootDAGRun.String()),
	}
	if req.WorkDir != "" {
		args = append(args, fmt.Sprintf("--default-working-dir=%s", req.WorkDir))
	}
	if req.StepName != "" {
		args = append(args, fmt.Sprintf("--step=%s", req.StepName))
	}
	return r.newCommand(ctx, req.SubWorkflowRequest, req.WorkDir, args, req.DAG.Location)
}

func (r *LocalCLI) newCommand(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	workDir string,
	args []string,
	target string,
	trailingArgs ...string,
) (*osexec.Cmd, error) {
	executable, err := executablePath(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to find executable path: %w", err)
	}

	fullArgs := append([]string{}, args...)
	if configFile := config.ConfigFileUsed(ctx); configFile != "" {
		fullArgs = append(fullArgs, "--config", configFile)
	}
	fullArgs = append(fullArgs, target)
	fullArgs = append(fullArgs, trailingArgs...)

	cmd := osexec.CommandContext(ctx, executable, fullArgs...) // nolint:gosec
	cmd.Dir = workDir

	rCtx := exec.GetContext(ctx)
	cmd.Env = localCLIEnv(rCtx)
	if req.ExternalStepRetry {
		cmd.Env = append(cmd.Env, exec.EnvKeyExternalStepRetry+"=1")
	}

	cmdutil.SetupCommand(cmd)
	return injectTraceContext(ctx, req.DAG, cmd), nil
}

func (r *LocalCLI) runCommand(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	cmd *osexec.Cmd,
) (*exec.RunStatus, error) {
	cmd.Stdout = io.Discard
	stderrTail := executor.NewTailWriter(io.Discard, 4096)
	cmd.Stderr = stderrTail

	logger.Info(ctx, "Executing child workflow through local CLI")

	process, err := cmdutil.StartManagedProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start child workflow: %w", err)
	}
	defer func() { _ = process.Release() }()

	r.mu.Lock()
	r.processes[req.RunID] = process
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.processes, req.RunID)
		r.mu.Unlock()
	}()

	waitErr := process.Wait()
	if waitErr != nil {
		logger.Error(ctx, "Child workflow execution returned error", tag.Error(waitErr))
	}

	if err := ctx.Err(); err != nil {
		return nil, errors.Join(errChildCancelled, err)
	}

	rCtx := exec.GetContext(ctx)
	if rCtx.DB == nil {
		return nil, errNoRunDatabase
	}
	result, resultErr := rCtx.DB.GetSubDAGRunStatus(ctx, req.RunID, req.RootDAGRun)
	if resultErr != nil {
		errMsg := fmt.Sprintf("child workflow run %q failed and wrote no status", req.RunID)
		if waitErr != nil {
			errMsg += fmt.Sprintf(" (process: %v)", waitErr)
		}
		if tail := strings.TrimSpace(stderrTail.Tail()); tail != "" {
			errMsg += fmt.Sprintf("; stderr: %s", tail)
		}
		return nil, fmt.Errorf("%s: %w", errMsg, resultErr)
	}

	if result.Status.IsSuccess() {
		logger.Info(ctx, "Child workflow completed successfully")
		return result, nil
	}
	return result, waitErr
}

func materializeLocalWorkspace(req executor.SubWorkflowRequest) (string, string, func(), error) {
	tmp, err := os.MkdirTemp("", "dagu-action-workspace-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create local action workspace: %w", err)
	}
	cleanup := func() {
		_ = fileutil.RemoveAll(tmp)
	}
	dest := filepath.Join(tmp, "workspace")
	if err := workspacebundle.Extract(req.Workspace.Archive, dest, req.Workspace.Descriptor, workspacebundle.DefaultLimits()); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("materialize action workspace for run %q: %w", req.RunID, err)
	}
	target := filepath.Join(dest, filepath.FromSlash(req.Workspace.Descriptor.DAGPath))
	return dest, target, cleanup, nil
}

func localCLIEnv(rCtx exec.Context) []string {
	env := baseEnvForLocalCLI(rCtx)
	return append(env, inheritedEnvForLocalCLI(rCtx.AllEnvs())...)
}

func baseEnvForLocalCLI(rCtx exec.Context) []string {
	if rCtx.BaseEnv != nil {
		env := rCtx.BaseEnv.AsSlice()
		if len(env) > 0 {
			return env
		}
	}
	return os.Environ()
}

func inheritedEnvForLocalCLI(envs []string) []string {
	if !hasDAGToolsEnv(envs) {
		return envs
	}
	filtered := make([]string, 0, len(envs))
	for _, env := range envs {
		key, _, ok := strings.Cut(env, "=")
		if ok && isDAGToolsEnvKey(key) {
			continue
		}
		filtered = append(filtered, env)
	}
	return filtered
}

func hasDAGToolsEnv(envs []string) bool {
	for _, env := range envs {
		key, _, ok := strings.Cut(env, "=")
		if ok && strings.EqualFold(key, dagutools.EnvManifest) {
			return true
		}
	}
	return false
}

func isDAGToolsEnvKey(key string) bool {
	for _, candidate := range []string{
		"PATH",
		"AQUA_ROOT_DIR",
		"AQUA_CONFIG",
		"AQUA_DISABLE_LAZY_INSTALL",
		"AQUA_CHECKSUM",
		"AQUA_REQUIRE_CHECKSUM",
		"AQUA_ENFORCE_CHECKSUM",
		"AQUA_ENFORCE_REQUIRE_CHECKSUM",
		dagutools.EnvManifest,
	} {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	return false
}

func executablePath(ctx context.Context) (string, error) {
	if cfg := config.GetConfig(ctx); cfg != nil && cfg.Paths.Executable != "" {
		return cfg.Paths.Executable, nil
	}
	if path := os.Getenv("DAGU_EXECUTABLE"); path != "" {
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return executable, nil
}

func injectTraceContext(ctx context.Context, dag *core.DAG, cmd *osexec.Cmd) *osexec.Cmd {
	logCtx := ctx
	if dag != nil {
		logCtx = logger.WithValues(ctx, tag.DAG(dag.Name))
	}
	traceEnvVars := telemetry.InjectTraceContext(ctx)
	if len(traceEnvVars) > 0 {
		cmd.Env = append(cmd.Env, traceEnvVars...)
		logger.Debug(logCtx, "Injecting trace context into child workflow",
			slog.Any("trace-env-vars", traceEnvVars),
		)
	} else {
		logger.Debug(logCtx, "No trace context to inject into child workflow")
	}
	return cmd
}
